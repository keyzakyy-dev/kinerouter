//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detach puts the gateway in its own session so a Ctrl-C in the launching
// terminal, or quitting the tray, does not deliver SIGINT to it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// stopProcess asks politely; the gateway already shuts down gracefully on SIGTERM.
func stopProcess(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
