// Package omp adapts the omp coding harness (Bun/TS CLI, https://omp.sh) as a
// omp-lark-bridge agent over its JSONL RPC mode (`omp --mode rpc-ui`).
//
// Product decisions (see README design notes):
//   - long-lived RPC sessions per chat (multi-turn context, steer/abort)
//   - permission is fixed to maximum (--approval-mode yolo); the bridge does
//     NOT implement permission prompting or a mode switcher
//   - the `ask` tool's dialogs ARE supported: set_ask_dialog is enabled and
//     extension_ui_request frames are rendered as chat question cards
//   - sessions are stored under the omp-lark-bridge data dir via --session-dir, so
//     omp's personal ~/.omp store is not polluted
//
// The protocol contract is documented in omp's own `omp://rpc.md` /
// `omp://approval-mode.md` (v18.12.6).
package omp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

func init() {
	core.RegisterAgent("omp", New)
}

// Agent drives the omp CLI in RPC mode.
type Agent struct {
	workDir string
	model   string
	// thinkingLevel is the agent-wide default omp thinking level (set via
	// ReasoningEffortSwitcher from the management UI / /reasoning). Applied on
	// the next session the agent creates (enterSession sends set_thinking_level),
	// so running sessions are never mutated mid-turn. "" = omp's default.
	// Not persisted across restarts (unlike model); the web dropdown reflects
	// the in-memory value and resets to "default" after a reload.
	thinkingLevel string
	cmd           string   // CLI binary name, default "omp"
	cliExtraArgs  []string // extra args parsed from cmd after the binary name
	configEnv     []string // env vars from [projects.agent.options.env]
	sessionEnv    []string
	ccDataDir     string // global omp-lark-bridge data dir (session stores + model cache)
	mu            sync.RWMutex

	// appendSystemPrompt is the omp-lark-bridge capability brief injected into each
	// live RPC session via --append-system-prompt (one process per session, so
	// the injection lands exactly once, before the session's first message).
	appendSystemPrompt string

	// One-shot cron overrides consumed by the next StartSession. Only
	// ExecuteCronJob's CronOverrideApplier path sets them (cron jobs pin their
	// own model / thinking level and always run in a fresh session).
	overrideMu      sync.Mutex
	pendingModel    string
	pendingThinking string
	pendingAgent    string // delegated subagent for the next cron-run session
	pendingGoal     string // goal-mode objective for the next cron-run session (goal_mode: on)

	// modelsCache caches the get_available_models discovery result so a web
	// dropdown doesn't spawn an omp process on every page load.
	modelsMu         sync.Mutex
	modelsCache      []core.ModelOption
	modelsCachedAt   time.Time
	modelsRefreshing bool // single-flight guard for warmModels

	// skillsCache caches `omp skill list --json` so /skills and skill slash-command
	// resolution don't spawn an omp process on every message.
	skillsMu       sync.Mutex
	skillsCache    []core.Skill
	skillsCachedAt time.Time
}

// modelCacheTTL bounds how long the discovered model list is reused. A cold
// discovery spawns an omp process and costs 3–8s, far more than the 3s a
// Feishu card callback is allowed, so the cache — not the spawn — is what makes
// /model complete. The provider model list rarely changes; a stale minute
// bought nothing but empty dropdowns.
const modelCacheTTL = 10 * time.Minute

func New(opts map[string]any) (core.Agent, error) {
	workDir, _ := opts["work_dir"].(string)
	if workDir == "" {
		workDir = "."
	}
	model, _ := opts["model"].(string)
	cmd, extraArgs := core.ParseCmdOpts(opts, "omp")
	ccDataDir, _ := opts["cc_data_dir"].(string)

	if _, err := exec.LookPath(cmd); err != nil {
		return nil, fmt.Errorf("omp: %q CLI not found in PATH, install from: https://omp.sh", cmd)
	}

	// Optional user-provided system prompt fragment injected via
	// --append-system-prompt; defaults to the compact omp-lark-bridge brief. An
	// explicitly empty string disables injection (omp then runs with no bridge
	// brief; it still discovers skills on its own).
	appendSystemPrompt, explicit := opts["append_system_prompt"].(string)
	if !explicit {
		appendSystemPrompt = core.CompactSystemPrompt()
	}

	return &Agent{
		workDir:            workDir,
		model:              model,
		cmd:                cmd,
		cliExtraArgs:       extraArgs,
		configEnv:          core.ParseConfigEnv(opts),
		ccDataDir:          ccDataDir,
		appendSystemPrompt: appendSystemPrompt,
	}, nil
}

func (a *Agent) Name() string { return "omp" }

func (a *Agent) Stop() error { return nil }

