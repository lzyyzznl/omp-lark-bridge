// Package omp adapts the omp coding harness (Bun/TS CLI, https://omp.sh) as a
// in-one-chat agent over its JSONL RPC mode (`omp --mode rpc-ui`).
//
// Product decisions (see README design notes):
//   - long-lived RPC sessions per chat (multi-turn context, steer/abort)
//   - permission is fixed to maximum (--approval-mode yolo); the bridge does
//     NOT implement permission prompting or a mode switcher
//   - the `ask` tool's dialogs ARE supported: set_ask_dialog is enabled and
//     extension_ui_request frames are rendered as chat question cards
//   - sessions are stored under the in-one-chat data dir via --session-dir, so
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

	"github.com/lzyyzznl/in-one-chat/core"
)

func init() {
	core.RegisterAgent("omp", New)
}

// Agent drives the omp CLI in RPC mode.
type Agent struct {
	workDir      string
	model        string
	cmd          string   // CLI binary name, default "omp"
	cliExtraArgs []string // extra args parsed from cmd after the binary name
	configEnv    []string // env vars from [projects.agent.options.env]
	sessionEnv   []string
	ccDataDir    string // global in-one-chat data dir (session stores + model cache)
	mu           sync.RWMutex

	// One-shot cron overrides consumed by the next StartSession. Only
	// ExecuteCronJob's CronOverrideApplier path sets them (cron jobs pin their
	// own model / thinking level and always run in a fresh session).
	overrideMu      sync.Mutex
	pendingModel    string
	pendingThinking string

	// modelsCache caches the get_available_models discovery result so a web
	// dropdown doesn't spawn an omp process on every page load.
	modelsMu       sync.Mutex
	modelsCache    []core.ModelOption
	modelsCachedAt time.Time
}

// modelCacheTTL bounds how long the discovered model list is reused.
const modelCacheTTL = 60 * time.Second

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

	return &Agent{
		workDir:      workDir,
		model:        model,
		cmd:          cmd,
		cliExtraArgs: extraArgs,
		configEnv:    core.ParseConfigEnv(opts),
		ccDataDir:    ccDataDir,
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

// AvailableModels implements core.ModelSwitcher's discovery entrypoint. It
// spawns a throwaway omp process to fetch the provider's real model list
// (cached for modelCacheTTL); on any failure it falls back to the configured
// single model so consumers always get a usable list.
func (a *Agent) AvailableModels(ctx context.Context) []core.ModelOption {
	a.modelsMu.Lock()
	if a.modelsCache != nil && time.Since(a.modelsCachedAt) < modelCacheTTL {
		out := a.modelsCache
		a.modelsMu.Unlock()
		return out
	}
	a.modelsMu.Unlock()

	a.mu.RLock()
	model := a.model
	cmd := a.cmd
	extraArgs := append([]string{}, a.cliExtraArgs...)
	workDir := a.workDir
	ccDataDir := a.ccDataDir
	env := append([]string{}, a.configEnv...)
	env = append(env, a.sessionEnv...)
	a.mu.RUnlock()

	models := discoverLoadedModels(ctx, cmd, extraArgs, workDir, ccDataDir, env)
	if len(models) == 0 {
		if model == "" {
			return nil
		}
		models = []core.ModelOption{{Name: model}}
	}
	a.modelsMu.Lock()
	a.modelsCache = models
	a.modelsCachedAt = time.Now()
	a.modelsMu.Unlock()
	return models
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

// -- sessions --

func (a *Agent) StartSession(ctx context.Context, sessionID string) (core.AgentSession, error) {
	a.mu.RLock()
	model := a.model
	cmd := a.cmd
	extraArgs := append([]string{}, a.cliExtraArgs...)
	workDir := a.workDir
	ccDataDir := a.ccDataDir
	extraEnv := append([]string{}, a.configEnv...)
	extraEnv = append(extraEnv, a.sessionEnv...)
	a.mu.RUnlock()

	// Consume any queued cron override (model overrides the agent default; the
	// thinking level is applied by the session at enter time).
	a.overrideMu.Lock()
	thinking := a.pendingThinking
	if a.pendingModel != "" {
		model = a.pendingModel
	}
	a.pendingModel, a.pendingThinking = "", ""
	a.overrideMu.Unlock()

	return newOmpSession(ctx, cmd, extraArgs, workDir, model, thinking, ccDataDir, sessionID, extraEnv)
}

// ListSessions lists the omp session stores this in-one-chat instance owns.
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
