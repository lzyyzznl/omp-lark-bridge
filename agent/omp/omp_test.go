package omp

import (
	"path/filepath"
	"testing"

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
