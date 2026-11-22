package oauth

// Per-provider authorization-code quirks, ported from
// src/lib/oauth/providers/{claude,codex,gemini-cli,antigravity,iflow,gitlab,
// cline,clinepass,kimchi}.js. The default in oauth.go is the plain PKCE form
// grant; anything listed here overrides the authorize query, the token
// exchange or both.
//
// ponytail: no browser-sidecar flows (zed RSA key exchange, trae/windsurf
// proxies, xiaomi ECDH) — add when KineRouter gets a desktop helper.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// tokenResult is the provider-neutral shape the save path understands.
type tokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    string
	Email        string
	Name         string
	IDToken      string
	ProjectID    string
	PSD          map[string]any
}

type codeFlow struct {
	// authorize replaces the default query builder. When nil, the plain PKCE
	// query is used.
	authorize func(m *Manager, o *providerdata.OAuth, clientID, redirectURI, state, challenge string, meta map[string]string) (string, error)
	// exchange replaces the default form grant. When nil, the plain PKCE form
	// grant is used.
	exchange func(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error)
	// noPKCE skips the code_challenge parameters (cline, clinepass, kimchi).
	noPKCE bool
}

var codeFlows = map[string]*codeFlow{
	"claude":      {authorize: claudeAuthorize, exchange: claudeExchange},
	"codex":       {exchange: codexExchange},
	"gemini-cli":  {authorize: googleAuthorize, exchange: googleExchange},
	"antigravity": {authorize: googleAuthorize, exchange: googleExchange},
	"iflow":       {authorize: iflowAuthorize, exchange: iflowExchange},
	"gitlab":      {authorize: gitlabAuthorize, exchange: gitlabExchange},
	"cline":       {authorize: clineAuthorize, noPKCE: true, exchange: clineExchange},
	"clinepass":   {authorize: clineAuthorize, noPKCE: true, exchange: clineExchange},
	"kimchi":      {authorize: kimchiAuthorize, noPKCE: true, exchange: kimchiExchange},
}

// ---------------------------------------------------------------- authorize

func claudeAuthorize(m *Manager, o *providerdata.OAuth, clientID, redirectURI, state, challenge string, _ map[string]string) (string, error) {
	q := baseQuery(o, clientID, redirectURI, state, challenge, true)
	q.Set("code", "true")
	return o.AuthorizeURL + "?" + q.Encode(), nil
}

func googleAuthorize(m *Manager, o *providerdata.OAuth, clientID, redirectURI, state, challenge string, _ map[string]string) (string, error) {
	q := baseQuery(o, clientID, redirectURI, state, challenge, true)
	q.Set("access_type", "offline")
	q.Set("prompt", "consent")
	return o.AuthorizeURL + "?" + q.Encode(), nil
}

func iflowAuthorize(m *Manager, o *providerdata.OAuth, clientID, redirectURI, state, challenge string, _ map[string]string) (string, error) {
	q := url.Values{
		"loginMethod": {strExtra(o, "loginMethod")},
		"type":        {strExtra(o, "type")},
		"redirect":    {redirectURI},
		"state":       {state},
		"client_id":   {clientID},
	}
	return o.AuthorizeURL + "?" + q.Encode(), nil
}

// gitlabAuthorize builds the URL from the connection's own instance
// (meta.baseUrl) — GitLab is self-hosted as often as not.
func gitlabAuthorize(m *Manager, o *providerdata.OAuth, _, redirectURI, state, challenge string, meta map[string]string) (string, error) {
	base := meta["baseUrl"]
	if base == "" {
		base = strExtra(o, "defaultBaseUrl")
	}
	clientID := meta["clientId"]
	if clientID == "" {
		return "", fmt.Errorf("gitlab needs a clientId (create an OAuth app on your GitLab instance)")
	}
	q := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"state":                 {state},
		"scope":                 {o.Scope},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return strings.TrimRight(base, "/") + strExtra(o, "authorizeUrlPath") + "?" + q.Encode(), nil
}

func clineAuthorize(m *Manager, o *providerdata.OAuth, _, redirectURI, _, _ string, _ map[string]string) (string, error) {
	q := url.Values{
		"client_type":  {"extension"},
		"callback_url": {redirectURI},
		"redirect_uri": {redirectURI},
	}
	return o.AuthorizeURL + "?" + q.Encode(), nil
}

func kimchiAuthorize(m *Manager, o *providerdata.OAuth, _, redirectURI, state, _ string, _ map[string]string) (string, error) {
	base := strings.TrimRight(strExtra(o, "webAppUrl"), "/")
	if base == "" {
		base = "https://app.kimchi.dev"
	}
	return base + "/cli-auth?" + url.Values{"callback": {redirectURI}, "state": {state}}.Encode(), nil
}

func baseQuery(o *providerdata.OAuth, clientID, redirectURI, state, challenge string, pkce bool) url.Values {
	q := url.Values{
		"client_id":     {clientID},
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"state":         {state},
	}
	if pkce {
		q.Set("code_challenge", challenge)
		method := o.CodeChallengeMethod
		if method == "" {
			method = "S256"
		}
		q.Set("code_challenge_method", method)
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
	return q
}

// ---------------------------------------------------------------- exchange

func claudeExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	clientID, _ := m.reg.ClientCreds(provider)
	// The pasted code may carry "#state" appended by the console.
	authCode, codeState := code, sess.State
	if i := strings.IndexByte(code, '#'); i >= 0 {
		authCode, codeState = code[:i], code[i+1:]
	}
	tok, err := m.postJSON(ctx, o.TokenURL, map[string]any{
		"code":          authCode,
		"state":         codeState,
		"grant_type":    "authorization_code",
		"client_id":     clientID,
		"redirect_uri":  sess.RedirectURI,
		"code_verifier": sess.Verifier,
	})
	if err != nil {
		return nil, err
	}
	return defaultResult(tok), nil
}

func codexExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	clientID, clientSecret := m.reg.ClientCreds(provider)
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
	psd := res.PSD
	if claims := jwtClaims(str(tok, "id_token")); claims != nil {
		if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			if v, ok := auth["chatgpt_account_id"].(string); ok && v != "" {
				psd["chatgptAccountId"] = v
			}
			if v, ok := auth["chatgpt_plan_type"].(string); ok && v != "" {
				psd["chatgptPlanType"] = v
			}
		}
		if res.Email == "" {
			res.Email = str(claims, "email")
		}
	}
	res.IDToken = str(tok, "id_token")
	return res, nil
}

// googleExchange covers gemini-cli and antigravity: Google's form grant plus
// the loadCodeAssist call that yields the projectId the CLI sends upstream.
func googleExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	clientID, clientSecret := m.reg.ClientCreds(provider)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {sess.RedirectURI},
	}
	if sess.Verifier != "" && o.CodeChallengeMethod != "" {
		form.Set("code_verifier", sess.Verifier)
	}
	tok, err := m.postForm(ctx, o.TokenURL, form)
	if err != nil {
		return nil, err
	}
	res := defaultResult(tok)
	if res.Email == "" && o.UserInfoURL != "" {
		res.Email = m.fetchEmail(ctx, o.UserInfoURL+"?alt=json", res.AccessToken)
	}
	if url := strExtra(o, "loadCodeAssistEndpoint"); url != "" {
		res.ProjectID = m.loadCodeAssist(ctx, provider, url, strExtra(o, "loadCodeAssistUserAgent"), res.AccessToken)
	}
	return res, nil
}

func iflowExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	clientID, clientSecret := m.reg.ClientCreds(provider)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL,
		strings.NewReader(url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {sess.RedirectURI},
			"client_id":     {clientID},
			"client_secret": {clientSecret},
		}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(clientID, clientSecret)
	tok, err := m.do(req)
	if err != nil {
		return nil, err
	}
	res := defaultResult(tok)
	// The iFlow API key only exists in the userinfo response — without it the
	// connection is useless, so a failure here fails the login.
	info, err := m.getJSON(ctx, o.UserInfoURL+"?accessToken="+url.QueryEscape(res.AccessToken), "")
	if err != nil {
		return nil, fmt.Errorf("iflow user info: %w", err)
	}
	if ok, _ := info["success"].(bool); !ok {
		return nil, fmt.Errorf("iflow user info: %s", str(info, "message"))
	}
	data, _ := info["data"].(map[string]any)
	if data == nil || str(data, "apiKey") == "" {
		return nil, fmt.Errorf("iflow returned an empty API key")
	}
	res.PSD["apiKey"] = str(data, "apiKey")
	if email := firstNonEmpty(str(data, "email"), str(data, "phone")); email != "" {
		res.Email = email
	} else {
		return nil, fmt.Errorf("iflow returned no account email/phone")
	}
	res.Name = firstNonEmpty(str(data, "nickname"), str(data, "name"))
	return res, nil
}

func gitlabExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	base := sess.Meta["baseUrl"]
	if base == "" {
		base = strExtra(o, "defaultBaseUrl")
	}
	base = strings.TrimRight(base, "/")
	form := url.Values{
		"client_id":     {sess.Meta["clientId"]},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {sess.RedirectURI},
		"code_verifier": {sess.Verifier},
	}
	if secret := sess.Meta["clientSecret"]; secret != "" {
		form.Set("client_secret", secret)
	}
	tok, err := m.postForm(ctx, base+strExtra(o, "tokenUrlPath"), form)
	if err != nil {
		return nil, err
	}
	res := defaultResult(tok)
	user, err := m.getJSON(ctx, base+strExtra(o, "userInfoUrlPath"), res.AccessToken)
	if err != nil {
		user = map[string]any{}
	}
	res.PSD["username"] = str(user, "username")
	res.PSD["email"] = firstNonEmpty(str(user, "email"), str(user, "public_email"))
	res.PSD["name"] = str(user, "name")
	res.PSD["baseUrl"] = base
	res.PSD["clientId"] = sess.Meta["clientId"]
	res.PSD["authKind"] = "oauth"
	if email, _ := res.PSD["email"].(string); email != "" && res.Email == "" {
		res.Email = email
	}
	return res, nil
}

// clineExchange unwraps the base64 JSON blob Cline hands back in `code`, and
// falls back to the documented JSON-grant endpoint.
func clineExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	if res := clineDecode(code); res != nil {
		return res, nil
	}
	endpoint := strExtra(o, "tokenExchangeUrl")
	if endpoint == "" {
		endpoint = o.TokenURL
	}
	raw, err := m.postJSON(ctx, endpoint, map[string]any{
		"grant_type":   "authorization_code",
		"code":         code,
		"client_type":  "extension",
		"redirect_uri": sess.RedirectURI,
	})
	if err != nil {
		return nil, err
	}
	data, _ := raw["data"].(map[string]any)
	if data == nil {
		data = raw
	}
	access := firstNonEmpty(str(data, "accessToken"), str(raw, "accessToken"))
	if access == "" {
		return nil, fmt.Errorf("%s token exchange returned no access token", provider)
	}
	res := &tokenResult{AccessToken: access,
		RefreshToken: firstNonEmpty(str(data, "refreshToken"), str(raw, "refreshToken")),
		ExpiresAt:    isoOrEmpty(firstNonEmpty(str(data, "expiresAt"), str(raw, "expiresAt"))),
		PSD:          map[string]any{}}
	if ui, ok := data["userInfo"].(map[string]any); ok {
		res.Email = str(ui, "email")
	}
	return res, nil
}

func clineDecode(code string) *tokenResult {
	b64 := code
	if pad := len(b64) % 4; pad != 0 {
		b64 += strings.Repeat("=", 4-pad)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(b64)
		if err != nil {
			return nil
		}
	}
	text := string(raw)
	if i := strings.LastIndexByte(text, '}'); i >= 0 {
		text = text[:i+1]
	}
	var data map[string]any
	if json.Unmarshal([]byte(text), &data) != nil || str(data, "accessToken") == "" {
		return nil
	}
	return &tokenResult{
		AccessToken:  str(data, "accessToken"),
		RefreshToken: str(data, "refreshToken"),
		Email:        str(data, "email"),
		ExpiresAt:    isoOrEmpty(str(data, "expiresAt")),
		PSD: map[string]any{
			"firstName": str(data, "firstName"),
			"lastName":  str(data, "lastName"),
		},
	}
}

// kimchiExchange takes a browser-issued token rather than a code: it is
// validated against the LLM gateway before being stored.
func kimchiExchange(ctx context.Context, m *Manager, provider string, sess *Session, code string) (*tokenResult, error) {
	o := m.reg.OAuthFor(provider)
	tok := strings.TrimSpace(code)
	if tok == "" {
		return nil, fmt.Errorf("missing Kimchi token")
	}
	if _, err := m.getJSON(ctx, strExtra(o, "validationUrl"), tok); err != nil {
		return nil, fmt.Errorf("kimchi token validation failed: %w", err)
	}
	user, err := m.getJSON(ctx, o.UserInfoURL, tok)
	if err != nil {
		user = map[string]any{}
	}
	psd := map[string]any{"authMethod": "browser_token"}
	userID := str(user, "id")
	psd["userId"] = userID
	psd["username"] = str(user, "username")
	res := &tokenResult{AccessToken: tok, Email: str(user, "email"),
		Name: firstNonEmpty(str(user, "name"), str(user, "username")), PSD: psd}
	if res.Email == "" && userID != "" {
		res.Email = "kimchi-user-" + userID
	}
	return res, nil
}

