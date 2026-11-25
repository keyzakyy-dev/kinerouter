package gateway

import (
	"testing"
	"time"
)

func TestOrderModelsByChoice(t *testing.T) {
	models := []string{"a/x", "b/y", "c/z"}
	r := &smartRoute{Choice: "c/z", Probabilities: map[string]float64{"b/y": 0.6, "a/x": 0.3}}
	got := orderModelsByChoice(models, r)
	want := []string{"c/z", "b/y", "a/x"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// Unknown choice keeps the original order.
	if got := orderModelsByChoice(models, &smartRoute{Choice: "zzz"}); got[0] != "a/x" {
		t.Fatalf("unknown choice reordered: %v", got)
	}
}

func TestParseSmartRoute(t *testing.T) {
	body := []byte(`{"answers":{"route":{"choice":"oc/m","confidence":0.91,"probabilities":{"oc/m":0.9,"a/b":"0.1"}}}}`)
	r := parseSmartRoute(body)
	if r == nil || r.Choice != "oc/m" || !r.HasConfidence || r.Confidence != 0.91 {
		t.Fatalf("parse: %#v", r)
	}
	if len(r.Probabilities) != 1 || r.Probabilities["oc/m"] != 0.9 {
		t.Fatalf("probabilities: %#v", r.Probabilities)
	}
	if parseSmartRoute([]byte(`{}`)) != nil {
		t.Fatal("empty payload must be nil")
	}
}

func TestExtractPromptState(t *testing.T) {
	body := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "first"},
		map[string]any{"role": "assistant", "content": "reply"},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "latest turn"}}},
	}}
	if got := extractPromptState(body); got != "latest turn" {
		t.Fatalf("state = %q", got)
	}
	long := make([]byte, 3000)
	for i := range long {
		long[i] = 'x'
	}
	if got := extractPromptState(map[string]any{"prompt": string(long)}); len(got) != smartMaxStateChars {
		t.Fatalf("cap = %d", len(got))
	}
}

func TestStickyComboModel(t *testing.T) {
	resetStickyComboStore()
	models := []string{"a", "b"}
	if got := getStickyComboModel("c", "s", models); got != "" {
		t.Fatalf("empty store: %q", got)
	}
	setStickyComboModel("c", "s", "b")
	if got := getStickyComboModel("c", "s", models); got != "b" {
		t.Fatalf("sticky: %q", got)
	}
	// A model that left the combo invalidates the entry.
	if got := getStickyComboModel("c", "s", []string{"a"}); got != "" {
		t.Fatalf("stale model kept: %q", got)
	}
	setStickyComboModel("c", "s", "a")
	clearStickyComboSession("c", "s")
	if got := getStickyComboModel("c", "s", models); got != "" {
		t.Fatalf("clear failed: %q", got)
	}
	resetStickyComboStore()
}

func TestFlattenToolHistory(t *testing.T) {
	in := []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "ok", "tool_calls": []any{
			map[string]any{"function": map[string]any{"name": "read"}},
		}},
		map[string]any{"role": "tool", "content": "file body"},
	}
	out := flattenToolHistory(in)
	if out[1].(map[string]any)["tool_calls"] != nil {
		t.Fatal("tool_calls kept")
	}
	if out[1].(map[string]any)["content"] != "ok\n"+toolCallPrefix+"read]" {
		t.Fatalf("assistant: %v", out[1])
	}
	if out[2].(map[string]any)["role"] != "user" || out[2].(map[string]any)["content"] != toolResultPrefix+"file body]" {
		// A tool result is environment output, so it reads as a user turn:
		// attributing it to the model made the panel/judge prompts claim the
		// assistant had produced the file contents itself.
		t.Fatalf("tool turn: %v", out[2])
	}
}

func TestExtractPanelText(t *testing.T) {
	if got := extractPanelText([]byte(`{"choices":[{"message":{"content":"hello"}}]}`)); got != "hello" {
		t.Fatalf("openai: %q", got)
	}
	if got := extractPanelText([]byte(`{"content":[{"type":"text","text":"claude"}]}`)); got != "claude" {
		t.Fatalf("claude: %q", got)
	}
	if got := extractPanelText([]byte(`{"candidates":[{"content":{"parts":[{"text":"gem"}]}}]}`)); got != "gem" {
		t.Fatalf("gemini: %q", got)
	}
	if got := extractPanelText([]byte(`{"output":[{"content":[{"text":"resp"}]}]}`)); got != "resp" {
		t.Fatalf("responses: %q", got)
	}
}

func TestStickyTTL(t *testing.T) {
	resetStickyComboStore()
	defer resetStickyComboStore()
	setStickyComboModel("c", "s", "a")
	stickySessions.Lock()
	stickySessions.m["c:s"].lastUsed = time.Now().Add(-smartStickyTTL - time.Minute)
	stickySessions.Unlock()
	if got := getStickyComboModel("c", "s", []string{"a"}); got != "" {
		t.Fatalf("expired entry returned: %q", got)
	}
}
