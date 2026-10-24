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
}

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

// AvailableModels implements core.ModelSwitcher's discovery entrypoint.
// omp owns model management internally; list only the configured model if set.
func (a *Agent) AvailableModels(_ context.Context) []core.ModelOption {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.model == "" {
		return nil
	}
	return []core.ModelOption{{Name: a.model}}
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

	return newOmpSession(ctx, cmd, extraArgs, workDir, model, ccDataDir, sessionID, extraEnv)
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
