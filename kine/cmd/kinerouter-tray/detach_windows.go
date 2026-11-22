//go:build windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedProcess / detachedWindow keep the gateway alive after the tray exits,
// and hide it when the tray was built as a GUI app (-H=windowsgui).
const (
	detachedProcess = 0x00000008
	detachedWindow  = 0x08000000
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: detachedProcess | detachedWindow,
	}
}

// stopProcess has no SIGTERM on Windows: the console-less gateway only answers
// to a kill, which is acceptable because SQLite is in WAL mode and the process
// holds no state that a hard exit loses beyond the in-flight request.
func stopProcess(p *os.Process) error {
	return p.Kill()
}
