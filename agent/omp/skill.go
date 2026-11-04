package omp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/lzyyzznl/omp-lark-bridge/core"
)

// skillCacheTTL bounds how long the `omp skill list` result is reused, so
// resolving a skill slash command doesn't spawn an omp process per message.
const skillCacheTTL = 60 * time.Second

// ListSkills implements core.SkillProvider via the omp protocol:
// `omp skill list <cwd> --json` reports exactly the skills a session in the
// agent's work dir resolves (same discovery, no duplication). Failures return
// an error the engine surfaces instead of breaking message flow.
func (a *Agent) ListSkills(ctx context.Context) ([]core.Skill, error) {
	a.skillsMu.Lock()
	if a.skillsCache != nil && time.Since(a.skillsCachedAt) < skillCacheTTL {
		cached := a.skillsCache
		a.skillsMu.Unlock()
		return cached, nil
	}
	a.skillsMu.Unlock()

	a.mu.RLock()
	bin := a.cmd
	extraArgs := append([]string{}, a.cliExtraArgs...)
	workDir := a.workDir
	env := append([]string{}, a.configEnv...)
	env = append(env, a.sessionEnv...)
	a.mu.RUnlock()

	skills, err := a.loadSkills(ctx, bin, extraArgs, workDir, env)
	if err != nil {
		return nil, err
	}
	a.skillsMu.Lock()
	a.skillsCache = skills
	a.skillsCachedAt = time.Now()
	a.skillsMu.Unlock()
	return skills, nil
}

func (a *Agent) loadSkills(ctx context.Context, bin string, extraArgs []string, workDir string, env []string) ([]core.Skill, error) {
	sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	args := append([]string{"skill", "list", "--json"}, extraArgs...)
	cmd := exec.CommandContext(sctx, bin, args...)
	cmd.Dir = workDir
	fullEnv := append([]string{"PI_NO_TITLE=1"}, os.Environ()...)
	fullEnv = append(fullEnv, env...)
	cmd.Env = fullEnv

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("omp: skill list: %w", err)
	}
	return core.ParseOmpSkillList(out)
}
