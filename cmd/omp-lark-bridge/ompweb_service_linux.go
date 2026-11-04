//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

// ensureOmpwebService makes ompweb.service a usable Wants target for the
// bridge unit: if the unit already exists it is enabled and started; otherwise
// ompweb's own systemd installer is invoked to create it.
func ensureOmpwebService(cfg config.OmpWebConfig) error {
	if userUnitExists("ompweb.service") {
		return userSystemctl("enable", "--now", "ompweb.service")
	}
	var cmd *exec.Cmd
	bin, npx := resolveOmpwebBin(cfg)
	if npx {
		cmd = exec.Command(bin, append([]string{"-y", ompwebNPMPackage}, "systemd", "install")...)
	} else {
		cmd = exec.Command(bin, "systemd", "install")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run `ompweb systemd install`: %w", err)
	}
	return userSystemctl("enable", "--now", "ompweb.service")
}

// userUnitExists reports whether systemd knows the given user unit.
func userUnitExists(name string) bool {
	return exec.Command("systemctl", "--user", "cat", name).Run() == nil
}

func userSystemctl(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %s (%w)", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}
