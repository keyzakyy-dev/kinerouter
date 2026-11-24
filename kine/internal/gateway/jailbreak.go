package gateway

// Persona / jailbreak presets: the dashboard library, its bindings and the
// request-path injection. Prompt injection is opt-in per model and defaults to
// off; a preset is only armed through an exact binding or a keyword rule.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	maxJailbreakBody  = 512 << 10
	maxJailbreakFiles = 200
)

func (g *Gateway) registerJailbreakRoutes(mux *http.ServeMux) {
	r := g.requireAuth
	mux.HandleFunc("GET /api/jailbreak", r(g.handleGetJailbreak))
	mux.HandleFunc("PUT /api/jailbreak", r(g.handlePutJailbreak))
	mux.HandleFunc("GET /api/jailbreak/presets", r(g.handleListJailbreakPresets))
	mux.HandleFunc("PUT /api/jailbreak/presets", r(g.handlePutJailbreakPreset))
	mux.HandleFunc("DELETE /api/jailbreak/presets/{id}", r(g.handleDeleteJailbreakPreset))
	mux.HandleFunc("POST /api/jailbreak/presets/{id}/duplicate", r(g.handleDuplicateJailbreakPreset))
	mux.HandleFunc("PUT /api/jailbreak/presets/order", r(g.handleReorderJailbreakPresets))
	mux.HandleFunc("POST /api/jailbreak/presets/upload", r(g.handleUploadJailbreakPreset))
	mux.HandleFunc("GET /api/jailbreak/models", r(g.handleListJailbreakBindings))
	mux.HandleFunc("GET /api/jailbreak/models/catalog", r(g.handleJailbreakModelCatalog))
	mux.HandleFunc("PUT /api/jailbreak/models", r(g.handleSetJailbreakModel))
	mux.HandleFunc("DELETE /api/jailbreak/models", r(g.handleDeleteJailbreakModel))
	mux.HandleFunc("PUT /api/jailbreak/keys/model", r(g.handleSetJailbreakKeyModel))
	mux.HandleFunc("GET /api/jailbreak/keys/bindings", r(g.handleListJailbreakKeyBindings))
	mux.HandleFunc("GET /api/jailbreak/excludes", r(g.handleGetJailbreakExcludes))
	mux.HandleFunc("PUT /api/jailbreak/excludes", r(g.handlePutJailbreakExcludes))
	mux.HandleFunc("POST /api/jailbreak/import", r(g.handleImportJailbreak))
	mux.HandleFunc("POST /api/jailbreak/resolve", r(g.handleResolveJailbreak))
	mux.HandleFunc("GET /api/jailbreak/usage", r(g.handleJailbreakUsage))
	mux.HandleFunc("POST /api/jailbreak/preview", r(g.handlePreviewJailbreak))
	mux.HandleFunc("GET /api/jailbreak/sync/sources", r(g.handleListJailbreakSources))
	mux.HandleFunc("PUT /api/jailbreak/sync/sources", r(g.handleSaveJailbreakSources))
	mux.HandleFunc("POST /api/jailbreak/sync/run", r(g.handleRunJailbreakSync))
	mux.HandleFunc("GET /api/jailbreak/github", r(g.handleGetJailbreakGithub))
	mux.HandleFunc("PUT /api/jailbreak/github/token", r(g.handleSaveJailbreakGithub))
}

// ---------------------------------------------------------------- handlers

func (g *Gateway) handleGetJailbreak(w http.ResponseWriter, r *http.Request) {
	doc, err := g.store.GetJailbreakPreset("default")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if doc == nil {
		doc = &store.JailbreakPreset{ID: "default", Name: "Default"}
	}
	writeJSON(w, http.StatusOK, doc)
}

func (g *Gateway) handlePutJailbreak(w http.ResponseWriter, r *http.Request) {
	var doc store.JailbreakPreset
	if err := decodeBody(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	doc.ID = "default"
	if err := validateJailbreakPreset(&doc); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := g.store.SaveJailbreakPreset(&doc); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "doc": doc})
}

