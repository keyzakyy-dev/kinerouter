package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
