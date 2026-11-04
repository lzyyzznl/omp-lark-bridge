//go:build !linux

package main

import "github.com/lzyyzznl/omp-lark-bridge/config"

// ensureOmpwebService is a no-op outside systemd platforms. ompweb remains a
// hard dependency (the CLI is installed), but unit-level ordering is only
// implemented for systemd today.
func ensureOmpwebService(_ config.OmpWebConfig) error { return nil }
