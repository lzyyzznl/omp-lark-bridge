//go:build windows

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

// ompwebLaunchCommand builds the child command for Windows. npm global bins are
// .cmd shims, which CreateProcess cannot exec directly, so they run through
// cmd.exe /c.
func ompwebLaunchCommand(cfg config.OmpWebConfig) *exec.Cmd {
	bin, npx := resolveOmpwebBin(cfg)
	port := strconv.Itoa(ompwebPort(cfg))
	var args []string
	if npx {
		args = []string{"-y", ompwebNPMPackage, "--no-open", "--port", port}
	} else {
		args = []string{"--no-open", "--port", port}
	}
	lower := strings.ToLower(bin)
	if npx || strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") {
		return exec.Command("cmd", append([]string{"/c", bin}, args...)...)
	}
	return exec.Command(bin, args...)
}

// ompwebSupervisor spawns ompweb as a child of the bridge and restarts it if it
// exits unexpectedly. A reachable instance on the port is reused.
type ompwebSupervisor struct {
	cfg    config.OmpWebConfig
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
}

// startOmpwebSupervisor launches the supervisor goroutine and returns it.
func startOmpwebSupervisor(cfg config.OmpWebConfig) ompwebStopper {
	ctx, cancel := context.WithCancel(context.Background())
	s := &ompwebSupervisor{cfg: cfg, cancel: cancel, done: make(chan struct{})}
	go s.run(ctx)
	return s
}

func (s *ompwebSupervisor) run(ctx context.Context) {
	defer close(s.done)

	url := ompwebURL(s.cfg)
	if ompwebReachable(ctx, url) {
		slog.Info("ompweb: already reachable; reusing", "url", url)
		return
	}

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		cmd := ompwebLaunchCommand(s.cfg)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}
		if err := cmd.Start(); err != nil {
			s.mu.Unlock()
			slog.Error("ompweb: start failed", "error", err)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}
		s.cmd = cmd
		s.mu.Unlock()
		slog.Info("ompweb: started", "pid", cmd.Process.Pid, "url", url)

		err := cmd.Wait()

		s.mu.Lock()
		s.cmd = nil
		stopped := s.stopped
		s.mu.Unlock()
		if stopped || ctx.Err() != nil {
			return
		}
		slog.Warn("ompweb: exited; restarting", "error", err, "backoff", backoff)
		if !sleepCtx(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// Stop terminates the child and waits for the supervisor goroutine to finish.
func (s *ompwebSupervisor) Stop() {
	s.mu.Lock()
	s.stopped = true
	cmd := s.cmd
	s.mu.Unlock()
	s.cancel()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		slog.Warn("ompweb: shutdown timed out")
	}
}

func ompwebReachable(ctx context.Context, url string) bool {
	c, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(c, http.MethodGet, url+"/", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
