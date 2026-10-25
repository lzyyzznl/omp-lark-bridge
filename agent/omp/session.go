package omp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lzyyzznl/omp-lark-bridge/core"
)

const (
	defaultSessionName = "default"
	readyTimeout       = 15 * time.Second
	maxFrameBytes      = 1 << 20 // omp v1 stdout frame limit (1 MiB)
)

// ompSession is a long-lived `omp --mode rpc-ui` subprocess bound to one chat
// session. It speaks omp's JSONL RPC protocol (see omp://rpc.md).
type ompSession struct {
	ctx    context.Context
	cancel context.CancelFunc
	bin    string
	args   []string
	env    []string
	dir    string

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser

	ready    chan struct{}
	readyErr error

	events chan core.Event
	alive  atomic.Bool

	sessionDir  string // --session-dir container handed to omp (stable per project)
	ompSessID   string // omp-side session id (from get_state)
	ompSessFile string // absolute .jsonl path; the value persisted as AgentSessionID

	// delegatedAgent is the cron-assigned subagent name. omp has no RPC command
	// to select an agent, so Send injects a delegation instruction into this
	// session's first prompt (main session asks its `task` tool to use the
	// named specialist). One-shot per session; only cron jobs set it.
	delegatedAgent string
	agentUsed      bool

	writeMu sync.Mutex
	pMu     sync.Mutex
	pending map[string]chan map[string]any

	// asks holds open extension UI dialogs (omp's `ask` tool, plus `select`)
	// keyed by the request id, so RespondPermission can translate the user's
	// card answer into an extension_ui_response.
	askMu sync.Mutex
	asks  map[string]*ompPendingAsk

	// thinking accumulation: engine's EventThinking handler overwrites the
	// card's thinking area with event.Content (never appends), so deltas must
	// be accumulated and emitted at a human cadence — otherwise the card shows
	// only the last 1-2 chars per delta ("one char per line").
	thinkMu      sync.Mutex
	thinkBuf     strings.Builder
	thinkEmitted bool
	thinkLast    time.Time
}

// ompSessionStoreDir returns the store container under the cc-data dir. The name
// is deliberately independent of the agent session id (see newOmpSession):
// per-conversation identity lives in the session file, not the directory.
func ompSessionStoreDir(ccDataDir, _ string) string {
	return filepath.Join(ompSessionsRoot(ccDataDir), defaultSessionName)
}

func ompSessionsRoot(ccDataDir string) string {
	if ccDataDir != "" {
		return filepath.Join(ccDataDir, "omp-sessions")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".omp-lark-bridge", "omp-sessions")
	}
	return ".omp-lark-bridge-omp-sessions"
}

func sanitizeSessionName(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return defaultSessionName
	}
	var b strings.Builder
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if s := b.String(); s != "" {
		return s
	}
	return defaultSessionName
}

func newOmpSession(ctx context.Context, bin string, extraArgs []string, workDir, model, thinking, delegatedAgent, ccDataDir, sessionID string, env []string, systemPrompt string) (*ompSession, error) {
	// spawnOmpProcess derives its own cancellable context (the returned session
	// owns it via ompSession.cancel), so this function needs no extra layer.
	// On enter failure we cancel that process context to tear the child down.
	s, err := spawnOmpProcess(ctx, bin, extraArgs, workDir, ccDataDir, env, systemPrompt)
	if err != nil {
		return nil, err
	}

	// Begin/resume the omp-side session inside the running process. sessionID
	// is whatever CurrentSessionID() reported last time: an absolute session file
	// path (current builds) or a bare id / directory name (legacy).
	if err := s.enterSession(model, thinking, sessionID); err != nil {
		s.cancel()
		return nil, err
	}

	// Ask omp to emit one dialog per `ask` tool call instead of one select per
	// option (see omp://rpc.md `set_ask_dialog`). Failure only costs the nicer
	// shape: the select path below still renders questions as cards.
	go s.enableAskDialog()

	s.delegatedAgent = delegatedAgent
	return s, nil
}

// enterSession resumes the conversation identified by resumeTarget, or starts a
// fresh omp session, then records the omp-side id and session file path.
//
// `new_session`'s response carries only {cancelled} (verified on omp 18.12.6), so
// the id/file must come from get_state — without that this adapter persisted an
// empty AgentSessionID and every daemon restart silently began a blank
// conversation.
func (s *ompSession) enterSession(model, thinking, resumeTarget string) error {
	kind, value := planEnterSession(resumeTarget, s.sessionDir)
	if err := s.enterWithFallback(kind, value); err != nil {
		return err
	}
	s.recordState()

	if model != "" {
		pid := uuid.NewString()
		mp := map[string]any{"id": pid, "type": "set_model"}
		if provider, mdl, ok := splitModel(model); ok {
			mp["provider"], mp["modelId"] = provider, mdl
		} else {
			mp["modelId"] = model
		}
		if err := s.writeFrame(mp); err != nil {
			return err
		}
		// A missing/invalid model is not fatal: omp rejects it with
		// `Model not found: <provider>/<id>` — warn and keep the session on its
		// default model so the job still runs (cron requirement).
		if _, err := s.waitResponse(pid); err != nil {
			slog.Warn("omp: set_model failed, falling back to default model", "model", model, "error", err)
		}
	}
	if thinking != "" {
		// set_thinking_level is lenient (verified on omp 18.12.6: unsupported
		// levels are accepted and clamped), so no response check needed.
		if err := s.writeFrame(map[string]any{"id": uuid.NewString(), "type": "set_thinking_level", "level": thinking}); err != nil {
			return err
		}
	}
	return nil
}

