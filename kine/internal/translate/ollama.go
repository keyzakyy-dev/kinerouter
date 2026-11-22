package translate

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Ollama wire format. Ported from open-sse/translator/request/openai-to-ollama.js
// and response/ollama-to-openai.js: string-only content, raw-base64 images,
// tool arguments as objects, NDJSON responses.
//
// ponytail: no thinking-block round-trip on the way out (Ollama's request side
// has no slot for it); add when a local model needs it back.

// ToOllama converts an OpenAI Chat Completions request body.
func ToOllama(raw map[string]any, model string, stream bool) (map[string]any, error) {
	out := map[string]any{
		"model":    model,
		"stream":   stream,
		"messages": ollamaMessages(raw["messages"]),
	}
	opts := map[string]any{}
	if v, ok := raw["temperature"]; ok && v != nil {
		opts["temperature"] = v
	}
	if v, ok := raw["top_p"]; ok && v != nil {
		opts["top_p"] = v
	}
	if v, ok := raw["max_tokens"]; ok && v != nil {
		opts["num_predict"] = v
	}
	if len(opts) > 0 {
		out["options"] = opts
	}
	if v, ok := raw["tools"]; ok && v != nil {
		out["tools"] = v
	}
	if v, ok := raw["tool_choice"]; ok && v != nil {
		out["tool_choice"] = v
	}
	return out, nil
}

func ollamaMessages(v any) []any {
	msgs, _ := v.([]any)
	toolNames := map[string]string{}
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg == nil || msg["role"] != "assistant" {
			continue
		}
		calls, _ := msg["tool_calls"].([]any)
		for _, c := range calls {
			cm, _ := c.(map[string]any)
			fn, _ := cm["function"].(map[string]any)
			if id, _ := cm["id"].(string); id != "" {
				toolNames[id], _ = fn["name"].(string)
			}
		}
	}
	out := make([]any, 0, len(msgs))
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "tool":
			text := ollamaContent(msg["content"])
			if text == "" {
				continue
			}
			name := toolNames[str(msg, "tool_call_id")]
			if name == "" {
				name = str(msg, "name")
			}
			if name == "" {
				name = "unknown_tool"
			}
			out = append(out, map[string]any{"role": "tool", "tool_name": name, "content": text})
		case "assistant":
			o := map[string]any{"role": "assistant", "content": ollamaContent(msg["content"])}
			if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
				list := make([]any, 0, len(calls))
				for i, c := range calls {
					cm, _ := c.(map[string]any)
					fn, _ := cm["function"].(map[string]any)
					args := fn["arguments"]
					if s, ok := args.(string); ok {
						var parsed any = map[string]any{}
						if json.Unmarshal([]byte(s), &parsed) != nil {
							parsed = map[string]any{}
						}
						args = parsed
					}
					idx := i
					if n, ok := cm["index"].(float64); ok {
						idx = int(n)
					}
					list = append(list, map[string]any{
						"type": "function",
						"function": map[string]any{
							"index": idx, "name": str(fn, "name"), "arguments": args,
						},
					})
				}
				o["tool_calls"] = list
			}
			out = append(out, o)
		default:
			text := ollamaContent(msg["content"])
			if text == "" {
				continue
			}
			o := map[string]any{"role": role, "content": text}
			if imgs := ollamaImages(msg["content"]); len(imgs) > 0 {
				o["images"] = imgs
			}
			out = append(out, o)
		}
	}
	return out
}

