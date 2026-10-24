package omp

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lzyyzznl/in-one-chat/core"
)

// captureWriteCloser records the frames the session writes to omp's stdin.
type captureWriteCloser struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureWriteCloser) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureWriteCloser) Close() error { return nil }

func (c *captureWriteCloser) frames(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var frames []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("written frame is not JSON: %v (%s)", err, line)
		}
		frames = append(frames, frame)
	}
	return frames
}

func newAskTestSession(t *testing.T) (*ompSession, *captureWriteCloser) {
	t.Helper()
	w := &captureWriteCloser{}
	return &ompSession{
		events: make(chan core.Event, 8),
		stdin:  w,
		asks:   make(map[string]*ompPendingAsk),
	}, w
}

func nextEvent(t *testing.T, s *ompSession) core.Event {
	t.Helper()
	select {
	case ev := <-s.events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for an engine event")
		return core.Event{}
	}
}

func askFrame() map[string]any {
	return map[string]any{
		"type":   "extension_ui_request",
		"id":     "ui_9",
		"method": "ask",
		"questions": []any{
			map[string]any{
				"id":       "db",
				"question": "Which database?",
				"options": []any{
					map[string]any{"label": "Postgres"},
					map[string]any{"label": "SQLite", "description": "local file"},
				},
				"recommended": float64(1),
			},
			map[string]any{
				"id":       "features",
				"question": "Which features?",
				"multi":    true,
				"options": []any{
					map[string]any{"label": "Auth"},
					map[string]any{"label": "Billing"},
				},
			},
		},
	}
}

func TestAskDialogBecomesQuestionCardEvent(t *testing.T) {
	s, _ := newAskTestSession(t)
	s.handleUIRequest(askFrame())

	ev := nextEvent(t, s)
	if ev.Type != core.EventPermissionRequest {
		t.Fatalf("event type = %v, want permission request", ev.Type)
	}
	if ev.ToolName != "AskUserQuestion" {
		t.Fatalf("ToolName = %q, want AskUserQuestion (engine renders cards for it)", ev.ToolName)
	}
	if ev.RequestID != "ui_9" {
		t.Fatalf("RequestID = %q, want ui_9", ev.RequestID)
	}
	if len(ev.Questions) != 2 {
		t.Fatalf("questions = %d, want 2", len(ev.Questions))
	}
	first := ev.Questions[0]
	if first.Question != "Which database?" || len(first.Options) != 2 || first.Options[1].Description != "local file" {
		t.Fatalf("first question = %#v", first)
	}
	if first.MultiSelect {
		t.Fatal("single-select question must not be multi")
	}
	if !ev.Questions[1].MultiSelect {
		t.Fatal("second question must be multi-select")
	}
}

func TestRespondPermissionSendsAskAnswers(t *testing.T) {
	s, w := newAskTestSession(t)
	s.handleUIRequest(askFrame())
	nextEvent(t, s)

	err := s.RespondPermission("ui_9", core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"Which database?": "SQLite",
				"Which features?": "Auth, Billing",
			},
		},
	})
	if err != nil {
		t.Fatalf("RespondPermission() error = %v", err)
	}
	frames := w.frames(t)
	if len(frames) != 1 {
		t.Fatalf("wrote %d frames, want 1: %#v", len(frames), frames)
	}
	frame := frames[0]
	if frame["type"] != "extension_ui_response" || frame["id"] != "ui_9" {
		t.Fatalf("frame = %#v, want extension_ui_response for ui_9", frame)
	}
	answers, ok := frame["answers"].([]any)
	if !ok || len(answers) != 2 {
		t.Fatalf("answers = %#v, want two entries", frame["answers"])
	}
	first, _ := answers[0].(map[string]any)
	if first["id"] != "db" {
		t.Fatalf("first answer id = %v, want db", first["id"])
	}
	if got := optionLabels(first); len(got) != 1 || got[0] != "SQLite" {
		t.Fatalf("first answer selectedOptions = %v, want [SQLite]", got)
	}
	second, _ := answers[1].(map[string]any)
	if got := optionLabels(second); len(got) != 2 || got[0] != "Auth" || got[1] != "Billing" {
		t.Fatalf("second answer selectedOptions = %v, want [Auth Billing]", got)
	}

	// The dialog is consumed: a stale answer must not be sent twice.
	if err := s.RespondPermission("ui_9", core.PermissionResult{Behavior: "allow"}); err != nil {
		t.Fatalf("second RespondPermission() error = %v", err)
	}
	if frames := w.frames(t); len(frames) != 1 {
		t.Fatalf("resolved dialog must not answer twice, wrote %d frames", len(frames))
	}
}

