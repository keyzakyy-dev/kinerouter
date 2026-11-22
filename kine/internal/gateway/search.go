package gateway

// POST /v1/search — web search across the registry's dedicated search APIs.
// Mirrors open-sse/handlers/search/{index,callers,normalizers}.js.
// Providers without a dedicated API (registry `searchViaChat`) go through
// searchViaChat in chatsearch.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type searchParams struct {
	query      string
	searchType string
	maxResults int
	token      string
	country    string
	language   string
	timeRange  string
	offset     int
	domains    []string
}

func (g *Gateway) handleSearch(w http.ResponseWriter, r *http.Request) {
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
	query, _ := body["query"].(string)
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		writeErr(w, http.StatusBadRequest, "Missing required field: query")
		return
	}
	if _, ok := g.authorizeRequest(w, r, providerInput); !ok {
		return
	}
	provider := g.reg.ResolveAlias(providerInput)
	if provider == "" {
		provider = providerInput
	}
	cfg := g.mediaConfig(provider, "searchConfig")
	viaChat := cfg == nil && g.mediaConfig(provider, "searchViaChat") != nil
	if cfg == nil && !viaChat {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider %s does not support web search", provider))
		return
	}

	p := searchParams{
		query:      query,
		searchType: str(body, "search_type"),
		maxResults: intAt(body, "max_results", intAt(cfg, "defaultMaxResults", 5)),
		country:    str(body, "country"),
		language:   str(body, "language"),
		timeRange:  str(body, "time_range"),
		offset:     intAt(body, "offset", 0),
	}
	if p.searchType == "" {
		if ts, ok := cfg["searchTypes"].([]any); ok && len(ts) > 0 {
			p.searchType, _ = ts[0].(string)
		}
	}
	if p.searchType == "" {
		p.searchType = "web"
	}
	if max, ok := num64(cfg["maxMaxResults"]); ok && int64(p.maxResults) > max {
		p.maxResults = int(max)
	}
	if p.maxResults <= 0 {
		p.maxResults = 5
	}
	if df, ok := body["domain_filter"].([]any); ok {
		for _, d := range df {
			if s, ok := d.(string); ok && s != "" {
				p.domains = append(p.domains, s)
			}
		}
	}

	var status int
	var out []byte
	var err error
	if viaChat {
		status, out, err = g.searchViaChat(r, provider, p)
	} else {
		status, out, err = g.searchOnce(r, provider, cfg, p)
	}
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(out)
}

