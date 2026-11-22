package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/auth"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// registerModelsAdminRoutes adds /api/models/{availability,custom,disabled,test}
// (src/app/api/models/*).
func (g *Gateway) registerModelsAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/models/availability", g.requireAuth(g.handleModelsAvailability))
	mux.HandleFunc("POST /api/models/availability", g.requireAuth(g.handleClearCooldown))
	mux.HandleFunc("GET /api/models/custom", g.requireAuth(g.handleListCustomModels))
	mux.HandleFunc("POST /api/models/custom", g.requireAuth(g.handleAddCustomModel))
	mux.HandleFunc("DELETE /api/models/custom", g.requireAuth(g.handleDeleteCustomModel))
	mux.HandleFunc("GET /api/models/disabled", g.requireAuth(g.handleListDisabledModels))
	mux.HandleFunc("POST /api/models/disabled", g.requireAuth(g.handleDisableModels))
	mux.HandleFunc("DELETE /api/models/disabled", g.requireAuth(g.handleEnableModels))
	mux.HandleFunc("POST /api/models/test", g.requireAuth(g.handleModelTest))
}

// ---------------------------------------------------------------- availability

const modelLockPrefix = "modelLock_"

// handleModelsAvailability lists active cooldowns/locks per connection.
func (g *Gateway) handleModelsAvailability(w http.ResponseWriter, r *http.Request) {
	conns, err := g.store.ListConnections("", false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch model availability")
		return
	}
	now := time.Now()
	out := []map[string]any{}
	for _, c := range conns {
		active := 0
		for _, k := range c.ModelLockKeys() {
			until := c.Str(k)
			t, err := time.Parse(time.RFC3339, until)
			if err != nil || !t.After(now) {
				continue
			}
			active++
			out = append(out, map[string]any{
				"provider":       c.Provider,
				"model":          strings.TrimPrefix(k, modelLockPrefix),
				"status":         "cooldown",
				"until":          until,
				"connectionId":   c.ID,
				"connectionName": connDisplayName(c),
				"lastError":      nullableAny(c.Str("lastError")),
			})
		}
		if active == 0 && c.Str("testStatus") == "unavailable" {
			out = append(out, map[string]any{
				"provider":       c.Provider,
				"model":          "__all",
				"status":         "unavailable",
				"connectionId":   c.ID,
				"connectionName": connDisplayName(c),
				"lastError":      nullableAny(c.Str("lastError")),
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out, "unavailableCount": len(out)})
}

// handleClearCooldown clears one model lock across a provider's connections.
func (g *Gateway) handleClearCooldown(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action   string `json:"action"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := decodeBody(r, &body); err != nil || body.Action != "clearCooldown" || body.Provider == "" || body.Model == "" {
		writeErr(w, http.StatusBadRequest, "Invalid request")
		return
	}
	conns, err := g.store.ListConnections(body.Provider, false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to clear cooldown")
		return
	}
	lockKey := modelLockPrefix + body.Model
	for _, c := range conns {
		if c.Str(lockKey) == "" && c.AuthType == "" {
			continue
		}
		if _, ok := c.Data[lockKey]; !ok {
			continue
		}
		patch := map[string]any{lockKey: nil}
		if c.Str("testStatus") == "unavailable" {
			patch["testStatus"] = "active"
			patch["lastError"] = nil
			patch["lastErrorAt"] = nil
			patch["backoffLevel"] = 0
		}
		if _, err := g.store.PatchConnection(c.ID, patch); err != nil {
			writeErr(w, http.StatusInternalServerError, "Failed to clear cooldown")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func connDisplayName(c *store.Connection) string {
	if c.Name != "" {
		return c.Name
	}
	if c.Email != "" {
		return c.Email
	}
	return c.ID
}

func nullableAny(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---------------------------------------------------------------- custom models

// sanitizeCaps keeps only the CAPACITY_META keys the JS route whitelists.
func sanitizeCaps(v any) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := map[string]any{}
	for _, k := range []string{"vision", "reasoning"} {
		if b, ok := m[k].(bool); ok {
			out[k] = b
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sttTransports mirrors STT_TRANSPORT_META keys.
func sttTransportOK(v string) bool { return v == "gemini-live" }

func (g *Gateway) handleListCustomModels(w http.ResponseWriter, r *http.Request) {
	models, err := g.store.ListCustomModels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch custom models")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}

func (g *Gateway) handleAddCustomModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderAlias string `json:"providerAlias"`
		ID            string `json:"id"`
		Type          string `json:"type"`
		Name          string `json:"name"`
		Caps          any    `json:"caps"`
		Transport     string `json:"transport"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request")
		return
	}
	if body.ProviderAlias == "" || body.ID == "" {
		writeErr(w, http.StatusBadRequest, "providerAlias and id required")
		return
	}
	typ := body.Type
	if typ == "" {
		typ = "llm"
	}
	transport := ""
	if typ == "stt" && sttTransportOK(strings.TrimSpace(body.Transport)) {
		transport = strings.TrimSpace(body.Transport)
	}
	added, err := g.store.AddCustomModel(body.ProviderAlias, body.ID, typ, body.Name, sanitizeCaps(body.Caps), transport)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to add custom model")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "added": added})
}

func (g *Gateway) handleDeleteCustomModel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	alias, id := q.Get("providerAlias"), q.Get("id")
	if alias == "" || id == "" {
		writeErr(w, http.StatusBadRequest, "providerAlias and id required")
		return
	}
	typ := firstNonEmpty(q.Get("type"), "llm")
	if err := g.store.DeleteCustomModel(alias, id, typ); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to delete custom model")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------------------------------------------------------------- disabled models

func (g *Gateway) handleListDisabledModels(w http.ResponseWriter, r *http.Request) {
	all, err := g.store.DisabledModels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch disabled models")
		return
	}
	if alias := r.URL.Query().Get("providerAlias"); alias != "" {
		writeJSON(w, http.StatusOK, map[string]any{"ids": all[alias]})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"disabled": all})
}

