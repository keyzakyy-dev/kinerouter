// Package oauth implements the OAuth flows for the milestone-1 providers:
// Claude (authorization code + PKCE), Codex/ChatGPT (PKCE, fixed port 1455),
// Gemini CLI (authorization code), plus Vertex service-account JWT. It also
// runs the background refresh loop that mirrors
// src/sse/services/backgroundTokenRefresh.js.
package oauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// Registry entry ids this package knows how to drive.
// supported gates the refresh loop. Vertex has no authorize flow (its
// credential is a service-account JSON pasted into the connection), so it is
// refreshed here but never offered as a browser login.
var supported = map[string]bool{
	"claude": true, "codex": true, "gemini-cli": true, "vertex": true,
	// Device-code providers: the refresh grant is the same form/JSON one.
	"grok-cli": true, "kimi": true, "muse": true, "nous": true,
	"minimax-code": true, "minimax-code-global": true, "github": true,
	// Authorization-code providers with refresh support.
	"antigravity": true, "agy-acp": true, "iflow": true, "cline": true, "clinepass": true,
}
var browserFlows = map[string]bool{
	"claude": true, "codex": true, "gemini-cli": true, "antigravity": true, "agy-acp": true,
	"iflow": true, "gitlab": true, "cline": true, "clinepass": true, "kimchi": true,
}

// Session is one in-flight authorization.
type Session struct {
	Provider    string
	State       string
	Verifier    string
	RedirectURI string
	Meta        map[string]string // per-request provider params (gitlab baseUrl/clientId)
	CreatedAt   time.Time
}

// Manager holds pending sessions and the HTTP client used for token calls.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	devices  map[string]*DeviceSession
	http     *http.Client
	reg      *providerdata.Registry
}

func NewManager() *Manager {
	return &Manager{
		sessions: map[string]*Session{},
		devices:  map[string]*DeviceSession{},
		http:     &http.Client{Timeout: 30 * time.Second},
		reg:      providerdata.Get(),
	}
}

// Start creates a PKCE session and returns the upstream authorize URL.
// meta carries provider-specific params (gitlab instance/clientId).
func (m *Manager) Start(provider, redirectURI string, meta map[string]string) (*Session, string, error) {
	o := m.reg.OAuthFor(provider)
	if o == nil {
		return nil, "", fmt.Errorf("provider %q has no OAuth configuration", provider)
	}
	flow := codeFlows[provider]
	if flow != nil && flow.authorize != nil && o.AuthorizeURL == "" && strExtra(o, "webAppUrl") == "" {
		return nil, "", fmt.Errorf("provider %q has no authorize URL", provider)
	}
	verifier, challenge, err := pkce()
	if err != nil {
		return nil, "", err
	}
	state, err := token(16)
	if err != nil {
		return nil, "", err
	}
	sess := &Session{Provider: provider, State: state, Verifier: verifier,
		RedirectURI: redirectURI, Meta: meta, CreatedAt: time.Now()}
	m.mu.Lock()
	m.sessions[state] = sess
	m.mu.Unlock()

	clientID, _ := m.reg.ClientCreds(provider)
	if flow != nil && flow.authorize != nil {
		authURL, err := flow.authorize(m, o, clientID, redirectURI, state, challenge, meta)
		if err != nil {
			return nil, "", err
		}
		return sess, authURL, nil
	}

	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	if scopes := o.ScopeList(); len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	} else if o.Scope != "" {
		q.Set("scope", o.Scope)
	}
	for k, v := range o.ExtraParams {
		if s, ok := v.(string); ok && s != "" {
			q.Set(k, s)
		}
	}
	return sess, o.AuthorizeURL + "?" + q.Encode(), nil
}

