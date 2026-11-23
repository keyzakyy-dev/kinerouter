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
	"llm":           {"llm"},
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
// given kinds. Active connections and no-auth providers contribute the registry
// catalogue; custom model rows and node models are added on top.
func (g *Gateway) buildModelsList(kinds []string) []map[string]any {
	want := kindSetFilter(kinds)
	conns, _ := g.store.ListConnections("", false)
	have := map[string]bool{}
	for _, c := range conns {
		if c.IsActive {
			have[c.Provider] = true
		}
	}
	disabled, _ := g.store.DisabledModels()
	disabledOf := func(alias, id string) bool {
		for _, d := range disabled[alias] {
			if d == id || d == "*" {
				return true
			}
		}
		return false
	}
	data := []map[string]any{}
	for _, e := range g.reg.Entries {
		// A no-auth provider is listable without a stored connection, matching
		// upstream's injected virtual account.
		if !have[e.ID] && !(g.exec != nil && g.exec.IsNoAuth(e.ID)) {
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
			if disabledOf(e.Alias, m.ID) || disabledOf(e.ID, m.ID) {
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
	if want == nil || want["llm"] {
		data = g.appendCustomModels(data, have, disabledOf)
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

// appendCustomModels publishes kv customModels rows under the node prefix (or
// the provider alias), skipping disabled rows and aliases with no live
// connection. Mirrors appendLooseCustomModels.
func (g *Gateway) appendCustomModels(data []map[string]any, have map[string]bool, disabledOf func(alias, id string) bool) []map[string]any {
	models, err := g.store.ListCustomModels()
	if err != nil {
		return data
	}
	nodes, _ := g.store.ListProviderNodes()
	prefixOf := map[string]string{}
	for _, n := range nodes {
		if p, _ := n.Data["prefix"].(string); p != "" {
			prefixOf[n.ID] = p
		}
	}
	seen := map[string]bool{}
	for _, m := range data {
		if id, ok := m["id"].(string); ok {
			seen[id] = true
		}
	}
	for _, cm := range models {
		provider, _ := cm["providerAlias"].(string)
		id, _ := cm["id"].(string)
		if provider == "" || id == "" || !have[provider] {
			continue
		}
		if typ, _ := cm["type"].(string); typ != "" && typ != "llm" {
			continue
		}
		prefix := provider
		if p := prefixOf[provider]; p != "" {
			prefix = p
		}
		if disabledOf(provider, id) || disabledOf(prefix, id) {
			continue
		}
		fullID := prefix + "/" + id
		if seen[fullID] {
			continue
		}
		seen[fullID] = true
		entry := map[string]any{"id": fullID, "object": "model", "owned_by": prefix}
		if caps, ok := cm["caps"].(map[string]any); ok && len(caps) > 0 {
			entry["capabilities"] = caps
		}
		data = append(data, entry)
	}
	return data
}

// handleModelsByPath serves /v1/models/{kind} and /v1/models/{provider}/{model}.
func (g *Gateway) handleModelsByPath(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api")
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
