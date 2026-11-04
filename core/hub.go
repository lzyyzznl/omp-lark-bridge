package core

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── hub: 多实例中心同步 ──────────────────────────────────────────────
// server 模式：聚合各客户端注册的 agents / sessions 目录，并把 run / agent-edit
// 请求投递到对应设备的 outbox，由客户端下一次 keepalive 取走执行并回传结果。
// client 模式：周期上报本机 inventory，轮询 outbox 里的转发 op，用本地引擎执行
// 后回传结果。客户端全程主动出站，无需入站端口，可跑在 NAT/Cloudflare 之后。

// HubDevice 是一台已在 hub 注册的 olb 设备（server 侧视图 / 客户端互查目录）。
type HubDevice struct {
	Token    string          `json:"token"`
	Name     string          `json:"name"`
	Address  string          `json:"address,omitempty"`
	LastSeen time.Time       `json:"last_seen"`
	Agents   []*OmpAgentInfo `json:"agents"`
	Sessions []HubSession    `json:"sessions"`
}

// HubSession 是客户端上报的会话元数据（不含对话内容，只读互查用）。
type HubSession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Size      int64     `json:"size"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HubOp 是 server 投递给某客户端的转发操作（outbox 队列，按注册顺序拉取）。
type HubOp struct {
	ID      string        `json:"id"`
	Type    string        `json:"type"` // "run" | "agent_edit"
	Agent   string        `json:"agent,omitempty"`
	Message string        `json:"message,omitempty"`
	Edit    *HubAgentEdit `json:"edit,omitempty"`
}

// HubAgentEdit 是跨设备编辑（服务端请求客户端本地修改，字段与本地 API 同构）。
type HubAgentEdit struct {
	Action string                `json:"action"` // "copy" | "update" | "delete"
	Source string                `json:"source,omitempty"`
	Name   string                `json:"name"`
	Fields *OmpAgentWriteRequest `json:"fields,omitempty"`
}

// HubOpResult 是客户端执行完转发 op 后的回执。
type HubOpResult struct {
	OpID    string `json:"op_id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// HubClientOptions 配置 client 模式的同步目标。
type HubClientOptions struct {
	ServerURL     string
	DeviceToken   string
	DeviceName    string
	SyncIntervalM int // 0 → 1 分钟
}

// hubData 挂在 ManagementServer 上；nil 表示 hub 未启用。
type hubData struct {
	mu         sync.Mutex
	secret     string
	deviceName string

	devices map[string]*HubDevice // server 模式：device_token → 设备
	outbox  map[string][]HubOp    // server 模式：device_token → 待投递 op
	results map[string]HubOpResult

	serverURL   string // client 模式
	clientToken string
	syncEvery   time.Duration
	remote      []HubDevice // client 模式：最近一次拉到的设备目录
	stop        chan struct{}
}

// EnableHub 启用 hub。serverMode=true 时注册 /hub/* 端点（必须在 Start 前调用）；
// 否则启动 client 同步循环，周期性上报本机 inventory 并执行 server 转发的 op。
func (m *ManagementServer) EnableHub(secret string, serverMode bool, opts HubClientOptions) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("hub: secret is required")
	}
	h := &hubData{
		secret:  secret,
		devices: map[string]*HubDevice{},
		outbox:  map[string][]HubOp{},
		results: map[string]HubOpResult{},
		stop:    make(chan struct{}),
	}
	if !serverMode {
		opts.ServerURL = strings.TrimRight(opts.ServerURL, "/")
		if opts.ServerURL == "" {
			return errors.New("hub: server_url is required in client mode")
		}
		if opts.DeviceToken == "" {
			return errors.New("hub: device_token is required in client mode")
		}
		h.serverURL = opts.ServerURL
		h.clientToken = opts.DeviceToken
		h.deviceName = opts.DeviceName
		if h.deviceName == "" {
			if host, err := os.Hostname(); err == nil {
				h.deviceName = host
			} else {
				h.deviceName = opts.DeviceToken
			}
		}
		h.syncEvery = time.Duration(opts.SyncIntervalM) * time.Minute
		if opts.SyncIntervalM <= 0 {
			h.syncEvery = time.Minute
		}
		m.hub = h
		m.startHubClientLoop()
		return nil
	}
	m.hub = h
	slog.Info("hub: server mode enabled")
	return nil
}

