package gateway

// searchViaChat — LLM-grounded web search for the providers that have no
// dedicated search API (their registry entry carries `searchViaChat`).
// Mirrors open-sse/handlers/search/chatSearch.js.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
)

const chatSearchTimeout = 15 * time.Second

// chatSearchCfg is one provider's request builder + answer extractor.
type chatSearchCfg struct {
	build   func(q, model string, creds executor.Credentials) map[string]any
	headers func(creds executor.Credentials) map[string]string
	// extract returns the answer text and citation list ({url,title,snippet,content}).
	extract func(data map[string]any) (string, []map[string]any)
}

var chatSearchCfgs = map[string]chatSearchCfg{
	"gemini": {
		build: func(q, _ string, _ executor.Credentials) map[string]any {
			return map[string]any{
				"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": q}}}},
				"tools":    []any{map[string]any{"google_search": map[string]any{}}},
			}
		},
		headers: func(c executor.Credentials) map[string]string {
			return map[string]string{"Content-Type": "application/json", "x-goog-api-key": chatSearchToken(c)}
		},
		extract: extractGeminiAnswer,
	},
	"antigravity": {
		build: func(q, model string, c executor.Credentials) map[string]any {
			return map[string]any{
				"project":     c.ProjectID,
				"model":       model,
				"userAgent":   "antigravity",
				"requestType": "search",
				"request": map[string]any{
					"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": q}}}},
					"tools":            []any{map[string]any{"googleSearch": map[string]any{}}},
					"generationConfig": map[string]any{"temperature": 1.0, "maxOutputTokens": 8192},
				},
			}
		},
		headers: func(c executor.Credentials) map[string]string {
			return map[string]string{
				"Content-Type":  "application/json",
				"Authorization": "Bearer " + chatSearchToken(c),
				"User-Agent":    "antigravity/hub/2.24.0 darwin/arm64",
			}
		},
		extract: func(data map[string]any) (string, []map[string]any) {
			if inner, ok := data["response"].(map[string]any); ok {
				data = inner
			}
			return extractGeminiAnswer(data)
		},
	},
	"openai":            openAIStyleChat(),
	"vercel-ai-gateway": openAIStyleChat(),
	"xai": {
		build: func(q, model string, _ executor.Credentials) map[string]any {
			return map[string]any{
				"model": model,
				"input": []any{map[string]any{"role": "user", "content": q}},
				"tools": []any{map[string]any{"type": "web_search"}},
			}
		},
		headers: bearerHeaders,
		extract: extractResponsesAnswer,
	},
	"perplexity-agent": {
		build: func(q, model string, _ executor.Credentials) map[string]any {
			return map[string]any{
				"model": model,
				"input": q,
				"tools": []any{map[string]any{"type": "web_search"}},
			}
		},
		headers: bearerHeaders,
		extract: extractResponsesAnswer,
	},
	"kimi": {
		build: func(q, model string, _ executor.Credentials) map[string]any {
			return map[string]any{
				"model":    model,
				"messages": []any{map[string]any{"role": "user", "content": q}},
				"tools": []any{map[string]any{"type": "builtin_function",
					"function": map[string]any{"name": "$web_search"}}},
			}
		},
		headers: bearerHeaders,
		extract: func(data map[string]any) (string, []map[string]any) {
			msg := firstChoiceMessage(data)
			text, _ := msg["content"].(string)
			var cites []map[string]any
			for _, call := range anyList(msg["tool_calls"]) {
				parsed := parseToolArgs(call)
				for _, it := range firstList(parsed, "search_results", "results", "references") {
					if c := chatCitation(it); c != nil {
						cites = append(cites, c)
					}
				}
			}
			return text, cites
		},
	},
	"minimax": {
		build: func(q, model string, _ executor.Credentials) map[string]any {
			return map[string]any{
				"model":    model,
				"messages": []any{map[string]any{"role": "user", "content": q}},
				"tools":    []any{map[string]any{"type": "web_search"}},
			}
		},
		headers: bearerHeaders,
		extract: func(data map[string]any) (string, []map[string]any) {
			msg := firstChoiceMessage(data)
			text, _ := msg["content"].(string)
			var cites []map[string]any
			for _, it := range anyList(data["web_search_results"]) {
				if c := chatCitation(it); c != nil {
					cites = append(cites, c)
				}
			}
			if len(cites) == 0 {
				for _, call := range anyList(msg["tool_calls"]) {
					parsed := parseToolArgs(call)
					for _, it := range firstList(parsed, "results", "search_results") {
						if c := chatCitation(it); c != nil {
							cites = append(cites, c)
						}
					}
				}
			}
			return text, cites
		},
	},
	"perplexity": {
		build: func(q, model string, _ executor.Credentials) map[string]any {
			return map[string]any{
				"model":    model,
				"messages": []any{map[string]any{"role": "user", "content": q}},
			}
		},
		headers: bearerHeaders,
		extract: func(data map[string]any) (string, []map[string]any) {
			msg := firstChoiceMessage(data)
			text, _ := msg["content"].(string)
			var cites []map[string]any
			for _, c := range anyList(data["citations"]) {
				if cc := normalizeChatCitation(c); cc != nil {
					cites = append(cites, cc)
				}
			}
			return text, cites
		},
	},
}