func (g *Gateway) handleListJailbreakPresets(w http.ResponseWriter, r *http.Request) {
	presets, err := g.store.ListJailbreakPresets()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, presets)
}

func (g *Gateway) handlePutJailbreakPreset(w http.ResponseWriter, r *http.Request) {
	var doc store.JailbreakPreset
	if err := decodeBody(r, &doc); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if doc.ID == "" {
		base := doc.Name
		if base == "" {
			base = doc.Source
		}
		doc.ID = slugify(base)
	}
	if err := validateJailbreakPreset(&doc); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := g.store.SaveJailbreakPreset(&doc); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preset": doc})
}

func validateJailbreakPreset(doc *store.JailbreakPreset) error {
	kws, err := store.NormalizeJailbreakKeywords(doc.Keywords)
	if err != nil {
		return err
	}
	excl, err := store.NormalizeJailbreakKeywords(doc.Excludes)
	if err != nil {
		return err
	}
	doc.Keywords, doc.Excludes = kws, excl
	switch doc.Position {
	case "", "system", "append":
	default:
		return fmt.Errorf("unknown position %q: want \"system\", \"append\" or empty for the legacy prepend", doc.Position)
	}
	switch doc.Mode {
	case "", "chat":
	default:
		return fmt.Errorf("unknown mode %q: want \"chat\" or empty for a system prompt", doc.Mode)
	}
	if doc.Mode == "chat" && doc.Position == "append" {
		return fmt.Errorf("mode \"chat\" has no system prompt to append to; leave position empty or use \"system\"")
	}
	return nil
}

