package translate

// Claude passthrough cleanup. A Claude client talking to a Claude-format
// provider used to have its body forwarded verbatim, which fails whenever the
// conversation holds a shape Anthropic rejects:
//
//   - a blank text block ("text content blocks must be non-empty"),
//   - an unsigned thinking block (the signature is what Anthropic verifies),
//   - a turn whose content array ends up empty (or was empty already),
//   - a mid-array "system" turn ("role 'system' must precede an 'assistant' message"),
//   - a conversation ending on an assistant turn that the cleanup exposed,
//   - an assistant prefill whose text ends in whitespace.
//
// KineRouter mirrors the upstream fixes for those (translator/request.go
// SanitizeClaudePassthrough and claude_trailing_turn.go) in a single pass,
// because a rejected request costs the caller the whole turn.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// TrailingUserPlaceholder is the turn appended when cleanup would otherwise
// leave the conversation ending on an assistant message.
const TrailingUserPlaceholder = "Continue."

const maxClaudeSignatureLen = 32 * 1024 * 1024
const claudeSignatureMarker = 0x12

func stripCachePrefix(raw string) string {
	sig := strings.TrimSpace(raw)
	if sig == "" {
		return ""
	}
	if idx := strings.Index(sig, "#"); idx >= 0 {
		return strings.TrimSpace(sig[idx+1:])
	}
	return sig
}

// IsValidClaudeSignature follows open-sse/utils/claudeSignature.js: one base64
// layer starting with 'E', or two when it starts with 'R', and the first byte
// of the innermost layer is the Claude signature marker 0x12.
func IsValidClaudeSignature(raw string) bool {
	sig := stripCachePrefix(raw)
	if sig == "" || len(sig) > maxClaudeSignatureLen {
		return false
	}
	switch sig[0] {
	case 'E':
		decoded, err := base64.StdEncoding.DecodeString(sig)
		return err == nil && len(decoded) > 0 && decoded[0] == claudeSignatureMarker
	case 'R':
		outer, err := base64.StdEncoding.DecodeString(sig)
		if err != nil || len(outer) == 0 || outer[0] != 'E' {
			return false
		}
		inner, err := base64.StdEncoding.DecodeString(string(outer))
		return err == nil && len(inner) > 0 && inner[0] == claudeSignatureMarker
	}
	return false
}

// emptyClaudeBlock reports a block Anthropic will refuse. Only shapes the
// gateway can prove empty are dropped: a tool_result keeps its turn alive
// whatever it holds, and unknown types are content.
func emptyClaudeBlock(block map[string]any) bool {
	switch t, _ := block["type"].(string); t {
	case "text":
		text, _ := block["text"].(string)
		return strings.TrimSpace(text) == ""
	case "thinking":
		sig, _ := block["signature"].(string)
		return !IsValidClaudeSignature(sig)
	}
	return false
}

// contentfulContent reports whether a message's content carries something
// Anthropic accepts. Both wire shapes count: a bare string and a block array.
func contentfulContent(content any) bool {
	switch c := content.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(c) != ""
	case []any:
		for _, raw := range c {
			if block, ok := raw.(map[string]any); ok && !emptyClaudeBlock(block) {
				return true
			}
		}
		return false
	}
	return true
}

func lastClaudeRole(messages []any) string {
	if len(messages) == 0 {
		return ""
	}
	msg, ok := messages[len(messages)-1].(map[string]any)
	if !ok {
		return ""
	}
	role, _ := msg["role"].(string)
	return strings.TrimSpace(role)
}

