package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// CLI helper shapes are answered locally; a real prompt is not.
func TestBypassShapes(t *testing.T) {
	cases := []struct {
		name string
		msgs []any
		want bool
	}{
		{"warmup", []any{map[string]any{"role": "user", "content": "Warmup"}}, true},
		{"count", []any{map[string]any{"role": "user", "content": "count"}}, true},
		{"title-extract", []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": skipPatterns[0] + " hello"},
		}}}, true},
		{"assistant-brace", []any{
			map[string]any{"role": "user", "content": "x"},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "{"}}},
		}, true},
		{"real-prompt", []any{map[string]any{"role": "user", "content": "fix the bug"}}, false},
	}
	for _, c := range cases {
		if got := isBypassShape(c.msgs); got != c.want {
			t.Fatalf("%s: got %v", c.name, got)
		}
	}
}

func TestRenderBypassClaudeStream(t *testing.T) {
	out := string(renderBypass("claude-sonnet-4", "hi", translate.FormatClaude, true))
	if !strings.HasPrefix(out, "event: message_start\n") {
		t.Fatalf("expected claude SSE, got %q", out[:40])
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatal("missing [DONE]")
	}
	out = string(renderBypass("gpt-5", "hi", translate.FormatOpenAI, false))
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil || m["object"] != "chat.completion" {
		t.Fatalf("openai body wrong: %s", out)
	}
}
