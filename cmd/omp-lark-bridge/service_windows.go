//go:build windows

package main

// Windows Service (SCM) hosting for the bridge.
//
// The binary doubles as its own service host: SCM starts
// `omp-lark-bridge.exe --service-run --config <service.json>`, which spawns the
// real bridge as a hidden child (CREATE_NO_WINDOW), restarts it with backoff
// when it exits, and shuts it down when SCM asks. No console window, no
// PowerShell wrapper, no interactive logon requirement.
//
// SCM runs services as LocalSystem, so the installer snapshots the invoking
// user's environment (PATH, HOME, USERPROFILE, ...) into the service config and
// the host replays it for the child — otherwise `omp`, node shims and the
// agent's ~/.omp state would be invisible to the service.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const (
	serviceName        = "omp-lark-bridge"
	serviceDisplayName = "omp-lark-bridge"
	serviceDescription = "Feishu/Lark <-> omp bridge (background service, no console window)"

	// CREATE_NO_WINDOW: give the child a console that is never shown.
	createNoWindow = 0x08000000
)

// serviceConfig is the on-disk descriptor written by `service install` and read
// by the host. Keeping it in JSON (instead of encoding everything in the SCM
// ImagePath) makes paths with spaces/quotes a non-issue and lets us carry the
// environment snapshot.
type serviceConfig struct {
	BinaryPath string            `json:"binary_path"` // bridge executable to supervise
	Args       []string          `json:"args"`        // args for the bridge (e.g. --config ...)
	WorkDir    string            `json:"work_dir"`
	LogFile    string            `json:"log_file"` // host events (child logs itself via CC_LOG_FILE)
	Env        map[string]string `json:"env"`      // replay for the child
}

func serviceConfigPath() string {
	base := os.Getenv("ProgramData")
	if strings.TrimSpace(base) == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, serviceName, "service.json")
}

func loadServiceConfig(path string) (*serviceConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service config %s: %w", path, err)
	}
	var cfg serviceConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("decode service config %s: %w", path, err)
	}
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		return nil, fmt.Errorf("service config %s has no binary_path", path)
	}
	return &cfg, nil
}

// hostLogger appends host events to its own file; the child owns the bridge log.
type hostLogger struct {
	mu   sync.Mutex
	path string
}

func (l *hostLogger) logf(format string, args ...any) {
	if l == nil || l.path == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02T15:04:05.000Z07:00"), fmt.Sprintf(format, args...))
}

// mergedEnv applies the snapshot on top of the service process environment.
func mergedEnv(overrides map[string]string) []string {
	env := make(map[string]string, len(overrides)+8)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[strings.ToUpper(k)] = v
		}
	}
	for k, v := range overrides {
		env[strings.ToUpper(k)] = v
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

type bridgeService struct {
	cfg      *serviceConfig
	log      *hostLogger
	mu       sync.Mutex
	child    *exec.Cmd
	stopping bool
	done     chan struct{}
}

func (s *bridgeService) Start() error {
	s.done = make(chan struct{})
	go s.supervise()
	return nil
}

// supervise runs the bridge, restarting it with growing backoff until Stop.
func (s *bridgeService) supervise() {
	defer close(s.done)
	backoff := 2 * time.Second

	for {
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			return
		}
		cmd := exec.Command(s.cfg.BinaryPath, s.cfg.Args...)
		cmd.Dir = s.cfg.WorkDir
		cmd.Env = mergedEnv(s.cfg.Env)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		cmd.Stdout = nil
		cmd.Stderr = nil
		startedAt := time.Now()
		if err := cmd.Start(); err != nil {
			s.mu.Unlock()
			s.log.logf("start %s failed: %v", s.cfg.BinaryPath, err)
			time.Sleep(backoff)
			if backoff < time.Minute {
				backoff *= 2
			}
			continue
		}
		s.child = cmd
		s.mu.Unlock()
		s.log.logf("child started pid=%d", cmd.Process.Pid)

		err := cmd.Wait()

		s.mu.Lock()
		s.child = nil
		stopping := s.stopping
		s.mu.Unlock()

		if stopping {
			s.log.logf("child exited after stop request: %v", err)
			return
		}
		s.log.logf("child exited (uptime %s): %v; restarting", time.Since(startedAt).Round(time.Second), err)
		if time.Since(startedAt) > 5*time.Minute {
			backoff = 2 * time.Second // crash-loop guard resets after a healthy run
		}
		time.Sleep(backoff)
		s.mu.Lock()
		stopRequested := s.stopping
		s.mu.Unlock()
		if stopRequested {
			return
		}
		if backoff < time.Minute {
			backoff *= 2
		}
	}
}