// hubEnabled reports whether hub data is configured (either mode).
func (m *ManagementServer) hubEnabled() bool { return m.hub != nil }

// authHub 校验 hub 共享密钥（不同设备的 X-Hub-Secret，常量时间比较）。
func (m *ManagementServer) authHub(r *http.Request) bool {
	h := m.hub
	if h == nil {
		return false
	}
	h.mu.Lock()
	secret := h.secret
	h.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Hub-Secret")), []byte(secret)) == 1
}

// ── server 模式：/hub/* 端点 ─────────────────────────────────────────

// handleHubRegister 接收客户端 inventory 上报（含 poll 拉取 outbox）：
// POST /api/v1/hub/register {device_token,name,agents,sessions,poll}
// 响应 {devices:[...], ops:[...]} —— devices 是全部在线设备目录（客户端互查）。
func (m *ManagementServer) handleHubRegister(w http.ResponseWriter, r *http.Request) {
	if !m.authHub(r) {
		mgmtError(w, http.StatusUnauthorized, "invalid hub secret")
		return
	}
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var body struct {
		Token    string          `json:"device_token"`
		Name     string          `json:"name"`
		Agents   []*OmpAgentInfo `json:"agents"`
		Sessions []HubSession    `json:"sessions"`
		Poll     bool            `json:"poll"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Token == "" {
		mgmtError(w, http.StatusBadRequest, "device_token is required")
		return
	}
	h := m.hub
	h.mu.Lock()
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = body.Token
	}
	dev := &HubDevice{
		Token:    body.Token,
		Name:     name,
		Address:  r.RemoteAddr,
		LastSeen: time.Now(),
		Agents:   body.Agents,
		Sessions: body.Sessions,
	}
	h.devices[body.Token] = dev
	var ops []HubOp
	if body.Poll {
		ops = h.outbox[body.Token]
		delete(h.outbox, body.Token)
	}
	devices := m.hubDevicesLocked()
	for i := range devices {
		if devices[i].Token == body.Token {
			devices[i] = *dev
		}
	}
	h.mu.Unlock()
	mgmtJSON(w, http.StatusOK, map[string]any{"devices": devices, "ops": ops})
}

// hubDevicesLocked 返回按名称排序的设备目录副本（调用方须已持有 h.mu）。
func (m *ManagementServer) hubDevicesLocked() []HubDevice {
	h := m.hub
	out := make([]HubDevice, 0, len(h.devices))
	for _, d := range h.devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// handleHubDevices 返回 server 本地聚合的设备目录（供 server 侧 web/API 查看）。
// GET /api/v1/hub/devices
func (m *ManagementServer) handleHubDevices(w http.ResponseWriter, r *http.Request) {
	if !m.authHub(r) {
		mgmtError(w, http.StatusUnauthorized, "invalid hub secret")
		return
	}
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	m.hub.mu.Lock()
	devices := m.hubDevicesLocked()
	m.hub.mu.Unlock()
	mgmtJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

var errHubDeviceNotFound = errors.New("hub: device not registered")

// queueDeviceOp 把转发 op 投递到指定设备的 outbox（server 模式本地）。op.ID 为空时
// 自动生成；设备未注册则报错。
func (m *ManagementServer) queueDeviceOp(deviceToken string, op HubOp) (string, error) {
	m.hub.mu.Lock()
	defer m.hub.mu.Unlock()
	if _, ok := m.hub.devices[deviceToken]; !ok {
		return "", errHubDeviceNotFound
	}
	if op.ID == "" {
		op.ID = fmt.Sprintf("op-%d", time.Now().UnixNano())
	}
	m.hub.outbox[deviceToken] = append(m.hub.outbox[deviceToken], op)
	return op.ID, nil
}

// handleHubDeviceRoutes 处理 /hub/{device}/run 与 /hub/{device}/agent-edit：
// 把转发请求投递到该设备的 outbox。POST /api/v1/hub/{device}/run {agent,message}
func (m *ManagementServer) handleHubDeviceRoutes(w http.ResponseWriter, r *http.Request) {
	if !m.authHub(r) {
		mgmtError(w, http.StatusUnauthorized, "invalid hub secret")
		return
	}
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/hub/"), "/")
	device, action, has := strings.Cut(rest, "/")
	if !has || device == "" || (action != "run" && action != "agent-edit") {
		mgmtError(w, http.StatusBadRequest, "expected /hub/{device}/run|agent-edit")
		return
	}
	jsonBody := func(target any) bool {
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(target); err != nil {
			mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return false
		}
		return true
	}
	op := HubOp{ID: fmt.Sprintf("op-%d", time.Now().UnixNano()), Type: "run"}
	if action == "agent-edit" {
		op.Type = "agent_edit"
		op.Edit = &HubAgentEdit{}
		if !jsonBody(op.Edit) {
			return
		}
	} else {
		var body struct {
			Agent   string `json:"agent"`
			Message string `json:"message"`
		}
		if !jsonBody(&body) {
			return
		}
		if strings.TrimSpace(body.Message) == "" {
			mgmtError(w, http.StatusBadRequest, "message is required")
			return
		}
		op.Agent = strings.TrimSpace(body.Agent)
		op.Message = body.Message
	}
	queued, err := m.queueDeviceOp(device, op)
	if err != nil {
		mgmtError(w, http.StatusNotFound, err.Error())
		return
	}
	mgmtJSON(w, http.StatusOK, map[string]any{"queued": queued})
}

// handleHubResult 接收客户端执行完转发 op 的回执。
// POST /api/v1/hub/result {op_id, ok, message, error}
func (m *ManagementServer) handleHubResult(w http.ResponseWriter, r *http.Request) {
	if !m.authHub(r) {
		mgmtError(w, http.StatusUnauthorized, "invalid hub secret")
		return
	}
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var res HubOpResult
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&res); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if res.OpID == "" {
		mgmtError(w, http.StatusBadRequest, "op_id is required")
		return
	}
	m.hub.mu.Lock()
	if len(m.hub.results) > 200 { // 防无限增长
		m.hub.results = map[string]HubOpResult{}
	}
	m.hub.results[res.OpID] = res
	m.hub.mu.Unlock()
	mgmtJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ── client 模式：同步循环 ───────────────────────────────────────────

// startHubClientLoop 启动周期同步：上报 inventory + 拉取并执行转发 op。
func (m *ManagementServer) startHubClientLoop() {
	go func() {
		ticker := time.NewTicker(m.hub.syncEvery)
		defer ticker.Stop()
		m.hubSyncOnce()
		for {
			select {
			case <-ticker.C:
				m.hubSyncOnce()
			case <-m.hub.stop:
				return
			}
		}
	}()
	slog.Info("hub: client mode enabled", "server_url", m.hub.serverURL)
}

func (m *ManagementServer) hubSyncOnce() {
	h := m.hub
	h.mu.Lock()
	serverURL, token, name := h.serverURL, h.clientToken, h.deviceName
	h.mu.Unlock()
	agents, sessions := m.collectHubInventory()
	body, _ := json.Marshal(map[string]any{
		"device_token": token,
		"name":         name,
		"agents":       agents,
		"sessions":     sessions,
		"poll":         true,
	})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/hub/register", strings.NewReader(string(body)))
	if err != nil {
		slog.Warn("hub: register request failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	h.mu.Lock()
	req.Header.Set("X-Hub-Secret", h.secret)
	h.mu.Unlock()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("hub: sync failed", "server", serverURL, "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		slog.Warn("hub: sync rejected", "status", resp.StatusCode, "body", string(b))
		return
	}
	var out struct {
		Devices []HubDevice `json:"devices"`
		Ops     []HubOp     `json:"ops"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		slog.Warn("hub: sync decode failed", "error", err)
		return
	}
	h.mu.Lock()
	h.remote = out.Devices
	h.mu.Unlock()
	for _, op := range out.Ops {
		res := m.executeHubOp(op)
		m.reportHubResult(serverURL, res)
	}
}

