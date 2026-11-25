package rtk

// compressMessages — RTK token saver. Ported from open-sse/rtk/index.js: walks
// an already-translated provider body and rewrites tool-result content in place
// to a shorter form. Shapes covered: OpenAI Responses function_call_output,
// OpenAI role:"tool" (string or parts), Claude tool_result blocks, and Kiro
// conversationState toolResults.
//
// Fail-open, like the JS: nothing here panics or returns an error, and any
// content that would come back empty or no shorter is left untouched.
//
// Ordering note (from open-sse/handlers/chatCore.js): for cursor the caller
// compresses *before* translation; every other provider after. This package is
// shape-driven and translation-agnostic, so the same function serves both — the
// caller picks the body it passes.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	rawCap          = 10 * 1024 * 1024
	minCompressSize = 500
)

// protectedTools are the tools whose output must never be rewritten. Read,
// Grep and friends return the file text or the match list the model asked for;
// truncating it destroys the answer the agent is working from. Matched
// case-insensitively because the same tool arrives as "Read" from one client
// and "read" from another.
var protectedTools = map[string]bool{
	"read": true, "grep": true, "edit": true, "write": true, "patch": true,
	"anchor": true, "glob": true, "view": true, "list_dir": true,
}

func isProtectedTool(name string) bool {
	return protectedTools[strings.ToLower(strings.TrimSpace(name))]
}

// toolNameFor resolves the tool behind a result: the inline name when the
// provider put one there, otherwise the call it references.
func toolNameFor(m map[string]any, tools map[string]string, idKey string) string {
	if name := str(m, "name"); name != "" {
		return name
	}
	return tools[str(m, idKey)]
}

// errorToolContentMarkers open an error output. The OpenAI Chat tool message
// has no error flag, so the text is the only signal it carries; a result that
// merely mentions the word is not an error, hence the anchored match.
var errorToolContentMarkers = []string{
	"Error:",
	"Exception:",
	"Traceback (most recent call last):",
}

