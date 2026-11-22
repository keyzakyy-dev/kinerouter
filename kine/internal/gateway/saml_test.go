package gateway

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
)

// Self-check for the SAML/OIDC helpers: certificate rewrap, origin resolution
// (loopback-only forwarding headers), attribute precedence and the metadata
// document the IdP is handed.
func TestFormatX509Certificate(t *testing.T) {
	raw := "MIIDazCCAlOgAwIBAgIUYz" // short, wrapped by hand below
	pem := formatX509Certificate("-----BEGIN CERTIFICATE-----\n" +
		strings.Join([]string{"AAAABBBBCCCCDDDD", "EEEEFFFFGGGGHHHH"}, "\n") +
		"\n-----END CERTIFICATE-----")
	lines := strings.Split(pem, "\n")
	if lines[0] != "-----BEGIN CERTIFICATE-----" || lines[len(lines)-1] != "-----END CERTIFICATE-----" {
		t.Fatalf("bad PEM markers: %q", pem)
	}
	if lines[1] != "AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHH" {
		t.Fatalf("body not joined at 64 cols: %q", lines[1])
	}
	if got := formatX509Certificate(raw); got != "-----BEGIN CERTIFICATE-----\n"+raw+"\n-----END CERTIFICATE-----" {
		t.Fatalf("unformatted input not rewrapped: %q", got)
	}
	if formatX509Certificate("!!!") != "" {
		t.Fatal("garbage input should produce an empty certificate")
	}
}

func TestPublicOriginTrustsForwardedHeadersFromLoopbackOnly(t *testing.T) {
	g := &Gateway{cfg: &config.Config{}}
	r := httptest.NewRequest("GET", "http://127.0.0.1:20128/api/auth/oidc/start", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "dash.example.com")
	if got := g.publicOrigin(r, map[string]any{}, false); got != "https://dash.example.com" {
		t.Fatalf("loopback origin = %q", got)
	}
	// A remote peer must not steer redirect_uri.
	r.RemoteAddr = "203.0.113.9:1234"
	if got := g.publicOrigin(r, map[string]any{}, false); got != "http://127.0.0.1:20128" {
		t.Fatalf("remote origin followed X-Forwarded-Host: %q", got)
	}
	// BASE_URL wins over everything.
	g.cfg.BaseURL = "https://router.internal/"
	if got := g.publicOrigin(r, map[string]any{}, false); got != "https://router.internal" {
		t.Fatalf("BASE_URL origin = %q", got)
	}
}

func TestPickSamlIdentity(t *testing.T) {
	settings := map[string]any{"samlAttributeEmail": "mail", "samlAttributeName": "displayName"}
	profile := map[string]any{
		"nameID":      "user@corp.example",
		"mail":        "custom@corp.example",
		"displayName": "Custom Name",
		"email":       "ignored@corp.example",
	}
	if got := pickSamlEmail(profile, settings); got != "custom@corp.example" {
		t.Fatalf("custom attribute must win: %q", got)
	}
	if got := pickSamlDisplayName(profile, settings); got != "Custom Name" {
		t.Fatalf("custom name attribute must win: %q", got)
	}
	// No custom attribute configured → common keys, then nameID.
	if got := pickSamlEmail(map[string]any{"nameID": "n@x", "email": "e@x"}, map[string]any{}); got != "e@x" {
		t.Fatalf("email key precedence: %q", got)
	}
	if got := pickSamlEmail(map[string]any{"nameID": "n@x"}, map[string]any{}); got != "n@x" {
		t.Fatalf("nameID fallback: %q", got)
	}
	if got := pickSamlDisplayName(map[string]any{"givenName": "Ada", "sn": "Lovelace"}, map[string]any{}); got != "Ada Lovelace" {
		t.Fatalf("given+surname combo: %q", got)
	}
}

func TestGenerateSamlMetadata(t *testing.T) {
	settings := map[string]any{
		"samlEntryPoint": "https://idp.example.com/sso",
		"samlIssuer":     "urn:kinerouter:sp",
		"samlCert":       "MIIC",
	}
	md, err := generateSamlMetadata("https://router.example.com", settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`EntityDescriptor`,
		`entityID="urn:kinerouter:sp"`,
		`AssertionConsumerService`,
		`https://router.example.com/api/auth/saml/acs`,
		`WantAssertionsSigned="true"`,
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("metadata missing %q:\n%s", want, md)
		}
	}
}

func TestValidateSamlResponseRejectsBadInResponseTo(t *testing.T) {
	settings := map[string]any{"samlEntryPoint": "https://idp.example.com/sso", "samlCert": "MIIC"}
	xmlBody := `<samlp:Response InResponseTo="_wrong"><Issuer>https://idp.example.com</Issuer></samlp:Response>`
	raw := base64.StdEncoding.EncodeToString([]byte(xmlBody))
	_, err := validateSamlResponse(settings, "https://router.example.com", raw, "_expected")
	if err == nil || !strings.Contains(err.Error(), "InResponseTo mismatch") {
		t.Fatalf("expected InResponseTo rejection, got %v", err)
	}
	if _, err := validateSamlResponse(settings, "https://router.example.com", "!!!notbase64", ""); err == nil {
		t.Fatal("garbage base64 should be rejected")
	}
}
