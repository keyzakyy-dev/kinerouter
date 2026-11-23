//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
)

func ownerAlive(pid int) bool {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
	return err == nil && strings.Contains(string(out), fmt.Sprintf(" %d ", pid))
}
