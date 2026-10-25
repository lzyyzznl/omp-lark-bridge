package omp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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

// fakeOmpRPC drives enterSession against an in-process omp protocol peer so
// the real writeFrame → waitResponse round-trip is exercised without spawning
// the CLI. respond() maps each incoming command frame to a response frame.
//
// Deterministic by construction: no io.Pipe or OS pipes — the peer is a single
// goroutine fed by a buffered channel writer, and each response is routed
// straight into waitResponse's pending slot (spinning until it is registered,
// which the caller's own goroutine does immediately after writeFrame).
func fakeOmpRPC(t *testing.T, respond func(req map[string]any) map[string]any) (*ompSession, func() []map[string]any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	cmdCh := make(chan []byte, 16)

	var mu sync.Mutex
	cmds := []map[string]any{}

	s := &ompSession{
		ctx:        ctx,
		cancel:     cancel,
		stdin:      &chanWriteCloser{ch: cmdCh},
		stderr:     io.NopCloser(bytes.NewReader(nil)),
		ready:      make(chan struct{}),
		events:     make(chan core.Event, 16),
		sessionDir: t.TempDir(),
		pending:    make(map[string]chan map[string]any),
	}
	s.alive.Store(true)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-cmdCh:
				if !ok {
					return
				}
				var req map[string]any
				if err := json.Unmarshal(line, &req); err != nil {
					continue
				}
				mu.Lock()
				cmds = append(cmds, req)
				mu.Unlock()
				resp := respond(req)
				if resp == nil {
					continue
				}
				id, _ := req["id"].(string)
				if id == "" {
					continue
				}
				resp["id"] = id
				for {
					s.pMu.Lock()
					ch, ok := s.pending[id]
					s.pMu.Unlock()
					if ok {
						ch <- resp
						break
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(time.Millisecond):
					}
				}
			}
		}
	}()
	t.Cleanup(cancel)
	return s, func() []map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := make([]map[string]any, len(cmds))
		copy(out, cmds)
		return out
	}
}

// chanWriteCloser is an io.WriteCloser that hands each JSON line to the peer
// goroutine over a buffered channel instead of an OS pipe.
type chanWriteCloser struct {
	ch chan<- []byte
}

func (w *chanWriteCloser) Write(p []byte) (int, error) {
	w.ch <- append([]byte(nil), p...)
	return len(p), nil
}

func (w *chanWriteCloser) Close() error { return nil }

func rpcFrameType(f map[string]any) string {
	t, _ := f["type"].(string)
	return t
}