// openAIStyleChat covers OpenAI and any OpenAI-compatible gateway
// (vercel-ai-gateway): plain chat completions, web_search tool added for any
// model that is not itself a search model.
func openAIStyleChat() chatSearchCfg {
	return chatSearchCfg{
		build: func(q, model string, _ executor.Credentials) map[string]any {
			b := map[string]any{
				"model":    model,
				"messages": []any{map[string]any{"role": "user", "content": q}},
			}
			if !strings.Contains(strings.ToLower(model), "search") {
				b["tools"] = []any{map[string]any{"type": "web_search"}}
			}
			return b
		},
		headers: bearerHeaders,
		extract: func(data map[string]any) (string, []map[string]any) {
			msg := firstChoiceMessage(data)
			text, _ := msg["content"].(string)
			var cites []map[string]any
			for _, a := range anyList(msg["annotations"]) {
				am, _ := a.(map[string]any)
				uc, _ := am["url_citation"].(map[string]any)
				if uc != nil {
					if c := chatCitation(uc); c != nil {
						cites = append(cites, c)
					}
				}
			}
			if len(cites) == 0 {
				for _, c := range anyList(data["citations"]) {
					if cc := normalizeChatCitation(c); cc != nil {
						cites = append(cites, cc)
					}
				}
			}
			return text, cites
		},
	}
}

func bearerHeaders(c executor.Credentials) map[string]string {
	return map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + chatSearchToken(c)}
}

func chatSearchToken(c executor.Credentials) string {
	if c.APIKey != "" {
		return c.APIKey
	}
	return c.AccessToken
}

// chatSearchEndpoint resolves the registry searchViaChat endpoint, falling back
// to the provider's transport base URL (openai/vercel-ai-gateway declare none).
func (g *Gateway) chatSearchEndpoint(provider, model string) string {
	if sc := g.mediaConfig(provider, "searchViaChat"); sc != nil {
		if ep := cfgString(sc, "endpoint"); ep != "" {
			return strings.ReplaceAll(ep, "{model}", model)
		}
	}
	if t := g.reg.TransportFor(provider); t != nil {
		return strings.TrimSpace(t.BaseURL)
	}
	return ""
}

