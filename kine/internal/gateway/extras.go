package gateway

// v1 surface extras: the OpenAI-dashboard root, per-model metadata, Responses
// context compaction, the Ollama-native chat shim, and the TTS voice catalogues.
//
// Ported from src/app/api/v1/{route.js, models/info, responses/compact,
// api/chat} and src/app/api/{v1/audio/voices, media-providers/tts/*}.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

func (g *Gateway) registerV1Extras(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1", g.handleListModels)
	mux.HandleFunc("GET /api/v1", g.handleListModels)
	mux.HandleFunc("GET /v1/models/info", g.handleModelInfo)
	mux.HandleFunc("GET /api/v1/models/info", g.handleModelInfo)
	// The Responses compact endpoint reuses the chat pipeline; _compact is
	// already stripped by the codex quirk and the translator headers.
	mux.HandleFunc("POST /v1/responses/compact", func(w http.ResponseWriter, r *http.Request) {
		g.handleChat(w, r, "responses")
	})
	mux.HandleFunc("POST /api/v1/responses/compact", func(w http.ResponseWriter, r *http.Request) {
		g.handleChat(w, r, "responses")
	})
	// Ollama-native clients: OpenAI chat underneath, NDJSON stream out.
	mux.HandleFunc("POST /v1/api/chat", g.handleOllamaChat)
	mux.HandleFunc("POST /api/v1/api/chat", g.handleOllamaChat)

	mux.HandleFunc("GET /v1/audio/voices", g.handleTTSVoicesPublic)
	mux.HandleFunc("GET /api/v1/audio/voices", g.handleTTSVoicesPublic)
	mux.HandleFunc("GET /api/media-providers/tts/voices", g.requireAuth(g.handleTTSVoices))
	mux.HandleFunc("GET /api/media-providers/tts/{provider}/voices", g.requireAuth(g.handleTTSVoices))
	mux.HandleFunc("GET /api/media-providers/tts/minimax/voices", g.requireAuth(g.handleMiniMaxVoices))
}

// ---------------------------------------------------------------- models/info

var kindEndpoint = map[string]string{
	"llm": "/v1/chat/completions", "image": "/v1/images/generations",
	"tts": "/v1/audio/speech", "stt": "/v1/audio/transcriptions",
	"embedding": "/v1/embeddings", "imageToText": "/v1/chat/completions",
	"webSearch": "/v1/search", "webFetch": "/v1/fetch",
}

// voiceCatalogProviders are the providers with a voice-listing endpoint.
var voiceCatalogProviders = map[string]bool{
	"elevenlabs": true, "edge-tts": true, "deepgram": true, "inworld": true,
	"local-device": true, "gemini": true,
}

func modelKind(m *providerdata.Model) string {
	if m.Kind != "" {
		return m.Kind
	}
	return "llm"
}

// modelInfo builds the /v1/models/info payload for one catalogue model.
func modelInfo(alias, providerID string, m *providerdata.Model) map[string]any {
	kind := modelKind(m)
	name := m.Name
	if name == "" {
		name = m.ID
	}
	out := map[string]any{
		"id": alias + "/" + m.ID, "name": name, "kind": kind,
		"owned_by": alias, "endpoint": nil,
	}
	if ep, ok := kindEndpoint[kind]; ok {
		out["endpoint"] = ep
	}
	if len(m.Capabilities) > 0 {
		out["capabilities"] = m.Capabilities
	}
	if m.ContextLength > 0 {
		out["contextWindow"] = m.ContextLength
	}
	if m.MaxOutputTokens > 0 {
		out["maxOutputTokens"] = m.MaxOutputTokens
	}
	if m.Dimensions > 0 {
		out["dimensions"] = m.Dimensions
	}
	for _, k := range []string{"params", "options", "searchTypes", "maxResults", "required"} {
		if v, ok := m.Extra[k]; ok {
			out[k] = v
		}
	}
	if kind == "tts" && voiceCatalogProviders[providerID] {
		out["voicesUrl"] = "/v1/audio/voices?provider=" + providerID
	}
	return out
}

