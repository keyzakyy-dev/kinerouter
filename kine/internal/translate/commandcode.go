package translate

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// CommandCode wire format. Ported from
// open-sse/translator/request/openai-to-commandcode.js and
// response/commandcode-to-openai.js: Anthropic-style params (system string,
// block arrays, tool-call/tool-result blocks) wrapped in the CLI's envelope,
// answered by AI-SDK-v5 NDJSON events.
//
// The envelope's config.structure/recentCommits/gitStatus stay empty exactly as
// openai-to-commandcode.js emits them — the CLI derives workspace context
// itself.

// MaxTokens matches the CLI default when the client sends none.
const commandCodeMaxTokens = 64000

// SessionUUID derives a stable uuid-shaped id from the client session id, the
// same shape toCommandCodeSessionUuid produces.
func SessionUUID(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	h := hex.EncodeToString(sum[:])
	variant := string("89abcdef"[sum[16]&0x3])
	return h[:8] + "-" + h[8:12] + "-4" + h[13:16] + "-" + variant + h[17:20] + "-" + h[20:32]
}

// ToCommandCode wraps an OpenAI Chat Completions request in the CLI envelope.
func ToCommandCode(raw map[string]any, model string, workingDir, threadID string) map[string]any {
	messages, system := commandCodeMessages(raw["messages"])
	params := map[string]any{
		"model":       model,
		"messages":    messages,
		"stream":      true,
		"max_tokens":  commandCodeMaxTokens,
		"temperature": 0.3,
	}
	for _, k := range []string{"max_tokens", "max_output_tokens"} {
		if v, ok := raw[k]; ok && v != nil {
			params["max_tokens"] = v
			break
		}
	}
	if v, ok := raw["temperature"]; ok && v != nil {
		params["temperature"] = v
	}
	if v, ok := raw["top_p"]; ok && v != nil {
		params["top_p"] = v
	}
	if system != "" {
		params["system"] = system
	}
	if tools := commandCodeTools(raw["tools"]); len(tools) > 0 {
		params["tools"] = tools
	}
	if effort := str(raw, "reasoning_effort"); effort != "" {
		params["reasoning_effort"] = effort
	}
	return map[string]any{
		"threadId":       threadID,
		"memory":         nil,
		"taste":          nil,
		"skills":         nil,
		"permissionMode": "standard",
		"config": map[string]any{
			"workingDir": workingDir,
			"date":       time.Now().UTC().Format("2006-01-02"),
			"environment": func() string {
				return "linux"
			}(),
			"structure":     []any{},
			"isGitRepo":     false,
			"currentBranch": "",
			"mainBranch":    "",
			"gitStatus":     "",
			"recentCommits": []any{},
		},
		"params": params,
	}
}

func commandCodeMessages(v any) ([]any, string) {
	msgs, _ := v.([]any)
	names := map[string]string{}
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg == nil || msg["role"] != "assistant" {
			continue
		}
		calls, _ := msg["tool_calls"].([]any)
		for _, c := range calls {
			cm, _ := c.(map[string]any)
			fn, _ := cm["function"].(map[string]any)
			if id, _ := cm["id"].(string); id != "" && str(fn, "name") != "" {
				names[id] = str(fn, "name")
			}
		}
	}
	var out []any
	var system []string
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg == nil {
			continue
		}
		switch str(msg, "role") {
		case "system":
			if t := flattenContent(msg["content"]); t != "" {
				system = append(system, t)
			}
		case "tool":
			name := str(msg, "name")
			if name == "" {
				name = names[str(msg, "tool_call_id")]
			}
			if name == "" {
				name = "unknown"
			}
			typ := "text"
			if msg["is_error"] == true || msg["status"] == "error" {
				typ = "error-text"
			}
			out = append(out, map[string]any{"role": "tool", "content": []any{map[string]any{
				"type":       "tool-result",
				"toolCallId": str(msg, "tool_call_id"),
				"toolName":   name,
				"output":     map[string]any{"type": typ, "value": flattenContent(msg["content"])},
			}}})
		case "assistant":
			var blocks []any
			if rc := firstStr(msg, "reasoning_content", "thought", "reasoning"); rc != "" {
				blocks = append(blocks, map[string]any{"type": "reasoning", "text": rc})
			}
			if t := flattenContent(msg["content"]); t != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": t})
			}
			calls, _ := msg["tool_calls"].([]any)
			for _, c := range calls {
				cm, _ := c.(map[string]any)
				fn, _ := cm["function"].(map[string]any)
				blocks = append(blocks, map[string]any{
					"type": "tool-call", "toolCallId": str(cm, "id"), "toolName": str(fn, "name"),
					"input": parseArgs(fn["arguments"]),
				})
			}
			if len(blocks) == 0 {
				blocks = []any{map[string]any{"type": "text", "text": ""}}
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		default:
			out = append(out, map[string]any{"role": "user", "content": commandCodeBlocks(msg["content"])})
		}
	}
	return out, strings.Join(system, "\n\n")
}

