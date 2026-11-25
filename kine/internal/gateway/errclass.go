package gateway

// Request-scoped 4xx errors that another combo member (or provider) can still
// answer. The chat dispatcher used to surface every 4xx except the account
// failures, which meant a combo whose first member has a 128K window rejected a
// 200K prompt outright instead of trying the member that advertises 1M.
//
// Upstream draws the same distinction in internal/errclass: context window,
// content filter and model-unavailable are chain-advancing Kinds, while a plain
// bad request is surfaced because no other target can change the answer.

import "strings"

// contextWindowPhrases come from upstream's errclass/errclass.go table, minus
// the entries that need a second clause (handled below).
var contextWindowPhrases = []string{
	"context_length_exceeded",
	"context length exceeded",
	"exceed context limit",
	"exceeds the context window",
	"exceeds context window",
	"this model's maximum context length is",
	"model's maximum context limit",
	"maximum context length",
	"is longer than the model's context length",
	"input tokens exceed the configured limit",
	"exceeds the available context size",
	"exceeds the maximum number of tokens allowed",
	"prompt is too long",
	"input is too long",
	"too many input tokens",
	"too many tokens",
	"request payload size exceeds",
}

// contextWindowExclusions are messages that look like an overflow but are not
// (a throttling message that merely counts tokens, mostly).
var contextWindowExclusions = []string{
	"string_above_max_length",
	"rate limit",
	"per min",
	"per second",
}

var contentFilterPhrases = []string{
	"content filter",
	"content_filter",
	"content policy",
	"content_policy",
	"violates our usage policies",
	"safety system",
	"blocked by safety",
	"responsible ai",
	"prohibited_content",
}

var modelUnavailablePhrases = []string{
	"not found", "does not exist", "not available", "not supported",
	"deprecated", "decommissioned", "has been retired",
}

var capacityPhrases = []string{
	"at capacity", "overloaded", "server_is_overloaded", "model_at_capacity", "degraded",
}

func containsPhrase(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func looksLikeContextWindow(msg string) bool {
	if msg == "" || containsPhrase(msg, contextWindowExclusions) {
		return false
	}
	if containsPhrase(msg, contextWindowPhrases) {
		return true
	}
	// Cerebras: "current length is N while limit is M". HuggingFace:
	// "maximum input length is N tokens". Both need their second clause.
	if strings.Contains(msg, "current length is") && strings.Contains(msg, "while limit is") {
		return true
	}
	return strings.Contains(msg, "maximum input length is") && strings.Contains(msg, "tokens")
}

// requestErrorAdvancesChain reports whether a 4xx is about the target rather
// than the request, so the combo chain should try the next member instead of
// surfacing the error. Account-level statuses (401/402/403/429) are handled by
// the caller and never reach here.
func requestErrorAdvancesChain(status int, message string) bool {
	if status < 400 || status >= 500 {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(message))
	switch {
	case msg == "":
		return false
	case looksLikeContextWindow(msg):
		return true
	case containsPhrase(msg, contentFilterPhrases):
		return true
	case containsPhrase(msg, capacityPhrases):
		return true
	case status == httpStatusNotFound && strings.Contains(msg, "model") &&
		containsPhrase(msg, modelUnavailablePhrases):
		return true
	}
	return false
}

const httpStatusNotFound = 404
