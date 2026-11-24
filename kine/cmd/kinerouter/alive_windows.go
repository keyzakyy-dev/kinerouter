//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func processAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	return err == nil && strings.Contains(string(out), fmt.Sprintf(" %d ", pid))
}

func signalTerm(pid int) error {
	if p, err := os.FindProcess(pid); err == nil {
		return p.Kill()
	}
	return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}

// signalKill is the escalation step; on Windows Kill() is already forceful, so
// this is the same call and exists only to keep the call site portable.
func signalKill(pid int) error { return signalTerm(pid) }