func (g *Gateway) handleDeleteJailbreakPreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing preset id")
		return
	}
	if err := g.store.DeleteJailbreakPreset(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (g *Gateway) handleDuplicateJailbreakPreset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	src, err := g.store.GetJailbreakPreset(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if src == nil {
		writeErr(w, http.StatusNotFound, "preset not found")
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	_ = decodeBody(r, &req)
	newID := strings.TrimSpace(req.ID)
	if newID == "" {
		base := src.ID
		if base == "" {
			base = slugify(src.Name)
		}
		newID = base + "-copy"
		for n := 2; ; n++ {
			existing, err := g.store.GetJailbreakPreset(newID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if existing == nil || existing.Prompt == "" {
				break
			}
			newID = fmt.Sprintf("%s-copy-%d", base, n)
		}
	}
	if newID == src.ID {
		writeErr(w, http.StatusBadRequest, "duplicate id must differ from the source")
		return
	}
	clone := &store.JailbreakPreset{
		ID: newID, Name: src.Name + " (copy)", Prompt: src.Prompt, Kind: src.Kind,
		GeminiSafety: src.GeminiSafety, Source: src.Source,
		Keywords: src.Keywords, Excludes: src.Excludes,
	}
	if err := g.store.SaveJailbreakPreset(clone); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preset": clone})
}

func (g *Gateway) handleReorderJailbreakPresets(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Order []string `json:"order"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Order) == 0 {
		writeErr(w, http.StatusBadRequest, "missing order")
		return
	}
	if err := g.store.ReorderJailbreakPresets(req.Order); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (g *Gateway) handleUploadJailbreakPreset(w http.ResponseWriter, r *http.Request) {
	name, content, kind := "", "", ""
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(maxJailbreakBody); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		name = r.FormValue("name")
		kind = r.FormValue("kind")
		file, header, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "missing file")
			return
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, maxJailbreakBody))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		content = string(raw)
		if name == "" {
			name = header.Filename
		}
	} else {
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
			Kind    string `json:"kind"`
		}
		if err := decodeBody(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		name, content, kind = req.Name, req.Content, req.Kind
	}
	if strings.TrimSpace(content) == "" {
		writeErr(w, http.StatusBadRequest, "empty preset file")
		return
	}
	doc := &store.JailbreakPreset{
		ID: slugify(name), Name: strings.TrimSuffix(name, path.Ext(name)),
		Prompt: content, Source: name, Kind: firstNonEmpty(kind, jailbreakKindOf(name)),
	}
	if err := g.store.SaveJailbreakPreset(doc); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preset": doc})
}

func (g *Gateway) handleListJailbreakBindings(w http.ResponseWriter, r *http.Request) {
	bindings, err := g.store.ListJailbreakBindings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bindings)
}

func (g *Gateway) handleListJailbreakKeyBindings(w http.ResponseWriter, r *http.Request) {
	keyID := r.URL.Query().Get("keyId")
	if keyID == "" || len(keyID) > 100 {
		writeErr(w, http.StatusBadRequest, "invalid api key id")
		return
	}
	bindings, err := g.store.ListJailbreakBindings()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := map[string]store.JailbreakBinding{}
	for id, cfg := range bindings {
		if i := strings.IndexByte(id, ':'); i > 0 && id[:i] == keyID {
			out[id[i+1:]] = cfg
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// bindingRequest decodes the body both model-binding routes share.
type bindingRequest struct {
	KeyID    string `json:"keyId"`
	ID       string `json:"id"`
	Enabled  *bool  `json:"enabled"`
	PresetID string `json:"presetId"`
}

func (req bindingRequest) resolve() (enabled bool, presetID string) {
	if req.Enabled != nil {
		enabled = *req.Enabled
	} else if req.PresetID != "" {
		enabled = req.PresetID != "off" && req.PresetID != "none"
	}
	presetID = req.PresetID
	if enabled && (presetID == "" || presetID == "on") {
		presetID = "default"
	}
	return enabled, presetID
}

func (g *Gateway) handleSetJailbreakModel(w http.ResponseWriter, r *http.Request) {
	var req bindingRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" || len(req.ID) > 300 {
		writeErr(w, http.StatusBadRequest, "invalid model id")
		return
	}
	enabled, presetID := req.resolve()
	if err := g.store.SetJailbreakBinding(req.ID, presetID, enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.ID, "enabled": enabled, "presetId": presetID})
}

func (g *Gateway) handleSetJailbreakKeyModel(w http.ResponseWriter, r *http.Request) {
	var req bindingRequest
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.KeyID == "" || len(req.KeyID) > 100 {
		writeErr(w, http.StatusBadRequest, "invalid api key id")
		return
	}
	if req.ID == "" || len(req.ID) > 300 {
		writeErr(w, http.StatusBadRequest, "invalid model id")
		return
	}
	enabled, presetID := req.resolve()
	if err := g.store.SetJailbreakBinding(req.KeyID+":"+req.ID, presetID, enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "keyId": req.KeyID, "id": req.ID, "enabled": enabled, "presetId": presetID})
}

func (g *Gateway) handleDeleteJailbreakModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ID) == "" || len(req.ID) > 300 {
		writeErr(w, http.StatusBadRequest, "invalid model id")
		return
	}
	if err := g.store.DeleteJailbreakBinding(req.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.ID})
}

func (g *Gateway) handleJailbreakModelCatalog(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, 0, 64)
	for _, m := range g.buildModelsList(nil) {
		if id, ok := m["id"].(string); ok && id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	writeJSON(w, http.StatusOK, map[string]any{"models": ids})
}

func (g *Gateway) handleGetJailbreakExcludes(w http.ResponseWriter, r *http.Request) {
	excludes, err := g.store.GetJailbreakExcludes()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"excludes": excludes})
}

func (g *Gateway) handlePutJailbreakExcludes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Excludes []store.JailbreakKeyword `json:"excludes"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := g.store.SaveJailbreakExcludes(req.Excludes); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	excludes, _ := g.store.GetJailbreakExcludes()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "excludes": excludes})
}

func (g *Gateway) handleResolveJailbreak(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string `json:"id"`
		APIKeyID string `json:"apiKeyId"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" || len(id) > 300 {
		writeErr(w, http.StatusBadRequest, "invalid model id")
		return
	}
	match, _ := g.store.ResolveJailbreak(strings.TrimSpace(req.APIKeyID), id)
	writeJSON(w, http.StatusOK, match)
}

func (g *Gateway) handleJailbreakUsage(w http.ResponseWriter, r *http.Request) {
	usage, err := g.store.JailbreakUsage()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}

func (g *Gateway) handlePreviewJailbreak(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID       string           `json:"id"`
		APIKeyID string           `json:"apiKeyId"`
		Messages []map[string]any `json:"messages"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	id := strings.TrimSpace(req.ID)
	if id == "" || len(id) > 300 {
		writeErr(w, http.StatusBadRequest, "invalid model id")
		return
	}
	match, doc := g.store.ResolveJailbreak(strings.TrimSpace(req.APIKeyID), id)
	out := map[string]any{"injected": match.Injected, "reason": match.Reason, "messages": req.Messages}
	if match.Injected && doc != nil && doc.Prompt != "" {
		msgs := append([]map[string]any{}, req.Messages...)
		switch {
		case doc.Mode == "chat":
			msgs = append(msgs, map[string]any{"role": "user", "content": doc.Prompt})
		case doc.Position == "append":
			msgs = append(msgs, map[string]any{"role": "system", "content": doc.Prompt})
		default:
			msgs = append([]map[string]any{{"role": "system", "content": doc.Prompt}}, msgs...)
		}
		out["presetId"] = doc.ID
		out["presetName"] = doc.Name
		out["prompt"] = doc.Prompt
		out["messages"] = msgs
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------- injection

// applyJailbreak injects the resolved preset into the outbound body. ids are
// the candidate model ids in specificity order; clientFormat only decides
// whether the Gemini safety switch can be expressed on this wire.
func (g *Gateway) applyJailbreak(raw map[string]any, apiKeyID string, ids []string, clientFormat string) {
	match, doc := g.store.ResolveJailbreak(apiKeyID, ids...)
	if !match.Injected || doc == nil || doc.Prompt == "" {
		return
	}
	switch {
	case doc.Mode == "chat":
		msgs, _ := raw["messages"].([]any)
		raw["messages"] = append(msgs, map[string]any{"role": "user", "content": doc.Prompt})
	case doc.Position == "append":
		appendSystemPrompt(raw, doc.Prompt)
	default:
		prependSystemPrompt(raw, doc.Prompt)
	}
	if doc.GeminiSafety {
		raw["safetySettings"] = geminiSafetyOff()
	}
	g.store.RecordJailbreakUsage(doc.ID)
	logf("jailbreak applied: model=%v preset=%s prompt_bytes=%d safety_off=%v", ids, doc.ID, len(doc.Prompt), doc.GeminiSafety)
}

// prependSystemPrompt puts the prompt ahead of the original content, on the
// OpenAI "messages" wire or the Claude top-level "system" field.
func prependSystemPrompt(body map[string]any, prompt string) {
	if raw, ok := body["system"]; ok {
		switch v := raw.(type) {
		case string:
			body["system"] = prompt + "\n\n" + v
			return
		case []any:
			body["system"] = append([]any{map[string]any{"type": "text", "text": prompt}}, v...)
			return
		}
	}
	msgs, _ := body["messages"].([]any)
	body["messages"] = append([]any{map[string]any{"role": "system", "content": prompt}}, msgs...)
}

// appendSystemPrompt puts the prompt after the original content instead.
func appendSystemPrompt(body map[string]any, prompt string) {
	if raw, ok := body["system"]; ok {
		switch v := raw.(type) {
		case string:
			body["system"] = v + "\n\n" + prompt
			return
		case []any:
			body["system"] = append(v, map[string]any{"type": "text", "text": prompt})
			return
		}
	}
	msgs, _ := body["messages"].([]any)
	body["messages"] = append(msgs, map[string]any{"role": "system", "content": prompt})
}

// geminiSafetyOff is the official safetySettings shape with every filter
// disabled; only providers that read it (Gemini family) are affected.
func geminiSafetyOff() []any {
	categories := []string{
		"HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT",
		"HARM_CATEGORY_CIVIC_INTEGRITY",
	}
	out := make([]any, 0, len(categories))
	for _, c := range categories {
		out = append(out, map[string]any{"category": c, "threshold": "BLOCK_NONE"})
	}
	return out
}

// jailbreakCandidateIDs lists the ids a request answers to: the id the client
// sent, then each forwarded leg as "provider/model" and bare model.
func (g *Gateway) jailbreakCandidateIDs(requested string, combo *store.Combo, targets []target) []string {
	ids := []string{requested}
	add := func(id string) {
		if id == "" {
			return
		}
		for _, seen := range ids {
			if seen == id {
				return
			}
		}
		ids = append(ids, id)
	}
	if combo != nil {
		for _, member := range g.flattenComboModels(combo.Name, combo.Models) {
			add(member)
			if _, model, ok := splitProviderModel(member); ok {
				add(model)
			}
		}
	}
	for _, t := range targets {
		add(t.Provider + "/" + t.Model)
		add(t.Model)
	}
	return ids
}

// ---------------------------------------------------------------- import / sync

func slugify(s string) string {
	s = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(s, ".md"), ".markdown"), ".txt")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
			b.WriteRune('-')
		}
	}
	res := strings.Trim(b.String(), "-")
	if res == "" {
		return fmt.Sprintf("preset-%d", time.Now().UnixNano())
	}
	return res
}

