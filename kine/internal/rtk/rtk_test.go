package rtk

import (
	"strings"
	"testing"
)

func bigGitLog() string {
	var b strings.Builder
	b.WriteString("commit 1234567890abcdef1234567890abcdef12345678\n")
	b.WriteString("Author: Someone <a@b.c>\n")
	b.WriteString("Date:   Mon Jan 1 00:00:00 2024 +0000\n\n")
	for i := 0; i < 200; i++ {
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
