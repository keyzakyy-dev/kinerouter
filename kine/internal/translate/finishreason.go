package translate

import (
	"bytes"
	"encoding/json"
)

// NormalizeToolCallFinish repairs a non-streaming OpenAI chat-completion body
// whose assistant turn carries tool calls but whose finish_reason says "stop"
// (or nothing). vLLM and a few OpenAI-compatible servers report a plain stop
// after a named tool_choice, and an agent loop keys off the finish reason: it
// reads "stop" as the final answer and ends the turn with the tool call never
// executed (upstream mydisha/keirouter e340784).
//
// Bodies that need no repair keep their exact bytes, so the common case stays a
// pass-through; a body that does not parse is relayed untouched.
func NormalizeToolCallFinish(body []byte) []byte {
	if !bytes.Contains(body, []byte(`"tool_calls"`)) {
		return body
	}
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		return body
	}
	choices, ok := resp["choices"].([]any)
	if !ok {
		return body
	}
	changed := false
	for _, c := range choices {
		choice, ok := c.(map[string]any)
		if !ok {
			continue
		}
		msg, ok := choice["message"].(map[string]any)
		if !ok {
			continue
		}
		calls, ok := msg["tool_calls"].([]any)
		if !ok || len(calls) == 0 {
			continue
		}
		reason, _ := choice["finish_reason"].(string)
		// "length" and "content_filter" are real verdicts about the turn; only a
		// plain stop (or a missing reason) contradicts a tool call.
		if reason != "" && reason != "stop" {
			continue
		}
		choice["finish_reason"] = "tool_calls"
		changed = true
	}
	if !changed {
		return body
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return body
	}
	return out
}