// commandCodeBlocks keeps message content an array (never a string) and maps
// images to the CLI's inline form.
func commandCodeBlocks(v any) []any {
	var blocks []any
	add := func(text string) {
		blocks = append(blocks, map[string]any{"type": "text", "text": text})
	}
	switch c := v.(type) {
	case string:
		add(c)
	case []any:
		for _, p := range c {
			switch part := p.(type) {
			case string:
				add(part)
			case map[string]any:
				if part["type"] == "image_url" {
					url := ""
					switch iu := part["image_url"].(type) {
					case string:
						url = iu
					case map[string]any:
						url, _ = iu["url"].(string)
					}
					if strings.HasPrefix(url, "data:") {
						mime := strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(url, ",", 2)[0], "data:"), ";base64")
						blocks = append(blocks, map[string]any{
							"type": "image", "image": url, "mimeType": mime, "mediaType": mime,
						})
						continue
					}
				}
				if t, _ := part["text"].(string); t != "" {
					add(t)
				}
			}
		}
	}
	if len(blocks) == 0 {
		add("")
	}
	return blocks
}

func commandCodeTools(v any) []any {
	tools, _ := v.([]any)
	var out []any
	for _, t := range tools {
		tm, _ := t.(map[string]any)
		if tm == nil {
			continue
		}
		fn, _ := tm["function"].(map[string]any)
		if fn == nil {
			if str(tm, "name") == "" {
				continue
			}
			fn = tm
		}
		schema := fn["parameters"]
		if schema == nil {
			schema = fn["input_schema"]
		}
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, map[string]any{
			"name": str(fn, "name"), "description": str(fn, "description"), "input_schema": schema,
		})
	}
	return out
}

func flattenContent(v any) string {
	switch c := v.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			switch part := p.(type) {
			case string:
				parts = append(parts, part)
			case map[string]any:
				if s, _ := part["text"].(string); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func parseArgs(v any) any {
	switch a := v.(type) {
	case nil:
		return map[string]any{}
	case string:
		var out any
		if json.Unmarshal([]byte(a), &out) != nil {
			return map[string]any{"raw": a}
		}
		if _, ok := out.(map[string]any); ok {
			return out
		}
		return map[string]any{"value": out}
	}
	return v
}

// ---------------------------------------------------------------- response side

// CommandCodeToOpenAI folds AI-SDK-v5 NDJSON events into OpenAI chunks.
type CommandCodeToOpenAI struct {
	ID       string
	Created  int64
	Model    string
	chunks   int
	toolIdx  int
	toolByID map[string]int
	finish   string
	Usage    Usage
	usage    map[string]any
}

func (c *CommandCodeToOpenAI) chunk(delta map[string]any, finish any) map[string]any {
	if c.ID == "" {
		c.ID = "chatcmpl-" + randomSuffix()
		c.Created = time.Now().Unix()
		c.toolByID = map[string]int{}
	}
	return map[string]any{
		"id": c.ID, "object": "chat.completion.chunk", "created": c.Created, "model": c.Model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
}

func (c *CommandCodeToOpenAI) lead() map[string]any {
	if c.chunks > 0 {
		return map[string]any{}
	}
	c.chunks++
	return map[string]any{"role": "assistant"}
}

// Transform returns the chunks for one event (nil when it carries nothing).
func (c *CommandCodeToOpenAI) Transform(ev map[string]any) map[string]any {
	if c.Model == "" {
		c.Model = str(ev, "model")
	}
	switch str(ev, "type") {
	case "text-delta":
		text := firstStr(ev, "text", "delta")
		if text == "" {
			return nil
		}
		delta := c.lead()
		delta["content"] = text
		c.chunks++
		return c.chunk(delta, nil)
	case "reasoning-delta":
		text := firstStr(ev, "text")
		if text == "" {
			return nil
		}
		delta := c.lead()
		delta["reasoning_content"] = text
		c.chunks++
		return c.chunk(delta, nil)
	case "tool-input-start":
		id := firstStr(ev, "id", "toolCallId")
		idx, ok := c.toolByID[id]
		if !ok {
			idx = c.toolIdx
			c.toolIdx++
			c.toolByID[id] = idx
		}
		delta := c.lead()
		delta["tool_calls"] = []any{map[string]any{
			"index": idx, "id": id, "type": "function",
			"function": map[string]any{"name": str(ev, "toolName"), "arguments": ""},
		}}
		c.chunks++
		return c.chunk(delta, nil)
	case "tool-input-delta":
		id := firstStr(ev, "id", "toolCallId")
		idx, ok := c.toolByID[id]
		if !ok {
			return nil
		}
		return c.chunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": idx, "function": map[string]any{"arguments": firstStr(ev, "delta", "inputTextDelta")},
		}}}, nil)
	case "tool-call":
		id := str(ev, "toolCallId")
		if _, seen := c.toolByID[id]; seen {
			return nil
		}
		idx := c.toolIdx
		c.toolIdx++
		c.toolByID[id] = idx
		args := ev["input"]
		if _, ok := args.(string); !ok {
			b, _ := json.Marshal(args)
			args = string(b)
		}
		delta := c.lead()
		delta["tool_calls"] = []any{map[string]any{
			"index": idx, "id": id, "type": "function",
			"function": map[string]any{"name": str(ev, "toolName"), "arguments": args},
		}}
		c.chunks++
		return c.chunk(delta, nil)
	case "finish-step":
		c.finish = commandCodeFinish(str(ev, "finishReason"))
		if u, ok := ev["usage"].(map[string]any); ok {
			c.usage = u
			c.Usage = commandCodeUsage(u)
		}
		return nil
	case "finish":
		reason := c.finish
		if reason == "" {
			reason = commandCodeFinish(firstStr(ev, "finishReason"))
		}
		if u, ok := ev["totalUsage"].(map[string]any); ok {
			c.usage = u
			c.Usage = commandCodeUsage(u)
		} else if c.usage != nil {
			c.Usage = commandCodeUsage(c.usage)
		}
		ch := c.chunk(map[string]any{}, reason)
		if c.Usage.PromptTokens+c.Usage.CompletionTokens > 0 {
			ch["usage"] = map[string]any{
				"prompt_tokens":         c.Usage.PromptTokens,
				"completion_tokens":     c.Usage.CompletionTokens,
				"total_tokens":          c.Usage.PromptTokens + c.Usage.CompletionTokens,
				"prompt_tokens_details": map[string]any{"cached_tokens": c.Usage.CacheRead},
			}
		}
		return ch
	}
	return nil
}