// planEnterSession decides how to bind the process to a conversation:
//   - "switch_session" for a session file path that still exists (exact resume)
//   - "open_session"   for a legacy bare dir name under the store root
//   - "new_session"    otherwise ("" target, "/new", or a vanished file)
func planEnterSession(resumeTarget, sessionDir string) (kind, value string) {
	target := strings.TrimSpace(resumeTarget)
	if target == "" {
		return "new_session", ""
	}
	if strings.HasSuffix(strings.ToLower(target), ".jsonl") {
		if _, err := os.Stat(target); err != nil {
			return "new_session", ""
		}
		return "switch_session", target
	}
	legacyDir := filepath.Join(filepath.Dir(sessionDir), sanitizeSessionName(target))
	if dirHasSessionData(legacyDir) {
		return "open_session", legacyDir
	}
	return "new_session", ""
}

// enterWithFallback performs the planned enter command and degrades to a fresh
// session when resume is refused (deleted/foreign session file, model gone).
func (s *ompSession) enterWithFallback(kind, value string) error {
	for attempt := 0; attempt < 2; attempt++ {
		id := uuid.NewString()
		payload := map[string]any{"id": id, "type": kind}
		switch kind {
		case "switch_session":
			payload["sessionPath"] = value
		case "open_session":
			payload["sessionDir"] = value
		}
		if err := s.writeFrame(payload); err != nil {
			return err
		}
		if _, err := s.waitResponse(id); err == nil {
			return nil
		} else if kind == "new_session" {
			return err
		} else {
			slog.Warn("omp: resume refused, starting a fresh session", "kind", kind, "target", value, "error", err)
			kind, value = "new_session", ""
		}
	}
	return nil
}

// recordState reads get_state and remembers the omp session id + file path.
func (s *ompSession) recordState() {
	id := uuid.NewString()
	if err := s.writeFrame(map[string]any{"id": id, "type": "get_state"}); err != nil {
		slog.Debug("omp: get_state write failed", "error", err)
		return
	}
	resp, err := s.waitResponse(id)
	if err != nil {
		slog.Debug("omp: get_state failed", "error", err)
		return
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		return
	}
	if sid, _ := data["sessionId"].(string); sid != "" {
		s.ompSessID = sid
	}
	if file, _ := data["sessionFile"].(string); file != "" {
		s.ompSessFile = file
	}
	if s.ompSessID == "" {
		s.ompSessID = s.ompSessFile
	}
}

// waitResponse registers a pending command id and blocks for omp's `response`
// frame carrying the same id (rpc.md:306-309).
func (s *ompSession) waitResponse(id string) (map[string]any, error) {
	ch := make(chan map[string]any, 1)
	s.pMu.Lock()
	s.pending[id] = ch
	s.pMu.Unlock()
	defer func() {
		s.pMu.Lock()
		delete(s.pending, id)
		s.pMu.Unlock()
	}()

	select {
	case resp := <-ch:
		if ok, _ := resp["success"].(bool); !ok {
			msg, _ := resp["error"].(string)
			if msg == "" {
				msg = "command rejected"
			}
			return nil, fmt.Errorf("omp: %s: %s", resp["command"], msg)
		}
		return resp, nil
	case <-s.ctx.Done():
		return nil, fmt.Errorf("omp: interrupted waiting for response: %w", s.ctx.Err())
	}
}

// -- protocol wire --

func (s *ompSession) writeFrame(f map[string]any) error {
	data, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("omp: encode frame: %w", err)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("omp: write frame: %w", err)
	}
	return nil
}

