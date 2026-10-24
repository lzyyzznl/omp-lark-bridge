package omp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lzyyzznl/in-one-chat/core"
)

func TestSanitizeSessionName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "default"},
		{"s1", "s1"},
		{"a/b/../c", "a-b----c"},
		{"my session:1", "my-session-1"},
		{"..", "--"},
		{"对话session-1", "--session-1"},
	}
	for _, c := range cases {
		if got := sanitizeSessionName(c.in); got != c.want {
			t.Errorf("sanitizeSessionName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A sanitized name must never escape the session root.
	root := sanitizeSessionName("a/../../etc/passwd")
	if filepath.Base(root) != root {
		t.Errorf("sanitized name escapes its directory: %q", root)
	}
}

func TestSanitizeSessionName_StableAndUnique(t *testing.T) {
	a := sanitizeSessionName("feishu:u1:s1")
	b := sanitizeSessionName("feishu:u1:s1")
	if a != b {
		t.Errorf("sanitization not stable: %q vs %q", a, b)
	}
	if sanitizeSessionName("s1") == sanitizeSessionName("s2") {
		t.Error("distinct sessions must not collide")
	}
}

func TestSplitModel(t *testing.T) {
	cases := []struct {
		in           string
		wantProvider string
		wantID       string
		wantOK       bool
	}{
		{"router", "", "", false},
		{"", "", "", false},
		{"/id", "", "", false},
		{"p/", "", "", false},
		{"anthropic/claude-sonnet-4", "anthropic", "claude-sonnet-4", true},
		{"router/deepseek-v3", "router", "deepseek-v3", true},
	}
	for _, c := range cases {
		prov, id, ok := splitModel(c.in)
		if ok != c.wantOK || prov != c.wantProvider || id != c.wantID {
			t.Errorf("splitModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.in, prov, id, ok, c.wantProvider, c.wantID, c.wantOK)
		}
	}
}

func TestExtForMime(t *testing.T) {
	cases := map[string]string{
		"image/png":                ".png",
		"image/jpeg":               ".jpg",
		"image/jpg":                ".jpg",
		"image/gif":                ".gif",
		"image/webp":               ".webp",
		"application/octet-stream": ".img",
		"":                         ".img",
	}
	for mime, want := range cases {
		if got := extForMime(mime); got != want {
			t.Errorf("extForMime(%q) = %q, want %q", mime, got, want)
		}
	}
}

// Regression: thinking_delta must be accumulated, never emitted per delta —
// the engine's EventThinking handler overwrites (not appends) the card's
// thinking area, so per-delta emission renders 1-2 chars per line.
func TestThinkingDeltaAccumulates(t *testing.T) {
	s := &ompSession{events: make(chan core.Event, 100)}
	feed := func(delta string) {
		s.handleMessageUpdate(map[string]any{
			"assistantMessageEvent": map[string]any{"type": "thinking_delta", "delta": delta},
		})
	}
	collect := func() []core.Event {
		var evs []core.Event
		for {
			select {
			case ev := <-s.events:
				evs = append(evs, ev)
			default:
				return evs
			}
		}
	}

	feed("第")
	feed("一") // lands inside the throttle window → must NOT emit separately
	first := collect()
	if len(first) != 1 || first[0].Content != "第" {
		t.Fatalf("quick deltas must emit once with first delta, got %+v", first)
	}

	s.handlePromptResult(map[string]any{"status": "completed"})
	var thinking string
	for _, ev := range collect() {
		if ev.Type == core.EventThinking {
			thinking = ev.Content
		}
	}
	if thinking != "第一" {
		t.Fatalf("turn-end flush must carry accumulated thinking, got %q", thinking)
	}

	feed("新") // next turn starts fresh
	if ev := <-s.events; ev.Type != core.EventThinking || ev.Content != "新" {
		t.Fatalf("next turn should start fresh, got %+v", ev)
	}
}

// The chat card can only show a tool's parameters/result if the adapter
// forwards them; omp sends them as `args` and `result` on the
// tool_execution_start/end frames.
func TestToolFramesCarryInputAndResult(t *testing.T) {
	s := &ompSession{events: make(chan core.Event, 10)}

	s.handleToolStart(map[string]any{
		"type":     "tool_execution_start",
		"toolName": "bash",
		"args": map[string]any{
			"command": "echo hello-from-omp",
			"timeout": float64(30),
			"cwd":     "C:/tmp",
			"pty":     false,
		},
		"intent": "Running echo command",
	})
	use := <-s.events
	if use.Type != core.EventToolUse || use.ToolName != "bash" {
		t.Fatalf("tool start event = %+v, want tool use for bash", use)
	}
	if !json.Valid([]byte(use.ToolInput)) {
		t.Fatalf("tool input must stay valid JSON for key extraction, got %q", use.ToolInput)
	}
	if !strings.Contains(use.ToolInput, `"command":"echo hello-from-omp"`) {
		t.Fatalf("tool input lost the command, got %q", use.ToolInput)
	}

	s.handleToolEnd(map[string]any{
		"type":     "tool_execution_end",
		"toolName": "bash",
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "hello-from-omp\n"}},
			"details": map[string]any{"exitCode": float64(0)},
		},
		"isError": false,
	})
	res := <-s.events
	if res.Type != core.EventToolResult || res.ToolName != "bash" {
		t.Fatalf("tool end event = %+v, want tool result for bash", res)
	}
	if res.ToolResult != "hello-from-omp" {
		t.Fatalf("tool result = %q, want the text content", res.ToolResult)
	}
	if res.ToolStatus != "completed" || res.ToolSuccess == nil || !*res.ToolSuccess {
		t.Fatalf("tool status = %q success=%v, want completed/true", res.ToolStatus, res.ToolSuccess)
	}
	if res.ToolExitCode == nil || *res.ToolExitCode != 0 {
		t.Fatalf("tool exit code = %v, want 0", res.ToolExitCode)
	}
}

