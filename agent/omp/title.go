package omp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// GenerateTitle derives a short session title from the supplied transcript
// context using the agent's own provider/model — a one-shot `omp -p` process
// that inherits the project's model and work dir (same approach as loadSkills).
// Seconds-scale latency; errors are surfaced so the caller can retry.
func (a *Agent) GenerateTitle(ctx context.Context, prompt string) (string, error) {
	a.mu.RLock()
	bin := a.cmd
	extraArgs := append([]string{}, a.cliExtraArgs...)
	workDir := a.workDir
	env := append([]string{}, a.configEnv...)
	env = append(env, a.sessionEnv...)
	a.mu.RUnlock()

	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := append([]string{"-p", prompt, "--no-session", "--no-title"}, extraArgs...)
	cmd := exec.CommandContext(sctx, bin, args...)
	cmd.Dir = workDir
	fullEnv := append([]string{"PI_NO_TITLE=1"}, os.Environ()...)
	fullEnv = append(fullEnv, env...)
	cmd.Env = fullEnv

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("omp: generate title: %w", err)
	}
	return cleanTitle(string(out)), nil
}

// cleanTitle strips surrounding whitespace, markdown fences and stray markers
// that one-shot omp -p output may include.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.Trim(strings.TrimSpace(s), "`*#-_ \t\n")
}
