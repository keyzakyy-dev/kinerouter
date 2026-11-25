package translate

import (
	"strings"
	"testing"
)

func bodyWith(messages ...any) map[string]any {
	return map[string]any{"model": "x", "max_tokens": 100, "messages": messages}
}

func text(t string) map[string]any       { return map[string]any{"type": "text", "text": t} }
func userM(blocks ...any) map[string]any { return map[string]any{"role": "user", "content": blocks} }
func asstM(blocks ...any) map[string]any {
	return map[string]any{"role": "assistant", "content": blocks}
}

func TestSanitizeDropsBlankTextBlock(t *testing.T) {
	body := bodyWith(userM(text("hello"), text("   ")))
	if !SanitizeClaudePassthrough(body) {
		t.Fatal("blank text block must be dropped")
	}
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(blocks))
	}
}

func TestSanitizeDropsUnsignedThinking(t *testing.T) {
	body := bodyWith(
		asstM(map[string]any{"type": "thinking", "thinking": "hm"}, text("answer")),
		userM(text("next")),
	)
	SanitizeClaudePassthrough(body)
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["type"] != "text" {
		t.Fatalf("unsigned thinking must be dropped: %#v", blocks)
	}
}

func TestSanitizeKeepsSignedThinking(t *testing.T) {
	signed := "E" + "Eg==" // base64 of 0x12 0x1f... placeholder below
	_ = signed
	// "Eg==" decodes to 0x12; the marker byte is what the check looks at.
	body := bodyWith(
		asstM(map[string]any{"type": "thinking", "thinking": "hm", "signature": "Eg=="}, text("answer")),
		userM(text("next")),
	)
	SanitizeClaudePassthrough(body)
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("signed thinking must survive: %#v", blocks)
	}
}

func TestSanitizeDropsEmptyTurnAndRepairsTail(t *testing.T) {
	body := bodyWith(
		userM(text("hi")),
		asstM(text("")),
		userM(text("")),
	)
	if !SanitizeClaudePassthrough(body) {
		t.Fatal("expected a repair")
	}
	msgs := body["messages"].([]any)
	tail := msgs[len(msgs)-1].(map[string]any)
	if tail["role"] != "user" {
		t.Fatalf("tail role: %v", tail["role"])
	}
	blocks := tail["content"].([]any)
	if len(blocks) != 1 || blocks[0].(map[string]any)["text"] != TrailingUserPlaceholder {
		t.Fatalf("emptied tail must hold the placeholder: %#v", blocks)
	}
	for _, raw := range msgs {
		if m := raw.(map[string]any); m["role"] == "assistant" && !contentfulContent(m["content"]) {
			t.Fatal("empty assistant turn must be dropped")
		}
	}
}

// Lifting a trailing system reminder can expose an assistant tail; that one is
// cleanup's doing, not the client's, so the placeholder turn goes in.
func TestSanitizeAppendsPlaceholderWhenCleanupExposesAssistantTail(t *testing.T) {
	body := bodyWith(
		userM(text("hi")),
		asstM(text("partial")),
		map[string]any{"role": "system", "content": "reminder: be brief"},
	)
	if !SanitizeClaudePassthrough(body) {
		t.Fatal("lifted system tail must be repaired")
	}
	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("want 3 turns, got %d", len(msgs))
	}
	if role := msgs[2].(map[string]any)["role"]; role != "user" {
		t.Fatalf("appended role: %v", role)
	}
}

// A client-written prefill is its own choice and must be left alone.
func TestSanitizeRespectsIntentionalPrefill(t *testing.T) {
	body := bodyWith(userM(text("hi")), asstM(text("partial")))
	SanitizeClaudePassthrough(body)
	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("prefill must not gain a turn: %d", len(msgs))
	}
}