func optionLabels(answer map[string]any) []string {
	raw, _ := answer["selectedOptions"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestRespondPermissionFreeTextBecomesCustomInput(t *testing.T) {
	s, w := newAskTestSession(t)
	s.handleUIRequest(askFrame())
	nextEvent(t, s)

	if err := s.RespondPermission("ui_9", core.PermissionResult{
		Behavior: "allow",
		UpdatedInput: map[string]any{
			"answers": map[string]any{
				"Which database?": "DuckDB",
				"Which features?": "Search",
			},
		},
	}); err != nil {
		t.Fatalf("RespondPermission() error = %v", err)
	}
	answers, _ := w.frames(t)[0]["answers"].([]any)
	first, _ := answers[0].(map[string]any)
	if first["customInput"] != "DuckDB" {
		t.Fatalf("customInput = %v, want DuckDB", first["customInput"])
	}
	if labels := optionLabels(first); len(labels) != 0 {
		t.Fatalf("free text must not be reported as a selected option: %v", labels)
	}
	second, _ := answers[1].(map[string]any)
	if second["customInput"] != "Search" {
		t.Fatalf("multi free text customInput = %v, want Search", second["customInput"])
	}
}

func TestSelectFallbackRendersAndAnswers(t *testing.T) {
	s, w := newAskTestSession(t)
	s.handleUIRequest(map[string]any{
		"type":    "extension_ui_request",
		"id":      "sel_1",
		"method":  "select",
		"title":   "Pick a model",
		"options": []any{"opus", "sonnet"},
		"optionDetails": []any{
			map[string]any{"description": "fast"},
			map[string]any{},
		},
	})

	ev := nextEvent(t, s)
	if ev.Type != core.EventPermissionRequest || len(ev.Questions) != 1 {
		t.Fatalf("select event = %#v", ev)
	}
	if got := ev.Questions[0].Options; len(got) != 2 || got[0].Label != "opus" || got[0].Description != "fast" {
		t.Fatalf("select options = %#v", got)
	}

	if err := s.RespondPermission("sel_1", core.PermissionResult{
		Behavior:     "allow",
		UpdatedInput: map[string]any{"answers": map[string]any{"Pick a model": "sonnet"}},
	}); err != nil {
		t.Fatalf("RespondPermission() error = %v", err)
	}
	frame := w.frames(t)[0]
	if frame["type"] != "extension_ui_response" || frame["value"] != "sonnet" {
		t.Fatalf("select response = %#v, want value sonnet", frame)
	}
}

func TestCancelRequestDropsPendingQuestion(t *testing.T) {
	s, _ := newAskTestSession(t)
	s.handleUIRequest(askFrame())
	nextEvent(t, s)

	s.handleUIRequest(map[string]any{
		"type":     "extension_ui_request",
		"method":   "cancel",
		"targetId": "ui_9",
	})
	if pending := s.takeAsk("ui_9"); pending != nil {
		t.Fatalf("cancelled dialog still pending: %#v", pending)
	}
}

func TestUnsupportedUIRequestIsCancelledNotIgnored(t *testing.T) {
	s, w := newAskTestSession(t)
	s.handleUIRequest(map[string]any{
		"type":    "extension_ui_request",
		"id":      "in_1",
		"method":  "input",
		"title":   "Branch name",
		"message": "name?",
	})
	frames := w.frames(t)
	if len(frames) != 1 || frames[0]["cancelled"] != true || frames[0]["id"] != "in_1" {
		t.Fatalf("unsupported request must be cancelled, wrote %#v", frames)
	}
}

func TestSplitAskAnswer(t *testing.T) {
	multi := ompAskQuestion{multi: true, labels: []string{"Auth", "Billing", "Search"}}
	if selected, custom := splitAskAnswer(multi, "Auth, Search"); custom != "" || len(selected) != 2 {
		t.Fatalf("multi exact labels = (%v, %q), want ([Auth Search], \"\")", selected, custom)
	}
	if selected, custom := splitAskAnswer(multi, "Auth, DuckDB"); custom != "Auth, DuckDB" || len(selected) != 0 {
		t.Fatalf("mixed answer must stay one custom string, got (%v, %q)", selected, custom)
	}
	single := ompAskQuestion{labels: []string{"Postgres", "SQLite"}}
	if selected, custom := splitAskAnswer(single, "SQLite"); custom != "" || len(selected) != 1 || selected[0] != "SQLite" {
		t.Fatalf("single label = (%v, %q)", selected, custom)
	}
	if selected, custom := splitAskAnswer(single, ""); len(selected) != 0 || custom != "" {
		t.Fatalf("empty answer = (%v, %q), want no selection", selected, custom)
	}
}
