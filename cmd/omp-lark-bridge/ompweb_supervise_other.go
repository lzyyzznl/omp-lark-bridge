//go:build !windows

package main

import "github.com/lzyyzznl/omp-lark-bridge/config"

// startOmpwebSupervisor is a no-op outside Windows: on systemd platforms the
// daemon unit starts ompweb.service alongside the bridge (Wants/After), so the
// bridge must not spawn a second instance.
func startOmpwebSupervisor(_ config.OmpWebConfig) ompwebStopper { return nil }
