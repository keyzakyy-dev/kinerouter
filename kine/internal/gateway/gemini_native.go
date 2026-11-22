package gateway

// Gemini-native client surface: /v1beta/models/{model}:generateContent and
// :streamGenerateContent. The Gemini body pivots to Chat Completions, runs the
// normal dispatch, and the answer is rewritten back to the Gemini wire shape.
// Mirrors src/app/api/v1beta/models/[...path]/route.js.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

func (g *Gateway) registerGeminiRoutes(mux *http.ServeMux) {
	// next.config.mjs rewrites /v1beta/* to /api/v1beta/*; both forms must answer.
	for _, p := range []string{"/v1beta", "/api/v1beta"} {
		mux.HandleFunc("POST "+p+"/models/{path...}", g.handleGeminiGenerate)
		mux.HandleFunc("OPTIONS "+p+"/{path...}", corsPreflight)
		mux.HandleFunc("GET "+p+"/models", g.handleGeminiModels)
	}
}

// handleGeminiGenerate accepts `model:action` or `provider/model:action`.
func (g *Gateway) handleGeminiGenerate(w http.ResponseWriter, r *http.Request) {
	spec := r.PathValue("path")
	action := ":generateContent"
	if strings.Contains(spec, ":streamGenerateContent") {
		action = ":streamGenerateContent"
	}
	model := strings.TrimSuffix(strings.TrimSuffix(spec, ":streamGenerateContent"), ":generateContent")
	// A leading "models/" is optional in the Gemini SDK path form.
	model = strings.TrimPrefix(model, "models/")
	if model == "" {
		writeErr(w, http.StatusBadRequest, "Missing model")
		return
	}
	stream := action == ":streamGenerateContent"

	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(r.Body, g.maxBody)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	chatBody := translate.GeminiRequestToChat(body, model, stream)
	g.geminiAnswer(w, r, chatBody, stream)
}

// geminiAnswer runs the chat pipeline and rewrites the answer as Gemini wire.
func (g *Gateway) geminiAnswer(w http.ResponseWriter, r *http.Request, raw map[string]any, stream bool) {
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		gw := &geminiSSE{ResponseWriter: w, conv: &translate.OpenAIToGemini{}}
		g.handleRaw(gw, r, raw, translate.FormatOpenAI, true)
		return
	}
	rec := &bufferedWriter{header: http.Header{}}
	g.handleRaw(rec, r, raw, translate.FormatOpenAI, false)
	out := rec.body.Bytes()
	var m map[string]any
	if json.Unmarshal(out, &m) == nil {
		if rec.status >= 400 {
			out = mustJSON(geminiError(m))
		} else {
			out = mustJSON(translate.OpenAIToGeminiResponse(m))
		}
	}
	for k, v := range rec.header {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	w.WriteHeader(rec.status)
	w.Write(out)
}

// geminiError rewraps a dashboard error body as a Gemini error envelope.
func geminiError(m map[string]any) map[string]any {
	msg, _ := m["error"].(string)
	if msg == "" {
		msg = string(mustJSON(m))
	}
	return map[string]any{"error": map[string]any{"message": msg, "code": 400}}
}

// geminiSSE rewrites the OpenAI SSE stream handleChat writes into Gemini SSE
// chunks. Gemini streams end at connection close, so [DONE] is dropped.
type geminiSSE struct {
	http.ResponseWriter
	conv *translate.OpenAIToGemini
	buf  []byte
}

func (s *geminiSSE) Write(p []byte) (int, error) {
	n := len(p)
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := bytes.TrimSpace(s.buf[:i])
		s.buf = s.buf[i+1:]
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var chunk map[string]any
		if json.Unmarshal(data, &chunk) != nil {
			continue
		}
		for _, ev := range s.conv.Transform(chunk) {
			s.ResponseWriter.Write([]byte("data: " + string(mustJSON(ev)) + "\r\n\r\n"))
		}
	}
	return n, nil
}

func (s *geminiSSE) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// bufferedWriter collects a non-stream response so it can be rewritten.
type bufferedWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (b *bufferedWriter) Header() http.Header { return b.header }

func (b *bufferedWriter) WriteHeader(code int) { b.status = code }

func (b *bufferedWriter) Write(p []byte) (int, error) { return b.body.Write(p) }

func (b *bufferedWriter) Flush() {}

// handleGeminiModels lists models in Gemini API shape.
func (g *Gateway) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	seen := map[string]bool{}
	var models []map[string]any
	add := func(name, display, desc string) {
		if seen[name] {
			return
		}
		seen[name] = true
		models = append(models, map[string]any{
			"name": name, "displayName": display, "description": desc,
			"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
			"inputTokenLimit":            128000,
			"outputTokenLimit":           8192,
		})
	}
	for _, e := range g.reg.Entries {
		for _, m := range e.Models {
			if m.Kind != "" && m.Kind != "llm" {
				continue
			}
			display := m.Name
			if display == "" {
				display = m.ID
			}
			add("models/"+e.ID+"/"+m.ID, display, e.ID+" model: "+display)
			if e.ID == "gemini" {
				add("models/"+m.ID, display, "Gemini model: "+display)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models})
}
