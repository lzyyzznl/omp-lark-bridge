package core

// ── mobile: 手机端网关 ────────────────────────────────────────────────
// 手机 App 是这个控制面的第二把钥匙：
//   - 节点网页（center relay 下的 omp-<token>.<suffix>）默认必须由手机确认，
//     确认后下发 HttpOnly 访问 cookie，浏览器才能进入节点控制台；
//   - 密钥类操作（节点登录码 / 隧道密钥 / 配对 / 移除节点）默认要求手机
//     私钥签名，浏览器里的 management token 单独无法完成。
// 设备密钥在 enroll 时一次性下发，保存到磁盘（0600）。每个 API 请求用
// HMAC-SHA256(secret, method\npath?query\ntimestamp\nsha256(body)) 签名，
// 时间戳窗口 ±5 分钟并带重放缓存。
//
// 历史接口分工：
//   - /api/v1/mobile/*         中心端口（31990），App 直接调用；
//   - /api/v1/mobile/approvals/status 与 /__omp/grant 由节点 relay（31991）
//     在节点域名下本地处理，不转发给节点进程。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	mobileDeviceHeader = "X-OMP-Mobile-Device"
	mobileTSHeader     = "X-OMP-Mobile-Ts"
	mobileSigHeader    = "X-OMP-Mobile-Sig"

	mobileAccessCookie = "omp_node_access"

	// mobileConsoleNode is the pseudo node key that scopes a grant to the
	// management console. Grants are per key, so approving the node list does
	// not open nodes and vice versa.
	mobileConsoleNode = "console"

	mobileApprovalTTL = 3 * time.Minute
	mobileLinkTTL     = 5 * time.Minute
	mobileGrantTTL    = 10 * time.Minute
	mobileAccessTTL   = 12 * time.Hour
	mobileClockSkew   = 5 * time.Minute
)

// MobileOptions configures the phone gateway. Zero values keep the defaults:
// both gates on, state file under the data dir.
type MobileOptions struct {
	StateFile      string
	RequireConsole bool // node web pages require phone approval
	RequireForKeys bool // key/pairing mutations require a phone signature
	// WebTrustedSecret, when set, is sent as X-Omp-Web-Auth towards the node's
	// ompweb once a phone has approved the request. ompweb must hold its SHA-256
	// digest in OMP_WEB_TRUSTED_HEADER_SHA256, which makes the workspace
	// password unnecessary for phone traffic while direct access still needs it.
	WebTrustedSecret string
}

// mobileDevice is an enrolled phone. Secret is the HMAC key handed out once.
type mobileDevice struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
	LastSeen  time.Time `json:"last_seen"`
}

type mobileState struct {
	Version int             `json:"version"`
	Devices []*mobileDevice `json:"devices"`
}

// mobileApproval is one "some browser wants into node X" request.
type mobileApproval struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // "node"
	Device     string    `json:"device"`
	DeviceName string    `json:"device_name,omitempty"`
	Host       string    `json:"host"`
	IP         string    `json:"ip"`
	UserAgent  string    `json:"user_agent"`
	Path       string    `json:"path"`
	Status     string    `json:"status"` // pending | approved | denied | expired
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Link       string    `json:"link,omitempty"` // one-time /__omp/grant token
}

// mobileLink is a one-time grant URL token. Direct grants are handed to the
// App (which opens a WebView immediately); approval links are handed to the
// browser page after the phone approves.
type mobileLink struct {
	Token     string
	Node      string
	ExpiresAt time.Time
	Used      bool
}

// mobileAccess is a browser cookie grant for one node.
type mobileAccess struct {
	Node      string
	ExpiresAt time.Time
	Device    string
}

type mobileClient struct {
	conn *websocket.Conn
	send chan []byte
}

type mobileData struct {
	mu sync.Mutex

	devices   map[string]*mobileDevice
	approvals map[string]*mobileApproval
	links     map[string]*mobileLink
	access    map[string]*mobileAccess
	replay    map[string]time.Time
	clients   map[*mobileClient]struct{}

	path           string
	requireConsole bool
	requireForKeys bool
	webTrustSecret string

	upgrader websocket.Upgrader
}

type mobileDeviceCtxKey struct{}

// mobileDeviceFrom returns the phone that signed this request, if any.
func mobileDeviceFrom(ctx context.Context) *mobileDevice {
	if ctx == nil {
		return nil
	}
	dev, _ := ctx.Value(mobileDeviceCtxKey{}).(*mobileDevice)
	return dev
}

