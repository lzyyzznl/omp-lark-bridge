package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestTranscriptOmpWeb_MirrorsIntoOmpSessionsDir pins the contract behind the
// console's「查看对话」link: the transcript is copied into omp's own sessions
// store (the only place ompweb enumerates) and the URL opens that session id.
func TestTranscriptOmpWeb_MirrorsIntoOmpSessionsDir(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("OMPWEB_URL", "http://127.0.0.1:9999/")

	dir := t.TempDir()
	id := "2026-01-01_abc"
	body := `{"type":"title","v":1,"title":"t"}` + "\n" +
		`{"type":"session","version":3,"id":"sess-1","cwd":"/w"}` + "\n" +
		`{"type":"message","id":"m1"}` + "\n"
	src := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(src, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &ManagementServer{}
	rec := httptest.NewRecorder()
	m.transcriptOmpWeb(rec, ompWebReq("127.0.0.1:31990"), dir, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			URL     string `json:"url"`
			Session string `json:"session"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if want := "http://127.0.0.1:9999/?session=sess-1"; resp.Data.URL != want {
		t.Errorf("url = %q, want %q", resp.Data.URL, want)
	}

	mirrored := filepath.Join(agentDir, "sessions", "omp-lark-bridge", "sess-1.jsonl")
	got, err := os.ReadFile(mirrored)
	if err != nil {
		t.Fatalf("mirror not written: %v", err)
	}
	if string(got) != body {
		t.Errorf("mirror content = %q, want %q", got, body)
	}

	// Path traversal in the id must be rejected before any file access.
	rec = httptest.NewRecorder()
	m.transcriptOmpWeb(rec, ompWebReq("127.0.0.1:31990"), dir, "../"+id)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("traversal status = %d, want 400", rec.Code)
	}

	// A transcript without a session header has no ompweb id to open.
	bare := filepath.Join(dir, "bare.jsonl")
	if err := os.WriteFile(bare, []byte("{\"type\":\"message\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	m.transcriptOmpWeb(rec, ompWebReq("127.0.0.1:31990"), dir, "bare")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("headerless status = %d, want 422", rec.Code)
	}
}

// TestOmpWebURLs pins the loopback/public split: probes stay on the internal
// address, while browser links prefer the configured public URL.
func TestOmpWebURLs(t *testing.T) {
	t.Setenv("OMPWEB_URL", "http://127.0.0.1:9999/")
	t.Setenv("OMPWEB_PUBLIC_URL", "")
	if got := ompWebPublicURL(); got != "http://127.0.0.1:9999" {
		t.Errorf("fallback public URL = %q, want internal", got)
	}
	t.Setenv("OMPWEB_PUBLIC_URL", "https://omp.example.com/")
	if got := ompWebPublicURL(); got != "https://omp.example.com" {
		t.Errorf("public URL = %q, want configured", got)
	}
	if got := ompWebConfiguredPublicURL(); got != "https://omp.example.com" {
		t.Errorf("configured public URL = %q", got)
	}
}

// ompWebReq builds a console request whose Host is how the browser reached it.
func ompWebReq(host string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ompweb", nil)
	req.Host = host
	return req
}

// TestOmpWebURLForRequest pins the routing priority: a console opened on this
// machine links to loopback, a LAN or tailnet entry links to that same host, and
// only a public entry point keeps the configured public domain.
func TestOmpWebURLForRequest(t *testing.T) {
	t.Setenv("OMPWEB_URL", "http://127.0.0.1:30177")
	t.Setenv("OMPWEB_PUBLIC_URL", "https://omp-lct950915.lizeyu.win")
	cases := []struct{ host, want string }{
		{"127.0.0.1:31990", "http://127.0.0.1:30177"},
		{"[::1]:31990", "http://127.0.0.1:30177"},
		{"localhost:31990", "http://127.0.0.1:30177"},
		{"192.168.67.74:31990", "http://192.168.67.74:30177"},
		{"10.0.0.9:31990", "http://10.0.0.9:30177"},
		{"100.78.175.122:31990", "http://100.78.175.122:30177"},
		{"omen-ubuntu.tail7fe07c.ts.net:31990", "http://omen-ubuntu.tail7fe07c.ts.net:30177"},
		{"desktop-1:31990", "http://desktop-1:30177"},
		{"omp-lct950915.lizeyu.win", "https://omp-lct950915.lizeyu.win"},
		{"bridge.lizeyu.win", "https://omp-lct950915.lizeyu.win"},
	}
	for _, c := range cases {
		if got := ompWebURLForRequest(ompWebReq(c.host)); got != c.want {
			t.Errorf("host %q -> %q, want %q", c.host, got, c.want)
		}
	}
}

// TestTranscriptOmpWeb_FollowsEntryHost is the regression test for the loopback
// detour: a LAN-opened console used to hand out the public domain.
func TestTranscriptOmpWeb_FollowsEntryHost(t *testing.T) {
	agentDir := filepath.Join(t.TempDir(), "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("OMPWEB_URL", "http://127.0.0.1:30177")
	t.Setenv("OMPWEB_PUBLIC_URL", "https://omp-lct950915.lizeyu.win")

	dir := t.TempDir()
	id := "2026-01-01_lan"
	body := `{"type":"session","version":3,"id":"sess-2","cwd":"/w"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &ManagementServer{}
	for host, want := range map[string]string{
		"192.168.67.74:31990":      "http://192.168.67.74:30177/?session=sess-2",
		"omp-lct950915.lizeyu.win": "https://omp-lct950915.lizeyu.win/?session=sess-2",
	} {
		rec := httptest.NewRecorder()
		m.transcriptOmpWeb(rec, ompWebReq(host), dir, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body = %s", host, rec.Code, rec.Body.String())
		}
		var resp struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode: %v", host, err)
		}
		if resp.Data.URL != want {
			t.Errorf("%s: url = %q, want %q", host, resp.Data.URL, want)
		}
	}
}
