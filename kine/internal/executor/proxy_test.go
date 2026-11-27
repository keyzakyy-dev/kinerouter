package executor

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// A connection's connectionNoProxy must reach the per-account transport. A
// bare http.ProxyURL would tunnel localhost and internal suffixes even when the
// operator explicitly excluded them in the provider detail page.
func TestProxyFuncWithNoProxy(t *testing.T) {
	u, err := url.Parse("http://proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	fn := proxyFuncWithNoProxy(u, "localhost, .internal.example, *.corp.example")

	cases := []struct {
		target  string
		bypass  bool
	}{
		{"http://localhost:1234/v1", true},
		{"http://api.internal.example/v1", true},
		{"http://x.corp.example/v1", true},
		{"http://public.example/v1", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("POST", tc.target, nil)
		got, err := fn(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.target, err)
		}
		if tc.bypass && got != nil {
			t.Fatalf("%s should bypass the proxy, got %v", tc.target, got)
		}
		if !tc.bypass && (got == nil || got.Host != "proxy.example:8080") {
			t.Fatalf("%s should use the proxy, got %v", tc.target, got)
		}
	}
}

func TestCredentialsFromConnectionCarriesNoProxy(t *testing.T) {
	c := &store.Connection{
		Data: map[string]any{
			"providerSpecificData": map[string]any{
				"connectionProxyEnabled": true,
				"connectionProxyUrl":     "http://proxy.example:8080",
				"connectionNoProxy":      "localhost,.internal.example",
			},
		},
	}
	creds := CredentialsFromConnection(c)
	if creds.ProxyURL != "http://proxy.example:8080" {
		t.Fatalf("proxy url: %q", creds.ProxyURL)
	}
	if creds.ProxyNoProxy != "localhost,.internal.example" {
		t.Fatalf("no_proxy: %q", creds.ProxyNoProxy)
	}
}