// Send delivers a user message to the running omp session. Images and files
// are persisted to disk and referenced by path in the prompt (same pattern as
// the ACP agent) rather than guessing omp's binary ImageContent encoding.
func (s *ompSession) Send(prompt string, messageID string, images []core.ImageAttachment, files []core.FileAttachment) error {
	if !s.alive.Load() {
		return fmt.Errorf("omp: session closed")
	}
	// Delegated-agent cron jobs wrap their very first message in a delegation
	// instruction so the main session routes the work to the named specialist
	// (omp's native "ask the session to use the agent by name" flow). Injected
	// exactly once, before the message enters the session, so resume and later
	// turns are unaffected.
	if s.delegatedAgent != "" && !s.agentUsed {
		s.agentUsed = true
		prompt = fmt.Sprintf(delegationTemplate(s.delegatedAgent), prompt)
	}
	filePaths := core.SaveFilesToDisk(s.dir, messageID, files)
	prompt = core.AppendFileRefs(prompt, filePaths)
	if len(images) > 0 {
		prompt = s.appendImageRefs(prompt, messageID, images)
	}

	return s.writeFrame(map[string]any{
		"id":      uuid.NewString(),
		"type":    "prompt",
		"message": prompt,
	})
}

// delegationTemplate wraps a cron job's first prompt so the main omp session
// routes the work to the named specialist (omp's native "use the agent by
// name" flow). The returned string has a single %s for the task body.
func delegationTemplate(agent string) string {
	return "本任务已指派给专用子代理 " + agent + " 执行。请使用 " + agent + " 专用子代理完成以下任务,完成后把它的结论完整汇报给用户。不要以主会话身份直接回答。\n\n任务内容:\n%s"
}

func (s *ompSession) appendImageRefs(prompt, messageID string, images []core.ImageAttachment) string {
	var refs []string
	for i, img := range images {
		path := s.saveImage(img, messageID, i)
		if path != "" {
			refs = append(refs, path)
		}
	}
	if len(refs) == 0 {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		prompt = "User sent image(s)."
	}
	return prompt + "\n\n(Image files saved locally: " + strings.Join(refs, ", ") + ")"
}

func (s *ompSession) saveImage(img core.ImageAttachment, messageID string, idx int) string {
	if len(img.Data) == 0 {
		return ""
	}
	dir := filepath.Join(s.dir, ".omp-lark-bridge", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("omp: mkdir images failed", "error", err)
		return ""
	}
	name := fmt.Sprintf("%s-%d%s", filepath.Base(messageID), idx, extForMime(img.MimeType))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, img.Data, 0o644); err != nil {
		slog.Warn("omp: save image failed", "error", err)
		return ""
	}
	return path
}

func extForMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".img"
	}
}

// CancelTurn aborts the running turn without terminating the session
// (core.AgentSessionCanceller, /stop).
func (s *ompSession) CancelTurn() error {
	if !s.alive.Load() {
		return nil
	}
	return s.writeFrame(map[string]any{"type": "abort"})
}

// RespondPermission answers an open extension UI dialog. Tool approvals never
// reach here (yolo mode); the dialogs that do are omp's `ask` tool and its
// per-option `select` fallback, whose card answers the engine collected.
func (s *ompSession) RespondPermission(requestID string, result core.PermissionResult) error {
	pending := s.takeAsk(requestID)
	if pending == nil {
		return nil
	}
	if result.Behavior == "deny" {
		return s.writeFrame(map[string]any{
			"type":      "extension_ui_response",
			"id":        requestID,
			"cancelled": true,
		})
	}
	switch pending.kind {
	case "select":
		answer := askAnswerText(pending.questions[0], result.UpdatedInput)
		return s.writeFrame(map[string]any{
			"type":  "extension_ui_response",
			"id":    requestID,
			"value": answer,
		})
	default:
		answers := make([]map[string]any, 0, len(pending.questions))
		for _, q := range pending.questions {
			selected, custom := splitAskAnswer(q, askAnswerText(q, result.UpdatedInput))
			entry := map[string]any{"id": q.id, "selectedOptions": selected}
			if custom != "" {
				entry["customInput"] = custom
			}
			answers = append(answers, entry)
		}
		return s.writeFrame(map[string]any{
			"type":    "extension_ui_response",
			"id":      requestID,
			"answers": answers,
		})
	}
}

func (s *ompSession) Events() <-chan core.Event { return s.events }

// CurrentSessionID is persisted by the engine as Session.AgentSessionID and fed
// back into StartSession, so it must be the value that identifies the
// conversation across process restarts: the session file path when known.
func (s *ompSession) CurrentSessionID() string {
	if s.ompSessFile != "" {
		return s.ompSessFile
	}
	return s.ompSessID
}

func (s *ompSession) Alive() bool { return s.alive.Load() }

func (s *ompSession) Close() error {
	// Closing stdin tells omp to drain, dispose and exit 0. It will also reject
	// any pending UI/host-tool requests.
	if s.alive.Load() {
		_ = s.stdin.Close()
	}
	select {
	case <-s.ctx.Done():
	case <-time.After(5 * time.Second):
		s.cancel()
	}
	return nil
}

// -- background loops --

