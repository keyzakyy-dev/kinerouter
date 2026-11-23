//go:build !windows

package launcherpid

import (
	"os"
	"strconv"
	"syscall"
)

const syscallZero = syscall.Signal(0)

func kill(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func itoa(n int) string { return strconv.Itoa(n) }

func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscallZero) == nil
}

// exeName resolves the real executable, not the process title: a program that
// rewrites its title must not defeat the PID-reuse guard.
func exeName(pid int) string {
	p, err := os.Readlink("/proc/" + itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return p
}
