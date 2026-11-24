package gateway

// POST /v1/web/fetch — URL extraction. Provider IS the model, as in
// src/sse/handlers/fetch.js. The client-supplied target URL is validated by
// netguard before any request leaves the process.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/netguard"
)

func (g *Gateway) handleWebFetch(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	providerInput, _ := body["provider"].(string)
	if providerInput == "" {
		providerInput, _ = body["model"].(string)
	}
	if providerInput == "" {
		writeErr(w, http.StatusBadRequest, "Missing required field: provider (or model)")
		return
	}
	target, _ := body["url"].(string)
	if target == "" {
		writeErr(w, http.StatusBadRequest, "Missing required field: url")
		return
	}
	if u, err := neturl.Parse(target); err != nil || u.Host == "" {
		writeErr(w, http.StatusBadRequest, "Invalid URL format")
		return
	}
	// SSRF: reject internal/metadata targets, including hostnames that merely
	// resolve to one.
	if err := netguard.CheckURL(r.Context(), target); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := g.authorizeRequest(w, r, providerInput); !ok {
		return
	}
	provider := g.reg.ResolveAlias(providerInput)
	cfg := g.mediaConfig(provider, "fetchConfig")
	if cfg == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider %s does not support web fetch", provider))
		return
	}
	format := str(body, "format")
	if format == "" {
		format = "markdown"
	}
	if formats, ok := cfg["formats"].([]any); ok && len(formats) > 0 {
		ok := false
		for _, f := range formats {
			if f == format {
				ok = true
			}
		}
		if !ok {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("Unsupported format for %s", provider))
			return
		}
	}
	maxChars := intAt(body, "max_characters", intAt(cfg, "maxCharacters", 100000))

	token := ""
	if conns, _ := g.store.ListConnections(provider, true); len(conns) > 0 {
		token = conns[0].Str("apiKey")
		if token == "" {
			token = conns[0].Str("accessToken")
		}
	}
	if cfg["authType"] != "none" && token == "" {
		writeErr(w, http.StatusUnauthorized, "No credentials for provider: "+provider)
		return
	}

	data, err := g.fetchPage(r.Context(), provider, cfg, token, target, format, maxChars)
	if err != nil {
		if e, ok := err.(*statusError); ok {
			writeErr(w, e.status, e.Error())
			return
		}
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(mustJSON(data))
}

// fetchPage runs one provider extraction and returns the unified payload.
func (g *Gateway) fetchPage(ctx context.Context, provider string, cfg map[string]any, token, target, format string, maxChars int) (map[string]any, error) {
	timeout := time.Duration(intAt(cfg, "timeoutMs", 15000)) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	base, _ := cfg["baseUrl"].(string)
	cost, hasCost := cfg["costPerQuery"].(float64)

	start := time.Now()
	reqURL, reqBody := "", []byte(nil)
	method := http.MethodPost
	headers := map[string]string{"Content-Type": "application/json"}
	switch provider {
	case "firecrawl":
		reqURL = base
		headers["Authorization"] = "Bearer " + token
		reqBody = mustJSON(map[string]any{"url": target, "formats": []any{format}})
	case "jina-reader":
		reqURL = strings.TrimRight(base, "/") + "/"
		headers["x-respond-with"] = format
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		reqBody = mustJSON(map[string]any{"url": target})
	case "tavily":
		reqURL = base
		headers["Authorization"] = "Bearer " + token
		reqBody = mustJSON(map[string]any{"urls": []any{target}, "extract_depth": "basic", "format": format})
	case "exa":
		reqURL = base
		headers["x-api-key"] = token
		reqBody = mustJSON(map[string]any{"ids": []any{target}, "text": true})
	case "ollama":
		reqURL = base
		headers["Authorization"] = "Bearer " + token
		reqBody = mustJSON(map[string]any{"url": target})
	case "tinyfish":
		reqURL = base
		headers["X-API-Key"] = token
		reqBody = mustJSON(map[string]any{"urls": []any{target}, "format": format})
	case "keenable":
		reqURL = base
		headers["X-Keenable-Title"] = "KineRouter"
		if token == "" {
			reqURL += "/public"
		} else {
			headers["X-API-Key"] = token
		}
		reqURL += "?url=" + neturl.QueryEscape(target)
		method = http.MethodGet
	default:
		return nil, fmt.Errorf("unsupported fetch provider %q", provider)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := mediaClient.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, &statusError{status: http.StatusGatewayTimeout, msg: "upstream timed out"}
		}
		return nil, &statusError{status: http.StatusBadGateway, msg: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	upstreamMs := time.Since(start).Milliseconds()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return nil, &statusError{status: resp.StatusCode, msg: fmt.Sprintf("fetch failed: %s", msg)}
	}

	title, text, meta, err := fetchExtract(provider, raw)
	if err != nil {
		return nil, err
	}
	if maxChars > 0 && len(text) > maxChars {
		text = text[:maxChars]
	}
	d := map[string]any{
		"provider": provider,
		"url":      target,
		"title":    nullStr(title),
		"content":  map[string]any{"format": format, "text": text, "length": len(text)},
		"metadata": map[string]any{"author": meta["author"], "published_at": meta["published_at"], "language": meta["language"]},
		"usage":    map[string]any{"fetch_cost_usd": nil},
		"metrics":  map[string]any{"response_time_ms": time.Since(start).Milliseconds(), "upstream_latency_ms": upstreamMs},
	}
	if hasCost {
		d["usage"] = map[string]any{"fetch_cost_usd": cost}
	}
	return d, nil
}

