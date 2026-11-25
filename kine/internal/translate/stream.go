package translate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// Usage tracks token accounting across a translated stream.
type Usage struct {
	PromptTokens     int64
	CompletionTokens int64
	CacheRead        int64
	CacheCreation    int64
}

// ---------------------------------------------------------------- Claude SSE → OpenAI chunks

// ClaudeStreamToOpenAI converts one decoded Claude SSE event into zero or more
// OpenAI chunk JSON payloads. State must live for the whole stream.
// Mirrors open-sse/translator/response/claude-to-openai.js.
type ClaudeToOpenAI struct {
	State ClaudeStreamState
	msgID string
	model string
	Usage Usage
}

type ClaudeStreamState struct {
	toolCalls   map[int]int // claude block index → openai tool index
	nextToolIdx int
	closed      bool
}

func (c *ClaudeToOpenAI) BlockIndex(block int) int {
	if c.State.toolCalls == nil {
		c.State.toolCalls = map[int]int{}
	}
	if idx, ok := c.State.toolCalls[block]; ok {
		return idx
	}
	idx := c.State.nextToolIdx
	c.State.toolCalls[block] = idx
	c.State.nextToolIdx++
	return idx
}

// Transform returns OpenAI chunks for one Claude SSE event. A nil/empty result
// means the event carries nothing to forward.
func (c *ClaudeToOpenAI) Transform(event map[string]any) []map[string]any {
	typ, _ := event["type"].(string)
	var out []map[string]any
	switch typ {
	case "message_start":
		if msg, ok := event["message"].(map[string]any); ok {
			c.msgID, _ = msg["id"].(string)
			c.model, _ = msg["model"].(string)
			if usage, ok := msg["usage"].(map[string]any); ok {
				c.Usage.PromptTokens = num(usage["input_tokens"])
				c.Usage.CacheRead = num(usage["cache_read_input_tokens"])
				c.Usage.CacheCreation = num(usage["cache_creation_input_tokens"])
			}
		}
		out = append(out, c.chunk(map[string]any{"role": "assistant"}, nil))
	case "content_block_start":
		block, _ := event["content_block"].(map[string]any)
		if block == nil {
			break
		}
		if t, _ := block["type"].(string); t == "tool_use" {
			idx := c.BlockIndex(int(num(event["index"])))
			out = append(out, c.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": idx,
				"id":    block["id"],
				"type":  "function",
				"function": map[string]any{
					"name":      block["name"],
					"arguments": "",
				},
			}}}, nil))
		}
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		if delta == nil {
			break
		}
		switch delta["type"] {
		case "text_delta":
			if s, ok := delta["text"].(string); ok && s != "" {
				out = append(out, c.chunk(map[string]any{"content": s}, nil))
			}
		case "thinking_delta":
			if s, ok := delta["thinking"].(string); ok && s != "" {
				out = append(out, c.chunk(map[string]any{"reasoning_content": s}, nil))
			}
		case "input_json_delta":
			if s, ok := delta["partial_json"].(string); ok && s != "" {
				idx := c.BlockIndex(int(num(event["index"])))
				out = append(out, c.chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index":    idx,
					"function": map[string]any{"arguments": s},
				}}}, nil))
			}
		}
	case "message_delta":
		if usage, ok := event["usage"].(map[string]any); ok {
			c.Usage.CompletionTokens = num(usage["output_tokens"])
			if v := num(usage["input_tokens"]); v > 0 {
				c.Usage.PromptTokens = v
			}
		}
		if d, ok := event["delta"].(map[string]any); ok {
			if reason, ok := d["stop_reason"].(string); ok && reason != "" {
				c.State.closed = true
				fr := claudeStopToOpenAI(reason)
				out = append(out, c.chunk(map[string]any{}, &fr))
			}
		}
	case "message_stop":
		if !c.State.closed {
			c.State.closed = true
			fr := "stop"
			out = append(out, c.chunk(map[string]any{}, &fr))
		}
	}
	return out
}

