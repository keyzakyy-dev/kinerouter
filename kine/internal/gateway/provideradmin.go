package gateway

// Provider administration routes: per-provider header overrides, live model
// listing, connection tests, and the client/validate/suggested-models probes.
// Ported from src/app/api/providers/{client,validate,test-batch,
// suggested-models,kilo/free-models, [id]/{models,overrides,test,test-models}}.
//
// Security:
//   - Override headers are validated at this boundary (name charset, length,
//     CR/LF, and a blocklist for host/authorization/cookie and other
//     request-structure headers), the same gates as the JS route.
//   - suggested-models fetches a client-supplied URL, so unlike the JS route it
//     runs checkPublicURL (SSRF) before dialing. The built-in catalogue hosts
//     are public and pass.
//   - Credentials never leave the server: the client view whitelists fields and
//     test results carry status/message only.
//
// ponytail: /api/providers/web-login is not ported — it drives a headless
// browser (Playwright) to log into provider web UIs, which needs a browser
// runtime this server does not ship. Add it with lazyRuntime if ever needed.
//
// ponytail: the ~20 per-provider model resolvers (claude/gemini/codex/cursor/
// kiro/...) are not ported; the registry's static catalogue plus node /models
// covers every model the router can actually serve. Add a resolver when a live
// list is needed for a specific provider.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func (g *Gateway) registerProviderAdminRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/providers/{id}/overrides", r(g.handleGetProviderOverrides))
	mux.HandleFunc("PUT /api/providers/{id}/overrides", r(g.handlePutProviderOverrides))
	mux.HandleFunc("GET /api/providers/{id}/models", r(g.handleProviderModels))
	mux.HandleFunc("POST /api/providers/{id}/test", r(g.handleProviderTest))
	mux.HandleFunc("POST /api/providers/{id}/test-models", r(g.handleProviderTestModels))
	mux.HandleFunc("POST /api/providers/validate", r(g.handleProviderValidate))
	mux.HandleFunc("POST /api/providers/test-batch", r(g.handleProviderTestBatch))
	mux.HandleFunc("GET /api/providers/suggested-models", r(g.handleSuggestedModels))
	mux.HandleFunc("GET /api/providers/kilo/free-models", r(g.handleKiloFreeModels))
}

// ---------------------------------------------------------------- overrides

const (
	maxOverrideHeaders     = 20
	maxOverrideHeaderValue = 8192
)

var (
	headerNameRE         = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	blockedOverrideNames = map[string]bool{
		"host": true, "content-length": true, "content-type": true, "connection": true,
		"transfer-encoding": true, "authorization": true, "cookie": true,
	}
)

// normalizeOverride validates a headers map. Returns the clean set and an
// error message ("" on success). An empty clean set means "delete".
func normalizeOverride(headers map[string]any) (map[string]string, string) {
	if len(headers) > maxOverrideHeaders {
		return nil, fmt.Sprintf("Too many headers (max %d)", maxOverrideHeaders)
	}
	clean := map[string]string{}
	for name, raw := range headers {
		if raw == nil || raw == "" {
			continue
		}
		if !headerNameRE.MatchString(name) {
			return nil, "Invalid header name: " + name
		}
		value, ok := raw.(string)
		if !ok || strings.ContainsAny(value, "\r\n") {
			return nil, "Invalid value for header " + name
		}
		if len(value) > maxOverrideHeaderValue {
			return nil, fmt.Sprintf("Header %s value too long (max %d)", name, maxOverrideHeaderValue)
		}
		if blockedOverrideNames[strings.ToLower(name)] {
			return nil, "Header " + name + " cannot be overridden"
		}
		clean[name] = value
	}
	return clean, ""
}

