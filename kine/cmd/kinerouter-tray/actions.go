package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// state is what the menu shows. Unknown exists so a failed probe is never
// rendered as "stopped": telling the user the gateway is down because the
// probe timed out is how you get a second copy started on the same port.
type state int

const (
	stateUnknown state = iota
	stateRunning
	stateStopped
)

var (
	mu      sync.Mutex
	child   *exec.Cmd // the gateway this tray started, if any
	port    = envPort()
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	probe   = &http.Client{Timeout: 2 * time.Second}
)

// envPort mirrors the gateway's own PORT default so the tray follows a custom
// port without sharing the full config loader (which also mints a JWT secret).
func envPort() int {
	if n, err := strconv.Atoi(os.Getenv("PORT")); err == nil && n > 0 && n < 65536 {
		return n
	}
	return 20128
}

// urlFor returns the dashboard URL. Loopback only: the tray is a local
// companion and must never be pointed at a remote host from the environment.
func urlFor(path string) string { return baseURL + path }

// gatewayPath resolves the gateway binary as a sibling of this executable.
// A bare PATH lookup is deliberately not used — a same-named program earlier
// on PATH would then be launched with the user's data directory.
func gatewayPath() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	name := "kinerouter"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := filepath.Dir(self)
	p := filepath.Join(dir, name)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return p, nil
	}
	// Release assets and the npm package name the gateway with its version
	// (kinerouter_0.1.1_windows_amd64.exe), so accept that shape too — still only
	// from this directory, never from PATH. npm never removes the previous
	// version, so the highest version wins; a directory scan is name-ordered,
	// which would happily launch 0.1.9 over 0.2.0.
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	if p := versionedGateway(dir, suffix); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%s not found next to the tray: install the two binaries in one directory", p)
}

// versionedGateway returns the newest kinerouter_<version>_<os>_<arch> file in
// dir, or "" when none exists. Versions compare numerically, component by
// component; a tie keeps the first name the scan returned.
func versionedGateway(dir, suffix string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best, bestVer := "", ""
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "kinerouter_") || !strings.HasSuffix(n, suffix) {
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(n, "kinerouter_"), suffix)
		if i := strings.IndexByte(v, '_'); i >= 0 {
			v = v[:i]
		}
		if best == "" || compareVersion(v, bestVer) > 0 {
			best, bestVer = n, v
		}
	}
	if best == "" {
		return ""
	}
	return filepath.Join(dir, best)
}

// compareVersion orders dotted numeric versions. A non-numeric component
// counts as 0, matching the gateway's own lenient update comparison.
func compareVersion(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = atoiTray(pa[i])
		}
		if i < len(pb) {
			y = atoiTray(pb[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

func atoiTray(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// probeState asks the gateway's own unauthenticated endpoint whether it is up.
func probeState() state {
	resp, err := probe.Get(urlFor("/api/version"))
	if err != nil {
		// Distinguish "refused" (down) from "not answering" (unknown).
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "timeout") {
			return stateUnknown
		}
		return stateStopped
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return stateUnknown
	}
	return stateRunning
}

// startGateway launches the sibling binary detached, so quitting the tray does
// not take the gateway down with it.
func startGateway() error {
	mu.Lock()
	defer mu.Unlock()
	if child != nil && child.ProcessState == nil {
		return errors.New("already started from this tray")
	}
	if probeState() == stateRunning {
		return errors.New("a gateway is already listening on " + baseURL)
	}
	bin, err := gatewayPath()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin)
	cmd.Env = os.Environ()
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	child = cmd
	go func() { _ = cmd.Wait() }() // reap; keeps ProcessState meaningful
	return nil
}

// stopGateway stops only a gateway this tray started. Anything else belongs to
// the user (a terminal, systemd, another launcher) and is not ours to kill.
func stopGateway() error {
	mu.Lock()
	defer mu.Unlock()
	if child == nil || child.Process == nil || child.ProcessState != nil {
		return errors.New("no gateway started from this tray")
	}
	if err := stopProcess(child.Process); err != nil {
		return err
	}
	child = nil
	return nil
}

func startedByTray() bool {
	mu.Lock()
	defer mu.Unlock()
	return child != nil && child.ProcessState == nil
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
