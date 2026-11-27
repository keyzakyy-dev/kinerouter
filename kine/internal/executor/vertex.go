package executor

// Vertex AI transport. Ported from open-sse/executors/vertex.js.
//
// Two providers share it:
//   vertex         Gemini bodies on publishers/google/models/<model>:<action>
//   vertex-partner OpenAI bodies on locations/global/endpoints/openapi/chat/completions
//
// Four credential shapes are accepted, in this order: a service-account JSON,
// an ADC authorized_user JSON (both pasted into the api key field, as the JS
// app does), a stored OAuth access token, or a raw API key sent as ?key=.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/oauth"
)

const vertexBaseURL = "https://aiplatform.googleapis.com"

var vertexProjectRe = regexp.MustCompile(`projects/([^/]+)/`)

// vertexTokenCache keeps minted service-account tokens alive across requests.
// ponytail: tokens are cached by client_email only, not evicted on 401 — a
// revoked key surfaces as the upstream error for up to the token lifetime.
var vertexTokenCache = struct {
	sync.Mutex
	m map[string]struct {
		token     string
		expiresAt time.Time
	}
}{m: map[string]struct {
	token     string
	expiresAt time.Time
}{}}

// vertexWire reports whether a provider id is served here, and whether it uses
// the partner (OpenAI-shaped) endpoint.
func vertexWire(p string) (partner bool, ok bool) {
	switch p {
	case "vertex":
		return false, true
	case "vertex-partner":
		return true, true
	}
	return false, false
}

// vertexCreds is what the executor needs from one connection.
type vertexCreds struct {
	accessToken string // Bearer, when any OAuth shape applies
	rawKey      string // ?key=, when nothing else does
	projectID   string
	location    string
}

// vertexJSON credential blobs are stored as a string in the api key field or as
// an object in providerSpecificData; both are accepted.
func vertexJSONBlob(c Credentials) map[string]any {
	if strings.HasPrefix(strings.TrimSpace(c.APIKey), "{") {
		var m map[string]any
		if json.Unmarshal([]byte(c.APIKey), &m) == nil {
			return m
		}
	}
	if c.Extra != nil {
		if _, ok := c.Extra["client_email"]; ok {
			return c.Extra
		}
		if _, ok := c.Extra["refresh_token"]; ok {
			return c.Extra
		}
	}
	return nil
}

func (e *Executor) resolveVertexCreds(ctx context.Context, c Credentials, model, provider string) (vertexCreds, error) {
	blob := vertexJSONBlob(c)
	out := vertexCreds{projectID: firstNonEmpty(c.ProjectID, strExtra(c.Extra, "projectId"))}
	location := strExtra(c.Extra, "location")

	if blob != nil {
		switch blob["type"] {
		case "service_account":
			out.projectID = firstNonEmpty(out.projectID, strAny(blob["project_id"]))
			if out.projectID == "" {
				return out, fmt.Errorf("Vertex service account has no project_id and no providerSpecificData.projectId")
			}
			token, err := e.vertexSAToken(ctx, blob)
			if err != nil {
				return out, err
			}
			out.accessToken = token
		case "authorized_user":
			out.projectID = firstNonEmpty(out.projectID, strAny(blob["quota_project_id"]))
			if out.projectID == "" {
				return out, fmt.Errorf("Vertex OAuth/ADC requires a project_id. Add quota_project_id to your ADC JSON or set providerSpecificData.projectId.")
			}
			token, _, err := oauth.RefreshGoogleToken(ctx, nil,
				strAny(blob["refresh_token"]), strAny(blob["client_id"]), strAny(blob["client_secret"]))
			if err != nil {
				return out, fmt.Errorf("vertex: refreshing ADC access token: %w", err)
			}
			out.accessToken = token
		}
	}

	if out.accessToken == "" && c.AccessToken != "" {
		out.accessToken = c.AccessToken
	}
	if out.accessToken == "" {
		if c.APIKey == "" {
			return out, fmt.Errorf("vertex needs a Service Account JSON, an ADC credential JSON or an access token")
		}
		out.rawKey = c.APIKey
	}

	if partner := provider == "vertex-partner"; partner && out.projectID == "" {
		// A raw partner key names no project; probe the API for it, as the JS
		// executor does, before giving up.
		id, err := e.probeVertexProject(ctx, c, out.rawKey)
		if err != nil || id == "" {
			return out, fmt.Errorf("Vertex partner models require a project_id. Add it in providerSpecificData or use Service Account JSON.")
		}
		out.projectID = id
		if c.Extra != nil {
			c.Extra["projectId"] = id
		}
	}

	if out.accessToken != "" && out.projectID == "" {
		return out, fmt.Errorf("Vertex OAuth/ADC requires a project_id. Add quota_project_id to your ADC JSON or set providerSpecificData.projectId.")
	}
	if location == "" {
		// Global-only models (registry defaultLocation, e.g. Argon) must not fall
		// back to a regional location.
		location = e.modelDefaultLocation(provider, model)
	}
	if location == "" {
		if out.accessToken != "" {
			location = "us-central1"
		} else {
			location = "global"
		}
	}
	out.location = location
	return out, nil
}