// EnableMobile loads enrolled phones and enables the gateway.
func (m *ManagementServer) EnableMobile(opts MobileOptions) error {
	d := &mobileData{
		path:           strings.TrimSpace(opts.StateFile),
		requireConsole: opts.RequireConsole,
		requireForKeys: opts.RequireForKeys,
		webTrustSecret: strings.TrimSpace(opts.WebTrustedSecret),
		devices:        map[string]*mobileDevice{},
		approvals:      map[string]*mobileApproval{},
		links:          map[string]*mobileLink{},
		access:         map[string]*mobileAccess{},
		replay:         map[string]time.Time{},
		clients:        map[*mobileClient]struct{}{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			// Auth happens via the signed request headers, not cookies.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
	if d.path == "" {
		d.path = defaultMobileStateFile()
	}
	if raw, err := os.ReadFile(d.path); err == nil && len(bytes.TrimSpace(raw)) > 0 {
		var st mobileState
		if err := json.Unmarshal(raw, &st); err != nil {
			return fmt.Errorf("mobile: parse %s: %w", d.path, err)
		}
		for _, dev := range st.Devices {
			if dev == nil || !validMobileID(dev.ID) || dev.Secret == "" {
				continue
			}
			d.devices[dev.ID] = dev
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("mobile: read %s: %w", d.path, err)
	}
	m.mobile = d
	slog.Info("mobile: gateway enabled",
		"devices", len(d.devices),
		"require_console", d.requireConsole,
		"require_for_keys", d.requireForKeys,
		"web_trusted_header", d.webTrustSecret != "",
		"state_file", d.path)
	return nil
}

func defaultMobileStateFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "mobile-devices.json"
	}
	return filepath.Join(home, ".omp-lark-bridge", "mobile-devices.json")
}

func validMobileID(id string) bool {
	if !strings.HasPrefix(id, "mob_") || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure means the process cannot be trusted with
		// secrets; abort instead of emitting weak credentials.
		panic("mobile: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func randToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("mobile: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// mobileSign returns the canonical signature for one request.
func mobileSign(secret, canonical string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mobileBodyHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// mobileCanonical builds the exact string both sides sign.
func mobileCanonical(method, requestURI, ts string, body []byte) string {
	return method + "\n" + requestURI + "\n" + ts + "\n" + mobileBodyHash(body)
}

// verifyMobileRequest authenticates a signed phone request. It restores the
// request body so handlers can decode it again.
func (m *ManagementServer) verifyMobileRequest(r *http.Request) (*mobileDevice, bool) {
	d := m.mobile
	if d == nil {
		return nil, false
	}
	id := strings.TrimSpace(r.Header.Get(mobileDeviceHeader))
	tsRaw := strings.TrimSpace(r.Header.Get(mobileTSHeader))
	sig := strings.TrimSpace(r.Header.Get(mobileSigHeader))
	if id == "" || tsRaw == "" || sig == "" {
		return nil, false
	}
	ts, err := strconv.ParseInt(tsRaw, 10, 64)
	if err != nil {
		return nil, false
	}
	now := time.Now()
	delta := now.Sub(time.Unix(ts, 0))
	if delta > mobileClockSkew || delta < -mobileClockSkew {
		return nil, false
	}
	var body []byte
	if r.Body != nil {
		body, err = io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			return nil, false
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	d.mu.Lock()
	dev := d.devices[id]
	d.mu.Unlock()
	if dev == nil {
		return nil, false
	}
	want := mobileSign(dev.Secret, mobileCanonical(r.Method, r.URL.RequestURI(), tsRaw, body))
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return nil, false
	}
	d.mu.Lock()
	replayKey := id + "|" + sig
	if _, seen := d.replay[replayKey]; seen {
		d.mu.Unlock()
		return nil, false
	}
	now = time.Now()
	d.replay[replayKey] = now.Add(2 * mobileClockSkew)
	for k, until := range d.replay {
		if until.Before(now) {
			delete(d.replay, k)
		}
	}
	d.mu.Unlock()
	return dev, true
}

// mobileTouch records activity for the device list.
func (m *ManagementServer) mobileTouch(dev *mobileDevice) {
	if m.mobile == nil || dev == nil {
		return
	}
	now := time.Now()
	m.mobile.mu.Lock()
	if now.Sub(dev.LastSeen) > time.Minute {
		dev.LastSeen = now
	}
	m.mobile.mu.Unlock()
}

func (d *mobileData) deviceCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.devices)
}

// mobileGateActive reports whether node web pages need phone approval.
// Before the first phone enrolls the gate stays open so the app can enroll;
// the management token still guards every API.
func (m *ManagementServer) mobileGateActive() bool {
	d := m.mobile
	if d == nil || !d.requireConsole {
		return false
	}
	return d.deviceCount() > 0
}

// mobileKeyOpsRequired reports whether key mutations need a phone signature.
func (m *ManagementServer) mobileKeyOpsRequired() bool {
	d := m.mobile
	if d == nil || !d.requireForKeys {
		return false
	}
	return d.deviceCount() > 0
}

// mobileKeyGuard wraps handlers that mutate node credentials / pairing.
func (m *ManagementServer) mobileKeyGuard(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			handler(w, r)
			return
		}
		if m.mobileKeyOpsRequired() && mobileDeviceFrom(r.Context()) == nil {
			mgmtError(w, http.StatusForbidden,
				"phone approval required: this is a key operation, sign the request with the OMP mobile app")
			return
		}
		handler(w, r)
	}
}

