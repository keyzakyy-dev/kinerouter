package translate

// OpenAI Responses API (<-> Chat Completions) translation, ported from
// open-sse/translator/request/openai-responses.js,
// open-sse/translator/response/openai-responses.js and
// open-sse/transformer/responsesTransformer.js.
//
// Responses is a "source" format like any other: requests pivot through Chat
// Completions, responses pivot back. The pivot keeps the Go engine on one
// intermediate shape, exactly as open-sse does.

import (
	"encoding/json"
	"strings"
	"time"
)

const maxResponsesCallIDLen = 64

// ---------------------------------------------------------------- request

// ResponsesToOpenAI converts a Responses API request body to Chat Completions.
func ResponsesRequestToChat(raw map[string]any) map[string]any {
	items, ok := normalizeResponsesInput(raw["input"])
	if !ok {
		return raw
	}
	out := map[string]any{}
	for k, v := range raw {
		if k == "input" || k == "instructions" || k == "include" || k == "store" ||
			k == "prompt_cache_key" || k == "reasoning" || k == "client_metadata" {
			continue
		}
		out[k] = v
	}
	var messages []any
	if s, ok := raw["instructions"].(string); ok && s != "" {
		messages = append(messages, map[string]any{"role": "system", "content": s})
	}

	var pendingAssistant map[string]any
	var pendingToolResults []any
	var pendingReasoning, pendingReasoningEncrypted string
	toolNames := map[string]bool{}

	flushAssistant := func() {
		if pendingAssistant != nil {
			messages = append(messages, pendingAssistant)
			pendingAssistant = nil
		}
	}
	flushResults := func() {
		messages = append(messages, pendingToolResults...)
		pendingToolResults = nil
	}
	attachReasoning := func(m map[string]any) {
		if pendingReasoning != "" {
			m["reasoning_content"] = pendingReasoning
		}
		if pendingReasoningEncrypted != "" {
			m["encrypted_content"] = pendingReasoningEncrypted
		}
		pendingReasoning, pendingReasoningEncrypted = "", ""
	}

	for _, it := range items {
		item, _ := it.(map[string]any)
		if item == nil {
			continue
		}
		typ, _ := item["type"].(string)
		if typ == "" {
			if _, hasRole := item["role"]; hasRole {
				typ = "message"
			}
		}
		switch typ {
		case "message":
			flushAssistant()
			flushResults()
			content := item["content"]
			var conv any = content
			if parts, ok := content.([]any); ok {
				mapped := make([]any, 0, len(parts))
				for _, p := range parts {
					pm, _ := p.(map[string]any)
					if pm == nil {
						continue
					}
					switch pm["type"] {
					case "input_text", "output_text":
						mapped = append(mapped, map[string]any{"type": "text", "text": pm["text"]})
					case "input_image":
						url, _ := pm["image_url"].(string)
						if url == "" {
							url, _ = pm["file_id"].(string)
						}
						detail, _ := pm["detail"].(string)
						if detail == "" {
							detail = "auto"
						}
						mapped = append(mapped, map[string]any{
							"type":      "image_url",
							"image_url": map[string]any{"url": url, "detail": detail},
						})
					default:
						mapped = append(mapped, pm)
					}
				}
				conv = mapped
			}
			msg := map[string]any{"role": item["role"], "content": conv}
			if item["role"] == "assistant" {
				attachReasoning(msg)
			} else {
				pendingReasoning, pendingReasoningEncrypted = "", ""
			}
			messages = append(messages, msg)
		case "function_call", "custom_tool_call":
			if pendingAssistant == nil {
				pendingAssistant = map[string]any{"role": "assistant", "content": nil}
				attachReasoning(pendingAssistant)
			}
			name, _ := item["name"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			var args string
			if typ == "custom_tool_call" {
				toolNames[name] = true
				if in, ok := item["input"].(string); ok {
					args = mustJSONString(map[string]any{"input": in})
				} else {
					args = mustJSONString(map[string]any{"input": item["input"]})
				}
			} else {
				args = coerceResponsesArguments(item["arguments"])
			}
			callID, _ := item["call_id"].(string)
			tcs, _ := pendingAssistant["tool_calls"].([]any)
			pendingAssistant["tool_calls"] = append(tcs, map[string]any{
				"id": callID, "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			})
		case "function_call_output", "custom_tool_call_output":
			flushAssistant()
			flushResults()
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": item["call_id"],
				"content":      coerceResponsesOutput(item["output"]),
			})
		case "additional_tools":
			// Merged below with body.tools.
		case "reasoning":
			if txt := extractReasoningText(item); txt != "" {
				if pendingReasoning != "" {
					pendingReasoning += "\n" + txt
				} else {
					pendingReasoning = txt
				}
			}
			if enc, ok := item["encrypted_content"].(string); ok && enc != "" {
				pendingReasoningEncrypted = enc
			}
		}
		_ = toolNames
	}
	flushAssistant()
	flushResults()

	// Tools: Responses declares functions flat ({type,name,parameters}), Chat
	// nests them under "function". Nameless hosted tools are dropped.
	var extraTools []any
	for _, it := range items {
		if item, ok := it.(map[string]any); ok && item["type"] == "additional_tools" {
			if ts, ok := item["tools"].([]any); ok {
				extraTools = append(extraTools, ts...)
			}
		}
	}
	if bodyTools, ok := raw["tools"].([]any); ok {
		extraTools = append(bodyTools, extraTools...)
	}
	if len(extraTools) > 0 {
		var tools []any
		for _, t := range extraTools {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			if fn, ok := tm["function"].(map[string]any); ok {
				tools = append(tools, map[string]any{"type": "function", "function": fn})
				continue
			}
			name, _ := tm["name"].(string)
			if strings.TrimSpace(name) == "" {
				continue
			}
			fn := map[string]any{
				"name":        name,
				"description": stringOr(tm["description"], ""),
				"parameters":  normalizeToolParameters(tm["parameters"]),
			}
			if tm["type"] == "custom" {
				fn["parameters"] = map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{"type": "string", "description": "Raw freeform input for this custom tool"},
					},
					"required":             []any{"input"},
					"additionalProperties": false,
				}
			}
			if strict, ok := tm["strict"]; ok {
				fn["strict"] = strict
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
	}

	if mot, ok := out["max_output_tokens"]; ok {
		if _, has := out["max_tokens"]; !has {
			out["max_tokens"] = mot
		}
		delete(out, "max_output_tokens")
	}
	if r, ok := raw["reasoning"].(map[string]any); ok {
		if e, ok := r["effort"].(string); ok {
			out["reasoning_effort"] = e
		}
	}
	out["messages"] = messages
	return out
}

// OpenAIToResponses converts a Chat Completions request body to Responses API.
func ChatRequestToResponses(raw map[string]any, model string, stream bool) map[string]any {
	if _, already := raw["input"]; already {
		out := map[string]any{}
		for k, v := range raw {
			out[k] = v
		}
		out["model"] = model
		out["stream"] = true
		if _, ok := out["max_output_tokens"]; !ok {
			for _, k := range []string{"max_completion_tokens", "max_tokens"} {
				if v, ok := out[k]; ok {
					out["max_output_tokens"] = v
					break
				}
			}
		}
		delete(out, "max_tokens")
		delete(out, "max_completion_tokens")
		return out
	}

	out := map[string]any{"model": model, "input": []any{}, "stream": true, "store": false}
	if s, ok := raw["stream"].(bool); ok {
		out["stream"] = stream || s
	}
	var input []any
	var instructions any
	haveInstructions := false

	msgs, _ := raw["messages"].([]any)
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		if msg == nil {
			continue
		}
		role, _ := msg["role"].(string)
		switch role {
		case "system", "developer":
			if !haveInstructions {
				instructions = extractInstructionsText(msg["content"])
				haveInstructions = true
			}
			continue
		case "user", "assistant":
			if role == "assistant" {
				if r := reasoningInputItem(msg); r != nil {
					input = append(input, r)
				}
			}
			ctype := "input_text"
			if role == "assistant" {
				ctype = "output_text"
			}
			var content []any
			switch c := msg["content"].(type) {
			case string:
				content = append(content, map[string]any{"type": ctype, "text": c})
			case []any:
				for _, p := range c {
					pm, _ := p.(map[string]any)
					if pm == nil {
						continue
					}
					switch pm["type"] {
					case "text":
						content = append(content, map[string]any{"type": ctype, "text": pm["text"]})
					case "image_url":
						url, detail := "", "auto"
						switch iu := pm["image_url"].(type) {
						case string:
							url = iu
						case map[string]any:
							url, _ = iu["url"].(string)
							if d, ok := iu["detail"].(string); ok && d != "" {
								detail = d
							}
						}
						content = append(content, map[string]any{"type": "input_image", "image_url": url, "detail": detail})
					case "input_image":
						content = append(content, pm)
					default:
						txt := stringOr(pm["text"], stringOr(pm["content"], ""))
						if txt == "" {
							txt = mustJSONString(pm)
						}
						content = append(content, map[string]any{"type": ctype, "text": txt})
					}
				}
			}
			if len(content) > 0 {
				input = append(input, map[string]any{"type": "message", "role": role, "content": content})
			}
		}
		if role == "assistant" {
			if tcs, ok := msg["tool_calls"].([]any); ok {
				for _, t := range tcs {
					tc, _ := t.(map[string]any)
					fn, _ := tc["function"].(map[string]any)
					if fn == nil {
						continue
					}
					name := strings.TrimSpace(stringOr(fn["name"], ""))
					if name == "" {
						continue
					}
					if len(name) > 128 {
						name = name[:128]
					}
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   clampResponsesCallID(stringOr(tc["id"], "")),
						"name":      name,
						"arguments": coerceResponsesArguments(fn["arguments"]),
					})
				}
			}
		}
		if role == "tool" {
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": clampResponsesCallID(stringOr(msg["tool_call_id"], "")),
				"output":  coerceResponsesOutput(msg["content"]),
			})
		}
	}
	out["input"] = input
	if haveInstructions {
		out["instructions"] = instructions
	} else {
		out["instructions"] = ""
	}

	if tools, ok := raw["tools"].([]any); ok {
		var conv []any
		for _, t := range tools {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			fn, _ := tm["function"].(map[string]any)
			if fn == nil {
				conv = append(conv, tm) // hosted / built-in tool, pass through
				continue
			}
			name := strings.TrimSpace(stringOr(fn["name"], ""))
			if name == "" {
				continue
			}
			if len(name) > 128 {
				name = name[:128]
			}
			tool := map[string]any{
				"type":        "function",
				"name":        name,
				"description": stringOr(fn["description"], ""),
				"parameters":  normalizeToolParameters(fn["parameters"]),
			}
			if s, ok := fn["strict"]; ok {
				tool["strict"] = s
			}
			conv = append(conv, tool)
		}
		out["tools"] = conv
	}

	if v, ok := raw["temperature"]; ok {
		out["temperature"] = v
	}
	if v, ok := raw["top_p"]; ok {
		out["top_p"] = v
	}
	for _, k := range []string{"max_output_tokens", "max_completion_tokens", "max_tokens"} {
		if v, ok := raw[k]; ok {
			out["max_output_tokens"] = v
			break
		}
	}
	if v, ok := raw["reasoning"]; ok {
		out["reasoning"] = v
	} else if e, ok := raw["reasoning_effort"].(string); ok && e != "" {
		out["reasoning"] = map[string]any{"effort": e, "summary": "auto"}
	}
	for _, k := range []string{"service_tier", "prompt_cache_key"} {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}
	return out
}

