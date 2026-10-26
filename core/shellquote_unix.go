//go:build !windows

package core

import "strings"

// shellChain joins commands in the platform shell (POSIX &&).
const shellChain = " && "

// shellQuote wraps s in POSIX single quotes, escaping embedded quotes.
// Used instead of fmt %q because the probe runs under /bin/sh.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