// mobileAuth authorizes a request with either a phone signature or the
// management token. Signed requests get the device injected into the context.
func (m *ManagementServer) mobileAuth(handler http.HandlerFunc) http.HandlerFunc {
	tokenWrapped := m.wrap(handler)
	return func(w http.ResponseWriter, r *http.Request) {
		m.setCORS(w, r)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if dev, ok := m.verifyMobileRequest(r); ok {
			m.mobileTouch(dev)
			handler(w, r.WithContext(context.WithValue(r.Context(), mobileDeviceCtxKey{}, dev)))
			return
		}
		tokenWrapped(w, r)
	}
}

// wrapKey authorizes like wrap, then requires a phone signature for mutating
// key operations once a phone is enrolled.
func (m *ManagementServer) wrapKey(handler http.HandlerFunc) http.HandlerFunc {
	return m.mobileAuth(m.mobileKeyGuard(handler))
}

// ── persistence ────────────────────────────────────────────────────

func (d *mobileData) saveLocked() {
	st := mobileState{Version: 1, Devices: make([]*mobileDevice, 0, len(d.devices))}
	for _, dev := range d.devices {
		st.Devices = append(st.Devices, dev)
	}
	sort.Slice(st.Devices, func(i, j int) bool { return st.Devices[i].CreatedAt.Before(st.Devices[j].CreatedAt) })
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		slog.Error("mobile: encode state", "error", err)
		return
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(d.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			slog.Error("mobile: create state dir", "error", err)
			return
		}
	}
	tmp, err := os.CreateTemp(dir, ".mobile-devices-*.tmp")
	if err != nil {
		slog.Error("mobile: create state temp", "error", err)
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		slog.Error("mobile: write state", "error", err)
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		slog.Warn("mobile: chmod state", "error", err)
	}
	if err := tmp.Close(); err != nil {
		slog.Error("mobile: close state", "error", err)
		return
	}
	if err := os.Rename(name, d.path); err != nil {
		_ = os.Remove(name)
		slog.Error("mobile: rename state", "error", err)
	}
}

// ── events ─────────────────────────────────────────────────────────

func (m *ManagementServer) mobileBroadcast(event any) {
	d := m.mobile
	if d == nil {
		return
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for c := range d.clients {
		select {
		case c.send <- raw:
		default:
			// Slow consumer: drop this event. Approvals are also pollable.
		}
	}
}

func (d *mobileData) pruneLocked(now time.Time) {
	for id, a := range d.approvals {
		if a.Status == "pending" && now.After(a.ExpiresAt) {
			a.Status = "expired"
		}
		if a.Status != "pending" && now.After(a.ExpiresAt.Add(mobileAccessTTL)) {
			delete(d.approvals, id)
		}
	}
	for token, l := range d.links {
		if now.After(l.ExpiresAt) {
			delete(d.links, token)
		}
	}
	for token, a := range d.access {
		if now.After(a.ExpiresAt) {
			delete(d.access, token)
		}
	}
}

// ── HTTP handlers (center port) ────────────────────────────────────

// handleMobile serves /api/v1/mobile*.
func (m *ManagementServer) handleMobile(w http.ResponseWriter, r *http.Request) {
	if m.mobile == nil {
		mgmtError(w, http.StatusServiceUnavailable, "mobile gateway is not enabled")
		return
	}
	rest := strings.Trim(mobileSubPath(r.URL.Path), "/")
	switch {
	case rest == "" || rest == "status":
		if r.Method != http.MethodGet {
			mgmtError(w, http.StatusMethodNotAllowed, "GET only")
			return
		}
		m.handleMobileStatus(w, r)
	case rest == "enroll":
		m.handleMobileEnroll(w, r)
	case rest == "devices":
		m.handleMobileDevices(w, r)
	case strings.HasPrefix(rest, "devices/") && strings.HasSuffix(rest, "/revoke"):
		m.handleMobileRevoke(w, r)
	case rest == "approvals":
		m.handleMobileApprovals(w, r)
	case strings.HasPrefix(rest, "approvals/"):
		m.handleMobileDecide(w, r)
	case rest == "access/grant":
		m.handleMobileAccessGrant(w, r)
	case rest == "stream":
		m.handleMobileStream(w, r)
	default:
		mgmtError(w, http.StatusNotFound, "unknown mobile endpoint")
	}
}

func mobileSubPath(path string) string {
	const prefix = "/api/v1/mobile"
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	return strings.TrimPrefix(path, prefix)
}

func (m *ManagementServer) handleMobileStatus(w http.ResponseWriter, _ *http.Request) {
	d := m.mobile
	d.mu.Lock()
	pending := 0
	now := time.Now()
	for _, a := range d.approvals {
		if a.Status == "pending" && now.Before(a.ExpiresAt) {
			pending++
		}
	}
	devices := len(d.devices)
	d.mu.Unlock()
	mgmtJSON(w, http.StatusOK, map[string]any{
		"enabled":          true,
		"devices":          devices,
		"pending":          pending,
		"require_console":  d.requireConsole,
		"require_for_keys": d.requireForKeys,
	})
}

func (m *ManagementServer) handleMobileEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	d := m.mobile
	// Enrollment is only reachable after mobileAuth, so the caller either
	// holds a valid management token or is an enrolled phone signing the
	// request. Both are admin-level credentials, so a second phone may enroll
	// from anywhere with the token instead of having to borrow an enrolled
	// phone or reach loopback.
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Android"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	now := time.Now()
	dev := &mobileDevice{
		ID:        "mob_" + randHex(8),
		Name:      name,
		Secret:    base64.RawURLEncoding.EncodeToString(mustRand(32)),
		CreatedAt: now,
		LastSeen:  now,
	}
	d.mu.Lock()
	d.devices[dev.ID] = dev
	d.saveLocked()
	d.mu.Unlock()
	m.mobileBroadcast(map[string]any{"type": "devices"})
	mgmtJSON(w, http.StatusOK, map[string]any{
		"device_id":   dev.ID,
		"device_name": dev.Name,
		"secret":      dev.Secret,
	})
}