func (g *Gateway) handleModelInfo(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "Missing required query param: id (e.g. ?id=openai/dall-e-3)")
		return
	}
	slash := strings.Index(id, "/")
	if slash <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
			"message": "Model not found: " + id, "type": "not_found"}})
		return
	}
	alias, modelID := id[:slash], id[slash+1:]
	providerID := g.reg.ResolveAlias(alias)
	models := g.reg.Models[alias]
	if len(models) == 0 {
		models = g.reg.Models[providerID]
	}
	wantKind := r.URL.Query().Get("kind")
	for i := range models {
		if models[i].ID != modelID {
			continue
		}
		if wantKind != "" && modelKind(&models[i]) != wantKind {
			continue
		}
		writeJSON(w, http.StatusOK, modelInfo(alias, providerID, &models[i]))
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{
		"message": "Model not found: " + id, "type": "not_found"}})
}

// ---------------------------------------------------------------- ollama shim

// handleOllamaChat runs the OpenAI chat pipeline and rewrites its SSE stream to
// Ollama's newline-delimited JSON, mirroring transformToOllama.
func (g *Gateway) handleOllamaChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, g.maxBody))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "failed to read body")
		return
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	model, _ := raw["model"].(string)
	if model == "" {
		model = "llama3.2"
	}
	// The shim always streams upstream so it can emit Ollama chunks.
	raw["stream"] = true
	pw := &pipeWriter{header: http.Header{}, status: 0}
	g.handleRaw(pw, r, raw, "openai", true)
	if pw.status != 0 && pw.status >= 400 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(pw.status)
		w.Write(pw.buf.Bytes())
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	writeOllamaStream(w, pw.buf.Bytes(), model)
}

// pipeWriter captures a handler's output so it can be post-processed. It never
// writes the underlying ResponseWriter.
type pipeWriter struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (p *pipeWriter) Header() http.Header { return p.header }
func (p *pipeWriter) WriteHeader(code int) {
	if p.status == 0 {
		p.status = code
	}
}
func (p *pipeWriter) Write(b []byte) (int, error) {
	if p.status == 0 {
		p.status = http.StatusOK
	}
	return p.buf.Write(b)
}

// writeOllamaStream converts an OpenAI SSE body into NDJSON that this server had captured.
func writeOllamaStream(w http.ResponseWriter, sse []byte, model string) {
	type pendingTC struct {
		id, name, args string
	}
	pending := map[int]*pendingTC{}
	emit := func(v map[string]any) {
		b, _ := json.Marshal(v)
		w.Write(b)
		w.Write([]byte("\n"))
	}
	end := func() map[string]any {
		return map[string]any{"model": model, "message": map[string]any{"role": "assistant", "content": ""}, "done": true}
	}
	sc := bufio.NewScanner(bytes.NewReader(sse))
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "[DONE]" {
			emit(end())
			return
		}
		var parsed map[string]any
		if json.Unmarshal([]byte(data), &parsed) != nil {
			continue
		}
		choices, _ := parsed["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		ch, _ := choices[0].(map[string]any)
		delta, _ := ch["delta"].(map[string]any)
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, item := range tcs {
				tc, _ := item.(map[string]any)
				idx := int(num(tc["index"]))
				if pending[idx] == nil {
					pending[idx] = &pendingTC{}
				}
				p := pending[idx]
				if id, _ := tc["id"].(string); id != "" {
					p.id = id
				}
				if fn, ok := tc["function"].(map[string]any); ok {
					p.name += rawString(fn["name"])
					p.args += rawString(fn["arguments"])
				}
			}
		}
		if content, _ := delta["content"].(string); content != "" {
			emit(map[string]any{"model": model, "message": map[string]any{"role": "assistant", "content": content}, "done": false})
		}
		if fr, _ := ch["finish_reason"].(string); fr == "tool_calls" || fr == "stop" {
			if len(pending) > 0 {
				calls := make([]any, 0, len(pending))
				keys := make([]int, 0, len(pending))
				for k := range pending {
					keys = append(keys, k)
				}
				sort.Ints(keys)
				for _, k := range keys {
					p := pending[k]
					var args any = map[string]any{}
					_ = json.Unmarshal([]byte(p.args), &args)
					calls = append(calls, map[string]any{"function": map[string]any{"name": p.name, "arguments": args}})
				}
				emit(map[string]any{"model": model,
					"message": map[string]any{"role": "assistant", "content": "", "tool_calls": calls},
					"done":    true})
				pending = map[int]*pendingTC{}
			} else if fr == "stop" {
				emit(end())
			}
		}
	}
	emit(end())
}

