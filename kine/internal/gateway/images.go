package gateway

// POST /v1/images/generations — OpenAI-compatible image generation.
// Mirrors open-sse/handlers/imageGenerationCore.js with the two adapter families
// that need no bespoke polling/scrape logic: OpenAI-compatible and Gemini.
// The non-OpenAI adapters live in imageadapters.go; the executor-driven web
// providers (uc-web, veoaifree-web) are served by webmedia.go.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// openAIImageProviders speaks the OpenAI images/generations body directly.
var openAIImageProviders = map[string]bool{
	"openai": true, "minimax": true, "openrouter": true, "recraft": true,
	"vercel-ai-gateway": true, "xai": true, "sensenova": true,
	"sensenova-intl": true, "tokenrouter": true, "venice": true,
}

func (g *Gateway) handleImages(w http.ResponseWriter, r *http.Request) {
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
	if p, _ := body["prompt"].(string); strings.TrimSpace(p) == "" {
		writeErr(w, http.StatusBadRequest, "Missing required field: prompt")
		return
	}

	if _, ok := g.authorizeRequest(w, r, modelStr); !ok {
		return
	}
	targets, err := g.resolveTargets(modelStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	t := targets[0]
	if webMediaGenerators[t.Provider] != nil && g.mediaConfig(t.Provider, "videoConfig") != nil {
		// Executor-driven web provider: no imageConfig, the site API is used.
		g.handleWebImage(w, r, t.Provider, t.Model, body)
		return
	}
	cfg := g.mediaConfig(t.Provider, "imageConfig")
	base, _ := cfg["baseUrl"].(string)
	if base == "" {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' does not support image generation", t.Provider))
		return
	}
	gemini := t.Provider == "gemini"
	adapter := imageAdapters[t.Provider] != nil
	if !gemini && !adapter && !openAIImageProviders[t.Provider] {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' image adapter is not implemented", t.Provider))
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
		var status int
		var respBody []byte
		var err error
		if adapter {
			status, respBody, err = g.imageAdapterOnce(r, conn, t, body, cfg)
			if err != nil && status == 0 {
				status = http.StatusBadGateway
				respBody = mustJSON(map[string]any{"error": map[string]any{"message": err.Error()}})
			}
		} else {
			status, respBody, err = g.imageOnce(r, conn, t, cfg, body, gemini)
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

// authorizeRequest applies the requireApiKey setting and per-key model ACL,
// returning the presented key.
func (g *Gateway) authorizeRequest(w http.ResponseWriter, r *http.Request, model string) (string, bool) {
	apiKey := extractAPIKey(r)
	settings, err := g.store.GetSettings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "settings unavailable")
		return "", false
	}
	require, _ := settings["requireApiKey"].(bool)
	if !require {
		return apiKey, true
	}
	if apiKey == "" {
		writeErr(w, http.StatusUnauthorized, "Missing API key")
		return "", false
	}
	ok, key, err := g.store.ValidateAPIKey(apiKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "key lookup failed")
		return "", false
	}
	if !ok {
		writeErr(w, http.StatusUnauthorized, "Invalid API key")
		return "", false
	}
	if !g.keyAllows(key, model) {
		writeErr(w, http.StatusForbidden, "API key is not allowed to use this model")
		return "", false
	}
	return apiKey, true
}

// imageOnce issues one provider request and normalizes Gemini's shape.
func (g *Gateway) imageOnce(r *http.Request, conn *store.Connection, t target, cfg, body map[string]any, gemini bool) (int, []byte, error) {
	creds := executor.CredentialsFromConnection(conn)
	base, _ := cfg["baseUrl"].(string)

	var payload []byte
	url := base
	if gemini {
		payload = mustJSON(geminiImageBody(body))
		model := strings.TrimPrefix(t.Model, "models/")
		url = strings.TrimRight(base, "/") + "/" + model + ":generateContent?key=" +
			urlQueryEscape(creds.APIKey+creds.AccessToken)
	} else {
		payload = mustJSON(openAIImageBody(t.Model, body, cfg))
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
		return http.StatusBadGateway, mustJSON(map[string]any{"error": map[string]any{"message": err.Error()}}), err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.Status < 200 || resp.Status >= 300 {
		return resp.Status, out, fmt.Errorf("upstream %d", resp.Status)
	}
	if gemini {
		out = geminiImageToOpenAI(out, body["prompt"])
	}
	return resp.Status, out, nil
}

// openAIImageBody applies the adapter's bodyFields whitelist.
func openAIImageBody(model string, body, cfg map[string]any) map[string]any {
	full := map[string]any{"model": model, "prompt": body["prompt"]}
	for _, k := range []string{"n", "size", "quality", "style", "response_format", "output_format", "watermark", "prompt_extend"} {
		if v, ok := body[k]; ok {
			full[k] = v
		}
	}
	fields, _ := cfg["bodyFields"].([]any)
	if fields == nil {
		return full
	}
	out := map[string]any{}
	for _, f := range fields {
		if s, ok := f.(string); ok {
			if v, has := full[s]; has {
				out[s] = v
			}
		}
	}
	return out
}

// geminiImageBody builds generateContent with IMAGE modality and an optional
// inline input image.
func geminiImageBody(body map[string]any) map[string]any {
	parts := []any{map[string]any{"text": body["prompt"]}}
	in := body["image"]
	if in == nil {
		if list, ok := body["images"].([]any); ok && len(list) > 0 {
			in = list[0]
		}
	}
	if s, ok := in.(string); ok && s != "" {
		if inline := dataURIInline(s); inline != nil {
			parts = append([]any{inline}, parts...)
		}
	}
	return map[string]any{
		"contents":         []any{map[string]any{"parts": parts}},
		"generationConfig": map[string]any{"responseModalities": []any{"TEXT", "IMAGE"}},
	}
}

// dataURIInline turns a data: URI (or bare base64) into a Gemini inlineData part.
func dataURIInline(s string) map[string]any {
	if strings.HasPrefix(s, "data:image/") {
		rest := strings.TrimPrefix(s, "data:")
		i := strings.Index(rest, ";base64,")
		if i < 0 {
			return nil
		}
		return map[string]any{"inlineData": map[string]any{"mimeType": rest[:i], "data": rest[i+8:]}}
	}
	if !strings.HasPrefix(s, "http") && len(s) > 100 {
		return map[string]any{"inlineData": map[string]any{"mimeType": "image/png", "data": s}}
	}
	return nil
}

// geminiImageToOpenAI collects inlineData image parts into the OpenAI shape.
func geminiImageToOpenAI(raw []byte, prompt any) []byte {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	var images []any
	cands, _ := m["candidates"].([]any)
	if len(cands) > 0 {
		c := cands[0].(map[string]any)
		content, _ := c["content"].(map[string]any)
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			inline, _ := pm["inlineData"].(map[string]any)
			if d, ok := inline["data"].(string); ok && d != "" {
				images = append(images, map[string]any{"b64_json": d})
			}
		}
	}
	if len(images) == 0 {
		images = []any{map[string]any{"b64_json": "", "revised_prompt": prompt}}
	}
	return mustJSON(map[string]any{"created": time.Now().Unix(), "data": images})
}

// mediaConfig returns one providerMedia sub-object (imageConfig, ttsConfig, ...).
func (g *Gateway) mediaConfig(provider, kind string) map[string]any {
	pm := g.reg.MediaFor(provider)
	if pm == nil {
		return nil
	}
	m, _ := pm[kind].(map[string]any)
	return m
}
