//go:build !windows

package main

import (
	"errors"
	"fmt"
)

var errServiceUnsupported = errors.New("windows service mode is only available on Windows")

func runServiceHost(string) error {
	return errServiceUnsupported
}

func runServiceCommand(args []string) error {
	fmt.Println("Windows service mode is only available on Windows; use `daemon install` instead.")
	return errServiceUnsupported
}
