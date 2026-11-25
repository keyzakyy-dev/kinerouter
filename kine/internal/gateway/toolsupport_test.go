package gateway

import (
	"testing"
)

func TestModelWithoutToolSupport(t *testing.T) {
	cases := []struct {
		provider, model string
		want            bool
	}{
		{"cloudflare-ai", "@cf/meta/llama-3.1-8b-instruct-fp8-fast", true},
		{"cloudflare-ai", "@cf/meta/llama-3.3-70b-instruct-fp8-fast", false},
		{"cloudflare-ai", "llama-3.3-70b-instruct-fp8-fast", false}, // bare last segment
		{"cloudflare-ai", "@cf/mistralai/mistral-small-3.1-24b-instruct", false},
		{"openai", "gpt-5", false}, // untracked provider is never touched
		{"", "whatever", false},
	}
	for _, c := range cases {
		if got := modelWithoutToolSupport(c.provider, c.model); got != c.want {
			t.Fatalf("%s/%s: got %v want %v", c.provider, c.model, got, c.want)
		}
	}
}

func TestStripToolsAndFlatten(t *testing.T) {
	body := map[string]any{
		"tools":       []any{map[string]any{"type": "function"}},
		"tool_choice": "auto",
		"messages": []any{
			map[string]any{"role": "user", "content": "list files"},
			map[string]any{"role": "assistant", "content": "sure", "tool_calls": []any{
				map[string]any{"id": "c1", "function": map[string]any{"name": "ls", "arguments": "{}"}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": "a.go b.go"},
		},
	}
	if !stripToolsAndFlatten(body) {
		t.Fatal("expected tool content to be stripped")
	}
	if body["tools"] != nil || body["tool_choice"] != nil {
		t.Fatal("tool definitions must be gone")
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("turn count changed: %d", len(msgs))
	}
	assistant := msgs[1].(map[string]any)
	if assistant["tool_calls"] != nil {
		t.Fatal("tool_calls must be gone")
	}
	if got, _ := assistant["content"].(string); got != "sure\n"+toolCallPrefix+"ls]" {
		t.Fatalf("assistant prose: %q", got)
	}
	tool := msgs[2].(map[string]any)
	if tool["role"] != "user" {
		t.Fatalf("tool result must read as a user turn, got %v", tool["role"])
	}
	if got, _ := tool["content"].(string); got != toolResultPrefix+"a.go b.go]" {
		t.Fatalf("tool prose: %q", got)
	}
}

func TestStripToolsAndFlattenResponsesShape(t *testing.T) {
	body := map[string]any{
		"tools": []any{map[string]any{"type": "function"}},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "hi"},
			map[string]any{"type": "function_call_output", "call_id": "c1", "output": "file contents"},
		},
	}
	if !stripToolsAndFlatten(body) {
		t.Fatal("expected a change")
	}
	items := body["input"].([]any)
	out := items[1].(map[string]any)
	if out["type"] != "message" || out["role"] != "user" {
		t.Fatalf("function_call_output shape: %#v", out)
	}
}

// A body without tools is left byte-identical.
func TestStripToolsNoop(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "hello"},
	}}
	if stripToolsAndFlatten(body) {
		t.Fatal("plain conversation must not be rewritten")
	}
	if stripToolsAndFlatten(nil) {
		t.Fatal("nil body")
	}
}
