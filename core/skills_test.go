package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var errTestSkillProvider = errors.New("skill provider failure")

// skillExecuteAgent is a full Agent (via resultAgent) that also lists skills.
type skillExecuteAgent struct {
	*resultAgent
	skills []Skill
}

func (a *skillExecuteAgent) ListSkills(_ context.Context) ([]Skill, error) { return a.skills, nil }

// waitForPrompts polls sentPrompts for n entries (the bridge sends async).
func waitForPrompts(s *resultAgentSession, n int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.sentPrompts) >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestHandleCommand_SkillInvocationForwardsOmpProtocol(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	agentSession := newResultAgentSession("done")
	ag := &skillExecuteAgent{
		resultAgent: &resultAgent{session: agentSession},
		skills:      []Skill{{Name: "edw-zmail", Description: "查邮件"}},
	}
	e := NewEngine("test", ag, []Platform{p}, "", LangEnglish)

	if handled := e.handleCommand(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"}, "/edw-zmail 查今天的邮件"); !handled {
		t.Fatal("skill slash command must be handled by the engine")
	}
	if !waitForPrompts(agentSession, 1) {
		t.Fatalf("agent never received a prompt: %v", agentSession.sentPrompts)
	}
	want := "/skill:edw-zmail 查今天的邮件"
	if got := agentSession.sentPrompts[0]; got != want {
		t.Errorf("agent prompt = %q, want %q", got, want)
	}
}

// skillStubAgent exposes a fixed skill list via SkillProvider.
type skillStubAgent struct {
	*stubAgent
	skills []Skill
	fail   error
}

func (a *skillStubAgent) ListSkills(_ context.Context) ([]Skill, error) {
	if a.fail != nil {
		return nil, a.fail
	}
	return a.skills, nil
}

func TestSplitSkillInvocation(t *testing.T) {
	tt := []struct {
		in         string
		name, args string
		ok         bool
	}{
		{"/edw-zmail 查邮件", "edw-zmail", "查邮件", true},
		{"/edw-zmail", "edw-zmail", "", true},
		{"  /skill  hello", "skill", "hello", true},
		{"plain text", "", "", false},
		{"/ a", "", "", false},
	}
	for _, tc := range tt {
		name, args, ok := splitSkillInvocation(tc.in)
		if ok != tc.ok || name != tc.name || args != tc.args {
			t.Errorf("splitSkillInvocation(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.in, name, args, ok, tc.name, tc.args, tc.ok)
		}
	}
}

func TestExpandSkillPrompt_UsesAgentSkills(t *testing.T) {
	ag := &skillStubAgent{skills: []Skill{
		{Name: "edw-zmail", Description: "查邮件"},
		{Name: "edw-kanban", Description: "看板", Hide: true},
	}}
	e := NewEngine("test", ag, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	got := e.expandSkillPrompt("/edw-zmail 查今天的邮件")
	want := "/skill:edw-zmail 查今天的邮件"
	if got != want {
		t.Errorf("expandSkillPrompt = %q, want %q", got, want)
	}

	// No args.
	if got := e.expandSkillPrompt("/edw-kanban"); got != "/skill:edw-kanban" {
		t.Errorf("expandSkillPrompt(no args) = %q, want /skill:edw-kanban", got)
	}

	// Unknown names and non-slash text pass through untouched.
	for _, in := range []string{"/no-such-skill foo", "summarize the logs", "/already skill:x 1"} {
		if e.expandSkillPrompt(in) != in {
			t.Errorf("expandSkillPrompt(%q) must pass through unchanged", in)
		}
	}
}

func TestExpandSkillPrompt_NoProviderPassesThrough(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	if got := e.expandSkillPrompt("/anything 1 2"); got != "/anything 1 2" {
		t.Errorf("expandSkillPrompt = %q, want passthrough when agent has no SkillProvider", got)
	}
}

func TestResolveSkill_HyphenUnderscoreEquivalent(t *testing.T) {
	ag := &skillStubAgent{skills: []Skill{{Name: "edw-zmail", Description: "邮件"}}}
	e := NewEngine("test", ag, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	for _, name := range []string{"edw-zmail", "edw_zmail", "EDW-ZMAIL", "EDW_ZMAIL"} {
		if _, ok := e.resolveSkill(context.Background(), name); !ok {
			t.Errorf("resolveSkill(%q) not found", name)
		}
	}
	if _, ok := e.resolveSkill(context.Background(), "nope"); ok {
		t.Errorf("resolveSkill(nope) should not match")
	}
}

func TestCmdSkills_PlainTextListsVisibleSkills(t *testing.T) {
	ag := &skillStubAgent{skills: []Skill{
		{Name: "edw-zmail", Description: "查邮件"},
		{Name: "hidden", Description: "不显示", Hide: true},
	}}
	e := NewEngine("test", ag, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	p := &stubPlatformEngine{n: "plain"}
	e.cmdSkills(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"})
	if len(p.sent) != 1 {
		t.Fatalf("sent = %d, want 1: %v", len(p.sent), p.sent)
	}
	if !strings.Contains(p.sent[0], "/edw-zmail") {
		t.Errorf("skills text missing /edw-zmail: %q", p.sent[0])
	}
	if strings.Contains(p.sent[0], "/hidden") {
		t.Errorf("skills text must omit hidden skill: %q", p.sent[0])
	}
}

func TestCmdSkills_EmptyWhenNoProvider(t *testing.T) {
	e := NewEngine("test", &stubAgent{}, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	p := &stubPlatformEngine{n: "plain"}
	e.cmdSkills(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"})
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "No skills found") {
		t.Errorf("expected empty-skills reply, got: %v", p.sent)
	}
}

func TestCommandSupported_SkillsRequiresProvider(t *testing.T) {
	if NewEngine("test", &stubAgent{}, nil, "", LangEnglish).commandSupported("skills") {
		t.Error("skills must be unsupported when agent has no SkillProvider")
	}
	ag := &skillStubAgent{skills: []Skill{{Name: "x", Description: "y"}}}
	if !NewEngine("test", ag, nil, "", LangEnglish).commandSupported("skills") {
		t.Error("skills must be supported when agent provides skills")
	}
}

func TestSkillProviderError_ReturnsEmptyList(t *testing.T) {
	ag := &skillStubAgent{fail: errTestSkillProvider}
	e := NewEngine("test", ag, []Platform{&stubPlatformEngine{n: "test"}}, "", LangEnglish)
	p := &stubPlatformEngine{n: "plain"}
	e.cmdSkills(p, &Message{SessionKey: "test:user1", ReplyCtx: "ctx"})
	if len(p.sent) != 1 || !strings.Contains(p.sent[0], "No skills found") {
		t.Errorf("provider failure must degrade to empty list, got: %v", p.sent)
	}
}