func (c *ClaudeToOpenAI) chunk(delta map[string]any, finish *string) map[string]any {
	model := c.model
	if model == "" {
		model = "unknown"
	}
	id := c.msgID
	if id == "" {
		id = "chatcmpl-" + randomSuffix()
	} else if !strings.HasPrefix(id, "chatcmpl-") {
		id = "chatcmpl-" + id
	}
	ch := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finishValue(finish),
		}},
	}
	return ch
}

// FinalUsage emits the trailing usage-only chunk, matching OpenAI's
// stream_options.include_usage behaviour.
func (c *ClaudeToOpenAI) FinalUsage() map[string]any {
	return map[string]any{
		"id":      "chatcmpl-" + c.msgID,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   c.model,
		"choices": []any{},
		"usage": map[string]any{
			"prompt_tokens":     c.Usage.PromptTokens + c.Usage.CacheRead + c.Usage.CacheCreation,
			"completion_tokens": c.Usage.CompletionTokens,
			"total_tokens":      c.Usage.PromptTokens + c.Usage.CacheRead + c.Usage.CacheCreation + c.Usage.CompletionTokens,
		},
	}
}

// finishValue keeps the "finish_reason": null shape clients expect on
// non-final chunks instead of emitting a pointer.
func finishValue(f *string) any {
	if f == nil {
		return nil
	}
	return *f
}

func claudeStopToOpenAI(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	}
	return "stop"
}

// ---------------------------------------------------------------- Claude non-stream → OpenAI

func ClaudeResponseToOpenAI(resp *ClaudeMessageResp) map[string]any {
	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []any
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			content.WriteString(b.Text)
		case "thinking":
			reasoning.WriteString(b.Thinking)
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id": b.ID, "type": "function",
				"function": map[string]any{"name": b.Name, "arguments": args},
			})
		}
	}
	msg := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	finish := "stop"
	if resp.StopReason != nil {
		finish = claudeStopToOpenAI(*resp.StopReason)
	}
	usage := map[string]any{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0}
	if resp.Usage != nil {
		prompt := resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens
		usage = map[string]any{
			"prompt_tokens": prompt, "completion_tokens": resp.Usage.OutputTokens,
			"total_tokens": prompt + resp.Usage.OutputTokens,
		}
	}
	return map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{
			"index": 0, "message": msg, "finish_reason": finish,
		}},
		"usage": usage,
	}
}

// ---------------------------------------------------------------- OpenAI SSE → Claude events

// OpenAIToClaude converts OpenAI streaming chunks into Claude SSE events.
// Mirrors open-sse/translator/response/openai-to-claude.js.
type OpenAIToClaude struct {
	State       OpenAIStreamState
	Usage       Usage
	MessageID   string
	Model       string
	nextBlock   int
	blockByKind map[string]int
	blockOpen   map[int]string
	// pendingStop holds the finish reason whose terminal is deferred: OpenAI
	// sends `usage` in a separate chunk after the finish chunk, so closing the
	// message immediately would report input/cache tokens as 0.
	pendingStop *string
}

// flushTerminal emits message_delta + message_stop once, or nothing while no
// finish reason is pending.
func (o *OpenAIToClaude) flushTerminal() []map[string]any {
	if o.State.Closed || o.pendingStop == nil {
		return nil
	}
	o.State.Closed = true
	// OpenAI's prompt_tokens already includes the cached ones; Claude expects
	// input_tokens to exclude them, or a client that sums both double counts.
	input := o.Usage.PromptTokens - o.Usage.CacheRead - o.Usage.CacheCreation
	if input < 0 {
		input = 0
	}
	usage := map[string]any{"input_tokens": input, "output_tokens": o.Usage.CompletionTokens}
	if o.Usage.CacheRead > 0 {
		usage["cache_read_input_tokens"] = o.Usage.CacheRead
	}
	if o.Usage.CacheCreation > 0 {
		usage["cache_creation_input_tokens"] = o.Usage.CacheCreation
	}
	return []map[string]any{
		{"type": "message_delta",
			"delta": map[string]any{"stop_reason": openAIFinishToClaude(*o.pendingStop), "stop_sequence": nil},
			"usage": usage},
		{"type": "message_stop"},
	}
}

