package gateway

// Provider nodes: the dashboard's custom-endpoint registry, ported from
// src/app/api/provider-nodes/{route.js,[id]/route.js,validate/route.js}.
//
// Security:
//   - Node base URLs are stored server-side and later dialed by the executor, so
//     every write is validated as an http(s) URL. `validate` additionally probes
//     a client-supplied URL: a non-local caller gets the netguard SSRF check,
//     matching the JS (which exempts loopback so ollama-local can be tested).
//   - Node ids are generated here; a client cannot choose one.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	nodePrefixOpenAI    = "openai-compatible-"
	nodePrefixAnthropic = "anthropic-compatible-"
	nodePrefixEmbedding = "custom-embedding-"
)

// isNodeProvider reports whether a provider id names a custom node.
func isNodeProvider(id string) bool {
	return strings.HasPrefix(id, nodePrefixOpenAI) ||
		strings.HasPrefix(id, nodePrefixAnthropic) ||
		strings.HasPrefix(id, nodePrefixEmbedding)
}

// nodeSpecific returns the providerSpecificData a connection must carry to use
// a node, matching the compatible branches of POST /api/providers.
func nodeSpecific(n *store.ProviderNode) map[string]any {
	out := map[string]any{"prefix": n.Data["prefix"], "baseUrl": n.Data["baseUrl"], "nodeName": n.Name}
	if n.Type == "openai-compatible" {
		out["apiType"] = n.Data["apiType"]
	}
	return out
}

func (g *Gateway) registerProviderNodeRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/provider-nodes", r(g.handleListProviderNodes))
	mux.HandleFunc("POST /api/provider-nodes", r(g.handleCreateProviderNode))
	mux.HandleFunc("PUT /api/provider-nodes/{id}", r(g.handleUpdateProviderNode))
	mux.HandleFunc("DELETE /api/provider-nodes/{id}", r(g.handleDeleteProviderNode))
	mux.HandleFunc("POST /api/provider-nodes/validate", r(g.handleValidateProviderNode))
}

func (g *Gateway) handleListProviderNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := g.store.ListProviderNodes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch provider nodes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
}

// sanitizeNodeBaseURL strips a trailing slash and, for anthropic/embedding
// nodes, the endpoint the user pasted so the runtime does not double-append it.
func sanitizeNodeBaseURL(nodeType, raw string) string {
	s := strings.TrimRight(strings.TrimSpace(raw), "/")
	switch nodeType {
	case "anthropic-compatible":
		return strings.TrimSuffix(s, "/messages")
	case "custom-embedding":
		return strings.TrimSuffix(s, "/embeddings")
	}
	return s
}

func (g *Gateway) handleCreateProviderNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Prefix  string `json:"prefix"`
		APIType string `json:"apiType"`
		BaseURL string `json:"baseUrl"`
		Type    string `json:"type"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "Name is required")
		return
	}
	if strings.TrimSpace(body.Prefix) == "" {
		writeErr(w, http.StatusBadRequest, "Prefix is required")
		return
	}
	nodeType := firstNonEmpty(body.Type, "openai-compatible")

	var id, defURL string
	switch nodeType {
	case "openai-compatible":
		if body.APIType != "chat" && body.APIType != "responses" {
			writeErr(w, http.StatusBadRequest, "Invalid OpenAI compatible API type")
			return
		}
		id = nodePrefixOpenAI + body.APIType + "-" + randomUUID()
		defURL = "https://api.openai.com/v1"
	case "anthropic-compatible":
		id = nodePrefixAnthropic + randomUUID()
		defURL = "https://api.anthropic.com/v1"
	case "custom-embedding":
		id = nodePrefixEmbedding + randomUUID()
		defURL = "https://api.openai.com/v1"
	default:
		writeErr(w, http.StatusBadRequest, "Invalid provider node type")
		return
	}
	baseURL := sanitizeNodeBaseURL(nodeType, firstNonEmpty(body.BaseURL, defURL))
	if err := checkPublicURL(baseURL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	data := map[string]any{"prefix": strings.TrimSpace(body.Prefix), "baseUrl": baseURL}
	if nodeType == "openai-compatible" {
		data["apiType"] = body.APIType
	}
	n := &store.ProviderNode{ID: id, Type: nodeType, Name: strings.TrimSpace(body.Name), Data: data}
	if err := g.store.SaveProviderNode(n); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to create provider node")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"node": n})
}

func (g *Gateway) handleUpdateProviderNode(w http.ResponseWriter, r *http.Request) {
	n, err := g.store.GetProviderNode(r.PathValue("id"))
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "Provider node not found")
		return
	}
	var body struct {
		Name    string `json:"name"`
		Prefix  string `json:"prefix"`
		APIType string `json:"apiType"`
		BaseURL string `json:"baseUrl"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "Name is required")
		return
	}
	if strings.TrimSpace(body.Prefix) == "" {
		writeErr(w, http.StatusBadRequest, "Prefix is required")
		return
	}
	if n.Type == "openai-compatible" && body.APIType != "chat" && body.APIType != "responses" {
		writeErr(w, http.StatusBadRequest, "Invalid OpenAI compatible API type")
		return
	}
	if strings.TrimSpace(body.BaseURL) == "" {
		writeErr(w, http.StatusBadRequest, "Base URL is required")
		return
	}
	baseURL := sanitizeNodeBaseURL(n.Type, body.BaseURL)
	if err := checkPublicURL(baseURL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n.Name = strings.TrimSpace(body.Name)
	if n.Data == nil {
		n.Data = map[string]any{}
	}
	n.Data["prefix"] = strings.TrimSpace(body.Prefix)
	n.Data["baseUrl"] = baseURL
	if n.Type == "openai-compatible" {
		n.Data["apiType"] = body.APIType
	}
	if err := g.store.SaveProviderNode(n); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to update provider node")
		return
	}
	// Push the node's endpoint into every connection that uses it.
	conns, _ := g.store.ListConnections(n.ID, false)
	for _, c := range conns {
		sp := map[string]any{}
		for k, v := range c.Specific() {
			sp[k] = v
		}
		sp["prefix"] = n.Data["prefix"]
		sp["baseUrl"] = baseURL
		sp["nodeName"] = n.Name
		if n.Type == "openai-compatible" {
			sp["apiType"] = body.APIType
		}
		_, _ = g.store.PatchConnection(c.ID, map[string]any{"providerSpecificData": sp})
	}
	writeJSON(w, http.StatusOK, map[string]any{"node": n})
}

