package gateway

import "testing"

func TestEmptyCompletion(t *testing.T) {
	for body, wantEmpty := range map[string]bool{
		`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`:  false,
		`{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`:    true,
		`{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`:  false,
		`{"choices":[{"message":{"content":"","tool_calls":[{"id":"t"}]}}]}`: false,
		`{"choices":[]}`:                            true,
		`{"error":{"message":"boom"}}`:              true,
		`{"content":[{"type":"text","text":"hi"}]}`: false,
		`{"content":[]}`:                            true,
		`{"output":[],"status":"completed"}`:        true,
		`{"output":[],"status":"incomplete"}`:       false,
		``:                                          true,
		`<html>bad gateway</html>`:                  true,
		`{"output":[{"type":"message"}]}`:           false,
	} {
		if got := emptyCompletion([]byte(body)) != ""; got != wantEmpty {
			t.Errorf("emptyCompletion(%q) empty=%v, want %v", body, got, wantEmpty)
		}
	}
}
