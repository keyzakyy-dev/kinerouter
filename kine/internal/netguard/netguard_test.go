package netguard

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// Loopback, RFC1918, link-local metadata and IPv6 unique-local must all be
// refused; a public address must pass.
func TestBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1",
		"169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fd00::1", "fe80::1"}
	for _, s := range blocked {
		ip := mustAddr(t, s)
		if !BlockedIP(ip) {
			t.Fatalf("%s should be blocked", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if BlockedIP(mustAddr(t, s)) {
			t.Fatalf("%s should be allowed", s)
		}
	}
	// An IPv4 address mapped into IPv6 must be judged by its v4 value.
	if !BlockedIP(mustAddr(t, "::ffff:127.0.0.1")) {
		t.Fatal("mapped loopback not blocked")
	}
}

func TestCheckURLRejectsNonPublicTargets(t *testing.T) {
	for _, u := range []string{
		"http://localhost/", "http://127.0.0.1:8080/x", "http://169.254.169.254/latest/meta-data/",
		"file:///etc/passwd", "gopher://example.com/", "http://10.0.0.5/",
	} {
		if err := CheckURL(t.Context(), u); err == nil {
			t.Fatalf("%s should be rejected", u)
		}
	}
}

// The dial itself must refuse a blocked address: CheckURL resolves once and the
// dial resolves again, so a client whose only defence is CheckURL is rebindable.
// A live loopback server stands in for the rebinding answer.
func TestClientRefusesBlockedDial(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("reached"))
	}))
	defer ts.Close()

	if _, err := Client(2 * time.Second).Get(ts.URL); err == nil {
		t.Fatal("Client dialed a loopback address; the Control hook is not wired")
	}
	if _, err := NewTransport(nil).RoundTrip(mustReq(t, ts.URL)); err == nil {
		t.Fatal("NewTransport dialed a loopback address")
	}
}

// Redirects are re-validated, so a public URL cannot bounce into a private one.
func TestClientRejectsRedirectToBlockedHost(t *testing.T) {
	c := Client(time.Second)
	req := mustReq(t, "http://127.0.0.1:8080/secret")
	if err := c.CheckRedirect(req, []*http.Request{req}); err == nil {
		t.Fatal("redirect to loopback was accepted")
	}
}

func mustReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("build request %s: %v", raw, err)
	}
	return req
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return a
}
