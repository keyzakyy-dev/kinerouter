package gateway

import "testing"

// A saved host override must not double the path it already contains.
func TestSystemoneURL(t *testing.T) {
	cfg := map[string]any{"baseUrl": "https://api.example.com/v1/systemone", "path": "/v1/systemone"}
	cases := map[string]string{
		"":                                 "https://api.example.com/v1/systemone",
		"https://host.tld":                 "https://host.tld/v1/systemone",
		"https://host.tld/":                "https://host.tld/v1/systemone",
		"https://host.tld/v1":              "https://host.tld/v1/systemone",
		"https://host.tld/v1/systemone":    "https://host.tld/v1/systemone",
		"https://host.tld/v1/systemone///": "https://host.tld/v1/systemone",
	}
	for in, want := range cases {
		if got := systemoneURL(in, cfg); got != want {
			t.Fatalf("systemoneURL(%q) = %q, want %q", in, got, want)
		}
	}
	// No path in the config: the override is the full URL.
	if got := systemoneURL("https://h/x", map[string]any{"baseUrl": "b"}); got != "https://h/x" {
		t.Fatalf("override without path: %q", got)
	}
}
