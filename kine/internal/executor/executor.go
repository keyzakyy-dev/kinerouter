// Package executor performs the upstream provider call. It is the Go
// counterpart of open-sse/executors: DefaultExecutor covers every
// OpenAI-compatible / Claude-native / Gemini-native provider, driven by the
// embedded registry (base URL, static headers, auth scheme).
package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// Credentials is the resolved account for one attempt.
type Credentials struct {
	APIKey       string
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
	ProxyURL     string
	ProxyNoProxy string // connectionNoProxy: hosts that bypass this account's proxy
	BaseURL      string         // override (custom nodes / ollama)
	ProjectID    string         // Cloud Code Assist project (gemini-cli, antigravity)
	SessionID    string         // Cloud Code session (antigravity stickiness)
	AuthMethod   string         // provider-specific authMethod (kiro: api_key | idc | external_idp)
	ProfileARN   string         // kiro profile ARN from the connection
	Region       string         // provider region (kiro endpoint regionalization)
	MachineID    string         // device machine id (cursor checksum + session derived from it)
	Extra        map[string]any // remaining providerSpecificData (cursor ghostMode, ...)
	ExtraHeaders map[string]string
	Hooks        []string // header quirks from the matched transport's auth.hooks
}

// Request is one upstream call.
type Request struct {
	Provider string
	Model    string
	Format   string // provider wire format: openai | claude | gemini
	Body     []byte
	Stream   bool
	Creds    Credentials
	// ConnectionID names the stored row, used by executors that need a
	// per-connection directory (agy-acp).
	ConnectionID string
	// URLOverride replaces the registry-derived endpoint (embeddings, media
	// endpoints carry their own base URL in providerMedia).
	URLOverride string
}

// Response wraps the upstream HTTP response. Body must be closed by callers.
type Response struct {
	Status int
	Header http.Header
	Body   io.ReadCloser
}

// Executor sends a request upstream.
type Executor struct {
	client    *http.Client
	transport *http.Transport
	reg       *providerdata.Registry
}

func New() *Executor {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 0, // streaming responses may keep headers late
	}
	return &Executor{
		client:    &http.Client{Transport: transport},
		transport: transport,
		reg:       providerdata.Get(),
	}
}

// SetProxyFunc installs the outbound proxy resolver the transport consults for
// every request. The gateway installs its own so the dashboard's outbound-proxy
// setting reaches upstream calls: Go's http.ProxyFromEnvironment reads the
// process environment once and caches it, so the reference's "write process.env"
// approach cannot work here.
//
// Call it before the listener starts: http.Transport.Proxy must not change
// while requests are in flight. A nil fn restores the environment lookup.
func (e *Executor) SetProxyFunc(fn func(*http.Request) (*url.URL, error)) {
	if fn == nil {
		fn = http.ProxyFromEnvironment
	}
	if e.transport != nil {
		e.transport.Proxy = fn
	}
}