// collectHubInventory 汇总本机 agents + 各引擎的会话元数据。
func (m *ManagementServer) collectHubInventory() ([]*OmpAgentInfo, []HubSession) {
	agents := DiscoverOmpAgents(m.projectWorkdirs())
	if agents == nil {
		agents = []*OmpAgentInfo{}
	}
	sessions := []HubSession{}
	m.mu.RLock()
	for _, e := range m.engines {
		dirProv, ok := e.agent.(SessionDirProvider)
		if !ok || dirProv.SessionDir() == "" {
			continue
		}
		entries, err := os.ReadDir(dirProv.SessionDir())
		if err != nil {
			continue
		}
		for _, en := range entries {
			if en.IsDir() || !strings.HasSuffix(en.Name(), ".jsonl") {
				continue
			}
			info, err := en.Info()
			if err != nil {
				continue
			}
			sessions = append(sessions, HubSession{
				ID:        strings.TrimSuffix(en.Name(), ".jsonl"),
				Size:      info.Size(),
				UpdatedAt: info.ModTime(),
			})
		}
	}
	m.mu.RUnlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt) })
	return agents, sessions
}

// executeHubOp 在本地执行一个转发 op（run → 委托专家保持独立会话；agent_edit →
// 本地全字段 CRUD），并在回传结果中携带浏览器可读的摘要。
func (m *ManagementServer) executeHubOp(op HubOp) HubOpResult {
	if op.ID == "" {
		return HubOpResult{op.ID, false, "", "missing op_id"}
	}
	switch op.Type {
	case "run":
		text, err := m.runTurnCollectize(op.Agent, op.Message)
		if err != nil {
			return HubOpResult{op.ID, false, "", err.Error()}
		}
		return HubOpResult{op.ID, true, text, ""}
	case "agent_edit":
		if op.Edit == nil {
			return HubOpResult{op.ID, false, "", "missing edit payload"}
		}
		msg, err := m.applyHubEdit(op.Edit)
		if err != nil {
			return HubOpResult{op.ID, false, "", err.Error()}
		}
		return HubOpResult{op.ID, true, msg, ""}
	default:
		return HubOpResult{op.ID, false, "", "unknown op type: " + op.Type}
	}
}