func mustRand(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("mobile: crypto/rand failed: " + err.Error())
	}
	return b
}

func (m *ManagementServer) handleMobileDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	d := m.mobile
	current := mobileDeviceFrom(r.Context())
	d.mu.Lock()
	devices := make([]map[string]any, 0, len(d.devices))
	for _, dev := range d.devices {
		devices = append(devices, map[string]any{
			"id":         dev.ID,
			"name":       dev.Name,
			"created_at": dev.CreatedAt,
			"last_seen":  dev.LastSeen,
			"current":    current != nil && current.ID == dev.ID,
		})
	}
	d.mu.Unlock()
	sort.Slice(devices, func(i, j int) bool {
		return fmt.Sprint(devices[i]["created_at"]) < fmt.Sprint(devices[j]["created_at"])
	})
	mgmtJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (m *ManagementServer) handleMobileRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	current := mobileDeviceFrom(r.Context())
	if current == nil {
		mgmtError(w, http.StatusForbidden, "phone signature required")
		return
	}
	rest := strings.Trim(mobileSubPath(r.URL.Path), "/")
	id := strings.TrimSuffix(strings.TrimPrefix(rest, "devices/"), "/revoke")
	id = strings.Trim(id, "/")
	if !validMobileID(id) {
		mgmtError(w, http.StatusBadRequest, "invalid device id")
		return
	}
	d := m.mobile
	d.mu.Lock()
	if _, ok := d.devices[id]; !ok {
		d.mu.Unlock()
		mgmtError(w, http.StatusNotFound, "device not found")
		return
	}
	delete(d.devices, id)
	for token, acc := range d.access {
		if acc.Device == id {
			delete(d.access, token)
		}
	}
	d.saveLocked()
	d.mu.Unlock()
	slog.Info("mobile: device revoked", "device", id, "by", current.ID)
	m.mobileBroadcast(map[string]any{"type": "devices"})
	mgmtJSON(w, http.StatusOK, map[string]any{"revoked": id})
}

func (m *ManagementServer) mobileApprovalView(a *mobileApproval) map[string]any {
	view := map[string]any{
		"id":         a.ID,
		"kind":       a.Kind,
		"device":     a.Device,
		"host":       a.Host,
		"ip":         a.IP,
		"user_agent": a.UserAgent,
		"path":       a.Path,
		"status":     a.Status,
		"created_at": a.CreatedAt,
		"expires_at": a.ExpiresAt,
	}
	if name := m.hubNodeName(a.Device); name != "" {
		view["device_name"] = name
	}
	return view
}

func (m *ManagementServer) hubNodeName(token string) string {
	h := m.hub
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if rec := h.paired[token]; rec != nil && rec.Name != "" {
		return rec.Name
	}
	if dev := h.devices[token]; dev != nil && dev.Name != "" {
		return dev.Name
	}
	return ""
}

