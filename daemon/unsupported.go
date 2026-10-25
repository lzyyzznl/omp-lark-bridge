//go:build !linux && !darwin && !windows

package daemon

import (
	"fmt"
	"runtime"
)

func newPlatformManager() (Manager, error) {
	return nil, fmt.Errorf("daemon management is not supported on %s; use a process manager (e.g. nssm, pm2) instead", runtime.GOOS)
}

// CheckLinger is a no-op on unsupported platforms (always returns false).
func CheckLinger() (enabled bool, user string) {
	return false, ""
}

// RestartServiceAsync is unsupported on this platform.
func RestartServiceAsync() error {
	return fmt.Errorf("async restart unsupported on %s", runtime.GOOS)
}
