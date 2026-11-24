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
	verNotes  string
	verAt     time.Time
)

func updateRepo() string {
	if r := os.Getenv("UPDATE_REPO"); r != "" {
		return r
	}
	return "keyzakyy-dev/kinerouter"
}

// fetchLatestRelease reads the newest kine-v release from updateRepo, with the
// release body used as dashboard notes. The /releases/latest endpoint is not
// used: this repository also publishes unprefixed tags, and "latest" is ordered
// by creation time across all of them, so it can name a release this binary
// cannot install. The list is newest-first, so the first kine-v entry wins.
func fetchLatestRelease() (string, string) {
	base := os.Getenv("KINEROUTER_UPDATE_API")
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequest(http.MethodGet,
		strings.TrimSuffix(base, "/")+"/repos/"+updateRepo()+"/releases?per_page=50", nil)
	if err != nil {
		return "", ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kinerouter/"+Version)
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ""
	}
	var out []struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil {
		return "", ""
	}
	for _, r := range out {
		v, ok := strings.CutPrefix(r.TagName, "kine-v")
		if !ok || v == "" {
			continue
		}
		return v, strings.TrimSpace(r.Body)
	}
	return "", ""
}

func latestVersionCached() string {
	verMu.Lock()
	defer verMu.Unlock()
	if verLatest != "" && time.Since(verAt) < versionCacheTTL {
		return verLatest
	}
	if v, notes := fetchLatestRelease(); v != "" {
		verLatest, verNotes, verAt = v, notes, time.Now()
	}
	return verLatest
}

// cachedReleaseNotes returns the notes that belong to the cached latest
// version. It is empty until the first successful check.
func cachedReleaseNotes() string {
	verMu.Lock()
	defer verMu.Unlock()
	return verNotes
}

// semver is a version split into its numeric core and prerelease identifiers.
// Build metadata after "+" is dropped: semver gives it no precedence.
type semver struct {
	core [3]int
	pre  []string
}

// parseSemver splits a version. A leading "v" is ignored, and a missing or
// malformed core component counts as 0.
func parseSemver(v string) semver {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var out semver
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, out.pre = v[:i], strings.Split(v[i+1:], ".")
	}
	parts := strings.Split(core, ".")
	for i := 0; i < 3 && i < len(parts); i++ {
		out.core[i] = atoiSafe(parts[i])
	}
	return out
}

func (s semver) prerelease() bool { return len(s.pre) > 0 }

// compareSemver returns 1, 0 or -1 by semver precedence: numeric core first,
// then prerelease identifiers (a final release outranks any prerelease of the
// same core; numeric identifiers compare as numbers).
func compareSemver(a, b string) int {
	x, y := parseSemver(a), parseSemver(b)
	for i := range x.core {
		if x.core[i] != y.core[i] {
			if x.core[i] > y.core[i] {
				return 1
			}
			return -1
		}
	}
	return comparePrerelease(x.pre, y.pre)
}

func comparePrerelease(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		an, aNum := numericID(a[i])
		bn, bNum := numericID(b[i])
		switch {
		case aNum && bNum:
			if an > bn {
				return 1
			}
			return -1
		case aNum:
			return -1
		case bNum:
			return 1
		case a[i] > b[i]:
			return 1
		default:
			return -1
		}
	}
	switch {
	case len(a) > len(b):
		return 1
	case len(a) < len(b):
		return -1
	}
	return 0
}

func numericID(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// autoApplyAllowed keeps an unattended update from moving a final release onto
// a prerelease. A user already on a prerelease keeps rolling forward, and a
// final release is always welcome.
func autoApplyAllowed(current, latest string) bool {
	return parseSemver(current).prerelease() || !parseSemver(latest).prerelease()
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
		return installMethodFor(exe)
	}
	return "script"
}

// installMethodFor classifies a binary path. It is separate from
// InstallMethod so the delivery shapes npm and the script installer produce
// can be asserted without replacing the test process.
func installMethodFor(exe string) string {
	p := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(exe), "\\", "/"))
	switch {
	case strings.Contains(p, "/node_modules/kinerouter/"):
		return "npm"
	case strings.Contains(p, "/kinerouter/runtime/"),
		strings.Contains(p, "/.kinerouter/runtime/"),
		// A custom DATA_DIR keeps the same delivery shape one level away
		// from the home directory, so match the versioned runtime file.
		strings.Contains(p, "/runtime/kinerouter_"):
		return "npm"
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

// refreshLatestVersion fetches from GitHub and refreshes the hour-long cache.
// The dashboard's explicit check and the update trigger use it so a stale
// cache never hides a release the user asked about.
func refreshLatestVersion() string {
	v, notes := fetchLatestRelease()
	if v == "" {
		return latestVersionCached()
	}
	verMu.Lock()
	verLatest, verNotes, verAt = v, notes, time.Now()
	verMu.Unlock()
	return v
}

func versionPayload(latest string) map[string]any {
	dev := IsDevBuild()
	hasUpdate := !dev && latest != "" && compareSemver(latest, Version) > 0
	// Both field naming schemes: KineRouter clients read currentVersion/latestVersion,
	// this repo's SPA and older clients read version/latest.
	return map[string]any{
		"version":         Version,
		"latest":          latest,
		"updateAvailable": hasUpdate,
		"currentVersion":  Version,
		"latestVersion":   latest,
		"hasUpdate":       hasUpdate,
		"channel":         map[bool]string{true: "dev", false: "release"}[dev],
		"installMethod":   InstallMethod(),
		"installCommand":  InstallCommand(),
		"releaseNotes":    cachedReleaseNotes(),
	}
}

func (g *Gateway) handleVersion(w http.ResponseWriter, r *http.Request) {
	latest := ""
	if !IsDevBuild() {
		latest = latestVersionCached()
	}
	writeJSON(w, http.StatusOK, versionPayload(latest))
}

// handleVersionCheck answers GET /api/version/check with a fresh GitHub query.
// Fail-open: an unreachable API returns the cached answer instead of an error.
func (g *Gateway) handleVersionCheck(w http.ResponseWriter, r *http.Request) {
	latest := refreshLatestVersion()
	if IsDevBuild() {
		latest = ""
	}
	writeJSON(w, http.StatusOK, versionPayload(latest))
}
