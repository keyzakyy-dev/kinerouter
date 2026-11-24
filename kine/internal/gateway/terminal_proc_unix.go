//go:build !windows

package gateway

import (
	"context"
	"os/exec"
	"syscall"
)

func terminalShell(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	// Own process group: a timeout kills the pipeline's children too, not just
	// the shell that spawned them.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

func killTerminal(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
