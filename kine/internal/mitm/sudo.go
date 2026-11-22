package mitm

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// SudoAvailable reports whether `sudo` is on PATH. Minimal images (Alpine in
// Docker) have none; there the caller runs commands unprivileged and the
// system-wide steps are simply skipped.
func SudoAvailable() bool {
	_, err := exec.LookPath("sudo")
	return err == nil
}

// SudoPasswordRequired is true when sudo exists but needs a password.
func SudoPasswordRequired() bool {
	if !SudoAvailable() {
		return false
	}
	return exec.Command("sudo", "-n", "true").Run() != nil
}

// ExecPrivileged runs one shell command, feeding the sudo password on stdin.
// Without sudo in PATH the command runs as the current user (containers).
func ExecPrivileged(command, password string) (string, error) {
	var cmd *exec.Cmd
	if SudoAvailable() && !IsRoot() {
		cmd = exec.Command("sudo", "-S", "sh", "-c", command)
		cmd.Stdin = strings.NewReader(password + "\n")
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "incorrect password") || strings.Contains(msg, "no password was provided") {
			return "", errors.New("Wrong sudo password")
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

// shellQuote single-quotes a value for sh -c.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// IsRoot reports whether this process already runs as root.
func IsRoot() bool { return os.Getuid() == 0 }