// claudeToolIDPattern is the only shape Anthropic accepts for a tool_use id.
// A conversation that arrived from an OpenAI-shaped client can easily hold
// "call.a:b" or a Responses id, which the API rejects with 400 "tool_use.id:
// String should match pattern '^[a-zA-Z0-9_-]+$'".
var claudeToolIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// SanitizeClaudeToolID rewrites a foreign id into an accepted one. The hash
// keeps it stable, so a tool_use and the tool_result that answers it are
// rewritten to the same value within a request and across retries.
func SanitizeClaudeToolID(id string) string {
	if id == "" || claudeToolIDPattern.MatchString(id) {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return "toolu_" + hex.EncodeToString(sum[:])[:24]
}

// sanitizeClaudeToolIDs rewrites every tool_use.id and tool_result.tool_use_id
// in a message array and reports whether anything changed.
func sanitizeClaudeToolIDs(messages []any) bool {
	changed := false
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, bRaw := range blocks {
			block, ok := bRaw.(map[string]any)
			if !ok {
				continue
			}
			if id, _ := block["id"].(string); id != "" {
				if clean := SanitizeClaudeToolID(id); clean != id {
					block["id"] = clean
					changed = true
				}
			}
			if id, _ := block["tool_use_id"].(string); id != "" {
				if clean := SanitizeClaudeToolID(id); clean != id {
					block["tool_use_id"] = clean
					changed = true
				}
			}
		}
	}
	return changed
}

// SanitizeClaudePassthrough repairs the shapes Anthropic rejects and reports
// whether it changed anything, so a caller can keep forwarding untouched bytes
// when the conversation was already valid.
func SanitizeClaudePassthrough(body map[string]any) bool {
	if body == nil {
		return false
	}
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		return false
	}
	changed := false

	// Foreign tool ids are refused outright, and a rewritten id must stay paired
	// with its result.
	if sanitizeClaudeToolIDs(messages) {
		changed = true
	}

	// An explicit assistant tail is the client's own prefill, not a leftover.
	intentionalPrefill := lastClaudeRole(messages) == "assistant"

	// A mid-array system turn is refused outright; fold its text into the
	// top-level system field, which is where it belongs.
	if kept, moved := liftMidSystemTurns(body, messages); moved {
		messages = kept
		changed = true
	}

	filtered := make([]any, 0, len(messages))
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			filtered = append(filtered, raw)
			continue
		}
		if blocks, ok := msg["content"].([]any); ok {
			kept := make([]any, 0, len(blocks))
			for _, bRaw := range blocks {
				block, ok := bRaw.(map[string]any)
				if ok && emptyClaudeBlock(block) {
					changed = true
					continue
				}
				kept = append(kept, bRaw)
			}
			if len(kept) != len(blocks) {
				msg["content"] = kept
			}
		}
		// The trailing user turn is kept even when empty: it is repaired below
		// instead of being dropped, because an empty tail is refused just as
		// hard as a missing one.
		isTail := i == len(messages)-1
		if !isTail || intentionalPrefill {
			if content, exists := msg["content"]; exists && !contentfulContent(content) {
				changed = true
				continue
			}
		}
		filtered = append(filtered, msg)
	}

	// Trim the whitespace Anthropic refuses on an assistant prefill.
	if len(filtered) > 0 {
		if last, ok := filtered[len(filtered)-1].(map[string]any); ok {
			if role, _ := last["role"].(string); role == "assistant" {
				if trimFinalAssistantWhitespace(last) {
					changed = true
				}
			}
		}
	}

	if !intentionalPrefill && len(filtered) > 0 {
		tail, ok := filtered[len(filtered)-1].(map[string]any)
		if ok {
			switch role, _ := tail["role"].(string); {
			case role == "assistant":
				changed = true
				filtered = append(filtered, map[string]any{
					"role":    "user",
					"content": []any{buildTextBlock(TrailingUserPlaceholder)},
				})
			case !contentfulContent(tail["content"]):
				// Replacing the emptied tail beats dropping it: Anthropic refuses
				// an empty final turn exactly like a missing one.
				tail["content"] = []any{buildTextBlock(TrailingUserPlaceholder)}
				changed = true
			}
		}
	}

	if changed {
		body["messages"] = filtered
	}
	if CapClaudeCacheControl(body) {
		changed = true
	}
	return changed
}

// cacheControlMarkerBudget is how many cache_control markers a Messages request
// may carry. Anthropic answers a fifth one with 400 "A maximum of 4 blocks with
// cache_control"; a client that anchors its own prompt cache, a quirk that
// prepends cacheable system blocks (zcode) and an injected reminder can easily
// add up past it.
const cacheControlMarkerBudget = 4

