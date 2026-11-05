package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// ── hub 配对 / 反向隧道 ─────────────────────────────────────────────
//
// server 模式：中心持有一条到每个节点的 WebSocket，节点全程主动出站。
// 浏览器的 omp-pcN.lizeyu.win 请求先到 cloudflared，再交给本地 relay；
// relay 按 Host 找到对应 tunnel，用 yamux 开一条流，节点把流原样转发到
// 127.0.0.1:30177。节点与中心断联时，本地 ompweb / 会话 / 定时任务不受影响。
//
// client 模式：节点用一次性配对码换取 device_token + secret，随后主动连
// /api/v1/hub/tunnel；断线后按指数退避重连。

const (
	hubPairTTL           = 10 * time.Minute
	hubPairAlphabet      = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	hubPairCodeLen       = 8
	hubTunnelDialTimeout = 10 * time.Second
)

// HubDeviceRecord is the center-side persisted record for a paired node.
type HubDeviceRecord struct {
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	Host      string    `json:"host"`
	WebURL    string    `json:"web_url"`
	CreatedAt time.Time `json:"created_at"`

	// Secret is this node's tunnel/heartbeat credential. Empty means the
	// legacy global hub secret (records created before per-node secrets).
	Secret string `json:"secret,omitempty"`
	// PrevSecret is accepted only while a rotated node catches up: the node
	// still authenticates with the old value until its next sync stores Secret.
	PrevSecret string `json:"prev_secret,omitempty"`
	// LoginCodeHash gates browser access through the center relay. Empty means
	// no center-side login prompt; the node's own OMP Web auth still applies.
	LoginCodeHash string `json:"login_code_hash,omitempty"`
}

// HubPairCode is a one-time enrollment code bound to a device slot.
type HubPairCode struct {
	Code    string    `json:"code"`
	Token   string    `json:"token"`
	Name    string    `json:"name"`
	Host    string    `json:"host"`
	Expires time.Time `json:"expires_at"`
}

type hubDevicesFile struct {
	Devices []*HubDeviceRecord `json:"devices"`
}

type hubClientState struct {
	ServerURL   string `json:"server_url"`
	Secret      string `json:"secret"`
	DeviceToken string `json:"device_token"`
	DeviceName  string `json:"device_name"`
	WebURL      string `json:"web_url"`
}

type hubTunnel struct {
	device    string
	conn      *websocket.Conn
	session   *yamux.Session
	lastSeen  time.Time
	closeOnce sync.Once
}

func (t *hubTunnel) close() {
	t.closeOnce.Do(func() {
		_ = t.session.Close()
		_ = t.conn.Close()
	})
}

var hubWSUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   32 * 1024,
	WriteBufferSize:  32 * 1024,
	// The tunnel is authenticated with X-Hub-Secret, not a browser Origin.
	CheckOrigin: func(*http.Request) bool { return true },
}

// ── state files ────────────────────────────────────────────────────

func loadHubClientState(path string) (*hubClientState, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st hubClientState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &st, nil
}

