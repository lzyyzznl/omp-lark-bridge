package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func postJSON(t *testing.T, handler http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestHubPair_OneTimeCodeAndPersistedDevice(t *testing.T) {
	stateFile := t.TempDir() + "/hub-devices.json"
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{StateFile: stateFile, RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()

	rec := postJSON(t, m.handleHubPairNew, "/api/v1/hub/pair/new", map[string]string{
		"device_token": "dev1",
		"name":         "Node One",
		"host":         "omp-test.example",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("pair/new status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode pair/new: %v", err)
	}
	if created.Data.Code == "" {
		t.Fatal("empty pairing code")
	}

	rec = postJSON(t, m.handleHubPairClaim, "/api/v1/hub/pair/claim", map[string]string{
		"code": created.Data.Code,
		"name": "Node One",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", rec.Code, rec.Body.String())
	}
	var claimed struct {
		Data struct {
			DeviceToken string `json:"device_token"`
			Secret      string `json:"secret"`
			WebURL      string `json:"web_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimed.Data.DeviceToken != "dev1" || len(claimed.Data.Secret) != 64 || claimed.Data.Secret == "s3cr3t" || claimed.Data.WebURL != "https://omp-test.example" {
		t.Fatalf("unexpected claim: %+v", claimed.Data)
	}

	rec = postJSON(t, m.handleHubPairClaim, "/api/v1/hub/pair/claim", map[string]string{"code": created.Data.Code})
	if rec.Code == http.StatusOK {
		t.Fatal("pairing code was accepted twice")
	}

	b, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if !strings.Contains(string(b), `"dev1"`) || !strings.Contains(string(b), "omp-test.example") {
		t.Fatalf("state file missing device: %s", b)
	}
}

func pairTestDevice(t *testing.T, m *ManagementServer, device, host string) string {
	t.Helper()
	rec := postJSON(t, m.handleHubPairNew, "/api/v1/hub/pair/new", map[string]string{
		"device_token": device,
		"name":         device,
		"host":         host,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("pair/new status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode pair/new: %v", err)
	}
	rec = postJSON(t, m.handleHubPairClaim, "/api/v1/hub/pair/claim", map[string]string{"code": created.Data.Code})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", rec.Code, rec.Body.String())
	}
	var claimed struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claimed.Data.Secret == "" {
		t.Fatal("claim returned empty secret")
	}
	return claimed.Data.Secret
}

func TestHubRelay_WebSocketUpgrade(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	defer backend.Close()

	relayPort := freeTCPPort(t)
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{
		StateFile: t.TempDir() + "/hub-devices.json",
		RelayPort: relayPort,
	}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	deviceSecret := pairTestDevice(t, m, "devws", "127.0.0.1")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hub/tunnel", m.handleHubTunnel)
	tunnelSrv := httptest.NewServer(mux)
	defer tunnelSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(tunnelSrv.URL, "http") + "/api/v1/hub/tunnel"
	header := http.Header{}
	header.Set("X-Hub-Secret", deviceSecret)
	header.Set("X-Hub-Device", "devws")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	session, err := yamux.Server(newWSNetConn(conn), hubYamuxConfig())
	if err != nil {
		t.Fatalf("yamux server: %v", err)
	}
	defer session.Close()
	go func() {
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return
			}
			go proxyHubStream(stream, backend.Listener.Addr().String())
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for m.hubTunnelFor("devws") == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.hubTunnelFor("devws") == nil {
		t.Fatal("tunnel was not registered")
	}

	client, _, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(relayPort)+"/", nil)
	if err != nil {
		t.Fatalf("dial relay websocket: %v", err)
	}
	defer client.Close()
	if err := client.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, msg, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(msg) != "ping" {
		t.Fatalf("echo=%q, want ping", msg)
	}
}

func TestHubPair_RemoveDevice(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{
		StateFile: t.TempDir() + "/hub-devices.json",
		RelayPort: -1,
	}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	_ = pairTestDevice(t, m, "devrm", "omp-rm.example")
	if _, ok := m.hub.paired["devrm"]; !ok {
		t.Fatal("device was not paired")
	}

	rec := httptest.NewRecorder()
	m.handleHubPairDevice(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/hub/pair/devrm", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("remove status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := m.hub.paired["devrm"]; ok {
		t.Fatal("device still paired after remove")
	}
	if _, ok := m.hub.hostIndex["omp-rm.example"]; ok {
		t.Fatal("host index still points at removed device")
	}

	rec = httptest.NewRecorder()
	m.handleHubNodes(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hub/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Nodes []HubNodeView `json:"nodes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode nodes: %v", err)
	}
	if len(out.Data.Nodes) != 0 {
		t.Fatalf("removed device still listed: %+v", out.Data.Nodes)
	}

	rec = httptest.NewRecorder()
	m.handleHubPairDevice(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/hub/pair/devrm", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second remove status=%d, want 404", rec.Code)
	}
}

func TestHubPair_AutoHost(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{
		StateFile:      t.TempDir() + "/hub-devices.json",
		RelayPort:      -1,
		NodeHostSuffix: "example.com",
	}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	rec := postJSON(t, m.handleHubPairNew, "/api/v1/hub/pair/new", map[string]string{
		"device_token": "devauto",
		"name":         "Auto",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("pair/new status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Host   string `json:"host"`
			WebURL string `json:"web_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Data.Host != "omp-devauto.example.com" || out.Data.WebURL != "https://omp-devauto.example.com" {
		t.Fatalf("unexpected auto host: %+v", out.Data)
	}
}

func TestHubTunnel_RelayHTTPAndOffline(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello from node")
	}))
	defer backend.Close()
	backendAddr := backend.Listener.Addr().String()

	relayPort := freeTCPPort(t)
	stateFile := t.TempDir() + "/hub-devices.json"
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("s3cr3t", true, HubClientOptions{StateFile: stateFile, RelayPort: relayPort}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()

	rec := postJSON(t, m.handleHubPairNew, "/api/v1/hub/pair/new", map[string]string{
		"device_token": "dev1",
		"name":         "Node One",
		"host":         "omp-test.example",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("pair/new status=%d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	rec = postJSON(t, m.handleHubPairClaim, "/api/v1/hub/pair/claim", map[string]string{"code": created.Data.Code})
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status=%d body=%s", rec.Code, rec.Body.String())
	}
	var claimed struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &claimed); err != nil || claimed.Data.Secret == "" {
		t.Fatalf("claim decode: %v body=%s", err, rec.Body.String())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hub/tunnel", m.handleHubTunnel)
	tunnelSrv := httptest.NewServer(mux)
	defer tunnelSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tunnelSrv.URL, "http") + "/api/v1/hub/tunnel"
	header := http.Header{}
	header.Set("X-Hub-Secret", claimed.Data.Secret)
	header.Set("X-Hub-Device", "dev1")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		t.Fatalf("dial tunnel: %v", err)
	}
	session, err := yamux.Server(newWSNetConn(conn), hubYamuxConfig())
	if err != nil {
		t.Fatalf("yamux server: %v", err)
	}
	defer session.Close()
	go func() {
		for {
			stream, err := session.AcceptStream()
			if err != nil {
				return
			}
			go proxyHubStream(stream, backendAddr)
		}
	}()

	deadline := time.Now().Add(2 * time.Second)
	for m.hubTunnelFor("dev1") == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if m.hubTunnelFor("dev1") == nil {
		t.Fatal("tunnel was not registered")
	}

	relayURL := "http://127.0.0.1:" + strconv.Itoa(relayPort) + "/"
	req, _ := http.NewRequest(http.MethodGet, relayURL, nil)
	req.Host = "omp-test.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("relay request: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "hello from node" {
		t.Fatalf("relay response status=%d body=%q", resp.StatusCode, body)
	}

	_ = session.Close()
	deadline = time.Now().Add(2 * time.Second)
	for m.hubTunnelFor("dev1") != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	req, _ = http.NewRequest(http.MethodGet, relayURL, nil)
	req.Host = "omp-test.example"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("offline relay request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("offline relay status=%d, want 502", resp.StatusCode)
	}
}

// A fresh node has no credentials yet: it must start in a pairing-only state,
// never accept an empty X-Hub-Secret as authentication, and report "unpaired".
func TestHubClient_StartsAwaitingPairing(t *testing.T) {
	stateFile := t.TempDir() + "/hub-client.json"
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("", false, HubClientOptions{ServerURL: "https://center.example", StateFile: stateFile}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	if m.hub == nil || m.hub.serverMode {
		t.Fatalf("hub = %+v, want client mode", m.hub)
	}
	if m.hub.clientToken != "" || m.hub.secret != "" {
		t.Fatalf("unpaired node holds credentials: token=%q", m.hub.clientToken)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/hub/register", strings.NewReader(`{"device_token":"x"}`))
	m.handleHubRegister(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("register without secret status=%d, want 401", rec.Code)
	}
	st := m.hubStatus()
	if st["mode"] != "client" || st["paired"] != false {
		t.Fatalf("status = %+v, want unpaired client", st)
	}
}

// fakePairCenter serves the center's claim endpoint and reports the payloads it
// received so tests can assert what the node sent.
func fakePairCenter(t *testing.T, status int, data map[string]any) (*httptest.Server, *[]map[string]string) {
	t.Helper()
	var seen []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/hub/pair/claim" {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body)
		seen = append(seen, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": data})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid or expired pairing code"})
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestHubClient_JoinFromConsole(t *testing.T) {
	t.Setenv("OMPWEB_PUBLIC_URL", "https://preconfigured.example")
	center, seen := fakePairCenter(t, http.StatusOK, map[string]any{
		"device_token": "dev-node",
		"name":         "Node From Center",
		"secret":       "shared-secret",
		"web_url":      "https://omp-node.example/",
	})
	stateFile := t.TempDir() + "/hub-client.json"
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("", false, HubClientOptions{ServerURL: center.URL, StateFile: stateFile}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()

	rec := postJSON(t, m.handleHubPairJoin, "/api/v1/hub/pair/join", map[string]string{"code": " abcd2345 ", "name": "typed name"})
	if rec.Code != http.StatusOK {
		t.Fatalf("join status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data struct {
			Applied         bool   `json:"applied"`
			RestartRequired bool   `json:"restart_required"`
			DeviceToken     string `json:"device_token"`
			WebURL          string `json:"web_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if !got.Data.Applied || got.Data.RestartRequired || got.Data.DeviceToken != "dev-node" {
		t.Fatalf("join data = %+v, want applied", got.Data)
	}
	if got.Data.WebURL != "https://omp-node.example" {
		t.Fatalf("web_url = %q, want trailing slash trimmed", got.Data.WebURL)
	}
	sent := (*seen)[0]
	if sent["code"] != "ABCD2345" || sent["name"] != "typed name" {
		t.Fatalf("claim payload = %v, want normalized code and typed name", sent)
	}

	b, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("state file: %v", err)
	}
	var st hubClientState
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("state decode: %v", err)
	}
	if st.Secret != "shared-secret" || st.DeviceToken != "dev-node" || st.ServerURL != center.URL || st.DeviceName != "Node From Center" {
		t.Fatalf("state = %+v", st)
	}
	// Windows ignores Unix permission bits; the 0600 intent only exists on POSIX.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(stateFile); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("state perms = %v, err=%v, want 0600", info.Mode().Perm(), err)
		}
	}
	m.hub.mu.Lock()
	token, secret, web := m.hub.clientToken, m.hub.secret, m.hub.webURL
	m.hub.mu.Unlock()
	if token != "dev-node" || secret != "shared-secret" || web != "https://omp-node.example" {
		t.Fatalf("live hub = token %q secret %q web %q", token, secret, web)
	}

	// Re-pairing a running node persists the new credentials but cannot swap
	// the secret the tunnel loop already holds: it must ask for a restart.
	center2, _ := fakePairCenter(t, http.StatusOK, map[string]any{
		"device_token": "dev-node2",
		"secret":       "new-secret",
		"web_url":      "https://omp-node2.example",
	})
	m.hub.mu.Lock()
	m.hub.serverURL = center2.URL
	m.hub.mu.Unlock()
	rec = postJSON(t, m.handleHubPairJoin, "/api/v1/hub/pair/join", map[string]string{"code": "EFGH2345"})
	if rec.Code != http.StatusOK {
		t.Fatalf("re-join status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.Applied || !got.Data.RestartRequired {
		t.Fatalf("re-join data = %+v, want restart_required", got.Data)
	}
	m.hub.mu.Lock()
	token = m.hub.clientToken
	m.hub.mu.Unlock()
	if token != "dev-node" {
		t.Fatalf("live token = %q, want old credentials until restart", token)
	}
	b, _ = os.ReadFile(stateFile)
	_ = json.Unmarshal(b, &st)
	if st.DeviceToken != "dev-node2" || st.Secret != "new-secret" {
		t.Fatalf("state after re-pair = %+v", st)
	}
}

func TestHubClient_JoinFailures(t *testing.T) {
	stateFile := t.TempDir() + "/hub-client.json"

	// Server-mode consoles issue codes; they must not pair themselves.
	center := NewManagementServer(0, "mgmt", nil)
	if err := center.EnableHub("sec", true, HubClientOptions{RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub(server): %v", err)
	}
	defer center.Stop()
	rec := postJSON(t, center.handleHubPairJoin, "/api/v1/hub/pair/join", map[string]string{"code": "ABCD2345"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("server-mode join status=%d, want 409", rec.Code)
	}

	// A rejected code surfaces the center's error and writes no state.
	badCenter, _ := fakePairCenter(t, http.StatusBadRequest, nil)
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("", false, HubClientOptions{ServerURL: badCenter.URL, StateFile: stateFile}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	rec = postJSON(t, m.handleHubPairJoin, "/api/v1/hub/pair/join", map[string]string{"code": "ABCD2345"})
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "invalid or expired pairing code") {
		t.Fatalf("join status=%d body=%s, want 502 with center error", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("state file written after failed join: err=%v", err)
	}
	rec = postJSON(t, m.handleHubPairJoin, "/api/v1/hub/pair/join", map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty code status=%d, want 400", rec.Code)
	}
}

func TestHubNodes_ClientReadOnlyDirectory(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("", false, HubClientOptions{
		ServerURL: "http://127.0.0.1:1",
		StateFile: t.TempDir() + "/hub-client.json",
	}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	m.hub.mu.Lock()
	m.hub.remoteNodes = []HubNodeView{
		{ID: "node-a", Name: "Node A", WebURL: "https://omp-a.example", Online: true, SecretSet: true},
	}
	m.hub.mu.Unlock()

	rec := httptest.NewRecorder()
	m.handleHubNodes(rec, httptest.NewRequest(http.MethodGet, "/api/v1/hub/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("client nodes status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data struct {
			Nodes   []HubNodeView `json:"nodes"`
			Role    string        `json:"role"`
			CanEdit bool          `json:"can_edit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.Role != "client" || got.Data.CanEdit {
		t.Fatalf("role=%q can_edit=%v, want client/false", got.Data.Role, got.Data.CanEdit)
	}
	if len(got.Data.Nodes) != 1 || got.Data.Nodes[0].ID != "node-a" {
		t.Fatalf("client nodes = %+v", got.Data.Nodes)
	}
}

func patchJSON(t *testing.T, handler http.HandlerFunc, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func TestHubNodeUpdate_CenterEditsNameSecretAndLoginCode(t *testing.T) {
	stateFile := t.TempDir() + "/hub-devices.json"
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("global-secret", true, HubClientOptions{StateFile: stateFile, RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	oldSecret := pairTestDevice(t, m, "devupd", "omp-upd.example")

	rec := patchJSON(t, m.handleHubNodeUpdate, "/api/v1/hub/nodes/devupd", map[string]any{
		"name":       "新名字",
		"secret":     "new-secret-0123456789",
		"login_code": "code-1234",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Data struct {
			Updated       bool `json:"updated"`
			SecretRotated bool `json:"secret_rotated"`
			LoginCodeSet  bool `json:"login_code_set"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if !got.Data.Updated || !got.Data.SecretRotated || !got.Data.LoginCodeSet {
		t.Fatalf("update data = %+v", got.Data)
	}
	m.hub.mu.Lock()
	rec2 := m.hub.paired["devupd"]
	view := m.hubNodeViewsLocked()
	m.hub.mu.Unlock()
	if rec2.Name != "新名字" || rec2.Secret != "new-secret-0123456789" || rec2.PrevSecret != oldSecret {
		t.Fatalf("record = %+v", rec2)
	}
	if len(view) != 1 || view[0].Name != "新名字" || !view[0].SecretSet || !view[0].LoginCodeSet {
		t.Fatalf("view = %+v", view)
	}

	// The old secret keeps working for sync/heartbeat while the node catches up.
	body, _ := json.Marshal(map[string]any{"device_token": "devupd"})
	registerReq := httptest.NewRequest(http.MethodPost, "/api/v1/hub/register", bytes.NewReader(body))
	registerReq.Header.Set("Content-Type", "application/json")
	registerReq.Header.Set("X-Hub-Secret", oldSecret)
	registerRec := httptest.NewRecorder()
	m.handleHubRegister(registerRec, registerReq)
	if registerRec.Code != http.StatusOK {
		t.Fatalf("register with old secret status=%d body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"secret":"new-secret-0123456789"`) {
		t.Fatalf("register response missing new secret: %s", registerRec.Body.String())
	}

	// Login gate accepts the configured code and rejects missing/wrong codes.
	authReq := httptest.NewRequest(http.MethodGet, "/", nil)
	authReq.SetBasicAuth("node", "code-1234")
	if !m.hubLoginAllowed(authReq, "devupd") {
		t.Fatal("valid login code rejected")
	}
	if m.hubLoginAllowed(httptest.NewRequest(http.MethodGet, "/", nil), "devupd") {
		t.Fatal("login gate allowed request without credentials")
	}
	badReq := httptest.NewRequest(http.MethodGet, "/", nil)
	badReq.SetBasicAuth("node", "wrong")
	if m.hubLoginAllowed(badReq, "devupd") {
		t.Fatal("login gate allowed wrong code")
	}
}

func TestHubNodeUpdate_ClientIsReadOnly(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("", false, HubClientOptions{
		ServerURL: "http://127.0.0.1:1",
		StateFile: t.TempDir() + "/hub-client.json",
	}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	rec := patchJSON(t, m.handleHubNodeUpdate, "/api/v1/hub/nodes/node-a", map[string]any{"name": "x"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("client update status=%d body=%s, want 409", rec.Code, rec.Body.String())
	}
}

func TestHubPair_SecretsArePerNode(t *testing.T) {
	m := NewManagementServer(0, "mgmt", nil)
	if err := m.EnableHub("global-secret", true, HubClientOptions{StateFile: t.TempDir() + "/hub-devices.json", RelayPort: -1}); err != nil {
		t.Fatalf("EnableHub: %v", err)
	}
	defer m.Stop()
	a := pairTestDevice(t, m, "node-a", "omp-a.example")
	b := pairTestDevice(t, m, "node-b", "omp-b.example")
	if a == b || a == "global-secret" || b == "global-secret" {
		t.Fatalf("per-node secrets not unique: a=%q b=%q", a, b)
	}
	reqA := httptest.NewRequest(http.MethodPost, "/", nil)
	reqA.Header.Set("X-Hub-Secret", a)
	reqB := httptest.NewRequest(http.MethodPost, "/", nil)
	reqB.Header.Set("X-Hub-Secret", b)
	if !m.authHubDevice(reqA, "node-a") || m.authHubDevice(reqB, "node-a") {
		t.Fatal("node-a secret did not authenticate only node-a")
	}
	if !m.authHubDevice(reqB, "node-b") || m.authHubDevice(reqA, "node-b") {
		t.Fatal("node-b secret did not authenticate only node-b")
	}
}