func providerOverridesSetting(settings map[string]any) map[string]any {
	m, _ := settings["providerOverrides"].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

// providerOverrideHeaders returns the stored user headers for a provider, ready
// to merge into Credentials.ExtraHeaders.
func (g *Gateway) providerOverrideHeaders(provider string) map[string]string {
	settings, err := g.store.GetSettings()
	if err != nil {
		return nil
	}
	o, _ := providerOverridesSetting(settings)[provider].(map[string]any)
	hs, _ := o["headers"].(map[string]any)
	if len(hs) == 0 {
		return nil
	}
	out := make(map[string]string, len(hs))
	for k, v := range hs {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func (g *Gateway) handleGetProviderOverrides(w http.ResponseWriter, r *http.Request) {
	canonical := g.reg.ResolveAlias(r.PathValue("id"))
	settings, err := g.store.GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to get overrides")
		return
	}
	o, _ := providerOverridesSetting(settings)[canonical].(map[string]any)
	headers, _ := o["headers"].(map[string]any)
	if headers == nil {
		headers = map[string]any{}
	}
	builtin := map[string]string{}
	if t := g.reg.TransportFor(canonical); t != nil && t.Headers != nil {
		builtin = t.Headers
	}
	writeJSON(w, http.StatusOK, map[string]any{"headers": headers, "builtinHeaders": builtin})
}

func (g *Gateway) handlePutProviderOverrides(w http.ResponseWriter, r *http.Request) {
	canonical := g.reg.ResolveAlias(r.PathValue("id"))
	var body struct {
		Headers map[string]any `json:"headers"`
	}
	_ = decodeBody(r, &body)
	clean, msg := normalizeOverride(body.Headers)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
		return
	}
	settings, err := g.store.GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to save overrides")
		return
	}
	next := map[string]any{}
	for k, v := range providerOverridesSetting(settings) {
		next[k] = v
	}
	if len(clean) == 0 {
		delete(next, canonical)
	} else {
		next[canonical] = map[string]any{"headers": clean}
	}
	if _, err := g.store.UpdateSettings(map[string]any{"providerOverrides": next}); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to save overrides")
		return
	}
	if clean == nil {
		clean = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"headers": clean})
}

// ---------------------------------------------------------------- models

// staticProviderModels is the registry catalogue for a provider id.
func (g *Gateway) staticProviderModels(provider string) []map[string]any {
	e := g.reg.ByID[provider]
	if e == nil {
		return nil
	}
	models := g.reg.Models[e.Alias]
	if len(models) == 0 {
		models = e.Models
	}
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, map[string]any{"id": m.ID, "name": name, "kind": m.Kind})
	}
	return out
}

// defaultModelFor picks the first catalogue model, mirroring getDefaultModel.
func (g *Gateway) defaultModelFor(provider string) string {
	if ms := g.staticProviderModels(provider); len(ms) > 0 {
		return ms[0]["id"].(string)
	}
	return ""
}

// listNodeModels fetches /models from a custom node.
func listNodeModels(base, apiKey, kind string) ([]any, int, error) {
	headers := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + apiKey}
	if kind == "anthropic-compatible" {
		headers["x-api-key"] = apiKey
		headers["anthropic-version"] = "2023-06-01"
	}
	client := &http.Client{Timeout: 15 * time.Second}
	status, raw, err := probeJSON(client, "GET", base+"/models", headers, nil)
	if err != nil {
		return nil, 0, err
	}
	if status < 200 || status >= 300 {
		return nil, status, fmt.Errorf("upstream %d", status)
	}
	var parsed map[string]any
	if json.Unmarshal(raw, &parsed) != nil {
		return nil, status, nil
	}
	if list, ok := parsed["data"].([]any); ok {
		return list, status, nil
	}
	if list, ok := parsed["models"].([]any); ok {
		return list, status, nil
	}
	return nil, status, nil
}

func (g *Gateway) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	conn, err := g.store.GetConnection(r.PathValue("id"))
	if err != nil || conn == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	sp := conn.Specific()
	if isNodeProvider(conn.Provider) {
		base := g.nodeBaseURL(conn.Provider, sp)
		if base == "" {
			writeErr(w, http.StatusBadRequest, "No base URL configured for this provider node")
			return
		}
		models, status, err := listNodeModels(base, conn.Str("apiKey"), conn.Provider)
		if err != nil {
			writeJSON(w, status, map[string]any{"error": fmt.Sprintf("Failed to fetch models: %d", status)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"provider": conn.Provider, "connectionId": conn.ID, "models": models,
		})
		return
	}
	models := g.staticProviderModels(g.reg.ResolveAlias(conn.Provider))
	if len(models) == 0 {
		writeErr(w, http.StatusBadRequest, "Provider "+conn.Provider+" does not support models listing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": conn.Provider, "connectionId": conn.ID, "models": models,
	})
}