func (s *bridgeService) Stop() {
	s.mu.Lock()
	s.stopping = true
	child := s.child
	s.mu.Unlock()

	if child != nil && child.Process != nil {
		// /T kills the omp child tree too, otherwise a restart would collide
		// with the previous omp processes.
		kill := exec.Command("taskkill", "/PID", fmt.Sprint(child.Process.Pid), "/T", "/F")
		kill.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		if out, err := kill.CombinedOutput(); err != nil {
			s.log.logf("taskkill failed: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		child.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(15 * time.Second):
		s.log.logf("supervisor did not stop within 15s")
	}
}

func (s *bridgeService) Execute(_ []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	elog, _ := eventlog.Open(serviceName)
	defer elog.Close()

	if err := s.Start(); err != nil {
		s.log.logf("start failed: %v", err)
		return false, 1
	}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for c := range r {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			s.log.logf("stop requested (event %d)", c.EventType)
			s.Stop()
			return false, 0
		case svc.Interrogate:
			changes <- c.CurrentStatus
		}
	}
	return false, 0
}

// runServiceHost is the entry point for SCM: `--service-run --config <json>`.
func runServiceHost(configPath string) error {
	if configPath == "" {
		configPath = serviceConfigPath()
	}
	cfg, err := loadServiceConfig(configPath)
	if err != nil {
		return err
	}
	logPath := cfg.LogFile
	if strings.TrimSpace(logPath) == "" {
		logPath = filepath.Join(filepath.Dir(configPath), "service-host.log")
	}
	svcImpl := &bridgeService{cfg: cfg, log: &hostLogger{path: logPath}}
	return svc.Run(serviceName, svcImpl)
}

// --- install / uninstall / control -----------------------------------------

func scCommand(args ...string) (string, error) {
	cmd := exec.Command("sc.exe", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("sc %s: %w (%s)", strings.Join(args, " "), err, text)
	}
	return text, nil
}

func serviceStatus() (string, error) {
	out, err := scCommand("query", serviceName)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(strings.ToUpper(line), "STATE") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				return strings.Join(fields[3:], " "), nil
			}
		}
	}
	return strings.TrimSpace(out), nil
}

func runServiceInstall(args []string) error {
	configPath := "config.toml"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 < len(args) {
				i++
				configPath = args[i]
			}
		case "--data-dir", "--help", "-h":
			fmt.Println("Usage: omp-lark-bridge service install [--config <path>]")
			fmt.Println("Installs the Windows service (LocalSystem, auto-start, restart on failure).")
			return nil
		}
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(binaryPath); err == nil {
		binaryPath = resolved
	}
	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	workDir := filepath.Dir(absConfig)

	dataDir := os.Getenv("ProgramData")
	if strings.TrimSpace(dataDir) == "" {
		dataDir = `C:\ProgramData`
	}
	svcDir := filepath.Join(dataDir, serviceName)
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		return err
	}

	env := captureServiceEnv()
	logFile := filepath.Join(env["USERPROFILE"], ".omp-lark-bridge", "logs", "omp-lark-bridge.log")
	if strings.TrimSpace(env["USERPROFILE"]) == "" {
		logFile = filepath.Join(svcDir, "omp-lark-bridge.log")
	}

	cfg := serviceConfig{
		BinaryPath: binaryPath,
		Args:       []string{"--config", absConfig},
		WorkDir:    workDir,
		LogFile:    filepath.Join(svcDir, "service-host.log"),
		Env:        env,
	}
	cfg.Env["CC_LOG_FILE"] = logFile
	cfg.Env["CC_LOG_MAX_SIZE"] = "10485760"
	cfg.Env["CC_LOG_MAX_BACKUPS"] = "3"

	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	cfgPath := serviceConfigPath()
	if err := os.WriteFile(cfgPath, raw, 0o644); err != nil {
		return err
	}

	// Tear down a previous installation (ignore "not found").
	_, _ = scCommand("stop", serviceName)
	_, _ = scCommand("delete", serviceName)

	binPath := fmt.Sprintf(`"%s" --service-run --config "%s"`, binaryPath, cfgPath)
	if _, err := scCommand("create", serviceName, "start=", "auto", "DisplayName=", serviceDisplayName, "binPath=", binPath); err != nil {
		return err
	}
	if _, err := scCommand("description", serviceName, serviceDescription); err != nil {
		fmt.Printf("warning: set description: %v\n", err)
	}
	// Restart policy: SCM restarts the host if it dies; the host itself keeps
	// the bridge alive between crashes.
	if _, err := scCommand("failure", serviceName, "reset=", "86400", "actions=", "restart/5000/restart/15000/restart/60000"); err != nil {
		fmt.Printf("warning: set failure actions: %v\n", err)
	}
	if _, err := scCommand("start", serviceName); err != nil {
		fmt.Printf("warning: start service: %v\n", err)
	}

	fmt.Printf("Service installed and started.\n")
	fmt.Printf("  Name:    %s\n", serviceName)
	fmt.Printf("  Binary:  %s\n", binaryPath)
	fmt.Printf("  Config:  %s\n", absConfig)
	fmt.Printf("  Data:    %s\n", cfgPath)
	fmt.Printf("  Log:     %s\n", logFile)
	return nil
}