// jailbreakKindOf labels a preset by the instruction-file convention its name
// follows: AGENTS.md, SOUL.md, PERSONA.md and the rest read by name.
func jailbreakKindOf(name string) string {
	base := strings.ToUpper(strings.TrimSuffix(path.Base(name), path.Ext(name)))
	switch base {
	case "AGENTS", "SOUL", "PERSONA", "LOAD", "SYSTEM", "PROMPT", "CLAUDE", "GEMINI":
		return base
	}
	return ""
}

func (g *Gateway) jailbreakGithubConfig() store.JailbreakGithub {
	cfg, _ := g.store.GetJailbreakGithub()
	if cfg.APIBase == "" {
		cfg.APIBase = "https://api.github.com"
	}
	return cfg
}

func (g *Gateway) githubGet(ctx context.Context, endpoint string) ([]byte, error) {
	cfg := g.jailbreakGithubConfig()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "kinerouter")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// parseGithubURL accepts https://github.com/owner/repo[/tree/ref[/path]],
// owner/repo, a raw file URL, or a gist.
func parseGithubURL(raw string) (repo, ref, sub string, file string) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http") {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", "", ""
		}
		host := strings.ToLower(u.Hostname())
		switch {
		case host == "raw.githubusercontent.com" || host == "gist.githubusercontent.com":
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			return strings.Join(parts[:2], "/"), "", "", raw
		case host == "github.com":
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) < 2 {
				return "", "", "", ""
			}
			repo = parts[0] + "/" + parts[1]
			rest := parts[2:]
			if len(rest) >= 2 && (rest[0] == "tree" || rest[0] == "blob") {
				ref = rest[1]
				rest = rest[2:]
			} else {
				rest = nil
			}
			sub = strings.Join(rest, "/")
			if len(parts) > 2 && parts[2] == "blob" {
				file = raw
			}
			return repo, ref, sub, file
		}
		return "", "", "", ""
	}
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1], "", strings.Join(parts[2:], "/"), ""
	}
	return "", "", "", ""
}