// applyHubEdit 在本地执行跨设备 agent 编辑（copy / update / delete，user scope）。
func (m *ManagementServer) applyHubEdit(edit *HubAgentEdit) (string, error) {
	switch edit.Action {
	case "copy":
		if _, err := CopyUserAgent(m.projectWorkdirs(), strings.TrimSpace(edit.Source), strings.TrimSpace(edit.Name)); err != nil {
			return "", err
		}
		return "copied " + edit.Name, nil
	case "delete":
		if err := DeleteUserAgent(strings.TrimSpace(edit.Name)); err != nil {
			return "", err
		}
		return "deleted " + edit.Name, nil
	case "update":
		if edit.Fields == nil {
			return "", errors.New("fields is required for update")
		}
		if _, err := UpdateUserAgent(edit.Fields.fields(strings.TrimSpace(edit.Name))); err != nil {
			return "", err
		}
		return "updated " + edit.Name, nil
	default:
		return "", fmt.Errorf("unknown edit action %q", edit.Action)
	}
}

// reportHubResult 把 op 回执 POST 给 hub server。
func (m *ManagementServer) reportHubResult(serverURL string, res HubOpResult) {
	body, _ := json.Marshal(res)
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/hub/result", strings.NewReader(string(body)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	m.hub.mu.Lock()
	req.Header.Set("X-Hub-Secret", m.hub.secret)
	m.hub.mu.Unlock()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("hub: result upload failed", "op", res.OpID, "error", err)
		return
	}
	resp.Body.Close()
}