func (s *ompSession) readLoop(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), maxFrameBytes*2)
	sc.Split(bufio.ScanLines)

	for sc.Scan() {
		if !s.alive.Load() {
			return
		}
		var frame map[string]any
		if err := json.Unmarshal(sc.Bytes(), &frame); err != nil {
			slog.Debug("omp: bad frame", "err", err, "line", truncateLine(sc.Bytes()))
			continue
		}
		typ, _ := frame["type"].(string)
		switch typ {
		case "ready":
			s.readyErr = nil
			close(s.ready)
		case "message_update":
			s.handleMessageUpdate(frame)
		case "tool_execution_start":
			s.handleToolStart(frame)
		case "tool_execution_end":
			s.handleToolEnd(frame)
		case "prompt_result":
			s.handlePromptResult(frame)
		case "message_start", "message_end", "agent_start", "agent_end",
			"turn_start", "turn_end", "session_settled", "queue_update",
			"auto_compaction_start", "auto_compaction_end":
			// lifecycle frames with no user-visible payload
		case "response":
			rid, _ := frame["id"].(string)
			if rid != "" {
				s.pMu.Lock()
				ch, ok := s.pending[rid]
				s.pMu.Unlock()
				if ok {
					ch <- frame
					break
				}
			}
			if ok, _ := frame["success"].(bool); !ok {
				slog.Warn("omp: command failed", "error", frame["error"], "command", frame["command"])
			}
		case "extension_ui_request":
			s.handleUIRequest(frame)
		case "notice":
			slog.Warn("omp: notice", "message", frame["message"])
		case "rpc_frame_error":
			slog.Warn("omp: rpc frame error", "error", frame["error"])
		default:
			slog.Debug("omp: unhandled frame", "type", typ)
		}
	}
	if err := sc.Err(); err != nil && s.alive.Load() {
		slog.Warn("omp: stdout read error", "err", err)
	}
}

func (s *ompSession) drainStderr() {
	sc := bufio.NewScanner(s.stderr)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		slog.Debug("omp: stderr", "line", truncateLine(sc.Bytes()))
	}
}

func (s *ompSession) waitExit() {
	err := s.cmd.Wait()
	s.alive.Store(false)
	select {
	case <-s.ready:
	default:
		close(s.ready) // unblock a straggler still waiting on the handshake
	}
	if err != nil && s.ctx.Err() == nil {
		slog.Warn("omp: process exited", "err", err)
	}
	// Signal turn end if nothing was emitted (process died mid-turn).
	select {
	case s.events <- core.Event{Type: core.EventResult, Done: true, Content: ""}:
	default:
	}
}

// -- frame handlers --

func (s *ompSession) handleMessageUpdate(frame map[string]any) {
	aeRaw, ok := frame["assistantMessageEvent"]
	if !ok {
		return
	}
	ae, ok := aeRaw.(map[string]any)
	if !ok {
		return
	}
	switch t, _ := ae["type"].(string); t {
	case "text_delta":
		if d, _ := ae["delta"].(string); d != "" {
			emitSafe(s.events, core.Event{Type: core.EventText, Content: d})
		}
	case "thinking_delta":
		if d, _ := ae["delta"].(string); d != "" {
			s.thinkMu.Lock()
			s.thinkBuf.WriteString(d)
			s.thinkMu.Unlock()
			s.flushThinking(false)
		}
	case "toolcall_delta":
		// tool call arguments stream in; surface the action name on start only
	default:
		slog.Debug("omp: assistantMessageEvent", "type", t)
	}
}

// flushThinking emits the accumulated thinking text as one EventThinking
// whose Content is the full accumulated text so far. force bypasses the
// throttle (used before turn end so the finished card holds the complete
// thinking). The first thinking of a turn is always emitted immediately so
// the card appears right away.
func (s *ompSession) flushThinking(force bool) {
	s.thinkMu.Lock()
	defer s.thinkMu.Unlock()
	if s.thinkBuf.Len() == 0 {
		return
	}
	if !force && s.thinkEmitted && time.Since(s.thinkLast) < 1500*time.Millisecond {
		return
	}
	s.thinkEmitted = true
	s.thinkLast = time.Now()
	emitSafe(s.events, core.Event{Type: core.EventThinking, Content: s.thinkBuf.String()})
}

// resetThinking clears accumulated thinking; called when a turn settles.
func (s *ompSession) resetThinking() {
	s.thinkMu.Lock()
	s.thinkBuf.Reset()
	s.thinkEmitted = false
	s.thinkLast = time.Time{}
	s.thinkMu.Unlock()
}

func (s *ompSession) handleToolStart(frame map[string]any) {
	name := toolFrameName(frame)
	args, _ := frame["args"].(map[string]any)
	summary := toolInputSummary(args)
	if summary == "" {
		// Some tools carry no args object; omp's `intent` is then the only
		// human-readable description of the call.
		summary = strField(frame, "intent")
	}
	emitSafe(s.events, core.Event{
		Type:      core.EventToolUse,
		ToolName:  name,
		ToolInput: summary,
	})
}

