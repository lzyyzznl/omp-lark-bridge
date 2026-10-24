package core

import (
	"context"
	"strings"
	"testing"
)

// capableAgent embeds the bare stub and adds every optional capability the
// engine probes for, so tests can prove the /help + panel filter is
// capability-driven rather than agent-specific.
type capableAgent struct {
	stubAgent
}

func (a *capableAgent) CompressCommand() string                         { return "/compact" }
func (a *capableAgent) DeleteSession(_ context.Context, _ string) error { return nil }
func (a *capableAgent) SetMode(string)                                  {}
func (a *capableAgent) GetMode() string                                 { return "yolo" }
func (a *capableAgent) PermissionModes() []PermissionModeInfo           { return nil }
func (a *capableAgent) SetReasoningEffort(string)                       {}
func (a *capableAgent) GetReasoningEffort() string                      { return "" }
func (a *capableAgent) AvailableReasoningEfforts() []string             { return nil }
func (a *capableAgent) SetProviders([]ProviderConfig)                   {}
func (a *capableAgent) SetActiveProvider(string) bool                   { return true }
func (a *capableAgent) GetActiveProvider() *ProviderConfig              { return nil }
func (a *capableAgent) ListProviders() []ProviderConfig                 { return nil }
func (a *capableAgent) GetUsage(context.Context) (*UsageReport, error) {
	return nil, nil
}
func (a *capableAgent) GetSessionHistory(context.Context, string, int) ([]HistoryEntry, error) {
	return nil, nil
}
func (a *capableAgent) SetWorkDir(string)                             {}
func (a *capableAgent) GetWorkDir() string                            { return "" }
func (a *capableAgent) ProjectMemoryFile() string                     { return "" }
func (a *capableAgent) GlobalMemoryFile() string                      { return "" }
func (a *capableAgent) SetModel(string)                               {}
func (a *capableAgent) GetModel() string                              { return "" }
func (a *capableAgent) AvailableModels(context.Context) []ModelOption { return nil }

func newCapabilityEngine(agent Agent) *Engine {
	return NewEngine("test", agent, nil, "", LangEnglish)
}

func TestCommandSupported_HidesCapabilitiesAgentLacks(t *testing.T) {
	e := newCapabilityEngine(&stubAgent{})
	unsupported := []string{"compress", "compact", "delete", "mode", "reasoning", "/effort", "provider", "usage", "history", "model", "workspace", "dir", "memory"}
	for _, id := range unsupported {
		if e.commandSupported(id) {
			t.Fatalf("commandSupported(%q) = true, want false for a bare agent", id)
		}
	}
	for _, id := range []string{"new", "/new", "cron", "status", "timer", "stop"} {
		if !e.commandSupported(id) {
			t.Fatalf("commandSupported(%q) = false, want true", id)
		}
	}

	full := newCapabilityEngine(&capableAgent{})
	for _, id := range unsupported {
		if !full.commandSupported(id) {
			t.Fatalf("commandSupported(%q) = false, want true for a fully capable agent", id)
		}
	}
}

func TestCommandSupported_EmptyCompressCommandStaysHidden(t *testing.T) {
	// omp implements ContextCompressor but returns "" — the generic /compress
	// flow would fail, so the command must stay hidden.
	e := newCapabilityEngine(&emptyCompressAgent{})
	if e.commandSupported("compress") {
		t.Fatal("compress must be hidden when CompressCommand() is empty")
	}
}

type emptyCompressAgent struct {
	stubAgent
}

func (a *emptyCompressAgent) CompressCommand() string { return "  " }

func TestGetAllCommands_SkipsUnsupported(t *testing.T) {
	e := newCapabilityEngine(&emptyCompressAgent{})

	ids := make(map[string]bool)
	for _, cmd := range e.GetAllCommands() {
		ids[cmd.Command] = true
	}
	for _, hidden := range []string{"compress", "delete", "mode", "reasoning", "provider", "usage", "model"} {
		if ids[hidden] {
			t.Fatalf("%q must not be advertised to the agent/panel, got %v", hidden, ids)
		}
	}
	for _, shown := range []string{"new", "list", "cron", "timer", "status", "help", "shell", "skills"} {
		if !ids[shown] {
			t.Fatalf("%q should still be advertised, got %v", shown, ids)
		}
	}
}

func TestHelpCard_OmitsUnsupportedCommands(t *testing.T) {
	e := newCapabilityEngine(&emptyCompressAgent{})

	text := e.renderHelpGroupCard("tools").RenderText()
	if strings.Contains(text, "**/compress**") {
		t.Fatalf("tools tab must not list /compress: %s", text)
	}
	if !strings.Contains(text, "**/cron**") {
		t.Fatalf("tools tab must keep supported commands: %s", text)
	}

	agentTab := e.renderHelpGroupCard("agent").RenderText()
	for _, hidden := range []string{"**/mode**", "**/reasoning**", "**/provider**"} {
		if strings.Contains(agentTab, hidden) {
			t.Fatalf("agent tab must not list %s: %s", hidden, agentTab)
		}
	}
	if !strings.Contains(agentTab, "**/lang**") {
		t.Fatalf("agent tab must keep always-available commands: %s", agentTab)
	}
}
