package gateway

// Capacity adapters. Ported from open-sse/services/capacityAdapter.js: global
// per-modality fallback pools (vision / pdf / audioInput / videoInput). When the
// request needs a capability none of the models about to be tried can serve, the
// pool's models are prepended as priority candidates; history is trimmed to the
// adapter model's context window before it is called.
//
// Capability and context numbers come from the synced models.dev catalog
// (catalogsync.go writes <DATA_DIR>/model-catalog.json: models keyed
// "provider:baseModelId" carry the modality flags, providers keyed by provider
// carry per-model "c" context). A model the catalog does not know falls back to
// the registry entry's contextLength and no modality flags — the safe floor.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultFallbackModel = "oc/mimo-v2.6-flash-free"

var hardCaps = []string{"vision", "pdf", "audioInput", "videoInput"}

// ------------------------------------------------------------------ catalog

type catalogCaps struct {
	Vision, PDF, AudioInput, VideoInput bool
}

var catalogCache struct {
	sync.Mutex
	mtime time.Time
	mods  map[string]catalogCaps
	ctx   map[string]int64 // "provider:model" -> context window
}

func (g *Gateway) loadCatalog() (map[string]catalogCaps, map[string]int64) {
	catalogCache.Lock()
	defer catalogCache.Unlock()
	st, err := os.Stat(g.catalogFile())
	if err != nil {
		return nil, nil
	}
	if catalogCache.mods != nil && !st.ModTime().After(catalogCache.mtime) {
		return catalogCache.mods, catalogCache.ctx
	}
	b, err := os.ReadFile(g.catalogFile())
	if err != nil {
		return catalogCache.mods, catalogCache.ctx
	}
	var f struct {
		Models    map[string]map[string]bool `json:"models"`
		Providers map[string]map[string]struct {
			C int64 `json:"c"`
		} `json:"providers"`
	}
	if json.Unmarshal(b, &f) != nil {
		return catalogCache.mods, catalogCache.ctx
	}
	mods := make(map[string]catalogCaps, len(f.Models))
	for key, m := range f.Models {
		mods[key] = catalogCaps{m["vision"], m["pdf"], m["audioInput"], m["videoInput"]}
	}
	ctx := map[string]int64{}
	for provider, models := range f.Providers {
		for model, v := range models {
			ctx[provider+":"+baseModelID(model)] = v.C
		}
	}
	catalogCache.mtime, catalogCache.mods, catalogCache.ctx = st.ModTime(), mods, ctx
	return mods, ctx
}

// capsFor resolves a "provider/model" string to its modality flags, reading the
// synchronized models.dev catalog first and the registry as the fallback floor.
func (g *Gateway) capsFor(modelStr string) catalogCaps {
	provider, model := splitModelRef(modelStr)
	mods, _ := g.loadCatalog()
	if c, ok := mods[provider+":"+baseModelID(model)]; ok {
		return c
	}
	// Registry models name their capabilities for image generation, not input
	// modalities; absent a catalog entry nothing extra is claimed.
	return catalogCaps{}
}

func (g *Gateway) contextWindowFor(modelStr string) int64 {
	provider, model := splitModelRef(modelStr)
	_, ctx := g.loadCatalog()
	if n, ok := ctx[provider+":"+baseModelID(model)]; ok && n > 0 {
		return n
	}
	if t, ok := g.reg.ByID[provider]; ok {
		for i := range t.Models {
			if t.Models[i].ID == model || t.Models[i].UpstreamModelID == model {
				return t.Models[i].ContextLength
			}
		}
	}
	return 0
}

func splitModelRef(modelStr string) (provider, model string) {
	if i := strings.IndexByte(modelStr, '/'); i > 0 {
		return modelStr[:i], modelStr[i+1:]
	}
	return "", modelStr
}

// ------------------------------------------------------- request detection