func (s *ompSession) handleToolEnd(frame map[string]any) {
	result := frame["result"]
	status := "completed"
	success := true
	if isError, _ := frame["isError"].(bool); isError {
		status = "failed"
		success = false
	}
	emitSafe(s.events, core.Event{
		Type:         core.EventToolResult,
		ToolName:     toolFrameName(frame),
		ToolResult:   toolResultText(result),
		ToolStatus:   status,
		ToolSuccess:  &success,
		ToolExitCode: toolResultExitCode(result),
	})
}

// toolFrameName reads the tool name from a tool_execution_* frame. Current omp
// builds send `toolName`; older ones used `name`.
func toolFrameName(frame map[string]any) string {
	if name := strField(frame, "name"); name != "" {
		return name
	}
	return strField(frame, "toolName")
}

const (
	// maxToolArgValueRunes bounds one string argument (a `command`, a file
	// body, …) so a single huge value cannot swallow the whole summary.
	maxToolArgValueRunes = 160
	// maxToolInputRunes keeps the marshalled args JSON below the engine's
	// default tool_max_len (500), so the platform can still parse it as JSON
	// and extract the interesting key (command / file path / pattern).
	maxToolInputRunes = 420
)

// toolInputSummary renders the tool_execution_start `args` object as compact
// JSON. The platform layer extracts a human-readable detail (command, path,
// pattern…) from well-known keys inside that JSON, so the payload must stay
// valid JSON: long values are re-truncated at tighter limits instead of cutting
// the marshalled tail.
func toolInputSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, limit := range []int{maxToolArgValueRunes, 80, 40, 20} {
		b, err := json.Marshal(pruneToolArgs(args, limit))
		if err != nil {
			return ""
		}
		if utf8.RuneCount(b) <= maxToolInputRunes || limit == 20 {
			return string(b)
		}
	}
	return ""
}

func pruneToolArgs(args map[string]any, limit int) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = pruneToolArgValue(v, limit)
	}
	return out
}

func pruneToolArgValue(v any, limit int) any {
	switch val := v.(type) {
	case string:
		return truncateRunes(val, limit)
	case []any:
		if len(val) > 4 {
			val = val[:4]
		}
		out := make([]any, 0, len(val))
		for _, item := range val {
			out = append(out, pruneToolArgValue(item, limit))
		}
		return out
	case map[string]any:
		return pruneToolArgs(val, limit)
	default:
		return v
	}
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

// toolResultText flattens omp's tool_execution_end `result` into the plain text
// the chat card shows: either the text blocks of `content` or a scalar output
// field. Non-text content (images, structured details) is dropped.
func toolResultText(result any) string {
	switch r := result.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(r)
	case map[string]any:
		if text := toolResultContentText(r["content"]); text != "" {
			return text
		}
		for _, key := range []string{"text", "output", "stdout", "message"} {
			if s := strings.TrimSpace(strField(r, key)); s != "" {
				return s
			}
		}
	}
	return ""
}

