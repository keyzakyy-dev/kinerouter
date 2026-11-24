//go:build windows

package updater

import (
	"os"
	"os/exec"
	"strings"
)

// Restart waits in a detached cmd, then starts the new binary. A shell delay is
// used instead of a child-side flag because the downloaded release may predate
// that flag.
func Restart(exe string) error {
	quoted := `"` + strings.ReplaceAll(exe, `"`, `""`) + `"`
	line := `timeout /t 2 /nobreak >nul & start "" ` + quoted
	cmd := exec.Command("cmd", "/c", line)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Start()
}
