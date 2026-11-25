package gateway

// OIDC single sign-on. Ported from src/lib/auth/oidc.js,
// src/app/api/auth/oidc/{start,callback,test}/route.js and the SSO branches of
// src/app/api/auth/login/route.js.
//
// Security notes:
//   - the issuer URL comes only from settings (admin-written) or, for the
//     authenticated test route, from the request body; no unauthenticated
//     caller can point discovery at an arbitrary host
//   - id_token is verified against the provider JWKS with issuer + audience,
//     and the nonce cookie is compared before any session is issued
//   - X-Forwarded-Proto/Host are honoured only from a loopback peer (the same
//     rule withPeerTrust applies to X-Forwarded-For), so a remote client
//     cannot steer the redirect_uri that gets handed to the IdP
//   - session claims are signed via auth.SignClaims, whose reserved keys
//     cannot be overwritten by caller-supplied values

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/keyzakyy-dev/kinerouter/internal/auth"
	"github.com/keyzakyy-dev/kinerouter/internal/netguard"
	"golang.org/x/oauth2"
)

const (
	oidcDefaultScopes     = "openid profile email"
	oidcDefaultLoginLabel = "Sign in with OIDC"
	oidcStateTTL          = 10 * 60
	oidcCookieState       = "oidc_state"
	oidcCookieNonce       = "oidc_nonce"
	oidcCookieVerifier    = "oidc_code_verifier"
)

type oidcConfig struct {
	issuerURL    string
	clientID     string
	clientSecret string
	scopes       string
	loginLabel   string
}

func trimSlashes(s string) string { return strings.TrimRight(strings.TrimSpace(s), "/") }

func (g *Gateway) oidcSettings() (map[string]any, oidcConfig) {
	settings, _ := g.store.GetSettings()
	scopes := strings.TrimSpace(str(settings, "oidcScopes"))
	if scopes == "" {
		scopes = oidcDefaultScopes
	}
	label := strings.TrimSpace(str(settings, "oidcLoginLabel"))
	if label == "" {
		label = oidcDefaultLoginLabel
	}
	return settings, oidcConfig{
		issuerURL:    trimSlashes(str(settings, "oidcIssuerUrl")),
		clientID:     strings.TrimSpace(str(settings, "oidcClientId")),
		clientSecret: strings.TrimSpace(str(settings, "oidcClientSecret")),
		scopes:       scopes,
		loginLabel:   label,
	}
}

func isOIDCConfigured(c oidcConfig) bool {
	return c.issuerURL != "" && c.clientID != "" && c.clientSecret != ""
}

// oidcRuntime mirrors getOidcRuntimeConfig: nil unless authMode enables OIDC
// and all three credentials are set.
func (g *Gateway) oidcRuntime() *oidcConfig {
	settings, c := g.oidcSettings()
	mode := str(settings, "authMode")
	if mode != "oidc" && mode != "both" {
		return nil
	}
	if !isOIDCConfigured(c) {
		return nil
	}
	return &c
}

