package gateway

// POST /v1/videos/{generations,edits,extensions} + GET /v1/videos/{id} — async
// video job proxy. The upstream body is forwarded byte-for-byte; only the
// provider prefix is stripped from `model`. Mirrors open-sse/handlers/videoCore.js
// and src/sse/handlers/videoGeneration.js.
// Executor-driven providers (videoConfig.executorDriven) are served by
// webmedia.go; grok-web (Chrome TLS fingerprint) and muse-web (headless
// browser) remain unsupported — ponytail: add when a browser runtime exists.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

var videoActions = map[string]bool{"generations": true, "edits": true, "extensions": true}

const defaultVideoProvider = "xai"

func (g *Gateway) handleVideoCreate(w http.ResponseWriter, r *http.Request, action string) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	contentType := r.Header.Get("Content-Type")

	var parsed map[string]any
	if strings.Contains(contentType, "application/json") {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			writeErr(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
	}

	provider, model := g.resolveVideoProvider(parsed)
	if provider == "" {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider does not support video generation"))
		return
	}
	if _, ok := g.authorizeRequest(w, r, provider); !ok {
		return
	}
	cfg := g.mediaConfig(provider, "videoConfig")
	if cfg == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' does not support video generation", provider))
		return
	}
	if driven, _ := cfg["executorDriven"].(bool); driven {
		// Synchronous site API: the artifact comes back inline, no job to poll.
		if action != "generations" && action != "edits" {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("[%s] supports 'generations' and 'edits' only (got '%s')", provider, action))
			return
		}
		g.handleWebVideo(w, r, provider, model, action, parsed)
		return
	}
	if model != "" && parsed["model"] != model {
		clone := make(map[string]any, len(parsed))
		for k, v := range parsed {
			clone[k] = v
		}
		clone["model"] = model
		raw = mustJSON(clone)
	}

	conns, _ := g.store.ListConnections(provider, true)
	tried := map[string]bool{}
	preferred := r.Header.Get("x-connection-id")
	var lastStatus int
	var lastBody []byte
	for i := 0; i <= len(conns); i++ {
		conn := g.pickConnection(conns, model, tried)
		if conn == nil && preferred != "" && !tried[preferred] {
			if c, err := g.store.GetConnection(preferred); err == nil && c.Provider == provider {
				conn = c
			}
		}
		if conn == nil {
			break
		}
		tried[conn.ID] = true
		status, body, err := g.videoOnce(r, provider, cfg, conn, action, "", raw, contentType, r.Header.Get("Idempotency-Key"))
		if err == nil && status >= 200 && status < 300 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("x-kinerouter-connection-id", conn.ID)
			w.WriteHeader(status)
			w.Write(body)
			return
		}
		lastStatus, lastBody = status, body
		if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests {
			g.markUnavailable(conn, status, upstreamMessage(body), model)
			continue
		}
		break
	}
	if lastStatus == 0 {
		writeErr(w, http.StatusBadRequest, "No credentials for provider: "+provider)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(lastStatus)
	w.Write(lastBody)
}

func (g *Gateway) handleVideoGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "Missing video request id")
		return
	}
	provider := r.URL.Query().Get("provider")
	if connID := r.Header.Get("x-connection-id"); connID != "" {
		if c, err := g.store.GetConnection(connID); err == nil && g.mediaConfig(c.Provider, "videoConfig") != nil {
			provider = c.Provider
		}
	}
	if provider == "" || g.mediaConfig(g.reg.ResolveAlias(provider), "videoConfig") == nil {
		provider = defaultVideoProvider
	}
	provider = g.reg.ResolveAlias(provider)
	if _, ok := g.authorizeRequest(w, r, provider); !ok {
		return
	}
	cfg := g.mediaConfig(provider, "videoConfig")
	if driven, _ := cfg["executorDriven"].(bool); driven {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("[%s] video generation completes inline; there is no job to poll", provider))
		return
	}
	conns, _ := g.store.ListConnections(provider, true)
	var conn *store.Connection
	if connID := r.Header.Get("x-connection-id"); connID != "" {
		if c, err := g.store.GetConnection(connID); err == nil && c.Provider == provider {
			conn = c
		}
	}
	if conn == nil {
		conn = g.pickConnection(conns, "", map[string]bool{})
	}
	if conn == nil {
		writeErr(w, http.StatusBadRequest, "No credentials for provider: "+provider)
		return
	}
	status, body, err := g.videoOnce(r, provider, cfg, conn, "", id, nil, "", "")
	if err != nil && status == 0 {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("x-kinerouter-connection-id", conn.ID)
	w.WriteHeader(status)
	w.Write(body)
}

// resolveVideoProvider maps the request's model to (provider, upstream model).
func (g *Gateway) resolveVideoProvider(parsed map[string]any) (string, string) {
	modelStr, _ := parsed["model"].(string)
	if modelStr == "" {
		return defaultVideoProvider, ""
	}
	targets, err := g.resolveTargets(modelStr)
	if err == nil {
		t := targets[0]
		if g.mediaConfig(t.Provider, "videoConfig") != nil {
			return t.Provider, t.Model
		}
		if !strings.Contains(modelStr, "/") {
			return defaultVideoProvider, modelStr
		}
		return "", ""
	}
	if !strings.Contains(modelStr, "/") {
		return defaultVideoProvider, modelStr
	}
	return "", ""
}

