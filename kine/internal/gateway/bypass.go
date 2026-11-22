package gateway

// Bypass filter. Ported from open-sse/utils/bypassHandler.js: Claude Code CLI
// fires prompt-less helper requests (warmup, `/count`, title extraction) that
// must not burn a combo rotation slot or a provider quota, so they are answered
// locally with a canned completion.

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

const bypassText = "CLI Command Execution: Clear Terminal"

// skipPatterns mirrors SKIP_PATTERNS: the title-extraction prompt Claude Code
// sends when it names a conversation.
var skipPatterns = []string{
	"Please write a 5-10 word title for the following conversation:",
}

// bypassResponse returns a locally-built completion when the request is a CLI
// helper call, or nil to route normally.
func (g *Gateway) bypassResponse(raw map[string]any, model, userAgent, clientFormat string, stream bool) []byte {
	if !strings.Contains(userAgent, "claude-cli") {
		return nil
	}
	msgs, _ := raw["messages"].([]any)
	if len(msgs) == 0 {
		return nil
	}
	text := ""
	if g.store.SettingBool("ccFilterNaming", false) {
		system := messagesSystemText(msgs)
		if system == "" {
			system = bodySystemText(raw["system"])
		}
		if strings.Contains(system, "isNewTopic") {
			text = namingText(msgs)
		}
	}
	bypass := text != ""
	if !bypass {
		bypass = isBypassShape(msgs)
	}
	if !bypass {
		return nil
	}
	if text == "" {
		text = bypassText
	}
	return renderBypass(model, text, clientFormat, stream)
}

// isBypassShape matches the four structural patterns bypassHandler checks.
func isBypassShape(msgs []any) bool {
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if last != nil && last["role"] == "assistant" {
		if blocks, ok := last["content"].([]any); ok && len(blocks) > 0 {
			if b, _ := blocks[0].(map[string]any); b != nil && b["text"] == "{" {
				return true
			}
		}
	}
	if first, _ := msgs[0].(map[string]any); first != nil && contentText(first["content"]) == "Warmup" {
		return true
	}
	if len(msgs) == 1 {
		if m, _ := msgs[0].(map[string]any); m != nil && m["role"] == "user" && contentText(m["content"]) == "count" {
			return true
		}
	}
	var user strings.Builder
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg != nil && msg["role"] == "user" {
			user.WriteString(contentText(msg["content"]))
			user.WriteString(" ")
		}
	}
	for _, p := range skipPatterns {
		if strings.Contains(user.String(), p) {
			return true
		}
	}
	return false
}

// namingText answers a title-extraction call: first three words of the user
// prompt, JSON-encoded the way the CLI expects.
func namingText(msgs []any) string {
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg == nil || msg["role"] != "user" {
			continue
		}
		words := strings.Fields(contentText(msg["content"]))
		if len(words) > 3 {
			words = words[:3]
		}
		b, _ := json.Marshal(map[string]any{"isNewTopic": true, "title": strings.Join(words, " ")})
		return string(b)
	}
	return ""
}

// contentText flattens a content value to plain text.
func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			if pm, ok := p.(map[string]any); ok {
				if s, _ := pm["text"].(string); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func messagesSystemText(msgs []any) string {
	var out []string
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg != nil && msg["role"] == "system" {
			out = append(out, contentText(msg["content"]))
		}
	}
	return strings.Join(out, " ")
}

func bodySystemText(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []any:
		return contentText(s)
	}
	return ""
}

// renderBypass builds the canned completion in the client's own format.
func renderBypass(model, text, clientFormat string, stream bool) []byte {
	completion := map[string]any{
		"id": "chatcmpl-bypass", "object": "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
	}
	if !stream {
		switch clientFormat {
		case translate.FormatClaude:
			return mustJSON(translate.OpenAIToClaudeResponse(completion))
		case translate.FormatResponses:
			return mustJSON(translate.OpenAINonStreamToResponses(completion))
		case translate.FormatGemini:
			return mustJSON(translate.OpenAIToGeminiResponse(completion))
		}
		return mustJSON(completion)
	}
	// Streaming: one content chunk, one finish chunk, [DONE] — run through the
	// Client-format translator so a Claude client sees message_start/blocks.
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": "chatcmpl-bypass", "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}
	first := chunk(map[string]any{"role": "assistant", "content": text}, nil)
	last := chunk(map[string]any{}, "stop")
	last["usage"] = completion["usage"]
	switch clientFormat {
	case translate.FormatClaude:
		var b strings.Builder
		tr := &translate.OpenAIToClaude{}
		for _, ch := range []map[string]any{first, last} {
			for _, ev := range tr.Transform(ch) {
				writeClaudeEvent(&b, ev)
			}
		}
		for _, ev := range tr.Finish() {
			writeClaudeEvent(&b, ev)
		}
		b.WriteString("data: [DONE]\n\n")
		return []byte(b.String())
	}
	out := "data: " + string(mustJSON(first)) + "\n\ndata: " + string(mustJSON(last)) + "\n\ndata: [DONE]\n\n"
	return []byte(out)
}