func (m *ManagementServer) handleMobileApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	d := m.mobile
	now := time.Now()
	d.mu.Lock()
	d.pruneLocked(now)
	list := make([]*mobileApproval, 0, len(d.approvals))
	for _, a := range d.approvals {
		if status == "" || status == "all" || a.Status == status {
			list = append(list, a)
		}
	}
	d.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	views := make([]map[string]any, 0, len(list))
	for _, a := range list {
		views = append(views, m.mobileApprovalView(a))
	}
	mgmtJSON(w, http.StatusOK, map[string]any{"approvals": views})
}

func (m *ManagementServer) handleMobileDecide(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	current := mobileDeviceFrom(r.Context())
	if current == nil {
		mgmtError(w, http.StatusForbidden, "phone signature required")
		return
	}
	rest := strings.Trim(mobileSubPath(r.URL.Path), "/")
	id := strings.Trim(strings.TrimPrefix(rest, "approvals/"), "/")
	var body struct {
		Approve *bool `json:"approve"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Approve == nil {
		mgmtError(w, http.StatusBadRequest, "approve is required")
		return
	}
	d := m.mobile
	now := time.Now()
	d.mu.Lock()
	a := d.approvals[id]
	if a == nil {
		d.mu.Unlock()
		mgmtError(w, http.StatusNotFound, "approval not found")
		return
	}
	if a.Status != "pending" {
		status := a.Status
		link := a.Link
		d.mu.Unlock()
		mgmtJSON(w, http.StatusOK, map[string]any{"status": status, "link": m.mobileLinkURL(link)})
		return
	}
	if now.After(a.ExpiresAt) {
		a.Status = "expired"
		d.mu.Unlock()
		mgmtError(w, http.StatusGone, "approval expired")
		return
	}
	link := ""
	if *body.Approve {
		token := randToken()
		d.links[token] = &mobileLink{Token: token, Node: a.Device, ExpiresAt: now.Add(mobileLinkTTL)}
		a.Status = "approved"
		a.Link = token
		link = token
	} else {
		a.Status = "denied"
	}
	d.mu.Unlock()
	slog.Info("mobile: approval decided", "approval", id, "status", a.Status, "device", a.Device, "by", current.ID)
	m.mobileBroadcast(map[string]any{"type": "approval_decided", "id": id, "status": a.Status})
	mgmtJSON(w, http.StatusOK, map[string]any{"status": a.Status, "link": m.mobileLinkURL(link)})
}

func (m *ManagementServer) mobileLinkURL(token string) string {
	if token == "" {
		return ""
	}
	return "/__omp/grant?t=" + token
}

func (m *ManagementServer) handleMobileAccessGrant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		mgmtError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	current := mobileDeviceFrom(r.Context())
	if current == nil {
		mgmtError(w, http.StatusForbidden, "phone signature required")
		return
	}
	var body struct {
		Device string `json:"device"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&body); err != nil {
		mgmtError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	token := strings.TrimSpace(body.Device)
	host, nodeToken := "", ""
	if token == mobileConsoleNode {
		// The console is not a paired node; its public origin is whatever
		// origin the app reached us on (the app talks to the console directly).
		host, nodeToken = mobileConsoleOrigin(r), mobileConsoleNode
	} else {
		host, nodeToken = m.mobileNodeHost(token)
	}
	if host == "" || nodeToken == "" {
		mgmtError(w, http.StatusNotFound, "node not found")
		return
	}
	now := time.Now()
	link := randToken()
	d := m.mobile
	d.mu.Lock()
	d.links[link] = &mobileLink{Token: link, Node: nodeToken, ExpiresAt: now.Add(mobileGrantTTL)}
	d.mu.Unlock()
	mgmtJSON(w, http.StatusOK, map[string]any{
		"url":        host + "/__omp/grant?t=" + link,
		"expires_at": now.Add(mobileGrantTTL),
		"device":     nodeToken,
	})
}

// mobileConsoleOrigin reconstructs the origin the app used to reach the console,
// so a grant link for the management UI works behind the tunnel (https via
// X-Forwarded-Proto) as well as on the LAN (plain http).
func mobileConsoleOrigin(r *http.Request) string {
	if r == nil || strings.TrimSpace(r.Host) == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") ||
		strings.EqualFold(r.Header.Get("X-Forwarded-Scheme"), "https") {
		scheme = "https"
	}
	return scheme + "://" + strings.TrimSpace(r.Host)
}

// mobileNodeHost maps a node token to (public host, token). Host comes from the
// paired record, falling back to the configured omp-<token>.<suffix> scheme.
func (m *ManagementServer) mobileNodeHost(token string) (string, string) {
	h := m.hub
	if h == nil || token == "" {
		return "", ""
	}
	h.mu.Lock()
	rec := h.paired[token]
	host, webURL, name := "", "", ""
	if rec != nil {
		host = rec.Host
		webURL = rec.WebURL
		name = rec.Name
	}
	if host == "" {
		if dev := h.devices[token]; dev != nil {
			host = dev.Address
		}
	}
	h.mu.Unlock()
	if rec == nil {
		return "", ""
	}
	if host == "" {
		host = m.defaultNodeHost(token)
	}
	if base := strings.TrimRight(strings.TrimSpace(webURL), "/"); base != "" {
		return base, token
	}
	if host == "" {
		slog.Warn("mobile: node has no host", "device", token, "name", name)
	}
	return mobileNodeBaseURL(host), token
}

// mobileNodeBaseURL guesses the public scheme for a node host. Cloudflare
// proxied DNS names (and tailnet names) are reached over https; raw IPs and
// localhost are plain http.
func mobileNodeBaseURL(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	bare := hostWithoutPort(host)
	if ip := net.ParseIP(bare); ip != nil {
		return "http://" + host
	}
	if strings.EqualFold(bare, "localhost") {
		return "http://" + host
	}
	return "https://" + host
}

// handleMobileStream upgrades a signed request to the event stream.
func (m *ManagementServer) handleMobileStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		mgmtError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}
	d := m.mobile
	if mobileDeviceFrom(r.Context()) == nil {
		mgmtError(w, http.StatusForbidden, "phone signature required")
		return
	}
	conn, err := d.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote an HTTP error.
		slog.Warn("mobile: stream upgrade failed", "error", err)
		return
	}
	client := &mobileClient{conn: conn, send: make(chan []byte, 32)}
	d.mu.Lock()
	d.clients[client] = struct{}{}
	d.mu.Unlock()

	go func() {
		hello, _ := json.Marshal(map[string]any{"type": "hello", "runtime": "omp-mobile"})
		client.send <- hello
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case raw, ok := <-client.send:
				if !ok {
					_ = conn.WriteMessage(websocket.CloseMessage, []byte{})
					return
				}
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	conn.SetReadLimit(8 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	}
	d.mu.Lock()
	delete(d.clients, client)
	close(client.send)
	d.mu.Unlock()
	_ = conn.Close()
}