// -- work dir / model (switchers act on the next session) --

func (a *Agent) GetWorkDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workDir
}

// SessionDir exposes the directory omp writes session transcripts into
// (<data_dir>/omp-sessions/default). The management UI scans it to render the
// session viewer without touching the agent processes.
func (a *Agent) SessionDir() string {
	a.mu.RLock()
	cc := a.ccDataDir
	a.mu.RUnlock()
	return ompSessionStoreDir(cc, "")
}

func (a *Agent) SetWorkDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.workDir = dir
	slog.Info("omp: work_dir changed", "work_dir", dir)
}

func (a *Agent) GetModel() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

// SetModel implements core.ModelSwitcher. Changes apply on the next session so
// the engine can keep running sessions stable.
func (a *Agent) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
	slog.Info("omp: model changed (next session)", "model", model)
}

// GetReasoningEffort implements core.ReasoningEffortSwitcher.
func (a *Agent) GetReasoningEffort() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinkingLevel
}

// SetReasoningEffort implements core.ReasoningEffortSwitcher. Changes apply on
// the next session (each web chat turn / cron run starts one and re-issues
// set_thinking_level), so running sessions are never mutated mid-turn.
func (a *Agent) SetReasoningEffort(level string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinkingLevel = level
	slog.Info("omp: thinking level changed (next session)", "level", level)
}

// AvailableReasoningEfforts implements core.ReasoningEffortSwitcher and mirrors
// the levels omp's set_thinking_level accepts (verified on omp 18.12.6; unknown
// levels are clamped rather than rejected).
func (a *Agent) AvailableReasoningEfforts() []string {
	return []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
}

// thinkingForNextSession resolves the thinking level a new session should use:
// a one-shot cron override wins when the job pins one, otherwise the
// agent-wide default from SetReasoningEffort.
func (a *Agent) thinkingForNextSession(override string) string {
	if override != "" {
		return override
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.thinkingLevel
}

// ApplyCronOverrides implements core.CronOverrideApplier. It stores a one-shot
// model / thinking-level override that StartSession consumes for the next
// session it creates (a cron job's own fresh session).
func (a *Agent) ApplyCronOverrides(model, thinkingLevel string) {
	a.overrideMu.Lock()
	defer a.overrideMu.Unlock()
	a.pendingModel = model
	a.pendingThinking = thinkingLevel
	if model != "" {
		slog.Info("omp: cron model override queued", "model", model, "thinking", thinkingLevel)
	} else if thinkingLevel != "" {
		slog.Info("omp: cron thinking override queued", "thinking", thinkingLevel)
	}
}

// ApplyCronAgent implements core.CronAgentApplier. It stores a one-shot
// delegated subagent name that StartSession consumes for the next session it
// creates. There is no RPC command to set an omp agent (agents are spawned by
// the main session's `task` tool), so the session injects the delegation
// instruction into its very first prompt instead (see ompSession.Send).
func (a *Agent) ApplyCronAgent(agent string) {
	a.overrideMu.Lock()
	defer a.overrideMu.Unlock()
	a.pendingAgent = agent
	if agent != "" {
		slog.Info("omp: cron agent override queued", "agent", agent)
	}
}

// ApplyCronGoal implements core.CronGoalApplier. It stores a one-shot goal-mode
// objective that StartSession consumes for the next session it creates. omp
// rejects the `--goal` launch flag in rpc-ui mode, so the session issues the
// RPC `goal` command (op: create) once it has entered (see ompSession.enterGoal).
func (a *Agent) ApplyCronGoal(objective string) {
	a.overrideMu.Lock()
	defer a.overrideMu.Unlock()
	a.pendingGoal = objective
}

// AvailableModels implements core.ModelSwitcher's discovery entrypoint. It
// spawns a throwaway omp process to fetch the provider's real model list
// (cached for modelCacheTTL); on any failure it falls back to the configured
// single model so consumers always get a usable list.
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	a.modelsMu.Lock()
	fresh := a.modelsCache != nil && time.Since(a.modelsCachedAt) < modelCacheTTL
	cached := a.modelsCache
	a.modelsMu.Unlock()
	if fresh {
		return cached
	}

	if models := a.discoverModels(ctx); len(models) > 0 {
		a.modelsMu.Lock()
		a.modelsCache = models
		a.modelsCachedAt = time.Now()
		a.modelsMu.Unlock()
		return models
	}

	// Discovery failed or the caller's deadline cut it short (a card callback
	// gets 3s, a cold spawn needs up to ~8s). Never cache the fallback — doing
	// so turned one slow spawn into a minute of single-model dropdowns. Serve
	// the stale list if we have one and refill in the background.
	a.warmModels()
	if cached != nil {
		return cached
	}
	a.mu.RLock()
	model := a.model
	a.mu.RUnlock()
	if model == "" {
		return nil
	}
	return []core.ModelOption{{Name: model}}
}