func TestToolEndMarksFailure(t *testing.T) {
	s := &ompSession{events: make(chan core.Event, 10)}
	s.handleToolEnd(map[string]any{
		"toolName": "bash",
		"result": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "boom"}},
			"details": map[string]any{"exitCode": float64(2)},
		},
		"isError": true,
	})
	ev := <-s.events
	if ev.ToolStatus != "failed" || ev.ToolSuccess == nil || *ev.ToolSuccess {
		t.Fatalf("tool status = %q success=%v, want failed/false", ev.ToolStatus, ev.ToolSuccess)
	}
	if ev.ToolResult != "boom" {
		t.Fatalf("tool result = %q, want failure output", ev.ToolResult)
	}
	if ev.ToolExitCode == nil || *ev.ToolExitCode != 2 {
		t.Fatalf("tool exit code = %v, want 2", ev.ToolExitCode)
	}
}

// A huge tool argument must not produce a truncated (invalid) JSON payload:
// the platform extracts command/path keys from it.
func TestToolInputSummaryBoundsLongArgs(t *testing.T) {
	got := toolInputSummary(map[string]any{
		"command":   strings.Repeat("x", 5000),
		"file_path": "a/b.go",
		"nested":    map[string]any{"body": strings.Repeat("y", 5000)},
	})
	if got == "" {
		t.Fatal("tool input summary is empty")
	}
	if !json.Valid([]byte(got)) {
		t.Fatalf("summary must stay valid JSON, got %q", got)
	}
	if n := utf8.RuneCountInString(got); n > maxToolInputRunes {
		t.Fatalf("summary is %d runes, want <= %d", n, maxToolInputRunes)
	}
	if !strings.Contains(got, "a/b.go") {
		t.Fatalf("summary dropped a short argument, got %q", got)
	}
}

func TestToolStartFallsBackToIntent(t *testing.T) {
	s := &ompSession{events: make(chan core.Event, 10)}
	s.handleToolStart(map[string]any{"toolName": "todo", "intent": "Updating todo list"})
	if ev := <-s.events; ev.ToolInput != "Updating todo list" {
		t.Fatalf("tool input = %q, want the intent fallback", ev.ToolInput)
	}
}
