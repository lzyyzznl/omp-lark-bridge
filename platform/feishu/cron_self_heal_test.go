package feishu

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

// cronSelfHealTestPlatform builds a Platform backed by an httptest server that
// stubs tenant token, contact user lookup and message create. The handler
// records the last message-create receive_id_type and body for assertions.
func cronSelfHealTestPlatform(t *testing.T, userOpenIDOK map[string]bool) (*Platform, *httptest.Server, *struct{ receiveType, receiveID string }) {
	t.Helper()
	var last struct{ receiveType, receiveID string }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"msg":"success","expire":7200,"tenant_access_token":"test-token"}`)
		case r.URL.Path == "/open-apis/im/v1/messages":
			last.receiveType = r.URL.Query().Get("receive_id_type")
			b, _ := io.ReadAll(r.Body)
			var body struct {
				ReceiveID string `json:"receive_id"`
			}
			_ = json.Unmarshal(b, &body)
			last.receiveID = body.ReceiveID
			_, _ = io.WriteString(w, `{"code":0,"msg":"success","data":{"message_id":"om_ok","chat_id":"oc_ok"}}`)
		case strings.HasPrefix(r.URL.Path, "/open-apis/contact/v3/users/"):
			openID := strings.TrimPrefix(r.URL.Path, "/open-apis/contact/v3/users/")
			if openID != "" {
				if idx := strings.Index(openID, "?"); idx >= 0 {
					openID = openID[:idx]
				}
			}
			ok, known := userOpenIDOK[openID]
			switch {
			case known && ok:
				_, _ = io.WriteString(w, `{"code":0,"msg":"success","data":{"user":{"open_id":"`+openID+`"}}}`)
			case known && !ok:
				_, _ = io.WriteString(w, `{"code":99992361,"msg":"open_id cross app"}`)
			default: // unknown open_id → contact permission error, caller should degrade
				_, _ = io.WriteString(w, `{"code":99991663,"msg":"no permission"}`)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	p := &Platform{
		platformName: "feishu",
		domain:       srv.URL,
		appID:        "cli_selfheal",
		appSecret:    "secret-selfheal",
		client: lark.NewClient("cli_selfheal", "secret-selfheal",
			lark.WithOpenBaseUrl(srv.URL),
			lark.WithHttpClient(srv.Client()),
		),
		replayClient: lark.NewClient("cli_selfheal", "secret-selfheal",
			lark.WithEnableTokenCache(false),
			lark.WithOpenBaseUrl(srv.URL),
			lark.WithHttpClient(srv.Client()),
		),
	}
	return p, srv, &last
}

func TestResolveCronReplyTarget_P2P_ReturnsOpenIDReplyContext(t *testing.T) {
	p, _, _ := cronSelfHealTestPlatform(t, map[string]bool{"ou_validuser": true})

	key := "feishu:oc_stale_chat:ou_validuser"
	resolvedKey, rctx, err := p.ResolveCronReplyTarget(key, "daily")
	if err != nil {
		t.Fatalf("ResolveCronReplyTarget error = %v", err)
	}
	if resolvedKey != key {
		t.Fatalf("resolvedKey = %q, want %q", resolvedKey, key)
	}
	rc, ok := rctx.(replyContext)
	if !ok {
		t.Fatalf("rctx type = %T, want replyContext", rctx)
	}
	if rc.receiveOpenID != "ou_validuser" {
		t.Fatalf("receiveOpenID = %q, want ou_validuser", rc.receiveOpenID)
	}
	if rc.chatID != "oc_stale_chat" {
		t.Fatalf("chatID = %q, want oc_stale_chat (kept for routing fallback)", rc.chatID)
	}
}

func TestResolveCronReplyTarget_CrossAppUser_Errors(t *testing.T) {
	p, _, _ := cronSelfHealTestPlatform(t, map[string]bool{"ou_foreign": false})

	_, _, err := p.ResolveCronReplyTarget("feishu:oc_chat:ou_foreign", "x")
	if err == nil {
		t.Fatal("expected error for cross-app open_id, got nil")
	}
	if !strings.Contains(err.Error(), "not a user of this app") {
		t.Fatalf("error = %q, want 'not a user of this app'", err.Error())
	}
}

func TestResolveCronReplyTarget_NonP2P_FallsBack(t *testing.T) {
	p, _, _ := cronSelfHealTestPlatform(t, nil)

	// thread-isolated session key → legacy chat_id path
	if _, _, err := p.ResolveCronReplyTarget("feishu:oc_group:root:om_root1", "x"); !isErrNotSupported(err) {
		t.Fatalf("thread key err = %v, want ErrNotSupported", err)
	}
	// unknown-prefix
	if _, _, err := p.ResolveCronReplyTarget("feishu:oc_chat:om_something", "x"); !isErrNotSupported(err) {
		t.Fatalf("om_ tail err = %v, want ErrNotSupported", err)
	}
	// wrong platform prefix
	if _, _, err := p.ResolveCronReplyTarget("other:oc_chat:ou_usr", "x"); !isErrNotSupported(err) {
		t.Fatalf("other platform err = %v, want ErrNotSupported", err)
	}
}

func TestCronSelfHeal_SendUsesOpenID_NotStaleChat(t *testing.T) {
	p, srv, last := cronSelfHealTestPlatform(t, map[string]bool{"ou_validuser": true})
	_ = srv

	key := "feishu:oc_stale_chat:ou_validuser"
	_, rctx, err := p.ResolveCronReplyTarget(key, "daily")
	if err != nil {
		t.Fatalf("resolve error = %v", err)
	}

	if err := p.Send(context.Background(), rctx, "self-healed delivery"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if last.receiveType != "open_id" {
		t.Fatalf("receive_id_type = %q, want open_id (self-healing must bypass stale chat_id)", last.receiveType)
	}
	if last.receiveID != "ou_validuser" {
		t.Fatalf("receive_id = %q, want ou_validuser", last.receiveID)
	}
}

func isErrNotSupported(err error) bool {
	if err == nil {
		return false
	}
	return strings.EqualFold(err.Error(), "operation not supported by this platform")
}
