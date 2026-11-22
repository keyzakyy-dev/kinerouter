package gateway

import "testing"

func TestDetectRequiredCapabilities(t *testing.T) {
	body := map[string]any{"messages": []any{
		// History media must not count.
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AA"}}}},
		map[string]any{"role": "assistant", "content": "ok"},
		map[string]any{"role": "user", "content": "plain text"},
	}}
	if need := detectRequiredCapabilities(body); need["vision"] {
		t.Fatal("history media must be ignored")
	}

	body["messages"] = append(body["messages"].([]any), map[string]any{
		"role": "user", "content": []any{
			map[string]any{"type": "input_file", "source": map[string]any{"media_type": "application/pdf"}},
		},
	})
	if need := detectRequiredCapabilities(body); !need["pdf"] {
		t.Fatalf("want pdf, got %+v", need)
	}
}

func TestStripHistoryForContext(t *testing.T) {
	var msgs []any
	msgs = append(msgs, map[string]any{"role": "system", "content": "sys"})
	for i := 0; i < 20; i++ {
		msgs = append(msgs, map[string]any{"role": "user", "content": string(make([]byte, 4000))})
		msgs = append(msgs, map[string]any{"role": "assistant", "content": "a"})
	}
	tailMsg := map[string]any{"role": "user", "content": "current turn"}
	msgs = append(msgs, tailMsg)
	body := map[string]any{"messages": msgs}

	out := stripHistoryForContext(body, 2000) // tiny window: ~6400 chars
	got := out["messages"].([]any)
	if len(got) >= len(msgs) {
		t.Fatalf("history was not trimmed: %d", len(got))
	}
	if got[0].(map[string]any)["role"] != "system" {
		t.Fatal("system message must be kept")
	}
	if last := got[len(got)-1].(map[string]any); last["content"] != "current turn" {
		t.Fatal("current user turn must be kept")
	}
}
