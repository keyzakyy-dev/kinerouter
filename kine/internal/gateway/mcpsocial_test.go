package gateway

// Tests for the MCP bridge text compression and the Kiro CLIProxyAPI importer's
// trust boundary (Microsoft-only token endpoint).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPCollapseRepeated(t *testing.T) {
	lines := make([]string, 0, 60)
	lines = append(lines, "root")
	for i := 0; i < 50; i++ {
		lines = append(lines, "  - link: item")
	}
	out := strings.Join(mcpCollapseRepeated(lines), "\n")
	if !strings.Contains(out, "items omitted by kinerouter bridge") {
		t.Fatalf("no collapse marker: %s", out)
	}
	if n := strings.Count(out, "  - link: item"); n != mcpCollapseKeepHead+mcpCollapseKeepTail {
		t.Fatalf("kept %d siblings, want %d", n, mcpCollapseKeepHead+mcpCollapseKeepTail)
	}
	// A short run must survive untouched.
	if got := mcpCollapseRepeated([]string{"  - link: a", "  - link: b"}); len(got) != 2 {
		t.Fatalf("short run changed: %v", got)
	}
}

func TestMCPFilterFrame(t *testing.T) {
	big := strings.Repeat("  - link: x\n", 4000)
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []any{map[string]any{"type": "text", "text": big}}}}
	raw, _ := json.Marshal(msg)
	out := mcpFilterFrame(string(raw))
	if len(out) >= len(raw) {
		t.Fatalf("frame not compressed: %d -> %d", len(raw), len(out))
	}
	// A non-content frame passes through byte-identical.
	plain := `{"jsonrpc":"2.0","id":2,"result":{"ok":true}}`
	if mcpFilterFrame(plain) != plain {
		t.Fatal("plain frame mutated")
	}
}

func TestKiroCLIProxyRejectsForeignEndpoint(t *testing.T) {
	base := map[string]any{
		"access_token": "a", "refresh_token": "r", "client_id": "c",
		"profile_arn": "arn:p", "scopes": "s",
	}
	for _, endpoint := range []string{
		"https://evil.example.com/token", "http://login.microsoftonline.com/token",
	} {
		in := map[string]any{}
		for k, v := range base {
			in[k] = v
		}
		in["token_endpoint"] = endpoint
		if _, err := normalizeKiroExternalIdpAuth(in); err == nil {
			t.Fatalf("%s must be rejected", endpoint)
		}
	}
	in := map[string]any{}
	for k, v := range base {
		in[k] = v
	}
	in["token_endpoint"] = "https://login.microsoftonline.com/x/oauth2/v2.0/token"
	c, err := normalizeKiroExternalIdpAuth(in)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "kiro" || c.Data["refreshToken"] != "r" {
		t.Fatalf("bad connection: %+v", c)
	}
	psd, _ := c.Data["providerSpecificData"].(map[string]any)
	if psd["authMethod"] != "external_idp" || psd["region"] != "us-east-1" {
		t.Fatalf("bad psd: %v", psd)
	}
}

// next.config.mjs rewrites /v1beta/* to /api/v1beta/*, so both must route.
func TestV1BetaAliasRoutes(t *testing.T) {
	mux := http.NewServeMux()
	(&Gateway{}).registerGeminiRoutes(mux)
	for _, p := range []string{"/v1beta/models", "/api/v1beta/models"} {
		req := httptest.NewRequest("GET", p, nil)
		if _, pattern := mux.Handler(req); pattern == "" {
			t.Fatalf("%s not routed", p)
		}
	}
	req := httptest.NewRequest("POST", "/api/v1beta/models/x:generateContent", nil)
	if _, pattern := mux.Handler(req); !strings.Contains(pattern, "path") {
		t.Fatalf("generateContent not routed: %q", pattern)
	}
}

func TestKiroSocialAuthorize(t *testing.T) {
	g := testGateway(t)
	rec := httptest.NewRecorder()
	g.kiroSocialAuthorize(rec, httptest.NewRequest("GET", "/api/oauth/kiro/social-authorize?provider=evil", nil))
	if rec.Code != 400 {
		t.Fatalf("bad provider should 400, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	g.kiroSocialAuthorize(rec, httptest.NewRequest("GET", "/api/oauth/kiro/social-authorize?provider=google", nil))
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "idp=Google") || !strings.Contains(body, "code_challenge_method=S256") {
		t.Fatalf("authorize: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "kiro.kiroAgent/authenticate-success&") {
		// redirect_uri must be query-escaped, not raw.
		t.Fatal("redirect_uri not escaped")
	}
}
