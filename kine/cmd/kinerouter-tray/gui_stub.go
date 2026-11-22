//go:build !windows && !tray

package main

import (
	"fmt"
	"runtime"
)

// Without the `tray` tag this is a plain Go binary, so the systray package (and
// its GTK3 / libayatana-appindicator3 dependencies) is never linked. Rebuilding
// with the tag is the fix, and saying so beats an unresolvable C symbol.
func runGUI() error {
	return fmt.Errorf(
		"this kinerouter-tray build has no GUI: rebuild with -tags tray "+
			"(required on %s; Windows needs no tag)", runtime.GOOS)
}
