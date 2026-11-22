package gateway

import "testing"

// trust-cert is server-wide and must pass without a tool id; the DNS actions
// need one. Guards the regression where the server-card "Trust cert" button
// (which sends tool:"") was rejected with 400.
func TestValidateMitmPatch(t *testing.T) {
	cases := []struct{ tool, action, want string }{
		{"", "trust-cert", ""},
		{"", "enable", "tool required"},
		{"", "disable", "tool required"},
		{"antigravity", "enable", ""},
		{"kiro", "disable", ""},
		{"", "", "action required"},
	}
	for _, c := range cases {
		if got := validateMitmPatch(c.tool, c.action); got != c.want {
			t.Errorf("validateMitmPatch(%q,%q) = %q, want %q", c.tool, c.action, got, c.want)
		}
	}
}
