package translate

import (
	"encoding/json"
	"testing"
)

func TestNormalizeToolCallFinish(t *testing.T) {
	body := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"Read","arguments":"{}"}}]},"finish_reason":"stop"}]}`)
	var out map[string]any
	if err := json.Unmarshal(NormalizeToolCallFinish(body), &out); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v", choice["finish_reason"])
	}

	// A missing reason is repaired too.
	missing := []byte(`{"choices":[{"message":{"tool_calls":[{"id":"c1"}]}}]}`)
	var m map[string]any
	json.Unmarshal(NormalizeToolCallFinish(missing), &m)
	if m["choices"].([]any)[0].(map[string]any)["finish_reason"] != "tool_calls" {
		t.Fatal("a missing finish_reason beside tool_calls must be repaired")
	}

	// Real verdicts and turns without tool calls are left alone, byte for byte.
	for _, untouched := range []string{
		`{"choices":[{"message":{"tool_calls":[{"id":"c1"}]},"finish_reason":"length"}]}`,
		`{"choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`,
		`not json at all`,
	} {
		if got := string(NormalizeToolCallFinish([]byte(untouched))); got != untouched {
			t.Fatalf("body was rewritten: %s", got)
		}
	}
}
