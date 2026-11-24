//go:build !windows

package updater

import (
	"os"
	"os/exec"
	"syscall"
)

// Restart starts a detached shell that waits for this process to release the
// listener, then execs the new binary. The delay cannot live in the child: an
// update often targets a release older than this code, which would not know
// the wait flag and would lose the port race.
func Restart(exe string) error {
	args := append([]string{"-c", `sleep 2; exec "$0" "$@"`, exe}, os.Args[1:]...)
	cmd := exec.Command("sh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Start()
}
