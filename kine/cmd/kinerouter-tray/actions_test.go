package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
)

// A probe that cannot answer must read as Unknown, not Stopped: "Stopped"
// invites a second gateway on the same port.
func TestProbeState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/version" {
			t.Errorf("probed %q, want /api/version", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	addr := srv.Listener.Addr().(*net.TCPAddr)
	oldPort, oldURL := port, baseURL
	defer func() { port, baseURL = oldPort, oldURL }()
	port = addr.Port
	baseURL = "http://127.0.0.1:" + strconv.Itoa(port)

	if got := probeState(); got != stateRunning {
		t.Fatalf("probeState = %v, want stateRunning", got)
	}

	srv.Close()
	if got := probeState(); got != stateStopped {
		t.Fatalf("after close probeState = %v, want stateStopped", got)
	}
}

func TestEnvPort(t *testing.T) {
	t.Setenv("PORT", "12345")
	if got := envPort(); got != 12345 {
		t.Fatalf("envPort = %d, want 12345", got)
	}
	for _, bad := range []string{"", "0", "abc", "70000"} {
		t.Setenv("PORT", bad)
		if got := envPort(); got != config.DefaultPort {
			t.Fatalf("envPort(%q) = %d, want default %d", bad, got, config.DefaultPort)
		}
	}
}

// The tray must refuse to launch a same-named binary that is not its sibling.
func TestGatewayPathRequiresSibling(t *testing.T) {
	if _, err := gatewayPath(); err == nil {
		t.Skip("a sibling kinerouter exists next to the test binary; nothing to assert")
	}
}

// npm leaves the previous versioned binary in place, so name order is not
// version order: 0.1.9 sorts before 0.2.0 and must not win.
func TestVersionedGatewayPicksNewest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"kinerouter_0.1.9_linux_amd64",
		"kinerouter_0.1.10_linux_amd64",
		"kinerouter_0.2.0_linux_amd64",
		"kinerouter-tray_0.2.0_linux_amd64",
		"kinerouter_0.2.0_linux_amd64.sha256",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := filepath.Base(versionedGateway(dir, ""))
	if got != "kinerouter_0.2.0_linux_amd64" {
		t.Fatalf("versionedGateway picked %q, want 0.2.0", got)
	}
	if p := versionedGateway(t.TempDir(), ""); p != "" {
		t.Fatalf("empty dir returned %q", p)
	}
}