// ---------------------------------------------------------------- connection test

// probeConnection checks one connection's credentials without spending quota
// where possible. Returns valid, message, and whether a token refresh ran.
func (g *Gateway) probeConnection(r *http.Request, conn *store.Connection) (bool, string, bool) {
	refreshed := false
	if conn.AuthType == "oauth" && conn.Str("refreshToken") != "" {
		if did, err := g.oauth.RefreshOne(r.Context(), g.store, conn, 2*time.Minute); err == nil && did {
			refreshed = true
			if fresh, _ := g.store.GetConnection(conn.ID); fresh != nil {
				conn = fresh
			}
		}
	}
	if isNodeProvider(conn.Provider) {
		base := g.nodeBaseURL(conn.Provider, conn.Specific())
		if base == "" {
			return false, "No base URL configured for this provider node", refreshed
		}
		client := &http.Client{Timeout: 15 * time.Second}
		headers := map[string]string{"Authorization": "Bearer " + conn.Str("apiKey")}
		if strings.HasPrefix(conn.Provider, nodePrefixAnthropic) {
			headers["x-api-key"] = conn.Str("apiKey")
			headers["anthropic-version"] = "2023-06-01"
		}
		status, _, err := probeJSON(client, "GET", strings.TrimSuffix(base, "/messages")+"/models", headers, nil)
		if err != nil {
			return false, probeErrorText(err), refreshed
		}
		valid := status >= 200 && status < 300
		if valid {
			return true, "", refreshed
		}
		return false, modelsProbeError(status), refreshed
	}
	model := g.defaultModelFor(g.reg.ResolveAlias(conn.Provider))
	if model == "" {
		return false, "No models configured for this provider", refreshed
	}
	kind := "llm"
	e := g.reg.ByID[g.reg.ResolveAlias(conn.Provider)]
	if e != nil {
		for _, m := range e.Models {
			if m.ID == model && m.Kind != "" {
				kind = m.Kind
				break
			}
		}
	}
	res, err := g.pingModel(r, e.Alias+"/"+model, kind)
	if err != nil {
		return false, err.Error(), refreshed
	}
	if ok, _ := res["ok"].(bool); ok {
		return true, "", refreshed
	}
	msg, _ := res["error"].(string)
	if msg == "" {
		msg = "Test failed"
	}
	return false, msg, refreshed
}

// saveTestStatus records the outcome on the connection.
func (g *Gateway) saveTestStatus(conn *store.Connection, valid bool, msg string) {
	patch := map[string]any{"testStatus": "error", "lastError": msg, "lastErrorAt": time.Now().UTC().Format(time.RFC3339)}
	if valid {
		patch = map[string]any{"testStatus": "active", "lastError": nil, "lastErrorAt": nil}
	}
	_, _ = g.store.PatchConnection(conn.ID, patch)
}

