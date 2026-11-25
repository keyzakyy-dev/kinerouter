package rtk

import (
	"strings"
	"testing"
)

// A Read result is the file text the model asked for; truncating it destroys
// the answer, so the compressor must leave it byte-identical.
func TestProtectedToolReadUntouched(t *testing.T) {
	file := strings.Repeat("0123456789 abcdefghij\n", 200)
	body := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "tu1", "name": "Read", "input": map[string]any{"file_path": "x.go"}},
			map[string]any{"type": "tool_use", "id": "tu2", "name": "Bash", "input": map[string]any{"command": "git log"}},
		}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "tu1", "content": file},
			map[string]any{"type": "tool_result", "tool_use_id": "tu2", "content": strings.ReplaceAll(file, "0123456789", "commit 0f1e2d")},
		}},
	}}
	CompressMessages(body, true)
	blocks := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if got := blocks[0].(map[string]any)["content"].(string); got != file {
		t.Fatal("read output must never be rewritten")
	}
	if got := blocks[1].(map[string]any)["content"].(string); len(got) >= len(file) {
		t.Fatal("non-protected tool output should still compress")
	}
}

// The OpenAI tool shape has no error flag: a result that opens with an error
// marker is an error and must be preserved.
func TestErrorContentPreservedOpenAIShape(t *testing.T) {
	body := strings.Repeat("0123456789 abcdefghij\n", 200)
	errText := "Error: " + body
	okText := strings.ReplaceAll(body, "0123456789", "commit 0f1e2d")
	msg := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
			map[string]any{"id": "c1", "function": map[string]any{"name": "shell", "arguments": `{"command":"ls"}`}},
			map[string]any{"id": "c2", "function": map[string]any{"name": "shell", "arguments": `{"command":"ls"}`}},
			map[string]any{"id": "c3", "function": map[string]any{"name": "shell", "arguments": `{"command":"ls"}`}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "c1", "content": errText},
		map[string]any{"role": "tool", "tool_call_id": "c2", "content": "Traceback (most recent call last):\n" + body},
		map[string]any{"role": "tool", "tool_call_id": "c3", "content": okText},
	}}
	CompressMessages(msg, true)
	items := msg["messages"].([]any)
	if got := items[1].(map[string]any)["content"].(string); got != errText {
		t.Fatal("Error: output must be preserved")
	}
	if got := items[2].(map[string]any)["content"].(string); !strings.HasPrefix(got, "Traceback") {
		t.Fatal("Traceback output must be preserved")
	}
	if got := items[3].(map[string]any)["content"].(string); len(got) >= len(okText) {
		t.Fatal("clean output should compress")
	}
}

// A mention of the word deeper in the text is not an error.
func TestErrorMarkerOnlyAnchorsAtStart(t *testing.T) {
	text := strings.Repeat("0123456789 abcdefghij\n", 200) + "note: Error: nothing failed here\n"
	if hasErrorToolContent(text) {
		t.Fatal("mid-text marker must not count as an error")
	}
	if !hasErrorToolContent("  Traceback (most recent call last):\n boom") {
		t.Fatal("leading marker (after spaces) must count")
	}
}

// Claude's flat tool_result keeps its text in "text".
func TestClaudeFlatToolResultCompresses(t *testing.T) {
	text := strings.ReplaceAll(strings.Repeat("0123456789 abcdefghij\n", 200), "0123456789", "commit 0f1e2d")
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "t1", "text": text},
		}},
	}}
	stats := CompressMessages(body, true)
	if stats == nil || len(stats.Hits) != 1 || stats.Hits[0].Shape != "claude-text" {
		t.Fatalf("flat tool_result: %+v", stats)
	}
}
