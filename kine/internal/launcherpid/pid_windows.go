//go:build windows

package launcherpid

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const syscallZero = syscall.Signal(0)

func kill(pid int) error {
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
}

func itoa(n int) string { return strconv.Itoa(n) }

func alive(pid int) bool {
	return exeName(pid) != ""
}

func exeName(pid int) string {
	out, err := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, "\"") {
		return ""
	}
	name := strings.SplitN(strings.TrimPrefix(line, "\""), "\"", 2)[0]
	if name == "" || strings.EqualFold(name, "INFO:") {
		return ""
	}
	return name
}