// ── node-origin endpoints (relay) ──────────────────────────────────

// mobileConsoleGate puts the same phone approval in front of the management
// console. Without it, restoring the public console hostname would hand the
// whole node/key API to anyone holding the management token; with it, a browser
// has to be approved once on the phone and then carries the shared access
// cookie (scoped to mobileConsoleNode) for its lifetime.
//
// The app's own API is not behind this gate: it signs every request, and
// blocking it would also block the approval list that the phone needs in order
// to approve anything.
func (m *ManagementServer) mobileConsoleGate(next http.Handler) http.Handler {
	if m.mobile == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.mobileGateActive() {
			next.ServeHTTP(w, r)
			return
		}
		host := requestHost(r)
		if m.mobileServeRelayEndpoint(w, r, mobileConsoleNode, host) {
			return
		}
		switch r.Method {
		case http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if mobileMachineEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if m.mobileAccessAllowed(r, mobileConsoleNode) {
			next.ServeHTTP(w, r)
			return
		}
		m.serveMobileGate(w, r, mobileConsoleNode, host)
	})
}

// mobileMachineEndpoint reports whether path belongs to a protocol spoken by
// other machines rather than by a browser: the phone's signed API and the hub
// node tunnel/pairing handshake. Those carry their own credentials, so putting
// the console gate in front of them would only break reconnects and would add a
// pending approval every retry.
func mobileMachineEndpoint(path string) bool {
	switch path {
	case "/api/v1/hub/tunnel", "/api/v1/hub/pair/claim":
		return true
	}
	return path == "/api/v1/mobile" || strings.HasPrefix(path, "/api/v1/mobile/")
}

// requestHost returns the request host without its port.
func requestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(strings.TrimSuffix(host, "."))
}

// mobileServeRelayEndpoint handles the two paths the relay answers itself:
// the one-time grant link and the approval status poll used by the gate page.
// It returns true when the request was handled.
func (m *ManagementServer) mobileServeRelayEndpoint(w http.ResponseWriter, r *http.Request, token, host string) bool {
	if m.mobile == nil {
		return false
	}
	switch r.URL.Path {
	case "/__omp/grant":
		m.serveMobileGrant(w, r, token)
		return true
	case "/api/v1/mobile/approvals/status":
		m.serveMobileApprovalStatus(w, r, token)
		return true
	}
	return false
}

