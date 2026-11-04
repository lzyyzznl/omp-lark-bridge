package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

// ompwebNPMPackage is the npm package that ships the ompweb CLI.
const ompwebNPMPackage = "@kahme247/ompweb"

// ompwebStopper is the platform-specific handle to a supervised ompweb child.
// On systemd platforms it is nil (systemd owns the unit); on Windows the
// bridge spawns and supervises the child itself.
type ompwebStopper interface {
	Stop()
}

// ompwebPort returns the configured listen port (default 30177).
func ompwebPort(cfg config.OmpWebConfig) int {
	if cfg.Port > 0 {
		return cfg.Port
	}
	return config.DefaultOmpWebPort
}

// ompwebURL is the loopback URL the console links to and probes.
func ompwebURL(cfg config.OmpWebConfig) string {
	return fmt.Sprintf("http://127.0.0.1:%d", ompwebPort(cfg))
}

// resolveOmpwebBin picks the ompweb executable. Order: explicit config bin →
// PATH `ompweb` → npx fallback. The bool reports whether the npx form was used.
func resolveOmpwebBin(cfg config.OmpWebConfig) (bin string, npx bool) {
	if p := strings.TrimSpace(cfg.Bin); p != "" {
		return p, false
	}
	if p, err := exec.LookPath("ompweb"); err == nil {
		return p, false
	}
	return "npx", true
}

// ensureOmpwebInstalled makes the ompweb CLI available. ompweb is a hard
// dependency: when neither the configured bin nor PATH resolves it, this
// installs it globally via npm and is fatal on failure.
func ensureOmpwebInstalled(cfg config.OmpWebConfig) error {
	if p := strings.TrimSpace(cfg.Bin); p != "" {
		if _, err := os.Stat(p); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath("ompweb"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return fmt.Errorf("ompweb is required but neither `ompweb` nor `npm` is available: install Node.js >= 24 and run `npm install -g %s`", ompwebNPMPackage)
	}
	slog.Info("ompweb: installing hard dependency", "package", ompwebNPMPackage)
	cmd := exec.Command("npm", "install", "-g", ompwebNPMPackage)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install %s: %w", ompwebNPMPackage, err)
	}
	if _, err := exec.LookPath("ompweb"); err != nil {
		return fmt.Errorf("installed %s but `ompweb` is not on PATH: %w", ompwebNPMPackage, err)
	}
	return nil
}