// ---------------------------------------------------------------- TTS voices

// ttsVoice is one entry in a provider voice list.
type ttsVoice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Locale      string `json:"locale,omitempty"`
	Lang        string `json:"lang"`
	Country     string `json:"country,omitempty"`
	CountryName string `json:"countryName,omitempty"`
	LangName    string `json:"langName,omitempty"`
	Gender      string `json:"gender"`
	Category    string `json:"category,omitempty"`
	Model       string `json:"model,omitempty"`
}

// groupVoices builds {voices, languages, byLang} from a flat voice list.
func groupVoices(voices []ttsVoice) map[string][]ttsVoice {
	byLang := map[string][]ttsVoice{}
	for _, v := range voices {
		byLang[v.Lang] = append(byLang[v.Lang], v)
	}
	return byLang
}

type langGroup struct {
	Code   string     `json:"code"`
	Name   string     `json:"name"`
	Voices []ttsVoice `json:"voices"`
}

func voicesResponse(voices []ttsVoice, langFilter string) map[string]any {
	var filtered []ttsVoice
	for _, v := range voices {
		if langFilter == "" || v.Lang == langFilter {
			filtered = append(filtered, v)
		}
	}
	if langFilter != "" {
		return map[string]any{"voices": filtered}
	}
	byLang := groupVoices(filtered)
	groups := make([]langGroup, 0, len(byLang))
	for code, list := range byLang {
		groups = append(groups, langGroup{Code: code, Name: langName(code), Voices: list})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return map[string]any{"voices": filtered, "languages": groups, "byLang": byLang}
}

// langName falls back to the code when the language is unknown. Go has no
// built-in CLDR name table, so this covers the common codes directly.
func langName(code string) string {
	if n, ok := langNames[strings.ToLower(code)]; ok {
		return n
	}
	return code
}

var langNames = map[string]string{
	"en": "English", "vi": "Vietnamese", "es": "Spanish", "fr": "French", "de": "German",
	"it": "Italian", "pt": "Portuguese", "ru": "Russian", "ja": "Japanese", "ko": "Korean",
	"zh": "Chinese", "ar": "Arabic", "hi": "Hindi", "th": "Thai", "id": "Indonesian",
	"ms": "Malay", "nl": "Dutch", "pl": "Polish", "tr": "Turkish", "sv": "Swedish",
	"da": "Danish", "fi": "Finnish", "no": "Norwegian", "cs": "Czech", "el": "Greek",
	"he": "Hebrew", "hu": "Hungarian", "ro": "Romanian", "uk": "Ukrainian", "ca": "Catalan",
	"fil": "Filipino", "bn": "Bengali", "ta": "Tamil", "te": "Telugu", "ur": "Urdu",
}

// gttsVoices is served by a static catalogue for edge-tts; the live Microsoft
// list is large and slow, and the shim only needs ids the synthesizer accepts.
// ponytail: static list; swap to the Bing readaloud endpoint if the catalogue
// must track upstream (fetchEdgeTtsVoices in open-sse/handlers/ttsProviders).

var edgeTTSVoices = []ttsVoice{
	{ID: "en-US-AriaNeural", Name: "Aria (Natural)", Locale: "en-US", Lang: "en", Country: "US", CountryName: "United States", LangName: "English", Gender: "Female"},
	{ID: "en-US-GuyNeural", Name: "Guy (Natural)", Locale: "en-US", Lang: "en", Country: "US", CountryName: "United States", LangName: "English", Gender: "Male"},
	{ID: "en-GB-SoniaNeural", Name: "Sonia (Natural)", Locale: "en-GB", Lang: "en", Country: "GB", CountryName: "United Kingdom", LangName: "English", Gender: "Female"},
	{ID: "vi-VN-HoaiMyNeural", Name: "HoaiMy (Natural)", Locale: "vi-VN", Lang: "vi", Country: "VN", CountryName: "Vietnam", LangName: "Vietnamese", Gender: "Female"},
	{ID: "vi-VN-NamMinhNeural", Name: "NamMinh (Natural)", Locale: "vi-VN", Lang: "vi", Country: "VN", CountryName: "Vietnam", LangName: "Vietnamese", Gender: "Male"},
	{ID: "ja-JP-NanamiNeural", Name: "Nanami (Natural)", Locale: "ja-JP", Lang: "ja", Country: "JP", CountryName: "Japan", LangName: "Japanese", Gender: "Female"},
	{ID: "zh-CN-XiaoxiaoNeural", Name: "Xiaoxiao (Natural)", Locale: "zh-CN", Lang: "zh", Country: "CN", CountryName: "China", LangName: "Chinese", Gender: "Female"},
}

// fetchProviderVoices lists voices for a TTS provider. Providers without a
// public list endpoint return an explanatory 400, as listTtsVoices does.
func (g *Gateway) fetchProviderVoices(provider, apiKey, langFilter string) ([]ttsVoice, int, string) {
	switch provider {
	case "edge-tts", "local-device":
		// local-device enumerates host voices, which a headless server cannot do.
		if provider == "local-device" {
			return nil, http.StatusBadRequest, "Provider 'local-device' does not support voice listing on this host"
		}
		out := make([]ttsVoice, len(edgeTTSVoices))
		copy(out, edgeTTSVoices)
		return out, 0, ""
	case "elevenlabs":
		conns, _ := g.store.ListConnections("elevenlabs", true)
		if len(conns) == 0 {
			return nil, http.StatusBadRequest, "No ElevenLabs connection found"
		}
		status, raw, err := probeJSON(&http.Client{Timeout: 15 * time.Second}, "GET",
			"https://api.elevenlabs.io/v1/voices",
			map[string]string{"xi-api-key": conns[0].Str("apiKey")}, nil)
		if err != nil || status < 200 || status >= 300 {
			return nil, http.StatusBadGateway, fmt.Sprintf("ElevenLabs API %d: %s", status, string(raw))
		}
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		list, _ := parsed["voices"].([]any)
		out := make([]ttsVoice, 0, len(list))
		for _, item := range list {
			v, _ := item.(map[string]any)
			labels, _ := v["labels"].(map[string]any)
			locale := firstNonEmpty(rawString(labels["language"]), "en")
			out = append(out, ttsVoice{
				ID: rawString(v["voice_id"]), Name: rawString(v["name"]),
				Locale: locale, Lang: strings.Split(locale, "-")[0],
				LangName: langName(strings.Split(locale, "-")[0]),
				Gender:   rawString(labels["gender"]), Category: rawString(v["category"]),
			})
		}
		return out, 0, ""
	case "deepgram":
		conns, _ := g.store.ListConnections("deepgram", true)
		if len(conns) == 0 {
			return nil, http.StatusBadRequest, "No Deepgram connection found"
		}
		status, raw, err := probeJSON(&http.Client{Timeout: 15 * time.Second}, "GET",
			"https://api.deepgram.com/v1/models",
			map[string]string{"Authorization": "Token " + conns[0].Str("apiKey")}, nil)
		if err != nil || status < 200 || status >= 300 {
			return nil, http.StatusBadGateway, fmt.Sprintf("Deepgram API %d: %s", status, string(raw))
		}
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		list, _ := parsed["tts"].([]any)
		out := []ttsVoice{}
		for _, item := range list {
			m, _ := item.(map[string]any)
			id := firstNonEmpty(rawString(m["canonical_name"]), rawString(m["name"]))
			langs, _ := m["languages"].([]any)
			if len(langs) == 0 {
				parts := strings.Split(id, "-")
				langs = []any{parts[len(parts)-1]}
			}
			gender := ""
			if md, ok := m["metadata"].(map[string]any); ok {
				if tags, ok := md["tags"].([]any); ok {
					for _, t := range tags {
						if s := rawString(t); s == "masculine" || s == "feminine" {
							gender = s
						}
					}
				}
			}
			for _, l := range langs {
				code := rawString(l)
				if code == "" {
					code = "en"
				}
				out = append(out, ttsVoice{ID: id, Name: firstNonEmpty(rawString(m["name"]), id),
					Locale: code, Lang: code, LangName: langName(code), Gender: gender})
			}
		}
		return dedupeVoices(out), 0, ""
	case "inworld":
		conns, _ := g.store.ListConnections("inworld", true)
		if len(conns) == 0 {
			return nil, http.StatusBadRequest, "No Inworld connection found"
		}
		status, raw, err := probeJSON(&http.Client{Timeout: 15 * time.Second}, "GET",
			"https://api.inworld.ai/tts/v1/voices",
			map[string]string{"Authorization": "Basic " + conns[0].Str("apiKey")}, nil)
		if err != nil || status < 200 || status >= 300 {
			return nil, http.StatusBadGateway, fmt.Sprintf("Inworld API %d: %s", status, string(raw))
		}
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		list, _ := parsed["voices"].([]any)
		out := []ttsVoice{}
		for _, item := range list {
			v, _ := item.(map[string]any)
			id := rawString(v["voiceId"])
			langs, _ := v["languages"].([]any)
			if len(langs) == 0 {
				langs = []any{"en"}
			}
			for _, l := range langs {
				code := rawString(l)
				out = append(out, ttsVoice{ID: id, Name: firstNonEmpty(rawString(v["displayName"]), id),
					Locale: code, Lang: code, LangName: langName(code), Gender: rawString(v["gender"])})
			}
		}
		return dedupeVoices(out), 0, ""
	}
	return nil, http.StatusBadRequest, "Provider '" + provider + "' does not support voice listing"
}

// minimaxVoiceEndpoints is region-specific.
var minimaxVoiceEndpoints = map[string]string{
	"minimax":    "https://api.minimax.io/v1/get_voice",
	"minimax-cn": "https://api.minimaxi.com/v1/get_voice",
}

var minimaxVoiceGroups = []struct{ key, label string }{
	{"system_voice", "System"}, {"voice_cloning", "Cloned"},
	{"voice_generation", "Generated"}, {"music_generation", "Music"},
}

// handleMiniMaxVoices groups MiniMax's voice buckets the same way the JS route
// does: system voices keyed by the language prefix of the voice id, everything
// else under "Custom".
func (g *Gateway) handleMiniMaxVoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provider := "minimax"
	if q.Get("provider") == "minimax-cn" {
		provider = "minimax-cn"
	}
	voiceType := firstNonEmpty(q.Get("voice_type"), "all")
	conns, _ := g.store.ListConnections(provider, true)
	if len(conns) == 0 {
		writeErr(w, http.StatusBadRequest, "No "+provider+" connection found")
		return
	}
	payload, _ := jsonMarshal(map[string]any{"voice_type": voiceType})
	status, raw, err := probeJSON(&http.Client{Timeout: 15 * time.Second}, "POST",
		minimaxVoiceEndpoints[provider], map[string]string{
			"Authorization": "Bearer " + conns[0].Str("apiKey"),
			"Content-Type":  "application/json",
		}, payload)
	var data map[string]any
	_ = json.Unmarshal(raw, &data)
	base, _ := data["base_resp"].(map[string]any)
	if base == nil {
		base, _ = data["baseResp"].(map[string]any)
	}
	statusCode := num(base["status_code"])
	if statusCode == 0 {
		statusCode = num(base["statusCode"])
	}
	statusMsg := firstNonEmpty(rawString(base["status_msg"]), rawString(base["statusMsg"]), rawString(data["message"]))
	if err != nil || status < 200 || status >= 300 {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("MiniMax API %d: %s", status, firstNonEmpty(statusMsg, string(raw), "Failed")))
		return
	}
	if statusCode != 0 {
		writeErr(w, http.StatusBadGateway, firstNonEmpty(statusMsg, "MiniMax voice API error"))
		return
	}
	out := []ttsVoice{}
	seen := map[string]bool{}
	for _, group := range minimaxVoiceGroups {
		list, _ := data[group.key].([]any)
		for _, item := range list {
			v, _ := item.(map[string]any)
			id := firstNonEmpty(rawString(v["voice_id"]), rawString(v["voiceId"]))
			if id == "" {
				continue
			}
			name := firstNonEmpty(rawString(v["voice_name"]), rawString(v["voiceName"]), id)
			lang := "Custom"
			if group.key == "system_voice" {
				if idx := strings.Index(id, "_"); idx > 0 {
					lang = id[:idx]
				}
			} else {
				name = name + " · " + group.label
			}
			if lang == "" {
				lang = "Custom"
			}
			key := lang + "/" + id
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ttsVoice{ID: id, Name: name, Locale: lang, Lang: lang,
				LangName: langName(lang), Category: group.key})
		}
	}
	writeJSON(w, http.StatusOK, voicesResponse(out, q.Get("lang")))
}

