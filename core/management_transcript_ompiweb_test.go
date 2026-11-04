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
	m.transcriptOmpWeb(rec, dir, id)
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
	m.transcriptOmpWeb(rec, dir, "../"+id)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("traversal status = %d, want 400", rec.Code)
	}

	// A transcript without a session header has no ompweb id to open.
	bare := filepath.Join(dir, "bare.jsonl")
	if err := os.WriteFile(bare, []byte("{\"type\":\"message\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	m.transcriptOmpWeb(rec, dir, "bare")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("headerless status = %d, want 422", rec.Code)
	}
}