type jailbreakFile struct {
	Path    string
	Content []byte
}

// fetchGithubPresetFiles reads every markdown instruction file in a repo
// subtree. A single-file URL returns just that file.
func (g *Gateway) fetchGithubPresetFiles(ctx context.Context, raw string) (string, []jailbreakFile, error) {
	cfg := g.jailbreakGithubConfig()
	repo, ref, sub, file := parseGithubURL(raw)
	if repo == "" {
		return "", nil, fmt.Errorf("unsupported URL: %s", raw)
	}
	if file != "" && strings.Contains(file, "/blob/") {
		content, err := g.githubContents(ctx, repo, path.Join(sub), ref)
		if err != nil {
			return "", nil, err
		}
		return repo, []jailbreakFile{{Path: sub, Content: content}}, nil
	}
	if file != "" {
		resp, err := (&http.Client{Timeout: 20 * time.Second}).Get(file)
		if err != nil {
			return "", nil, err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if resp.StatusCode != http.StatusOK {
			return "", nil, fmt.Errorf("fetch %s: HTTP %d", file, resp.StatusCode)
		}
		return repo, []jailbreakFile{{Path: path.Base(file), Content: body}}, nil
	}
	if ref == "" {
		ref = "HEAD"
	}
	treeURL := fmt.Sprintf("%s/repos/%s/git/trees/%s?recursive=1", strings.TrimRight(cfg.APIBase, "/"), repo, url.PathEscape(ref))
	rawTree, err := g.githubGet(ctx, treeURL)
	if err != nil {
		return "", nil, err
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.Unmarshal(rawTree, &tree); err != nil {
		return "", nil, err
	}
	out := []jailbreakFile{}
	for _, entry := range tree.Tree {
		if entry.Type != "blob" || !jailbreakPresetFile(entry.Path) {
			continue
		}
		if sub != "" && !strings.HasPrefix(entry.Path, strings.Trim(sub, "/")+"/") {
			continue
		}
		if len(out) >= maxJailbreakFiles {
			break
		}
		content, err := g.githubContents(ctx, repo, entry.Path, ref)
		if err != nil {
			continue
		}
		out = append(out, jailbreakFile{Path: entry.Path, Content: content})
	}
	if len(out) == 0 {
		return "", nil, fmt.Errorf("no markdown presets found in %s", raw)
	}
	return repo, out, nil
}

func jailbreakPresetFile(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".markdown", ".txt":
		return true
	}
	return false
}

func (g *Gateway) githubContents(ctx context.Context, repo, file, ref string) ([]byte, error) {
	cfg := g.jailbreakGithubConfig()
	endpoint := fmt.Sprintf("%s/repos/%s/contents/%s", strings.TrimRight(cfg.APIBase, "/"), repo, file)
	if ref != "" && ref != "HEAD" {
		endpoint += "?ref=" + url.QueryEscape(ref)
	}
	raw, err := g.githubGet(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	if payload.Encoding == "base64" {
		return base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	}
	return []byte(payload.Content), nil
}

func (g *Gateway) handleImportJailbreak(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Text) != "" {
		doc := &store.JailbreakPreset{
			ID: slugify(firstNonEmpty(req.Name, "imported")), Name: firstNonEmpty(req.Name, "Imported"),
			Prompt: req.Text, Kind: req.Kind, Source: req.Name,
		}
		if err := g.store.SaveJailbreakPreset(doc); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "imported": 1, "presets": []*store.JailbreakPreset{doc}})
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeErr(w, http.StatusBadRequest, "url or text is required")
		return
	}
	repo, files, err := g.fetchGithubPresetFiles(r.Context(), req.URL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	presets := make([]*store.JailbreakPreset, 0, len(files))
	for _, f := range files {
		name := path.Base(f.Path)
		doc := &store.JailbreakPreset{
			ID:   slugify(strings.TrimSuffix(strings.ReplaceAll(strings.TrimSuffix(f.Path, path.Ext(f.Path)), "/", "-"), "-")),
			Name: strings.TrimSuffix(name, path.Ext(name)), Prompt: string(f.Content),
			Source: name, Kind: jailbreakKindOf(f.Path), SyncOrigin: repo, SyncPath: f.Path,
		}
		if err := g.store.SaveJailbreakPreset(doc); err != nil {
			continue
		}
		presets = append(presets, doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "repo": repo, "imported": len(presets), "presets": presets})
}

