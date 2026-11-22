package oauth

import (
	"encoding/json"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// The registry carries the device-code endpoints for the RFC 8628 providers.
func TestDeviceProvidersPresent(t *testing.T) {
	m := NewManager()
	list := m.DeviceProviders()
	want := map[string]bool{"grok-cli": true, "kimi": true, "muse": true, "nous": true, "github": true,
		"minimax-code": true, "minimax-code-global": true}
	for _, id := range list {
		delete(want, id)
	}
	for id := range want {
		t.Fatalf("device provider %q missing from registry listings (%v)", id, list)
	}
	for _, id := range list {
		if m.reg.OAuthFor(id) == nil {
			t.Fatalf("%s advertised but OAuthFor is nil", id)
		}
	}
}

// A device poll with an unknown state must fail, never mint a connection.
func TestDevicePollUnknownState(t *testing.T) {
	if _, err := NewManager().DevicePoll(t.Context(), nil, "nope"); err == nil {
		t.Fatal("unknown device state accepted")
	}
}

// scopeString resolves both the string and array registry shapes.
func TestScopeString(t *testing.T) {
	var arr providerdata.OAuth
	json.Unmarshal([]byte(`{"scopes":["a","b"]}`), &arr)
	if got := scopeString(&arr); got != "a b" {
		t.Fatalf("array scope: %q", got)
	}
	var str providerdata.OAuth
	json.Unmarshal([]byte(`{"scope":"x y"}`), &str)
	if got := scopeString(&str); got != "x y" {
		t.Fatalf("string scope: %q", got)
	}
}