// CapClaudeCacheControl trims a Claude body to the marker budget and reports
// whether it changed anything. The head anchors — the last system block and the
// last cacheable tool — are kept because they cover the whole prefix; of the
// remaining markers the tail-most survive, so the truncation drops the oldest
// anchors a rolling history left behind.
func CapClaudeCacheControl(body map[string]any) bool {
	if body == nil {
		return false
	}
	var head, rest []map[string]any
	sys, _ := body["system"].([]any)
	if len(sys) > 0 {
		if last, ok := sys[len(sys)-1].(map[string]any); ok && last["cache_control"] != nil {
			head = append(head, last)
		}
	}
	tools, _ := body["tools"].([]any)
	lastToolIdx := -1
	for i, t := range tools {
		// Anthropic rejects a tool carrying both defer_loading and a marker.
		if m, ok := t.(map[string]any); ok && m["cache_control"] != nil && m["defer_loading"] != true {
			lastToolIdx = i
		}
	}
	if lastToolIdx >= 0 {
		head = append(head, tools[lastToolIdx].(map[string]any))
	}
	for i := 0; i < len(sys)-1; i++ {
		if m, ok := sys[i].(map[string]any); ok && m["cache_control"] != nil {
			rest = append(rest, m)
		}
	}
	for i, t := range tools {
		if i == lastToolIdx {
			continue
		}
		if m, ok := t.(map[string]any); ok && m["cache_control"] != nil {
			rest = append(rest, m)
		}
	}
	if msgs, ok := body["messages"].([]any); ok {
		for _, raw := range msgs {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			blocks, ok := m["content"].([]any)
			if !ok {
				continue
			}
			for _, b := range blocks {
				if bm, ok := b.(map[string]any); ok && bm["cache_control"] != nil {
					rest = append(rest, bm)
				}
			}
		}
	}
	keep := cacheControlMarkerBudget - len(head)
	if keep < 0 {
		keep = 0
	}
	excess := len(rest) - keep
	if excess <= 0 {
		return false
	}
	for i := 0; i < excess; i++ {
		delete(rest[i], "cache_control")
	}
	return true
}

func buildTextBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

// trimFinalAssistantWhitespace trims the whitespace at the end of the final
// assistant turn and drops text blocks that trim to nothing.
func trimFinalAssistantWhitespace(msg map[string]any) bool {
	changed := false
	switch content := msg["content"].(type) {
	case string:
		if trimmed := strings.TrimRightFunc(content, unicode.IsSpace); trimmed != content {
			msg["content"] = trimmed
			changed = true
		}
	case []any:
		for len(content) > 0 {
			block, ok := content[len(content)-1].(map[string]any)
			if !ok {
				break
			}
			if t, _ := block["type"].(string); t != "text" {
				break
			}
			text, _ := block["text"].(string)
			trimmed := strings.TrimRightFunc(text, unicode.IsSpace)
			if trimmed != "" {
				if trimmed != text {
					block["text"] = trimmed
					changed = true
				}
				break
			}
			content = content[:len(content)-1]
			msg["content"] = content
			changed = true
		}
	}
	return changed
}

// liftMidSystemTurns moves system turns that appear after the head into the
// top-level system prompt. The head system turn is left where the client put
// it; the API accepts neither an empty top-level system nor a mid-array one.
func liftMidSystemTurns(body map[string]any, messages []any) ([]any, bool) {
	moved := false
	kept := make([]any, 0, len(messages))
	var lifted []string
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		role, _ := msg["role"].(string)
		if i > 0 && role == "system" {
			if text := claudeContentText(msg["content"]); text != "" {
				lifted = append(lifted, text)
			}
			moved = true
			continue
		}
		kept = append(kept, msg)
	}
	if !moved {
		return messages, false
	}
	if len(lifted) > 0 {
		existing := strings.TrimSpace(claudeSystemText(body["system"]))
		joined := strings.Join(append([]string{existing}, lifted...), "\n")
		body["system"] = strings.TrimSpace(joined)
	}
	return kept, true
}

func claudeContentText(content any) string {
	switch c := content.(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		var parts []string
		for _, raw := range c {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := block["type"].(string); t == "text" {
				if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
