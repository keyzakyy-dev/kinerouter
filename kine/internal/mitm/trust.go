package mitm

// System trust-store installation for the Root CA. Mirrors
// src/mitm/cert/install.js: Linux probes four distro layouts, then adds the
// cert to every user NSS database (Chrome / Firefox) when `certutil` exists.
// macOS uses the System keychain. Windows is not ported.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

const trustDestName = "kinerouter-root-ca.crt"

type linuxTrust struct {
	dir string
	cmd string
}

var linuxTrustDirs = []linuxTrust{
	{"/usr/local/share/ca-certificates", "update-ca-certificates"},
	{"/etc/ca-certificates/trust-source/anchors", "update-ca-trust"},
	{"/etc/pki/ca-trust/source/anchors", "update-ca-trust"},
	{"/etc/pki/trust/anchors", "update-ca-certificates"},
}

func currentLinuxTrust() linuxTrust {
	for _, t := range linuxTrustDirs {
		if st, err := os.Stat(t.dir); err == nil && st.IsDir() {
			return t
		}
	}
	return linuxTrustDirs[0]
}

// CertTrusted reports whether the Root CA is installed in the system store.
func CertTrusted(p Paths) bool {
	if _, err := os.Stat(p.RootCert); err != nil {
		return false
	}
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("security", "find-certificate", "-a", "-c", rootCACN,
			"/Library/Keychains/System.keychain").Output()
		return err == nil && strings.Contains(string(out), rootCACN)
	default:
		dest := currentLinuxTrust().dir + "/" + trustDestName
		_, err := os.Stat(dest)
		return err == nil
	}
}

// TrustCert installs the Root CA system-wide. The bool reports whether the
// system store was updated; user NSS databases are updated regardless.
func TrustCert(p Paths, password string) (bool, error) {
	if _, err := os.Stat(p.RootCert); err != nil {
		return false, fmt.Errorf("Certificate file not found")
	}
	if runtime.GOOS == "darwin" {
		if !SudoAvailable() {
			return false, fmt.Errorf("sudo is required to install the certificate")
		}
		cmd := `security delete-certificate -c ` + shellQuote(rootCACN) +
			` /Library/Keychains/System.keychain 2>/dev/null || true; ` +
			`security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ` + shellQuote(p.RootCert)
		if _, err := ExecPrivileged(cmd, password); err != nil {
			return false, fmt.Errorf("Certificate install failed: %v", err)
		}
		return true, nil
	}

	t := currentLinuxTrust()
	dest := t.dir + "/" + trustDestName
	// NSS first: works without sudo.
	updateNSS(p.RootCert, "add")
	if !SudoAvailable() {
		return false, nil
	}
	cmd := "cp " + shellQuote(p.RootCert) + " " + shellQuote(dest) +
		" && (" + t.cmd + " 2>/dev/null || true)"
	if _, err := ExecPrivileged(cmd, password); err != nil {
		return false, fmt.Errorf("Certificate install failed: %v", err)
	}
	return true, nil
}

// UntrustCert removes the Root CA from the system store and user databases.
func UntrustCert(p Paths, password string) error {
	updateNSS(p.RootCert, "delete")
	switch runtime.GOOS {
	case "darwin":
		if !SudoAvailable() {
			return nil
		}
		_, err := ExecPrivileged("security delete-certificate -c "+shellQuote(rootCACN)+
			" /Library/Keychains/System.keychain", password)
		return err
	default:
		if !SudoAvailable() {
			return nil
		}
		t := currentLinuxTrust()
		cmd := "rm -f " + shellQuote(t.dir+"/"+trustDestName) + " && (" + t.cmd + " 2>/dev/null || true)"
		_, err := ExecPrivileged(cmd, password)
		return err
	}
}

// updateNSS adds/removes the cert from every NSS DB it can find. Best-effort:
// a missing certutil or no databases is not an error.
func updateNSS(certPath, action string) {
	if _, err := exec.LookPath("certutil"); err != nil {
		return
	}
	dbs := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		for _, d := range firefoxProfiles(home) {
			dbs = append(dbs, d)
		}
	}
	// NSS DBs that may contain the cert, in insertion order.
	home, _ := os.UserHomeDir()
	for _, d := range []string{home + "/.pki/nssdb", home + "/snap/chromium/current/.pki/nssdb"} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			dbs = append(dbs, d)
		}
	}
	for _, db := range dbs {
		var cmd *exec.Cmd
		if action == "add" {
			cmd = exec.Command("certutil", "-d", "sql:"+db, "-A", "-t", "C,,", "-n", rootCACN, "-i", certPath)
		} else {
			cmd = exec.Command("certutil", "-d", "sql:"+db, "-D", "-n", rootCACN)
		}
		_ = cmd.Run()
	}
}

func firefoxProfiles(home string) []string {
	var out []string
	for _, base := range []string{
		home + "/.mozilla/firefox",
		home + "/snap/firefox/common/.mozilla/firefox",
	} {
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := base + "/" + e.Name()
			if _, err := os.Stat(dir + "/cert9.db"); err == nil {
				out = append(out, dir)
			}
		}
	}
	return out
}