func toolResultContentText(content any) string {
	switch c := content.(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		parts := make([]string, 0, len(c))
		for _, item := range c {
			block, ok := item.(map[string]any)
			if !ok || strField(block, "type") != "text" {
				continue
			}
			if text := strings.TrimSpace(strField(block, "text")); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// toolResultExitCode pulls a process exit code out of result.details or the
// result root, when the tool (bash and friends) reports one.
func toolResultExitCode(result any) *int {
	root, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	sources := []map[string]any{root}
	if details, ok := root["details"].(map[string]any); ok {
		sources = append([]map[string]any{details}, sources...)
	}
	for _, src := range sources {
		for _, key := range []string{"exitCode", "exit_code"} {
			switch v := src[key].(type) {
			case float64:
				code := int(v)
				return &code
			case int:
				code := v
				return &code
			}
		}
	}
	return nil
}

func (s *ompSession) handlePromptResult(frame map[string]any) {
	// final flush so the settled card carries the complete thinking, then
	// reset for the next turn.
	s.flushThinking(true)
	s.resetThinking()
	status, _ := frame["status"].(string)
	switch status {
	case "completed":
		emitSafe(s.events, core.Event{Type: core.EventResult, Done: true, SessionID: s.ompSessID})
	case "aborted":
		emitSafe(s.events, core.Event{Type: core.EventResult, Done: true, SessionID: s.ompSessID})
	case "error":
		var err error
		if em, ok := frame["error"].(map[string]any); ok {
			if msg, _ := em["message"].(string); msg != "" {
				err = fmt.Errorf("omp: %s", msg)
			}
		}
		if err == nil {
			err = fmt.Errorf("omp: turn failed")
		}
		emitSafe(s.events, core.Event{Type: core.EventError, Error: err, Done: true})
	default:
		slog.Debug("omp: prompt_result", "status", status, "agentInvoked", frame["agentInvoked"])
	}
}

// handleUIRequest answers omp's out-of-band UI requests. Under yolo there are
// no approval dialogs; omp still emits informational ones (setStatus/notify —
// fine to ignore). Only a `select` request (approval) is answered with Deny so
// the pipe never deadlocks if the mode ever changes.
func (s *ompSession) handleUIRequest(frame map[string]any) {
	method, _ := frame["method"].(string)
	id, _ := frame["id"].(string)
	switch method {
	case "ask":
		s.handleAskDialog(id, frame)
	case "select":
		s.handleSelectDialog(id, frame)
	case "cancel":
		// omp timed out or aborted its own dialog; the answer is no longer wanted.
		if target, _ := frame["targetId"].(string); target != "" {
			s.forgetAsk(target)
		}
	case "notify", "setStatus", "setWidget", "setTitle", "set_editor_text", "open_url":
		// presentation-only frames need no response
	default:
		// confirm/input/editor have no card surface here; cancelling keeps the
		// agent from blocking on a dialog the host cannot show.
		if id != "" {
			slog.Debug("omp: cancelling unsupported ui request", "method", method, "id", id)
			_ = s.writeFrame(map[string]any{"type": "extension_ui_response", "id": id, "cancelled": true})
		}
	}
}

// enableAskDialog opts this process into the combined `ask` dialog. Builds
// without the command answer with a failure, which is fine: the `select`
// fallback below still renders questions.
func (s *ompSession) enableAskDialog() {
	id := uuid.NewString()
	if err := s.writeFrame(map[string]any{
		"id":      id,
		"type":    "set_ask_dialog",
		"enabled": true,
	}); err != nil {
		slog.Debug("omp: set_ask_dialog write failed", "error", err)
		return
	}
	if _, err := s.waitResponse(id); err != nil {
		slog.Debug("omp: set_ask_dialog unsupported, using select fallback", "error", err)
	}
}

// handleAskDialog renders omp's `ask` tool call (every question of the call in
// one request) as engine question cards.
func (s *ompSession) handleAskDialog(requestID string, frame map[string]any) {
	rawQuestions, _ := frame["questions"].([]any)
	pending := &ompPendingAsk{kind: "ask"}
	userQuestions := make([]core.UserQuestion, 0, len(rawQuestions))
	for _, raw := range rawQuestions {
		qm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		q := ompAskQuestion{
			id:          strField(qm, "id"),
			question:    strField(qm, "question"),
			header:      strField(qm, "header"),
			multi:       boolField(qm, "multi"),
			recommended: intField(qm, "recommended"),
		}
		if q.question == "" {
			continue
		}
		if q.id == "" {
			q.id = q.question
		}
		options, _ := qm["options"].([]any)
		userOptions := make([]core.UserQuestionOption, 0, len(options))
		for _, rawOpt := range options {
			om, ok := rawOpt.(map[string]any)
			if !ok {
				continue
			}
			label := strField(om, "label")
			if label == "" {
				continue
			}
			q.labels = append(q.labels, label)
			userOptions = append(userOptions, core.UserQuestionOption{
				Label:       label,
				Description: strField(om, "description"),
			})
		}
		pending.questions = append(pending.questions, q)
		userQuestions = append(userQuestions, core.UserQuestion{
			Question:    q.question,
			Header:      q.header,
			Options:     userOptions,
			MultiSelect: q.multi,
		})
	}
	if len(userQuestions) == 0 {
		slog.Warn("omp: ask request without renderable questions", "id", requestID)
		_ = s.writeFrame(map[string]any{"type": "extension_ui_response", "id": requestID, "cancelled": true})
		return
	}
	s.storeAsk(requestID, pending)

	slog.Info("omp: question dialog", "id", requestID, "questions", len(userQuestions))
	emitSafe(s.events, core.Event{
		Type:      core.EventPermissionRequest,
		RequestID: requestID,
		ToolName:  "AskUserQuestion",
		ToolInput: userQuestions[0].Question,
		ToolInputRaw: map[string]any{
			"request_id": requestID,
			"questions":  userQuestions,
		},
		Questions: userQuestions,
	})
}

// handleSelectDialog maps omp's single-choice fallback (one select per option
// list, used when build or config keeps `ask` on the per-choice path).
func (s *ompSession) handleSelectDialog(requestID string, frame map[string]any) {
	question := strField(frame, "title")
	if question == "" {
		question = strField(frame, "message")
	}
	options, _ := frame["options"].([]any)
	details, _ := frame["optionDetails"].([]any)
	q := ompAskQuestion{id: requestID, question: question}
	userQuestion := core.UserQuestion{Question: question, Header: question}
	for i, raw := range options {
		label, ok := raw.(string)
		if !ok || label == "" {
			continue
		}
		q.labels = append(q.labels, label)
		opt := core.UserQuestionOption{Label: label}
		if i < len(details) {
			if dm, ok := details[i].(map[string]any); ok {
				opt.Description = strField(dm, "description")
			}
		}
		userQuestion.Options = append(userQuestion.Options, opt)
	}
	if len(userQuestion.Options) == 0 {
		_ = s.writeFrame(map[string]any{"type": "extension_ui_response", "id": requestID, "cancelled": true})
		return
	}
	s.storeAsk(requestID, &ompPendingAsk{kind: "select", questions: []ompAskQuestion{q}})

	slog.Info("omp: select dialog", "id", requestID, "options", len(userQuestion.Options))
	emitSafe(s.events, core.Event{
		Type:      core.EventPermissionRequest,
		RequestID: requestID,
		ToolName:  "AskUserQuestion",
		ToolInput: question,
		ToolInputRaw: map[string]any{
			"request_id": requestID,
			"questions":  []core.UserQuestion{userQuestion},
		},
		Questions: []core.UserQuestion{userQuestion},
	})
}

func (s *ompSession) storeAsk(id string, pending *ompPendingAsk) {
	if id == "" {
		return
	}
	s.askMu.Lock()
	defer s.askMu.Unlock()
	if s.asks == nil {
		s.asks = make(map[string]*ompPendingAsk)
	}
	s.asks[id] = pending
}

func (s *ompSession) takeAsk(id string) *ompPendingAsk {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	pending := s.asks[id]
	delete(s.asks, id)
	return pending
}

func (s *ompSession) forgetAsk(id string) {
	s.askMu.Lock()
	defer s.askMu.Unlock()
	delete(s.asks, id)
}

// askAnswerText pulls one question's answer out of the engine's UpdatedInput
// (buildAskQuestionResponse keys answers by question text).
func askAnswerText(q ompAskQuestion, updatedInput map[string]any) string {
	if updatedInput == nil {
		return ""
	}
	answers, ok := updatedInput["answers"].(map[string]any)
	if !ok {
		return ""
	}
	answer, _ := answers[q.question].(string)
	return strings.TrimSpace(answer)
}

// splitAskAnswer turns the engine's answer text back into omp's
// `selectedOptions` + `customInput` pair. The engine joins multi-select labels
// with ", "; anything that is not an exact option label is free text.
func splitAskAnswer(q ompAskQuestion, answer string) ([]string, string) {
	selected := []string{}
	if answer == "" {
		return selected, ""
	}
	for _, label := range q.labels {
		if label == answer {
			return []string{label}, ""
		}
	}
	if !q.multi {
		return selected, answer
	}
	var custom []string
	for _, piece := range strings.Split(answer, ",") {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		matched := false
		for _, label := range q.labels {
			if label == piece {
				selected = append(selected, label)
				matched = true
				break
			}
		}
		if !matched {
			custom = append(custom, piece)
		}
	}
	if len(custom) > 0 && len(selected) > 0 {
		// omp rejects mixing options with free text on a multi-select; keep the
		// typed text as the whole answer instead of dropping one half.
		return []string{}, answer
	}
	return selected, strings.Join(custom, ", ")
}

func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

func intField(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return -1
}

// ompAskQuestion is one question of an `ask` request, in omp's own ids.
type ompAskQuestion struct {
	id          string
	question    string
	header      string
	multi       bool
	recommended int
	labels      []string
}

// ompPendingAsk is an open extension UI dialog awaiting the user's card answer.
type ompPendingAsk struct {
	kind      string // "ask" (answers payload) or "select" (value payload)
	questions []ompAskQuestion
}

func emitSafe(ch chan core.Event, ev core.Event) {
	select {
	case ch <- ev:
	default:
		slog.Debug("omp: event channel full, dropping", "type", ev.Type)
	}
}

func strField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func truncateLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

func splitModel(model string) (provider, id string, ok bool) {
	i := strings.Index(model, "/")
	if i <= 0 || i == len(model)-1 {
		return "", "", false
	}
	return model[:i], model[i+1:], true
}

func dirHasSessionData(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") && !e.IsDir() {
			return true
		}
	}
	return false
}

// buildSpawnArgs assembles the omp rpc-ui CLI args. A non-empty systemPrompt
// (live sessions only) adds --append-system-prompt so the omp-lark-bridge brief is
// injected once per session process; query-only processes pass "".
func buildSpawnArgs(sessionDir, workDir string, extraArgs []string, systemPrompt string) []string {
	args := append([]string{
		// rpc-ui (not rpc) is required for the `ask` tool's tool UI: plain rpc
		// never emits extension_ui_request tool dialogs, so `ask` would resolve
		// to its defaults without ever reaching the user (verified against
		// omp 18.12.6: set_ask_dialog succeeds but no ask frame follows).
		"--mode", "rpc-ui",
		"--approval-mode", "yolo",
		"--session-dir", sessionDir,
		"--cwd", workDir,
	}, extraArgs...)

	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}
	return args
}

