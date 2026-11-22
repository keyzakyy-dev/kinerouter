package gateway

// Translator debugger routes: replay a request through each translation step
// and inspect/save the artifacts. Ported from src/app/api/translator/{translate,
// send, load, save}/route.js.
//
// Security: the save/load routes accept only the eight fixed artifact names, so
// a request can never read or write an arbitrary path.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

var translatorArtifacts = map[string]bool{
	"1_req_client.json": true, "2_req_source.json": true, "3_req_openai.json": true,
	"4_req_target.json": true, "5_res_provider.txt": true, "6_res_openai.txt": true,
	"7_res_client.txt": true, "7_res_client.json": true,
}

func (g *Gateway) translatorLogDir() string {
	return filepath.Join(g.cfg.DataDir, "logs", "translator")
}

func (g *Gateway) registerTranslatorDebugRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("POST /api/translator/translate", r(g.handleTranslatorTranslate))
	mux.HandleFunc("POST /api/translator/send", r(g.handleTranslatorSend))
	mux.HandleFunc("GET /api/translator/load", r(g.handleTranslatorLoad))
	mux.HandleFunc("POST /api/translator/save", r(g.handleTranslatorSave))
}

// detectClientFormat inspects a request body for its client grammar.
func detectClientFormat(body map[string]any) string {
	if body["contents"] != nil || body["systemInstruction"] != nil {
		return translate.FormatGemini
	}
	if body["system"] != nil || body["anthropic_version"] != nil {
		return translate.FormatClaude
	}
	if body["input"] != nil && body["messages"] == nil {
		return translate.FormatResponses
	}
	return translate.FormatOpenAI
}

func (g *Gateway) handleTranslatorTranslate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Step     int            `json:"step"`
		Body     map[string]any `json:"body"`
		Provider string         `json:"provider"`
		Model    string         `json:"model"`
	}
	if decodeBody(r, &body) != nil || body.Step == 0 || body.Body == nil {
		writeErr(w, http.StatusBadRequest, "Step and body required")
		return
	}
	inner := body.Body
	if nested, ok := inner["body"].(map[string]any); ok {
		inner = nested
	}
	modelStr, _ := inner["model"].(string)
	stream, _ := inner["stream"].(bool)
	if _, ok := inner["stream"]; !ok {
		stream = true
	}
	switch body.Step {
	case 1:
		targets, err := g.resolveTargets(modelStr)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		t := targets[0]
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "result": map[string]any{
			"provider": t.Provider, "model": t.Model,
			"sourceFormat": detectClientFormat(inner), "targetFormat": g.reg.Format(t.Provider),
		}})
	case 2:
		src := detectClientFormat(inner)
		out, err := translateRequestWithSession(src, translate.FormatOpenAI, inner, modelStr, stream, "")
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		delete(out, "_toolNameMap")
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "result": map[string]any{"body": out}})
	case 3:
		if body.Provider == "" || body.Model == "" {
			writeErr(w, http.StatusBadRequest, "provider and model required")
			return
		}
		targetFormat := g.reg.Format(g.reg.ResolveAlias(body.Provider))
		translated, err := translateRequestWithSession(translate.FormatOpenAI, targetFormat, inner, body.Model, stream, "")
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
			return
		}
		delete(translated, "_toolNameMap")
		conns, _ := g.store.ListConnections(body.Provider, true)
		if len(conns) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"success": false, "error": "No active connection for provider: " + body.Provider})
			return
		}
		// The executor's URL/header derivation is internal to Execute, so the
		// translated body and the registry transport are what the debugger shows.
		// ponytail: expose build() if URL/header introspection is needed.
		t := g.reg.TransportFor(g.reg.ResolveAlias(body.Provider))
		result := map[string]any{"body": translated, "format": targetFormat}
		if t != nil {
			result["baseUrl"] = t.BaseURL
			result["url"] = strings.TrimRight(t.BaseURL, "/") + "/chat/completions"
			result["headers"] = t.Headers
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "result": result})
	default:
		writeErr(w, http.StatusBadRequest, "Invalid step (1-3)")
	}
}

// handleTranslatorSend sends one raw body to a provider and relays the stream.
func (g *Gateway) handleTranslatorSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string         `json:"provider"`
		Model    string         `json:"model"`
		Body     map[string]any `json:"body"`
	}
	if decodeBody(r, &body) != nil || body.Provider == "" || body.Model == "" || body.Body == nil {
		writeErr(w, http.StatusBadRequest, "provider, model, and body required")
		return
	}
	provider := g.reg.ResolveAlias(body.Provider)
	conns, _ := g.store.ListConnections(provider, true)
	if len(conns) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": "No active connection for provider: " + body.Provider})
		return
	}
	conn := conns[0]
	stream := true
	if b, ok := body.Body["stream"].(bool); ok {
		stream = b
	}
	payload, _ := json.Marshal(body.Body)
	resp, err := g.exec.Execute(r.Context(), executor.Request{
		Provider: provider, Model: body.Model, Format: g.reg.Format(provider),
		Body: payload, Stream: stream, Creds: executor.CredentialsFromConnection(conn),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.Status < 200 || resp.Status >= 300 {
		text, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		writeJSON(w, resp.Status, map[string]any{
			"success": false, "error": "Provider error: " + http.StatusText(resp.Status),
			"details": string(text),
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
}

func (g *Gateway) handleTranslatorLoad(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("file")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "File parameter required"})
		return
	}
	if !translatorArtifacts[name] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid file name"})
		return
	}
	content, err := os.ReadFile(filepath.Join(g.translatorLogDir(), name))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": "File not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "content": string(content)})
}

func (g *Gateway) handleTranslatorSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		File    string `json:"file"`
		Content string `json:"content"`
	}
	if decodeBody(r, &body) != nil || body.File == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "File and content required"})
		return
	}
	if !translatorArtifacts[body.File] {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": "Invalid file name"})
		return
	}
	dir := g.translatorLogDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	if err := os.WriteFile(filepath.Join(dir, body.File), []byte(body.Content), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

var _ = store.Connection{}
var _ = strings.TrimSpace