// detectRequiredCapabilities returns the hard modalities the current user turn
// carries. Only the trailing user run is scanned: history media must not pin the
// choice to a vision model.
func detectRequiredCapabilities(body map[string]any) map[string]bool {
	need := map[string]bool{}
	addByMime := func(mime string) {
		switch {
		case strings.HasPrefix(mime, "image/"):
			need["vision"] = true
		case mime == "application/pdf":
			need["pdf"] = true
		case strings.HasPrefix(mime, "audio/"):
			need["audioInput"] = true
		case strings.HasPrefix(mime, "video/"):
			need["videoInput"] = true
		}
	}
	var scanBlock func(map[string]any)
	scanBlock = func(b map[string]any) {
		switch b["type"] {
		case "image_url", "image", "input_image":
			need["vision"] = true
		case "input_audio", "audio_url", "audio":
			need["audioInput"] = true
		case "input_video", "video_url", "video":
			need["videoInput"] = true
		case "file", "document", "input_file":
			mime := ""
			if f, ok := b["file"].(map[string]any); ok {
				if s, ok := f["file_data"].(string); ok {
					mime = dataURIMime(s)
				}
			} else if src, ok := b["source"].(map[string]any); ok {
				mime, _ = src["media_type"].(string)
				if mime == "" {
					if s, ok := src["data"].(string); ok {
						mime = dataURIMime(s)
					}
				}
			}
			if mime != "" {
				addByMime(mime)
			} else {
				need["pdf"] = true
			}
		}
		if in, ok := b["inlineData"].(map[string]any); ok {
			if m, ok := in["mimeType"].(string); ok {
				addByMime(m)
			}
		}
		if fd, ok := b["fileData"].(map[string]any); ok {
			if m, ok := fd["mimeType"].(string); ok {
				addByMime(m)
			}
		}
	}
	scanContent := func(c any) {
		if arr, ok := c.([]any); ok {
			for _, it := range arr {
				if blk, ok := it.(map[string]any); ok {
					scanBlock(blk)
				}
			}
		}
	}
	scanMessage := func(m map[string]any) {
		if imgs, ok := m["images"].([]any); ok && len(imgs) > 0 {
			need["vision"] = true
		}
		for _, key := range []string{"experimental_attachments", "attachments"} {
			if atts, ok := m[key].([]any); ok {
				for _, a := range atts {
					att, ok := a.(map[string]any)
					if !ok {
						continue
					}
					mime, _ := att["contentType"].(string)
					if mime == "" {
						mime, _ = att["mediaType"].(string)
					}
					if mime == "" {
						if url, ok := att["url"].(string); ok {
							mime = dataURIMime(url)
						}
					}
					if mime != "" {
						addByMime(mime)
					} else {
						need["vision"] = true
					}
				}
			}
		}
		if m["image_url"] != nil || m["image"] != nil {
			need["vision"] = true
		}
		if m["audio_url"] != nil || m["audio"] != nil {
			need["audioInput"] = true
		}
		scanContent(m["content"])
		if s, ok := m["content"].(string); ok {
			switch {
			case strings.Contains(s, "data:image/"):
				need["vision"] = true
			case strings.Contains(s, "data:audio/"):
				need["audioInput"] = true
			case strings.Contains(s, "data:application/pdf"):
				need["pdf"] = true
			}
		}
	}

	for _, key := range []string{"messages", "input", "contents"} {
		arr, ok := body[key].([]any)
		if !ok {
			continue
		}
		for _, it := range trailingUserItems(arr) {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if key == "contents" {
				scanContent(m["parts"])
			} else if key == "input" {
				scanContent(m["content"])
			} else {
				scanMessage(m)
			}
		}
	}
	if req, ok := body["request"].(map[string]any); ok {
		if arr, ok := req["contents"].([]any); ok {
			for _, it := range trailingUserItems(arr) {
				if m, ok := it.(map[string]any); ok {
					scanContent(m["parts"])
				}
			}
		}
	}
	return need
}

func dataURIMime(s string) string {
	const p = "data:"
	if !strings.HasPrefix(s, p) {
		return ""
	}
	rest := s[len(p):]
	if i := strings.IndexAny(rest, ";,"); i >= 0 {
		return rest[:i]
	}
	return ""
}

func sysRole(v any) bool { r, _ := v.(string); return r == "system" || r == "developer" }

// trailingUserItems is everything after the last assistant/model turn.
func trailingUserItems(arr []any) []any {
	for i := len(arr) - 1; i >= 0; i-- {
		m, ok := arr[i].(map[string]any)
		if !ok {
			continue
		}
		if role, _ := m["role"].(string); role == "assistant" || role == "model" {
			return arr[i+1:]
		}
	}
	return arr
}

// -------------------------------------------------------------- adapter pools

type capEntry struct {
	enabled    bool
	roundRobin bool
	models     []string
}

func (g *Gateway) capEntry(cap string) capEntry {
	settings, err := g.store.GetSettings()
	if err != nil {
		return capEntry{}
	}
	all, _ := settings["capacityAdapter"].(map[string]any)
	raw, _ := all[cap].(map[string]any)
	if raw == nil {
		return capEntry{}
	}
	e := capEntry{enabled: true, roundRobin: raw["roundRobin"] == true}
	if v, ok := raw["enabled"].(bool); ok {
		e.enabled = v
	}
	if list, ok := raw["models"].([]any); ok {
		for _, m := range list {
			if s, ok := m.(string); ok && s != "" {
				if s == "oc/mimo-v2.5-free" {
					s = defaultFallbackModel
				}
				e.models = append(e.models, s)
			}
		}
	}
	if e.enabled && len(e.models) == 0 {
		e.models = []string{defaultFallbackModel}
	}
	return e
}

