package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hubTestServer 构建一个挂着 hub server 端点的测试 mux（与 management Start
// 一致：hub 端点不走 management token，只用 X-Hub-Secret）。
func hubTestServer(t *testing.T, secret string) (*ManagementServer, *httptest.Server) {
	t.Helper()
	mgmt := NewManagementServer(0, "", nil)
	if err := mgmt.EnableHub(secret, true, HubClientOptions{}); err != nil {
		t.Fatalf("EnableHub(server): %v", err)
	}
	dir := t.TempDir()
	e := NewEngine("test-project", &expertRunAgent{chatTranscriptAgent: chatTranscriptAgent{stubTranscriptAgent: stubTranscriptAgent{dir: dir}}}, nil, "", LangEnglish)
	e.sessions = NewSessionManager("")
	mgmt.RegisterEngine("test-project", e)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hub/register", mgmt.handleHubRegister)
	mux.HandleFunc("/api/v1/hub/devices", mgmt.handleHubDevices)
	mux.HandleFunc("/api/v1/hub/result", mgmt.handleHubResult)
	mux.HandleFunc("/api/v1/hub/", mgmt.handleHubDeviceRoutes)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return mgmt, ts
}

func hubPost(t *testing.T, url, secret string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-Hub-Secret", secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// registerBody 解析 handleHubRegister 的 {data:{devices,ops}} 响应。
func registerBody(t *testing.T, data []byte) (devices []HubDevice, ops []HubOp) {
	t.Helper()
	var wrapped struct {
		Data struct {
			Devices []HubDevice `json:"devices"`
			Ops     []HubOp     `json:"ops"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		t.Fatalf("decode register response %q: %v", data, err)
	}
	return wrapped.Data.Devices, wrapped.Data.Ops
}

func TestHub_RegistryAuthAndForward(t *testing.T) {
	mgmt, ts := hubTestServer(t, "s3cr3t")

	// 无/错密钥被拒。
	if code, _ := hubPost(t, ts.URL+"/api/v1/hub/register", "", map[string]any{"device_token": "devA"}); code != http.StatusUnauthorized {
		t.Errorf("register without secret = %d, want 401", code)
	}
	if code, _ := hubPost(t, ts.URL+"/api/v1/hub/register", "wrong", map[string]any{"device_token": "devA"}); code != http.StatusUnauthorized {
		t.Errorf("register with wrong secret = %d, want 401", code)
	}

	// devA 首次注册（poll：取 outbox，应为空）→ 目录可见。
	agents := []*OmpAgentInfo{{Name: "qa-bot", Description: "QA 检查", Scope: "user"}}
	sessions := []HubSession{{ID: "s1", Size: 42}}
	code, data := hubPost(t, ts.URL+"/api/v1/hub/register", "s3cr3t", map[string]any{
		"device_token": "devA", "name": "机房A", "agents": agents, "sessions": sessions, "poll": true,
	})
	if code != http.StatusOK {
		t.Fatalf("register devA = %d (%s)", code, data)
	}
	devices, ops := registerBody(t, data)
	if len(devices) != 1 || devices[0].Token != "devA" || devices[0].Name != "机房A" || len(devices[0].Agents) != 1 {
		t.Errorf("devices after devA register = %+v", devices)
	}
	if len(ops) != 0 {
		t.Errorf("initial poll ops = %+v, want empty", ops)
	}

	// server 投递 run op 给 devA。
	code, data = hubPost(t, ts.URL+"/api/v1/hub/devA/run", "s3cr3t", map[string]any{"agent": "scout", "message": "hi"})
	if code != http.StatusOK {
		t.Fatalf("forward run = %d (%s)", code, data)
	}

	// devA 下次注册拉取该 op。
	code, data = hubPost(t, ts.URL+"/api/v1/hub/register", "s3cr3t", map[string]any{"device_token": "devA", "poll": true})
	if code != http.StatusOK {
		t.Fatalf("re-register = %d", code)
	}
	devices, ops = registerBody(t, data)
	if len(devices) != 1 {
		t.Errorf("re-register devices = %+v", devices)
	}
	if len(ops) != 1 || ops[0].Type != "run" || ops[0].Message != "hi" || ops[0].Agent != "scout" {
		t.Fatalf("pulled ops = %+v, want one run op", ops)
	}

	// 投递给未注册设备 → 404。
	if code, _ := hubPost(t, ts.URL+"/api/v1/hub/nope/run", "s3cr3t", map[string]any{"message": "x"}); code != http.StatusNotFound {
		t.Errorf("forward to unregistered device = %d, want 404", code)
	}

	// 客户端执行后回传结果，server 留存。executeHubOp 会等完整一轮事件流，
	// 因此在 goroutine 里跑并向其会话注入事件（模拟远端设备实际执行）。
	op := ops[0]
	rd := make(chan HubOpResult, 1)
	go func() { rd <- mgmt.executeHubOp(op) }()
	ea := mgmt.firstEngine().agent.(*expertRunAgent)
	sess := waitChatSession(t, &ea.chatTranscriptAgent)
	sess.events <- Event{Type: EventText, Content: "远端结论"}
	sess.events <- Event{Type: EventResult, Done: true}
	res := <-rd
	if !res.OK {
		t.Fatalf("executed op failed: %s", res.Error)
	}
	if !strings.Contains(res.Message, "远端结论") {
		t.Errorf("op result message = %q, want 远端结论", res.Message)
	}
	code, _ = hubPost(t, ts.URL+"/api/v1/hub/result", "s3cr3t", res)
	if code != http.StatusOK {
		t.Fatalf("result upload = %d", code)
	}
	mgmt.hub.mu.Lock()
	stored, ok := mgmt.hub.results[op.ID]
	mgmt.hub.mu.Unlock()
	if !ok || !stored.OK {
		t.Errorf("stored result = %+v, want ok for %s", stored, op.ID)
	}
}

func TestHub_RunTurnCollectsText(t *testing.T) {
	dir := t.TempDir()
	agent := &expertRunAgent{chatTranscriptAgent: chatTranscriptAgent{stubTranscriptAgent: stubTranscriptAgent{dir: dir}}}
	mgmt := NewManagementServer(0, "", nil)
	if err := mgmt.EnableHub("sec", true, HubClientOptions{}); err != nil {
		t.Fatal(err)
	}
	e := NewEngine("test-project", agent, nil, "", LangEnglish)
	e.sessions = NewSessionManager("")
	mgmt.RegisterEngine("test-project", e)

	type runRes struct {
		text string
		err  error
	}
	done := make(chan runRes, 1)
	go func() {
		text, err := mgmt.runTurnCollectize("scout", "检查一下")
		done <- runRes{text, err}
	}()
	sess := waitChatSession(t, &agent.chatTranscriptAgent)
	sess.events <- Event{Type: EventText, Content: "结论一"}
	sess.events <- Event{Type: EventText, Content: "结论二"}
	sess.events <- Event{Type: EventResult, Done: true}
	res := <-done
	if res.err != nil {
		t.Fatalf("runTurnCollectize error = %v", res.err)
	}
	if !strings.Contains(res.text, "结论一") || !strings.Contains(res.text, "结论二") {
		t.Errorf("collected text = %q, want 结论一+结论二", res.text)
	}
}

func TestHub_AgentEditOps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if _, err := CreateUserAgent(&OmpAgentFields{Name: "src-agent", Description: "S", Body: "b"}); err != nil {
		t.Fatal(err)
	}

	// copy：跨设备复制。
	op := HubOp{ID: "op-copy", Type: "agent_edit", Edit: &HubAgentEdit{Action: "copy", Source: "src-agent", Name: "cloned"}}
	res := executeHubOpTest(t, op)
	if !res.OK {
		t.Fatalf("copy op failed: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "agents", "cloned.md")); err != nil {
		t.Errorf("cloned file missing: %v", err)
	}

	// delete。
	op = HubOp{ID: "op-del", Type: "agent_edit", Edit: &HubAgentEdit{Action: "delete", Name: "cloned"}}
	res = executeHubOpTest(t, op)
	if !res.OK {
		t.Fatalf("delete op failed: %s", res.Error)
	}
	if _, err := os.Stat(filepath.Join(home, ".omp", "agent", "agents", "cloned.md")); !os.IsNotExist(err) {
		t.Errorf("cloned file still present: %v", err)
	}

	// update。
	op = HubOp{ID: "op-up", Type: "agent_edit", Edit: &HubAgentEdit{
		Action: "update", Name: "src-agent",
		Fields: &OmpAgentWriteRequest{Description: "S2", Body: "b2"},
	}}
	res = executeHubOpTest(t, op)
	if !res.OK {
		t.Fatalf("update op failed: %s", res.Error)
	}
	meta, body := ParseAgentMarkdown(mustReadFile(t, filepath.Join(home, ".omp", "agent", "agents", "src-agent.md")))
	if meta["description"] != "S2" || body != "b2" {
		t.Errorf("after update op: meta=%v body=%q", meta, body)
	}
}

// executeHubOpTest 调用 mgmt.executeHubOp（需要 hub 已启用以便读取默认引擎）。
func executeHubOpTest(t *testing.T, op HubOp) HubOpResult {
	t.Helper()
	mgmt := NewManagementServer(0, "", nil)
	if err := mgmt.EnableHub("sec", true, HubClientOptions{}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	e := NewEngine("test-project", &chatTranscriptAgent{stubTranscriptAgent: stubTranscriptAgent{dir: t.TempDir()}}, nil, "", LangEnglish)
	e.sessions = NewSessionManager("")
	mgmt.RegisterEngine("test-project", e)
	return mgmt.executeHubOp(op)
}

// TestHub_ClientSyncLoopIntegration 用两个真实 ManagementServer 通过 HTTP 互连走
// 完整链路：client 周期上报 → server 目录可见 → server 转发 run op → client 下次
// 同步拉取并本地执行 → 结果回传 server 留存（客户端互查 + 服务端对话转发）。
func TestHub_ClientSyncLoopIntegration(t *testing.T) {
	sdir := t.TempDir()
	serverM := NewManagementServer(0, "", nil)
	if err := serverM.EnableHub("sec", true, HubClientOptions{}); err != nil {
		t.Fatal(err)
	}
	se := NewEngine("server-proj", &chatTranscriptAgent{stubTranscriptAgent: stubTranscriptAgent{dir: sdir}}, nil, "", LangEnglish)
	se.sessions = NewSessionManager("")
	serverM.RegisterEngine("server-proj", se)
	smux := http.NewServeMux()
	smux.HandleFunc("/api/v1/hub/register", serverM.handleHubRegister)
	smux.HandleFunc("/api/v1/hub/devices", serverM.handleHubDevices)
	smux.HandleFunc("/api/v1/hub/result", serverM.handleHubResult)
	smux.HandleFunc("/api/v1/hub/", serverM.handleHubDeviceRoutes)
	sts := httptest.NewServer(smux)
	defer sts.Close()

	cdir := t.TempDir()
	clientAgent := &expertRunAgent{chatTranscriptAgent: chatTranscriptAgent{stubTranscriptAgent: stubTranscriptAgent{dir: cdir}}}
	cm := NewManagementServer(0, "", nil)
	ce := NewEngine("client-proj", clientAgent, nil, "", LangEnglish)
	ce.sessions = NewSessionManager("")
	cm.RegisterEngine("client-proj", ce)
	if err := cm.EnableHub("sec", false, HubClientOptions{
		ServerURL: sts.URL, DeviceToken: "devC", DeviceName: "客户端C", SyncIntervalM: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// EnableHub 会立刻跑一次首次同步；等它把 devC 注册上。
	deadline := time.Now().Add(5 * time.Second)
	for {
		serverM.hub.mu.Lock()
		_, ok := serverM.hub.devices["devC"]
		serverM.hub.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("client never registered with server")
		}
		time.Sleep(10 * time.Millisecond)
	}
	serverM.hub.mu.Lock()
	devName := serverM.hub.devices["devC"].Name
	serverM.hub.mu.Unlock()
	if devName != "客户端C" {
		t.Errorf("server device name = %q, want 客户端C", devName)
	}

	// 服务端转发「用专家执行」给 devC。
	if code, _ := hubPost(t, sts.URL+"/api/v1/hub/devC/run", "sec", map[string]any{"agent": "scout", "message": "你好"}); code != http.StatusOK {
		t.Fatalf("forward run status = %d", code)
	}

	// client 下次同步：带 poll 拉取 server 投递的 op（走真实 HTTP 端点）。
	code, data := hubPost(t, sts.URL+"/api/v1/hub/register", "sec", map[string]any{"device_token": "devC", "name": "客户端C", "poll": true})
	if code != http.StatusOK {
		t.Fatalf("client pull register = %d", code)
	}
	_, pulled := registerBody(t, data)
	if len(pulled) != 1 || pulled[0].Message != "你好" {
		t.Fatalf("client pulled ops = %+v, want one run op", pulled)
	}

	// client 本地执行拉到的 op（goroutine 里等事件流，主 goroutine 注入）。
	op := pulled[0]
	rd := make(chan HubOpResult, 1)
	go func() { rd <- cm.executeHubOp(op) }()
	sess := waitChatSession(t, &clientAgent.chatTranscriptAgent)
	sess.events <- Event{Type: EventText, Content: "同步结果"}
	sess.events <- Event{Type: EventResult, Done: true}
	res := <-rd
	if !res.OK {
		t.Fatalf("client executed op failed: %s", res.Error)
	}

	// client 把结果回传，server 留存（服务端可查）。
	cm.reportHubResult(sts.URL, res)
	serverM.hub.mu.Lock()
	stored, ok := serverM.hub.results[op.ID]
	serverM.hub.mu.Unlock()
	if !ok || !stored.OK || !strings.Contains(stored.Message, "同步结果") {
		t.Errorf("server stored result = %+v (ok=%v), want executed 同步结果", stored, ok)
	}
}

// TestHub_WebOverviewEndpoints 验证 web 看板用的 hub 视图/转发/结果查询三个端点
// （逻辑层，不含 wrap 鉴权——wrap 已在 management 路由测试覆盖）。
func TestHub_WebOverviewEndpoints(t *testing.T) {
	mgmt, ts := hubTestServer(t, "sec")
	code, _ := hubPost(t, ts.URL+"/api/v1/hub/register", "sec", map[string]any{
		"device_token": "devX", "name": "设备X",
	})
	if code != http.StatusOK {
		t.Fatalf("register status = %d", code)
	}

	// 全网视图应含 devX。
	rec := httptest.NewRecorder()
	mgmt.handleHubOverview(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hub/overview", nil))
	var got struct {
		Data struct {
			Devices []HubDevice `json:"devices"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if len(got.Data.Devices) != 1 || got.Data.Devices[0].Token != "devX" || got.Data.Devices[0].Name != "设备X" {
		t.Fatalf("overview devices = %+v, want [devX]", got.Data.Devices)
	}

	// 转发 run → queued id；结果未回传时 pending。
	rec = httptest.NewRecorder()
	body := strings.NewReader(`{"agent":"scout","message":"跑一下"}`)
	mgmt.handleHubOverviewRoutes(rec, httptest.NewRequest(http.MethodPost, "/api/v1/hub/overview/devX/run", body))
	var queued struct {
		Data struct {
			Queued string `json:"queued"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &queued); err != nil || queued.Data.Queued == "" {
		t.Fatalf("overview run = %q, err=%v", queued.Data.Queued, err)
	}
	rec = httptest.NewRecorder()
	mgmt.handleHubOverviewRoutes(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hub/overview/result/"+queued.Data.Queued, nil))
	var pending struct {
		Data struct {
			Pending bool `json:"pending"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil || !pending.Data.Pending {
		t.Fatalf("result before execution: pending=%v err=%v", pending.Data.Pending, err)
	}

	// 执行回传后，结果可查。
	mgmt.hub.mu.Lock()
	mgmt.hub.results[queued.Data.Queued] = HubOpResult{OpID: queued.Data.Queued, OK: true, Message: "执行完成"}
	mgmt.hub.mu.Unlock()
	rec = httptest.NewRecorder()
	mgmt.handleHubOverviewRoutes(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hub/overview/result/"+queued.Data.Queued, nil))
	if !strings.Contains(rec.Body.String(), "执行完成") {
		t.Fatalf("result after execution = %s, want 执行完成", rec.Body.String())
	}
}
