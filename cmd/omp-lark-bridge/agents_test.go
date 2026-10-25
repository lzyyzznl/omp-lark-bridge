package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// TestAgentsCommandLifecycle drives the full `omp-lark-bridge agents` surface
// in an isolated HOME: new → list → show → edit → del, asserting the user-scope
// markdown file on disk at each step. The command layer is thin (flag parsing +
// core delegates), but this locks the CLI/UX contract so the subcommands cannot
// drift from the web UI's agent 人格 page.
func TestAgentsCommandLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runAgentsNew([]string{"qabot", "--desc", "run QA", "--model", "@slow", "--thinking", "high", "--spawns", "scout, task", "--body", "Run the test suite and report."})
	path := filepath.Join(os.Getenv("HOME"), ".omp", "agent", "agents", "qabot.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("agent file not created after new: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "thinkingLevel: high") || !strings.Contains(string(data), "spawns:") {
		t.Fatalf("created markdown missing thinking/spawns:\n%s", data)
	}

	// list shows the new agent with its scope.
	out := captureStdout(t, func() { runAgentsList(nil) })
	if !strings.Contains(out, "qabot") || !strings.Contains(out, "user") {
		t.Errorf("agents list output missing qabot:\n%s", out)
	}
	if !strings.Contains(out, "THINKING") {
		t.Errorf("agents list should show the thinking column:\n%s", out)
	}

	// show prints the full source.
	out = captureStdout(t, func() { runAgentsShow([]string{"qabot"}) })
	if !strings.Contains(out, "name: qabot") || !strings.Contains(out, "Run the test suite") {
		t.Errorf("agents show output:\n%s", out)
	}

	// edit keeps untouched fields, applies the changed one.
	runAgentsEdit([]string{"qabot", "--thinking", "low", "--desc", "run QA harder"})
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), "thinkingLevel: low") {
		t.Errorf("edit did not change thinkingLevel:\n%s", data)
	}
	if !strings.Contains(string(data), "model: @slow") || !strings.Contains(string(data), "Run the test suite") {
		t.Errorf("edit dropped untouched fields:\n%s", data)
	}

	// del removes the file.
	runAgentsDel([]string{"qabot"})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("agent file still exists after del: %v", err)
	}
}

// TestPrintAgentsUsage_DocumentsSubcommands guards the help text so operators
// discover the full command surface (`agents` mirrors the web 人格 page).
func TestPrintAgentsUsage_DocumentsSubcommands(t *testing.T) {
	out := captureStdout(t, printAgentsUsage)
	for _, want := range []string{"agents list", "agents show", "agents new", "agents edit", "agents del", "--thinking", "--spawns"} {
		if !strings.Contains(out, want) {
			t.Errorf("printAgentsUsage missing %q; got:\n%s", want, out)
		}
	}
}

// TestPrintCronAddUsage_DocumentsAgent mirrors the --silent guard: the --agent
// flag must stay documented so cron delegation is discoverable from the CLI.
func TestPrintCronAddUsage_DocumentsAgent(t *testing.T) {
	out := captureStdout(t, printCronAddUsage)
	if !strings.Contains(out, "--agent <name>") {
		t.Errorf("printCronAddUsage does not document --agent; got:\n%s", out)
	}
}
