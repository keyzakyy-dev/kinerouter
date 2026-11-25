package gateway

import "testing"

func TestRequestErrorAdvancesChain(t *testing.T) {
	cases := []struct {
		status  int
		message string
		want    bool
	}{
		// Context window: another target with a larger window can answer.
		{400, "This model's maximum context length is 128000 tokens", true},
		{400, "prompt is too long: 210000 tokens > 200000 maximum", true},
		{413, "request payload size exceeds the limit: 20 MB", true},
		{400, "context_length_exceeded", true},
		{400, "current length is 5000 while limit is 4096", true},
		// Throttling that mentions tokens is not an overflow.
		{429, "rate limit reached for tokens per min", false},
		{400, "rate limit: too many tokens per min", false},
		// Content filter: another provider may accept the prompt.
		{400, "Your request was rejected by the safety system", true},
		{400, "content_policy_violation", true},
		// Capacity.
		{400, "model_at_capacity", true},
		{503, "overloaded", false}, // 5xx is the caller's retry path
		// Model gone from this provider.
		{404, "The model `gpt-9` does not exist", true},
		{404, "model not found: llama-x", true},
		{404, "no route for /v1/foo", false},
		// Plain request errors must be surfaced: no target can fix them.
		{400, "messages.0.content: field required", false},
		{422, "invalid tool schema", false},
		{401, "invalid api key", false},
		{400, "", false},
		{200, "context length exceeded", false},
	}
	for _, c := range cases {
		if got := requestErrorAdvancesChain(c.status, c.message); got != c.want {
			t.Fatalf("%d %q: got %v want %v", c.status, c.message, got, c.want)
		}
	}
}