// Flush closes a stream that ended without a finish event.
func (c *CommandCodeToOpenAI) Flush() map[string]any {
	reason := c.finish
	if reason == "" {
		reason = "stop"
	}
	return c.chunk(map[string]any{}, reason)
}

// CollectCommandCode folds a whole NDJSON body into a chat.completion object.
func CollectCommandCode(body io.Reader) map[string]any {
	c := &CommandCodeToOpenAI{}
	var content, thinking strings.Builder
	var calls []any
	byIdx := map[int]map[string]any{}
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line == "[DONE]" {
			continue
		}
		line = strings.TrimPrefix(line, "data:")
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if c.Model == "" {
			c.Model = str(ev, "model")
		}
		switch str(ev, "type") {
		case "text-delta":
			content.WriteString(firstStr(ev, "text", "delta"))
		case "reasoning-delta":
			thinking.WriteString(str(ev, "text"))
		case "tool-call":
			args := ev["input"]
			if _, ok := args.(string); !ok {
				b, _ := json.Marshal(args)
				args = string(b)
			}
			calls = append(calls, map[string]any{
				"id": str(ev, "toolCallId"), "type": "function",
				"function": map[string]any{"name": str(ev, "toolName"), "arguments": args},
			})
		case "tool-input-start":
			idx := len(byIdx)
			byIdx[idx] = map[string]any{
				"id": firstStr(ev, "id", "toolCallId"), "type": "function",
				"function": map[string]any{"name": str(ev, "toolName"), "arguments": ""},
			}
		case "tool-input-delta":
			if m := byIdx[len(byIdx)-1]; m != nil {
				fn := m["function"].(map[string]any)
				fn["arguments"] = fn["arguments"].(string) + firstStr(ev, "delta", "inputTextDelta")
			}
		case "finish-step":
			c.finish = commandCodeFinish(str(ev, "finishReason"))
			if u, ok := ev["usage"].(map[string]any); ok {
				c.Usage = commandCodeUsage(u)
			}
		}
	}
	if len(calls) == 0 {
		for i := 0; i < len(byIdx); i++ {
			calls = append(calls, byIdx[i])
		}
	}
	message := map[string]any{"role": "assistant"}
	if content.Len() > 0 {
		message["content"] = content.String()
	}
	if thinking.Len() > 0 {
		message["reasoning_content"] = thinking.String()
	}
	if len(calls) > 0 {
		message["tool_calls"] = calls
		c.finish = "tool_calls"
	}
	if message["content"] == nil && message["tool_calls"] == nil {
		message["content"] = ""
	}
	if c.finish == "" {
		c.finish = "stop"
	}
	out := map[string]any{
		"id": "chatcmpl-" + randomSuffix(), "object": "chat.completion",
		"created": time.Now().Unix(), "model": c.Model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": c.finish}},
	}
	if c.Usage.PromptTokens+c.Usage.CompletionTokens > 0 {
		out["usage"] = map[string]any{
			"prompt_tokens":     c.Usage.PromptTokens,
			"completion_tokens": c.Usage.CompletionTokens,
			"total_tokens":      c.Usage.PromptTokens + c.Usage.CompletionTokens,
		}
	}
	return out
}

func commandCodeUsage(raw map[string]any) Usage {
	u := Usage{
		PromptTokens:     intAt64(raw, "inputTokens"),
		CompletionTokens: intAt64(raw, "outputTokens"),
	}
	if d, ok := raw["inputTokenDetails"].(map[string]any); ok {
		u.CacheRead = intAt64(d, "cacheReadTokens")
		u.CacheCreation = intAt64(d, "cacheWriteTokens")
	}
	return u
}

func commandCodeFinish(reason string) string {
	switch reason {
	case "tool-calls", "tool_calls", "tool_use":
		return "tool_calls"
	case "length", "max_tokens":
		return "length"
	}
	return "stop"
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