// normalizeResponsesInput mirrors normalizeResponsesInput in responsesApi.js:
// a string or empty array becomes one user message; invalid input reports false.
func normalizeResponsesInput(input any) ([]any, bool) {
	switch v := input.(type) {
	case nil:
		return nil, false
	case string:
		text := v
		if strings.TrimSpace(text) == "" {
			text = "..."
		}
		return []any{map[string]any{"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": text}}}}, true
	case []any:
		if len(v) == 0 {
			return []any{map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "..."}}}}, true
		}
		return v, true
	}
	return nil, false
}

// clampResponsesCallID truncates overlong ids and mints one when missing.
func clampResponsesCallID(id string) string {
	if id == "" {
		return "call_" + itoa(time.Now().UnixNano())
	}
	if len(id) > maxResponsesCallIDLen {
		return id[:maxResponsesCallIDLen]
	}
	return id
}

// coerceResponsesArguments single-stringifies tool arguments.
func coerceResponsesArguments(v any) string {
	switch x := v.(type) {
	case nil:
		return "{}"
	case string:
		if x == "" {
			return "{}"
		}
		var probe any
		if json.Unmarshal([]byte(x), &probe) != nil {
			return "{}"
		}
		return x
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
}

// coerceResponsesOutput forces function_call_output to a string.
func coerceResponsesOutput(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []any:
		var sb strings.Builder
		for _, c := range x {
			if cm, ok := c.(map[string]any); ok {
				if t, ok := cm["text"].(string); ok {
					sb.WriteString(t)
					continue
				}
			}
			sb.WriteString(mustJSONString(c))
		}
		return sb.String()
	default:
		return mustJSONString(x)
	}
}

