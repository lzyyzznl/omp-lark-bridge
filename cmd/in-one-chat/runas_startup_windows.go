//go:build windows

package main

import (
	"context"

	"github.com/lzyyzznl/in-one-chat/config"
)

func runRunAsUserStartupChecks(_ context.Context, _ *config.Config) error {
	return nil
}
