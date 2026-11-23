package providerdata

import "testing"

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