func (g *Gateway) handleListJailbreakSources(w http.ResponseWriter, r *http.Request) {
	sources, err := g.store.GetJailbreakSources()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

func (g *Gateway) handleSaveJailbreakSources(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sources []store.JailbreakSyncSource `json:"sources"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Sources) > 50 {
		writeErr(w, http.StatusBadRequest, "too many sources")
		return
	}
	if err := g.store.SaveJailbreakSources(req.Sources); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sources": req.Sources})
}

func (g *Gateway) handleRunJailbreakSync(w http.ResponseWriter, r *http.Request) {
	sources, err := g.store.GetJailbreakSources()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type outcome struct {
		Source   string `json:"source"`
		Imported int    `json:"imported"`
		Removed  int    `json:"removed"`
		Error    string `json:"error,omitempty"`
	}
	out := []outcome{}
	for _, src := range sources {
		if !src.Enabled || src.Repo == "" {
			continue
		}
		o := outcome{Source: src.Repo}
		origin := src.Repo
		if src.Ref != "" {
			origin += "@" + src.Ref
		}
		target := src.Repo
		if src.Path != "" {
			target += "/tree/" + firstNonEmpty(src.Ref, "HEAD") + "/" + strings.Trim(src.Path, "/")
		}
		repo, files, err := g.fetchGithubPresetFiles(r.Context(), target)
		if err != nil {
			o.Error = err.Error()
			out = append(out, o)
			continue
		}
		seen := map[string]bool{}
		for _, f := range files {
			seen[f.Path] = true
			doc := &store.JailbreakPreset{
				ID:   slugify(strings.TrimSuffix(strings.ReplaceAll(strings.TrimSuffix(f.Path, path.Ext(f.Path)), "/", "-"), "-")),
				Name: strings.TrimSuffix(path.Base(f.Path), path.Ext(f.Path)), Prompt: string(f.Content),
				Source: path.Base(f.Path), Kind: jailbreakKindOf(f.Path), SyncOrigin: origin, SyncPath: f.Path,
			}
			if err := g.store.SaveJailbreakPreset(doc); err != nil {
				continue
			}
			o.Imported++
		}
		// Drop presets this source used to publish and no longer does. Only
		// rows carrying this origin are candidates, so operator edits survive.
		presets, _ := g.store.ListJailbreakPresets()
		for _, p := range presets {
			if p.SyncOrigin == origin && !seen[p.SyncPath] {
				if err := g.store.DeleteJailbreakPreset(p.ID); err == nil {
					o.Removed++
				}
			}
		}
		if repo != "" {
			o.Source = repo
		}
		out = append(out, o)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "results": out})
}

func (g *Gateway) handleGetJailbreakGithub(w http.ResponseWriter, r *http.Request) {
	cfg := g.jailbreakGithubConfig()
	writeJSON(w, http.StatusOK, map[string]any{"hasToken": cfg.Token != "", "apiBase": cfg.APIBase})
}

func (g *Gateway) handleSaveJailbreakGithub(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token   string `json:"token"`
		APIBase string `json:"apiBase"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, _ := g.store.GetJailbreakGithub()
	if strings.TrimSpace(req.Token) == "" || req.Token == "keep" {
		req.Token = existing.Token
	}
	if err := g.store.SaveJailbreakGithub(store.JailbreakGithub{Token: req.Token, APIBase: req.APIBase}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "hasToken": req.Token != ""})
}