// fetchExtract pulls title/text/metadata out of each provider's payload.
func fetchExtract(provider string, raw []byte) (title, text string, meta map[string]any, err error) {
	meta = map[string]any{"author": nil, "published_at": nil, "language": nil}
	var m map[string]any
	json.Unmarshal(raw, &m)
	first := func(key string) map[string]any {
		l, _ := m[key].([]any)
		if len(l) == 0 {
			return nil
		}
		e, _ := l[0].(map[string]any)
		return e
	}
	switch provider {
	case "firecrawl":
		d, _ := m["data"].(map[string]any)
		for _, k := range []string{"markdown", "html", "text"} {
			if s, ok := d[k].(string); ok && s != "" {
				text = s
				break
			}
		}
		if md, ok := d["metadata"].(map[string]any); ok {
			title, _ = md["title"].(string)
		}
	case "jina-reader":
		text = string(raw)
		title = jinaTitle(text)
	case "tavily":
		if r := first("results"); r != nil {
			text, _ = r["raw_content"].(string)
		}
	case "exa":
		if r := first("results"); r != nil {
			text, _ = r["text"].(string)
			title, _ = r["title"].(string)
		}
	case "ollama":
		text, _ = m["content"].(string)
		if text == "" {
			return "", "", meta, &statusError{status: http.StatusBadGateway, msg: "ollama returned no content"}
		}
	case "keenable":
		text, _ = m["content"].(string)
		title, _ = m["title"].(string)
		if text == "" {
			return "", "", meta, &statusError{status: http.StatusBadGateway, msg: "keenable returned no content"}
		}
		title, _ = m["title"].(string)
	case "tinyfish":
		if r := first("results"); r != nil {
			text, _ = r["text"].(string)
			title, _ = r["title"].(string)
			meta["author"] = r["author"]
			meta["published_at"] = r["published_date"]
			meta["language"] = r["language"]
		}
		if errs, ok := m["errors"].([]any); ok && len(errs) > 0 {
			e, _ := errs[0].(map[string]any)
			msg, _ := e["error"].(string)
			st := intAt(e, "status", http.StatusBadGateway)
			return "", "", meta, &statusError{status: st, msg: "tinyfish fetch failed: " + msg}
		}
		if text == "" {
			return "", "", meta, &statusError{status: http.StatusBadGateway, msg: "tinyfish returned no extractable content"}
		}
	}
	if text == "" {
		return "", "", meta, &statusError{status: http.StatusBadGateway, msg: provider + " returned no extractable content"}
	}
	return
}

var jinaTitleRe = regexp.MustCompile(`(?mi)^\s*Title:\s*(.+)$`)
var jinaHeadingRe = regexp.MustCompile(`(?m)^\s*#\s+(.+)$`)

func jinaTitle(s string) string {
	if mm := jinaTitleRe.FindStringSubmatch(s); mm != nil {
		return strings.TrimSpace(mm[1])
	}
	if mm := jinaHeadingRe.FindStringSubmatch(s); mm != nil {
		return strings.TrimSpace(mm[1])
	}
	return ""
}

// statusError carries the HTTP status a failure should be reported with.
type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }
