package providerdata

import (
	"strings"
	"testing"
)

// A chat transport must point at an endpoint, not a bare host: the executor
// treats any baseUrl with a scheme as the full URL, so "https://opencode.ai"
// made every OpenCode Free request POST the site root and fail with 405.
func TestChatTransportsHaveEndpointPaths(t *testing.T) {
	r := Get()
	for _, id := range []string{"opencode", "opencode-zen", "opencode-go"} {
		tf := r.TransportFor(id)
		if tf == nil {
			t.Fatalf("%s has no transport", id)
		}
		if !strings.Contains(strings.TrimPrefix(tf.BaseURL, "https://"), "/") {
			t.Errorf("%s baseUrl %q has no endpoint path", id, tf.BaseURL)
		}
	}
}

// A registry effort marker is request metadata, never part of the wire id.
func TestUpstreamModelIDStripsEffortSuffix(t *testing.T) {
	r := Get()
	for model, want := range map[string]string{
		"gemini-4-carbon-high": "gemini-4-carbon-high",
		"gemini-3.8-flash":     "gemini-3.8-flash-medium",
		"gemini-3.7-flash-low": "gemini-3.7-flash-tiered",
	} {
		if got := r.UpstreamModelID("antigravity", model); got != want {
			t.Errorf("UpstreamModelID(ag, %q) = %q, want %q", model, got, want)
		}
	}
}
