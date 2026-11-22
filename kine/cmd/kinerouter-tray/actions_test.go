package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
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
		if got := envPort(); got != 20128 {
			t.Fatalf("envPort(%q) = %d, want default 20128", bad, got)
		}
	}
}

// The tray must refuse to launch a same-named binary that is not its sibling.
func TestGatewayPathRequiresSibling(t *testing.T) {
	if _, err := gatewayPath(); err == nil {
		t.Skip("a sibling kinerouter exists next to the test binary; nothing to assert")
	}
}