// ollamaContent flattens content blocks to the string Ollama accepts.
func ollamaContent(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, b := range c {
			bm, _ := b.(map[string]any)
			if bm != nil && bm["type"] == "text" {
				if s, _ := bm["text"].(string); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// ollamaImages pulls raw base64 out of data-URI image blocks.
func ollamaImages(v any) []string {
	blocks, _ := v.([]any)
	var out []string
	for _, b := range blocks {
		bm, _ := b.(map[string]any)
		if bm == nil || bm["type"] != "image_url" {
			continue
		}
		url := ""
		switch iu := bm["image_url"].(type) {
		case string:
			url = iu
		case map[string]any:
			url, _ = iu["url"].(string)
		}
		if comma := strings.Index(url, ","); comma > 0 && strings.Contains(url[:comma], ";base64") {
			out = append(out, url[comma+1:])
		}
	}
	return out
}

// ---------------------------------------------------------------- response side

// OllamaToOpenAI converts NDJSON stream objects to OpenAI chat.completion.chunks.
type OllamaToOpenAI struct {
	ID      string
	Model   string
	HadTool bool
	Usage   Usage
}

func (o *OllamaToOpenAI) chunk(delta map[string]any, finish any) map[string]any {
	if o.ID == "" {
		o.ID = "chatcmpl-" + randomSuffix()
	}
	return map[string]any{
		"id": o.ID, "object": "chat.completion.chunk",
		"created": nowUnix(), "model": o.Model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
}

func (o *OllamaToOpenAI) Transform(ev map[string]any) map[string]any {
	if o.Model == "" {
		o.Model, _ = ev["model"].(string)
	}
	if done, _ := ev["done"].(bool); done {
		o.Usage = ollamaUsage(ev)
		reason := ollamaFinish(str(ev, "done_reason"))
		if o.HadTool {
			reason = "tool_calls"
		}
		ch := o.chunk(map[string]any{}, reason)
		ch["usage"] = ollamaUsageMap(o.Usage)
		return ch
	}
	msg, _ := ev["message"].(map[string]any)
	if msg == nil {
		return nil
	}
	delta := map[string]any{}
	if s, _ := msg["content"].(string); s != "" {
		delta["content"] = s
	}
	if s, _ := msg["thinking"].(string); s != "" {
		delta["reasoning_content"] = s
	}
	if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
		o.HadTool = true
		delta["tool_calls"] = ollamaToolCalls(calls)
	}
	if len(delta) == 0 {
		return nil
	}
	return o.chunk(delta, nil)
}

// Flush ends a stream that never produced done=true.
func (o *OllamaToOpenAI) Flush() []map[string]any {
	return []map[string]any{o.chunk(map[string]any{}, ollamaFinish(""))}
}

// OllamaBodyToOpenAI folds a non-stream /api/chat body into chat.completion.
func OllamaBodyToOpenAI(body map[string]any) map[string]any {
	msg, _ := body["message"].(map[string]any)
	u := ollamaUsage(body)
	message := map[string]any{"role": "assistant"}
	if s, _ := msg["content"].(string); s != "" {
		message["content"] = s
	}
	if s, _ := msg["thinking"].(string); s != "" {
		message["reasoning_content"] = s
	}
	reason := ollamaFinish(str(body, "done_reason"))
	if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
		message["tool_calls"] = ollamaToolCalls(calls)
		reason = "tool_calls"
	}
	if message["content"] == nil && message["tool_calls"] == nil {
		message["content"] = ""
	}
	return map[string]any{
		"id": "chatcmpl-" + randomSuffix(), "object": "chat.completion",
		"created": nowUnix(), "model": str(body, "model"),
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": reason}},
		"usage":   ollamaUsageMap(u),
	}
}

func ollamaToolCalls(calls []any) []any {
	out := make([]any, 0, len(calls))
	for i, c := range calls {
		cm, _ := c.(map[string]any)
		fn, _ := cm["function"].(map[string]any)
		idx := i
		if n, ok := fn["index"].(float64); ok {
			idx = int(n)
		}
		args := fn["arguments"]
		if _, ok := args.(string); !ok {
			b, _ := json.Marshal(args)
			args = string(b)
		}
		id, _ := cm["id"].(string)
		if id == "" {
			id = fmt.Sprintf("call_%s_%d", randomSuffix(), idx)
		}
		out = append(out, map[string]any{
			"index": idx, "id": id, "type": "function",
			"function": map[string]any{"name": str(fn, "name"), "arguments": args},
		})
	}
	return out
}

func ollamaUsage(raw map[string]any) Usage {
	return Usage{
		PromptTokens:     intAt64(raw, "prompt_eval_count"),
		CompletionTokens: intAt64(raw, "eval_count"),
		CacheRead:        intAt64(raw, "prompt_eval_cached_count"),
	}
}

func ollamaUsageMap(u Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":         u.PromptTokens,
		"completion_tokens":     u.CompletionTokens,
		"total_tokens":          u.PromptTokens + u.CompletionTokens,
		"prompt_tokens_details": map[string]any{"cached_tokens": u.CacheRead},
	}
}

func ollamaFinish(reason string) string {
	switch reason {
	case "tool_calls", "tool_use":
		return "tool_calls"
	case "length", "max_tokens":
		return "length"
	}
	return "stop"
}

func nowUnix() int64 { return time.Now().Unix() }

func intAt64(m map[string]any, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}
