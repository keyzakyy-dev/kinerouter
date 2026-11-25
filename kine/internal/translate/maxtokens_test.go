package translate

import (
	"testing"
)

func TestRequiresMaxCompletionTokens(t *testing.T) {
	yes := []string{"gpt-5", "gpt-5.1-codex", "openai/gpt-5-nano", "GPT-5", "gpt-6", "o1-mini", "o3-pro", "o4-mini", "openai/o3-deep"}
	no := []string{"gpt-4o", "gpt-4.1", "claude-sonnet-4-5", "o1", "llama-3-o3x", "gemini-2.5-pro"}
	for _, m := range yes {
		if !RequiresMaxCompletionTokens(m) {
			t.Fatalf("%q should require max_completion_tokens", m)
		}
	}
	for _, m := range no {
		if RequiresMaxCompletionTokens(m) {
			t.Fatalf("%q should keep max_tokens", m)
		}
	}
}

func TestRenameMaxTokensPassthrough(t *testing.T) {
	body := map[string]any{"model": "x", "max_tokens": 512.0, "messages": []any{}}
	if !RenameMaxTokens(body, "gpt-5.1") {
		t.Fatal("expected a rename for gpt-5.1")
	}
	if body["max_tokens"] != nil {
		t.Fatal("max_tokens must be gone")
	}
	if body["max_completion_tokens"] != 512.0 {
		t.Fatalf("budget not carried over: %#v", body["max_completion_tokens"])
	}
	kept := map[string]any{"model": "x", "max_tokens": 512.0}
	if RenameMaxTokens(kept, "gpt-4o") {
		t.Fatal("gpt-4o keeps max_tokens")
	}
	if kept["max_tokens"] != 512.0 {
		t.Fatal("gpt-4o body must be untouched")
	}
	if RenameMaxTokens(map[string]any{"model": "x"}, "gpt-5") {
		t.Fatal("nothing to rename")
	}
}

func TestClaudeToOpenAIRenamesForGPT5(t *testing.T) {
	in := &ClaudeRequest{Model: "gpt-5", MaxTokens: 4096}
	out := ClaudeRequestToOpenAI(in, "gpt-5")
	if out.MaxCompletionTokens == nil || *out.MaxCompletionTokens != 4096 {
		t.Fatalf("want max_completion_tokens 4096, got %+v", out.MaxCompletionTokens)
	}
	if out.MaxTokens != nil {
		t.Fatal("max_tokens must be unset for gpt-5")
	}
	old := ClaudeRequestToOpenAI(&ClaudeRequest{Model: "gpt-4o", MaxTokens: 4096}, "gpt-4o")
	if old.MaxTokens == nil || *old.MaxTokens != 4096 || old.MaxCompletionTokens != nil {
		t.Fatalf("gpt-4o keeps max_tokens: %+v", old)
	}
}

// An OpenAI client that already sends max_completion_tokens keeps its budget
// when the request is translated to a Claude or Gemini provider.
func TestOpenAIToClaudeReadsMaxCompletionTokens(t *testing.T) {
	mt := 2048
	in := &OpenAIRequest{Model: "gpt-5", MaxCompletionTokens: &mt}
	cl := OpenAIRequestToClaude(in, "claude-sonnet-4-5")
	if cl.MaxTokens != 2048 {
		t.Fatalf("claude max_tokens: %d", cl.MaxTokens)
	}
	g, err := ToGemini(map[string]any{"model": "x", "max_completion_tokens": float64(2048),
		"messages": []any{map[string]any{"role": "user", "content": "hi"}}}, "gemini-2.5-pro", false)
	if err != nil {
		t.Fatal(err)
	}
	gen, _ := g["generationConfig"].(map[string]any)
	if gen["maxOutputTokens"] != 2048 {
		t.Fatalf("gemini maxOutputTokens: %#v", g["generationConfig"])
	}
}