// searchViaChat runs one LLM-grounded search and normalizes the answer.
func (g *Gateway) searchViaChat(r *http.Request, provider string, p searchParams) (int, []byte, error) {
	cfgFn, ok := chatSearchCfgs[provider]
	if !ok {
		return http.StatusBadRequest, nil, fmt.Errorf("Unsupported chat-search provider: %s", provider)
	}
	sc := g.mediaConfig(provider, "searchViaChat")
	model, _ := sc["defaultModel"].(string)
	if model == "" {
		return http.StatusBadRequest, nil, fmt.Errorf("Provider %s has no default search model", provider)
	}
	conns := g.searchConnections(provider)
	conn := g.pickConnection(conns, provider, map[string]bool{})
	if conn == nil {
		return http.StatusUnauthorized, nil, fmt.Errorf("No credentials for provider: %s", provider)
	}
	creds := executor.CredentialsFromConnection(conn)
	if chatSearchToken(creds) == "" {
		return http.StatusUnauthorized, nil, fmt.Errorf("Missing credentials (apiKey or accessToken)")
	}
	if provider == "antigravity" && creds.ProjectID == "" {
		return http.StatusUnauthorized, nil, fmt.Errorf("Antigravity account has no projectId — reconnect the account")
	}
	url := g.chatSearchEndpoint(provider, model)
	if url == "" {
		return http.StatusBadRequest, nil, fmt.Errorf("Provider %s has no search endpoint", provider)
	}

	payload := mustJSON(cfgFn.build(p.query, model, creds))
	ctx, cancel := context.WithTimeout(r.Context(), chatSearchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return http.StatusBadRequest, nil, err
	}
	for k, v := range cfgFn.headers(creds) {
		req.Header.Set(k, v)
	}
	start := time.Now()
	client := mediaClient
	if creds.ProxyURL != "" {
		client = executor.ClientWithProxy(creds.ProxyURL)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return http.StatusGatewayTimeout, nil, fmt.Errorf("Upstream timeout")
		}
		return http.StatusBadGateway, nil, fmt.Errorf("Network error: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	latency := time.Since(start).Milliseconds()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil, fmt.Errorf("%s returned %d: %.200s", provider, resp.StatusCode, raw)
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return http.StatusBadGateway, nil, fmt.Errorf("Invalid upstream response (status %d)", resp.StatusCode)
	}

	text, citations := cfgFn.extract(data)
	limit := p.maxResults
	if limit <= 0 {
		limit = 10
	}
	if len(citations) > limit {
		citations = citations[:limit]
	}
	results := make([]any, 0, len(citations))
	for i, c := range citations {
		results = append(results, chatSearchResult(provider, c, i))
	}
	out := mustJSON(map[string]any{
		"provider": provider,
		"query":    p.query,
		"results":  results,
		"answer":   map[string]any{"source": provider, "text": text, "model": model},
		"usage":    map[string]any{"queries_used": 1, "search_cost_usd": 0, "llm_tokens": chatSearchTokens(data)},
		"metrics": map[string]any{"response_time_ms": latency, "upstream_latency_ms": latency,
			"total_results_available": nil},
		"errors": []any{},
	})
	return http.StatusOK, out, nil
}

func chatSearchResult(provider string, c map[string]any, i int) map[string]any {
	u, _ := c["url"].(string)
	title, _ := c["title"].(string)
	snippet, _ := c["snippet"].(string)
	content, _ := c["content"].(string)
	var contentAny any
	if content != "" {
		contentAny = content
	}
	return map[string]any{
		"title":        title,
		"url":          u,
		"display_url":  displayURL(u),
		"snippet":      snippet,
		"position":     i + 1,
		"score":        nil,
		"published_at": nil,
		"favicon_url":  nil,
		"content":      contentAny,
		"metadata":     map[string]any{},
		"citation": map[string]any{"provider": provider, "rank": i + 1,
			"retrieved_at": time.Now().UTC().Format(time.RFC3339)},
		"provider_raw": nil,
	}
}

// ------------------------------------------------------------------ parsing

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}

func firstList(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if l := anyList(m[k]); len(l) > 0 {
			return l
		}
	}
	return nil
}

func firstChoiceMessage(data map[string]any) map[string]any {
	choices := anyList(data["choices"])
	if len(choices) == 0 {
		return map[string]any{}
	}
	c, _ := choices[0].(map[string]any)
	msg, _ := c["message"].(map[string]any)
	if msg == nil {
		return map[string]any{}
	}
	return msg
}

func parseToolArgs(call any) map[string]any {
	cm, _ := call.(map[string]any)
	fn, _ := cm["function"].(map[string]any)
	args := fn["arguments"]
	var parsed map[string]any
	switch a := args.(type) {
	case string:
		if json.Unmarshal([]byte(a), &parsed) != nil {
			return nil
		}
	case map[string]any:
		parsed = a
	}
	return parsed
}

// chatCitation coerces one item into {url,title,snippet}.
func chatCitation(it any) map[string]any {
	im, _ := it.(map[string]any)
	if im == nil {
		return nil
	}
	url := firstStr(im, "url", "link")
	if url == "" {
		return nil
	}
	return map[string]any{"url": url, "title": firstStr(im, "title"),
		"snippet": firstStr(im, "snippet", "summary")}
}

// normalizeChatCitation accepts either a bare URL string or {url,...} object.
func normalizeChatCitation(c any) map[string]any {
	switch v := c.(type) {
	case string:
		if v == "" {
			return nil
		}
		return map[string]any{"url": v, "title": "", "snippet": ""}
	case map[string]any:
		url, _ := v["url"].(string)
		if url == "" {
			return nil
		}
		title, _ := v["title"].(string)
		return map[string]any{"url": url, "title": title, "snippet": ""}
	}
	return nil
}