func (g *Gateway) handleDisableModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProviderAlias string   `json:"providerAlias"`
		IDs           []string `json:"ids"`
	}
	if err := decodeBody(r, &body); err != nil || body.ProviderAlias == "" || body.IDs == nil {
		writeErr(w, http.StatusBadRequest, "providerAlias and ids[] required")
		return
	}
	if err := g.store.DisableModels(body.ProviderAlias, body.IDs); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to disable models")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (g *Gateway) handleEnableModels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	alias := q.Get("providerAlias")
	if alias == "" {
		writeErr(w, http.StatusBadRequest, "providerAlias required")
		return
	}
	ids := []string{}
	if id := q.Get("id"); id != "" {
		ids = append(ids, id)
	}
	if err := g.store.EnableModels(alias, ids); err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to enable models")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ---------------------------------------------------------------- model test

// handleModelTest pings one model through this gateway, the same way the JS
// dashboard's "test" button does (POST /api/models/test → ping.js).
func (g *Gateway) handleModelTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
		Kind  string `json:"kind"`
	}
	if err := decodeBody(r, &body); err != nil || body.Model == "" {
		writeErr(w, http.StatusBadRequest, "Model required")
		return
	}
	kind := firstNonEmpty(body.Kind, "llm")
	result, err := g.pingModel(r, body.Model, kind)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// pingModel issues a self-request to the local gateway. Auth: an unrestricted