type OpenAIStreamState struct {
	Started  bool
	Closed   bool
	ToolKeys map[int]bool
}

func (o *OpenAIToClaude) init() {
	if o.blockByKind == nil {
		o.blockByKind = map[string]int{}
		o.blockOpen = map[int]string{}
	}
}

func (o *OpenAIToClaude) startBlock(kind string) []map[string]any {
	o.init()
	if idx, ok := o.blockByKind[kind]; ok && o.blockOpen[idx] == kind {
		return nil
	}
	idx := o.nextBlock
	o.nextBlock++
	o.blockByKind[kind] = idx
	o.blockOpen[idx] = kind
	var block map[string]any
	switch kind {
	case "text":
		block = map[string]any{"type": "text", "text": ""}
	case "thinking":
		block = map[string]any{"type": "thinking", "thinking": ""}
	}
	return []map[string]any{{"type": "content_block_start", "index": idx, "content_block": block}}
}

func (o *OpenAIToClaude) closeBlock(kind string) []map[string]any {
	o.init()
	idx, ok := o.blockByKind[kind]
	if !ok || o.blockOpen[idx] != kind {
		return nil
	}
	delete(o.blockOpen, idx)
	return []map[string]any{{"type": "content_block_stop", "index": idx}}
}

// Transform converts one OpenAI chunk into Claude events.
func (o *OpenAIToClaude) Transform(chunk map[string]any) []map[string]any {
	var out []map[string]any
	o.init()
	if !o.State.Started {
		o.State.Started = true
		if id, ok := chunk["id"].(string); ok {
			o.MessageID = strings.TrimPrefix(id, "chatcmpl-")
		}
		if o.MessageID == "" {
			o.MessageID = "msg_" + randomSuffix()
		}
		if m, ok := chunk["model"].(string); ok {
			o.Model = m
		}
		out = append(out, map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id": o.MessageID, "type": "message", "role": "assistant",
				"model": o.Model, "content": []any{},
				"stop_reason": nil, "stop_sequence": nil,
				"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	}
	if usage, ok := chunk["usage"].(map[string]any); ok {
		o.Usage.PromptTokens = num(usage["prompt_tokens"])
		o.Usage.CompletionTokens = num(usage["completion_tokens"])
		if d, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			o.Usage.CacheRead = num(d["cached_tokens"])
			o.Usage.CacheCreation = num(d["cache_creation_tokens"])
		}
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		// A usage-only chunk arrives after the finish chunk, so this is the
		// first point where the terminal can carry real token counts.
		return append(out, o.flushTerminal()...)
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)

	if delta != nil {
		if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
			out = append(out, o.closeBlock("text")...)
			out = append(out, o.startBlock("thinking")...)
			idx := o.blockByKind["thinking"]
			out = append(out, map[string]any{"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": "thinking_delta", "thinking": rc}})
		}
		if c, ok := delta["content"].(string); ok && c != "" {
			out = append(out, o.closeBlock("thinking")...)
			out = append(out, o.startBlock("text")...)
			idx := o.blockByKind["text"]
			out = append(out, map[string]any{"type": "content_block_delta", "index": idx,
				"delta": map[string]any{"type": "text_delta", "text": c}})
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, raw := range tcs {
				tc, _ := raw.(map[string]any)
				if tc == nil {
					continue
				}
				fn, _ := tc["function"].(map[string]any)
				if fn != nil {
					if name, ok := fn["name"].(string); ok && name != "" {
						out = append(out, o.closeBlock("text")...)
						out = append(out, o.closeBlock("thinking")...)
						idx := o.nextBlock
						o.nextBlock++
						o.blockByKind["tool:"+name] = idx
						o.blockOpen[idx] = "tool"
						out = append(out, map[string]any{
							"type": "content_block_start", "index": idx,
							"content_block": map[string]any{
								"type": "tool_use", "id": tc["id"], "name": name, "input": map[string]any{},
							},
						})
					}
					if args, ok := fn["arguments"].(string); ok && args != "" {
						idx := o.nextBlock - 1
						out = append(out, map[string]any{"type": "content_block_delta", "index": idx,
							"delta": map[string]any{"type": "input_json_delta", "partial_json": args}})
					}
				}
			}
		}
	}

	if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
		out = append(out, o.closeBlock("text")...)
		out = append(out, o.closeBlock("thinking")...)
		for idx, kind := range o.blockOpen {
			if kind == "tool" {
				out = append(out, map[string]any{"type": "content_block_stop", "index": idx})
				delete(o.blockOpen, idx)
			}
		}
		// Hold the terminal: the trailing usage chunk still has to arrive, and
		// message_delta is the only place Claude clients read input tokens.
		o.pendingStop = &fr
	}
	return out
}

