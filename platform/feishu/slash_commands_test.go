package feishu

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

// slashCommandTestServer stubs the token endpoint plus the app_slash_commands
// OpenAPI. It records created/patch bodies so tests can assert the sync diff.
type slashCommandTestServer struct {
	srv      *httptest.Server
	list     []map[string]any
	created  []map[string]any
	patched  []map[string]any
	deleted  []string
	rejected int
}

func newSlashCommandTestServer(t *testing.T, existing []map[string]any) *slashCommandTestServer {
	t.Helper()
	s := &slashCommandTestServer{list: existing}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			writeJSON(t, w, map[string]any{
				"code":                0,
				"msg":                 "success",
				"expire":              7200,
				"tenant_access_token": "slash-token",
			})
		case r.URL.Path == feishuSlashCommandPath && r.Method == http.MethodGet:
			writeJSON(t, w, map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{"items": s.list, "has_more": false},
			})
		case r.URL.Path == feishuSlashCommandPath && r.Method == http.MethodPost:
			body := decodeBody(t, r)
			s.created = append(s.created, body)
			writeJSON(t, w, map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{"command_id": "cmd-new"},
			})
		case strings.HasPrefix(r.URL.Path, feishuSlashCommandPath+"/") && r.Method == http.MethodDelete:
			s.deleted = append(s.deleted, strings.TrimPrefix(r.URL.Path, feishuSlashCommandPath+"/"))
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{}})
		case strings.HasPrefix(r.URL.Path, feishuSlashCommandPath+"/") && r.Method == http.MethodPatch:
			body := decodeBody(t, r)
			body["command_id"] = strings.TrimPrefix(r.URL.Path, feishuSlashCommandPath+"/")
			s.patched = append(s.patched, body)
			writeJSON(t, w, map[string]any{"code": 0, "msg": "success", "data": map[string]any{}})
		default:
			s.rejected++
			writeJSON(t, w, map[string]any{"code": 404, "msg": "unexpected path " + r.URL.Path})
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body %q: %v", string(raw), err)
	}
	return body
}

func newSlashCommandPlatform(srvURL string, enabled bool) *Platform {
	return &Platform{
		platformName:  "feishu",
		domain:        srvURL,
		appID:         "cli_test",
		appSecret:     "sec_test",
		slashCommands: enabled,
	}
}

func slashCommandItems(t *testing.T, entries map[string]string) []map[string]any {
	t.Helper()
	items := make([]map[string]any, 0, len(entries))
	for name, desc := range entries {
		items = append(items, map[string]any{
			"command_id": "cmd-" + name,
			"command":    name,
			"description": map[string]any{
				"default_value": desc,
			},
		})
	}
	return items
}

func descriptionOf(t *testing.T, body map[string]any) string {
	t.Helper()
	desc, ok := body["description"].(map[string]any)
	if !ok {
		t.Fatalf("body has no description object: %#v", body)
	}
	value, _ := desc["default_value"].(string)
	return value
}

func TestRegisterCommands_CreatesMissingAndPatchesChanged(t *testing.T) {
	s := newSlashCommandTestServer(t, slashCommandItems(t, map[string]string{
		"new":  "旧描述",           // changed → PATCH
		"list": "List sessions", // unchanged → skipped
	}))
	p := newSlashCommandPlatform(s.srv.URL, true)

	err := p.RegisterCommands([]core.BotCommandInfo{
		{Command: "new", Description: "Start a new session"},
		{Command: "/list", Description: "List sessions"},
		{Command: "stop", Description: "Stop the running turn"},
		{Command: "运行", Description: "中文命令名无法注册"}, // invalid name → skipped
	})
	if err != nil {
		t.Fatalf("RegisterCommands() error = %v", err)
	}

	if len(s.created) != 1 {
		t.Fatalf("created = %#v, want only 'stop'", s.created)
	}
	if got := s.created[0]["command"]; got != "stop" {
		t.Fatalf("created command = %v, want stop", got)
	}
	if got := descriptionOf(t, s.created[0]); got != "Stop the running turn" {
		t.Fatalf("created description = %q", got)
	}

	if len(s.patched) != 1 {
		t.Fatalf("patched = %#v, want only the stale 'new' description", s.patched)
	}
	if got := s.patched[0]["command_id"]; got != "cmd-new" {
		t.Fatalf("patched command_id = %v, want cmd-new", got)
	}
	if got := descriptionOf(t, s.patched[0]); got != "Start a new session" {
		t.Fatalf("patched description = %q, want the new text", got)
	}
	if s.rejected != 0 {
		t.Fatalf("server saw %d unexpected requests", s.rejected)
	}
}