// runTurnCollectize 在默认引擎上以指定专家创建一个全新的独立会话并执行一条
// 消息，收集最终文本结果（hub 转发专用；不是 SSE 流式）。首条 prompt 注入
// 专家委托（delegationTemplate），结束后清理一次性 override 防止泄漏。
func (m *ManagementServer) runTurnCollectize(agentName, msg string) (string, error) {
	e := m.firstEngine()
	if e == nil || e.agent == nil {
		return "", errors.New("no usable engine")
	}
	if applier, ok := e.agent.(CronAgentApplier); !ok {
		return "", errors.New("agent does not support expert delegation")
	} else if agentName != "" {
		applier.ApplyCronAgent(agentName)
		defer applier.ApplyCronAgent("")
	}
	dirProv, ok := e.agent.(SessionDirProvider)
	if !ok || dirProv.SessionDir() == "" {
		return "", errors.New("agent does not expose session transcripts")
	}
	m.chatMu.Lock()
	defer m.chatMu.Unlock()
	// 与 streamTranscriptTurn 同理：以不存在的占位文件启动 → omp 回退全新会话。
	id := "hub-" + fmt.Sprintf("%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	sess, err := e.agent.StartSession(ctx, filepath.Join(dirProv.SessionDir(), id+".jsonl"))
	if err != nil {
		return "", err
	}
	defer sess.Close()
	if err := sess.Send(msg, "hub"+id, nil, nil); err != nil {
		return "", err
	}
	var out strings.Builder
	for ev := range sess.Events() {
		switch ev.Type {
		case EventText:
			out.WriteString(ev.Content)
		case EventError:
			if ev.Error != nil {
				return out.String(), ev.Error
			}
			return out.String(), errors.New("session error")
		case EventResult:
			if ev.Done {
				return out.String(), nil
			}
		}
	}
	return out.String(), errors.New("session ended unexpectedly")
}

// handleHubOverview 是 web 看板用的 hub 全网视图（受 management token 保护，
// 由 m.wrap 挂载）：server 模式返回聚合的注册设备目录；client 模式返回最近一次
// 从 server 同步到的远端设备目录（只读）。GET /api/v1/hub/overview
func (m *ManagementServer) handleHubOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	h := m.hub
	h.mu.Lock()
	var devices []HubDevice
	if h.serverURL != "" {
		devices = append(devices, h.remote...)
	} else {
		devices = m.hubDevicesLocked()
	}
	h.mu.Unlock()
	mgmtJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

// handleHubOverviewRoutes 处理 web 的转发与结果查询（受 management token 保护）：
//   - POST /api/v1/hub/overview/{device}/run {agent,message} → 投递到设备 outbox（返回 queued id）
//   - GET  /api/v1/hub/overview/result/{op} → {pending:true} 或 {result:{...}}
//
// client 模式下远端不落本地 outbox，这里只支持查询，不做转发。
func (m *ManagementServer) handleHubOverviewRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/hub/overview/"), "/")
	if strings.HasPrefix(rest, "result/") {
		if r.Method != http.MethodGet {
			mgmtError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		opID := strings.TrimPrefix(rest, "result/")
		h := m.hub
		h.mu.Lock()
		res, ok := h.results[opID]
		h.mu.Unlock()
		if !ok {
			mgmtJSON(w, http.StatusOK, map[string]any{"pending": true, "op": opID})
			return
		}
		mgmtJSON(w, http.StatusOK, map[string]any{"result": res})
		return
	}
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	device, action, has := strings.Cut(rest, "/")
	if !has || device == "" || action != "run" {
		mgmtError(w, http.StatusBadRequest, "expected /hub/overview/{device}/run")
		return
	}
	var body struct {
		Agent   string `json:"agent"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Message) == "" {
		mgmtError(w, http.StatusBadRequest, "message is required")
		return
	}
	queued, err := m.queueDeviceOp(device, HubOp{
		Type: "run", Agent: strings.TrimSpace(body.Agent), Message: body.Message,
	})
	if err != nil {
		mgmtError(w, http.StatusNotFound, err.Error())
		return
	}
	mgmtJSON(w, http.StatusOK, map[string]any{"queued": queued})
}
