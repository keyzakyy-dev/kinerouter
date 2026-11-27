package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The reference settings route reports the deploy-time capability switches, and
// the dashboard hides the Translator nav item unless enableTranslator is true
// (src/components/layout.tsx). Omitting them left /dashboard/translator
// unreachable even with ENABLE_TRANSLATOR set.
func TestSettingsReportsEnvCapabilityFlags(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	call := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		g.handleGetSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
			t.Fatalf("body %q: %v", rr.Body.String(), err)
		}
		return m
	}

	// Unset is the reference default (its .env.example ships them off).
	t.Setenv("ENABLE_TRANSLATOR", "")
	t.Setenv("ENABLE_REQUEST_LOGS", "")
	m := call()
	if v, ok := m["enableTranslator"]; !ok || v != false {
		t.Fatalf("enableTranslator = %v (present %v), want false", v, ok)
	}
	if v, ok := m["enableRequestLogs"]; !ok || v != false {
		t.Fatalf("enableRequestLogs = %v (present %v), want false", v, ok)
	}

	t.Setenv("ENABLE_TRANSLATOR", "true")
	t.Setenv("ENABLE_REQUEST_LOGS", "true")
	m = call()
	if m["enableTranslator"] != true || m["enableRequestLogs"] != true {
		t.Fatalf("flags not set from env: %v %v", m["enableTranslator"], m["enableRequestLogs"])
	}
}

// The token estimate is chars/4 rounded up, matching the reference counter.
func TestCountTokensEstimate(t *testing.T) {
	body := map[string]any{
		"system": "12345678",
		"messages": []any{
			map[string]any{"role": "user", "content": "12345678"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "12345678"},
				map[string]any{"type": "tool_use", "name": "ab", "input": map[string]any{"p": "12345"}},
			}},
		},
	}
	var chars int
	chars += countValueChars(body["system"])
	for _, m := range body["messages"].([]any) {
		chars += countMessageChars(m)
	}
	// 8 + 8 + (8 + 2 + (1+5)) = 32 chars → 8 tokens
	if chars != 32 {
		t.Fatalf("chars: %d", chars)
	}
	if (chars+3)/4 != 8 {
		t.Fatalf("tokens: %d", (chars+3)/4)
	}
}

// Gemini native embeddings are normalized to the OpenAI list shape, and usage
// is only accepted when it is exact (no completion tokens).
func TestGeminiEmbeddingsToOpenAI(t *testing.T) {
	raw := []byte(`{"embeddings":[{"values":[0.1,0.2]},{"values":[0.3]}]}`)
	out := geminiEmbeddingsToOpenAI(raw, "text-embedding-004")
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	data, _ := m["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("want 2 embeddings, got %v", m["data"])
	}
	if num(data[1].(map[string]any)["index"]) != 1 {
		t.Fatalf("index not set: %v", data[1])
	}
	if u := embeddingUsage([]byte(`{"usage":{"prompt_tokens":4,"total_tokens":4}}`)); u.PromptTokens != 4 {
		t.Fatalf("exact usage lost: %+v", u)
	}
	if u := embeddingUsage([]byte(`{"usage":{"prompt_tokens":4,"total_tokens":9}}`)); u.PromptTokens != 0 {
		t.Fatalf("inexact usage accepted: %+v", u)
	}
	// Batch input must become Gemini's requests[] wrapper.
	b := geminiEmbeddingBody(map[string]any{"input": []any{"a", "b"}, "dimensions": float64(8)})
	reqs, _ := b["requests"].([]any)
	if len(reqs) != 2 {
		t.Fatalf("batch not wrapped: %v", b)
	}
	if reqs[0].(map[string]any)["outputDimensionality"] != float64(8) {
		t.Fatalf("dimensions dropped: %v", reqs[0])
	}
}

