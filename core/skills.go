package core

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// skillCacheTTL bounds how long the engine reuses the agent's skill list so
// resolving a skill slash command doesn't hit the agent's discovery (omp spawn)
// on every message.
const skillCacheTTL = 60 * time.Second

// skillsFromProvider returns the agent's skills, lazily fetched via
// SkillProvider and cached. Returns nil when the agent exposes no provider or
// discovery fails (the bridge degrades to "no skills" instead of breaking flow).
func (e *Engine) skillsFromProvider(ctx context.Context) []Skill {
	sp, ok := e.agent.(SkillProvider)
	if !ok {
		return nil
	}
	e.skillsMu.Lock()
	defer e.skillsMu.Unlock()
	if e.skills != nil && time.Since(e.skillsAt) < skillCacheTTL {
		return e.skills
	}
	skills, err := sp.ListSkills(ctx)
	if err != nil {
		slog.Warn("skill discovery failed", "agent", e.agent.Name(), "error", err)
		return nil
	}
	e.skills = skills
	e.skillsAt = time.Now()
	return skills
}

// visibleSkills returns non-hidden skills sorted by name for listings.
func (e *Engine) visibleSkills(ctx context.Context) []Skill {
	var out []Skill
	for _, s := range e.skillsFromProvider(ctx) {
		if !s.Hide {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// resolveSkill looks up a skill by its slash-command name, folding case and
// treating hyphens/underscores as equivalent (see normalizeCommandName).
func (e *Engine) resolveSkill(ctx context.Context, name string) (Skill, bool) {
	norm := normalizeCommandName(strings.TrimPrefix(name, "skill:"))
	for _, s := range e.skillsFromProvider(ctx) {
		if normalizeCommandName(s.Name) == norm {
			return s, true
		}
	}
	return Skill{}, false
}

// splitSkillInvocation splits "/name arg1 arg2" into ("name", "arg1 arg2").
// Returns ok=false for plain text or malformed invocations.
func splitSkillInvocation(text string) (name, args string, ok bool) {
	trimmed := strings.TrimLeft(text, " \t")
	if !strings.HasPrefix(trimmed, "/") || len(trimmed) < 2 {
		return "", "", false
	}
	rest := trimmed[1:]
	name, tail, _ := strings.Cut(rest, " ")
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "/\t") {
		return "", "", false
	}
	return name, strings.TrimSpace(tail), true
}

// expandSkillPrompt rewrites a leading "/<skill> <args>" invocation to the
// agent's native "/skill:<skill> <args>" protocol so cron/timer jobs trigger a
// skill exactly the way a chat message would. Unknown names pass through.
func (e *Engine) expandSkillPrompt(content string) string {
	name, args, ok := splitSkillInvocation(content)
	if !ok {
		return content
	}
	skill, found := e.resolveSkill(context.Background(), name)
	if !found {
		return content
	}
	out := "/skill:" + skill.Name
	if args != "" {
		out += " " + args
	}
	return out
}

// executeSkillCommand forwards "/skill:<name> <args>" to the agent — the
// agent's native skill invocation protocol. The agent builds the skill prompt
// from its own skill store; the bridge never reads the skill body.
func (e *Engine) executeSkillCommand(p Platform, msg *Message, skill Skill, args []string) {
	prompt := "/skill:" + skill.Name
	if len(args) > 0 {
		prompt += " " + strings.Join(args, " ")
	}

	agent, sessions, interactiveKey, workspaceDir, err := e.commandContextWithWorkspace(p, msg)
	if err != nil {
		e.reply(p, msg.ReplyCtx, e.i18n.Tf(MsgWsResolutionError, err))
		return
	}
	session := sessions.GetOrCreateActive(interactiveKey)
	if !session.TryLock() {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgPreviousProcessing))
		return
	}
	slog.Info("executing skill",
		"skill", skill.Name,
		"user", msg.UserName,
		"workspace", workspaceDir,
	)

	msg.Content = prompt
	go e.processInteractiveMessageWith(p, msg, session, agent, sessions, interactiveKey, workspaceDir, msg.SessionKey)
}

// cmdSkills lists the agent's skills as text or a card.
func (e *Engine) cmdSkills(p Platform, msg *Message) {
	skills := e.visibleSkills(context.Background())
	if len(skills) == 0 {
		e.reply(p, msg.ReplyCtx, e.i18n.T(MsgSkillsEmpty))
		return
	}
	if !supportsCards(p) {
		var sb strings.Builder
		sb.WriteString(e.i18n.Tf(MsgSkillsTitle, e.agent.Name(), len(skills)))
		for _, s := range skills {
			sb.WriteString(fmt.Sprintf("/%s — %s\n", s.Name, s.Description))
		}
		sb.WriteString(e.i18n.T(MsgSkillsHint))
		e.reply(p, msg.ReplyCtx, sb.String())
		return
	}
	e.replyWithCard(p, msg.ReplyCtx, e.renderSkillsCard(skills))
}

// renderSkillsCard renders the /skills listing card.
func (e *Engine) renderSkillsCard(skills []Skill) *Card {
	cb := NewCard().Title(e.i18n.T(MsgCardTitleSkills), "purple")
	cb.Markdown(e.i18n.Tf(MsgSkillsTitle, e.agent.Name(), len(skills)))
	for _, s := range skills {
		desc := strings.TrimSpace(s.Description)
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		cb.Markdown(fmt.Sprintf("**/%s**\n%s", s.Name, desc))
	}
	cb.Note(e.i18n.T(MsgSkillsHint))
	return cb.Build()
}
