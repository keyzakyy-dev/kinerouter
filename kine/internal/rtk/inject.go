package rtk

// System-prompt injection for the token savers (caveman, ponytail, custom).
// Ported from open-sse/rtk/systemInject.js: appends an instruction into the
// system message of the already-translated provider body, dispatching by wire
// shape so a translated and a native-passthrough request behave the same.
//
// Injectable points, in the JS's precedence:
//   - Kiro: conversationState first user turn's content (never a top-level
//     systemPrompt — kiro.dev 400s on REQUEST_BODY_INVALID for that field).
//   - Claude (by format label): body.system, string or block array, inserted
//     before the trailing cache_control block.
//   - Gemini (by format label): systemInstruction / system_instruction, so
//     Antigravity's body.request wrapper still lands.
//   - OpenAI-shaped, sniffed: instructions string → messages[] → input[].
//
// Idempotency: a prompt is only appended when it is not already present as its
// own SEP-delimited segment, so a combo retry does not stack it.

import (
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

const sep = "\n\n"

// InjectSystemPrompt adds prompt to the body's system slot for the given
// provider format. Fail-open: it does nothing on a shape it does not know.
func InjectSystemPrompt(body map[string]any, format, prompt string) {
	prompt = strings.TrimSpace(prompt)
	if body == nil || prompt == "" {
		return
	}
	if isKiroBody(body) || format == "kiro" {
		injectKiro(body, prompt)
		return
	}
	switch format {
	case translate.FormatClaude:
		injectClaude(body, prompt)
		return
	case translate.FormatGemini:
		injectGemini(body, prompt)
		return
	}
	if s, ok := body["instructions"].(string); ok {
		if !hasPrompt(s, prompt) {
			body["instructions"] = dedupAppend(s, prompt)
		}
		return
	}
	if arr, ok := body["messages"].([]any); ok {
		body["messages"] = injectChat(arr, prompt)
		return
	}
	if arr, ok := body["input"].([]any); ok {
		body["input"] = injectResponses(arr, prompt)
	}
	// A string input must stay untouched.
}

// hasPrompt reports whether prompt is already present as its own segment.
func hasPrompt(haystack, prompt string) bool {
	if haystack == prompt {
		return true
	}
	for _, seg := range strings.Split(haystack, sep) {
		if seg == prompt {
			return true
		}
	}
	return false
}

func dedupAppend(curr, prompt string) string {
	if curr == "" {
		return prompt
	}
	if hasPrompt(curr, prompt) {
		return curr
	}
	return curr + sep + prompt
}

// ------------------------------------------------------------------- Chat

func injectChat(arr []any, prompt string) []any {
	if containsPromptChat(arr, prompt) {
		return arr
	}
	idx := -1
	for i, it := range arr {
		if m, ok := it.(map[string]any); ok && isSystemRole(m["role"]) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return append([]any{map[string]any{"role": "system", "content": prompt}}, arr...)
	}
	appendChatMessage(arr[idx], prompt)
	return arr
}

func containsPromptChat(arr []any, prompt string) bool {
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok || !isSystemRole(m["role"]) {
			continue
		}
		switch c := m["content"].(type) {
		case string:
			if hasPrompt(c, prompt) {
				return true
			}
		case []any:
			for _, p := range c {
				if part, ok := p.(map[string]any); ok {
					if t, ok := part["text"].(string); ok && hasPrompt(t, prompt) {
						return true
					}
				}
			}
		}
	}
	return false
}

func appendChatMessage(msg any, prompt string) {
	m, ok := msg.(map[string]any)
	if !ok {
		return
	}
	switch c := m["content"].(type) {
	case string:
		m["content"] = dedupAppend(c, prompt)
	case []any:
		for _, p := range c {
			if part, ok := p.(map[string]any); ok && part["text"] == prompt {
				return
			}
		}
		m["content"] = append(c, map[string]any{"type": "text", "text": prompt})
	default:
		m["content"] = prompt
	}
}

func isSystemRole(role any) bool {
	r, _ := role.(string)
	return r == "system" || r == "developer"
}

// -------------------------------------------------------------- Responses