// set_thinking_level is fire-and-forget (no waitResponse), so the peer goroutine
// may still be appending it when enterSession returns — poll until it lands.
func waitForFrame(t *testing.T, cmds func() []map[string]any, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, c := range cmds() {
			if rpcFrameType(c) == want {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s frame", want)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// A rejected set_model ("Model not found") must NOT fail the session — the job
// keeps its default model and still runs (cron requirement).
func TestEnterSession_SetModelFailureFallsBackToDefault(t *testing.T) {
	ok := func(req map[string]any) map[string]any {
		switch req["type"] {
		case "new_session":
			return map[string]any{"type": "response", "command": "new_session", "success": true, "data": map[string]any{"cancelled": false}}
		case "get_state":
			return map[string]any{"type": "response", "command": "get_state", "success": true, "data": map[string]any{"sessionId": "s1", "sessionFile": "/tmp/s1.jsonl"}}
		case "set_model":
			return map[string]any{"type": "response", "command": "set_model", "success": false, "error": "Model not found: bogus-prov/no-such-model"}
		default:
			return map[string]any{"type": "response", "command": req["type"], "success": true}
		}
	}
	s, cmds := fakeOmpRPC(t, ok)
	if err := s.enterSession("bogus-prov/no-such-model", "high", ""); err != nil {
		t.Fatalf("enterSession() error = %v, want nil (fall back to default model)", err)
	}
	waitForFrame(t, cmds, "set_thinking_level")
	types := map[string]bool{}
	for _, c := range cmds() {
		types[rpcFrameType(c)] = true
	}
	if !types["set_model"] {
		t.Error("expected a set_model frame")
	}
	if !types["set_thinking_level"] {
		t.Error("expected a set_thinking_level frame even after set_model failure")
	}
}

func TestEnterSession_SendsModelAndThinkingLevel(t *testing.T) {
	ok := func(req map[string]any) map[string]any {
		return map[string]any{"type": "response", "command": req["type"], "success": true}
	}
	s, cmds := fakeOmpRPC(t, ok)
	if err := s.enterSession("deepseek/deepseek-flash", "high", ""); err != nil {
		t.Fatalf("enterSession() error = %v", err)
	}
	waitForFrame(t, cmds, "set_thinking_level")
	sawModel, sawThink := false, false
	for _, c := range cmds() {
		switch rpcFrameType(c) {
		case "set_model":
			if c["provider"] != "deepseek" || c["modelId"] != "deepseek-flash" {
				t.Errorf("set_model frame = %v, want provider=deepseek modelId=deepseek-flash", c)
			}
			sawModel = true
		case "set_thinking_level":
			if c["level"] != "high" {
				t.Errorf("set_thinking_level level = %v, want high", c["level"])
			}
			sawThink = true
		}
	}
	if !sawModel || !sawThink {
		t.Errorf("missing frames: set_model=%v set_thinking_level=%v", sawModel, sawThink)
	}
}

func TestEnterSession_NoThinkingFrameWhenEmpty(t *testing.T) {
	ok := func(req map[string]any) map[string]any {
		return map[string]any{"type": "response", "command": req["type"], "success": true}
	}
	s, cmds := fakeOmpRPC(t, ok)
	if err := s.enterSession("", "", ""); err != nil {
		t.Fatalf("enterSession() error = %v", err)
	}
	for _, c := range cmds() {
		if rpcFrameType(c) == "set_thinking_level" {
			t.Error("unexpected set_thinking_level frame for empty thinking value")
		}
	}
}

func TestParseModelsResponse(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want []string // expected model names in order
	}{
		{
			name: "full list",
			data: map[string]any{"models": []any{
				map[string]any{"provider": "deepseek", "id": "deepseek-flash", "name": "DeepSeek Flash"},
				map[string]any{"provider": "router", "id": "auto", "name": "Auto"},
			}},
			want: []string{"deepseek/deepseek-flash", "router/auto"},
		},
		{
			name: "entries missing provider or id are skipped",
			data: map[string]any{"models": []any{
				map[string]any{"id": "no-provider", "name": "x"},
				map[string]any{"provider": "p", "name": "no-id"},
			}},
			want: []string{},
		},
		{
			name: "nil data",
			data: nil,
			want: []string{},
		},
		{
			name: "models not an array",
			data: map[string]any{"models": "oops"},
			want: []string{},
		},
	}
	for _, tt := range tests {
		got := parseModelsResponse(tt.data)
		names := make([]string, len(got))
		for i, m := range got {
			names[i] = m.Name
		}
		if !reflect.DeepEqual(names, tt.want) {
			t.Errorf("%s: parseModelsResponse = %v, want %v", tt.name, names, tt.want)
		}
	}
}

func TestBuildSpawnArgs_AppendsSystemPromptForLiveSessions(t *testing.T) {
	args := buildSpawnArgs("/store", "/work", []string{"--extra"}, "in-one-chat brief")
	found := false
	for i, a := range args {
		if a == "--append-system-prompt" {
			if i+1 >= len(args) || args[i+1] != "in-one-chat brief" {
				t.Fatalf("--append-system-prompt missing its value: %v", args)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("live session args must include --append-system-prompt: %v", args)
	}
}

func TestBuildSpawnArgs_SkipsInjectionForQueryProcess(t *testing.T) {
	args := buildSpawnArgs("/store", "/work", nil, "")
	for _, a := range args {
		if a == "--append-system-prompt" {
			t.Fatalf("query-only process must not inject system prompt: %v", args)
		}
	}
}

func TestPrependSelfDirToPath_AddsBridgeDir(t *testing.T) {
	env := []string{"HOME=/home/u", "PATH=/usr/bin:/bin"}
	out := prependSelfDirToPath(env, ":")
	if len(out) != 2 {
		t.Fatalf("env length = %d, want 2", len(out))
	}
	selfDir := filepath.Dir(mustExecutable(t))
	want := "PATH=" + selfDir + ":/usr/bin:/bin"
	if out[1] != want {
		t.Fatalf("PATH = %q, want %q", out[1], want)
	}
}

func TestPrependSelfDirToPath_AlreadyPresentSkips(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(self)
	env := []string{"PATH=" + dir + ":/usr/bin"}
	out := prependSelfDirToPath(env, ":")
	if len(out) != 1 || out[0] != "PATH="+dir+":/usr/bin" {
		t.Fatalf("already-present PATH must not change: %v", out)
	}
}

func TestPrependSelfDirToPath_NoPathAppends(t *testing.T) {
	out := prependSelfDirToPath([]string{}, ":")
	if len(out) != 1 || !strings.HasPrefix(out[0], "PATH=") {
		t.Fatalf("missing PATH must be appended: %v", out)
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return self
}