func runServiceUninstall() error {
	_, _ = scCommand("stop", serviceName)
	out, err := scCommand("delete", serviceName)
	if err != nil {
		return err
	}
	_ = os.Remove(serviceConfigPath())
	fmt.Println(strings.TrimSpace(out))
	return nil
}

func runServiceControl(action string) error {
	switch action {
	case "start":
		if _, err := scCommand("start", serviceName); err != nil {
			return err
		}
	case "stop":
		if _, err := scCommand("stop", serviceName); err != nil {
			return err
		}
	case "restart":
		_, _ = scCommand("stop", serviceName)
		time.Sleep(2 * time.Second)
		if _, err := scCommand("start", serviceName); err != nil {
			return err
		}
	}
	status, _ := serviceStatus()
	fmt.Printf("%s: %s\n", serviceName, status)
	return nil
}

func runServiceStatus() error {
	out, err := scCommand("query", serviceName)
	if err != nil {
		fmt.Printf("%s: not installed\n", serviceName)
		return nil
	}
	cfg, cfgErr := loadServiceConfig(serviceConfigPath())
	fmt.Println(strings.TrimSpace(out))
	if cfgErr == nil {
		fmt.Printf("\nBinary: %s\nArgs:   %s\nWorkDir:%s\n", cfg.BinaryPath, strings.Join(cfg.Args, " "), cfg.WorkDir)
	}
	return nil
}

// captureServiceEnv snapshots the environment the service needs. SCM starts
// services with the bare SYSTEM environment, which has none of the user's
// PATH shims (npm/scoop/node) and no HOME for the agent's ~/.omp state.
func captureServiceEnv() map[string]string {
	keep := []string{
		"PATH", "PATHEXT", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
		"TEMP", "TMP", "SystemRoot", "SystemDrive", "ComSpec", "USERNAME",
		"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "PROGRAMDATA",
		"OS", "NUMBER_OF_PROCESSORS", "COMPUTERNAME", "LANG", "TZ",
	}
	out := make(map[string]string, len(keep))
	for _, k := range keep {
		if v := os.Getenv(k); v != "" {
			out[k] = v
		}
	}
	return out
}

// runServiceCommand dispatches `omp-lark-bridge service ...`.
func runServiceCommand(args []string) error {
	if len(args) == 0 {
		fmt.Println("Usage: omp-lark-bridge service <install|uninstall|start|stop|restart|status> [--config <path>]")
		return nil
	}
	switch strings.ToLower(args[0]) {
	case "install":
		return runServiceInstall(args[1:])
	case "uninstall", "remove":
		return runServiceUninstall()
	case "start", "stop", "restart":
		return runServiceControl(strings.ToLower(args[0]))
	case "status":
		return runServiceStatus()
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}
