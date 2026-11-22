package gateway

// Kiro social login (Google/GitHub via AWS Cognito) and the CLIProxyAPI
// external_idp importer. Ported from src/app/api/oauth/kiro/{social-authorize,
// social-exchange,import-cli-proxy}/route.js.
//
// Security:
//   - codeVerifier is generated server-side and returned to the caller, exactly
//     as the JS route does (the dashboard holds it until the callback); it is
//     never stored.
//   - tokenEndpoint in a CLIProxyAPI blob is pinned to a Microsoft login host
//     over https, so an imported file cannot redirect the refresh grant.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const kiroAuthService = "https://prod.us-east-1.auth.desktop.kiro.dev"
const kiroSocialRedirect = "kiro://kiro.kiroAgent/authenticate-success"

func (g *Gateway) registerKiroSocialRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/oauth/kiro/social-authorize", g.requireAuth(g.kiroSocialAuthorize))
	mux.HandleFunc("POST /api/oauth/kiro/social-exchange", g.requireAuth(g.kiroSocialExchange))
	mux.HandleFunc("POST /api/oauth/kiro/import-cli-proxy", g.requireAuth(g.importKiroCLIProxy))
}

func kiroSocialIDP(provider string) (string, bool) {
	switch provider {
	case "google":
		return "Google", true
	case "github":
		return "Github", true
	}
	return "", false
}

// kiroSocialAuthorize mints PKCE + state and returns the Cognito login URL. The
// kiro:// redirect is the only one Cognito whitelists, so localhost cannot be
// used here.
func (g *Gateway) kiroSocialAuthorize(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	idp, ok := kiroSocialIDP(provider)
	if !ok {
		writeErr(w, http.StatusBadRequest, "Invalid provider. Use 'google' or 'github'")
		return
	}
	verifier, challenge := pkcePair()
	state := randomToken(16)
	q := url.Values{
		"idp": {idp}, "redirect_uri": {kiroSocialRedirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"state": {state}, "prompt": {"select_account"},
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authUrl": kiroAuthService + "/login?" + q.Encode(),
		"state":   state, "codeVerifier": verifier, "codeChallenge": challenge,
		"provider": provider,
	})
}

func (g *Gateway) kiroSocialExchange(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"codeVerifier"`
		Provider     string `json:"provider"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	body.Code = strings.TrimSpace(body.Code)
	body.CodeVerifier = strings.TrimSpace(body.CodeVerifier)
	if body.Code == "" || body.CodeVerifier == "" {
		writeErr(w, http.StatusBadRequest, "Missing required fields")
		return
	}
	if _, ok := kiroSocialIDP(body.Provider); !ok {
		writeErr(w, http.StatusBadRequest, "Invalid provider")
		return
	}

	payload, _ := json.Marshal(map[string]any{
		"code": body.Code, "code_verifier": body.CodeVerifier, "redirect_uri": kiroSocialRedirect,
	})
	tok, err := postJSONBody(r.Context(), kiroAuthService+"/oauth/token", payload, nil)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "Token exchange failed: "+err.Error())
		return
	}
	access := strings.TrimSpace(strAny(tok["accessToken"]))
	if access == "" {
		writeErr(w, http.StatusBadGateway, "Token exchange returned no access token")
		return
	}
	expiresIn := num(tok["expiresIn"])
	if expiresIn <= 0 {
		expiresIn = 3600
	}

	conn := newConn("kiro", "oauth")
	conn.Email = jwtEmail(access)
	conn.Data["accessToken"] = access
	conn.Data["refreshToken"] = strAny(tok["refreshToken"])
	conn.Data["expiresAt"] = time.Now().Add(time.Duration(expiresIn) * time.Second).UTC().Format(time.RFC3339)
	conn.Data["providerSpecificData"] = map[string]any{
		"profileArn": strAny(tok["profileArn"]),
		"authMethod": body.Provider,
		"provider":   strings.ToUpper(body.Provider[:1]) + body.Provider[1:],
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true,
			"connection": map[string]any{"id": conn.ID, "provider": conn.Provider, "email": conn.Email}})
	}
}

// ------------------------------------------------------------- CLIProxyAPI

var microsoftTokenHosts = map[string]bool{
	"login.microsoftonline.com": true,
	"login.microsoft.com":       true,
	"login.windows.net":         true,
}