// Exchange completes the flow and persists a provider connection.
func (m *Manager) Exchange(ctx context.Context, st *store.Store, state, code string) (*store.Connection, error) {
	m.mu.Lock()
	sess := m.sessions[state]
	delete(m.sessions, state)
	m.mu.Unlock()
	if sess == nil {
		return nil, fmt.Errorf("unknown or expired OAuth state")
	}
	o := m.reg.OAuthFor(sess.Provider)
	if o == nil {
		return nil, fmt.Errorf("provider %q has no OAuth configuration", sess.Provider)
	}
	if flow := codeFlows[sess.Provider]; flow != nil && flow.exchange != nil {
		res, err := flow.exchange(ctx, m, sess.Provider, sess, code)
		if err != nil {
			return nil, err
		}
		return m.saveResult(ctx, st, sess.Provider, res)
	}
	clientID, clientSecret := m.reg.ClientCreds(sess.Provider)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"redirect_uri":  {sess.RedirectURI},
		"code_verifier": {sess.Verifier},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	tok, err := m.postForm(ctx, o.TokenURL, form)
	if err != nil {
		return nil, err
	}
	res := defaultResult(tok)
	if res.Email == "" && o.UserInfoURL != "" {
		res.Email = m.fetchEmail(ctx, o.UserInfoURL, res.AccessToken)
	}
	return m.saveResult(ctx, st, sess.Provider, res)
}

// saveResult persists a code-flow token result.
func (m *Manager) saveResult(ctx context.Context, st *store.Store, provider string, res *tokenResult) (*store.Connection, error) {
	if res.AccessToken == "" {
		return nil, fmt.Errorf("%s token exchange returned no access token", provider)
	}
	if res.PSD == nil {
		res.PSD = map[string]any{}
	}
	conn := &store.Connection{
		Provider: provider,
		AuthType: "oauth",
		Email:    res.Email,
		Name:     res.Name,
		IsActive: true,
		Data: map[string]any{
			"accessToken":          res.AccessToken,
			"refreshToken":         res.RefreshToken,
			"expiresAt":            res.ExpiresAt,
			"providerSpecificData": res.PSD,
		},
	}
	if res.IDToken != "" {
		conn.Data["idToken"] = res.IDToken
	}
	if res.ProjectID != "" {
		conn.Data["projectId"] = res.ProjectID
	}
	if res.RefreshToken == "" {
		delete(conn.Data, "refreshToken")
	}
	if _, ok := res.PSD["apiKey"]; ok {
		conn.Data["apiKey"] = res.PSD["apiKey"]
	}
	return conn, st.UpsertConnection(conn)
}

// persistConnection stores one token response as a provider connection and
// derives the account email when the token carries one.
func (m *Manager) persistConnection(ctx context.Context, st *store.Store, provider string, tok map[string]any) (*store.Connection, error) {
	conn := &store.Connection{
		Provider: provider,
		AuthType: "oauth",
		IsActive: true,
		Data: map[string]any{
			"accessToken":  tok["access_token"],
			"refreshToken": tok["refresh_token"],
			"expiresAt":    expiry(tok),
			"tokenType":    tok["token_type"],
		},
	}
	if name, _ := tok["account"].(map[string]any); name != nil {
		if email, ok := name["email_address"].(string); ok {
			conn.Email = email
		}
	}
	if o := m.reg.OAuthFor(provider); o != nil && o.UserInfoURL != "" && conn.Email == "" {
		if email := m.fetchEmail(ctx, o.UserInfoURL, conn.Str("accessToken")); email != "" {
			conn.Email = email
		}
	}
	return conn, st.UpsertConnection(conn)
}

