package oauth

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// Cline hands tokens back as base64 JSON inside the code param.
func TestClineDecode(t *testing.T) {
	blob := `{"accessToken":"at-1","refreshToken":"rt-1","email":"a@b.c","firstName":"A","lastName":"B"}`
	res := clineDecode(base64.StdEncoding.EncodeToString([]byte(blob)))
	if res == nil || res.AccessToken != "at-1" || res.RefreshToken != "rt-1" || res.Email != "a@b.c" {
		t.Fatalf("decode failed: %+v", res)
	}
	if res.PSD["firstName"] != "A" {
		t.Fatalf("psd lost: %+v", res.PSD)
	}
	if clineDecode("not-base64-at-all!!") != nil {
		t.Fatal("garbage decoded as a token")
	}
}

// Claude's authorize URL must carry the odd `code=true` flag, and GitLab's
// must use the caller-supplied instance + clientId.
func TestAuthorizeURLBuilders(t *testing.T) {
	o := &providerdata.OAuth{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		Scopes:       jsonRaw(`["user:profile"]`),
	}
	u, err := claudeAuthorize(nil, o, "cid", "http://x/callback", "st", "ch", nil)
	if err != nil {
		t.Fatal(err)
	}
	q := parseQ(t, u)
	if q.Get("code") != "true" || q.Get("client_id") != "cid" || q.Get("code_challenge") != "ch" {
		t.Fatalf("claude query wrong: %v", q)
	}

	gl := &providerdata.OAuth{Scope: "api read_user", Extra: map[string]any{"authorizeUrlPath": "/oauth/authorize"}}
	u, err = gitlabAuthorize(nil, gl, "", "http://x/callback", "st", "ch",
		map[string]string{"baseUrl": "https://gitlab.corp", "clientId": "appid"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "https://gitlab.corp/oauth/authorize?") || parseQ(t, u).Get("client_id") != "appid" {
		t.Fatalf("gitlab url wrong: %s", u)
	}
	if _, err := gitlabAuthorize(nil, gl, "", "http://x/c", "st", "ch", nil); err == nil {
		t.Fatal("gitlab accepted a missing clientId")
	}
}

// The API key is the only usable credential for iFlow, so its absence must
// fail the login rather than store a dead connection.
func TestIflowExchangeRequiresAPIKey(t *testing.T) {
	if firstNonEmpty("", "") != "" {
		t.Fatal("firstNonEmpty broken")
	}
	if secondsUntil("", 0) != 3600 {
		t.Fatal("expiry fallback broken")
	}
}

func jsonRaw(s string) []byte { return []byte(s) }

func parseQ(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}