func (m *ManagementServer) serveMobileGrant(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	linkToken := strings.TrimSpace(r.URL.Query().Get("t"))
	now := time.Now()
	d := m.mobile
	d.mu.Lock()
	link := d.links[linkToken]
	if link == nil || link.Node != token {
		d.mu.Unlock()
		m.writeMobilePage(w, http.StatusForbidden, "链接无效", "该访问链接无效，请回到手机 App 重新打开节点。", "")
		return
	}
	if link.Used {
		d.mu.Unlock()
		m.writeMobilePage(w, http.StatusGone, "链接已使用", "该访问链接已使用过，请回到手机 App 重新打开节点。", "")
		return
	}
	if now.After(link.ExpiresAt) {
		d.mu.Unlock()
		m.writeMobilePage(w, http.StatusGone, "链接已过期", "该访问链接已过期，请回到手机 App 重新打开节点。", "")
		return
	}
	link.Used = true
	accessToken := randToken()
	d.access[accessToken] = &mobileAccess{Node: token, ExpiresAt: now.Add(mobileAccessTTL)}
	d.mu.Unlock()

	cookiePath := "/"
	http.SetCookie(w, &http.Cookie{
		Name:     mobileAccessCookie,
		Value:    accessToken,
		Path:     cookiePath,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(mobileAccessTTL / time.Second),
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func requestIsHTTPS(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func (m *ManagementServer) serveMobileApprovalStatus(w http.ResponseWriter, r *http.Request, token string) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("nonce"))
	d := m.mobile
	now := time.Now()
	d.mu.Lock()
	a := d.approvals[id]
	if a == nil || a.Device != token {
		d.mu.Unlock()
		http.Error(w, `{"ok":false,"error":"not found"}`, http.StatusNotFound)
		return
	}
	if a.Status == "pending" && now.After(a.ExpiresAt) {
		a.Status = "expired"
	}
	status, link := a.Status, a.Link
	d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"status": status,
		"link":   m.mobileLinkURL(link),
	})
}

// mobileWebTrustedHeader is the header ompweb reads from a trusted proxy
// (see OMP_WEB_TRUSTED_HEADER_SHA256 in its README).
const mobileWebTrustedHeader = "X-Omp-Web-Auth"

// applyMobileWebTrust strips any client-claimed trust header and, when a
// secret is configured and the request carries a valid phone-approved access
// cookie, vouches for it to the node's ompweb. The phone approval therefore
// replaces the workspace password; direct access to ompweb still needs it.
func (m *ManagementServer) applyMobileWebTrust(r *http.Request, token string) {
	if r == nil {
		return
	}
	r.Header.Del(mobileWebTrustedHeader)
	d := m.mobile
	if d == nil || d.webTrustSecret == "" || token == "" {
		return
	}
	if !m.mobileAccessAllowed(r, token) {
		return
	}
	r.Header.Set(mobileWebTrustedHeader, d.webTrustSecret)
}

// mobileAccessAllowed checks the browser cookie grant for a node.
func (m *ManagementServer) mobileAccessAllowed(r *http.Request, token string) bool {
	d := m.mobile
	if d == nil {
		return false
	}
	c, err := r.Cookie(mobileAccessCookie)
	if err != nil || c.Value == "" {
		return false
	}
	now := time.Now()
	d.mu.Lock()
	acc := d.access[c.Value]
	ok := acc != nil && acc.Node == token && now.Before(acc.ExpiresAt)
	d.mu.Unlock()
	return ok
}