func TestSanitizeTrimsAssistantPrefillWhitespace(t *testing.T) {
	body := bodyWith(userM(text("hi")), asstM(text("partial\n\n  ")))
	if !SanitizeClaudePassthrough(body) {
		t.Fatal("trailing whitespace must be trimmed")
	}
	msgs := body["messages"].([]any)
	blocks := msgs[1].(map[string]any)["content"].([]any)
	if got := blocks[0].(map[string]any)["text"]; got != "partial" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeLiftsMidSystemTurn(t *testing.T) {
	body := bodyWith(
		map[string]any{"role": "system", "content": "be brief"},
		userM(text("hi")),
		map[string]any{"role": "system", "content": "stay in English"},
		asstM(text("ok")),
		userM(text("more")),
	)
	if !SanitizeClaudePassthrough(body) {
		t.Fatal("mid-array system turn must be lifted")
	}
	msgs := body["messages"].([]any)
	for _, raw := range msgs {
		if role := raw.(map[string]any)["role"]; role == "system" {
			if raw.(map[string]any)["content"] == "stay in English" {
				t.Fatal("mid-array system turn must not be forwarded in place")
			}
		}
	}
	sys, _ := body["system"].(string)
	if !strings.Contains(sys, "stay in English") {
		t.Fatalf("lifted text missing from system: %q", sys)
	}
}

func TestSanitizeLeavesValidBodyUntouched(t *testing.T) {
	body := bodyWith(userM(text("hi")), asstM(text("hello")), userM(text("bye")))
	if SanitizeClaudePassthrough(body) {
		t.Fatal("valid conversation must report no change")
	}
}

func TestIsValidClaudeSignature(t *testing.T) {
	cases := map[string]bool{
		"":            false,
		"not-base64!": false,
		"Eg==":        true, // decodes to 0x12
		"Aw==":        false,
		"cache#Eg==":  true,
	}
	for sig, want := range cases {
		if got := IsValidClaudeSignature(sig); got != want {
			t.Fatalf("%q: got %v want %v", sig, got, want)
		}
	}
}

func TestSanitizeClaudeToolID(t *testing.T) {
	for _, id := range []string{"toolu_01abc", "call_1", "abc-DEF_123", ""} {
		if got := SanitizeClaudeToolID(id); got != id {
			t.Fatalf("SanitizeClaudeToolID(%q) = %q, want unchanged", id, got)
		}
	}
	for _, id := range []string{"call.a:b", "toolu/1", "id with space", "訊息1", "resp|fc_7"} {
		got := SanitizeClaudeToolID(id)
		if !claudeToolIDPattern.MatchString(got) {
			t.Fatalf("SanitizeClaudeToolID(%q) = %q, not accepted by Anthropic", id, got)
		}
		if again := SanitizeClaudeToolID(id); again != got {
			t.Fatalf("SanitizeClaudeToolID(%q) is not deterministic: %q then %q", id, got, again)
		}
	}
}

// A conversation that arrived from an OpenAI-shaped client carries ids the
// Anthropic API refuses; both halves must be rewritten to the same value or the
// tool_result stops answering its tool_use.
func TestSanitizeClaudePassthroughRewritesForeignToolIDs(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "read the file"}}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "call.a:b", "name": "Read",
				"input": map[string]any{"path": "x"}}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "call.a:b",
				"content": "file body"}}},
	}}
	if !SanitizeClaudePassthrough(body) {
		t.Fatal("foreign tool ids must mark the body changed")
	}
	msgs := body["messages"].([]any)
	use := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	useID, _ := use["id"].(string)
	resID, _ := res["tool_use_id"].(string)
	if !claudeToolIDPattern.MatchString(useID) {
		t.Fatalf("tool_use.id = %q", useID)
	}
	if useID != resID {
		t.Fatalf("pairing broke: %q vs %q", useID, resID)
	}
	// An already-valid conversation is not touched.
	valid := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "hi"}}},
		map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "tool_use", "id": "toolu_01", "name": "Read",
				"input": map[string]any{}}}},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": "toolu_01",
				"content": "ok"}}},
	}}
	if SanitizeClaudePassthrough(valid) {
		t.Fatal("a valid body must not be reported as changed")
	}
}

// Anthropic answers a fifth cache_control marker with 400. A long history that
// rolls its own anchors, plus a quirk that prepends cacheable system blocks,
// can exceed the budget without the client ever asking for it.
func TestCapClaudeCacheControl(t *testing.T) {
	marker := map[string]any{"type": "ephemeral"}
	mk := func() map[string]any { return map[string]any{"type": "text", "text": "x", "cache_control": marker} }
	body := map[string]any{
		"system": []any{mk(), mk(), mk()},
		"tools": []any{
			map[string]any{"name": "a", "cache_control": marker},
			map[string]any{"name": "b", "cache_control": marker},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{mk(), mk()}},
			map[string]any{"role": "assistant", "content": []any{mk()}},
		},
	}
	if !CapClaudeCacheControl(body) {
		t.Fatal("an over-budget body must report a change")
	}
	count := 0
	sys := body["system"].([]any)
	for _, b := range sys {
		if b.(map[string]any)["cache_control"] != nil {
			count++
		}
	}
	tools := body["tools"].([]any)
	for _, b := range tools {
		if b.(map[string]any)["cache_control"] != nil {
			count++
		}
	}
	for _, m := range body["messages"].([]any) {
		for _, b := range m.(map[string]any)["content"].([]any) {
			if b.(map[string]any)["cache_control"] != nil {
				count++
			}
		}
	}
	if count > cacheControlMarkerBudget {
		t.Fatalf("%d markers survive, budget is %d", count, cacheControlMarkerBudget)
	}
	// The head anchors are the ones that cover the whole prefix.
	if sys[len(sys)-1].(map[string]any)["cache_control"] == nil {
		t.Fatal("the last system block must keep its marker")
	}
	if tools[len(tools)-1].(map[string]any)["cache_control"] == nil {
		t.Fatal("the last tool must keep its marker")
	}
	// The tail-most remaining markers survive, the oldest are dropped.
	msgs := body["messages"].([]any)
	lastBlocks := msgs[1].(map[string]any)["content"].([]any)
	if lastBlocks[0].(map[string]any)["cache_control"] == nil {
		t.Fatal("the newest marker must survive")
	}

	// A body already within budget is untouched.
	ok := map[string]any{
		"system":   []any{mk()},
		"messages": []any{map[string]any{"role": "user", "content": []any{mk()}}},
	}
	if CapClaudeCacheControl(ok) {
		t.Fatal("a body within budget must not change")
	}
}
