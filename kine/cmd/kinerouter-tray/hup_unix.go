//go:build !windows

package main

import (
	"os/signal"
	"syscall"
)

// ignoreHangup keeps the icon alive when the terminal that launched it closes.
// On macOS the gateway intentionally stays in the login session (a detached
// status item does not render), so the tray — not the gateway — is the process
// that would otherwise die on SIGHUP.
func ignoreHangup() {
	signal.Ignore(syscall.SIGHUP)
}