func (e *Executor) vertexSAToken(ctx context.Context, sa map[string]any) (string, error) {
	email := strAny(sa["client_email"])
	vertexTokenCache.Lock()
	if hit, ok := vertexTokenCache.m[email]; ok && time.Until(hit.expiresAt) > 5*time.Minute {
		vertexTokenCache.Unlock()
		return hit.token, nil
	}
	vertexTokenCache.Unlock()

	token, exp, err := oauth.MintVertexToken(ctx, nil, sa)
	if err != nil {
		return "", fmt.Errorf("vertex: failed to mint access token from Service Account JSON: %w", err)
	}
	vertexTokenCache.Lock()
	vertexTokenCache.m[email] = struct {
		token     string
		expiresAt time.Time
	}{token, exp}
	vertexTokenCache.Unlock()
	return token, nil
}

// probeVertexProject asks the API with a deliberately bogus model and reads the
// project id out of the 404 message. The key travels in the query string, so it
// never leaves this one call.
func (e *Executor) probeVertexProject(ctx context.Context, c Credentials, rawKey string) (string, error) {
	if rawKey == "" {
		return "", nil
	}
	endpoint := vertexBaseURL + "/v1/publishers/google/models/__probe__:generateContent?key=" + url.QueryEscape(rawKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader("{}"))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 15 * time.Second}
	if c.ProxyURL != "" {
		if u, err := url.Parse(c.ProxyURL); err == nil {
			client = *ClientWithProxy(u.String())
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	match := vertexProjectRe.FindSubmatch(data)
	if match == nil {
		return "", nil
	}
	return string(match[1]), nil
}

func vertexURL(partner bool, model string, creds vertexCreds, stream bool) string {
	action := "generateContent"
	if stream {
		action = "streamGenerateContent"
	}
	if partner {
		u := vertexBaseURL + "/v1/projects/" + creds.projectID +
			"/locations/global/endpoints/openapi/chat/completions"
		if creds.rawKey != "" {
			u += "?key=" + url.QueryEscape(creds.rawKey)
		}
		return u
	}
	prefix := vertexBaseURL + "/v1"
	if creds.projectID != "" {
		prefix += "/projects/" + creds.projectID + "/locations/" + creds.location
	}
	u := prefix + "/publishers/google/models/" + model + ":" + action
	if stream {
		u += "?alt=sse"
	}
	if creds.rawKey != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "key=" + url.QueryEscape(creds.rawKey)
	}
	return u
}

func (e *Executor) executeVertex(ctx context.Context, req Request, partner bool) (*Response, error) {
	creds, err := e.resolveVertexCreds(ctx, req.Creds, req.Model, req.Provider)
	if err != nil {
		return nil, err
	}
	endpoint := req.URLOverride
	if endpoint == "" {
		endpoint = vertexURL(partner, req.Model, creds, req.Stream)
	}
	headers := map[string]string{"Content-Type": "application/json"}
	if creds.accessToken != "" {
		headers["Authorization"] = "Bearer " + creds.accessToken
	}
	if req.Stream {
		headers["Accept"] = "text/event-stream"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(req.Body)))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
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
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}

func (e *Executor) modelDefaultLocation(provider, model string) string {
	entry := e.reg.ByID[provider]
	if entry == nil {
		return ""
	}
	for _, m := range entry.Models {
		if m.ID == model {
			return strAny(m.Extra["defaultLocation"])
		}
	}
	return ""
}

func strExtra(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	return strAny(extra[key])
}