// fetchEmail asks a userinfo endpoint for the account email (best-effort).
func (m *Manager) fetchEmail(ctx context.Context, endpoint, accessToken string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	data, err := m.raw(req)
	if err != nil {
		return ""
	}
	var u map[string]any
	if json.Unmarshal(data, &u) != nil {
		return ""
	}
	for _, k := range []string{"email", "public_email", "mail"} {
		if s, ok := u[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// RefreshOne refreshes a single connection if it expires within leadMs.
// Returns (refreshed, error). A nil error with refreshed=false means "no work".
func (m *Manager) RefreshOne(ctx context.Context, st *store.Store, conn *store.Connection, lead time.Duration) (bool, error) {
	o := m.reg.OAuthFor(conn.Provider)
	if o == nil || !supported[conn.Provider] {
		return false, nil
	}
	if conn.Provider == "vertex" {
		return m.refreshVertex(ctx, st, conn)
	}
	exp := parseTime(conn.Str("expiresAt"))
	if !exp.IsZero() && time.Until(exp) > lead {
		return false, nil
	}
	refreshToken := conn.Str("refreshToken")
	if refreshToken == "" {
		return false, fmt.Errorf("%s connection %s has no refresh token", conn.Provider, conn.ID)
	}
	tok, err := m.refreshFor(ctx, conn.Provider, o, refreshToken)
	if err != nil {
		return false, err
	}
	patch := map[string]any{
		"accessToken":   tok["access_token"],
		"expiresAt":     expiry(tok),
		"lastRefreshAt": time.Now().UTC().Format(time.RFC3339),
	}
	// Rotating refresh tokens: keep the new one when the server sends it.
	if rt, ok := tok["refresh_token"].(string); ok && rt != "" {
		patch["refreshToken"] = rt
	}
	_, err = st.PatchConnection(conn.ID, patch)
	return err == nil, err
}

func (m *Manager) refreshGrant(ctx context.Context, provider string, o *providerdata.OAuth, refreshToken string) (map[string]any, error) {
	encoding := "form"
	if o.Refresh != nil {
		if e, ok := o.Refresh["encoding"].(string); ok {
			encoding = e
		}
	}
	clientID, clientSecret := m.reg.ClientCreds(provider)
	if encoding == "json" {
		body := map[string]any{
			"grant_type":    "refresh_token",
			"refresh_token": refreshToken,
			"client_id":     clientID,
		}
		if clientSecret != "" {
			body["client_secret"] = clientSecret
		}
		return m.postJSON(ctx, o.TokenURL, body)
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	if o.Refresh != nil {
		if scope, ok := o.Refresh["scope"].(string); ok && scope != "" {
			form.Set("scope", scope)
		}
	}
	return m.postForm(ctx, o.TokenURL, form)
}

// refreshVertex mints a Google service-account JWT access token.
func (m *Manager) refreshVertex(ctx context.Context, st *store.Store, conn *store.Connection) (bool, error) {
	sa := conn.Specific()
	if sa == nil {
		return false, fmt.Errorf("vertex connection has no service account")
	}
	accessToken, expiresAt, err := MintVertexToken(ctx, m.http, sa)
	if err != nil {
		return false, err
	}
	_, err = st.PatchConnection(conn.ID, map[string]any{
		"accessToken": accessToken, "expiresAt": expiresAt.UTC().Format(time.RFC3339),
	})
	return err == nil, err
}

// MintVertexToken exchanges a service-account JSON (client_email + private_key)
// for a cloud-platform access token via a signed JWT assertion. Shared with the
// executor, which mints per request when the connection stores the SA JSON as
// its api key instead of a stored access token.
func MintVertexToken(ctx context.Context, client *http.Client, sa map[string]any) (string, time.Time, error) {
	clientEmail, _ := sa["client_email"].(string)
	privateKey, _ := sa["private_key"].(string)
	if clientEmail == "" || privateKey == "" {
		return "", time.Time{}, fmt.Errorf("vertex service account is missing client_email/private_key")
	}
	key, err := parseRSAKey(privateKey)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{
		"iss":   clientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", time.Time{}, err
	}
	assertion := signing + "." + base64.RawURLEncoding.EncodeToString(sig)
	tok, err := postTokenForm(ctx, client, "https://oauth2.googleapis.com/token", url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	})
	if err != nil {
		return "", time.Time{}, err
	}
	accessToken, _ := tok["access_token"].(string)
	if accessToken == "" {
		return "", time.Time{}, fmt.Errorf("vertex token endpoint returned no access_token")
	}
	exp := time.Now().Add(time.Hour)
	if secs, ok := tok["expires_in"].(float64); ok && secs > 0 {
		exp = time.Now().Add(time.Duration(secs) * time.Second)
	}
	return accessToken, exp, nil
}

// postTokenForm posts a form body with an explicit client and returns the JSON.
func postTokenForm(ctx context.Context, client *http.Client, endpoint string, form url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %d: %s", req.URL.Host, resp.StatusCode, truncate(string(data), 300))
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	return out, nil
}

// RefreshGoogleToken refreshes an ADC authorized_user credential.
func RefreshGoogleToken(ctx context.Context, client *http.Client, refreshToken, clientID, clientSecret string) (string, time.Time, error) {
	if refreshToken == "" {
		return "", time.Time{}, fmt.Errorf("ADC credential is missing refresh_token")
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	tok, err := postTokenForm(ctx, client, "https://oauth2.googleapis.com/token", form)
	if err != nil {
		return "", time.Time{}, err
	}
	accessToken, _ := tok["access_token"].(string)
	if accessToken == "" {
		return "", time.Time{}, fmt.Errorf("google token endpoint returned no access_token")
	}
	exp := time.Now().Add(time.Hour)
	if secs, ok := tok["expires_in"].(float64); ok && secs > 0 {
		exp = time.Now().Add(time.Duration(secs) * time.Second)
	}
	return accessToken, exp, nil
}

func (m *Manager) postForm(ctx context.Context, endpoint string, form url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	return m.do(req)
}

func (m *Manager) postJSON(ctx context.Context, endpoint string, body any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return m.do(req)
}

func (m *Manager) do(req *http.Request) (map[string]any, error) {
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s returned %d: %s", req.URL.Host, resp.StatusCode, truncate(string(data), 300))
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------- background loop

// StartBackgroundRefresh refreshes OAuth connections every interval, in the
// same spirit as backgroundTokenRefresh.js (10s warmup, 5min cadence).
func (m *Manager) StartBackgroundRefresh(ctx context.Context, st *store.Store) {
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			m.refreshAll(ctx, st)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (m *Manager) refreshAll(ctx context.Context, st *store.Store) {
	conns, err := st.ListConnections("", true)
	if err != nil {
		return
	}
	for _, c := range conns {
		if !supported[c.Provider] {
			continue
		}
		lead := 30 * time.Minute
		if o := m.reg.OAuthFor(c.Provider); o != nil && o.RefreshLeadMs > 0 {
			lead = time.Duration(o.RefreshLeadMs) * time.Millisecond
		}
		if _, err := m.RefreshOne(ctx, st, c, lead); err != nil {
			st.PatchConnection(c.ID, map[string]any{
				"lastError":   err.Error(),
				"lastErrorAt": time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
}

// ---------------------------------------------------------------- helpers

func pkce() (verifier, challenge string, err error) {
	buf := make([]byte, 64)
	if _, err = rand.Read(buf); err != nil {
		return
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func token(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func expiry(tok map[string]any) string {
	if secs, ok := tok["expires_in"].(float64); ok && secs > 0 {
		return time.Now().Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339)
	}
	return ""
}

func parseTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("invalid private key PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := key.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, fmt.Errorf("not an RSA key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// CallbackAddr is the fixed loopback address the CLI/desktop flow listens on.
func CallbackAddr(provider string) string {
	if provider == "codex" {
		return "127.0.0.1:1455"
	}
	return "127.0.0.1:1456"
}

// WaitForCallback runs a one-shot loopback HTTP server for the redirect URI and
// returns the authorization code.
func WaitForCallback(ctx context.Context, addr, path string) (string, string, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", "", err
	}
	defer ln.Close()
	type result struct{ code, state string }
	ch := make(chan result, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<h2>KineRouter authorization received. You can close this window.</h2>")
		ch <- result{code: r.URL.Query().Get("code"), state: r.URL.Query().Get("state")}
	})}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	select {
	case <-ctx.Done():
		return "", "", ctx.Err()
	case res := <-ch:
		if res.code == "" {
			return "", "", fmt.Errorf("authorization failed")
		}
		return res.code, res.state, nil
	}
}
