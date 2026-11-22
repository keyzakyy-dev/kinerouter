package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// A Responses client routed to a Claude provider must pivot through Chat
// Completions instead of failing with "unsupported translation".
func TestTranslateRequestResponsesToClaude(t *testing.T) {
	raw := map[string]any{
		"model": "claude-sonnet-4",
		"input": []any{map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "hi"}}}},
	}
	out, err := translateRequestWithSession(translate.FormatResponses, translate.FormatClaude, raw, "claude-sonnet-4", true, "")
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := out["messages"].([]any)
	if len(msgs) != 1 || msgs[0].(map[string]any)["role"] != "user" {
		t.Fatalf("responses→claude lost the user turn: %v", out["messages"])
	}
	if out["max_tokens"] == nil {
		t.Fatalf("claude max_tokens default not applied: %v", out)
	}
}

// A Claude client routed to a Responses provider must produce a Responses body
// pinned to stream (Responses upstreams always stream).
func TestTranslateRequestClaudeToResponses(t *testing.T) {
	raw := map[string]any{
		"model":      "gpt-5",
		"max_tokens": float64(64),
		"messages": []any{
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "yo"}}},
		},
	}
	out, err := translateRequestWithSession(translate.FormatClaude, translate.FormatResponses, raw, "gpt-5", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if out["stream"] != true {
		t.Fatalf("responses upstream must stream: %v", out["stream"])
	}
	input, _ := out["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("claude turns not carried into input: %v", out["input"])
	}
	if out["max_output_tokens"] != float64(64) {
		t.Fatalf("max_tokens not remapped: %v", out["max_output_tokens"])
	}
}

// Non-stream folds: a Chat Completions body with tool calls becomes a Responses
// body with function_call output items.
func TestTranslateNonStreamResponsesClient(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"id": "chatcmpl-1", "created": 1, "model": "gpt-5",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "hey"}}},
		"usage": map[string]any{"prompt_tokens": 2, "completion_tokens": 1},
	})
	out, u := translateNonStream(translate.FormatOpenAI, translate.FormatResponses, body)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["object"] != "response" {
		t.Fatalf("not converted to Responses shape: %v", m)
	}
	items, _ := m["output"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["type"] != "message" {
		t.Fatalf("assistant text missing: %v", m["output"])
	}
	if u.PromptTokens != 2 || u.CompletionTokens != 1 {
		t.Fatalf("usage not extracted: %+v", u)
	}
}

// A Gemini provider must answer non-stream clients in their own format:
// chat (OpenAI) clients get a chat.completion, Claude clients a message.
func TestTranslateNonStreamGeminiProvider(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "hi"}}},
			"finishReason": "STOP"}},
		"usageMetadata": map[string]any{"promptTokenCount": float64(3),
			"candidatesTokenCount": float64(2), "totalTokenCount": float64(5)},
	})
	out, u := translateNonStream(translate.FormatGemini, translate.FormatOpenAI, body)
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["object"] != "chat.completion" {
		t.Fatalf("gemini body not converted: %v", m)
	}
	if u.PromptTokens != 3 || u.CompletionTokens != 2 {
		t.Fatalf("usage: %+v", u)
	}
	out, _ = translateNonStream(translate.FormatGemini, translate.FormatClaude, body)
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "message" {
		t.Fatalf("claude client did not get a message: %v", m)
	}
}

// A Claude client routed to Gemini must pivot through Chat Completions
// first — ToGemini only accepts the OpenAI shape.
func TestTranslateRequestClaudeToGemini(t *testing.T) {
	raw := map[string]any{
		"model":      "gemini-2.5-pro",
		"max_tokens": float64(20),
		"messages":   []any{map[string]any{"role": "user", "content": "hi"}},
	}
	out, err := translateRequestWithSession(translate.FormatClaude, translate.FormatGemini, raw, "gemini-2.5-pro", false, "")
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := out["contents"].([]translate.GeminiContent)
	if len(contents) != 1 || contents[0].Parts[0].Text != "hi" {
		t.Fatalf("claude turn not carried into gemini contents: %v", out["contents"])
	}
}

// geminiSSE rewrites the OpenAI SSE stream into Gemini chunks and drops the
// [DONE] sentinel.
func TestGeminiSSERewriter(t *testing.T) {
	var out bytes.Buffer
	gw := &geminiSSE{ResponseWriter: nopWriter{&out}, conv: &translate.OpenAIToGemini{}}
	gw.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	gw.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
	gw.Write([]byte("data: [DONE]\n\n"))
	s := out.String()
	if strings.Contains(s, "[DONE]") {
		t.Fatalf("sentinel leaked: %q", s)
	}
	if !strings.Contains(s, "\"parts\":[{\"text\":\"hi\"}]") {
		t.Fatalf("text part missing: %q", s)
	}
	if !strings.Contains(s, "\"finishReason\":\"STOP\"") {
		t.Fatalf("finish missing: %q", s)
	}
}

// nopWriter is a minimal ResponseWriter for the rewriter test.
type nopWriter struct{ b *bytes.Buffer }

func (n nopWriter) Header() http.Header         { return http.Header{} }
func (n nopWriter) Write(p []byte) (int, error) { return n.b.Write(p) }
func (n nopWriter) WriteHeader(int)             {}