func (g *Gateway) handleProviderTest(w http.ResponseWriter, r *http.Request) {
	conn, err := g.store.GetConnection(r.PathValue("id"))
	if err != nil || conn == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	valid, msg, refreshed := g.probeConnection(r, conn)
	g.saveTestStatus(conn, valid, msg)
	out := map[string]any{"valid": valid, "refreshed": refreshed}
	if !valid {
		out["error"] = msg
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) handleProviderTestModels(w http.ResponseWriter, r *http.Request) {
	conn, err := g.store.GetConnection(r.PathValue("id"))
	if err != nil || conn == nil {
		writeErr(w, http.StatusNotFound, "Connection not found")
		return
	}
	alias := conn.Provider
	if e := g.reg.ByID[g.reg.ResolveAlias(conn.Provider)]; e != nil {
		alias = e.Alias
	}
	type modelEntry struct {
		id   string
		name string
		kind string
	}
	var models []modelEntry
	if isNodeProvider(conn.Provider) {
		base := g.nodeBaseURL(conn.Provider, conn.Specific())
		if base != "" {
			if list, _, err := listNodeModels(base, conn.Str("apiKey"), conn.Provider); err == nil {
				for _, item := range list {
					m, _ := item.(map[string]any)
					id := firstNonEmpty(rawString(m["id"]), rawString(m["name"]))
					if id != "" {
						models = append(models, modelEntry{id: id, name: firstNonEmpty(rawString(m["name"]), id), kind: "llm"})
					}
				}
			}
		}
	} else {
		for _, m := range g.staticProviderModels(alias) {
			id, _ := m["id"].(string)
			name, _ := m["name"].(string)
			kind, _ := m["kind"].(string)
			if kind == "" {
				kind = "llm"
			}
			models = append(models, modelEntry{id: id, name: name, kind: kind})
		}
	}
	if len(models) == 0 {
		writeErr(w, http.StatusBadRequest, "No models configured for this provider")
		return
	}
	// Sequential: the first call warms the credential (token refresh) so the
	// rest do not race the same refresh.
	results := make([]map[string]any, 0, len(models))
	for _, m := range models {
		res, _ := g.pingModel(r, alias+"/"+m.id, m.kind)
		entry := map[string]any{"modelId": m.id, "name": m.name}
		for k, v := range res {
			entry[k] = v
		}
		results = append(results, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": conn.Provider, "connectionId": conn.ID, "results": results,
	})
}

// authGroup mirrors getAuthGroup in the JS test-batch route.
func authGroup(provider string, conn *store.Connection, g *Gateway) string {
	if conn != nil && conn.AuthType != "" {
		if conn.AuthType == "oauth" {
			if e := g.reg.ByID[g.reg.ResolveAlias(provider)]; e != nil && e.Category == "free" {
				return "free"
			}
			return "oauth"
		}
		return conn.AuthType
	}
	if e := g.reg.ByID[g.reg.ResolveAlias(provider)]; e != nil {
		if e.Category == "free" {
			return "free"
		}
		if e.AuthType == "oauth" {
			return "oauth"
		}
		return "apikey"
	}
	if isNodeProvider(provider) {
		return "compatible"
	}
	return "apikey"
}

func (g *Gateway) handleProviderTestBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode       string `json:"mode"`
		ProviderID string `json:"providerId"`
	}
	if err := decodeBody(r, &body); err != nil || body.Mode == "" {
		writeErr(w, http.StatusBadRequest, "mode is required")
		return
	}
	conns, err := g.store.ListConnections("", true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Batch test failed")
		return
	}
	var picked []*store.Connection
	for _, c := range conns {
		match := false
		switch body.Mode {
		case "provider":
			match = body.ProviderID != "" && c.Provider == body.ProviderID
		case "oauth", "free", "apikey":
			match = authGroup(c.Provider, c, g) == body.Mode
		case "compatible":
			match = isNodeProvider(c.Provider)
		case "all":
			match = true
		default:
			writeErr(w, http.StatusBadRequest, "Invalid mode. Use: provider, oauth, free, apikey, compatible, all")
			return
		}
		if match {
			picked = append(picked, c)
		}
	}
	results := make([]map[string]any, 0, len(picked))
	passed := 0
	for _, c := range picked {
		start := time.Now()
		valid, msg, _ := g.probeConnection(r, c)
		if valid {
			passed++
		}
		entry := map[string]any{
			"provider": c.Provider, "connectionId": c.ID,
			"connectionName": firstNonEmpty(c.Name, c.Email, c.Provider),
			"authType":       firstNonEmpty(c.AuthType, authGroup(c.Provider, c, g)),
			"valid":          valid,
			"latencyMs":      time.Since(start).Milliseconds(),
			"testedAt":       time.Now().UTC().Format(time.RFC3339),
		}
		if !valid {
			entry["error"] = msg
		}
		results = append(results, entry)
	}
	var providerID any
	if body.ProviderID != "" {
		providerID = body.ProviderID
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode": body.Mode, "providerId": providerID, "results": results,
		"testedAt": time.Now().UTC().Format(time.RFC3339),
		"summary":  map[string]any{"total": len(results), "passed": passed, "failed": len(results) - passed},
	})
}