func hasErrorToolContent(content any) bool {
	text, ok := content.(string)
	if !ok {
		return false
	}
	trimmed := strings.TrimSpace(text)
	for _, marker := range errorToolContentMarkers {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return false
}

// isErrorResult reports whether a tool result carries a failure. Error traces
// are preserved: the agent needs them verbatim to recover.
func isErrorResult(m map[string]any) bool {
	if m == nil {
		return false
	}
	for _, key := range []string{"is_error", "isError"} {
		if b, ok := m[key].(bool); ok && b {
			return true
		}
	}
	if s, ok := m["status"].(string); ok && strings.EqualFold(s, "error") {
		return true
	}
	for _, key := range []string{"content", "output", "text"} {
		if hasErrorToolContent(m[key]) {
			return true
		}
	}
	return false
}

// buildToolCallMap indexes every tool call in the conversation by the id its
// result references, so a result can be matched to the tool that produced it
// and to the protected-tool skip list.
func buildToolCallMap(items []any) map[string]string {
	out := map[string]string{}
	note := func(id, name string) {
		if id != "" && name != "" {
			out[id] = name
		}
	}
	for _, it := range items {
		msg, ok := it.(map[string]any)
		if !ok || msg == nil {
			continue
		}
		if list, ok := msg["tool_calls"].([]any); ok {
			for _, tc := range list {
				tcMap, _ := tc.(map[string]any)
				fn, _ := tcMap["function"].(map[string]any)
				id, _ := tcMap["id"].(string)
				name, _ := fn["name"].(string)
				note(id, name)
			}
		}
		if t, _ := msg["type"].(string); t == "function_call" {
			note(str(msg, "call_id"), str(msg, "name"))
		}
		if blocks, ok := msg["content"].([]any); ok {
			for _, b := range blocks {
				block, _ := b.(map[string]any)
				if t, _ := block["type"].(string); t == "tool_use" {
					note(str(block, "id"), str(block, "name"))
				}
			}
		}
	}
	return out
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// Stats reports what compressMessages did; zero Hits means it changed nothing.
type Stats struct {
	BytesBefore int
	BytesAfter  int
	Hits        []Hit
}

// Hit is one compressed tool result.
type Hit struct {
	Shape  string
	Filter string
	Saved  int
}

// LogLine formats the "⚙ RTK" summary, or "" when nothing changed.
func (s *Stats) LogLine() string {
	if s == nil || len(s.Hits) == 0 {
		return ""
	}
	saved := s.BytesBefore - s.BytesAfter
	pct := 0.0
	if s.BytesBefore > 0 {
		pct = float64(saved) / float64(s.BytesBefore) * 100
	}
	seen := map[string]bool{}
	var filters []string
	for _, h := range s.Hits {
		if !seen[h.Filter] {
			seen[h.Filter] = true
			filters = append(filters, h.Filter)
		}
	}
	return fmt.Sprintf("[RTK] saved %dB / %dB (%.1f%%) via [%s] hits=%d",
		saved, s.BytesBefore, pct, strings.Join(filters, ","), len(s.Hits))
}

// CompressMessages compresses a provider request body in place. Returns nil when
// disabled or when the body matches no known shape.
func CompressMessages(body map[string]any, enabled bool) *Stats {
	if !enabled || body == nil {
		return nil
	}
	if cs, ok := body["conversationState"].(map[string]any); ok && cs != nil {
		return compressKiro(cs)
	}
	items, ok := body["messages"].([]any)
	if !ok {
		items, ok = body["input"].([]any)
		if !ok {
			return nil
		}
	}
	stats := &Stats{}
	tools := buildToolCallMap(items)
	for _, it := range items {
		msg, ok := it.(map[string]any)
		if !ok || msg == nil {
			continue
		}
		if msg["type"] == "function_call_output" {
			if isErrorResult(msg) || isProtectedTool(toolNameFor(msg, tools, "call_id")) {
				continue
			}
			switch out := msg["output"].(type) {
			case string:
				msg["output"] = compressText(out, stats, "openai-responses-string")
			case []any:
				for _, p := range out {
					if part, ok := p.(map[string]any); ok && (part["type"] == "input_text" || part["type"] == "text") {
						if t, ok := part["text"].(string); ok {
							part["text"] = compressText(t, stats, "openai-responses-array")
						}
					}
				}
			}
			continue
		}
		role, _ := msg["role"].(string)
		if role == "tool" {
			if isErrorResult(msg) || isProtectedTool(toolNameFor(msg, tools, "tool_call_id")) {
				continue
			}
			switch c := msg["content"].(type) {
			case string:
				msg["content"] = compressText(c, stats, "openai-tool")
				continue
			case []any:
				for _, p := range c {
					if part, ok := p.(map[string]any); ok && part["type"] == "text" {
						if t, ok := part["text"].(string); ok {
							part["text"] = compressText(t, stats, "openai-tool-array")
						}
					}
				}
				continue
			}
		}
		blocks, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, bl := range blocks {
			block, ok := bl.(map[string]any)
			if !ok || block["type"] != "tool_result" {
				continue
			}
			if isErrorResult(block) || isProtectedTool(toolNameFor(block, tools, "tool_use_id")) {
				continue
			}
			// The Claude flat shape keeps the text in "text" instead of "content".
			if t, ok := block["text"].(string); ok {
				block["text"] = compressText(t, stats, "claude-text")
			}
			switch c := block["content"].(type) {
			case string:
				block["content"] = compressText(c, stats, "claude-string")
			case []any:
				for _, p := range c {
					if part, ok := p.(map[string]any); ok && part["type"] == "text" {
						if t, ok := part["text"].(string); ok {
							part["text"] = compressText(t, stats, "claude-array")
						}
					}
				}
			}
		}
	}
	return stats
}

func compressKiro(cs map[string]any) *Stats {
	stats := &Stats{}
	var msgs []map[string]any
	if hist, ok := cs["history"].([]any); ok {
		for _, h := range hist {
			if m, ok := h.(map[string]any); ok {
				msgs = append(msgs, m)
			}
		}
	}
	if cur, ok := cs["currentMessage"].(map[string]any); ok {
		msgs = append(msgs, cur)
	}
	for _, msg := range msgs {
		uim, _ := msg["userInputMessage"].(map[string]any)
		ctx, _ := uim["userInputMessageContext"].(map[string]any)
		results, ok := ctx["toolResults"].([]any)
		if !ok {
			continue
		}
		for _, r := range results {
			tr, ok := r.(map[string]any)
			if !ok || tr["status"] == "error" {
				continue // preserve error traces
			}
			parts, ok := tr["content"].([]any)
			if !ok {
				continue
			}
			for _, p := range parts {
				if part, ok := p.(map[string]any); ok {
					if t, ok := part["text"].(string); ok {
						part["text"] = compressText(t, stats, "kiro-tool-result")
					}
				}
			}
		}
	}
	return stats
}

func compressText(text string, stats *Stats, shape string) string {
	n := len(text)
	stats.BytesBefore += n
	// JS gates on text.length (UTF-16 units); bytes are stricter for the cap
	// but looser for the floor, so the floor counts units.
	if utf8.RuneCountInString(text) < minCompressSize || n > rawCap {
		stats.BytesAfter += n
		return text
	}
	f := autoDetect(text)
	if f.apply == nil {
		stats.BytesAfter += n
		return text
	}
	out := safeApply(f.apply, text)
	if out == "" || len(out) >= n {
		stats.BytesAfter += n
		return text
	}
	stats.BytesAfter += len(out)
	stats.Hits = append(stats.Hits, Hit{Shape: shape, Filter: f.name, Saved: n - len(out)})
	return out
}

// safeApply is the catch_unwind equivalent: a filter that panics yields the
// original text rather than a broken request.
func safeApply(fn func(string) string, text string) (out string) {
	defer func() {
		if recover() != nil {
			out = text
		}
	}()
	if fn == nil {
		return text
	}
	return fn(text)
}
