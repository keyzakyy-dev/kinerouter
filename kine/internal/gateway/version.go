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
	"strings"
	"sync"
	"time"
)

// Version is the local build version; override at link time:
//
//	go build -ldflags "-X .../internal/gateway.Version=0.2.0"
var Version = "0.1.0"

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

func (g *Gateway) handleVersion(w http.ResponseWriter, r *http.Request) {
	latest := latestVersionCached()
	hasUpdate := latest != "" && compareSemver(latest, Version) > 0
	// Both field naming schemes: KineRouter clients read currentVersion/latestVersion,
	// this repo's SPA and older clients read version/latest.
	writeJSON(w, http.StatusOK, map[string]any{
		"version":         Version,
		"latest":          latest,
		"updateAvailable": hasUpdate,
		"currentVersion":  Version,
		"latestVersion":   latest,
		"hasUpdate":       hasUpdate,
	})
}
