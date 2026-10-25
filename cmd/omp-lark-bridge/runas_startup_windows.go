//go:build windows

package main

import (
	"context"

	"github.com/lzyyzznl/omp-lark-bridge/config"
)

func runRunAsUserStartupChecks(_ context.Context, _ *config.Config) error {
	return nil
}
