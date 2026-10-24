package omp

import (
	"os"
	"path/filepath"
	"testing"
)

// planEnterSession is what makes a conversation survive a daemon restart: the
// engine persists CurrentSessionID() and feeds it back into StartSession, so a
// session file path must map to switch_session while a vanished one must degrade
// to a fresh session instead of failing the turn.
func TestPlanEnterSession(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, defaultSessionName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionFile := filepath.Join(sessionDir, "2026-01-01T00-00-00_deadbeef.jsonl")
	if err := os.WriteFile(sessionFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	kind, value := planEnterSession("", sessionDir)
	if kind != "new_session" || value != "" {
		t.Fatalf(`planEnterSession("") = (%q, %q), want new_session`, kind, value)
	}

	kind, value = planEnterSession(sessionFile, sessionDir)
	if kind != "switch_session" || value != sessionFile {
		t.Fatalf("planEnterSession(file) = (%q, %q), want switch_session with the path", kind, value)
	}

	missing := filepath.Join(sessionDir, "gone.jsonl")
	if kind, _ := planEnterSession(missing, sessionDir); kind != "new_session" {
		t.Fatalf("vanished session file must fall back to new_session, got %q", kind)
	}

	// Legacy value: a bare store directory name with data on disk.
	legacy := filepath.Join(root, "s7")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "a.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	kind, value = planEnterSession("s7", sessionDir)
	if kind != "open_session" || value != legacy {
		t.Fatalf("planEnterSession(legacy dir) = (%q, %q), want open_session %q", kind, value, legacy)
	}

	if kind, _ := planEnterSession("s404", sessionDir); kind != "new_session" {
		t.Fatalf("unknown legacy id must start fresh, got %q", kind)
	}
}

// The store directory must stay stable regardless of the agent session id, or
// every restart lands in a fresh empty directory and resume can never work.
func TestOmpSessionStoreDirIsStable(t *testing.T) {
	dataDir := t.TempDir()
	a := ompSessionStoreDir(dataDir, "")
	b := ompSessionStoreDir(dataDir, "01a16ec6-e29f-7000-9c83-d35e25346975")
	if a != b {
		t.Fatalf("store dir depends on the agent session id: %q vs %q", a, b)
	}
	if filepath.Base(a) != defaultSessionName {
		t.Fatalf("store dir base = %q, want %q", filepath.Base(a), defaultSessionName)
	}
}