// prependSelfDirToPath puts the omp-lark-bridge bridge's own directory at the front
// of PATH in childEnv, so the agent shell can resolve `omp-lark-bridge` even when
// the bridge was started from a non-PATH location (the single binary provides
// the send/cron/timer commands the injected brief references). It is a no-op
// when the directory is already listed.
func prependSelfDirToPath(childEnv []string, sep string) []string {
	self, err := os.Executable()
	if err != nil {
		return childEnv
	}
	dir := filepath.Dir(self)

	for i, kv := range childEnv {
		key, val, ok := strings.Cut(kv, "=")
		if !ok || key != "PATH" {
			continue
		}
		for _, p := range strings.Split(val, sep) {
			if p == dir {
				return childEnv // already on PATH
			}
		}
		out := append([]string{}, childEnv...)
		out[i] = "PATH=" + dir + sep + val
		return out
	}
	return append([]string{"PATH=" + dir}, childEnv...)
}

// spawnOmpProcess launches an omp rpc-ui process and waits for the `ready`
// handshake. It does NOT begin/resume any conversation: live sessions call
// enterSession next, while query-only callers (model discovery) just send a
// command and tear the process down.
func spawnOmpProcess(ctx context.Context, bin string, extraArgs []string, workDir, ccDataDir string, env []string, systemPrompt string) (*ompSession, error) {
	sctx, cancel := context.WithCancel(ctx)

	// The store directory is a stable container for every conversation of this
	// project. Per-conversation identity is carried by the session *file* path
	// (see enterSession), so the directory must NOT be derived from the agent
	// session id: that produced a fresh empty directory per restart and made
	// resume impossible.
	sessionDir := ompSessionStoreDir(ccDataDir, "")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		cancel()
		return nil, fmt.Errorf("omp: mkdir session store: %w", err)
	}

	args := buildSpawnArgs(sessionDir, workDir, extraArgs, systemPrompt)

	fullEnv := append([]string{"PI_NO_TITLE=1"}, os.Environ()...)
	fullEnv = append(fullEnv, env...)
	fullEnv = prependSelfDirToPath(fullEnv, string(os.PathListSeparator))

	cmd := exec.CommandContext(sctx, bin, args...)
	cmd.Dir = workDir
	cmd.Env = fullEnv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("omp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("omp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("omp: stderr pipe: %w", err)
	}

	s := &ompSession{
		ctx:        sctx,
		cancel:     cancel,
		bin:        bin,
		args:       args,
		env:        fullEnv,
		dir:        workDir,
		cmd:        cmd,
		stdin:      stdin,
		stderr:     stderr,
		ready:      make(chan struct{}),
		events:     make(chan core.Event, 512),
		sessionDir: sessionDir,
		pending:    make(map[string]chan map[string]any),
		asks:       make(map[string]*ompPendingAsk),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("omp: start %s: %w", bin, err)
	}
	s.alive.Store(true)

	go s.readLoop(stdout)
	go s.drainStderr()
	go s.waitExit()

	// Wait for the omp `ready` handshake before the caller sends anything.
	select {
	case <-s.ready:
	case <-time.After(readyTimeout):
		cancel()
		return nil, fmt.Errorf("omp: timed out waiting for rpc ready frame; args=%v", args)
	}
	if s.readyErr != nil {
		cancel()
		return nil, fmt.Errorf("omp: rpc handshake: %w", s.readyErr)
	}

	return s, nil
}