func (m *ManagementServer) loadHubDevices() error {
	h := m.hub
	if h == nil || strings.TrimSpace(h.stateFile) == "" {
		return nil
	}
	b, err := os.ReadFile(h.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var file hubDevicesFile
	if err := json.Unmarshal(b, &file); err != nil {
		return fmt.Errorf("parse %s: %w", h.stateFile, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rec := range file.Devices {
		if rec == nil || strings.TrimSpace(rec.Token) == "" {
			continue
		}
		h.paired[rec.Token] = rec
		if host := strings.ToLower(strings.TrimSpace(rec.Host)); host != "" {
			h.hostIndex[host] = rec.Token
		}
	}
	return nil
}

// saveHubDevicesLocked persists paired devices. The caller must hold h.mu.
func (m *ManagementServer) saveHubDevicesLocked() {
	h := m.hub
	if h == nil || strings.TrimSpace(h.stateFile) == "" {
		return
	}
	file := hubDevicesFile{Devices: make([]*HubDeviceRecord, 0, len(h.paired))}
	for _, rec := range h.paired {
		file.Devices = append(file.Devices, rec)
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		slog.Error("hub: encode paired devices", "error", err)
		return
	}
	b = append(b, '\n')
	dir := filepath.Dir(h.stateFile)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			slog.Error("hub: create state dir", "error", err)
			return
		}
	}
	tmp, err := os.CreateTemp(dir, ".hub-devices-*.tmp")
	if err != nil {
		slog.Error("hub: create state temp", "error", err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		slog.Error("hub: chmod state temp", "error", err)
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		slog.Error("hub: write state temp", "error", err)
		return
	}
	if err := tmp.Close(); err != nil {
		slog.Error("hub: close state temp", "error", err)
		return
	}
	if err := os.Rename(tmpName, h.stateFile); err != nil {
		slog.Error("hub: rename state", "error", err)
	}
}

// ── pairing API ────────────────────────────────────────────────────

// handleHubPairNew creates a one-time pairing code. Mounted behind the
// management token by buildHandler.
func (m *ManagementServer) handleHubPairNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !m.requireHubServer(w) {
		return
	}
	var body struct {
		DeviceToken string `json:"device_token"`
		Name        string `json:"name"`
		Host        string `json:"host"`
		Force       bool   `json:"force"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	token := strings.TrimSpace(body.DeviceToken)
	if !validHubToken(token) {
		mgmtError(w, http.StatusBadRequest, "device_token must be 1-64 chars of [A-Za-z0-9_-]")
		return
	}
	host := ""
	if strings.TrimSpace(body.Host) == "" {
		host = m.defaultNodeHost(token)
		if host == "" {
			mgmtError(w, http.StatusBadRequest, "host is required; set [hub] node_host_suffix or pass host")
			return
		}
	} else {
		var err error
		host, err = normalizeHubHost(body.Host)
		if err != nil {
			mgmtError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = token
	}
	now := time.Now()
	h := m.hub
	h.mu.Lock()
	if _, exists := h.paired[token]; exists && !body.Force {
		h.mu.Unlock()
		mgmtError(w, http.StatusConflict, "device_token is already paired; pass force=true to replace it")
		return
	}
	code := ""
	for range 5 {
		candidate, genErr := newHubPairCode()
		if genErr != nil {
			h.mu.Unlock()
			mgmtError(w, http.StatusInternalServerError, "generate pairing code: "+genErr.Error())
			return
		}
		if _, exists := h.pairCodes[candidate]; !exists {
			code = candidate
			break
		}
	}
	if code == "" {
		h.mu.Unlock()
		mgmtError(w, http.StatusInternalServerError, "could not allocate pairing code")
		return
	}
	pc := &HubPairCode{
		Code:    code,
		Token:   token,
		Name:    name,
		Host:    host,
		Expires: now.Add(hubPairTTL),
	}
	h.pairCodes[code] = pc
	h.mu.Unlock()

	base := publicHubBaseURL(r)
	command := fmt.Sprintf("omp-lark-bridge hub pair %s %s --name %s", base, code, shellQuote(name))
	mgmtJSON(w, http.StatusOK, map[string]any{
		"code":         code,
		"device_token": token,
		"name":         name,
		"host":         host,
		"web_url":      "https://" + host,
		"expires_at":   pc.Expires,
		"command":      command,
	})
}

// handleHubPairClaim exchanges a one-time code for the device credentials.
// It is intentionally not wrapped by the management token: the code itself is
// the authorization, and it is consumed on first use.
func (m *ManagementServer) handleHubPairClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	if !m.requireHubServer(w) {
		return
	}
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))
	if code == "" {
		mgmtError(w, http.StatusBadRequest, "code is required")
		return
	}
	h := m.hub
	h.mu.Lock()
	pc, ok := h.pairCodes[code]
	if !ok {
		h.mu.Unlock()
		mgmtError(w, http.StatusBadRequest, "invalid or expired pairing code")
		return
	}
	delete(h.pairCodes, code)
	if time.Now().After(pc.Expires) {
		h.mu.Unlock()
		mgmtError(w, http.StatusGone, "pairing code expired")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = pc.Name
	}
	if name == "" {
		name = pc.Token
	}
	secret, err := newHubDeviceSecret()
	if err != nil {
		h.mu.Unlock()
		mgmtError(w, http.StatusInternalServerError, "generate device secret: "+err.Error())
		return
	}
	rec := &HubDeviceRecord{
		Token:     pc.Token,
		Name:      name,
		Host:      pc.Host,
		WebURL:    "https://" + pc.Host,
		CreatedAt: time.Now(),
		Secret:    secret,
	}
	h.paired[pc.Token] = rec
	h.hostIndex[pc.Host] = pc.Token
	if old := h.tunnels[pc.Token]; old != nil {
		delete(h.tunnels, pc.Token)
		go old.close()
	}
	m.saveHubDevicesLocked()
	h.mu.Unlock()

	mgmtJSON(w, http.StatusOK, map[string]any{
		"device_token": pc.Token,
		"name":         name,
		"secret":       secret,
		"host":         pc.Host,
		"web_url":      rec.WebURL,
		"server_url":   publicHubBaseURL(r),
	})
}

// saveHubClientState persists enrollment credentials with owner-only perms.
func saveHubClientState(path string, st hubClientState) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("hub: state_file is not configured")
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Windows does not always replace an existing destination atomically.
		// Fall back to an in-place write; the file remains owner-only.
		if werr := os.WriteFile(path, b, 0o600); werr != nil {
			_ = os.Remove(tmp)
			return werr
		}
		_ = os.Remove(tmp)
	}
	return nil
}

// handleHubPairJoin enrolls this node with its configured center using a
// one-time code typed into the console; it is the web counterpart of
// `omp-lark-bridge hub pair`. Client mode only, behind the management token.
//
// A fresh node applies the credentials immediately (its client loops have not
// started yet). Re-pairing an already-enrolled node persists the new state and
// asks for a restart, because the running loops still hold the old secret.
func (m *ManagementServer) handleHubPairJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	h := m.hub
	if h == nil {
		mgmtError(w, http.StatusServiceUnavailable, "hub is not enabled")
		return
	}
	if h.serverMode {
		mgmtError(w, http.StatusConflict, "hub is in server mode; issue pairing codes on the center instead")
		return
	}
	var body struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.Code))
	if code == "" {
		mgmtError(w, http.StatusBadRequest, "code is required")
		return
	}
	h.mu.Lock()
	if h.pairingNow {
		h.mu.Unlock()
		mgmtError(w, http.StatusConflict, "pairing already in progress")
		return
	}
	h.pairingNow = true
	serverURL := strings.TrimRight(strings.TrimSpace(h.serverURL), "/")
	stateFile := h.stateFile
	alreadyPaired := h.clientToken != ""
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.pairingNow = false
		h.mu.Unlock()
	}()
	if serverURL == "" {
		mgmtError(w, http.StatusBadRequest, "hub: server_url is not configured")
		return
	}

	payload, _ := json.Marshal(map[string]string{"code": code, "name": strings.TrimSpace(body.Name)})
	req, err := http.NewRequest(http.MethodPost, serverURL+"/api/v1/hub/pair/claim", bytes.NewReader(payload))
	if err != nil {
		mgmtError(w, http.StatusBadGateway, "build claim request: "+err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		mgmtError(w, http.StatusBadGateway, "contact center: "+err.Error())
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Data  struct {
			DeviceToken string `json:"device_token"`
			Name        string `json:"name"`
			Secret      string `json:"secret"`
			WebURL      string `json:"web_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		mgmtError(w, http.StatusBadGateway, fmt.Sprintf("center returned %s", resp.Status))
		return
	}
	if resp.StatusCode != http.StatusOK || !envelope.OK {
		msg := envelope.Error
		if msg == "" {
			msg = resp.Status
		}
		mgmtError(w, http.StatusBadGateway, "pair failed: "+msg)
		return
	}
	out := envelope.Data
	if out.DeviceToken == "" || out.Secret == "" {
		mgmtError(w, http.StatusBadGateway, "center returned an incomplete pairing response")
		return
	}
	if out.Name == "" {
		out.Name = out.DeviceToken
	}
	st := hubClientState{
		ServerURL:   serverURL,
		Secret:      out.Secret,
		DeviceToken: out.DeviceToken,
		DeviceName:  out.Name,
		WebURL:      normalizeHubWebURL(out.WebURL),
	}
	if err := saveHubClientState(stateFile, st); err != nil {
		mgmtError(w, http.StatusInternalServerError, "persist pairing state: "+err.Error())
		return
	}
	applied := false
	if !alreadyPaired {
		h.mu.Lock()
		h.secret = st.Secret
		h.clientToken = st.DeviceToken
		h.deviceName = st.DeviceName
		h.webURL = st.WebURL
		webURL := h.webURL
		h.mu.Unlock()
		if webURL != "" && strings.TrimSpace(os.Getenv("OMPWEB_PUBLIC_URL")) == "" {
			os.Setenv("OMPWEB_PUBLIC_URL", webURL)
		}
		m.startHubClientLoop()
		m.startHubTunnelClient()
		applied = true
	}
	slog.Info("hub: node paired", "device", st.DeviceToken, "name", st.DeviceName,
		"server_url", st.ServerURL, "applied", applied)
	mgmtJSON(w, http.StatusOK, map[string]any{
		"paired":           true,
		"applied":          applied,
		"restart_required": !applied,
		"device_token":     st.DeviceToken,
		"device_name":      st.DeviceName,
		"web_url":          st.WebURL,
		"server_url":       st.ServerURL,
	})
}

// handleHubPairDevice removes a paired device. The node keeps running locally;
// its next tunnel attempt is rejected until an admin pairs it again.
func (m *ManagementServer) handleHubPairDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		mgmtError(w, http.StatusMethodNotAllowed, "DELETE only")
		return
	}
	if !m.requireHubServer(w) {
		return
	}
	device := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/hub/pair/"), "/")
	if !validHubToken(device) {
		mgmtError(w, http.StatusBadRequest, "invalid device_token")
		return
	}
	h := m.hub
	h.mu.Lock()
	rec, ok := h.paired[device]
	if !ok {
		h.mu.Unlock()
		mgmtError(w, http.StatusNotFound, "device is not paired")
		return
	}
	delete(h.paired, device)
	delete(h.devices, device)
	delete(h.hostIndex, strings.ToLower(rec.Host))
	if tun := h.tunnels[device]; tun != nil {
		delete(h.tunnels, device)
		go tun.close()
	}
	m.saveHubDevicesLocked()
	h.mu.Unlock()
	slog.Info("hub: device removed", "device", device, "host", rec.Host)
	mgmtJSON(w, http.StatusOK, map[string]any{"removed": true, "device_token": device})
}

