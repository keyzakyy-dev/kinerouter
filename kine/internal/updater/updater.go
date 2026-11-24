// Package updater replaces the running gateway with the release of a given
// version. It is channel-aware on purpose: the npm shim owns its versioned
// binary in the data dir, so npm installs update through npm; a script install
// owns a single bin path, so it downloads and swaps that file.
//
// Verification is mandatory: no update is written without a sha256 from the
// release's checksums.txt.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultRepo = "keyzakyy-dev/kinerouter"
	maxAsset    = 150 << 20
)

// Method is how this binary was delivered; it decides who replaces the file.
const (
	MethodNPM    = "npm"
	MethodScript = "script"
	MethodDocker = "docker"
)

func repoName() string {
	if r := os.Getenv("UPDATE_REPO"); r != "" {
		return r
	}
	return defaultRepo
}

// releaseBase is the release download root. Tests point it at a local server.
func releaseBase() string {
	if b := os.Getenv("KINEROUTER_RELEASE_BASE"); b != "" {
		return strings.TrimSuffix(b, "/")
	}
	return "https://github.com/" + repoName() + "/releases/download"
}

func assetName(version string) string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("kinerouter_%s_%s_%s%s", version, runtime.GOOS, runtime.GOARCH, ext)
}

// Apply installs version and returns the binary to start next. A docker
// install cannot replace its own image and reports the command instead.
func Apply(ctx context.Context, method, version, dataDir string) (string, error) {
	switch method {
	case MethodNPM:
		if err := npmInstall(ctx); err != nil {
			return "", err
		}
		exe, err := npmGateway(dataDir, version)
		if err != nil {
			return "", err
		}
		return exe, nil
	case MethodDocker:
		return "", fmt.Errorf("docker installs update with: docker pull ghcr.io/%s:latest && docker restart kinerouter", repoName())
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if err := installScript(ctx, exe, version); err != nil {
		return "", err
	}
	return exe, nil
}

// npmInstall hands the update to npm so the package, its shim and the runtime
// binary stay one version. --prefer-online defeats a stale local cache.
func npmInstall(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "npm", "i", "-g", "kinerouter@latest", "--prefer-online")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm update failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// npmGateway resolves the versioned gateway binary npm just wrote. The exact
// target name is preferred; otherwise the newest one in runtime/ is returned.
func npmGateway(dataDir, version string) (string, error) {
	dir := filepath.Join(dataDir, "runtime")
	exact := filepath.Join(dir, assetName(version))
	if st, err := os.Stat(exact); err == nil && !st.IsDir() {
		return exact, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read runtime dir: %w", err)
	}
	best, bestVer := "", ""
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, "kinerouter_") || !strings.HasSuffix(n, suffix) {
			continue
		}
		v := strings.TrimSuffix(strings.TrimPrefix(n, "kinerouter_"), suffix)
		if i := strings.IndexByte(v, '_'); i >= 0 {
			v = v[:i]
		}
		if best == "" || compare(v, bestVer) > 0 {
			best, bestVer = n, v
		}
	}
	if best == "" {
		return "", fmt.Errorf("npm finished but no gateway binary is in %s", dir)
	}
	return filepath.Join(dir, best), nil
}

func compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, y := 0, 0
		if i < len(pa) {
			x = atoi(pa[i])
		}
		if i < len(pb) {
			y = atoi(pb[i])
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

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// installScript downloads the release asset, verifies it against the release
// checksums and swaps it into exe. A missing or mismatched digest keeps the
// running binary untouched.
func installScript(ctx context.Context, exe, version string) error {
	asset := assetName(version)
	base := releaseBase() + "/kine-v" + version
	client := &http.Client{Timeout: 90 * time.Second}

	sums, err := get(ctx, client, base+"/checksums.txt", 1<<20)
	if err != nil {
		return fmt.Errorf("release checksums: %w", err)
	}
	want := checksumFor(string(sums), asset)
	if want == "" {
		return fmt.Errorf("release has no checksum for %s", asset)
	}
	raw, err := get(ctx, client, base+"/"+asset, maxAsset)
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	got := sha256.Sum256(raw)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		return fmt.Errorf("checksum mismatch for %s", asset)
	}

	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".kinerouter-update-*")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("chmod temp binary: %w", err)
	}

	// Windows permits renaming a running image but not replacing it in place,
	// so the old file moves aside first and is restored if any rename fails.
	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("backup running binary: %w", err)
	}
	if err := os.Rename(tmpPath, exe); err != nil {
		_ = os.Rename(old, exe)
		return fmt.Errorf("install new binary: %w", err)
	}
	_ = os.Remove(old)
	return nil
}

func get(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kinerouter-updater")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func checksumFor(sums, asset string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "./")
		if name == asset {
			return fields[0]
		}
	}
	return ""
}
