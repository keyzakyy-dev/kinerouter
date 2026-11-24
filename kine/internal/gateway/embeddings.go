package gateway

// POST /v1/embeddings — OpenAI-compatible embeddings. One upstream call, no
// format translation: the adapter is picked from the registry's embeddingConfig
// and only Gemini's native shape differs.
// Mirrors open-sse/handlers/embeddingsCore.js + sse/handlers/embeddings.js.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

type embeddingConfig struct {
	BaseURL    string
	AuthHeader string
	Format     string
}

func (g *Gateway) embeddingConfig(provider string) *embeddingConfig {
	cfg := g.mediaConfig(provider, "embeddingConfig")
	if cfg == nil {
		return nil
	}
	base, _ := cfg["baseUrl"].(string)
	if base == "" {
		return nil
	}
	auth, _ := cfg["authHeader"].(string)
	format, _ := cfg["format"].(string)
	return &embeddingConfig{BaseURL: base, AuthHeader: auth, Format: format}
}

func (g *Gateway) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	modelStr, _ := body["model"].(string)
	if modelStr == "" {
		writeErr(w, http.StatusBadRequest, "Missing model")
		return
	}
	input, ok := body["input"]
	if !ok || input == nil {
		writeErr(w, http.StatusBadRequest, "Missing required field: input")
		return
	}
	switch input.(type) {
	case string, []any:
	default:
		writeErr(w, http.StatusBadRequest, "input must be a string or array of strings")
		return
	}

	apiKey := extractAPIKey(r)
	settings, err := g.store.GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "settings unavailable")
		return
	}
	if require, _ := settings["requireApiKey"].(bool); require {
		if apiKey == "" {
			writeErr(w, http.StatusUnauthorized, "Missing API key")
			return
		}
		ok, key, err := g.store.ValidateAPIKey(apiKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "key lookup failed")
			return
		}
		if !ok {
			writeErr(w, http.StatusUnauthorized, "Invalid API key")
			return
		}
		if !g.keyAllows(key, modelStr) {
			writeErr(w, http.StatusForbidden, "API key is not allowed to use this model")
			return
		}
	}

	targets, err := g.resolveTargets(modelStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t := targets[0]
	cfg := g.embeddingConfig(t.Provider)
	// Custom nodes (custom-embedding-*, openai-compatible-*) carry their own
	// /embeddings endpoint (embeddingProviders/openaiCompatNode.js).
	if isNodeProvider(t.Provider) {
		cfg = &embeddingConfig{Format: "openai"}
	}
	if cfg == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' does not support embeddings.", t.Provider))
		return
	}

	conns, _ := g.store.ListConnections(t.Provider, true)
	tried := map[string]bool{}
	var lastStatus int
	var lastBody []byte
	for i := 0; i <= len(conns); i++ {
		conn := g.pickConnection(conns, t.Model, tried)
		if conn == nil {
			break
		}
		tried[conn.ID] = true
		start := time.Now()
		status, respBody, used, err := g.embedOnce(r, conn, t, cfg, body)
		if used.PromptTokens > 0 {
			g.recordUsage(conn, t, apiKey, "embeddings", used, start, nil, 0)
		}
		if err == nil && status >= 200 && status < 300 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(respBody)
			return
		}
		lastStatus, lastBody = status, respBody
		if status == http.StatusUnauthorized || status == http.StatusForbidden ||
			status == http.StatusTooManyRequests || status == http.StatusPaymentRequired {
			g.markUnavailable(conn, status, upstreamMessage(respBody), t.Model)
			continue
		}
		break
	}
	if lastStatus == 0 {
		writeErr(w, http.StatusServiceUnavailable, "No available credentials for this model")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(lastStatus)
	w.Write(lastBody)
}

