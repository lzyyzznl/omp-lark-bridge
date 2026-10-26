//go:build windows

package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

const (
	windowsTaskName   = ServiceName
	windowsScriptName = "omp-lark-bridge-daemon.ps1"

	// CREATE_NO_WINDOW: DETACHED_PROCESS (0x8) looks right but makes
	// powershell.exe exit immediately — it refuses to run without a console and
	// standard handles.
	createNoWindow = 0x08000000
	// CREATE_BREAKAWAY_FROM_JOB: Task Scheduler runs the task inside a job
	// object, and `schtasks /End` terminates that whole job, so the helper must
	// leave it first or it dies before issuing /Run.
	createBreakawayFromJob = 0x01000000
)

var runPowerShell = func(script string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", strictPowerShell(script))
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func strictPowerShell(script string) string {
	return "$ErrorActionPreference = 'Stop'\n" + script
}

type schtasksManager struct{}

func newPlatformManager() (Manager, error) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		return nil, fmt.Errorf("powershell.exe not found: Windows Task Scheduler management requires PowerShell")
	}
	return &schtasksManager{}, nil
}

func (*schtasksManager) Platform() string { return "schtasks" }

func (m *schtasksManager) Install(cfg Config) error {
	if err := os.MkdirAll(DefaultDataDir(), 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	scriptPath := windowsTaskScriptPath()
	// 0644 has weak semantics on Windows; the file ACL is what matters.
	// We still write 0600 so the file's POSIX bits do not advertise read
	// access, and rely on the user's own profile ACLs for primary defense
	// (the script lives under %USERPROFILE%\.omp-lark-bridge by default).
	// WriteFile only applies perm on create, so Chmod the existing file
	// after writing to harden reinstalls of pre-existing 0644 scripts.
	if err := os.WriteFile(scriptPath, []byte(buildWindowsTaskScript(cfg)), 0600); err != nil {
		return fmt.Errorf("write task script: %w", err)
	}
	if err := os.Chmod(scriptPath, 0600); err != nil {
		return fmt.Errorf("chmod task script: %w", err)
	}

	if err := stopWindowsTask(); err != nil {
		slog.Warn("schtasks: stop existing task failed", "error", err)
	}
	if err := deleteWindowsTask(); err != nil {
		if windowsTaskMatchesAction(scriptPath) {
			if err := m.Start(); err != nil {
				return fmt.Errorf("start existing task: %w", err)
			}
			return nil
		}
		return err
	}

	if err := createWindowsTask(scriptPath); err != nil {
		return err
	}

	if err := m.Start(); err != nil {
		return fmt.Errorf("start task: %w", err)
	}
	return nil
}

func (*schtasksManager) Uninstall() error {
	if err := stopWindowsTask(); err != nil {
		slog.Warn("schtasks: stop task failed", "error", err)
	}
	if err := deleteWindowsTask(); err != nil {
		return err
	}
	if err := os.Remove(windowsTaskScriptPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove task script: %w", err)
	}
	return nil
}

func (*schtasksManager) Start() error {
	return startWindowsTask()
}

func (*schtasksManager) Stop() error {
	if err := stopWindowsTask(); err != nil {
		return err
	}
	return nil
}

func (*schtasksManager) Restart() error {
	if err := stopWindowsTask(); err != nil {
		slog.Warn("schtasks: stop before restart failed", "error", err)
	}
	return startWindowsTask()
}

func (*schtasksManager) Status() (*Status, error) {
	st := &Status{Platform: "schtasks"}

	out, err := runPowerShell(fmt.Sprintf(`
$task = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if ($null -eq $task) { exit 1 }
Write-Output $task.State
`, powerShellLiteral(windowsTaskName)))
	if err != nil {
		return st, nil
	}
	st.Installed = true

	taskStatus := strings.TrimSpace(out)
	if strings.EqualFold(taskStatus, "Running") {
		st.Running = true
	}
	return st, nil
}

func windowsTaskScriptPath() string {
	return filepath.Join(DefaultDataDir(), windowsScriptName)
}

func windowsTaskAction(scriptPath string) string {
	return fmt.Sprintf(`powershell.exe %s`, windowsTaskActionArgs(scriptPath))
}

func windowsTaskActionArgs(scriptPath string) string {
	return fmt.Sprintf(`-WindowStyle Hidden -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%s"`, scriptPath)
}

func createWindowsTask(scriptPath string) error {
	out, err := runPowerShell(fmt.Sprintf(`
$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument %s
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited
Register-ScheduledTask -TaskName %s -Action $action -Trigger $trigger -Principal $principal -Force | Out-Null
`, powerShellLiteral(windowsTaskActionArgs(scriptPath)), powerShellLiteral(windowsTaskName)))
	if err != nil {
		return fmt.Errorf("register scheduled task: %s (%w)", out, err)
	}
	return nil
}

func windowsTaskMatchesAction(scriptPath string) bool {
	out, err := runPowerShell(fmt.Sprintf(`
$task = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if ($null -eq $task) { exit 1 }
$expectedArgs = %s
foreach ($action in $task.Actions) {
	if (($action.Execute -ieq 'powershell.exe') -and ($action.Arguments -eq $expectedArgs)) {
		Write-Output 'true'
		exit 0
	}
}
exit 1
`, powerShellLiteral(windowsTaskName), powerShellLiteral(windowsTaskActionArgs(scriptPath))))
	return err == nil && strings.EqualFold(strings.TrimSpace(out), "true")
}

func buildWindowsTaskScript(cfg Config) string {
	var sb strings.Builder
	sb.WriteString("$ErrorActionPreference = 'Stop'\r\n")
	writePowerShellEnv(&sb, "CC_LOG_FILE", cfg.LogFile)
	writePowerShellEnv(&sb, "CC_LOG_MAX_SIZE", strconv.FormatInt(cfg.LogMaxSize, 10))
	writePowerShellEnv(&sb, "CC_LOG_MAX_BACKUPS", strconv.Itoa(cfg.LogMaxBackups))
	if cfg.EnvPATH != "" {
		writePowerShellEnv(&sb, "PATH", cfg.EnvPATH)
	}
	if len(cfg.EnvExtra) > 0 {
		keys := make([]string, 0, len(cfg.EnvExtra))
		for key := range cfg.EnvExtra {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if !isValidEnvName(key) {
				slog.Warn("daemon: windows: dropping invalid env name from EnvExtra",
					"key", key)
				continue
			}
			value := cfg.EnvExtra[key]
			if value == "" {
				continue
			}
			writePowerShellEnv(&sb, key, value)
		}
	}
	fmt.Fprintf(&sb, "Set-Location -LiteralPath %s\r\n", powerShellLiteral(cfg.WorkDir))
	sb.WriteString("while ($true) {\r\n")
	fmt.Fprintf(&sb, "  & %s\r\n", powerShellLiteral(cfg.BinaryPath))
	sb.WriteString("  $exitCode = $LASTEXITCODE\r\n")
	sb.WriteString("  if ($exitCode -eq 0) { exit 0 }\r\n")
	sb.WriteString("  Start-Sleep -Seconds 10\r\n")
	sb.WriteString("}\r\n")
	return sb.String()
}

func writePowerShellEnv(sb *strings.Builder, key, value string) {
	fmt.Fprintf(sb, "$env:%s = %s\r\n", key, powerShellLiteral(value))
}

func powerShellLiteral(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func stopWindowsTask() error {
	out, err := runPowerShell(fmt.Sprintf(`
$task = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if ($null -eq $task) { exit 0 }
if ($task.State -eq 'Running') {
	Stop-ScheduledTask -TaskName %s
}
for ($i = 0; $i -lt 20; $i++) {
	$task = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
	if ($null -eq $task -or $task.State -ne 'Running') { exit 0 }
	Start-Sleep -Milliseconds 500
}
Write-Error 'scheduled task did not stop within timeout'
exit 1
`, powerShellLiteral(windowsTaskName), powerShellLiteral(windowsTaskName), powerShellLiteral(windowsTaskName)))
	if err != nil {
		return fmt.Errorf("stop scheduled task: %s (%w)", out, err)
	}
	return nil
}

func startWindowsTask() error {
	out, err := runPowerShell(fmt.Sprintf(`
$task = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if ($null -eq $task) { Write-Error 'scheduled task not found'; exit 1 }
if ($task.State -ne 'Running') { Start-ScheduledTask -TaskName %s }
`, powerShellLiteral(windowsTaskName), powerShellLiteral(windowsTaskName)))
	if err != nil {
		return fmt.Errorf("start scheduled task: %s (%w)", out, err)
	}
	return nil
}

func deleteWindowsTask() error {
	out, err := runPowerShell(fmt.Sprintf(`
$task = Get-ScheduledTask -TaskName %s -ErrorAction SilentlyContinue
if ($null -eq $task) { exit 0 }
Unregister-ScheduledTask -TaskName %s -Confirm:$false
`, powerShellLiteral(windowsTaskName), powerShellLiteral(windowsTaskName)))
	if err != nil {
		return fmt.Errorf("delete scheduled task: %s (%w)", out, err)
	}
	return nil
}

// CheckLinger is a no-op on Windows (always returns false).
func CheckLinger() (enabled bool, user string) {
	return false, ""
}

// RestartServiceAsync restarts the scheduled task without blocking the caller.
//
// The helper waits for the HTTP response to flush, ends the task (which kills
// the wrapper and this bridge), then runs it again. It must not live in the
// task's process tree: `schtasks /End` terminates the task's job object, so a
// child of the bridge would die at /End and never reach /Run, leaving the
// service down. WMI process creation parents the helper under WmiPrvSE instead,
// which is why it is the primary path; the fallback is an explicit job
// breakaway, and if the job forbids that we fail instead of gambling on a
// helper that /End can reach.
//
// Only valid under the Task Scheduler daemon: the generated wrapper exports
// CC_LOG_FILE, so its presence is what distinguishes "managed by schtasks" from
// a foreground run (where ending the task would leave two instances competing
// for the instance lock).
func RestartServiceAsync() error {
	if strings.TrimSpace(os.Getenv("CC_LOG_FILE")) == "" {
		return fmt.Errorf("async restart needs the scheduled-task daemon (CC_LOG_FILE is unset)")
	}
	// The helper outlives the bridge, so its transcript only survives on disk.
	helperLog := filepath.Join(os.TempDir(), "omp-lark-bridge-restart.log")
	scriptPath := filepath.Join(os.TempDir(), "omp-lark-bridge-restart.ps1")
	script := fmt.Sprintf(`'helper start ' + (Get-Date -Format o) | Add-Content -Encoding utf8 -LiteralPath %s
Start-Sleep -Seconds 2
schtasks.exe /End /TN %s *>> %s
Start-Sleep -Seconds 2
schtasks.exe /Run /TN %s *>> %s
'helper done ' + (Get-Date -Format o) | Add-Content -Encoding utf8 -LiteralPath %s
`,
		powerShellLiteral(helperLog),
		powerShellLiteral(windowsTaskName), powerShellLiteral(helperLog),
		powerShellLiteral(windowsTaskName), powerShellLiteral(helperLog),
		powerShellLiteral(helperLog))
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write restart helper script: %w", err)
	}

	helperCmd := "powershell.exe -NoProfile -NonInteractive -WindowStyle Hidden " +
		"-ExecutionPolicy Bypass -File \"" + scriptPath + "\""
	spawnViaWMI := fmt.Sprintf(
		`$r = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{CommandLine=%s}
if ($r.ReturnValue -ne 0) { throw ('Win32_Process.Create returned ' + $r.ReturnValue) }`,
		powerShellLiteral(helperCmd))
	if _, err := runPowerShell(spawnViaWMI); err == nil {
		return nil
	}

	cmd := exec.Command("powershell.exe",
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-ExecutionPolicy", "Bypass", "-File", scriptPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | createBreakawayFromJob,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn restart helper (WMI and job breakaway both failed): %w", err)
	}
	return cmd.Process.Release()
}
