package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentMarkdown(t *testing.T) {
	content := "---\nname: release-auditor\ndescription: Check release readiness.\nmodel: \"@slow\"\ntools: read, grep, glob, bash\n---\n\nInspect the assigned release scope. Report blockers."
	meta, body := ParseAgentMarkdown(content)
	if meta["name"] != "release-auditor" {
		t.Errorf("name = %q", meta["name"])
	}
	if meta["description"] != "Check release readiness." {
		t.Errorf("description = %q", meta["description"])
	}
	if meta["model"] != "@slow" {
		t.Errorf("model = %q (quoted value must be trimmed)", meta["model"])
	}
	if meta["tools"] != "read, grep, glob, bash" {
		t.Errorf("tools = %q", meta["tools"])
	}
	if body != "Inspect the assigned release scope. Report blockers." {
		t.Errorf("body = %q", body)
	}
}

func TestParseAgentMarkdown_NoFrontmatterFallsBack(t *testing.T) {
	meta, body := ParseAgentMarkdown("just instructions")
	if len(meta) != 0 {
		t.Errorf("meta = %v, want empty", meta)
	}
	if body != "just instructions" {
		t.Errorf("body = %q", body)
	}
}

func TestRenderAgentMarkdown_RoundTripsThroughParser(t *testing.T) {
	md := RenderAgentMarkdown("qa-bot", "Runs the test suite", "deepseek/deepseek-flash", "bash, read", "medium", "scout, task", "Run go test ./... and report failures.")
	meta, body := ParseAgentMarkdown(md)
	if meta["name"] != "qa-bot" || meta["description"] != "Runs the test suite" ||
		meta["model"] != "deepseek/deepseek-flash" || meta["tools"] != "bash, read" {
		t.Errorf("round-trip meta = %v", meta)
	}
	if meta["thinkingLevel"] != "medium" {
		t.Errorf("thinkingLevel = %q, want medium", meta["thinkingLevel"])
	}
	if meta["spawns"] != "scout,task" {
		t.Errorf("spawns = %q, want scout,task (list flattened)", meta["spawns"])
	}
	if !strings.Contains(body, "go test") {
		t.Errorf("round-trip body = %q", body)
	}
}

func TestParseAgentMarkdown_SpawnsListFlatens(t *testing.T) {
	content := "---\nname: lead\nspawns:\n  - scout\n  - task\n---\n\nDelegate everything.\n"
	meta, _ := ParseAgentMarkdown(content)
	if meta["spawns"] != "scout,task" {
		t.Errorf("spawns = %q, want scout,task", meta["spawns"])
	}
}

func TestRenderSpawns_SingleAndWildcard(t *testing.T) {
	if got := renderSpawns("*"); got != `spawns: "*"` {
		t.Errorf("renderSpawns(*) = %q", got)
	}
	if got := renderSpawns("scout"); got != "spawns: scout" {
		t.Errorf("renderSpawns(scout) = %q", got)
	}
	if got := renderSpawns(" scout , task "); got != "spawns:\n  - scout\n  - task" {
		t.Errorf("renderSpawns(list) = %q", got)
	}
	if got := renderSpawns(""); got != "" {
		t.Errorf("renderSpawns('') = %q, want empty", got)
	}
}

// User-scope CRUD must stay in the test's HOME, never the developer's
// ~/.omp/agent/agents directory.
func withTempAgentHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows resolves the home directory from USERPROFILE, so HOME alone
	// leaves these tests writing into the real profile.
	t.Setenv("USERPROFILE", home)
	return home
}

func TestUserAgentCRUD(t *testing.T) {
	home := withTempAgentHome(t)

	info, err := CreateUserAgent("my-agent", "desc", "model-x", "bash", "medium", "scout, task", "do the thing")
	if err != nil {
		t.Fatalf("createUserAgent() error = %v", err)
	}
	if info.Scope != "user" {
		t.Errorf("scope = %q, want user", info.Scope)
	}
	if info.ThinkingLevel != "medium" || info.Spawns != "scout, task" {
		t.Errorf("info fields = %+v", info)
	}
	path := filepath.Join(home, ".omp", "agent", "agents", "my-agent.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("agent file not written: %v", err)
	}

	// Duplicate create must fail.
	if _, err := CreateUserAgent("my-agent", "x", "", "", "", "", "y"); err == nil {
		t.Error("duplicate create = nil, want error")
	}

	// Update rewrites content.
	updated, err := UpdateUserAgent("my-agent", "new desc", "model-y", "", "", "", "new body")
	if err != nil {
		t.Fatalf("updateUserAgent() error = %v", err)
	}
	if updated.Description != "new desc" || updated.Model != "model-y" {
		t.Errorf("updated = %+v", updated)
	}
	meta, body := ParseAgentMarkdown(mustReadFile(t, path))
	if meta["description"] != "new desc" || body != "new body" {
		t.Errorf("file after update: meta=%v body=%q", meta, body)
	}

	// Update missing agent fails.
	if _, err := UpdateUserAgent("nope", "", "", "", "", "", ""); err == nil {
		t.Error("update missing = nil, want error")
	}

	// Delete removes the file; double delete fails.
	if err := DeleteUserAgent("my-agent"); err != nil {
		t.Fatalf("deleteUserAgent() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("agent file still exists after delete: %v", err)
	}
	if err := DeleteUserAgent("my-agent"); err == nil {
		t.Error("delete missing = nil, want error")
	}
}

func TestCreateUserAgent_RejectsBadName(t *testing.T) {
	withTempAgentHome(t)
	for _, name := range []string{"", "a b", "x/y", "a.b", strings.Repeat("a", 65)} {
		if _, err := CreateUserAgent(name, "", "", "", "", "", ""); err == nil {
			t.Errorf("createUserAgent(%q) = nil, want error", name)
		}
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
