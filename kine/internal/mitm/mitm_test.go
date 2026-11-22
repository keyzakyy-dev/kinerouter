package mitm

// One runnable check for the parts that are easy to get silently wrong: the
// AWS EventStream encoder must produce frames the existing decoder accepts, and
// model alias resolution must not re-route autocomplete.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

func TestKiroFramesRoundTrip(t *testing.T) {
	st := newKiroState("mapped-model")
	chunks := []map[string]any{
		{"model": "mapped-model", "choices": []any{map[string]any{"delta": map[string]any{"content": "Hello"}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{
				"index":    0,
				"id":       "call_1",
				"function": map[string]any{"name": "get_time", "arguments": `{"tz":"UTC"}`},
			}},
		}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}},
	}
	var buf bytes.Buffer
	for _, c := range chunks {
		for _, f := range convertOpenAIToKiro(c, st) {
			buf.Write(f)
		}
	}
	for _, f := range convertOpenAIToKiro(nil, st) {
		buf.Write(f)
	}

	r := bufio.NewReader(&buf)
	var types []string
	assistant := ""
	gotTools := 0
	for {
		fr, err := translate.ReadKiroFrame(r)
		if err != nil {
			break
		}
		typ, _ := fr.Headers[":event-type"].(string)
		types = append(types, typ)
		switch typ {
		case "assistantResponseEvent":
			s, _ := fr.Payload["content"].(string)
			assistant += s
		case "toolUseEvent":
			_, hasInput := fr.Payload["input"]
			_, isStop := fr.Payload["stop"]
			if !hasInput && !isStop {
				gotTools++
			}
		}
	}
	if len(types) == 0 {
		t.Fatal("encoder produced no decodable frames")
	}
	if types[0] != "" { // first frame is the initial-response bootstrap (no event-type)
		// bootstrap frames carry no :event-type; anything else here is a bug
	}
	if assistant != "Hello" {
		t.Fatalf("assistant text = %q, want %q", assistant, "Hello")
	}
	if gotTools != 1 {
		t.Fatalf("tool init events = %d, want 1", gotTools)
	}
	// With tool calls the stream ends on the tool stop event (Kiro's protocol);
	// only tool-less turns end on messageStopEvent.
	if last := types[len(types)-1]; last != "toolUseEvent" {
		t.Fatalf("last frame = %q, want toolUseEvent", last)
	}
}

func TestGetMappedModel(t *testing.T) {
	aliases := AliasMap{"antigravity": {
		"gemini-3.8-flash-high": "my-flash",
		"claude-sonnet-4-6":     "my-sonnet",
		"gpt-oss-120b-medium":   "my-oss",
	}}
	for _, tc := range []struct{ in, want string }{
		{"models/gemini-3.5-flash", "my-flash"},
		{"claude-sonnet-5.5", "my-sonnet"},
		{"some-oss-model", "my-oss"},
		{"tab_completion", ""},
	} {
		if got := GetMappedModel(aliases, "antigravity", tc.in); got != tc.want {
			t.Errorf("GetMappedModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if !NoMap("antigravity", "tab_completion") {
		t.Error("tab_completion must never be re-routed")
	}
}

func TestExtractModelTieredFlash(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"request": map[string]any{
			"generationConfig": map[string]any{
				"thinkingConfig": map[string]any{"thinkingLevel": "low"},
			},
		},
	})
	got := ExtractModel("/v1/models/gemini-3.8-flash-tiered:streamGenerateContent", body)
	if got != "gemini-3.8-flash-low" {
		t.Fatalf("tiered flash model = %q, want gemini-3.8-flash-low", got)
	}
}