func (g *Gateway) handleDeleteProviderNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := g.store.GetProviderNode(id)
	if err != nil || n == nil {
		writeErr(w, http.StatusNotFound, "Provider node not found")
		return
	}
	if _, err := g.store.DeleteProviderNode(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to delete provider node")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleValidateProviderNode probes a client-supplied endpoint. See the file
// header: non-local callers are SSRF-checked, loopback callers are not.
//
// ponytail: no per-provider special-casing here (the JS route has none either);
// the three node types below cover every shape the dashboard can create.
func (g *Gateway) handleValidateProviderNode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
		Type    string `json:"type"`
		APIType string `json:"apiType"`
		ModelID string `json:"modelId"`
	}
	if decodeBody(r, &body) != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	baseURL := strings.TrimSpace(body.BaseURL)
	if baseURL == "" || body.APIKey == "" {
		writeErr(w, http.StatusBadRequest, "Base URL and API key required")
		return
	}
	if u, err := neturl.Parse(baseURL); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		writeErr(w, http.StatusBadRequest, "Invalid URL format")
		return
	}
	if !isLocalRequest(r) {
		if err := checkPublicURL(baseURL); err != nil {
			writeErr(w, http.StatusBadRequest, "URL not allowed")
			return
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	modelID := strings.TrimSpace(body.ModelID)
	norm := strings.TrimRight(baseURL, "/")

	if body.Type == "custom-embedding" {
		if modelID == "" {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "Model ID required for embedding validation"})
			return
		}
		payload, _ := jsonMarshal(map[string]any{"model": modelID, "input": "ping"})
		embedURL := g.nodeEmbeddingURL("custom-embedding-x", map[string]any{"baseUrl": norm})
		status, raw, err := probeJSON(client, "POST", embedURL,
			map[string]string{"Authorization": "Bearer " + body.APIKey, "Content-Type": "application/json"}, payload)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": probeErrorText(err)})
			return
		}
		if status >= 200 && status < 300 {
			var parsed map[string]any
			_ = jsonUnmarshalStr(string(raw), &parsed)
			dims := 0
			if data, ok := parsed["data"].([]any); ok && len(data) > 0 {
				if m, ok := data[0].(map[string]any); ok {
					if emb, ok := m["embedding"].([]any); ok {
						dims = len(emb)
					}
				}
			}
			out := map[string]any{"valid": true, "method": "embeddings"}
			if dims > 0 {
				out["dimensions"] = dims
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		if status == 401 || status == 403 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "API key unauthorized"})
			return
		}
		msg := fmt.Sprintf("Embeddings request failed (%d)", status)
		if s := strings.TrimSpace(string(raw)); s != "" {
			msg += ": " + truncHead(s, 200)
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": msg, "method": "embeddings"})
		return
	}

	if body.Type == "anthropic-compatible" {
		norm = strings.TrimSuffix(norm, "/messages")
		hdr := map[string]string{
			"x-api-key": body.APIKey, "anthropic-version": "2023-06-01",
			"Authorization": "Bearer " + body.APIKey,
		}
		status, _, err := probeJSON(client, "GET", g.nodeModelsURL("anthropic-compatible-x", map[string]any{"baseUrl": norm}), hdr, nil)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": probeErrorText(err)})
			return
		}
		if status >= 200 && status < 300 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": true})
			return
		}
		if status == 401 || status == 403 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "API key unauthorized"})
			return
		}
		if modelID != "" {
			payload, _ := jsonMarshal(map[string]any{
				"model": modelID, "max_tokens": 1,
				"messages": []map[string]any{{"role": "user", "content": "ping"}},
			})
			hs := map[string]string{"Content-Type": "application/json"}
			for k, v := range hdr {
				hs[k] = v
			}
			chatURL, _ := g.nodeEndpoint("anthropic-compatible-x", map[string]any{"baseUrl": norm})
			st2, _, err := probeJSON(client, "POST", chatURL, hs, payload)
			if err != nil {
				writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": probeErrorText(err), "method": "chat"})
				return
			}
			if st2 >= 200 && st2 < 300 {
				writeJSON(w, http.StatusOK, map[string]any{"valid": true, "method": "chat"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": chatProbeError(st2), "method": "chat"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": modelsProbeError(status)})
		return
	}

	// OpenAI compatible (default). A 200 from GET /models is not enough: some
	// gateways expose /models publicly, so a chat probe with a real model
	// verifies the key before the node is accepted.
	sp := map[string]any{"baseUrl": norm}
	if body.APIType != "" {
		sp["apiType"] = body.APIType
	}
	provider := "openai-compatible-chat-x"
	if body.APIType == "responses" {
		provider = "openai-compatible-responses-x"
	}
	modelsURL := g.nodeModelsURL(provider, sp)
	status, raw, err := probeJSON(client, "GET", modelsURL, map[string]string{"Authorization": "Bearer " + body.APIKey}, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": probeErrorText(err)})
		return
	}
	if status == 401 || status == 403 {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "API key unauthorized"})
		return
	}
	effectiveModel := modelID
	if effectiveModel == "" && status >= 200 && status < 300 {
		var parsed struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if jsonUnmarshalStr(string(raw), &parsed) == nil && len(parsed.Data) > 0 {
			effectiveModel = parsed.Data[0].ID
		}
	}
	if effectiveModel != "" {
		payload, _ := jsonMarshal(map[string]any{
			"model": effectiveModel, "max_tokens": 1,
			"messages": []map[string]any{{"role": "user", "content": "ping"}},
		})
		chatURL, _ := g.nodeEndpoint(provider, sp)
		st2, _, err := probeJSON(client, "POST", chatURL,
			map[string]string{"Authorization": "Bearer " + body.APIKey, "Content-Type": "application/json"}, payload)
		if err != nil && status != http.StatusOK {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": probeErrorText(err), "method": "chat"})
			return
		}
		if st2 == 401 || st2 == 403 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": "API key unauthorized"})
			return
		}
		if st2 >= 200 && st2 < 300 {
			writeJSON(w, http.StatusOK, map[string]any{"valid": true, "method": "chat"})
			return
		}
		if status != http.StatusOK {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": chatProbeError(st2), "method": "chat"})
			return
		}
	}
	if status >= 200 && status < 300 {
		writeJSON(w, http.StatusOK, map[string]any{"valid": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": modelsProbeError(status)})
}

// isLocalRequest mirrors dashboardGuard.isLocalRequest: the peer is loopback,
// it did not arrive through a reverse proxy, and any Origin is loopback too.
func isLocalRequest(r *http.Request) bool {
	if r.Header.Get("x-kine-via-proxy") != "" {
		return false
	}
	if !isLoopback(clientHost(r)) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := neturl.Parse(origin)
		if err != nil || !isLoopback(u.Hostname()) {
			return false
		}
	}
	return true
}

func modelsProbeError(status int) string {
	switch {
	case status == 401 || status == 403:
		return "API key unauthorized"
	case status == 404:
		return "/models endpoint not found - try chat validation with model ID"
	case status >= 500:
		return "Server error - try again later"
	}
	return fmt.Sprintf("Unexpected response (%d)", status)
}

func chatProbeError(status int) string {
	switch {
	case status == 401 || status == 403:
		return "API key unauthorized"
	case status == 400:
		return "Invalid model or bad request"
	case status == 404:
		return "Chat endpoint not found"
	case status >= 500:
		return "Server error - try again later"
	}
	return fmt.Sprintf("Chat request failed (%d)", status)
}

// probeJSON issues one request and returns status + body. body nil = GET.
func probeJSON(client *http.Client, method, url string, headers map[string]string, body []byte) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, raw, nil
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// probeErrorText maps a transport error to the friendly message the JS uses.
func probeErrorText(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Connection refused - provider node offline or unreachable"
	case strings.Contains(msg, "no such host"):
		return "DNS lookup failed - invalid domain or network issue"
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "Client.Timeout"):
		return "Request timeout (>10s) - provider node not responding"
	case strings.Contains(msg, "certificate"):
		return "SSL certificate verification failed"
	}
	return "Network connection failed - check URL and network connectivity"
}