// validateMsTokenEndpoint rejects anything that is not an https Microsoft login
// URL, so an imported blob cannot point the refresh grant at an arbitrary host.
func validateMsTokenEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("token_endpoint is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("token_endpoint must be a valid URL")
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("token_endpoint must use https")
	}
	if !microsoftTokenHosts[strings.ToLower(u.Hostname())] {
		return "", fmt.Errorf("token_endpoint must be a Microsoft login endpoint")
	}
	return u.String(), nil
}

// normalizeKiroExternalIdpAuth parses a CLIProxyAPI Kiro auth blob into a
// connection. Mirrors normalizeKiroExternalIdpAuth in kiroExternalIdp.js.
func normalizeKiroExternalIdpAuth(raw any) (*store.Connection, error) {
	input, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("CLIProxyAPI auth JSON is required")
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	if m := str("auth_method", "authMethod"); m != "" && m != "external_idp" {
		return nil, fmt.Errorf("Only external_idp Kiro auth is supported by this importer")
	}
	access := str("access_token", "accessToken")
	refresh := str("refresh_token", "refreshToken")
	clientID := str("client_id", "clientId")
	endpoint, err := validateMsTokenEndpoint(str("token_endpoint", "tokenEndpoint"))
	if err != nil {
		return nil, err
	}
	profileArn := str("profile_arn", "profileArn")
	region := firstNonEmpty(str("region"), "us-east-1")
	if !awsRegionRe.MatchString(region) {
		return nil, fmt.Errorf("Invalid region")
	}
	var scope string
	switch s := input["scopes"].(type) {
	case []any:
		parts := make([]string, 0, len(s))
		for _, v := range s {
			if piece, ok := v.(string); ok && strings.TrimSpace(piece) != "" {
				parts = append(parts, strings.TrimSpace(piece))
			}
		}
		scope = strings.Join(parts, " ")
	case string:
		scope = strings.TrimSpace(s)
	}
	if scope == "" {
		scope = str("scope")
	}
	switch {
	case access == "":
		return nil, fmt.Errorf("access_token is required")
	case refresh == "":
		return nil, fmt.Errorf("refresh_token is required")
	case clientID == "":
		return nil, fmt.Errorf("client_id is required")
	case scope == "":
		return nil, fmt.Errorf("scopes is required")
	case profileArn == "":
		return nil, fmt.Errorf("profile_arn is required")
	}

	// resolveExpiresAt order: explicit timestamp, then expires_in, then the JWT
	// exp claim, then a one-hour default.
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	secs := num(input["expires_in"])
	if secs == 0 {
		secs = num(input["expiresIn"])
	}
	if v := str("expired", "expires_at", "expiresAt"); v != "" {
		expiresAt = v
	} else if secs > 0 {
		expiresAt = time.Now().Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339)
	} else if exp := num(jwtPayload(access)["exp"]); exp > 0 {
		expiresAt = time.Unix(exp, 0).UTC().Format(time.RFC3339)
	}

	conn := newConn("kiro", "oauth")
	conn.Email = firstNonEmpty(str("email"), jwtEmail(access))
	conn.Data["accessToken"] = access
	conn.Data["refreshToken"] = refresh
	conn.Data["expiresAt"] = expiresAt
	conn.Data["providerSpecificData"] = map[string]any{
		"profileArn": profileArn, "region": region, "authMethod": "external_idp",
		"provider": "CLIProxyAPI", "clientId": clientID, "tokenEndpoint": endpoint, "scope": scope,
	}
	return conn, nil
}

func (g *Gateway) importKiroCLIProxy(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "CLIProxyAPI import failed")
		return
	}
	raw := any(body)
	for _, k := range []string{"cliProxyAuth", "auth", "json"} {
		if v, ok := body[k]; ok && v != nil {
			if s, ok := v.(string); ok {
				var parsed any
				if json.Unmarshal([]byte(s), &parsed) != nil {
					writeErr(w, http.StatusBadRequest, "CLIProxyAPI auth JSON is invalid")
					return
				}
				raw = parsed
			} else {
				raw = v
			}
			break
		}
	}
	conn, err := normalizeKiroExternalIdpAuth(raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if saveConn(w, g.store, conn) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true,
			"connection": map[string]any{"id": conn.ID, "provider": conn.Provider, "email": conn.Email}})
	}
}