// discoverLoadedModels spawns a throwaway omp rpc process and asks for the
// available model list (get_available_models). It returns nil on any failure so
// callers can fall back to the configured model.
func discoverLoadedModels(ctx context.Context, bin string, extraArgs []string, workDir, ccDataDir string, env []string) []core.ModelOption {
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	s, err := spawnOmpProcess(sctx, bin, extraArgs, workDir, ccDataDir, env, "")
	if err != nil {
		cancel()
		slog.Warn("omp: model discovery spawn failed", "error", err)
		return nil
	}
	defer func() {
		_ = s.Close()
		cancel()
	}()

	pid := uuid.NewString()
	if err := s.writeFrame(map[string]any{"id": pid, "type": "get_available_models"}); err != nil {
		return nil
	}
	resp, err := s.waitResponse(pid)
	if err != nil {
		return nil
	}
	data, _ := resp["data"].(map[string]any)
	return parseModelsResponse(data)
}

// parseModelsResponse converts a get_available_models response payload into
// model options, using the `provider/id` form that omp's set_model expects.
func parseModelsResponse(data map[string]any) []core.ModelOption {
	var out []core.ModelOption
	raw, _ := data["models"].([]any)
	for _, it := range raw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		provider, _ := m["provider"].(string)
		id, _ := m["id"].(string)
		if provider == "" || id == "" {
			continue
		}
		name, _ := m["name"].(string)
		out = append(out, core.ModelOption{Name: provider + "/" + id, Desc: name})
	}
	return out
}
