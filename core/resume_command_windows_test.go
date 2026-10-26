//go:build windows

package core

import (
	"strings"
	"testing"
)

// The resume one-liner is pasted into a Windows shell. The default
// powershell.exe (5.1) rejects "&&", so the chain must use ";", and quoting
// must follow PowerShell rules (embedded ' doubled).
func TestResumeCommand_QuotesPathsAndCdsToWorkDir(t *testing.T) {
	// 带空格的 work_dir 必须整体加引号，否则粘贴到终端会拆成多个参数。
	got := resumeCommand("/tmp/store", "a.jsonl", "/srv/my dir")
	if !strings.Contains(got, "cd '/srv/my dir'") {
		t.Errorf("cwd not quoted: %q", got)
	}
	if !strings.Contains(got, " ; ") || strings.Contains(got, " && ") {
		t.Errorf("command chain must use ';' for powershell.exe: %q", got)
	}
	// 会话文件用 Windows 分隔符拼接并整体加引号（字面量：分隔符正是契约）。
	if !strings.Contains(got, `'\tmp\store\a.jsonl'`) {
		t.Errorf("session file not quoted with windows separators: %q", got)
	}
	if !strings.Contains(got, "'/tmp/store'") {
		t.Errorf("session dir not quoted: %q", got)
	}
	if !strings.Contains(got, " --resume ") || !strings.Contains(got, " --session-dir ") {
		t.Errorf("missing resume flags: %q", got)
	}
	// 没有 cwd 的会话不拼 cd。
	if got := resumeCommand("/tmp/store", "a.jsonl", ""); strings.Contains(got, "cd ") {
		t.Errorf("no-cwd command should not cd: %q", got)
	}
	// 单引号按 PowerShell 规则双写，否则命令会被截断。
	if got := resumeCommand("/t/it's", "a.jsonl", ""); !strings.Contains(got, "''s") {
		t.Errorf("single quote not escaped: %q", got)
	}
}