// active API key (if any) plus the derived CLI token, so a login wall cannot
// block the probe.
func (g *Gateway) pingModel(r *http.Request, model, kind string) (map[string]any, error) {
	headers := map[string]string{"Content-Type": "application/json"}
	if key := g.selfPingKey(); key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	headers["x-kine-cli-token"] = auth.CLIToken(g.cliMachineID)
	base := fmt.Sprintf("http://127.0.0.1:%d", g.cfg.Port)

	var path string
	var payload any
	switch kind {
	case "embedding":
		path, payload = "/api/v1/embeddings", map[string]any{"model": model, "input": "test"}
	case "image":
		path, payload = "/api/v1/images/generations", map[string]any{"model": model, "prompt": "test"}
	case "systemone":
		path, payload = "/api/v1/systemone", map[string]any{
			"model": model,
			"state": "Customer: I was charged twice for my order this morning.",
			"questions": map[string]any{
				"probe": map[string]any{"type": "noul", "instructions": "Is the customer reporting a billing problem?"},
			},
		}
	case "stt":
		return g.pingStt(base, headers, model)
	default:
		path, payload = "/api/v1/chat/completions", map[string]any{
			"model":      model,
			"max_tokens": 1024,
			"stream":     false,
			"messages":   []map[string]any{{"role": "user", "content": "hi"}},
		}
	}

	start := time.Now()
	body, _ := json.Marshal(payload)
	res, err := httpPostJSON(r.Context(), base+path, headers, body)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "latencyMs": latency, "error": err.Error()}, nil
	}
	return g.checkPingResponse(kind, res.status, res.body, latency), nil
}

type pingResponse struct {
	status int
	body   []byte
}

func (g *Gateway) checkPingResponse(kind string, status int, raw []byte, latency int64) map[string]any {
	var parsed map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &parsed)
	}
	if status < 200 || status >= 300 {
		detail := pingDetail(parsed, raw)
		return map[string]any{"ok": false, "latencyMs": latency, "status": status,
			"error": fmt.Sprintf("HTTP %d: %s", status, truncHead(detail, 240))}
	}
	switch kind {
	case "embedding":
		data, _ := parsed["data"].([]any)
		if len(data) == 0 {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": "Provider returned no embedding data"}
		}
		if m, ok := data[0].(map[string]any); !ok || m["embedding"] == nil {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": "Provider returned no embedding data"}
		}
	case "image":
		if data, _ := parsed["data"].([]any); len(data) == 0 {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": "Provider returned no image data for this model"}
		}
	case "stt":
		if strings.TrimSpace(rawString(parsed["text"])) == "" {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": "Provider returned no transcription text for this model"}
		}
	case "systemone":
		if ans, ok := parsed["answers"].(map[string]any); !ok || len(ans) == 0 {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": "Provider returned no answers for this model"}
		}
	default:
		if parsedErr := pingProviderError(parsed); parsedErr != "" {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": parsedErr}
		}
		choices, _ := parsed["choices"].([]any)
		if len(choices) == 0 {
			return map[string]any{"ok": false, "latencyMs": latency, "status": status, "error": "Provider returned no completion choices for this model"}
		}
		first, _ := choices[0].(map[string]any)
		if pingReasoningOnly(first) {
			return map[string]any{"ok": true, "latencyMs": latency, "status": status,
				"note": "reasoning-only response (length-limited)"}
		}
	}
	return map[string]any{"ok": true, "latencyMs": latency, "status": status}
}

// pingProviderError catches an upstream error relayed inside a 200 body with a
// provider status, mirroring ping.js.
func pingProviderError(parsed map[string]any) string {
	if parsed == nil {
		return ""
	}
	if st, ok := parsed["status"]; ok && st != nil {
		s := fmt.Sprint(st)
		if s != "" && s != "200" && s != "0" {
			if msg := rawString(parsed["msg"]); msg == "" {
				if msg = rawString(parsed["message"]); msg != "" {
					return truncHead("Provider status "+s+": "+msg, 240)
				}
			} else {
				return truncHead("Provider status "+s+": "+msg, 240)
			}
		}
	}
	if e, ok := parsed["error"]; ok && e != nil {
		switch v := e.(type) {
		case string:
			return truncHead(v, 240)
		case map[string]any:
			return truncHead(firstNonEmpty(rawString(v["message"]), "Provider returned an error"), 240)
		default:
			return truncHead(fmt.Sprint(v), 240)
		}
	}
	return ""
}

