// Package mitm is the local MITM proxy: a TLS server on :443 that
// terminates connections for tool hosts redirected here by /etc/hosts, and
// either forwards them untouched or re-routes them through this router.
// Mirrors src/mitm/*. Linux and macOS only — Windows uses a different
// elevation model (UAC / PowerShell) and is not ported: ponytail, add when a
// Windows build exists.
package mitm

import "path/filepath"

// Paths under <DATA_DIR>/mitm. Kept in one place so the dashboard API and the
// privileged server process agree without argument.
type Paths struct {
	Dir       string
	PIDFile   string
	LockFile  string
	RootKey   string
	RootCert  string
	AliasFile string
}

func NewPaths(dataDir string) Paths {
	dir := filepath.Join(dataDir, "mitm")
	return Paths{
		Dir:       dir,
		PIDFile:   filepath.Join(dir, ".mitm.pid"),
		LockFile:  filepath.Join(dir, ".mitm.lock"),
		RootKey:   filepath.Join(dir, "rootCA.key"),
		RootCert:  filepath.Join(dir, "rootCA.crt"),
		AliasFile: filepath.Join(dir, "aliases.json"),
	}
}
