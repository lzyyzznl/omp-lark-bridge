package core

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
	if claimed.Data.DeviceToken != "dev1" || claimed.Data.Secret != "s3cr3t" || claimed.Data.WebURL != "https://omp-test.example" {
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

func pairTestDevice(t *testing.T, m *ManagementServer, device, host string) {
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
	pairTestDevice(t, m, "devws", "127.0.0.1")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hub/tunnel", m.handleHubTunnel)
	tunnelSrv := httptest.NewServer(mux)
	defer tunnelSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(tunnelSrv.URL, "http") + "/api/v1/hub/tunnel"
	header := http.Header{}
	header.Set("X-Hub-Secret", "s3cr3t")
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
	pairTestDevice(t, m, "devrm", "omp-rm.example")
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

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/hub/tunnel", m.handleHubTunnel)
	tunnelSrv := httptest.NewServer(mux)
	defer tunnelSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tunnelSrv.URL, "http") + "/api/v1/hub/tunnel"
	header := http.Header{}
	header.Set("X-Hub-Secret", "s3cr3t")
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