// discoverModels runs one get_available_models round-trip, bounded by ctx.
func (a *Agent) discoverModels(ctx context.Context) []core.ModelOption {
	a.mu.RLock()
	cmd := a.cmd
	extraArgs := append([]string{}, a.cliExtraArgs...)
	workDir := a.workDir
	ccDataDir := a.ccDataDir
	env := append([]string{}, a.configEnv...)
	env = append(env, a.sessionEnv...)
	a.mu.RUnlock()
	return discoverLoadedModels(ctx, cmd, extraArgs, workDir, ccDataDir, env)
}

// warmModels refills the model cache out of band so a later /model open is
// served from cache instead of racing its 3s deadline. Single-flight, and a
// no-op while the cache is still fresh.
func (a *Agent) warmModels() {
	a.modelsMu.Lock()
	if a.modelsRefreshing || (a.modelsCache != nil && time.Since(a.modelsCachedAt) < modelCacheTTL) {
		a.modelsMu.Unlock()
		return
	}
	a.modelsRefreshing = true
	a.modelsMu.Unlock()

	go func() {
		defer func() {
			a.modelsMu.Lock()
			a.modelsRefreshing = false
			a.modelsMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		models := a.discoverModels(ctx)
		if len(models) == 0 {
			slog.Warn("omp: background model discovery returned nothing")
			return
		}
		a.modelsMu.Lock()
		a.modelsCache = models
		a.modelsCachedAt = time.Now()
		a.modelsMu.Unlock()
	}()
}

func (a *Agent) SetSessionEnv(env []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessionEnv = env
}

// -- memory (/memory) --

func (a *Agent) ProjectMemoryFile() string {
	a.mu.RLock()
	workDir := a.workDir
	a.mu.RUnlock()
	absDir, err := filepath.Abs(workDir)
	if err != nil {
		absDir = workDir
	}
	return filepath.Join(absDir, "AGENTS.md")
}

func (a *Agent) GlobalMemoryFile() string {
	return ""
}

// CompressCommand reports no native slash command: omp compacts via the RPC
// `compact` command, which the engine cannot reach as plain text.
func (a *Agent) CompressCommand() string { return "" }

// HasSystemPromptSupport implements core.SystemPromptSupporter: the omp-lark-bridge
// capability brief is injected natively via --append-system-prompt at spawn, so
// the engine need not write it to a memory file.
func (a *Agent) HasSystemPromptSupport() bool { return true }

// -- sessions --

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	model := a.model
	cmd := a.cmd
	extraArgs := append([]string{}, a.cliExtraArgs...)
	workDir := a.workDir
	ccDataDir := a.ccDataDir
	appendSystemPrompt := a.appendSystemPrompt
	extraEnv := append([]string{}, a.configEnv...)
	extraEnv = append(extraEnv, a.sessionEnv...)
	a.mu.RUnlock()

	// Consume any queued cron overrides (model overrides the agent default; the
	// thinking level is applied by the session at enter time; the delegated
	// agent is injected into the session's first prompt). All three are
	// one-shot: they belong to exactly the next session this agent creates.
	a.overrideMu.Lock()
	thinking := a.thinkingForNextSession(a.pendingThinking)
	agent := a.pendingAgent
	goal := a.pendingGoal
	if a.pendingModel != "" {
		model = a.pendingModel
	}
	a.pendingModel, a.pendingThinking, a.pendingAgent, a.pendingGoal = "", "", "", ""
	a.overrideMu.Unlock()

	s, err := newOmpSession(ctx, cmd, extraArgs, workDir, model, thinking, agent, goal, ccDataDir, sessionID, extraEnv, appendSystemPrompt)
	if err == nil {
		// A live session is the moment a /model open becomes likely; warm the
		// list now so the card does not have to spawn omp inside its 3s budget.
		a.warmModels()
	}
	return s, err
}

// ListSessions lists the omp session stores this omp-lark-bridge instance owns.
func (a *Agent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error) {
	a.mu.RLock()
	ccDataDir := a.ccDataDir
	workDir := a.workDir
	a.mu.RUnlock()

	dir := ompSessionsRoot(ccDataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("omp: list sessions: %w", err)
	}
	var out []core.AgentSessionInfo
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		summary := e.Name()
		if summary == defaultSessionName {
			summary = "(default)"
		}
		_ = workDir
		out = append(out, core.AgentSessionInfo{ID: e.Name(), Summary: summary})
	}
	return out, nil
}
