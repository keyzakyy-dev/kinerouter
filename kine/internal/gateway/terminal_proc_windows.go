//go:build windows

package gateway

import (
	"context"
	"os/exec"
)

// ponytail: cmd.exe has no process groups, so a timeout stops the shell only.
// Add a Job Object when a wedged Windows pipeline needs the whole tree killed.
func terminalShell(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "cmd", "/c", command)
}

func killTerminal(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