// Finish closes the message when the upstream ended without a usable finish
// chunk, and flushes a terminal that is still deferred.
func (o *OpenAIToClaude) Finish() []map[string]any {
	if o.State.Closed {
		return nil
	}
	if o.pendingStop == nil {
		stop := "stop"
		o.pendingStop = &stop
	}
	var out []map[string]any
	out = append(out, o.closeBlock("text")...)
	out = append(out, o.closeBlock("thinking")...)
	for idx, kind := range o.blockOpen {
		if kind == "tool" {
			out = append(out, map[string]any{"type": "content_block_stop", "index": idx})
			delete(o.blockOpen, idx)
		}
	}
	out = append(out, o.flushTerminal()...)
	return out
}

func openAIFinishToClaude(fr string) string {
	switch fr {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "refusal"
	}
	return "end_turn"
}

// OpenAIToClaudeResponse converts a non-streaming OpenAI body to Claude.
func OpenAIToClaudeResponse(in map[string]any) map[string]any {
	var content []any
	var stopReason any = "end_turn"
	model, _ := in["model"].(string)
	id, _ := in["id"].(string)
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}

	if u, ok := in["usage"].(map[string]any); ok {
		usage["input_tokens"] = num(u["prompt_tokens"])
		usage["output_tokens"] = num(u["completion_tokens"])
		if d, ok := u["prompt_tokens_details"].(map[string]any); ok {
			if v := num(d["cached_tokens"]); v > 0 {
				usage["cache_read_input_tokens"] = v
			}
		}
	}
	if choices, ok := in["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
					content = append(content, map[string]any{"type": "thinking", "thinking": rc})
				}
				if txt, ok := msg["content"].(string); ok && txt != "" {
					content = append(content, map[string]any{"type": "text", "text": txt})
				}
				if tcs, ok := msg["tool_calls"].([]any); ok {
					for _, raw := range tcs {
						tc, _ := raw.(map[string]any)
						if tc == nil {
							continue
						}
						fn, _ := tc["function"].(map[string]any)
						var input any = map[string]any{}
						if fn != nil {
							if args, ok := fn["arguments"].(string); ok {
								json.Unmarshal([]byte(args), &input)
							}
						}
						content = append(content, map[string]any{
							"type": "tool_use", "id": tc["id"],
							"name": fn["name"], "input": input,
						})
					}
				}
			}
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				stopReason = openAIFinishToClaude(fr)
			}
		}
	}
	if content == nil {
		content = []any{}
	}
	return map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stopReason, "stop_sequence": nil,
		"usage": usage,
	}
}

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	}
	return 0
}

func randomSuffix() string {
	b := make([]byte, 12)
	rand.Read(b)
	return hex.EncodeToString(b)
}
