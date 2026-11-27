package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The reference accepts these schemes and silently ignores anything else, so a
// typo in the dashboard must not break every upstream call.
func TestParseOutboundProxy(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantURL bool
	}{
		{"http", "http://127.0.0.1:7897", true},
		{"https", "https://proxy.example:8080", true},
		{"socks5", "socks5://127.0.0.1:1080", true},
		{"socks5h", "socks5h://user:pw@127.0.0.1:1080", true},
		{"unsupported scheme", "ftp://127.0.0.1:21", false},
		{"no host", "http://", false},
		{"newline", "http://127.0.0.1:7897\nX: y", false},
		{"shell", "http://127.0.0.1:7897`", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseOutboundProxy(map[string]any{
				"outboundProxyEnabled": true,
				"outboundProxyUrl":     tc.url,
			})
			if !cfg.enabled {
				t.Fatal("enabled lost")
			}
			if got := cfg.url != nil; got != tc.wantURL {
				t.Fatalf("url parsed = %v, want %v (%v)", got, tc.wantURL, cfg.url)
			}
		})
	}

	cfg := parseOutboundProxy(map[string]any{
		"outboundProxyEnabled": true,
		"outboundProxyUrl":     "http://p:1",
		"outboundNoProxy":      " localhost , .example.com,",
	})
	if len(cfg.noProxy) != 2 || cfg.noProxy[0] != "localhost" || cfg.noProxy[1] != ".example.com" {
		t.Fatalf("noProxy = %v", cfg.noProxy)
	}
}

func TestNoProxyMatch(t *testing.T) {
	cases := []struct {
		host string
		list []string
		want bool
	}{
		{"api.example.com", []string{"example.com"}, true},
		{"api.example.com", []string{".example.com"}, true},
		{"api.example.com", []string{"*.example.com"}, true},
		{"example.com", []string{"*.example.com"}, true},
		{"example.com", []string{"notexample.com"}, false},
		{"api.example.com:443", []string{"example.com:443"}, true},
		{"api.example.com", []string{"*"}, true},
		{"api.example.com", nil, false},
		{"", []string{"*"}, false},
	}
	for _, tc := range cases {
		if got := noProxyMatch(tc.host, tc.list); got != tc.want {
			t.Errorf("noProxyMatch(%q, %v) = %v, want %v", tc.host, tc.list, got, tc.want)
		}
	}
}

// A proxy saved from the dashboard must reach the next upstream request. The
// reference wrote process.env; Go's http.ProxyFromEnvironment caches the
// environment, so the setting would otherwise never apply.
func TestOutboundProxyAppliesFromSettings(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"

	patch := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/settings", strings.NewReader(body))
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rec := httptest.NewRecorder()
		g.handlePatchSettings(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
		}
	}
	upstream := func(path string) *http.Request {
		return httptest.NewRequest(http.MethodPost, path, nil)
	}

	// Disabled by default: no proxy from the setting.
	g.refreshOutboundProxy()
	if u, _ := g.proxyForRequest(upstream("https://api.example.com/v1")); u != nil {
		if u.Host == "127.0.0.1:7897" {
			t.Fatalf("setting applied while disabled: %v", u)
		}
	}

	patch(`{"outboundProxyEnabled":true,"outboundProxyUrl":"http://127.0.0.1:7897"}`)
	u, err := g.proxyForRequest(upstream("https://api.example.com/v1"))
	if err != nil || u == nil || u.Host != "127.0.0.1:7897" {
		t.Fatalf("proxy not applied: %v %v", u, err)
	}

	// NO_PROXY exempts the host without dropping the proxy elsewhere.
	patch(`{"outboundNoProxy":"example.com"}`)
	if u, _ := g.proxyForRequest(upstream("https://api.example.com/v1")); u != nil {
		t.Fatalf("noProxy ignored: %v", u)
	}
	if u, _ := g.proxyForRequest(upstream("https://other.test/v1")); u == nil || u.Host != "127.0.0.1:7897" {
		t.Fatalf("proxy lost for a non-exempt host: %v", u)
	}

	// An unusable URL is inert rather than fatal.
	patch(`{"outboundProxyUrl":"ftp://p:21"}`)
	if cfg, _ := g.outboundProxy.Load().(outboundProxyConfig); cfg.url != nil {
		t.Fatalf("invalid url kept: %v", cfg.url)
	}
}
