package gateway

import (
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func connectionStub() *store.Connection {
	return &store.Connection{
		ID: "c1", Provider: "synthetic", AuthType: "apikey", Name: "acct", IsActive: true,
		Data: map[string]any{
			"apiKey": "sk-secret", "accessToken": "at", "refreshToken": "rt", "idToken": "it",
			"providerSpecificData": map[string]any{"nodeName": "n"},
		},
	}
}

// A client-supplied baseUrl/providerSpecificData must never reach the stored
// connection (SSRF), and credentials must never come back in a response.
func TestPickSpecificDropsUnknownKeys(t *testing.T) {
	got := pickSpecific(map[string]any{
		"baseUrl":      "http://169.254.169.254/latest/meta-data",
		"nodeName":     "x",
		"defaultModel": "m",
	})
	if _, ok := got["baseUrl"]; ok {
		t.Fatal("baseUrl was kept — SSRF vector")
	}
	if got["nodeName"] != "x" || got["defaultModel"] != "m" {
		t.Fatalf("allowed keys dropped: %#v", got)
	}
}

func TestSanitizeConnStripsCredentials(t *testing.T) {
	c := connectionStub()
	out := sanitizeConn(c)
	for _, k := range []string{"apiKey", "accessToken", "refreshToken", "idToken"} {
		if _, ok := out[k]; ok {
			t.Fatalf("%s leaked in response", k)
		}
	}
	if out["name"] != "acct" {
		t.Fatalf("name lost: %v", out["name"])
	}
}

func TestCheckProxyRejectsInvalidURL(t *testing.T) {
	msg, ok := checkProxy(map[string]any{"connectionProxyEnabled": true, "connectionProxyUrl": ""})
	if ok || !strings.Contains(msg, "required") {
		t.Fatalf("enabled proxy with empty url should fail, got %q %v", msg, ok)
	}
}
