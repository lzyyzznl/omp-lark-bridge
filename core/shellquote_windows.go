//go:build windows

package core

import "strings"

// shellChain joins commands in the platform shell. Windows PowerShell 5.1
// (the default powershell.exe) does not accept "&&", so chain with ";".
const shellChain = " ; "

// shellQuote wraps s in PowerShell single quotes (embedded ' doubled).
// The one-liners built with it are copy-paste hints for Windows terminals,
// so they must parse under powershell.exe, not /bin/sh.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
