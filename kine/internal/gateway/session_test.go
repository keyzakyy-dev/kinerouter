package gateway

import "testing"

// Client-provided session ids must win over generated ones (prompt caches hinge
// on it), in the same priority order as sessionManager.js.
func TestResolveSessionID(t *testing.T) {
	if got := resolveSessionID(nil, map[string]any{"prompt_cache_key": "k1"}, "c1"); got != "k1" {
		t.Fatalf("prompt_cache_key ignored: %q", got)
	}
	h := map[string][]string{"X-Claude-Code-Session-Id": {"abc-123"}}
	if got := resolveSessionID(h, nil, "c1"); got != "claude:abc-123" {
		t.Fatalf("claude header ignored: %q", got)
	}
	body := map[string]any{"metadata": map[string]any{"user_id": "user_xxx_session_43f0de60-1234"}}
	if got := resolveSessionID(nil, body, "c1"); got != "claude:43f0de60-1234" {
		t.Fatalf("metadata user_id ignored: %q", got)
	}
	if got := resolveSessionID(nil, nil, ""); got == "" {
		t.Fatal("no id generated for an anonymous connection")
	}
}
