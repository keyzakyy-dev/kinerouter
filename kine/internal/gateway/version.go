package gateway

// Version reporting + update check. Ported from src/app/api/version/route.js,
// adapted for a Go binary: the "latest" version comes from GitHub Releases
// The npm package `kinerouter` is only a shim around these same binaries, so
// the Go binary's own update check must follow the release tag, not the registry.
//
// Fail-open: an offline host, a proxy, or a 404 all yield "no update", never an
// error on the route — a gateway must start and serve regardless.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Version is the local build version; override at link time:
//
//	go build -ldflags "-X .../internal/gateway.Version=0.2.0"
var Version = "0.1.0"

// IsDevBuild reports a working-copy build: a `-dev` version suffix (see the
// dev build command below) or KINEROUTER_DEV set. Dev builds never advertise
// an update, because their version string is a git rev, not a release number.
//
//	go build -ldflags "-X .../internal/gateway.Version=$(git rev-parse --short HEAD)-dev"
func IsDevBuild() bool {
	return strings.Contains(Version, "-dev") || os.Getenv("KINEROUTER_DEV") != ""
}

const versionCacheTTL = time.Hour

var (
	verMu     sync.Mutex
	verLatest string
	verAt     time.Time
)

func updateRepo() string {
	if r := os.Getenv("UPDATE_REPO"); r != "" {
		return r
	}
	return "keyzakyy-dev/kinerouter"
}

// fetchLatestVersion reads the newest GitHub release tag for updateRepo.
func fetchLatestVersion() string {
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/"+updateRepo()+"/releases/latest", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kinerouter/"+Version)
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		TagName string `json:"tag_name"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return ""
	}
	// Releases are tagged kine-vX.Y.Z so the Go binary's versions never collide
	// with KineRouter's own vX.Y.Z tags in this repo.
	return strings.TrimPrefix(strings.TrimPrefix(out.TagName, "kine-"), "v")
}

func latestVersionCached() string {
	verMu.Lock()
	defer verMu.Unlock()
	if verLatest != "" && time.Since(verAt) < versionCacheTTL {
		return verLatest
	}
	if v := fetchLatestVersion(); v != "" {
		verLatest, verAt = v, time.Now()
	}
	return verLatest
}

// compareSemver returns 1, 0 or -1. Unparseable segments count as 0.
func compareSemver(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			x = atoiSafe(pa[i])
		}
		if i < len(pb) {
			y = atoiSafe(pb[i])
		}
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// UpdateInfo returns the newest release and whether it is newer than this
// build. One call, one network attempt, shared by the launcher and the API.
// A dev build never has an update: it is ahead of the last release by design.
func UpdateInfo() (string, bool) {
	if IsDevBuild() {
		return "", false
	}
	latest := latestVersionCached()
	return latest, latest != "" && compareSemver(latest, Version) > 0
}

// InstallMethod names the way this binary was delivered, decided from the
// binary's own path so it survives a restart without the shim's environment:
//
//   - npm keeps the binary under node_modules/kinerouter or the runtime dir
//     (~/.kinerouter/runtime, %LOCALAPPDATA%\kinerouter\runtime)
//   - a script/manual install puts it in a bin directory
//   - Docker mounts the binary at /kinerouter, next to /.dockerenv
//
// Update advice must match the real install path: telling a curl user to run
// npm leaves two binaries on PATH, which is exactly the drift the single-owner
// rule avoids. KINEROUTER_INSTALL remains an explicit override for layouts
// this cannot classify.
func InstallMethod() string {
	if v := os.Getenv("KINEROUTER_INSTALL"); v != "" {
		return v
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	if exe, err := os.Executable(); err == nil {
		p := strings.ToLower(filepath.ToSlash(exe))
		switch {
		case strings.Contains(p, "/node_modules/kinerouter/"):
			return "npm"
		case strings.Contains(p, "/kinerouter/runtime/"), strings.Contains(p, "/.kinerouter/runtime/"):
			return "npm"
		}
	}
	return "script"
}

// InstallCommand is the one-liner that updates an existing install.
func InstallCommand() string {
	switch InstallMethod() {
	case "npm":
		return "npm i -g kinerouter@latest --prefer-online"
	case "docker":
		return "docker pull ghcr.io/keyzakyy-dev/kinerouter:latest && docker restart kinerouter"
	}
	return "curl -fsSL https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.sh | sh"
}

func (g *Gateway) handleVersion(w http.ResponseWriter, r *http.Request) {
	dev := IsDevBuild()
	latest := ""
	hasUpdate := false
	if !dev {
		latest = latestVersionCached()
		hasUpdate = latest != "" && compareSemver(latest, Version) > 0
	}
	// Both field naming schemes: KineRouter clients read currentVersion/latestVersion,
	// this repo's SPA and older clients read version/latest.
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         Version,
		"latest":          latest,
		"updateAvailable": hasUpdate,
		"currentVersion":  Version,
		"latestVersion":   latest,
		"hasUpdate":       hasUpdate,
		"channel":         map[bool]string{true: "dev", false: "release"}[dev],
		"installMethod":   InstallMethod(),
		"installCommand":  InstallCommand(),
	})
}