// mobileRelayToken resolves the node a relay request belongs to when the Host
// header is not a paired host name. That happens when one front door (for
// example https://home-ubuntu.tailxxxx.ts.net) fronts every node instead of
// giving each node its own DNS name: the grant link names the node, the access
// cookie remembers it, and the gate page's status poll names it by nonce.
// Host-based routing remains the primary path; this is only the fallback.
func (m *ManagementServer) mobileRelayToken(r *http.Request) string {
	d := m.mobile
	if d == nil || r == nil {
		return ""
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, err := r.Cookie(mobileAccessCookie); err == nil && c.Value != "" {
		if acc := d.access[c.Value]; acc != nil && now.Before(acc.ExpiresAt) {
			return acc.Node
		}
	}
	switch r.URL.Path {
	case "/__omp/grant":
		if link := d.links[strings.TrimSpace(r.URL.Query().Get("t"))]; link != nil && now.Before(link.ExpiresAt) {
			return link.Node
		}
	case "/api/v1/mobile/approvals/status":
		if a := d.approvals[strings.TrimSpace(r.URL.Query().Get("nonce"))]; a != nil {
			return a.Device
		}
	}
	return ""
}

// serveMobileGate renders the approval page and asks the phone.
func (m *ManagementServer) serveMobileGate(w http.ResponseWriter, r *http.Request, token, host string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET")
		http.Error(w, "phone approval required", http.StatusUnauthorized)
		return
	}
	d := m.mobile
	if d.deviceCount() == 0 {
		http.Error(w, "phone approval required but no phone is enrolled", http.StatusUnauthorized)
		return
	}
	now := time.Now()
	kind := "node"
	pageTitle := host
	target := "节点 <span class=\"mono\">" + htmlEscape(host) + "</span>"
	deviceName := m.hubNodeName(token)
	if token == mobileConsoleNode {
		kind, pageTitle, target, deviceName = "console", "OMP 控制台", "OMP 控制台", "OMP 控制台"
	}
	approval := &mobileApproval{
		ID:         "apr_" + randHex(8),
		Kind:       kind,
		Device:     token,
		Host:       host,
		IP:         requestIP(r),
		UserAgent:  r.UserAgent(),
		Path:       r.URL.Path,
		Status:     "pending",
		CreatedAt:  now,
		ExpiresAt:  now.Add(mobileApprovalTTL),
		DeviceName: deviceName,
	}
	d.mu.Lock()
	d.pruneLocked(now)
	d.approvals[approval.ID] = approval
	d.mu.Unlock()
	m.mobileBroadcast(map[string]any{"type": "approval", "approval": m.mobileApprovalView(approval)})
	slog.Info("mobile: approval requested", "id", approval.ID, "node", token, "host", host, "ip", approval.IP)

	page := fmt.Sprintf(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>等待手机确认 · %s</title>
<style>
:root{color-scheme:dark}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
background:#0f1115;color:#e6e6e6;font:16px/1.6 -apple-system,"Segoe UI",system-ui,sans-serif}
.card{width:min(420px,90vw);background:#181b22;border:1px solid #2a2f3a;border-radius:16px;padding:28px}
h1{margin:0 0 6px;font-size:20px}
p{margin:10px 0;color:#a6adbb}
.mono{font-family:ui-monospace,Consolas,monospace;color:#e6e6e6}
.spinner{width:22px;height:22px;border:2px solid #2f3a4a;border-top-color:#4f8cff;
border-radius:50%%;animation:spin 1s linear infinite;display:inline-block;vertical-align:-4px;margin-right:8px}
@keyframes spin{to{transform:rotate(360deg)}}
.bad{color:#ff6b6b}.good{color:#3ecf8e}
</style></head>
<body><div class="card">
<h1>等待手机确认</h1>
<p>%s 的访问请求已发送到 OMP App。</p>
<p>来源 <span class="mono">%s</span> · 有效期 <span id="left">%d 秒</span></p>
<p id="state"><span class="spinner"></span>等待手机确认…</p>
<p><small>如果手机没有收到通知，请打开 App 的「审批」页面。</small></p>
</div>
<script>
(function(){
  var nonce = %q;
  var left = %d;
  var state = document.getElementById('state');
  var leftEl = document.getElementById('left');
  var timer = setInterval(function(){ if(left>0){left--;leftEl.textContent=left+' 秒';} },1000);
  function fail(msg){clearInterval(timer);state.className='bad';state.textContent=msg;}
  function poll(){
    fetch('/api/v1/mobile/approvals/status?nonce='+encodeURIComponent(nonce),{cache:'no-store'})
      .then(function(r){return r.json();})
      .then(function(j){
        if(!j.ok){fail('请求已失效，请刷新页面重试');return;}
        if(j.status==='approved' && j.link){clearInterval(timer);state.className='good';state.textContent='已确认，正在进入…';location.replace(j.link);return;}
        if(j.status==='denied'){fail('手机拒绝了本次访问');return;}
        if(j.status==='expired'){fail('确认超时，请刷新页面重试');return;}
        setTimeout(poll,1500);
      })
      .catch(function(){setTimeout(poll,2500);});
  }
  setTimeout(poll,800);
})();
</script>
</body></html>`, htmlEscape(pageTitle), target, htmlEscape(requestIP(r)), int(mobileApprovalTTL/time.Second), approval.ID, int(mobileApprovalTTL/time.Second))
	m.writeMobileHTML(w, http.StatusUnauthorized, page)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

func (m *ManagementServer) writeMobilePage(w http.ResponseWriter, status int, title, body, extra string) {
	page := "<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\">" +
		"<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>" + htmlEscape(title) + "</title>" +
		"<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;" +
		"background:#0f1115;color:#e6e6e6;font:16px/1.6 -apple-system,\"Segoe UI\",system-ui,sans-serif}" +
		".card{width:min(420px,90vw);background:#181b22;border:1px solid #2a2f3a;border-radius:16px;padding:28px}" +
		"h1{margin:0 0 8px;font-size:20px}p{margin:8px 0;color:#a6adbb}</style></head><body><div class=\"card\">" +
		"<h1>" + htmlEscape(title) + "</h1><p>" + htmlEscape(body) + "</p>" + extra + "</div></body></html>"
	m.writeMobileHTML(w, status, page)
}

func (m *ManagementServer) writeMobileHTML(w http.ResponseWriter, status int, page string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, page)
}