func TestRegisterCommands_NoWritesWhenAlreadyInSync(t *testing.T) {
	s := newSlashCommandTestServer(t, slashCommandItems(t, map[string]string{
		"new":  "Start a new session",
		"help": "Show help",
	}))
	p := newSlashCommandPlatform(s.srv.URL, true)

	if err := p.RegisterCommands([]core.BotCommandInfo{
		{Command: "new", Description: "Start a new session"},
		{Command: "help", Description: "Show help"},
	}); err != nil {
		t.Fatalf("RegisterCommands() error = %v", err)
	}
	if len(s.created) != 0 || len(s.patched) != 0 || s.rejected != 0 {
		t.Fatalf("expected no writes, got created=%#v patched=%#v rejected=%d", s.created, s.patched, s.rejected)
	}
}

func TestRegisterCommands_DisabledSkipsNetwork(t *testing.T) {
	s := newSlashCommandTestServer(t, nil)
	p := newSlashCommandPlatform(s.srv.URL, false)

	if err := p.RegisterCommands([]core.BotCommandInfo{{Command: "new", Description: "x"}}); err != nil {
		t.Fatalf("RegisterCommands() error = %v", err)
	}
	if s.rejected != 0 || len(s.created) != 0 {
		t.Fatalf("disabled registration must not call the API: rejected=%d created=%#v", s.rejected, s.created)
	}
}

func TestRegisterCommands_SurfacesScopeRequirement(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			writeJSON(t, w, map[string]any{
				"code":                0,
				"msg":                 "success",
				"expire":              7200,
				"tenant_access_token": "slash-token",
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"code": 99991672,
			"msg":  "Access denied. One of the following scopes is required: [application:app_slash_command:write]",
		})
	}))
	defer srv.Close()

	p := newSlashCommandPlatform(srv.URL, true)
	err := p.RegisterCommands([]core.BotCommandInfo{{Command: "new", Description: "Start a new session"}})
	if err == nil {
		t.Fatal("expected a permission error")
	}
	if !strings.Contains(err.Error(), "app_slash_command:write") {
		t.Fatalf("error = %v, want the actionable scope hint", err)
	}
}

func TestNormalizeFeishuSlashCommandName(t *testing.T) {
	cases := map[string]string{
		"new":                   "new",
		"/Retry":                "retry",
		"  list_all ":           "list_all",
		"skill-x":               "skill-x",
		"运行":                    "",
		"-bad":                  "",
		"":                      "",
		strings.Repeat("a", 33): "",
	}
	for input, want := range cases {
		if got := normalizeFeishuSlashCommandName(input); got != want {
			t.Fatalf("normalizeFeishuSlashCommandName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRegisterCommands_PrunesCommandsNotAdvertised(t *testing.T) {
	s := newSlashCommandTestServer(t, slashCommandItems(t, map[string]string{
		"new":      "Start a new session",
		"compress": "Compress the context",
		"mode":     "Switch mode",
		"removed":  "stale entry",
	}))
	p := newSlashCommandPlatform(s.srv.URL, true)

	if err := p.RegisterCommands([]core.BotCommandInfo{
		{Command: "new", Description: "Start a new session"},
	}); err != nil {
		t.Fatalf("RegisterCommands() error = %v", err)
	}

	// Only the advertised command survives: capability-filtered and stale
	// entries are removed from the panel so users never see dead commands.
	if len(s.deleted) != 3 {
		t.Fatalf("deleted = %v, want 3 pruned commands", s.deleted)
	}
	for _, id := range s.deleted {
		if id == "cmd-new" {
			t.Fatalf("must keep advertised command, deleted = %v", s.deleted)
		}
	}
	if s.rejected != 0 {
		t.Fatalf("unexpected requests: %d", s.rejected)
	}
}