// ---------------------------------------------------------------- validate

// validateNodeProbe checks a custom node's key against its own endpoint.
func validateNodeProbe(node *store.ProviderNode, apiKey string) (bool, string) {
	base := strings.TrimRight(nodeStr(node, "baseUrl"), "/")
	client := &http.Client{Timeout: 10 * time.Second}
	switch {
	case strings.HasPrefix(node.ID, nodePrefixAnthropic):
		base = strings.TrimSuffix(base, "/messages")
		payload, _ := jsonMarshal(map[string]any{
			"model": "claude-3-haiku-20240307", "max_tokens": 1,
			"messages": []map[string]any{{"role": "user", "content": "test"}},
		})
		status, _, err := probeJSON(client, "POST", base+"/v1/messages", map[string]string{
			"x-api-key": apiKey, "anthropic-version": "2023-06-01",
			"content-type": "application/json", "Authorization": "Bearer " + apiKey,
		}, payload)
		if err != nil {
			return false, probeErrorText(err)
		}
		if status == 401 || status == 403 {
			return false, "Invalid API key"
		}
		return true, ""
	case strings.HasPrefix(node.ID, nodePrefixEmbedding):
		status, _, err := probeJSON(client, "GET", base+"/models",
			map[string]string{"Authorization": "Bearer " + apiKey}, nil)
		if err == nil && status >= 200 && status < 300 {
			return true, ""
		}
		if status == 401 || status == 403 {
			return false, "Invalid API key"
		}
		payload, _ := jsonMarshal(map[string]any{"model": "test", "input": "ping"})
		st2, _, err := probeJSON(client, "POST", base+"/embeddings", map[string]string{
			"Authorization": "Bearer " + apiKey, "Content-Type": "application/json",
		}, payload)
		if err != nil {
			return false, probeErrorText(err)
		}
		if st2 == 401 || st2 == 403 {
			return false, "Invalid API key"
		}
		return true, ""
	}
	status, _, err := probeJSON(client, "GET", base+"/models",
		map[string]string{"Authorization": "Bearer " + apiKey}, nil)
	if err != nil {
		return false, probeErrorText(err)
	}
	if status >= 200 && status < 300 {
		return true, ""
	}
	return false, "Invalid API key"
}