// searchOnce runs one dedicated search and normalizes its results.
func (g *Gateway) searchOnce(r *http.Request, provider string, cfg map[string]any, p searchParams) (int, []byte, error) {
	conns, _ := g.store.ListConnections(provider, true)
	conn := g.pickConnection(conns, provider, map[string]bool{})
	if conn != nil {
		p.token = conn.Str("apiKey")
		if p.token == "" {
			p.token = conn.Str("accessToken")
		}
	}
	if cfg["authType"] != "none" && p.token == "" {
		return http.StatusUnauthorized, nil, fmt.Errorf("No credentials for provider: %s", provider)
	}

	base, _ := cfg["baseUrl"].(string)
	reqURL, method, headers, payload, err := buildSearchRequest(provider, base, cfg, p)
	if err != nil {
		return http.StatusBadRequest, nil, err
	}
	req, err := http.NewRequestWithContext(r.Context(), method, reqURL, bytes.NewReader(payload))
	if err != nil {
		return http.StatusBadRequest, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := mediaClient.Do(req)
	if err != nil {
		return http.StatusBadGateway, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	status := resp.StatusCode
	if err != nil {
		return status, nil, err
	}
	if status < 200 || status >= 300 {
		return status, nil, fmt.Errorf("%s returned %d: %.200s", provider, status, raw)
	}
	results := normalizeSearchResults(provider, raw, p.searchType)
	out := mustJSON(map[string]any{
		"provider": provider,
		"query":    p.query,
		"results":  results,
		"answer":   nil,
		"usage":    map[string]any{"queries_used": 1, "search_cost_usd": cfg["costPerQuery"]},
		"metrics":  map[string]any{"response_time_ms": 0, "total_results_available": len(results)},
		"errors":   []any{},
	})
	return http.StatusOK, out, nil
}

// buildSearchRequest returns the provider-specific request. Every URL is
// assembled from the registry baseUrl — never from client input.
func buildSearchRequest(provider, base string, cfg map[string]any, p searchParams) (string, string, map[string]string, []byte, error) {
	base = strings.TrimRight(base, "/")
	jsonH := map[string]string{"Content-Type": "application/json"}
	auth := func(h map[string]string) map[string]string {
		switch cfg["authHeader"] {
		case "bearer":
			h["Authorization"] = "Bearer " + p.token
		case "x-api-key":
			h["x-api-key"] = p.token
		case "x-subscription-token":
			h["X-Subscription-Token"] = p.token
		case "api_key":
			h["api_key"] = p.token
		case "key":
			h["key"] = p.token
		default:
			h["Authorization"] = "Bearer " + p.token
		}
		return h
	}
	q := func(k, v string) string {
		if v == "" {
			return ""
		}
		return "&" + k + "=" + urlQueryEscape(v)
	}
	includes, excludes := splitDomains(p.domains)

	switch provider {
	case "serper":
		ep := "/search"
		if p.searchType == "news" {
			ep = "/news"
		}
		b := map[string]any{"q": p.query, "num": p.maxResults}
		if p.country != "" {
			b["gl"] = strings.ToLower(p.country)
		}
		if p.language != "" {
			b["hl"] = p.language
		}
		return base + ep, http.MethodPost, auth(jsonH), mustJSON(b), nil
	case "brave-search":
		ep := "/web/search"
		if p.searchType == "news" {
			ep = "/news/search"
		}
		u := base + ep + "?q=" + urlQueryEscape(p.query) + "&count=" + strconv.Itoa(p.maxResults) +
			q("country", p.country) + q("search_lang", p.language)
		return u, http.MethodGet, auth(map[string]string{"Accept": "application/json"}), nil, nil
	case "tavily":
		b := map[string]any{"query": p.query, "max_results": p.maxResults, "topic": "general"}
		if p.searchType == "news" {
			b["topic"] = "news"
		}
		if len(includes) > 0 {
			b["include_domains"] = includes
		}
		if len(excludes) > 0 {
			b["exclude_domains"] = excludes
		}
		return base, http.MethodPost, auth(jsonH), mustJSON(b), nil
	case "exa":
		b := map[string]any{"query": p.query, "numResults": p.maxResults, "type": "auto", "text": true, "highlights": true}
		if len(includes) > 0 {
			b["includeDomains"] = includes
		}
		if len(excludes) > 0 {
			b["excludeDomains"] = excludes
		}
		if p.searchType == "news" {
			b["category"] = "news"
		}
		return base, http.MethodPost, auth(jsonH), mustJSON(b), nil
	case "google-pse":
		cx := strFromCfg(cfg, "cx")
		u := base + "?key=" + urlQueryEscape(p.token) + "&cx=" + urlQueryEscape(cx) +
			"&q=" + urlQueryEscape(p.query) + "&num=" + strconv.Itoa(minInt(p.maxResults, 10)) +
			q("gl", strings.ToLower(p.country)) + q("hl", p.language)
		if cx == "" {
			return "", "", nil, nil, fmt.Errorf("Google Programmable Search requires cx")
		}
		return u, http.MethodGet, map[string]string{"Accept": "application/json"}, nil, nil
	case "linkup":
		b := map[string]any{"q": p.query, "depth": "standard", "outputType": "searchResults", "maxResults": p.maxResults}
		if len(includes) > 0 {
			b["includeDomains"] = includes
		}
		if len(excludes) > 0 {
			b["excludeDomains"] = excludes
		}
		return base, http.MethodPost, auth(jsonH), mustJSON(b), nil
	case "searchapi":
		engine := "google"
		if p.searchType == "news" {
			engine = "google_news"
		}
		u := base + "?engine=" + engine + "&q=" + urlQueryEscape(p.query) + "&api_key=" + urlQueryEscape(p.token) +
			q("gl", strings.ToLower(p.country)) + q("hl", p.language)
		return u, http.MethodGet, map[string]string{"Accept": "application/json"}, nil, nil
	case "youcom":
		u := base + "?query=" + urlQueryEscape(p.query) + "&count=" + strconv.Itoa(minInt(p.maxResults, 100))
		if len(includes) > 0 {
			u += "&include_domains=" + urlQueryEscape(strings.Join(includes, ","))
		}
		if len(excludes) > 0 {
			u += "&exclude_domains=" + urlQueryEscape(strings.Join(excludes, ","))
		}
		return u, http.MethodGet, auth(map[string]string{"Accept": "application/json"}), nil, nil
	case "searxng":
		cat := "general"
		if p.searchType == "news" {
			cat = "news"
		}
		u := base
		if !strings.HasSuffix(u, "/search") {
			u += "/search"
		}
		u += "?q=" + urlQueryEscape(p.query) + "&format=json&categories=" + cat + q("language", p.language)
		if p.timeRange != "" && p.timeRange != "any" {
			u += "&time_range=" + urlQueryEscape(p.timeRange)
		}
		return u, http.MethodGet, map[string]string{"Accept": "application/json"}, nil, nil
	case "ollama-search":
		b := map[string]any{"query": p.query, "max_results": p.maxResults}
		return base, http.MethodPost, auth(jsonH), mustJSON(b), nil
	case "glm":
		b := map[string]any{"jsonrpc": "2.0", "id": "kinerouter", "method": "tools/call",
			"params": map[string]any{"name": "web_search_prime",
				"arguments": map[string]any{"search_query": p.query, "count": p.maxResults}}}
		return base, http.MethodPost, auth(jsonH), mustJSON(b), nil
	case "tinyfish":
		u := base + "?query=" + urlQueryEscape(p.query) + q("location", p.country) + q("language", p.language)
		if p.searchType != "web" {
			u += "&domain_type=" + urlQueryEscape(p.searchType)
		}
		return u, http.MethodGet, auth(map[string]string{"Accept": "application/json"}), nil, nil
	case "xquik":
		u := base + "?q=" + urlQueryEscape(p.query) + "&limit=" + strconv.Itoa(p.maxResults)
		return u, http.MethodGet, auth(map[string]string{"Accept": "application/json"}), nil, nil
	}
	return "", "", nil, nil, fmt.Errorf("search provider %q is not implemented", provider)
}

// normalizeSearchResults maps each provider's payload onto one result shape.
func normalizeSearchResults(provider string, raw []byte, searchType string) []any {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return []any{}
	}
	var items []any
	list := func(v any) []any { l, _ := v.([]any); return l }
	switch provider {
	case "serper":
		if searchType == "news" {
			items = list(m["news"])
		} else {
			items = list(m["organic"])
		}
	case "brave-search":
		container, _ := m["web"].(map[string]any)
		if searchType == "news" {
			container, _ = m["news"].(map[string]any)
		}
		items = list(container["results"])
	case "tavily", "exa", "linkup", "searxng", "ollama-search":
		items = list(m["results"])
	case "google-pse":
		items = list(m["items"])
	case "searchapi":
		items = list(m["organic_results"])
		if len(items) == 0 {
			items = list(m["top_stories"])
		}
	case "youcom":
		if r, ok := m["results"].(map[string]any); ok {
			items = list(r["web"])
		}
		if len(items) == 0 {
			items = list(m["results"])
		}
	case "glm":
		items = parseGLMResults(m)
	case "tinyfish":
		items = list(m["results"])
	case "xquik":
		items = list(m["tweets"])
	}
	out := make([]any, 0, len(items))
	for i, it := range items {
		im, _ := it.(map[string]any)
		if im == nil {
			continue
		}
		title := firstStr(im, "title", "name")
		if provider == "xquik" {
			title = firstStr(im, "text")
			if author, ok := im["author"].(map[string]any); ok {
				title = "@" + firstStr(author, "username") + ": " + title
			}
		}
		link := firstStr(im, "url", "link")
		snippet := firstStr(im, "snippet", "description", "content", "text")
		if provider == "searxng" {
			snippet = firstStr(im, "content", "snippet")
		}
		published := firstStr(im, "published_at", "publishedDate", "date", "page_age", "createdAt")
		out = append(out, map[string]any{
			"title":        title,
			"url":          link,
			"display_url":  displayURL(link),
			"snippet":      snippet,
			"position":     i + 1,
			"score":        nil,
			"published_at": nullStr(published),
			"favicon_url":  nil,
			"content":      nil,
			"metadata":     map[string]any{"author": nil, "language": nil, "source_type": nil, "image_url": nil},
			"citation":     map[string]any{"provider": provider, "retrieved_at": time.Now().UTC().Format(time.RFC3339), "rank": i + 1},
		})
	}
	return out
}

// parseGLMResults unwraps the MCP envelope: result.content[0].text is JSON.
func parseGLMResults(m map[string]any) []any {
	result, _ := m["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return nil
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var inner map[string]any
	if json.Unmarshal([]byte(text), &inner) != nil {
		return nil
	}
	if l, ok := inner["results"].([]any); ok {
		return l
	}
	return nil
}

func displayURL(u string) any {
	if u == "" {
		return nil
	}
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	s = strings.TrimPrefix(s, "www.")
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	return s
}

func splitDomains(list []string) (includes, excludes []string) {
	for _, d := range list {
		if strings.HasPrefix(d, "-") {
			excludes = append(excludes, strings.TrimPrefix(d, "-"))
		} else {
			includes = append(includes, d)
		}
	}
	return
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func intAt(m map[string]any, k string, def int) int {
	if v, ok := num64(m[k]); ok {
		return int(v)
	}
	return def
}

func num64(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int:
		return int64(x), true
	case int64:
		return x, true
	}
	return 0, false
}

func strFromCfg(cfg map[string]any, k string) string {
	if s, ok := cfg[k].(string); ok {
		return s
	}
	return ""
}