// Execute performs the call. It returns an error only for transport failures;
// non-2xx upstream responses are returned as a Response so the caller can
// apply the same account-fallback rules as the JS engine.
func (e *Executor) Execute(ctx context.Context, req Request) (*Response, error) {
	if req.Provider == "agy-acp" {
		return e.executeAGYACP(ctx, req)
	}
	// Bedrock signs per request and derives its URL from the resolved region, so
	// it cannot go through build's registry-derived endpoint.
	if _, ok := bedrockWire(req.Provider); ok {
		return e.executeBedrock(ctx, req)
	}
	// Vertex derives its URL and auth from the credential shape (SA JSON, ADC,
	// access token or raw key), which the registry cannot express.
	if partner, ok := vertexWire(req.Provider); ok {
		return e.executeVertex(ctx, req, partner)
	}
	endpoint, headers, err := e.build(req)
	if err != nil {
		return nil, err
	}
	if t := e.reg.TransportFor(req.Provider); req.Creds.Hooks == nil && t != nil && t.Auth != nil {
		req.Creds.Hooks = t.Auth.Hooks
	}
	if st := e.reg.TransportForSource(req.Provider, req.Format); st != nil && st.Auth != nil && len(st.Auth.Hooks) > 0 {
		req.Creds.Hooks = st.Auth.Hooks
	}
	if u, _ := ApplyQuirksTransport(req, headers); u != "" {
		endpoint = u
	}
	if req.URLOverride != "" {
		endpoint = req.URLOverride
	}
	body := req.Body
	// Free / no-auth providers never send a stored credential. MiMo's free lane
	// also needs a bootstrap JWT, the CLI marker message and a browser UA, all
	// set before the request is built.
	if e.IsNoAuth(req.Provider) {
		req.Creds = noAuthConnection()
		if req.Provider == "mimo-free" || req.Provider == "mmf" {
			if err := e.prepareMimo(&req, headers); err != nil {
				return nil, err
			}
			body = req.Body
		}
	}
	if isCloudCodeFormat(req.Format) {
		body, err = wrapCloudCode(req.Format, req.Model, req.Body, req.Creds)
		if err != nil {
			return nil, err
		}
	}
	// ZCode inspects the request body: the plan endpoint only answers to the
	// desktop client's identity system blocks and metadata.
	if req.Provider == "zcode" {
		if err := zcodeIdentityErr(); err != nil {
			return nil, err
		}
		seed, _ := req.Creds.Extra["sessionSeed"].(string)
		shaped, err := zcodeShapeBody(body, zcodeSessionID(req.Creds.SessionID, seed), zcodeDeviceID(req), zcodeAccountUUID(req))
		if err != nil {
			return nil, err
		}
		body = shaped
	}
	// Freebuff needs session admission and an agent run around every turn, so it
	// bypasses the plain passthrough below.
	if req.Provider == "freebuff" {
		fbClient := e.client
		if req.Creds.ProxyURL != "" {
			if u, err := url.Parse(req.Creds.ProxyURL); err == nil {
				fbClient = e.clientWithProxyNoProxy(u, req.Creds.ProxyNoProxy)
			}
		}
		return e.executeFreebuff(ctx, req, endpoint, headers, fbClient)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	if req.Stream && req.Format != "cursor" {
		httpReq.Header.Set("Accept", "text/event-stream")
	}
	client := e.client
	if req.Creds.ProxyURL != "" {
		if u, err := url.Parse(req.Creds.ProxyURL); err == nil {
			client = e.clientWithProxyNoProxy(u, req.Creds.ProxyNoProxy)
		}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	// Free Pi answers 409 concurrent_session when the account holds its session
	// cap. Release the other sessions once and replay the same request.
	if req.Provider == "free-pi" && resp.StatusCode == http.StatusConflict {
		conflict, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		token := firstNonEmpty(req.Creds.APIKey, req.Creds.AccessToken)
		if freePiIsConcurrent(conflict) && freePiResetConcurrent(ctx, client, token) {
			retry, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(req.Body))
			if err != nil {
				return nil, err
			}
			retry.Header = httpReq.Header
			resp, err = client.Do(retry)
			if err != nil {
				return nil, err
			}
		} else {
			resp.Body = io.NopCloser(bytes.NewReader(conflict))
		}
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}

// ClientWithProxy is the exported form of clientWithProxy for gateway code
// that issues its own media requests (image adapters) instead of going through
// Execute.
func ClientWithProxy(proxyURL string) *http.Client {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return &http.Client{Timeout: 300 * time.Second}
	}
	t := &http.Transport{
		Proxy:                 http.ProxyURL(u),
		MaxIdleConns:          50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Transport: t, Timeout: 300 * time.Second}
}

func (e *Executor) clientWithProxy(u *url.URL) *http.Client {
	return e.clientWithProxyNoProxy(u, "")
}

// clientWithProxyNoProxy applies the account's own NO_PROXY list. A bare
// http.ProxyURL ignores it, so a connection configured to bypass localhost (or
// a corporate suffix) would still be tunnelled through its proxy.
func (e *Executor) clientWithProxyNoProxy(u *url.URL, noProxy string) *http.Client {
	t := e.client.Transport.(*http.Transport).Clone()
	t.Proxy = proxyFuncWithNoProxy(u, noProxy)
	return &http.Client{Transport: t}
}

// proxyFuncWithNoProxy mirrors the shape of the reference's connection proxy
// options: a matching host dials direct, everything else uses the account's
// proxy. An empty list behaves exactly like http.ProxyURL.
func proxyFuncWithNoProxy(u *url.URL, noProxy string) func(*http.Request) (*url.URL, error) {
	entries := splitNoProxyList(noProxy)
	if len(entries) == 0 {
		return http.ProxyURL(u)
	}
	return func(req *http.Request) (*url.URL, error) {
		host := strings.ToLower(strings.TrimSuffix(req.URL.Hostname(), "."))
		for _, entry := range entries {
			if entry == "*" || host == entry || strings.HasSuffix(host, "."+entry) {
				return nil, nil
			}
		}
		return u, nil
	}
}

func splitNoProxyList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		p := strings.ToLower(strings.TrimSpace(part))
		p = strings.TrimPrefix(strings.TrimPrefix(p, "*"), ".")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// build derives URL + headers from the registry entry.
func (e *Executor) build(req Request) (string, map[string]string, error) {
	headers := map[string]string{"Content-Type": "application/json"}
	t := e.reg.TransportFor(req.Provider)
	// A multi-endpoint provider whose table names this wire format takes that
	// entry's URL/auth/headers verbatim (no translation, no suffix guessing).
	if st := e.reg.TransportForSource(req.Provider, req.Format); st != nil && st.BaseURL != "" {
		t = st
	}

	baseURL := req.Creds.BaseURL
	// Azure's endpoint is deployment-shaped and derived in ApplyQuirksTransport;
	// the registry carries no static base URL for it.
	if baseURL == "" && req.Provider == "azure" {
		baseURL = "https://api.openai.com"
	}
	if baseURL == "" && t != nil {
		baseURL = t.BaseURL
	}
	if baseURL == "" && t != nil && len(t.BaseURLs) > 0 {
		baseURL = t.BaseURLs[0]
	}
	if baseURL == "" {
		return "", nil, fmt.Errorf("provider %q has no base URL", req.Provider)
	}
	if t != nil {
		for k, v := range t.Headers {
			headers[k] = v
		}
	}
	if req.Creds.ExtraHeaders != nil {
		for k, v := range req.Creds.ExtraHeaders {
			headers[k] = v
		}
	}

	// Custom / compatible nodes already carry the full endpoint path.
	if !isOllamaFormat(req.Format) && (!strings.Contains(baseURL, "://") || strings.HasSuffix(baseURL, "/v1") || strings.HasSuffix(baseURL, "/v1/")) {
		if t != nil && t.Format == "claude" {
			baseURL = strings.TrimRight(baseURL, "/") + "/messages"
		} else if t != nil && t.Format == "gemini" {
			baseURL = strings.TrimRight(baseURL, "/") + "/models/" + req.Model + ":streamGenerateContent?alt=sse"
		} else {
			baseURL = strings.TrimRight(baseURL, "/") + "/chat/completions"
		}
	}

	// Registry urlSuffix (e.g. glm/kimi/minimax claude lanes want ?beta=true).
	if t != nil && t.URLSuffix != "" && !strings.Contains(baseURL, t.URLSuffix) {
		if strings.HasPrefix(t.URLSuffix, "?") && strings.Contains(baseURL, "?") {
			baseURL += "&" + strings.TrimPrefix(t.URLSuffix, "?")
		} else {
			baseURL += t.URLSuffix
		}
	}

	switch {
	case isCloudCodeFormat(req.Format):
		baseURL = cloudCodeURL(baseURL, req.Format, req.Stream)
		if t != nil && req.Format == "gemini-cli" {
			headers["User-Agent"] = geminiCLIUserAgent(t.CLIVersion, req.Model)
			if t.APIClient != "" {
				headers["X-Goog-Api-Client"] = t.APIClient
			}
		}
	case isOllamaFormat(req.Format):
		if u := ollamaEndpoint(req.Provider, req.Creds); u != "" {
			baseURL = u
		}
	case req.Format == "gemini":
		sep := "?"
		if strings.Contains(baseURL, "?") {
			sep = "&"
		}
		if !strings.Contains(baseURL, ":streamGenerateContent") && !strings.Contains(baseURL, ":generateContent") {
			action := ":generateContent"
			if req.Stream {
				action = ":streamGenerateContent?alt=sse"
				sep = ""
			}
			baseURL = strings.TrimRight(baseURL, "/") + "/models/" + req.Model + action
			if sep == "&" && req.Stream {
				baseURL += "&"
			}
		}
	}

	// Cursor: ConnectRPC framing and the token-derived checksum headers; the
	// endpoint comes from URLOverride (legacy vs agent surface).
	if req.Format == "cursor" {
		cursorHeaders, err := CursorHeaders(req.Creds)
		if err != nil {
			return "", nil, err
		}
		for k, v := range cursorHeaders {
			headers[k] = v
		}
		delete(headers, "Cookie")
		return baseURL, headers, nil
	}

	// Kiro: the auth header, agent-mode markers and X-Amz-Target depend on the
	// auth method and the surface (codewhisperer.* wants the target header), and
	// api_key / idc / external_idp accounts must hit the Amazon surfaces first.
	if req.Format == "kiro" {
		baseURL = kiroURL(baseURL, t)
		if req.Creds.Region != "" {
			baseURL = kiroRegionalize(baseURL, req.Creds.Region)
		}
		for k, v := range kiroHeaders(req.Creds, baseURL) {
			headers[k] = v
		}
		delete(headers, "Cookie")
		return baseURL, headers, nil
	}

	// Auth: entry transport.auth overrides the default Bearer header.
	token := req.Creds.AccessToken
	if req.Creds.APIKey != "" {
		token = req.Creds.APIKey
	}
	spec := (*providerdata.AuthSpec)(nil)
	if t != nil {
		spec = t.Auth
	}
	if req.Format == "claude" && spec == nil {
		spec = &providerdata.AuthSpec{Combined: true, Header: "x-api-key", Scheme: "raw"}
		if token != "" && strings.HasPrefix(token, "sk-ant-oat") {
			spec = &providerdata.AuthSpec{Combined: true, Header: "Authorization", Scheme: "bearer"}
		}
	}
	if isOllamaFormat(req.Format) {
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
	} else if req.Format == "gemini" {
		q := url.Values{}
		if token != "" {
			q.Set("key", token)
		}
		if enc := q.Encode(); enc != "" {
			if strings.Contains(baseURL, "?") {
				baseURL += "&" + enc
			} else {
				baseURL += "?" + enc
			}
		}
	} else if spec != nil {
		if spec.Combined {
			if spec.Scheme == "bearer" {
				headers[spec.Header] = "Bearer " + token
			} else {
				headers[spec.Header] = token
			}
		} else {
			if req.Creds.APIKey != "" && spec.APIKey != nil {
				headers[spec.APIKey.Header] = bearerOrRaw(spec.APIKey, req.Creds.APIKey)
			} else if req.Creds.AccessToken != "" && spec.OAuth != nil {
				headers[spec.OAuth.Header] = bearerOrRaw(spec.OAuth, req.Creds.AccessToken)
			}
		}
	} else if token != "" {
		headers["Authorization"] = "Bearer " + token
	}

	if req.Format == "claude" && headers["anthropic-version"] == "" {
		headers["anthropic-version"] = "2023-06-01"
	}
	// Strip credential leakage through debug headers.
	delete(headers, "Cookie")
	return baseURL, headers, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func bearerOrRaw(spec *providerdata.AuthSpec, token string) string {
	if spec.Scheme == "bearer" {
		return "Bearer " + token
	}
	return token
}

// CredentialsFromConnection builds executor credentials from a stored
// provider connection, mirroring the fields the JS auth layer reads.
func CredentialsFromConnection(c *store.Connection) Credentials {
	cr := Credentials{
		APIKey:       c.Str("apiKey"),
		AccessToken:  c.Str("accessToken"),
		RefreshToken: c.Str("refreshToken"),
		BaseURL:      c.Str("baseUrl"),
		ProjectID:    c.Str("projectId"),
	}
	if exp, err := time.Parse(time.RFC3339, c.Str("expiresAt")); err == nil {
		cr.ExpiresAt = exp
	}
	if cr.ProjectID == "" {
		cr.ProjectID = c.SpecificStr("projectId")
	}
	cr.SessionID = firstNonEmpty(c.SpecificStr("sessionId"), c.Email, c.ID)
	cr.AuthMethod = c.SpecificStr("authMethod")
	cr.ProfileARN = c.SpecificStr("profileArn")
	cr.Region = c.SpecificStr("region")
	cr.MachineID = c.SpecificStr("machineId")
	if sp := c.Specific(); sp != nil {
		cr.Extra = sp
		if v, ok := sp["baseUrl"].(string); ok && v != "" {
			cr.BaseURL = v
		}
		if sp["connectionProxyEnabled"] == true {
			if v, ok := sp["connectionProxyUrl"].(string); ok {
				cr.ProxyURL = v
			}
			if v, ok := sp["connectionNoProxy"].(string); ok {
				cr.ProxyNoProxy = v
			}
		}
	}
	if cr.AccessToken == "" {
		cr.AccessToken = c.Str("token")
	}
	return cr
}