// extractGeminiAnswer reads candidates[0] text + groundingChunks.
func extractGeminiAnswer(data map[string]any) (string, []map[string]any) {
	cands := anyList(data["candidates"])
	if len(cands) == 0 {
		return "", nil
	}
	cand, _ := cands[0].(map[string]any)
	content, _ := cand["content"].(map[string]any)
	var text strings.Builder
	for _, p := range anyList(content["parts"]) {
		pm, _ := p.(map[string]any)
		s, _ := pm["text"].(string)
		text.WriteString(s)
	}
	grounding, _ := cand["groundingMetadata"].(map[string]any)
	var cites []map[string]any
	seen := map[string]bool{}
	for _, ch := range anyList(grounding["groundingChunks"]) {
		chm, _ := ch.(map[string]any)
		web, _ := chm["web"].(map[string]any)
		if web == nil {
			continue
		}
		url := firstStr(web, "uri", "url")
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		cites = append(cites, map[string]any{
			"url": url, "title": firstStr(web, "title"),
			"snippet": antigravityCitationSnippet(grounding, text.String(), len(cites)),
		})
	}
	return text.String(), cites
}

// extractResponsesAnswer reads a Responses-API payload (xai, perplexity-agent).
func extractResponsesAnswer(data map[string]any) (string, []map[string]any) {
	var text strings.Builder
	var cites []map[string]any
	for _, item := range anyList(data["output"]) {
		im, _ := item.(map[string]any)
		for _, p := range anyList(im["content"]) {
			pm, _ := p.(map[string]any)
			if s, ok := pm["text"].(string); ok {
				text.WriteString(s)
			}
			for _, a := range anyList(pm["annotations"]) {
				am, _ := a.(map[string]any)
				if am == nil {
					continue
				}
				var c map[string]any
				if _, ok := am["url"]; ok {
					c = normalizeChatCitation(am)
				} else if uc, ok := am["url_citation"].(map[string]any); ok {
					c = chatCitation(uc)
				}
				if c != nil {
					cites = append(cites, c)
				}
			}
		}
		for _, r := range anyList(im["results"]) {
			if c := chatCitation(r); c != nil {
				cites = append(cites, c)
			}
		}
	}
	if len(cites) == 0 {
		for _, c := range anyList(data["citations"]) {
			if cc := normalizeChatCitation(c); cc != nil {
				cites = append(cites, cc)
			}
		}
	}
	return text.String(), cites
}

func chatSearchTokens(data map[string]any) int64 {
	if n, ok := num64(data["usage"].(map[string]any)["total_tokens"]); ok {
		return n
	}
	if n, ok := num64(data["usageMetadata"].(map[string]any)["totalTokenCount"]); ok {
		return n
	}
	return 0
}

// antigravityCitationSnippet ties a chunk back to the answer sentence that
// grounded it (groundingSupports), widened to the surrounding text.
func antigravityCitationSnippet(grounding map[string]any, answer string, idx int) string {
	for _, s := range anyList(grounding["groundingSupports"]) {
		sm, _ := s.(map[string]any)
		indices := anyList(sm["groundingChunkIndices"])
		hit := false
		for _, i := range indices {
			if n, ok := num64(i); ok && int(n) == idx {
				hit = true
			}
		}
		if !hit {
			continue
		}
		seg, _ := sm["segment"].(map[string]any)
		if expanded := expandSegment(answer, seg); expanded != "" {
			return expanded
		}
		if t, ok := seg["text"].(string); ok {
			return t
		}
	}
	return ""
}

// expandSegment widens a grounding segment to its surrounding sentence(s).
func expandSegment(text string, seg map[string]any) string {
	if text == "" || seg == nil {
		return ""
	}
	start64, ok1 := num64(seg["startIndex"])
	end64, ok2 := num64(seg["endIndex"])
	if !ok1 || !ok2 {
		return ""
	}
	start := int(start64) - 150
	if start < 0 {
		start = 0
	}
	end := int(end64) + 250
	if end > len(text) {
		end = len(text)
	}
	out := strings.TrimSpace(text[start:end])
	if start > 0 {
		// Drop the partial word the window cut off.
		if i := strings.IndexAny(out, " \t\n"); i >= 0 {
			out = "..." + out[i:]
		}
	}
	if end < len(text) {
		if i := strings.LastIndexAny(out, " \t\n"); i >= 0 {
			out = out[:i] + "..."
		}
	}
	return strings.TrimSpace(out)
}