func (g *Gateway) adapterModels() []string {
	seen := map[string]bool{}
	var out []string
	for _, cap := range hardCaps {
		e := g.capEntry(cap)
		if !e.enabled {
			continue
		}
		for _, m := range e.models {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

func (g *Gateway) adapterStrategy(required map[string]bool) string {
	for _, cap := range hardCaps {
		if !required[cap] {
			continue
		}
		e := g.capEntry(cap)
		if !e.enabled || len(e.models) == 0 {
			continue
		}
		if e.roundRobin {
			return "round-robin"
		}
		return "fallback"
	}
	return "fallback"
}

func (g *Gateway) modelSatisfies(modelStr string, hard []string) bool {
	c := g.capsFor(modelStr)
	for _, cap := range hard {
		switch cap {
		case "vision":
			if !c.Vision {
				return false
			}
		case "pdf":
			if !c.PDF {
				return false
			}
		case "audioInput":
			if !c.AudioInput {
				return false
			}
		case "videoInput":
			if !c.VideoInput {
				return false
			}
		}
	}
	return true
}

// augmentWithCapacity prepends adapter models when none of models can serve the
// request's hard capabilities. Returns models unchanged otherwise.
func (g *Gateway) augmentWithCapacity(models []string, required map[string]bool) []string {
	var hard []string
	for _, cap := range hardCaps {
		if required[cap] {
			hard = append(hard, cap)
		}
	}
	if len(hard) == 0 || len(models) == 0 {
		return models
	}
	for _, m := range models {
		if g.modelSatisfies(m, hard) {
			return models
		}
	}
	have := map[string]bool{}
	for _, m := range models {
		have[m] = true
	}
	var pool []string
	for _, m := range g.adapterModels() {
		if !have[m] && g.modelSatisfies(m, hard) {
			pool = append(pool, m)
		}
	}
	if len(pool) == 0 {
		return models
	}
	return append(pool, models...)
}

// -------------------------------------------------------- history trimming

const (
	charsPerToken = 4
	headKeep      = 6
)

// stripHistoryForContext drops middle turns so the body fits contextWindow. It
// keeps every system message, the first few turns, and the trailing user run.
func stripHistoryForContext(body map[string]any, contextWindow int64) map[string]any {
	key := ""
	for _, k := range []string{"messages", "input", "contents"} {
		if _, ok := body[k].([]any); ok {
			key = k
			break
		}
	}
	if key == "" {
		return body
	}
	arr, _ := body[key].([]any)
	if len(arr) == 0 {
		return body
	}
	var systemMsgs, rest []any
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok && sysRole(m["role"]) {
			systemMsgs = append(systemMsgs, it)
		} else {
			rest = append(rest, it)
		}
	}
	if len(rest) == 0 {
		return body
	}
	lastAssistant := -1
	for i := len(rest) - 1; i >= 0; i-- {
		if m, ok := rest[i].(map[string]any); ok {
			if r, _ := m["role"].(string); r == "assistant" || r == "model" {
				lastAssistant = i
				break
			}
		}
	}
	tail := rest[lastAssistant+1:]
	older := rest[:lastAssistant+1]
	if len(older) == 0 {
		return body
	}
	budget := float64(contextWindow)
	if budget <= 0 {
		budget = 200000
	}
	budgetChars := budget * 0.8 * charsPerToken

	head := older
	if len(head) > headKeep {
		head = head[:headKeep]
	}
	total := 0.0
	for _, group := range [][]any{systemMsgs, head, tail} {
		for _, it := range group {
			total += blockLength(it)
		}
	}
	for total > budgetChars && len(head) > 0 {
		total -= blockLength(head[len(head)-1])
		head = head[:len(head)-1]
	}
	if len(head) == len(older) {
		return body
	}
	body[key] = append(append(append([]any{}, systemMsgs...), head...), tail...)
	return body
}

func blockLength(item any) float64 {
	m, ok := item.(map[string]any)
	if !ok {
		return 0
	}
	switch c := m["content"].(type) {
	case string:
		return float64(len(c))
	case []any:
		total := 0.0
		for _, b := range c {
			if blk, ok := b.(map[string]any); ok {
				if s, ok := blk["text"].(string); ok {
					total += float64(len(s))
					continue
				}
			}
			total += 50
		}
		return total
	}
	return 0
}

// isAdapterModel reports whether provider/model is in an enabled adapter pool.
func (g *Gateway) isAdapterModel(provider, model string) bool {
	ref := provider + "/" + model
	for _, m := range g.adapterModels() {
		if m == ref {
			return true
		}
	}
	return false
}
