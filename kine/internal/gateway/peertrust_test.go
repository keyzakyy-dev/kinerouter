package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
)

// A remote peer must not be able to spoof any forwarding header: X-Forwarded-For
// selects the client IP for rate limiting, and Proto/Host decide the session
// cookie's Secure flag and the OAuth redirect_uri scheme. A loopback peer is a
// local reverse proxy and is trusted, as before.
func TestPeerTrustStripsForwardingHeadersFromRemotePeer(t *testing.T) {
	spoof := []string{"X-Forwarded-For", "X-Real-IP", "X-Kine-Real-IP",
		"X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Ssl", "Front-End-Https"}

	check := func(remote string) (kept []string) {
		h := (&Gateway{}).withPeerTrust(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, k := range spoof {
				if r.Header.Get(k) != "" {
					kept = append(kept, k)
				}
			}
		}))
		r := httptest.NewRequest(http.MethodGet, "http://example/api/settings", nil)
		r.RemoteAddr = remote
		for _, k := range spoof {
			r.Header.Set(k, "spoofed")
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return kept
	}

	if kept := check("203.0.113.7:41234"); len(kept) != 0 {
		t.Fatalf("remote peer kept forwarding headers: %v", kept)
	}
	if kept := check("127.0.0.1:41234"); len(kept) != len(spoof) {
		t.Fatalf("loopback proxy lost forwarding headers: kept %v", kept)
	}
}

// TRUST_PROXY must actually change the limiter key, but only from a loopback
// peer: the reference trusts XFF only behind a configured reverse proxy.
func TestClientIPHonorsTrustProxy(t *testing.T) {
	newReq := func(remote, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://example/api/auth/login", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	direct := &Gateway{}
	proxied := &Gateway{cfg: &config.Config{TrustProxy: true}}

	if got := proxied.clientIP(newReq("127.0.0.1:4000", "198.51.100.9, 10.0.0.1")); got != "198.51.100.9" {
		t.Fatalf("trusted proxy clientIP = %q", got)
	}
	if got := direct.clientIP(newReq("127.0.0.1:4000", "198.51.100.9")); got != "127.0.0.1" {
		t.Fatalf("untrusted clientIP = %q", got)
	}
	if got := proxied.clientIP(newReq("203.0.113.7:4000", "198.51.100.9")); got != "203.0.113.7" {
		t.Fatalf("remote peer spoofed XFF: %q", got)
	}
}

// CORS stays open for the compatibility API but is not advertised on cookie-
// protected dashboard endpoints: the SPA is same-origin and needs no ACAO.
func TestCORSScope(t *testing.T) {
	check := func(path string) string {
		h := (&Gateway{}).withCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		return rr.Header().Get("Access-Control-Allow-Origin")
	}
	if got := check("/v1/chat/completions"); got != "*" {
		t.Fatalf("v1 ACAO = %q, want *", got)
	}
	if got := check("/api/keys"); got != "" {
		t.Fatalf("dashboard ACAO = %q, want empty", got)
	}
}
