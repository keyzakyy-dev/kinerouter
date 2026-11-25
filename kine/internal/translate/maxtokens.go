package translate

// max_completion_tokens. OpenAI's gpt-5/gpt-6/o-series models reject
// "max_tokens" ("Unsupported parameter: 'max_tokens' is not supported with
// this model. Use 'max_completion_tokens' instead"), so any lane that builds an
// OpenAI body for one of those models has to send the other field. Older models
// only know "max_tokens", which is why the choice is per model.

import (
	"strings"
)

// RequiresMaxCompletionTokens reports whether a model takes
// max_completion_tokens instead of max_tokens. Matches upstream's rule: gpt-5.x
// and gpt-6.x anywhere in the name, and o1/o3/o4 with a hyphen so "o1-mini" and
// "o3-pro" count while an unrelated name holding "o1" does not.
func RequiresMaxCompletionTokens(model string) bool {
	lower := strings.ToLower(model)
	if strings.Contains(lower, "gpt-5") || strings.Contains(lower, "gpt-6") {
		return true
	}
	for _, prefix := range []string{"o1-", "o3-", "o4-"} {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}

// RequestedMaxTokens is the output budget the client asked for, whichever field
// it used. Callers that need one number (Claude, Gemini) read this.
func RequestedMaxTokens(in *OpenAIRequest) *int {
	if in == nil {
		return nil
	}
	if in.MaxCompletionTokens != nil {
		return in.MaxCompletionTokens
	}
	return in.MaxTokens
}

// RenameMaxTokens moves the budget to the field the model accepts. It reports
// whether it changed the body.
func RenameMaxTokens(body map[string]any, model string) bool {
	if body == nil || !RequiresMaxCompletionTokens(model) {
		return false
	}
	v, ok := body["max_tokens"]
	if !ok {
		return false
	}
	body["max_completion_tokens"] = v
	delete(body, "max_tokens")
	return true
}
