package gateway

// /v1/models catalog: the kind-filtered list and the single-model lookup.
// Ported from src/app/api/v1/models/route.js buildModelsList and
// src/app/api/v1/models/[...model]/route.js.
//
// ponytail: the live per-provider catalogue resolvers (kiro/qoder/kimchi/
// copilot/cursor/grok-cli/...) are not ported; the registry catalogue is the
// source of truth here. Add a resolver when a provider's live list diverges.

import (
	"net/http"
	"strings"
)

// kindSlug maps a /v1/models/{kind} slug to the kinds it exposes.
var kindSlug = map[string][]string{
	"image":         {"image"},
	"tts":           {"tts"},
	"stt":           {"stt"},
	"embedding":     {"embedding"},
	"image-to-text": {"imageToText"},
	"web":           {"webSearch", "webFetch"},
}

func kindSetFilter(kinds []string) map[string]bool {
	if len(kinds) == 0 {
		return nil
	}
	set := map[string]bool{}
	for _, k := range kinds {
		set[k] = true
	}
	return set
}

// buildModelsList lists every model the router can serve, optionally only the
// given kinds. Only active connections contribute, matching buildModelsList.
func (g *Gateway) buildModelsList(kinds []string) []map[string]any {
	want := kindSetFilter(kinds)
	conns, _ := g.store.ListConnections("", false)
	have := map[string]bool{}
	for _, c := range conns {
		if c.IsActive {
			have[c.Provider] = true
		}
	}
	data := []map[string]any{}
	for _, e := range g.reg.Entries {
		if !have[e.ID] {
			continue
		}
		models := g.reg.Models[e.Alias]
		if len(models) == 0 {
			models = e.Models
		}
		for i := range models {
			m := &models[i]
			kind := modelKind(m)
			if want != nil && !want[kind] {
				continue
			}
			entry := map[string]any{
				"id": e.Alias + "/" + m.ID, "object": "model", "owned_by": e.Alias,
			}
			if len(m.Capabilities) > 0 {
				entry["capabilities"] = m.Capabilities
			}
			if m.ContextLength > 0 {
				entry["context_length"] = m.ContextLength
			}
			if m.MaxOutputTokens > 0 {
				entry["max_completion_tokens"] = m.MaxOutputTokens
			}
			data = append(data, entry)
		}
	}
	combos, _ := g.store.ListCombos()
	for _, c := range combos {
		kind := c.Kind
		if kind == "" {
			kind = "llm"
		}
		if want != nil && !want[kind] {
			continue
		}
		entry := map[string]any{"id": c.Name, "object": "model", "owned_by": "combo"}
		if kind == "webSearch" || kind == "webFetch" {
			entry["kind"] = kind
		}
		data = append(data, entry)
	}
	if want == nil || want["llm"] {
		aliases, _ := g.store.GetModelAliases()
		for alias := range aliases {
			data = append(data, map[string]any{"id": alias, "object": "model", "owned_by": "alias"})
		}
	}
	return data
}

// handleModelsByPath serves /v1/models/{kind} and /v1/models/{provider}/{model}.
func (g *Gateway) handleModelsByPath(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api"), "/")
	id = strings.TrimPrefix(id, "/v1/models/")
	if id == "" || id == "models" {
		g.handleListModels(w, r)
		return
	}
	segs := strings.Split(id, "/")
	if kinds, ok := kindSlug[id]; ok && len(segs) == 1 {
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": g.buildModelsList(kinds)})
		return
	}
	for _, m := range g.buildModelsList([]string{"llm"}) {
		if m["id"] == id {
			writeJSON(w, http.StatusOK, m)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
		"message": "The model '" + id + "' does not exist or you do not have access to it.",
		"type":    "invalid_request_error", "code": "model_not_found"}})
}