// embedOnce sends one embeddings request and returns the upstream status/body.
func (g *Gateway) embedOnce(r *http.Request, conn *store.Connection, t target, cfg *embeddingConfig, body map[string]any) (int, []byte, translate.Usage, error) {
	creds := executor.CredentialsFromConnection(conn)
	reqBody := body
	if cfg.Format == "gemini" {
		reqBody = geminiEmbeddingBody(body)
	}
	reqBody["model"] = t.Model
	payload, _ := json.Marshal(reqBody)

	url := cfg.BaseURL
	if isNodeProvider(t.Provider) {
		url = g.nodeEmbeddingURL(t.Provider, conn.Specific())
	}
	if cfg.Format == "gemini" {
		op := "embedContent"
		if _, isList := body["input"].([]any); isList {
			op = "batchEmbedContents"
		}
		model := t.Model
		if !strings.HasPrefix(model, "models/") {
			model = "models/" + model
		}
		url = strings.TrimRight(cfg.BaseURL, "/") + "/" + model + ":" + op + "?key=" + urlQueryEscape(creds.APIKey+creds.AccessToken)
	}

	resp, err := g.exec.Execute(r.Context(), executor.Request{
		Provider:    t.Provider,
		Model:       t.Model,
		Format:      "openai",
		Body:        payload,
		Creds:       creds,
		URLOverride: url,
	})
	if err != nil {
		return http.StatusBadGateway, []byte(`{"error":{"message":"` + err.Error() + `"}}`), translate.Usage{}, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.Status < 200 || resp.Status >= 300 {
		return resp.Status, out, translate.Usage{}, fmt.Errorf("upstream %d", resp.Status)
	}
	if cfg.Format == "gemini" {
		out = geminiEmbeddingsToOpenAI(out, t.Model)
	}
	return resp.Status, out, embeddingUsage(out), nil
}

// geminiEmbeddingBody reshapes the OpenAI body for Gemini's embedContent API.
func geminiEmbeddingBody(body map[string]any) map[string]any {
	wrap := func(text any) map[string]any {
		m := map[string]any{"content": map[string]any{"parts": []any{map[string]any{"text": text}}}}
		if d, ok := body["dimensions"]; ok {
			if f, ok := d.(float64); ok && f > 0 {
				m["outputDimensionality"] = f
			}
		}
		return m
	}
	out := wrap(body["input"])
	if list, ok := body["input"].([]any); ok {
		reqs := make([]any, 0, len(list))
		for _, item := range list {
			reqs = append(reqs, wrap(item))
		}
		out = map[string]any{"requests": reqs}
	}
	for _, k := range []string{"encoding_format", "input", "dimensions"} {
		delete(out, k)
	}
	return out
}

// geminiEmbeddingsToOpenAI converts embedContent/batchEmbedContents to the
// OpenAI embeddings shape.
func geminiEmbeddingsToOpenAI(raw []byte, model string) []byte {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	var items []any
	if list, ok := m["embeddings"].([]any); ok {
		items = list
	} else if emb, ok := m["embedding"].(map[string]any); ok {
		items = []any{emb}
	} else {
		return raw
	}
	data := make([]any, 0, len(items))
	for i, item := range items {
		em, _ := item.(map[string]any)
		if em == nil {
			continue
		}
		data = append(data, map[string]any{
			"object": "embedding", "index": i, "embedding": em["values"],
		})
	}
	out := map[string]any{"object": "list", "data": data, "model": model}
	if u, ok := m["usageMetadata"].(map[string]any); ok {
		out["usage"] = map[string]any{"prompt_tokens": num(u["promptTokenCount"]), "total_tokens": num(u["totalTokenCount"])}
	}
	b, _ := json.Marshal(out)
	return b
}

// embeddingUsage reads only an exact usage block (prompt==total, no completion),
// mirroring exactEmbeddingUsage in the JS handler.
func embeddingUsage(body []byte) translate.Usage {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return translate.Usage{}
	}
	u, _ := m["usage"].(map[string]any)
	if u == nil {
		return translate.Usage{}
	}
	prompt := num(u["prompt_tokens"])
	total := num(u["total_tokens"])
	if prompt <= 0 || total != prompt || num(u["completion_tokens"]) != 0 {
		return translate.Usage{}
	}
	return translate.Usage{PromptTokens: prompt}
}

func urlQueryEscape(s string) string { return neturl.QueryEscape(s) }