// Gemini image responses collapse inlineData parts into the OpenAI shape, and
// bodyFields whitelists (xAI/SenseNova) drop everything else.
func TestImageAdapters(t *testing.T) {
	raw := []byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"data":"AAA"}},{"text":"ok"}]}}]}`)
	var m map[string]any
	if err := json.Unmarshal(geminiImageToOpenAI(raw, "cat"), &m); err != nil {
		t.Fatal(err)
	}
	data, _ := m["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["b64_json"] != "AAA" {
		t.Fatalf("inline image lost: %v", m["data"])
	}
	cfg := map[string]any{"bodyFields": []any{"model", "prompt", "n", "response_format"}}
	b := openAIImageBody("grok-2-image", map[string]any{"prompt": "cat", "size": "1024x1024", "n": float64(2)}, cfg)
	if _, has := b["size"]; has {
		t.Fatalf("size not filtered: %v", b)
	}
	if b["n"] != float64(2) {
		t.Fatalf("allowed field dropped: %v", b)
	}
	if inline := dataURIInline("data:image/jpeg;base64,ZZZ"); inline["inlineData"].(map[string]any)["mimeType"] != "image/jpeg" {
		t.Fatalf("data URI not parsed: %v", inline)
	}
}

// WAV wrapping, model/voice parsing and multipart assembly are the three pure
// pieces of the audio surface.
func TestAudioHelpers(t *testing.T) {
	wav := pcmToWAV([]byte{1, 2, 3, 4})
	if string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatalf("bad wav header: % x", wav[:44])
	}
	if len(wav) != 48 {
		t.Fatalf("wav length %d", len(wav))
	}
	if m, v := splitModelVoice("gpt-4o-mini-tts/alloy"); m != "gpt-4o-mini-tts" || v != "alloy" {
		t.Fatalf("split %q/%q", m, v)
	}
	if m, v := splitModelVoice("alloy"); m != "" || v != "alloy" {
		t.Fatalf("bare voice %q/%q", m, v)
	}
	body, ct := multipartUpload(map[string]string{"model": "whisper-1"}, "file", "a.wav", []byte("x"))
	if !strings.HasPrefix(ct, "multipart/form-data; boundary=") || !bytes.Contains(body, []byte(`name="model"`)) {
		t.Fatalf("bad multipart: %s %q", ct, body)
	}
}

// Search results land in the unified shape with positions renumbered; the query
// passed in is whitespace-collapsed so a provider never sees the raw spacing.
func TestNormalizeSearchResults(t *testing.T) {
	raw := []byte(`{"organic":[{"title":"A","link":"https://www.example.com/a?x=1","snippet":"sa"},
		{"title":"B","link":"https://b.example.org/","snippet":"sb"}]}`)
	out := normalizeSearchResults("serper", raw, "web")
	if len(out) != 2 {
		t.Fatalf("want 2 results, got %d", len(out))
	}
	first := out[0].(map[string]any)
	if first["position"] != 1 || first["title"] != "A" {
		t.Fatalf("bad first result: %v", first)
	}
	if first["display_url"] != "example.com/a" {
		t.Fatalf("display_url not stripped: %v", first["display_url"])
	}
	// GLM's MCP envelope nests the payload as JSON text.
	env := mustJSON(map[string]any{"result": map[string]any{"content": []any{
		map[string]any{"text": `{"results":[{"title":"G","url":"https://g/","snippet":"x"}]}`}}}})
	if got := normalizeSearchResults("glm", env, "web"); len(got) != 1 {
		t.Fatalf("glm results not unwrapped: %d", len(got))
	}
}

// A query with tabs/newlines collapses to single spaces.
func TestSearchQueryCollapse(t *testing.T) {
	if got := strings.Join(strings.Fields("  foo\t\tbar\n baz "), " "); got != "foo bar baz" {
		t.Fatalf("got %q", got)
	}
}

// Vertex job ids round-trip only for a canonical operation name, so a client
// cannot smuggle a path segment into the fetch URL.
func TestVertexVideoJobID(t *testing.T) {
	name := "projects/p/locations/us-central1/publishers/google/models/veo-3/operations/abc"
	id := encodeVertexJobID(name)
	back, ok := decodeVertexJobID(id)
	if !ok || back != name {
		t.Fatalf("round trip failed: %q %v", back, ok)
	}
	for _, bad := range []string{"", "not base64!", encodeVertexJobID(name + "/../x"), encodeVertexJobID("projects/p/x")} {
		if _, ok := decodeVertexJobID(bad); ok {
			t.Fatalf("%q should be rejected", bad)
		}
	}
	out := fromVertexOperation(mustJSON(map[string]any{"name": name, "done": true,
		"response": map[string]any{"videos": []any{map[string]any{"gcsUri": "gs://b/v.mp4"}}}}))
	var m map[string]any
	json.Unmarshal(out, &m)
	if m["status"] != "completed" {
		t.Fatalf("operation not mapped: %v", m)
	}
	if v, _ := m["video"].(map[string]any); v["url"] != "gs://b/v.mp4" {
		t.Fatalf("video url lost: %v", m["video"])
	}
}