// pingReasoningOnly is the issue-#3010 soft-pass: finish_reason="length" with
// empty content but non-empty reasoning is a healthy connection.
func pingReasoningOnly(choice map[string]any) bool {
	if choice == nil {
		return false
	}
	if rawString(choice["finish_reason"]) != "length" {
		return false
	}
	msg, _ := choice["message"].(map[string]any)
	if msg == nil {
		return false
	}
	if strings.TrimSpace(rawString(msg["content"])) != "" {
		return false
	}
	for _, k := range []string{"reasoning", "reasoning_content", "thinking", "thinking_content"} {
		if strings.TrimSpace(rawString(msg[k])) != "" {
			return true
		}
	}
	return false
}

func (g *Gateway) pingStt(base string, headers map[string]string, model string) (map[string]any, error) {
	start := time.Now()
	status, raw, err := httpPostWav(base+"/api/v1/audio/transcriptions", headers, model)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{"ok": false, "latencyMs": latency, "error": err.Error()}, nil
	}
	return g.checkPingResponse("stt", status, raw, latency), nil
}

// selfPingKey picks an active unrestricted API key, else any active key.
func (g *Gateway) selfPingKey() string {
	keys, err := g.store.ListAPIKeys()
	if err != nil {
		return ""
	}
	var fallback string
	for _, k := range keys {
		if !k.IsActive {
			continue
		}
		if !k.AccessRestricted {
			return k.Key
		}
		if fallback == "" {
			fallback = k.Key
		}
	}
	return fallback
}

// ---------------------------------------------------------------- http helpers

func httpPostJSON(ctx context.Context, url string, headers map[string]string, body []byte) (pingResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return pingResponse{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	cl := &http.Client{Timeout: 15 * time.Second}
	res, err := cl.Do(req)
	if err != nil {
		return pingResponse{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return pingResponse{status: res.StatusCode, body: raw}, nil
}

// httpPostWav posts a minimal silent WAV as multipart/form-data.
func httpPostWav(url string, headers map[string]string, model string) (int, []byte, error) {
	boundary := "----kinerouterprobe"
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\n%s\r\n", boundary, model)
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"test.wav\"\r\nContent-Type: audio/wav\r\n\r\n", boundary)
	buf.Write(silentWav())
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)

	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	for k, v := range headers {
		if strings.EqualFold(k, "Content-Type") {
			continue
		}
		req.Header.Set(k, v)
	}
	cl := &http.Client{Timeout: 15 * time.Second}
	res, err := cl.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, raw, nil
}

// silentWav builds a 250 ms 16 kHz mono 16-bit silent WAV (createSilentWavFile).
func silentWav() []byte {
	const sampleRate, bits, ms = 16000, 16, 250
	samples := sampleRate * ms / 1000
	dataSize := samples * (bits / 8)
	b := make([]byte, 44+dataSize)
	copy(b, "RIFF")
	le32(b[4:], uint32(36+dataSize))
	copy(b[8:], "WAVEfmt ")
	le32(b[16:], 16)
	le16(b[20:], 1)
	le16(b[22:], 1)
	le32(b[24:], sampleRate)
	le32(b[28:], sampleRate*2)
	le16(b[32:], 2)
	le16(b[34:], bits)
	copy(b[36:], "data")
	le32(b[40:], uint32(dataSize))
	return b
}

func le32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func le16(b []byte, v uint16) { b[0], b[1] = byte(v), byte(v>>8) }

func pingDetail(parsed map[string]any, raw []byte) string {
	if parsed != nil {
		if e, ok := parsed["error"].(map[string]any); ok {
			if s := rawString(e["message"]); s != "" {
				return s
			}
		}
		for _, k := range []string{"msg", "message", "error"} {
			if s := rawString(parsed[k]); s != "" {
				return s
			}
		}
	}
	return string(raw)
}

func rawString(v any) string {
	s, _ := v.(string)
	return s
}

func truncHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