// videoOnce issues one adapter-shaped request. requestID selects polling.
func (g *Gateway) videoOnce(r *http.Request, provider string, cfg map[string]any, conn *store.Connection,
	action, requestID string, raw []byte, contentType, idempotencyKey string) (int, []byte, error) {

	base, _ := cfg["baseUrl"].(string)
	base = strings.TrimRight(base, "/")
	token := conn.Str("accessToken")
	if token == "" {
		token = conn.Str("apiKey")
	}
	headers := map[string]string{"Accept": "application/json"}

	switch provider {
	case "vertex":
		return g.vertexVideo(r, cfg, conn, action, requestID, raw)
	case "openrouter":
		for k, v := range mapHeaders(cfg["headers"]) {
			headers[k] = v
		}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		if requestID != "" {
			return doVideoRequest(r, http.MethodGet, base+"/"+urlQueryEscape(requestID), headers, nil)
		}
		if action != "generations" {
			return http.StatusBadRequest, nil, fmt.Errorf("OpenRouter video supports 'generations' only (got '%s')", action)
		}
		if !strings.Contains(contentType, "application/json") {
			return http.StatusBadRequest, nil, fmt.Errorf("OpenRouter video requires an application/json body")
		}
		headers["Content-Type"] = "application/json"
		return doVideoRequest(r, http.MethodPost, base, headers, raw)
	}

	// Default: xAI shape — forward raw bytes to {base}/{action}.
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if requestID != "" {
		return doVideoRequest(r, http.MethodGet, base+"/"+urlQueryEscape(requestID), headers, nil)
	}
	if !videoActions[action] {
		return http.StatusBadRequest, nil, fmt.Errorf("Unknown video action: %s", action)
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	if idempotencyKey != "" {
		headers["Idempotency-Key"] = idempotencyKey
	}
	return doVideoRequest(r, http.MethodPost, base+"/"+action, headers, raw)
}

func doVideoRequest(r *http.Request, method, url string, headers map[string]string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, url, rd)
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
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		return resp.StatusCode, mustJSON(map[string]any{"error": fmt.Sprintf("[upstream] %s", sanitizeSecretText(msg))}), nil
	}
	return resp.StatusCode, out, nil
}

func mapHeaders(v any) map[string]string {
	out := map[string]string{}
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
	}
	return out
}