func (g *Gateway) handleProviderValidate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider         string         `json:"provider"`
		APIKey           string         `json:"apiKey"`
		ProviderSpecific map[string]any `json:"providerSpecificData"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Provider and API key required")
		return
	}
	provider := g.reg.ResolveAlias(strings.TrimSpace(body.Provider))
	entry := g.reg.ByID[provider]
	node := (*store.ProviderNode)(nil)
	if isNodeProvider(provider) {
		node, _ = g.store.GetProviderNode(provider)
	}
	noAuth := entry != nil && entry.NoAuth
	if provider == "" || (body.APIKey == "" && provider != "ollama-local" && !noAuth) {
		writeErr(w, http.StatusBadRequest, "Provider and API key required")
		return
	}
	if isNodeProvider(provider) {
		if node == nil {
			writeErr(w, http.StatusNotFound, "Provider node not found")
			return
		}
		valid, msg := validateNodeProbe(node, body.APIKey)
		out := map[string]any{"valid": valid}
		if !valid {
			out["error"] = msg
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	t := g.reg.TransportFor(provider)
	if entry == nil || t == nil || t.BaseURL == "" || (t.Format != "" && t.Format != "openai") {
		writeErr(w, http.StatusBadRequest, "Provider validation not supported")
		return
	}
	if noAuth || t.NoAuth {
		writeJSON(w, http.StatusOK, map[string]any{"valid": true})
		return
	}
	headers := map[string]string{"Content-Type": "application/json"}
	for k, v := range t.Headers {
		headers[k] = v
	}
	if t.Auth != nil && (strings.EqualFold(t.Auth.Header, "x-api-key") || strings.EqualFold(t.Auth.Header, "api-key")) {
		headers["X-API-Key"] = body.APIKey
		headers["api-key"] = body.APIKey
	} else {
		headers["Authorization"] = "Bearer " + body.APIKey
	}
	modelsURL := strings.TrimSuffix(t.BaseURL, "/chat/completions")
	modelsURL = strings.TrimSuffix(modelsURL, "/chatbot") + "/models"
	client := &http.Client{Timeout: 8 * time.Second}
	status, _, err := probeJSON(client, "GET", modelsURL, headers, nil)
	if err == nil {
		if status == 401 || status == 403 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "Invalid API key"})
			return
		}
		if status >= 200 && status < 300 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": true})
			return
		}
	}
	model := g.defaultModelFor(provider)
	if model == "" {
		model = "test"
	}
	payload, _ := jsonMarshal(map[string]any{
		"model": model, "max_tokens": 1,
		"messages": []map[string]any{{"role": "user", "content": "ping"}},
	})
	chatHeaders := map[string]string{}
	for k, v := range headers {
		chatHeaders[k] = v
	}
	chatHeaders["Content-Type"] = "application/json"
	st2, _, err := probeJSON(client, "POST", t.BaseURL, chatHeaders, payload)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": probeErrorText(err)})
		return
	}
	if st2 == 401 || st2 == 403 {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "Invalid API key"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

// ---------------------------------------------------------------- suggested-models

func suggestedFilter(kind string, list []any) []map[string]any {
	out := []map[string]any{}
	asNum := func(v any) float64 {
		f, _ := v.(float64)
		return f
	}
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m == nil {
			continue
		}
		id := rawString(m["id"])
		switch kind {
		case "openrouter-free":
			pricing, _ := m["pricing"].(map[string]any)
			ctx := asNum(m["context_length"])
			if rawString(pricing["prompt"]) == "0" && rawString(pricing["completion"]) == "0" && ctx >= 200000 {
				out = append(out, map[string]any{"id": id, "name": firstNonEmpty(rawString(m["name"]), id), "contextLength": int64(ctx)})
			}
		case "opencode-free":
			if id == "deepseek-v4-flash-free" {
				continue
			}
			if strings.HasSuffix(id, "-free") || id == "big-pickle" {
				out = append(out, map[string]any{"id": id, "name": id})
			}
		case "opencode-go":
			if id != "" {
				out = append(out, map[string]any{"id": id, "name": id})
			}
		case "mimo-free":
			name := rawString(m["name"])
			if strings.HasPrefix(id, "mimo") || strings.Contains(strings.ToLower(name), "mimo") {
				out = append(out, map[string]any{"id": id, "name": firstNonEmpty(name, id)})
			}
		case "minimax-code":
			providers, _ := m["providers"].([]any)
			for _, p := range providers {
				pm, _ := p.(map[string]any)
				if rawString(pm["providerId"]) != "minimax" {
					continue
				}
				cfg, _ := pm["config"].(map[string]any)
				models, _ := cfg["models"].(map[string]any)
				for mid, mv := range models {
					mm, _ := mv.(map[string]any)
					limit, _ := mm["limit"].(map[string]any)
					entry := map[string]any{"id": mid, "name": firstNonEmpty(rawString(mm["name"]), mid)}
					if c := int64(asNum(limit["context"])); c > 0 {
						entry["contextLength"] = c
					}
					out = append(out, entry)
				}
			}
		case "airforce-free":
			mediaType := rawString(m["media_type"])
			supportsChat, _ := m["supports_chat"].(bool)
			if (m["tier"] == "free" || strings.HasSuffix(id, ":free")) && supportsChat &&
				(mediaType == "" || mediaType == "chat" || mediaType == "text") {
				entry := map[string]any{"id": id, "name": firstNonEmpty(rawString(m["name"]), id)}
				if c := int64(asNum(m["context_length"])); c > 0 {
					entry["contextLength"] = c
				}
				out = append(out, entry)
			}
		case "nous-free":
			pricing, _ := m["pricing"].(map[string]any)
			ctx := asNum(m["context_length"])
			if rawString(pricing["prompt"]) == "0" && rawString(pricing["completion"]) == "0" && ctx >= 200000 {
				out = append(out, map[string]any{"id": id, "name": firstNonEmpty(rawString(m["name"]), id), "contextLength": int64(ctx)})
			}
		case "openai":
			out = append(out, map[string]any{"id": id, "name": firstNonEmpty(rawString(m["name"]), id)})
		}
	}
	return out
}

func (g *Gateway) handleSuggestedModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rawURL, kind := q.Get("url"), q.Get("type")
	if rawURL == "" || kind == "" {
		writeErr(w, http.StatusBadRequest, "Missing url or type")
		return
	}
	switch kind {
	case "openai", "openrouter-free", "opencode-free", "opencode-go", "mimo-free", "minimax-code", "airforce-free", "nous-free":
	default:
		writeErr(w, http.StatusBadRequest, "Unknown filter type")
		return
	}
	// Client-supplied URL: SSRF-check before dialing (stricter than the JS route).
	if err := checkPublicURL(rawURL); err != nil {
		writeErr(w, http.StatusBadRequest, "URL not allowed")
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get(rawURL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		return
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}})
		return
	}
	var parsed any
	_ = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&parsed)
	root, _ := parsed.(map[string]any)
	var raw any
	switch {
	case root["data"] != nil:
		raw = root["data"]
	case root["models"] != nil:
		raw = root["models"]
	default:
		raw = parsed
	}
	list, ok := raw.([]any)
	if !ok {
		if _, isObj := raw.(map[string]any); isObj {
			list = []any{raw}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": suggestedFilter(kind, list)})
}

// ---------------------------------------------------------------- kilo free models

const kiloModelsURL = "https://api.kilo.ai/api/gateway/models"

var (
	kiloCacheMu sync.Mutex
	kiloCache   []map[string]any
	kiloCacheAt time.Time
)

func (g *Gateway) handleKiloFreeModels(w http.ResponseWriter, r *http.Request) {
	kiloCacheMu.Lock()
	if kiloCache != nil && time.Since(kiloCacheAt) < time.Hour {
		cached := kiloCache
		kiloCacheMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"models": cached, "cached": true})
		return
	}
	kiloCacheMu.Unlock()

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", kiloModelsURL, nil)
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	var models []map[string]any
	if err == nil {
		defer res.Body.Close()
		if res.StatusCode >= 200 && res.StatusCode < 300 {
			var parsed map[string]any
			_ = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&parsed)
			all, _ := parsed["data"].([]any)
			models = []map[string]any{}
			for _, item := range all {
				m, _ := item.(map[string]any)
				if free, _ := m["isFree"].(bool); !free {
					continue
				}
				ctx := int64(0)
				if f, ok := m["context_length"].(float64); ok {
					ctx = int64(f)
				}
				models = append(models, map[string]any{
					"id": rawString(m["id"]), "name": rawString(m["name"]),
					"isFree": true, "context_length": ctx,
				})
			}
			kiloCacheMu.Lock()
			kiloCache, kiloCacheAt = models, time.Now()
			kiloCacheMu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{"models": models, "cached": false})
			return
		}
		err = fmt.Errorf("Kilo API returned %d", res.StatusCode)
	}
	kiloCacheMu.Lock()
	cached := kiloCache
	kiloCacheMu.Unlock()
	if cached != nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": cached, "cached": true, "warning": err.Error()})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"models": []any{}, "error": "Failed to fetch Kilo models: " + err.Error()})
}

// nodeStr reads one node data key as a string.
func nodeStr(n *store.ProviderNode, key string) string {
	if n == nil || n.Data == nil {
		return ""
	}
	s, _ := n.Data[key].(string)
	return s
}
