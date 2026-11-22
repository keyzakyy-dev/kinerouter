// Command kinerouter-tray puts a KineRouter icon in the desktop tray and can
// start the gateway on demand.
//
// It is a second binary on purpose. A GUI toolkit cannot be linked into
// `kinerouter` without breaking the CGO_ENABLED=0 cross-build the release
// workflow depends on, so the tray lives outside the core program. gui.go
// carries `//go:build windows || tray`; everywhere else this compiles to a stub
// that explains the missing tag instead of failing in a confusing way, which
// keeps `go vet ./...` and `go test ./...` green on a machine without GTK.
//
// Rules the tray follows:
//
//   - It never reads or prints a credential. Branding comes from /api/version,
//     which is unauthenticated; the dashboard password is never involved.
//   - It starts only its own sibling `kinerouter` binary, resolved next to the
//     tray executable. A same-named program earlier on PATH is not trusted.
//   - "Quit Tray" releases the icon only. It does not stop a gateway it did not
//     start, and it does not stop one it did.
//   - Unreadable state renders as unknown, never as "stopped".
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := runGUI(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
