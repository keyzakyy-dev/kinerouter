package translate

import (
	"strings"
	"testing"
)

// The envelope wants a system string, block arrays, tool-call/tool-result
// blocks, and a uuid-shaped threadId from the client session.
func TestToCommandCode(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "be terse"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "hi"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
			}},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": "c1", "function": map[string]any{"name": "run", "arguments": `{"a":1}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": "done"},
		},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "run", "description": "d", "parameters": map[string]any{"type": "object"},
		}}},
	}
	body := ToCommandCode(raw, "m", "/tmp/w", SessionUUID("sess-1"))
	params := body["params"].(map[string]any)
	if params["system"] != "be terse" {
		t.Fatalf("system not lifted: %v", params["system"])
	}
	msgs := params["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("system message should not appear in messages: %d", len(msgs))
	}
	user := msgs[0].(map[string]any)
	blocks := user["content"].([]any)
	if blocks[1].(map[string]any)["type"] != "image" {
		t.Fatalf("image block missing: %v", blocks)
	}
	asst := msgs[1].(map[string]any)["content"].([]any)
	call := asst[0].(map[string]any)
	if call["type"] != "tool-call" || call["input"].(map[string]any)["a"] != float64(1) {
		t.Fatalf("tool-call block wrong: %v", call)
	}
	tool := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if tool["type"] != "tool-result" || tool["toolName"] != "run" {
		t.Fatalf("tool-result block wrong: %v", tool)
	}
	if id, _ := body["threadId"].(string); len(id) != 36 || id[14] != '4' {
		t.Fatalf("threadId not uuid-shaped: %q", id)
	}
	if body["config"].(map[string]any)["workingDir"] != "/tmp/w" {
		t.Fatal("workingDir not carried")
	}
}

// AI-SDK v5 events fold into OpenAI chunks; a consolidated tool-call after
// tool-input-* deltas must not duplicate the call.
func TestCommandCodeToOpenAI(t *testing.T) {
	c := &CommandCodeToOpenAI{}
	if ch := c.Transform(map[string]any{"type": "text-delta", "text": "hi"}); ch == nil ||
		ch["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)["content"] != "hi" {
		t.Fatal("text delta not forwarded")
	}
	c.Transform(map[string]any{"type": "tool-input-start", "id": "t1", "toolName": "run"})
	c.Transform(map[string]any{"type": "tool-input-delta", "id": "t1", "delta": `{"a":`})
	if ch := c.Transform(map[string]any{"type": "tool-call", "toolCallId": "t1", "toolName": "run", "input": map[string]any{"a": 1}}); ch != nil {
		t.Fatal("consolidated tool-call duplicated a streamed one")
	}
	c.Transform(map[string]any{"type": "finish-step", "finishReason": "tool-calls", "usage": map[string]any{"inputTokens": 5, "outputTokens": 2}})
	final := c.Transform(map[string]any{"type": "finish", "totalUsage": map[string]any{"inputTokens": 5, "outputTokens": 2}})
	choice := final["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish reason wrong: %v", choice["finish_reason"])
	}
	if final["usage"].(map[string]any)["total_tokens"] != int64(7) {
		t.Fatalf("usage wrong: %v", final["usage"])
	}

	body := CollectCommandCode(strings.NewReader(
		"{\"type\":\"text-delta\",\"text\":\"he\"}\n{\"type\":\"text-delta\",\"text\":\"llo\"}\n" +
			"{\"type\":\"finish-step\",\"finishReason\":\"stop\"}\n"))
	if body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)["content"] != "hello" {
		t.Fatalf("ndjson fold wrong: %v", body)
	}
}