// ---------------------------------------------------------------- helpers

func defaultResult(tok map[string]any) *tokenResult {
	res := &tokenResult{
		AccessToken:  str(tok, "access_token"),
		RefreshToken: str(tok, "refresh_token"),
		ExpiresAt:    expiry(tok),
		PSD:          map[string]any{},
	}
	if acct, ok := tok["account"].(map[string]any); ok {
		if email, ok := acct["email_address"].(string); ok {
			res.Email = email
		}
	}
	return res
}

// loadCodeAssist asks Google's Code Assist backend for the cloud project the
// connection must bill against.
func (m *Manager) loadCodeAssist(ctx context.Context, provider, endpoint, userAgent, accessToken string) string {
	meta := map[string]any{"ideType": 9, "platform": platformEnum(), "pluginType": 2}
	body := map[string]any{"metadata": meta}
	if provider == "gemini-cli" {
		body["mode"] = 1
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(b)))
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("x-request-source", "local")
	}
	data, err := m.raw(req)
	if err != nil {
		return ""
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return ""
	}
	if p, ok := out["cloudaicompanionProject"].(map[string]any); ok {
		return str(p, "id")
	}
	if p, ok := out["cloudaicompanionProject"].(string); ok {
		return p
	}
	return ""
}

// platformEnum mirrors getOAuthPlatformEnum (the CLI reports its own platform).
func platformEnum() int {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return 2
		}
		return 1
	case "linux":
		if runtime.GOARCH == "arm64" {
			return 4
		}
		return 3
	case "windows":
		return 5
	}
	return 0
}

func (m *Manager) getJSON(ctx context.Context, endpoint, bearer string) (map[string]any, error) {
	if endpoint == "" {
		return nil, fmt.Errorf("no endpoint configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return m.do(req)
}

func strExtra(o *providerdata.OAuth, key string) string {
	if o == nil || o.Extra == nil {
		return ""
	}
	s, _ := o.Extra[key].(string)
	return s
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// isoOrEmpty keeps an already-ISO expiry, otherwise stamps now+lifetime.
func isoOrEmpty(v string) string {
	if v == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z", v); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

// refreshFor routes the refresh grant to the provider's own variant; the
// default is the plain OAuth2 form grant in oauth.go.
func (m *Manager) refreshFor(ctx context.Context, provider string, o *providerdata.OAuth, refreshToken string) (map[string]any, error) {
	switch provider {
	case "cline", "clinepass":
		endpoint := strExtra(o, "refreshUrl")
		if endpoint == "" {
			return nil, fmt.Errorf("%s has no refresh endpoint", provider)
		}
		raw, err := m.postJSON(ctx, endpoint, map[string]any{
			"refreshToken": refreshToken,
			"grantType":    "refresh_token",
			"clientType":   "extension",
		})
		if err != nil {
			return nil, err
		}
		data, _ := raw["data"].(map[string]any)
		if data == nil {
			data = raw
		}
		access := firstNonEmpty(str(data, "accessToken"), str(raw, "accessToken"))
		if access == "" {
			return nil, fmt.Errorf("%s refresh returned no access token", provider)
		}
		return map[string]any{
			"access_token":  access,
			"refresh_token": firstNonEmpty(str(data, "refreshToken"), refreshToken),
			"expires_in":    secondsUntil(firstNonEmpty(str(data, "expiresAt"), str(raw, "expiresAt")), numOf(data, "expiresIn")),
		}, nil
	case "iflow":
		clientID, clientSecret := m.reg.ClientCreds(provider)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.TokenURL,
			strings.NewReader(url.Values{
				"grant_type":    {"refresh_token"},
				"refresh_token": {refreshToken},
				"client_id":     {clientID},
				"client_secret": {clientSecret},
			}.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.SetBasicAuth(clientID, clientSecret)
		return m.do(req)
	}
	return m.refreshGrant(ctx, provider, o, refreshToken)
}

func numOf(m map[string]any, key string) float64 {
	f, _ := m[key].(float64)
	return f
}

// secondsUntil turns an absolute expiry into a relative lifetime; a missing
// one falls back to the provider-declared lifetime, else an hour.
func secondsUntil(expiryISO string, fallback float64) float64 {
	if expiryISO != "" {
		if t, err := time.Parse(time.RFC3339, expiryISO); err == nil {
			return float64(int(time.Until(t).Seconds()))
		}
	}
	if fallback > 0 {
		return fallback
	}
	return 3600
}