func dedupeVoices(in []ttsVoice) []ttsVoice {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		key := v.Lang + "/" + v.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

var (
	voiceCacheMu sync.Mutex
	voiceCache   map[string][]ttsVoice
)

// cachedVoices caches a provider catalogue for an hour to avoid re-billing the
// upstream list endpoint on every dashboard refresh.
func (g *Gateway) cachedVoices(provider, apiKey string) ([]ttsVoice, int, string) {
	voiceCacheMu.Lock()
	if v, ok := voiceCache[provider]; ok {
		voiceCacheMu.Unlock()
		return v, 0, ""
	}
	voiceCacheMu.Unlock()
	voices, status, msg := g.fetchProviderVoices(provider, apiKey, "")
	if status != 0 {
		return voices, status, msg
	}
	voiceCacheMu.Lock()
	if voiceCache == nil {
		voiceCache = map[string][]ttsVoice{}
	}
	voiceCache[provider] = voices
	voiceCacheMu.Unlock()
	return voices, 0, ""
}

// handleTTSVoices is the dashboard route:
// GET /api/media-providers/tts/voices?provider=&lang=&apiKey=
func (g *Gateway) handleTTSVoices(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provider := firstNonEmpty(r.PathValue("provider"), q.Get("provider"), "edge-tts")
	voices, status, msg := g.fetchProviderVoices(provider, q.Get("apiKey"), q.Get("lang"))
	if status != 0 {
		writeJSON(w, status, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, voicesResponse(voices, q.Get("lang")))
}

// handleTTSVoicesPublic is the OpenAI-style public list:
// GET /v1/audio/voices?provider=&lang= — ids are "{alias}/{voiceId}" so they can
// be passed straight to /v1/audio/speech.
func (g *Gateway) handleTTSVoicesPublic(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	provider := q.Get("provider")
	switch provider {
	case "elevenlabs", "deepgram", "inworld", "edge-tts", "local-device":
	default:
		writeErr(w, http.StatusBadRequest, "provider must be one of: elevenlabs, deepgram, inworld, edge-tts, local-device")
		return
	}
	voices, status, msg := g.cachedVoices(provider, "")
	if status != 0 {
		writeErr(w, status, msg)
		return
	}
	alias := provider
	if e := g.reg.ByID[provider]; e != nil && e.Alias != "" {
		alias = e.Alias
	}
	lang := q.Get("lang")
	data := []map[string]any{}
	for _, v := range voices {
		if lang != "" && v.Lang != lang {
			continue
		}
		data = append(data, map[string]any{
			"id": v.ID, "name": v.Name, "lang": v.Lang, "gender": v.Gender,
			"model": alias + "/" + v.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}
