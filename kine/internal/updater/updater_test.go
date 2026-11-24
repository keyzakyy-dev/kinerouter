package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestChecksumFor(t *testing.T) {
	sums := "abc  kinerouter_0.2.0_linux_amd64\n" +
		"def ./kinerouter-tray_0.2.0_linux_amd64\n" +
		"broken line\n" +
		"ghi  kinerouter_0.2.0_windows_amd64.exe\n"
	if got := checksumFor(sums, "kinerouter_0.2.0_linux_amd64"); got != "abc" {
		t.Fatalf("exact = %q", got)
	}
	if got := checksumFor(sums, "kinerouter_0.2.0_windows_amd64.exe"); got != "ghi" {
		t.Fatalf("exe = %q", got)
	}
	if got := checksumFor(sums, "kinerouter_0.2.1_linux_amd64"); got != "" {
		t.Fatalf("missing entry = %q", got)
	}
}

// installScript must verify and swap the file, and leave no backup behind.
func TestInstallScript(t *testing.T) {
	payload := []byte("new-binary-" + strings.Repeat("x", 64))
	sum := sha256.Sum256(payload)
	asset := assetName("0.0.2")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n"))
		case strings.HasSuffix(r.URL.Path, "/"+asset):
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("KINEROUTER_RELEASE_BASE", srv.URL)

	exe := filepath.Join(t.TempDir(), "kinerouter")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installScript(context.Background(), exe, "0.0.2"); err != nil {
		t.Fatalf("installScript: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("installed payload differs (%d bytes)", len(got))
	}
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatalf("backup left behind: %v", err)
	}
}

// A release without a digest for this asset is not installable.
func TestInstallScriptRequiresChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			_, _ = w.Write([]byte("deadbeef  some-other-asset\n"))
			return
		}
		_, _ = w.Write([]byte("payload"))
	}))
	defer srv.Close()
	t.Setenv("KINEROUTER_RELEASE_BASE", srv.URL)

	exe := filepath.Join(t.TempDir(), "kinerouter")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := installScript(context.Background(), exe, "0.0.2")
	if err == nil || !strings.Contains(err.Error(), "no checksum") {
		t.Fatalf("err = %v, want missing checksum", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old" {
		t.Fatalf("binary was modified on a refused update: %q", got)
	}
}

func TestInstallScriptRejectsMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checksums.txt") {
			_, _ = w.Write([]byte(strings.Repeat("a", 64) + "  " + assetName("0.0.2") + "\n"))
			return
		}
		_, _ = w.Write([]byte("tampered"))
	}))
	defer srv.Close()
	t.Setenv("KINEROUTER_RELEASE_BASE", srv.URL)

	exe := filepath.Join(t.TempDir(), "kinerouter")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := installScript(context.Background(), exe, "0.0.2")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want checksum mismatch", err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "old" {
		t.Fatalf("binary replaced despite mismatch")
	}
}

// npm keeps every version it installed; the resolver must pick the newest and
// prefer the exact target when it exists.
func TestNpmGateway(t *testing.T) {
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "runtime")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, v := range []string{"0.1.9", "0.2.0"} {
		name := "kinerouter_" + v + "_" + runtime.GOOS + "_" + runtime.GOARCH + ext
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := npmGateway(dataDir, "0.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "kinerouter_0.1.9_"+runtime.GOOS+"_"+runtime.GOARCH+ext {
		t.Fatalf("exact version not preferred: %s", got)
	}
	got, err = npmGateway(dataDir, "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "kinerouter_0.2.0_"+runtime.GOOS+"_"+runtime.GOARCH+ext {
		t.Fatalf("newest not chosen: %s", got)
	}
}
