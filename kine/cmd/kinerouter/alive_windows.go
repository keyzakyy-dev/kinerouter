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