// handleHubNodeUpdate lets the center rename a node and set its tunnel secret
// or browser login code. Node consoles are read-only: this handler is guarded
// by requireHubServer.
func (m *ManagementServer) handleHubNodeUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		mgmtError(w, http.StatusMethodNotAllowed, "PATCH only")
		return
	}
	if !m.requireHubServer(w) {
		return
	}
	device := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/hub/nodes/"), "/")
	if !validHubToken(device) {
		mgmtError(w, http.StatusBadRequest, "invalid device_token")
		return
	}
	var body struct {
		Name           *string `json:"name"`
		Secret         *string `json:"secret"`
		LoginCode      *string `json:"login_code"`
		ClearLoginCode bool    `json:"clear_login_code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Name == nil && body.Secret == nil && body.LoginCode == nil && !body.ClearLoginCode {
		mgmtError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	if body.LoginCode != nil && body.ClearLoginCode {
		mgmtError(w, http.StatusBadRequest, "login_code and clear_login_code are mutually exclusive")
		return
	}
	h := m.hub
	h.mu.Lock()
	rec := h.paired[device]
	if rec == nil {
		h.mu.Unlock()
		mgmtError(w, http.StatusNotFound, "device is not paired")
		return
	}
	secretRotated := false
	if body.Name != nil {
		name := strings.TrimSpace(*body.Name)
		if name == "" {
			name = device
		}
		rec.Name = name
	}
	if body.Secret != nil {
		secret := strings.TrimSpace(*body.Secret)
		if len(secret) < 16 || len(secret) > 128 || strings.ContainsAny(secret, " \t\r\n") {
			h.mu.Unlock()
			mgmtError(w, http.StatusBadRequest, "secret must be 16-128 chars without whitespace")
			return
		}
		if rec.Secret != "" {
			rec.PrevSecret = rec.Secret
		} else {
			rec.PrevSecret = h.secret
		}
		rec.Secret = secret
		secretRotated = true
	}
	if body.ClearLoginCode {
		rec.LoginCodeHash = ""
	}
	if body.LoginCode != nil {
		code := strings.TrimSpace(*body.LoginCode)
		if !validHubLoginCode(code) {
			h.mu.Unlock()
			mgmtError(w, http.StatusBadRequest, "login_code must be 4-128 printable chars")
			return
		}
		rec.LoginCodeHash = hashHubLoginCode(code)
	}
	loginSet := rec.LoginCodeHash != ""
	m.saveHubDevicesLocked()
	var tun *hubTunnel
	if secretRotated {
		tun = h.tunnels[device]
		delete(h.tunnels, device)
	}
	h.mu.Unlock()
	if tun != nil {
		go tun.close()
	}
	slog.Info("hub: node updated", "device", device, "secret_rotated", secretRotated,
		"login_code_set", loginSet)
	mgmtJSON(w, http.StatusOK, map[string]any{
		"updated":          true,
		"device_token":     device,
		"secret_rotated":   secretRotated,
		"login_code_set":   loginSet,
		"restart_required": false,
	})
}

// ── tunnel server ─────────────────────────────────────────────────

// handleHubTunnel upgrades a node's outbound WebSocket and serves yamux streams.
func (m *ManagementServer) handleHubTunnel(w http.ResponseWriter, r *http.Request) {
	if !m.requireHubServer(w) {
		return
	}
	device := strings.TrimSpace(r.Header.Get("X-Hub-Device"))
	if device == "" {
		mgmtError(w, http.StatusBadRequest, "X-Hub-Device is required")
		return
	}
	if !m.authHubDevice(r, device) {
		mgmtError(w, http.StatusUnauthorized, "invalid hub secret")
		return
	}
	h := m.hub
	h.mu.Lock()
	_, paired := h.paired[device]
	h.mu.Unlock()
	if !paired {
		mgmtError(w, http.StatusForbidden, "device is not paired")
		return
	}
	conn, err := hubWSUpgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("hub: tunnel upgrade failed", "device", device, "error", err)
		return
	}
	ws := newWSNetConn(conn)
	session, err := yamux.Client(ws, hubYamuxConfig())
	if err != nil {
		_ = conn.Close()
		slog.Warn("hub: yamux session failed", "device", device, "error", err)
		return
	}
	tun := &hubTunnel{device: device, conn: conn, session: session, lastSeen: time.Now()}
	h.mu.Lock()
	if old := h.tunnels[device]; old != nil {
		delete(h.tunnels, device)
		go old.close()
	}
	h.tunnels[device] = tun
	h.mu.Unlock()
	slog.Info("hub: tunnel connected", "device", device)

	<-session.CloseChan()

	h.mu.Lock()
	if h.tunnels[device] == tun {
		delete(h.tunnels, device)
	}
	h.mu.Unlock()
	tun.close()
	slog.Info("hub: tunnel disconnected", "device", device)
}

// hubTunnelFor returns the live tunnel for a device, if any.
func (m *ManagementServer) hubTunnelFor(device string) *hubTunnel {
	h := m.hub
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tunnels[device]
}

// hubHostToken maps a public Host header to a device token.
func (m *ManagementServer) hubHostToken(host string) string {
	h := m.hub
	if h == nil {
		return ""
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hostIndex[host]
}

// hubLoginAllowed enforces the optional center-side login code for a node.
// The code is stored as a hash; browsers use it as the HTTP Basic password.
// An empty hash preserves the previous direct-access behavior.
func (m *ManagementServer) hubLoginAllowed(req *http.Request, token string) bool {
	h := m.hub
	if h == nil {
		return false
	}
	h.mu.Lock()
	hash := ""
	if rec := h.paired[token]; rec != nil {
		hash = rec.LoginCodeHash
	}
	h.mu.Unlock()
	if hash == "" {
		return true
	}
	_, code, ok := req.BasicAuth()
	if !ok || code == "" {
		return false
	}
	sum := sha256.Sum256([]byte(code))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(hash)) == 1
}

// ── relay ─────────────────────────────────────────────────────────

var errHubNodeOffline = errors.New("hub: node offline")

type hubRelay struct {
	m         *ManagementServer
	srv       *http.Server
	transport *http.Transport
}

// startHubRelay starts the loopback HTTP endpoint that cloudflared points
// omp-pcN.lizeyu.win at. It must only listen on loopback.
func (m *ManagementServer) startHubRelay() error {
	h := m.hub
	if h == nil || h.relayPort < 0 {
		return nil
	}
	port := h.relayPort
	if port == 0 {
		port = 31991
	}
	relay := &hubRelay{m: m}
	relay.transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			tun := m.hubTunnelFor(host)
			if tun == nil {
				return nil, errHubNodeOffline
			}
			return tun.session.OpenStream()
		},
		DisableKeepAlives:     true,
		ForceAttemptHTTP2:     false,
		ResponseHeaderTimeout: 90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	relay.srv = &http.Server{
		Addr:              net.JoinHostPort("127.0.0.1", strconv.Itoa(port)),
		Handler:           http.HandlerFunc(relay.handle),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", relay.srv.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", relay.srv.Addr, err)
	}
	h.mu.Lock()
	h.relay = relay
	h.mu.Unlock()
	go func() {
		if err := relay.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("hub: relay server error", "error", err)
		}
	}()
	slog.Info("hub: relay started", "addr", relay.srv.Addr)
	return nil
}

func (r *hubRelay) handle(w http.ResponseWriter, req *http.Request) {
	host := req.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	token := r.m.hubHostToken(host)
	if token == "" {
		http.Error(w, "unknown node", http.StatusNotFound)
		return
	}
	if !r.m.hubLoginAllowed(req, token) {
		w.Header().Set("WWW-Authenticate", `Basic realm="OMP node"`)
		http.Error(w, "login required", http.StatusUnauthorized)
		return
	}
	if r.m.hubTunnelFor(token) == nil {
		http.Error(w, "node offline", http.StatusBadGateway)
		return
	}
	proxy := &httputil.ReverseProxy{
		Director: func(out *http.Request) {
			out.URL.Scheme = "http"
			out.URL.Host = token + ":30177"
			out.Host = host
		},
		Transport:     r.transport,
		FlushInterval: -1,
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			if errors.Is(err, errHubNodeOffline) {
				http.Error(rw, "node offline", http.StatusBadGateway)
				return
			}
			slog.Warn("hub: relay proxy failed", "host", host, "device", token, "error", err)
			http.Error(rw, "bad gateway", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, req)
}

// ── tunnel client ─────────────────────────────────────────────────

func (m *ManagementServer) startHubTunnelClient() {
	h := m.hub
	if h == nil || h.serverURL == "" || h.clientToken == "" {
		return
	}
	go m.hubTunnelClientLoop(h)
}

func (m *ManagementServer) hubTunnelClientLoop(h *hubData) {
	backoff := time.Second
	for {
		started := time.Now()
		err := m.runHubTunnelOnce(h)
		if err != nil {
			slog.Warn("hub: tunnel disconnected", "server", h.serverURL, "error", err)
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		select {
		case <-h.stop:
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

func (m *ManagementServer) runHubTunnelOnce(h *hubData) error {
	u, err := url.Parse(h.serverURL)
	if err != nil {
		return fmt.Errorf("parse server_url: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return fmt.Errorf("unsupported server_url scheme %q", u.Scheme)
	}
	u.Path = "/api/v1/hub/tunnel"
	u.RawQuery = ""
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = hubTunnelDialTimeout
	header := http.Header{}
	header.Set("X-Hub-Secret", h.secret)
	header.Set("X-Hub-Device", h.clientToken)
	conn, resp, err := dialer.Dial(u.String(), header)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial tunnel: %w (status %s)", err, resp.Status)
		}
		return fmt.Errorf("dial tunnel: %w", err)
	}
	ws := newWSNetConn(conn)
	session, err := yamux.Server(ws, hubYamuxConfig())
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("yamux session: %w", err)
	}
	defer session.Close()
	slog.Info("hub: tunnel established", "server", h.serverURL, "device", h.clientToken)
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			return err
		}
		go proxyHubStream(stream, h.targetAddr)
	}
}

// proxyHubStream pipes one yamux stream to the local ompweb listener.
func proxyHubStream(stream net.Conn, target string) {
	local, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		slog.Warn("hub: local ompweb unreachable", "target", target, "error", err)
		_ = stream.Close()
		return
	}
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = local.Close()
			_ = stream.Close()
		})
	}
	go func() {
		_, _ = io.Copy(local, stream)
		closeBoth()
	}()
	_, _ = io.Copy(stream, local)
	closeBoth()
}

// ── websocket net.Conn adapter ────────────────────────────────────

type wsNetConn struct {
	conn    *websocket.Conn
	readMu  sync.Mutex
	writeMu sync.Mutex
	reader  io.Reader
}

func newWSNetConn(conn *websocket.Conn) *wsNetConn {
	return &wsNetConn{conn: conn}
}

func (c *wsNetConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if c.reader == nil {
			mt, r, err := c.conn.NextReader()
			if err != nil {
				return 0, err
			}
			if mt != websocket.BinaryMessage {
				continue
			}
			c.reader = r
		}
		n, err := c.reader.Read(p)
		if errors.Is(err, io.EOF) {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsNetConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsNetConn) Close() error                     { return c.conn.Close() }
func (c *wsNetConn) LocalAddr() net.Addr              { return c.conn.LocalAddr() }
func (c *wsNetConn) RemoteAddr() net.Addr             { return c.conn.RemoteAddr() }
func (c *wsNetConn) SetDeadline(time.Time) error      { return nil }
func (c *wsNetConn) SetReadDeadline(time.Time) error  { return nil }
func (c *wsNetConn) SetWriteDeadline(time.Time) error { return nil }

// ── helpers ───────────────────────────────────────────────────────

func hubYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.LogOutput = io.Discard
	return cfg
}

func newHubPairCode() (string, error) {
	buf := make([]byte, hubPairCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = hubPairAlphabet[int(buf[i])%len(hubPairAlphabet)]
	}
	return string(buf), nil
}

func newHubDeviceSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func hashHubLoginCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func validHubLoginCode(code string) bool {
	if len(code) < 4 || len(code) > 128 {
		return false
	}
	for _, r := range code {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validHubToken(token string) bool {
	if token == "" || len(token) > 64 {
		return false
	}
	for _, r := range token {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// defaultNodeHost derives omp-<token>.<suffix> when the admin leaves the host
// blank. The suffix is explicitly configured or taken from the center's OMP Web
// public URL (e.g. https://omp-center.lizeyu.win -> lizeyu.win).
func (m *ManagementServer) defaultNodeHost(token string) string {
	h := m.hub
	if h == nil {
		return ""
	}
	h.mu.Lock()
	suffix := h.nodeHostSuffix
	h.mu.Unlock()
	if suffix == "" {
		if u, err := url.Parse(ompWebConfiguredPublicURL()); err == nil && u.Hostname() != "" {
			if _, rest, ok := strings.Cut(u.Hostname(), "."); ok {
				suffix = rest
			}
		}
	}
	if suffix == "" {
		return ""
	}
	return "omp-" + token + "." + suffix
}

func normalizeHubHost(raw string) (string, error) {
	host := strings.ToLower(strings.TrimSpace(raw))
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", errors.New("host is required (e.g. omp-pc1.example.com)")
	}
	if len(host) > 253 || strings.ContainsAny(host, "/:@?# \t\r\n") || !strings.Contains(host, ".") {
		return "", errors.New("host must be a bare DNS name without scheme, port, or path")
	}
	return host, nil
}

func publicHubBaseURL(r *http.Request) string {
	scheme := "https"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	} else if r.TLS == nil {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch host {
		case "127.0.0.1", "localhost", "::1":
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host
}
