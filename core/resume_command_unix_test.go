//go:build !windows

package core

import (
	"strings"
	"testing"
)

// The resume one-liner is pasted into a POSIX shell: quoting and the command
// separator are shell-specific, so this exact-output assertion is POSIX-only.
// The Windows counterpart lives in resume_command_windows_test.go.
func TestResumeCommand_QuotesPathsAndCdsToWorkDir(t *testing.T) {
	// 带空格的 work_dir 必须整体加引号，否则粘贴到终端会拆成多个参数。
	got := resumeCommand("/tmp/store", "a.jsonl", "/srv/my dir")
	want := `cd '/srv/my dir' && omp --resume '/tmp/store/a.jsonl' --session-dir '/tmp/store'`
	if got != want {
		t.Fatalf("resumeCommand = %q, want %q", got, want)
	}
	// 没有 cwd 的会话不拼 cd。
	if got := resumeCommand("/tmp/store", "a.jsonl", ""); strings.Contains(got, "cd ") {
		t.Errorf("no-cwd command should not cd: %q", got)
	}
	// 路径里的单引号要转义，否则命令会被截断。
	if got := resumeCommand("/t/it's", "a.jsonl", ""); !strings.Contains(got, `'/t/it'\''s/a.jsonl'`) {
		t.Errorf("single quote not escaped: %q", got)
	}
}
