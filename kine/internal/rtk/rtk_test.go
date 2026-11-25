package rtk

import (
	"fmt"
	"strings"
	"testing"
)

func bigGitLog() string {
	var b strings.Builder
	b.WriteString("commit 1234567890abcdef1234567890abcdef12345678\n")
	b.WriteString("Author: Someone <a@b.c>\n")
	b.WriteString("Date:   Mon Jan 1 00:00:00 2024 +0000\n\n")
	for i := 0; i < 300; i++ {
		b.WriteString("    subject line number " + strings.Repeat("x", 20) + "\n")
		b.WriteString("diff --git a/f b/f\n")
	}
	return b.String()
}

func TestCompressClaudeToolResult(t *testing.T) {
	log := bigGitLog()
	body := map[string]any{"messages": []any{
		map[string]any{"role": "assistant", "content": "hi"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "1", "content": log},
			map[string]any{"type": "tool_result", "tool_use_id": "2", "is_error": true, "content": log},
		}},
	}}
	stats := CompressMessages(body, true)
	if stats == nil || len(stats.Hits) != 1 {
		t.Fatalf("want 1 hit, got %+v", stats)
	}
	blocks := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
	if got := blocks[0].(map[string]any)["content"].(string); len(got) >= len(log) {
		t.Fatal("first tool_result was not compressed")
	}
	if got := blocks[1].(map[string]any)["content"].(string); got != log {
		t.Fatal("is_error tool_result must be left untouched")
	}
}

func TestCompressOpenAITool(t *testing.T) {
	log := bigGitLog()
	body := map[string]any{"messages": []any{
		map[string]any{"role": "tool", "content": log},
	}}
	if stats := CompressMessages(body, true); stats == nil || len(stats.Hits) != 1 {
		t.Fatalf("want 1 hit, got %+v", stats)
	}
	if s := body["messages"].([]any)[0].(map[string]any)["content"].(string); len(s) >= len(log) {
		t.Fatal("openai tool content was not compressed")
	}
	if CompressMessages(body, false) != nil {
		t.Fatal("disabled must return nil")
	}
}

func TestInjectChatIdempotent(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "base"},
		map[string]any{"role": "user", "content": "hi"},
	}}
	InjectSystemPrompt(body, "openai", "PROMPT")
	InjectSystemPrompt(body, "openai", "PROMPT")
	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("want 2 messages, got %d", len(msgs))
	}
	if got := msgs[0].(map[string]any)["content"].(string); got != "base\n\nPROMPT" {
		t.Fatalf("unexpected content %q", got)
	}
}

func TestInjectClaudeCacheBlock(t *testing.T) {
	body := map[string]any{"system": []any{
		map[string]any{"type": "text", "text": "a", "cache_control": map[string]any{"type": "ephemeral"}},
	}}
	InjectSystemPrompt(body, "claude", "P")
	sys := body["system"].([]any)
	if len(sys) != 2 || sys[0].(map[string]any)["text"] != "P" {
		t.Fatalf("prompt must land before the cache_control block: %+v", sys)
	}
}

func TestInjectKiroNeverTopLevel(t *testing.T) {
	body := map[string]any{"conversationState": map[string]any{
		"currentMessage": map[string]any{"userInputMessage": map[string]any{"content": "hi"}},
	}}
	InjectSystemPrompt(body, "kiro", "P")
	if _, ok := body["systemPrompt"]; ok {
		t.Fatal("top-level systemPrompt breaks kiro.dev")
	}
	cs := body["conversationState"].(map[string]any)
	uim := cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	if uim["content"] != "hi\n\nP" {
		t.Fatalf("unexpected content %q", uim["content"])
	}
}

// A head/tail truncation used to drop everything between the head and the tail,
// which is where a single failure line usually sits. The middle is now scanned
// for diagnostics and they are re-added.
func TestTruncateKeepsMiddleDiagnostics(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "ok line %d\n", i)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	lines[180] = "ERROR: src/app.ts(12,4): Cannot find name 'foo'."
	lines[181] = "done"
	input := strings.Join(lines, "\n")

	out := smartTruncate(input)
	if !strings.Contains(out, "Cannot find name 'foo'") {
		t.Fatalf("the middle diagnostic must survive:\n%s", out)
	}
	if !strings.Contains(out, "lines truncated") {
		t.Fatalf("marker missing:\n%s", out)
	}
	// 300 lines, head 120 + tail 60 leaves 120 in the middle; one is kept.
	if !strings.Contains(out, "+119 lines truncated") {
		t.Fatalf("the marker must count only the dropped lines:\n%s", out)
	}
	if got := strings.Count(out, "Cannot find name 'foo'"); got != 1 {
		t.Fatalf("the diagnostic must appear exactly once, got %d", got)
	}
	// read-numbered goes through the same truncation.
	out = readNumbered(input)
	if !strings.Contains(out, "Cannot find name 'foo'") {
		t.Fatalf("read-numbered must keep the diagnostic:\n%s", out)
	}
}

func TestPreservedDiagnosticLinesCapsAndDedupes(t *testing.T) {
	middle := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		middle = append(middle, fmt.Sprintf("error number %d", i))
	}
	kept := preservedDiagnosticLines(middle, []string{"error number 0"})
	if len(kept) != maxPreservedDiagnosticLines {
		t.Fatalf("cap not applied: %d", len(kept))
	}
	for _, l := range kept {
		if l == "error number 0" {
			t.Fatal("a line already in the head must not be re-added")
		}
	}
}