func injectResponses(arr []any, prompt string) []any {
	if containsPromptResponses(arr, prompt) {
		return arr
	}
	idx := -1
	for i, it := range arr {
		if m, ok := it.(map[string]any); ok && m["type"] == "message" && isSystemRole(m["role"]) {
			idx = i
			break
		}
	}
	if idx < 0 {
		msg := map[string]any{"type": "message", "role": "system",
			"content": []any{map[string]any{"type": "input_text", "text": prompt}}}
		return append([]any{msg}, arr...)
	}
	m := arr[idx].(map[string]any)
	switch c := m["content"].(type) {
	case string:
		m["content"] = dedupAppend(c, prompt)
	case []any:
		for _, p := range c {
			if part, ok := p.(map[string]any); ok && part["text"] == prompt {
				return arr
			}
		}
		m["content"] = append(c, map[string]any{"type": "input_text", "text": prompt})
	default:
		m["content"] = []any{map[string]any{"type": "input_text", "text": prompt}}
	}
	return arr
}

func containsPromptResponses(arr []any, prompt string) bool {
	for _, it := range arr {
		m, ok := it.(map[string]any)
		if !ok || m["type"] != "message" || !isSystemRole(m["role"]) {
			continue
		}
		switch c := m["content"].(type) {
		case string:
			if hasPrompt(c, prompt) {
				return true
			}
		case []any:
			for _, p := range c {
				if part, ok := p.(map[string]any); ok {
					if t, ok := part["text"].(string); ok && hasPrompt(t, prompt) {
						return true
					}
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------- Claude

func injectClaude(body map[string]any, prompt string) {
	switch sys := body["system"].(type) {
	case string:
		body["system"] = dedupAppend(sys, prompt)
	case []any:
		for _, b := range sys {
			if blk, ok := b.(map[string]any); ok && blk["text"] == prompt {
				return
			}
		}
		block := map[string]any{"type": "text", "text": prompt}
		lastCache := -1
		for i := len(sys) - 1; i >= 0; i-- {
			if b, ok := sys[i].(map[string]any); ok && b["cache_control"] != nil {
				lastCache = i
				break
			}
		}
		next := make([]any, 0, len(sys)+1)
		if lastCache >= 0 {
			next = append(next, sys[:lastCache]...)
			next = append(next, block)
			next = append(next, sys[lastCache:]...)
		} else {
			next = append(next, sys...)
			next = append(next, block)
		}
		body["system"] = next
	default:
		body["system"] = prompt
	}
}

// ---------------------------------------------------------------- Gemini

func injectGemini(body map[string]any, prompt string) {
	target := body
	if req, ok := body["request"].(map[string]any); ok {
		target = req
	}
	key := "systemInstruction"
	if _, ok := target["system_instruction"]; ok {
		key = "system_instruction"
	}
	if sys, ok := target[key].(map[string]any); ok {
		if parts, ok := sys["parts"].([]any); ok {
			for _, p := range parts {
				if part, ok := p.(map[string]any); ok && part["text"] == prompt {
					return
				}
			}
			sys["parts"] = append(parts, map[string]any{"text": prompt})
			return
		}
	}
	target[key] = map[string]any{"parts": []any{map[string]any{"text": prompt}}}
}

// ------------------------------------------------------------------ Kiro

func isKiroBody(body map[string]any) bool {
	cs, ok := body["conversationState"].(map[string]any)
	if !ok || cs == nil {
		return false
	}
	if hist, ok := cs["history"].([]any); ok {
		for _, it := range hist {
			if m, ok := it.(map[string]any); ok {
				if m["userInputMessage"] != nil || m["assistantResponseMessage"] != nil {
					return true
				}
			}
		}
	}
	if cur, ok := cs["currentMessage"].(map[string]any); ok && cur["userInputMessage"] != nil {
		return true
	}
	return false
}

// injectKiro appends into the first user turn's content — where the Kiro
// translator already mirrors system text — never a top-level systemPrompt.
func injectKiro(body map[string]any, prompt string) {
	cs, _ := body["conversationState"].(map[string]any)
	var target map[string]any
	if hist, ok := cs["history"].([]any); ok {
		for _, it := range hist {
			if m, ok := it.(map[string]any); ok {
				if uim, ok := m["userInputMessage"].(map[string]any); ok {
					target = uim
					break
				}
			}
		}
	}
	if target == nil {
		if cur, ok := cs["currentMessage"].(map[string]any); ok {
			if uim, ok := cur["userInputMessage"].(map[string]any); ok {
				target = uim
			}
		}
	}
	if target == nil {
		return
	}
	content, _ := target["content"].(string)
	target["content"] = dedupAppend(content, prompt)
}
