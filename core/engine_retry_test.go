package core

import (
	"strings"
	"testing"
	"time"
)

func TestIsRetryCommand(t *testing.T) {
	for _, input := range []string{"/retry", "/RETRY", "  /Retry  ", "/regenerate"} {
		if !isRetryCommand(input) {
			t.Fatalf("isRetryCommand(%q) = false, want true", input)
		}
	}
	for _, input := range []string{"retry", "/retrying", "", "/model"} {
		if isRetryCommand(input) {
			t.Fatalf("isRetryCommand(%q) = true, want false", input)
		}
	}
}

func TestLastRetryableUserMessage(t *testing.T) {
	if got := lastRetryableUserMessage(nil); got != "" {
		t.Fatalf("nil session = %q, want empty", got)
	}

	empty := &Session{}
	if got := lastRetryableUserMessage(empty); got != "" {
		t.Fatalf("empty history = %q, want empty", got)
	}

	session := &Session{}
	session.AddHistory("user", "调研一下 deepseek 的定价")
	session.AddHistory("assistant", "好的，正在调研")
	session.AddHistory("user", "/model")
	session.AddHistory("assistant", "已切换模型")
	if got := lastRetryableUserMessage(session); got != "调研一下 deepseek 的定价" {
		t.Fatalf("lastRetryableUserMessage = %q, want the last real prompt (slash commands skipped)", got)
	}

	session.AddHistory("user", "  再补一个表格  ")
	session.AddHistory("assistant", "表格如下")
	if got := lastRetryableUserMessage(session); got != "再补一个表格" {
		t.Fatalf("lastRetryableUserMessage = %q, want the trimmed latest prompt", got)
	}
}

func TestHandleMessage_RetryReplaysLastUserPrompt(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agentSession := newResultAgentSession("重试后的回答")
	e := NewEngine("test", &resultAgent{session: agentSession}, []Platform{p}, "", LangEnglish)
	defer e.cancel()

	key := "test:u1"
	active := e.sessions.GetOrCreateActive(key)
	active.AddHistory("user", "帮我看看这个方案")
	active.AddHistory("assistant", "看完了")

	e.handleMessage(p, &Message{
		SessionKey: key,
		Platform:   "test",
		UserID:     "u1",
		UserName:   "user",
		Content:    "/retry",
		ReplyCtx:   "ctx",
	})

	deadline := time.After(3 * time.Second)
	for len(p.getSent()) == 0 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for the retried turn; sent=%#v prompts=%v", p.getSent(), agentSession.getSentPrompts())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if got := agentSession.getSentPrompts(); len(got) != 1 {
		t.Fatalf("prompts = %#v, want exactly one retried prompt", got)
	}
	got := agentSession.getSentPrompts()[0]
	if !strings.Contains(got, "帮我看看这个方案") {
		t.Fatalf("retried prompt = %q, want the previous user message", got)
	}
	if strings.Contains(got, "/retry") {
		t.Fatalf("retried prompt = %q, must not forward the /retry command verbatim", got)
	}
}

func TestHandleMessage_RetryWithoutHistoryRepliesNothingToRetry(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agentSession := newResultAgentSession("unused")
	e := NewEngine("test", &resultAgent{session: agentSession}, []Platform{p}, "", LangEnglish)
	defer e.cancel()

	e.handleMessage(p, &Message{
		SessionKey: "test:u2",
		Platform:   "test",
		UserID:     "u2",
		UserName:   "user",
		Content:    "/retry",
		ReplyCtx:   "ctx",
	})

	sent := p.getSent()
	if len(sent) != 1 || !strings.Contains(sent[0], "Nothing to retry") {
		t.Fatalf("sent = %#v, want a single 'nothing to retry' reply", sent)
	}
	if len(agentSession.getSentPrompts()) != 0 {
		t.Fatalf("prompts = %#v, want none (no history to replay)", agentSession.getSentPrompts())
	}
}