// publicOrigin mirrors getPublicOrigin / getSamlBaseUrl. A configured
// BASE_URL (or the SAML setting's own baseUrl) wins; otherwise forwarding
// headers are trusted only from a loopback peer.
func (g *Gateway) publicOrigin(r *http.Request, settings map[string]any, saml bool) string {
	if saml {
		if v := trimSlashes(str(settings, "baseUrl")); v != "" {
			return v
		}
	}
	if v := trimSlashes(g.cfg.BaseURL); v != "" {
		return v
	}
	if isLoopback(clientHost(r)) {
		proto := r.Header.Get("X-Forwarded-Proto")
		host := r.Header.Get("X-Forwarded-Host")
		if host != "" {
			if proto == "" {
				proto = "http"
			}
			return proto + "://" + trimSlashes(host)
		}
	}
	if r.Host != "" {
		proto := "http"
		if r.TLS != nil {
			proto = "https"
		}
		return proto + "://" + r.Host
	}
	return "http://localhost:" + itoa(int64(g.gatewayPort()))
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkcePair() (verifier, challenge string) {
	verifier = randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (g *Gateway) secureCookie(r *http.Request) bool {
	return g.cfg.AuthCookieSecure || r.Header.Get("X-Forwarded-Proto") == "https"
}

func setShortCookie(w http.ResponseWriter, r *http.Request, name, value string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: oidcStateTTL,
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func loginRedirect(w http.ResponseWriter, r *http.Request, origin, errCode string) {
	http.Redirect(w, r, origin+"/login?error="+url.QueryEscape(errCode), http.StatusFound)
}

// oidcHTTPClient is the client for discovery and the token endpoint. Both URLs
// originate from a request body or from settings, so the dial itself refuses
// private, loopback and link-local addresses — CheckURL alone resolves once and
// a rebinding answer would get through on the second lookup. An on-prem IdP on a
// private network is the one legitimate case; OIDC_ALLOW_PRIVATE=1 restores the
// unguarded client for it.
func oidcHTTPClient() *http.Client {
	if os.Getenv("OIDC_ALLOW_PRIVATE") != "" {
		return &http.Client{Timeout: 20 * time.Second}
	}
	return netguard.Client(20 * time.Second)
}

// ---------------------------------------------------------------- start

func (g *Gateway) handleOIDCStart(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.oidcSettings()
	origin := g.publicOrigin(r, settings, false)
	cfg := g.oidcRuntime()
	if cfg == nil {
		loginRedirect(w, r, origin, "oidc_not_configured")
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, oidcHTTPClient())
	provider, err := oidc.NewProvider(ctx, cfg.issuerURL)
	if err != nil {
		loginRedirect(w, r, origin, err.Error())
		return
	}
	state := randomToken(16)
	nonce := randomToken(16)
	verifier, challenge := pkcePair()
	conf := oauth2.Config{
		ClientID:     cfg.clientID,
		ClientSecret: cfg.clientSecret,
		RedirectURL:  origin + "/api/auth/oidc/callback",
		Endpoint:     provider.Endpoint(),
		Scopes:       strings.Fields(cfg.scopes),
	}
	authURL := conf.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	secure := g.secureCookie(r)
	setShortCookie(w, r, oidcCookieState, state, secure)
	setShortCookie(w, r, oidcCookieNonce, nonce, secure)
	setShortCookie(w, r, oidcCookieVerifier, verifier, secure)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// ---------------------------------------------------------------- callback

func (g *Gateway) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	settings, _ := g.oidcSettings()
	origin := g.publicOrigin(r, settings, false)
	q := r.URL.Query()
	clearAll := func() {
		clearCookie(w, oidcCookieState)
		clearCookie(w, oidcCookieNonce)
		clearCookie(w, oidcCookieVerifier)
	}
	if e := q.Get("error"); e != "" {
		clearAll()
		loginRedirect(w, r, origin, e)
		return
	}
	code, state := q.Get("code"), q.Get("state")
	if code == "" || state == "" {
		clearAll()
		loginRedirect(w, r, origin, "oidc_missing_code")
		return
	}
	storedState := cookieValue(r, oidcCookieState)
	storedNonce := cookieValue(r, oidcCookieNonce)
	codeVerifier := cookieValue(r, oidcCookieVerifier)
	if storedState == "" || storedNonce == "" || codeVerifier == "" || storedState != state {
		clearAll()
		loginRedirect(w, r, origin, "oidc_invalid_state")
		return
	}
	cfg := g.oidcRuntime()
	if cfg == nil {
		clearAll()
		loginRedirect(w, r, origin, "oidc_not_configured")
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, oidcHTTPClient())
	provider, err := oidc.NewProvider(ctx, cfg.issuerURL)
	if err != nil {
		clearAll()
		loginRedirect(w, r, origin, err.Error())
		return
	}
	conf := oauth2.Config{
		ClientID:     cfg.clientID,
		ClientSecret: cfg.clientSecret,
		RedirectURL:  origin + "/api/auth/oidc/callback",
		Endpoint:     provider.Endpoint(),
	}
	tok, err := conf.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		clearAll()
		loginRedirect(w, r, origin, err.Error())
		return
	}
	rawIDToken, _ := tok.Extra("id_token").(string)
	if rawIDToken == "" {
		clearAll()
		loginRedirect(w, r, origin, "OIDC provider did not return an id_token")
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: cfg.clientID}).Verify(ctx, rawIDToken)
	if err != nil {
		clearAll()
		loginRedirect(w, r, origin, err.Error())
		return
	}
	if idToken.Nonce != storedNonce {
		clearAll()
		loginRedirect(w, r, origin, "nonce mismatch")
		return
	}
	var claims map[string]any
	_ = idToken.Claims(&claims)
	clearAll()
	token, err := auth.SignClaims(g.cfg.JWTSecret, 24*time.Hour, map[string]any{
		"oidc":      true,
		"oidcSub":   nulIfEmpty(idToken.Subject),
		"oidcEmail": nulIfEmpty(pickOIDCEmail(claims)),
		"oidcName":  pickOIDCDisplayName(claims),
	})
	if err != nil {
		loginRedirect(w, r, origin, "failed to sign session")
		return
	}
	auth.SetSessionCookie(w, r, token, g.cfg.AuthCookieSecure)
	http.Redirect(w, r, origin+"/dashboard", http.StatusFound)
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

func nulIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func pickOIDCDisplayName(claims map[string]any) string {
	for _, k := range []string{"preferred_username", "email", "name", "given_name", "sub"} {
		if v := str(claims, k); v != "" {
			return v
		}
	}
	return "OIDC user"
}

func pickOIDCEmail(claims map[string]any) string { return str(claims, "email") }

// ---------------------------------------------------------------- test

func (g *Gateway) canAccessAuthTest(w http.ResponseWriter, r *http.Request) bool {
	if !g.store.SettingBool("requireLogin", true) {
		return true
	}
	if _, ok := auth.ParseClaims(g.cfg.JWTSecret, auth.SessionToken(r)); ok {
		return true
	}
	if tok := r.Header.Get("x-kine-cli-token"); tok != "" && auth.ValidateCLIToken(tok, g.cliMachineID) {
		return true
	}
	return false
}

type oidcDiscovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSUri               string `json:"jwks_uri"`
}

func fetchOIDCDiscovery(ctx context.Context, issuerURL string) (*oidcDiscovery, error) {
	u := trimSlashes(issuerURL) + "/.well-known/openid-configuration"
	// The issuer comes from the request body or settings; refuse internal hosts
	// so this cannot be pointed at metadata or a loopback service.
	if err := netguard.CheckURL(ctx, u); err != nil {
		return nil, errors.New("Issuer URL is not reachable: it must be a public http(s) address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := oidcHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New("Failed to load OIDC discovery document from " + u)
	}
	var d oidcDiscovery
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

type secretProbe struct {
	tested  bool
	valid   *bool
	message string
	raw     any
}

func probeOIDCClientSecret(ctx context.Context, tokenEndpoint, clientID, clientSecret, redirectURI string) secretProbe {
	if clientSecret == "" {
		return secretProbe{message: "No client secret was provided, so secret validation was skipped."}
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {"__oidc_test_invalid_code__"},
		"redirect_uri":  {redirectURI},
		"code_verifier": {"__oidc_test_invalid_verifier__"},
	}
	// tokenEndpoint comes from the issuer's discovery document, so it is only as
	// trustworthy as a URL from the request body. Refuse internal hosts.
	if err := netguard.CheckURL(ctx, tokenEndpoint); err != nil {
		return secretProbe{tested: true, message: "Token endpoint is not a public http(s) address."}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return secretProbe{tested: true, message: err.Error()}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := oidcHTTPClient().Do(req)
	if err != nil {
		return secretProbe{tested: true, message: err.Error()}
	}
	defer res.Body.Close()
	var data map[string]any
	_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&data)
	errCode := strings.ToLower(str(data, "error"))
	errDesc := str(data, "error_description")
	if errDesc == "" {
		errDesc = errCode
	}
	ok := res.StatusCode >= 200 && res.StatusCode < 300
	yes, no := true, false
	lowerDesc := strings.ToLower(errDesc)
	switch {
	case ok:
		return secretProbe{tested: true, valid: &yes, message: "Client secret was accepted by the token endpoint.", raw: data}
	case errCode == "invalid_client" || errCode == "unauthorized_client" ||
		(strings.Contains(lowerDesc, "client") && (strings.Contains(lowerDesc, "invalid") || strings.Contains(lowerDesc, "failed") || strings.Contains(lowerDesc, "mismatch"))):
		msg := errDesc
		if msg == "" {
			msg = "Client secret is not valid."
		}
		return secretProbe{tested: true, valid: &no, message: msg, raw: data}
	case errCode == "invalid_grant" || errCode == "invalid_code" ||
		strings.Contains(lowerDesc, "grant") || strings.Contains(lowerDesc, "code"):
		return secretProbe{tested: true, valid: &yes, raw: data,
			message: "Client secret was accepted; the token exchange failed only because the test authorization code is invalid."}
	}
	msg := errDesc
	if msg == "" {
		msg = "Token endpoint responded with " + itoa(int64(res.StatusCode))
	}
	return secretProbe{tested: true, message: msg, raw: data}
}

func (g *Gateway) handleOIDCTest(w http.ResponseWriter, r *http.Request) {
	if !g.canAccessAuthTest(w, r) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "Unauthorized"})
		return
	}
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		body = map[string]any{}
	}
	settings, _ := g.oidcSettings()
	pick := func(key string) string {
		if v, ok := body[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return strings.TrimSpace(str(settings, key))
	}
	issuerURL := pick("oidcIssuerUrl")
	if v := strings.TrimSpace(str(body, "issuerUrl")); v != "" {
		issuerURL = v
	}
	clientID := pick("oidcClientId")
	if v := strings.TrimSpace(str(body, "clientId")); v != "" {
		clientID = v
	}
	scopes := strings.TrimSpace(str(body, "scopes"))
	if scopes == "" {
		scopes = strings.TrimSpace(str(settings, "oidcScopes"))
	}
	if scopes == "" {
		scopes = oidcDefaultScopes
	}
	clientSecret := str(settings, "oidcClientSecret")
	if v, ok := body["clientSecret"]; ok {
		clientSecret, _ = v.(string)
	}
	clientSecret = strings.TrimSpace(clientSecret)
	if issuerURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Issuer URL is required"})
		return
	}
	if clientID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Client ID is required"})
		return
	}
	disc, err := fetchOIDCDiscovery(r.Context(), issuerURL)
	if err != nil {
		// Fixed text: a dial/resolve error names the internal host or address the
		// probe reached, which is exactly what an SSRF probe is hunting for.
		log.Printf("oidc test: discovery failed for issuer %q: %v", issuerURL, err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "Could not load the OIDC discovery document for that issuer.",
		})
		return
	}
	redirectURI := g.publicOrigin(r, settings, false) + "/api/auth/oidc/callback"
	probe := probeOIDCClientSecret(r.Context(), disc.TokenEndpoint, clientID, clientSecret, redirectURI)
	payload := map[string]any{
		"discoveryOk":           true,
		"clientSecretTested":    probe.tested,
		"clientSecretValid":     probe.valid,
		"issuerUrl":             issuerURL,
		"clientId":              clientID,
		"scopes":                scopes,
		"redirectUri":           redirectURI,
		"authorizationEndpoint": disc.AuthorizationEndpoint,
		"tokenEndpoint":         disc.TokenEndpoint,
		"jwksUri":               disc.JWKSUri,
	}
	if probe.tested && probe.valid != nil && !*probe.valid {
		payload["ok"] = false
		payload["error"] = "Discovery loaded, but the client secret is not valid: " + probe.message
		writeJSON(w, http.StatusOK, payload)
		return
	}
	payload["ok"] = true
	payload["message"] = probe.message
	writeJSON(w, http.StatusOK, payload)
}

// ssoPasswordLoginBlocked reports whether password auth must be refused
// because the dashboard is in an SSO mode whose provider is configured.
func (g *Gateway) ssoPasswordLoginBlocked() string {
	settings, cfg := g.oidcSettings()
	switch str(settings, "authMode") {
	case "sso", "saml", "oidc":
	default:
		return ""
	}
	ssoType := str(settings, "ssoType")
	if ssoType == "" {
		if str(settings, "authMode") == "saml" {
			ssoType = "saml"
		} else {
			ssoType = "oidc"
		}
	}
	if ssoType == "saml" && isSAMLConfigured(settings) {
		return "Password login is disabled. Use SAML SSO sign in."
	}
	if ssoType == "oidc" && isOIDCConfigured(cfg) {
		return "Password login is disabled. Use OIDC sign in."
	}
	return ""
}
