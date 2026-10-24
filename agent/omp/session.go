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

	"github.com/google/uuid"
	"github.com/lzyyzznl/in-one-chat/core"
)

const (
	defaultSessionName = "default"
	readyTimeout       = 15 * time.Second
	maxFrameBytes      = 1 << 20 // omp v1 stdout frame limit (1 MiB)
)

// ompSession is a long-lived `omp --mode rpc` subprocess bound to one chat
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

	sessionDir string // --session-dir store handed to omp
	ompSessID  string // omp-side session id (from new_session / open_session)

	writeMu sync.Mutex
	pMu     sync.Mutex
	pending map[string]chan map[string]any

	// thinking accumulation: engine's EventThinking handler overwrites the
	// card's thinking area with event.Content (never appends), so deltas must
	// be accumulated and emitted at a human cadence — otherwise the card shows
	// only the last 1-2 chars per delta ("one char per line").
	thinkMu      sync.Mutex
	thinkBuf     strings.Builder
	thinkEmitted bool
	thinkLast    time.Time
}

// ompSessionStoreDir returns the per-session store directory under the cc-data
// dir. Using --session-dir keeps omp's store out of the user's ~/.omp.
func ompSessionStoreDir(ccDataDir, sessionID string) string {
	return filepath.Join(ompSessionsRoot(ccDataDir), sanitizeSessionName(sessionID))
}

func ompSessionsRoot(ccDataDir string) string {
	if ccDataDir != "" {
		return filepath.Join(ccDataDir, "omp-sessions")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".in-one-chat", "omp-sessions")
	}
	return ".in-one-chat-omp-sessions"
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

func newOmpSession(ctx context.Context, bin string, extraArgs []string, workDir, model, ccDataDir, sessionID string, env []string) (*ompSession, error) {
	sctx, cancel := context.WithCancel(ctx)

	sessionDir := ompSessionStoreDir(ccDataDir, sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		cancel()
		return nil, fmt.Errorf("omp: mkdir session store: %w", err)
	}

	args := append([]string{
		"--mode", "rpc",
		"--approval-mode", "yolo",
		"--session-dir", sessionDir,
		"--cwd", workDir,
	}, extraArgs...)

	fullEnv := append([]string{"PI_NO_TITLE=1"}, os.Environ()...)
	fullEnv = append(fullEnv, env...)

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

	// Begin/resume the omp-side session inside the running process.
	if err := s.enterSession(model, sessionID != ""); err != nil {
		cancel()
		return nil, err
	}

	return s, nil
}

// enterSession opens (resume) or starts a fresh omp session and remembers the
// omp-side session id.
func (s *ompSession) enterSession(model string, resume bool) error {
	cmd := "new_session"
	if resume && dirHasSessionData(s.sessionDir) {
		cmd = "open_session"
	}
	id := uuid.NewString()
	var payload map[string]any
	switch cmd {
	case "open_session":
		payload = map[string]any{"id": id, "type": cmd, "sessionDir": s.sessionDir}
	default:
		payload = map[string]any{"id": id, "type": cmd}
	}
	if err := s.writeFrame(payload); err != nil {
		return err
	}

	resp, err := s.waitResponse(id)
	if err != nil {
		return err
	}
	if data, ok := resp["data"].(map[string]any); ok {
		if sid, _ := data["sessionId"].(string); sid != "" {
			s.ompSessID = sid
		}
		if file, _ := data["sessionFile"].(string); file != "" && s.ompSessID == "" {
			s.ompSessID = file
		}
	}

	if model != "" {
		pid := uuid.NewString()
		mp := map[string]any{"id": pid, "type": "set_model"}
		if provider, mdl, ok := splitModel(model); ok {
			mp["provider"], mp["modelId"] = provider, mdl
		} else {
			mp["modelId"] = model
		}
		return s.writeFrame(mp)
	}
	return nil
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
	dir := filepath.Join(s.dir, ".in-one-chat", "images")
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

// RespondPermission is a no-op: the agent runs yolo with no approval prompts.
func (s *ompSession) RespondPermission(_ string, _ core.PermissionResult) error { return nil }

func (s *ompSession) Events() <-chan core.Event { return s.events }

func (s *ompSession) CurrentSessionID() string { return s.ompSessID }

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
	name, _ := frame["name"].(string)
	if name == "" {
		if n, ok := frame["toolName"].(string); ok {
			name = n
		}
	}
	emitSafe(s.events, core.Event{Type: core.EventToolUse, ToolName: name})
}

func (s *ompSession) handleToolEnd(frame map[string]any) {
	emitSafe(s.events, core.Event{
		Type:       core.EventToolResult,
		ToolName:   strField(frame, "name"),
		ToolStatus: "completed",
	})
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
	if method != "select" {
		return
	}
	if id, ok := frame["id"].(string); ok {
		_ = s.writeFrame(map[string]any{"type": "extension_ui_response", "id": id, "value": "Deny"})
	}
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
