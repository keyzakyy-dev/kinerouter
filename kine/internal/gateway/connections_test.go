package gateway

import (
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
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

// A provider may declare a providerSpecificData field in place of the API key
// (bedrock's `profile`); the reference accepts the connection in that case.
func TestAPIKeyOptionalSubstitute(t *testing.T) {
	entry := &providerdata.Entry{ID: "bedrock", APIKeyOptionalWith: "profile"}
	if !apiKeyOptional(entry, map[string]any{"profile": "default"}) {
		t.Fatal("profile should stand in for the key")
	}
	if apiKeyOptional(entry, map[string]any{"profile": "  "}) {
		t.Fatal("blank profile must not count")
	}
	if apiKeyOptional(entry, nil) {
		t.Fatal("missing providerSpecificData must not count")
	}
	if apiKeyOptional(&providerdata.Entry{ID: "openai"}, map[string]any{"profile": "x"}) {
		t.Fatal("provider without apiKeyOptionalWith must require a key")
	}
	if apiKeyOptional(nil, map[string]any{"profile": "x"}) {
		t.Fatal("nil entry must require a key")
	}
}

// The per-connection NO_PROXY field is part of the same proxy block the reference
// stores (connectionProxyEnabled/Url/NoProxy).
func TestPickSpecificKeepsConnectionNoProxy(t *testing.T) {
	got := pickSpecific(map[string]any{"connectionNoProxy": "localhost,.corp"})
	if got["connectionNoProxy"] != "localhost,.corp" {
		t.Fatalf("connectionNoProxy dropped: %#v", got)
	}
}
