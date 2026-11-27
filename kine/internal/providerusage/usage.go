// Package providerusage fetches live quota/usage from provider APIs for the
// dashboard (/api/usage/{connectionId}). Ported from open-sse/services/usage/*.
//
// Security: every endpoint URL comes from the embedded provider registry
// (providerTransport.Usage), never from the request, so user input cannot
// redirect a credentialed call. The optional connection proxy URL was already
// validated with checkPublicURL when the connection was saved.
//
// ponytail: only the OAuth pair (claude, codex) plus the cheap api-key quotas
// (deepseek/glm/groq/opencode) are ported. Any other provider returns the same
// `{message: "Usage API not implemented for X"}` the JS dispatcher returns for
// an unknown provider; add a handler here when a dashboard surface needs it.

package providerusage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// Conn is the subset of a connection the usage handlers read.
type Conn struct {
	Provider    string
	AuthType    string
	AccessToken string
	APIKey      string
	Specific    map[string]any
	Force       bool
}

// Client builds the outbound HTTP client. A connection proxy wins; otherwise a
// direct client (strictProxy=false behaviour: quota reads never fail over a
// broken proxy config). The optional noProxy argument is the connection's
// connectionNoProxy list; without it the proxy is used for every host.
func Client(proxyURL string, noProxy ...string) *http.Client {
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			var bypass string
			if len(noProxy) > 0 {
				bypass = noProxy[0]
			}
			return &http.Client{
				Transport: &http.Transport{Proxy: proxyFunc(u, bypass)},
				Timeout:   20 * time.Second,
			}
		}
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func proxyFunc(u *url.URL, noProxy string) func(*http.Request) (*url.URL, error) {
	if strings.TrimSpace(noProxy) == "" {
		return http.ProxyURL(u)
	}
	entries := []string{}
	for _, part := range strings.Split(noProxy, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			entries = append(entries, strings.TrimPrefix(strings.TrimPrefix(p, "*"), "."))
		}
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

// proxyURLOf reads the connection proxy opt-in out of providerSpecificData.
func proxyURLOf(c Conn) string {
	if c.Specific == nil {
		return ""
	}
	if on, _ := c.Specific["connectionProxyEnabled"].(bool); !on {
		return ""
	}
	s, _ := c.Specific["connectionProxyUrl"].(string)
	return s
}

func connNoProxyOf(c Conn) string {
	if c.Specific == nil {
		return ""
	}
	if on, _ := c.Specific["connectionProxyEnabled"].(bool); !on {
		return ""
	}
	s, _ := c.Specific["connectionNoProxy"].(string)
	return s
}

// Fetch dispatches to the provider handler. Unknown providers (and providers
// whose handler is not ported yet) return the JS fallback payload.
func Fetch(ctx context.Context, c Conn) map[string]any {
	reg := providerdata.Get()
	id := reg.ResolveAlias(c.Provider)
	e := reg.ByID[id]
	client := Client(proxyURLOf(c), connNoProxyOf(c))

	switch id {
	case "claude":
		return fetchClaude(ctx, client, e, c.AccessToken, c.Force)
	case "codex":
		return fetchCodex(ctx, client, e, c.AccessToken)
	case "deepseek":
		return fetchDeepseek(ctx, client, e, c.APIKey)
	case "glm", "glm-cn":
		return fetchGlm(ctx, client, e, c.APIKey, c.AccessToken)
	case "groq":
		return fetchGroq(ctx, client, e, c.APIKey)
	case "opencode-go", "opencode-zen":
		return fetchOpenCode(ctx, client, e, c.APIKey)
	}
	return map[string]any{"message": "Usage API not implemented for " + c.Provider}
}

// ---------------------------------------------------------------- helpers

func usageURL(e *providerdata.Entry, key string) string {
	if e == nil {
		return ""
	}
	return e.UsageURL(key)
}

func convertFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case int:
		return float64(x), true
	}
	return 0, false
}

func toNum(v any, def float64) float64 {
	if f, ok := convertFloat(v); ok {
		return f
	}
	if s, ok := v.(string); ok && s != "" {
		var f float64
		if _, err := jsonNumberParse(s, &f); err == nil {
			return f
		}
	}
	return def
}

func jsonNumberParse(s string, f *float64) (int, error) {
	var n json.Number
	if err := json.Unmarshal([]byte(s), &n); err != nil {
		return 0, err
	}
	v, err := n.Float64()
	*f = v
	return 0, err
}

/*
parseResetTime mirrors usage/shared.js: a numeric unix timestamp is seconds
when below 1e12, otherwise milliseconds; a numeric string is treated the
same; any other string is parsed as a date. Output is RFC3339 or empty.
*/
func parseResetTime(v any) string {
	if v == nil {
		return ""
	}
	if f, ok := convertFloat(v); ok {
		return unixToISO(f)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	var n float64
	if _, err := jsonNumberParse(s, &n); err == nil {
		return unixToISO(n)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

func unixToISO(n float64) string {
	if n <= 0 {
		return ""
	}
	sec := n
	if n >= 1e12 {
		sec = n / 1000
	}
	return time.Unix(int64(sec), 0).UTC().Format(time.RFC3339)
}

// getJSON performs a GET and decodes the body; headers nil is honoured.
func getJSON(ctx context.Context, c *http.Client, url string, headers map[string]string) (int, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	var m map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &m)
	}
	return res.StatusCode, m, nil
}