func normalizeToolParameters(p any) any {
	m, ok := p.(map[string]any)
	if !ok {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if m["type"] == "object" {
		if _, has := m["properties"]; !has {
			m["properties"] = map[string]any{}
		}
	}
	return m
}

func extractInstructionsText(content any) any {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			if t, ok := pm["text"].(string); ok {
				parts = append(parts, t)
			} else if t, ok := pm["content"].(string); ok {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// reasoningInputItem rebuilds a Responses reasoning item from Chat Completions
// assistant fields (store=false continuity for Codex / Grok CLI).
func reasoningInputItem(msg map[string]any) map[string]any {
	enc := stringOr(msg["encrypted_content"], stringOr(msg["reasoning_encrypted_content"], ""))
	summary := ""
	if s, ok := msg["reasoning_content"].(string); ok && strings.TrimSpace(s) != "" {
		summary = s
	} else if s, ok := msg["reasoning"].(string); ok && strings.TrimSpace(s) != "" {
		summary = s
	} else if ds, ok := msg["reasoning_details"].([]any); ok {
		var parts []string
		for _, d := range ds {
			dm, _ := d.(map[string]any)
			if dm == nil {
				continue
			}
			if s, ok := dm["text"].(string); ok {
				parts = append(parts, s)
			} else if s, ok := dm["content"].(string); ok {
				parts = append(parts, s)
			}
		}
		summary = strings.Join(parts, "\n")
	}
	if enc == "" && summary == "" {
		return nil
	}
	item := map[string]any{"type": "reasoning"}
	if summary != "" {
		item["summary"] = []any{map[string]any{"type": "summary_text", "text": summary}}
	}
	if enc != "" {
		item["encrypted_content"] = enc
	}
	return item
}

func extractReasoningText(item map[string]any) string {
	if ss, ok := item["summary"].([]any); ok {
		var parts []string
		for _, s := range ss {
			if sm, ok := s.(map[string]any); ok {
				if t, ok := sm["text"].(string); ok && t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func stringOr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

func mustJSONString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func itoa(v int64) string { return mustJSONString(v) }
