package gateway

// POST /v1/systemone — System One (Jev) decision endpoint. Body and JSON
// response are forwarded untouched; no chat translation layer. Mirrors
// open-sse/handlers/systemoneCore.js + src/sse/handlers/systemone.js.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

func (g *Gateway) handleSystemone(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	modelStr, _ := body["model"].(string)
	if modelStr == "" {
		writeErr(w, http.StatusBadRequest, "Missing model")
		return
	}
	if body["state"] == nil {
		writeErr(w, http.StatusBadRequest, "Missing required field: state")
		return
	}
	if _, ok := body["questions"].(map[string]any); !ok {
		writeErr(w, http.StatusBadRequest, "Missing required field: questions")
		return
	}
	if _, ok := g.authorizeRequest(w, r, modelStr); !ok {
		return
	}
	targets, err := g.resolveTargets(modelStr)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid model format")
		return
	}
	t := targets[0]
	cfg := g.mediaConfig(t.Provider, "systemoneConfig")
	if cfg == nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("Provider '%s' does not support System One.", t.Provider))
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
		// Refresh an expired OAuth token before use (no-op for API keys).
		if _, err := g.oauth.RefreshOne(r.Context(), g.store, conn, 2*time.Minute); err == nil {
			if fresh, err := g.store.GetConnection(conn.ID); err == nil {
				conn = fresh
			}
		}
		start := time.Now()
		status, out, usage, err := g.systemoneOnce(r, conn, t, cfg, body)
		if status == http.StatusOK && err == nil {
			if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
				g.recordUsage(conn, t, "", "/v1/systemone", usage, start)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(out)
			return
		}
		lastStatus, lastBody = status, out
		if status == http.StatusUnauthorized || status == http.StatusForbidden ||
			status == http.StatusTooManyRequests || status == http.StatusPaymentRequired {
			g.markUnavailable(conn, status, string(out), t.Model)
			continue
		}
		break
	}
	if lastStatus == 0 {
		writeErr(w, http.StatusBadRequest, "No credentials for provider: "+t.Provider)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(lastStatus)
	w.Write(lastBody)
}

// systemoneURL joins a saved host override with the registry path without
// doubling a "/v1" or "/v1/systemone" the user already included.
func systemoneURL(override string, cfg map[string]any) string {
	base := strings.TrimSpace(override)
	cfgBase, _ := cfg["baseUrl"].(string)
	if base == "" {
		return cfgBase
	}
	path, _ := cfg["path"].(string)
	if path == "" {
		return base
	}
	host := strings.TrimRight(base, "/")
	path = strings.TrimRight(path, "/")
	if strings.HasSuffix(host, path) {
		return host
	}
	if i := strings.LastIndex(path, "/"); i > 0 {
		if prefix := path[:i]; strings.HasSuffix(host, prefix) {
			return host + path[len(prefix):]
		}
	}
	return host + path
}

func (g *Gateway) systemoneOnce(r *http.Request, conn *store.Connection, t target, cfg, body map[string]any) (int, []byte, translate.Usage, error) {
	override := ""
	if sa := conn.Specific(); sa != nil {
		override, _ = sa["baseUrl"].(string)
	}
	url := systemoneURL(override, cfg)
	if url == "" {
		return http.StatusBadRequest, nil, translate.Usage{}, fmt.Errorf("unsupported")
	}
	if strings.Contains(url, "{accountId}") {
		accountID := conn.SpecificStr("accountId")
		if accountID == "" {
			return http.StatusBadRequest, nil, translate.Usage{},
				fmt.Errorf("Provider '%s' requires accountId in providerSpecificData.", t.Provider)
		}
		url = strings.ReplaceAll(url, "{accountId}", accountID)
	}
	url = strings.ReplaceAll(url, "{model}", t.Model)

	payload := make(map[string]any, len(body))
	for k, v := range body {
		payload[k] = v
	}
	payload["model"] = g.reg.UpstreamModelID(t.Provider, t.Model)

	headers := map[string]string{"Content-Type": "application/json"}
	if token := conn.Str("apiKey"); token != "" {
		headers["Authorization"] = "Bearer " + token
	} else if token := conn.Str("accessToken"); token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	for k, v := range mapHeaders(cfg["headers"]) {
		headers[k] = v
	}
	if t.Provider == "opencode" || t.Provider == "opencode-zen" {
		headers["x-opencode-session"] = opencodeSessionID()
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, url, bytes.NewReader(mustJSON(payload)))
	if err != nil {
		return http.StatusBadRequest, nil, translate.Usage{}, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := mediaClient.Do(req)
	if err != nil {
		return http.StatusBadGateway, mustJSON(map[string]any{"error": "upstream fetch failed"}), translate.Usage{}, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, mustJSON(map[string]any{"error": fmt.Sprintf("[%s/%s] %s", t.Provider, t.Model, sanitizeSecretText(string(out)))}), translate.Usage{}, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	u, _ := m["usage"].(map[string]any)
	return http.StatusOK, out, translate.Usage{
		PromptTokens:     num(u["input_tokens"]),
		CompletionTokens: num(u["output_tokens"]),
	}, nil
}

// opencodeSessionID generates a client session id in the opencode shape.
// ponytail: random suffix only — the descending-time prefix is cosmetic.
func opencodeSessionID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "ses_" + hex.EncodeToString(b)
}