// sanitizeSecretText strips bearer tokens from upstream error text.
var bearerRe = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{8,}`)

func sanitizeSecretText(s string) string {
	return bearerRe.ReplaceAllString(s, "Bearer [redacted]")
}

// ---------------------------------------------------------------- Vertex

var vertexOperationRe = regexp.MustCompile(`^projects/[^/]+/locations/[^/]+/publishers/[^/]+/models/[^/]+/operations/[^/]+$`)
var vertexModelRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func (g *Gateway) vertexVideo(r *http.Request, cfg map[string]any, conn *store.Connection,
	action, requestID string, raw []byte) (int, []byte, error) {

	sa := conn.Specific()
	projectID := conn.SpecificStr("projectId")
	if projectID == "" {
		projectID, _ = sa["project_id"].(string)
	}
	location := conn.SpecificStr("location")
	if location == "" {
		location = "us-central1"
	}
	if projectID == "" {
		return http.StatusBadRequest, nil, fmt.Errorf("Vertex video requires a project_id")
	}
	token, err := g.vertexToken(r, conn)
	if err != nil {
		return http.StatusBadRequest, nil, err
	}
	base, _ := cfg["baseUrl"].(string)
	if base == "" {
		base = "https://aiplatform.googleapis.com"
	}
	base = strings.TrimRight(base, "/")
	headers := map[string]string{"Accept": "application/json", "Content-Type": "application/json",
		"Authorization": "Bearer " + token}

	if requestID != "" {
		name, ok := decodeVertexJobID(requestID)
		if !ok {
			return http.StatusBadRequest, nil, fmt.Errorf("Invalid Vertex video job id")
		}
		modelPath := name[:strings.Index(name, "/operations/")]
		status, body, err := doVideoRequest(r, http.MethodPost,
			base+"/v1/"+modelPath+":fetchPredictOperation", headers, mustJSON(map[string]any{"operationName": name}))
		if err != nil {
			return status, body, err
		}
		return status, fromVertexOperation(body), nil
	}
	if action != "generations" {
		return http.StatusBadRequest, nil, fmt.Errorf("Vertex video supports 'generations' only (got '%s')", action)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return http.StatusBadRequest, nil, fmt.Errorf("Invalid JSON body")
	}
	model, _ := body["model"].(string)
	if model == "" {
		return http.StatusBadRequest, nil, fmt.Errorf("Vertex video requires a model")
	}
	if !vertexModelRe.MatchString(model) {
		return http.StatusBadRequest, nil, fmt.Errorf("Invalid Vertex video model id")
	}
	if body["prompt"] == nil && body["image"] == nil && body["image_url"] == nil {
		return http.StatusBadRequest, nil, fmt.Errorf("Vertex video requires a prompt or an image")
	}
	url := base + "/v1/projects/" + projectID + "/locations/" + location +
		"/publishers/google/models/" + model + ":predictLongRunning"
	status, respBody, err := doVideoRequest(r, http.MethodPost, url, headers, mustJSON(toVertexVideoBody(body)))
	if err != nil {
		return status, respBody, err
	}
	return status, fromVertexOperation(respBody), nil
}

// vertexToken reuses a live access token or mints a fresh one.
func (g *Gateway) vertexToken(r *http.Request, conn *store.Connection) (string, error) {
	if tok := conn.Str("accessToken"); tok != "" {
		if exp, err := time.Parse(time.RFC3339, conn.Str("expiresAt")); err == nil && time.Until(exp) > 2*time.Minute {
			return tok, nil
		}
	}
	if _, err := g.oauth.RefreshOne(r.Context(), g.store, conn, 2*time.Minute); err != nil {
		return "", fmt.Errorf("Vertex video: %v", err)
	}
	if c, err := g.store.GetConnection(conn.ID); err == nil {
		if tok := c.Str("accessToken"); tok != "" {
			return tok, nil
		}
	}
	return "", fmt.Errorf("Vertex video requires Service Account JSON or an OAuth access token")
}

func toVertexVideoBody(body map[string]any) map[string]any {
	instance := map[string]any{"prompt": body["prompt"]}
	image := body["image"]
	if image == nil {
		image = body["image_url"]
	}
	switch img := image.(type) {
	case nil:
	case map[string]any:
		instance["image"] = img
	case string:
		if strings.HasPrefix(img, "data:") {
			if i := strings.Index(img, ";base64,"); i > 5 {
				instance["image"] = map[string]any{"bytesBase64Encoded": img[i+8:], "mimeType": img[5:i]}
			}
		} else if img != "" {
			instance["image"] = map[string]any{"gcsUri": img}
		}
	}
	if v, ok := body["video"].(map[string]any); ok {
		instance["video"] = v
	}
	params := map[string]any{}
	for _, k := range []string{"n", "duration", "aspect_ratio", "resolution", "seed", "negative_prompt", "storage_uri"} {
		if body[k] != nil {
			params[k] = body[k]
		}
	}
	out := map[string]any{"instances": []any{instance}}
	if len(params) > 0 {
		out["parameters"] = params
	}
	return out
}

func decodeVertexJobID(id string) (string, bool) {
	if id == "" || len(id) > 1024 {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", false
	}
	name := string(decoded)
	if base64.RawURLEncoding.EncodeToString(decoded) != id {
		return "", false
	}
	return name, vertexOperationRe.MatchString(name)
}

func encodeVertexJobID(name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name))
}

// fromVertexOperation maps a Vertex long-running operation onto the async-job
// shape clients poll for.
func fromVertexOperation(raw []byte) []byte {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	name, _ := m["name"].(string)
	if name == "" {
		return raw
	}
	id := encodeVertexJobID(name)
	out := map[string]any{"id": id, "request_id": id}
	if err, ok := m["error"]; ok && err != nil {
		out["status"] = "failed"
		out["error"] = err
		return mustJSON(out)
	}
	done, _ := m["done"].(bool)
	if !done {
		out["status"] = "pending"
		return mustJSON(out)
	}
	resp, _ := m["response"].(map[string]any)
	samples := listOf(resp["videos"])
	if len(samples) == 0 {
		gr, _ := resp["generateVideoResponse"].(map[string]any)
		samples = listOf(gr["generatedSamples"])
	}
	videos := make([]any, 0, len(samples))
	for _, s := range samples {
		sm, _ := s.(map[string]any)
		if sm == nil {
			continue
		}
		v, _ := sm["video"].(map[string]any)
		entry := map[string]any{
			"url":       firstNonEmpty(strAny(sm["gcsUri"]), strAny(v["uri"]), strAny(sm["uri"])),
			"b64_json":  firstNonEmpty(strAny(sm["bytesBase64Encoded"]), strAny(v["bytesBase64Encoded"])),
			"mime_type": firstNonEmpty(strAny(sm["mimeType"]), strAny(v["mimeType"]), "video/mp4"),
		}
		if entry["url"] == "" {
			entry["url"] = nil
		}
		if entry["b64_json"] == "" {
			entry["b64_json"] = nil
		}
		videos = append(videos, entry)
	}
	out["status"] = "completed"
	if len(videos) > 0 {
		out["video"] = videos[0]
	} else {
		out["video"] = nil
	}
	out["videos"] = videos
	return mustJSON(out)
}

func listOf(v any) []any {
	l, _ := v.([]any)
	return l
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
