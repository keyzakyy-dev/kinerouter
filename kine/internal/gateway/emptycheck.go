package gateway

// A 200 is not proof of an answer. Gateways in front of a provider return 200
// with an empty body, an HTML page or a {"error":...} envelope; treating that
// as a completed turn skips failover and clears the account. Ported from
// 9router-go proxy/empty_success.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type emptyProbe struct {
	Error   json.RawMessage `json:"error"`
	Choices json.RawMessage `json:"choices"`
	Content json.RawMessage `json:"content"`
	Output  json.RawMessage `json:"output"`
	Status  string          `json:"status"`
	Stop    string          `json:"stop_reason"`
}

type emptyChoice struct {
	Message struct {
		Content          json.RawMessage `json:"content"`
		ToolCalls        json.RawMessage `json:"tool_calls"`
		Function         json.RawMessage `json:"function_call"`
		ReasoningContent json.RawMessage `json:"reasoning_content"`
		Reasoning        json.RawMessage `json:"reasoning"`
		Refusal          json.RawMessage `json:"refusal"`
	} `json:"message"`
	Delta struct {
		Content   json.RawMessage `json:"content"`
		ToolCalls json.RawMessage `json:"tool_calls"`
		Reasoning json.RawMessage `json:"reasoning_content"`
	} `json:"delta"`
	Text   string `json:"text"`
	Finish string `json:"finish_reason"`
}

func carriesValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	switch trimmed[0] {
	case '"':
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s != ""
		}
	case '[':
		return !bytes.Equal(trimmed, []byte("[]"))
	case '{':
		return !bytes.Equal(trimmed, []byte("{}"))
	}
	return true
}

func (c emptyChoice) carriesAnswer() bool {
	return carriesValue(c.Message.Content) || carriesValue(c.Message.ToolCalls) ||
		carriesValue(c.Message.Function) || carriesValue(c.Message.ReasoningContent) || carriesValue(c.Message.Reasoning) ||
		carriesValue(c.Message.Refusal) || carriesValue(c.Delta.Content) ||
		carriesValue(c.Delta.ToolCalls) || carriesValue(c.Delta.Reasoning)
}

// emptyCompletion names why a 200 body carries no answer, or "" when it does.
// A body it cannot model is relayed unchanged rather than guessed at.
func emptyCompletion(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "upstream returned an empty body with status 200"
	}
	if isSSE(trimmed) {
		return ""
	}
	if trimmed[0] != '{' {
		return "upstream returned a non-JSON body with status 200"
	}
	var p emptyProbe
	if json.Unmarshal(trimmed, &p) != nil {
		return "upstream returned an unparseable body with status 200"
	}
	if carriesValue(p.Error) && !bytes.Equal(bytes.TrimSpace(p.Error), []byte("null")) {
		return "upstream answered 200 with an error envelope"
	}
	switch {
	case p.Choices != nil:
		var choices []emptyChoice
		if json.Unmarshal(p.Choices, &choices) != nil {
			return ""
		}
		if len(choices) == 0 {
			return "choices is empty"
		}
		if choices[0].Text != "" || choices[0].carriesAnswer() {
			return ""
		}
		if choices[0].Finish == "length" {
			return ""
		}
		return "the first choice carries no content, tool call or reasoning"
	case p.Content != nil:
		if carriesValue(p.Content) || p.Stop == "max_tokens" {
			return ""
		}
		return "content is empty"
	case p.Output != nil:
		var output []json.RawMessage
		if json.Unmarshal(p.Output, &output) != nil {
			return ""
		}
		if len(output) == 0 && p.Status != "incomplete" && p.Status != "failed" &&
			p.Status != "cancelled" && p.Status != "cancelling" {
			return "output is empty"
		}
		return ""
	}
	return "no choices, content or output"
}

func emptyCompletionError(body []byte) *upstreamError {
	reason := emptyCompletion(body)
	if reason == "" {
		return nil
	}
	return &upstreamError{
		status:  502,
		message: reason,
		raw: string(mustJSON(map[string]any{"error": map[string]any{
			"message": fmt.Sprintf("upstream answered 200 without a completion: %s", reason),
			"type":    "upstream_error",
			"code":    502,
		}})),
	}
}
