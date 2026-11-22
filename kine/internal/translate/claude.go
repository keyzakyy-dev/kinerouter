package translate

import (
	"encoding/json"
	"strings"
)

// OpenAIRequestToClaude converts an OpenAI Chat Completions body into a Claude
// Messages body. Mirrors open-sse/translator/request/openai-to-claude.js:
// system messages are hoisted, tool results become user blocks placed before
// any other content in the same turn.
func OpenAIRequestToClaude(in *OpenAIRequest, model string) *ClaudeRequest {
	out := &ClaudeRequest{Model: model, Stream: in.Stream}
	if in.MaxTokens != nil {
		out.MaxTokens = *in.MaxTokens
	} else {
		out.MaxTokens = 64000
	}
	if out.MaxTokens > 64000 {
		out.MaxTokens = 64000
	}
	out.Temperature = in.Temperature
	out.TopP = in.TopP

	var systemParts []string
	for _, m := range in.Messages {
		if m.Role == "system" {
			systemParts = append(systemParts, contentToText(m.Content))
		}
	}
	if len(systemParts) > 0 {
		out.System = mustJSON(strings.Join(systemParts, "\n"))
	}

	var pending *ClaudeMessage
	flush := func() {
		if pending != nil && len(pending.Content) > 2 {
			out.Messages = append(out.Messages, *pending)
		}
		pending = nil
	}
	for _, m := range in.Messages {
		if m.Role == "system" {
			continue
		}
		blocks := blocksFromOpenAIMessage(m)
		if len(blocks) == 0 {
			continue
		}
		role := "assistant"
		if m.Role == "user" || m.Role == "tool" {
			role = "user"
		}
		var toolResults, others []ClaudeContentBlock
		for _, b := range blocks {
			if b.Type == "tool_result" {
				toolResults = append(toolResults, b)
			} else {
				others = append(others, b)
			}
		}
		if len(toolResults) > 0 {
			flush()
			out.Messages = append(out.Messages, ClaudeMessage{Role: "user", Content: mustJSON(toolResults)})
		}
		if len(others) > 0 {
			if pending != nil && pending.Role != role {
				flush()
			}
			if pending == nil {
				pending = &ClaudeMessage{Role: role}
			}
			merged := append(existingBlocks(pending.Content), others...)
			pending.Content = mustJSON(merged)
		}
	}
	flush()

	for _, t := range in.Tools {
		out.Tools = append(out.Tools, ClaudeTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		})
	}
	if len(in.ToolChoice) > 0 {
		out.ToolChoice = convertToolChoiceToClaude(in.ToolChoice)
	}
	out.StopSequences = parseStopSequences(in.Stop)
	return out
}

func existingBlocks(raw json.RawMessage) []ClaudeContentBlock {
	if len(raw) == 0 {
		return nil
	}
	var b []ClaudeContentBlock
	json.Unmarshal(raw, &b)
	return b
}

// blocksFromOpenAIMessage flattens one OpenAI message into Claude blocks.
func blocksFromOpenAIMessage(m OpenAIMessage) []ClaudeContentBlock {
	var blocks []ClaudeContentBlock
	if m.Role == "tool" {
		return []ClaudeContentBlock{{
			Type:      "tool_result",
			ToolUseID: m.ToolCallID,
			Content:   mustJSON(contentToText(m.Content)),
		}}
	}
	if len(m.Content) > 0 {
		blocks = append(blocks, blocksFromOpenAIContent(m.Content)...)
	}
	for _, tc := range m.ToolCalls {
		args := json.RawMessage(tc.Function.Arguments)
		if len(args) == 0 || !json.Valid(args) {
			args = json.RawMessage("{}")
		}
		blocks = append(blocks, ClaudeContentBlock{
			Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: args,
		})
	}
	return blocks
}

// blocksFromOpenAIContent handles the string and []part shapes.
func blocksFromOpenAIContent(raw json.RawMessage) []ClaudeContentBlock {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []ClaudeContentBlock{{Type: "text", Text: s}}
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	out := make([]ClaudeContentBlock, 0, len(parts))
	for _, p := range parts {
		typ, _ := p["type"].(string)
		switch typ {
		case "text", "input_text":
			text, _ := p["text"].(string)
			if text != "" {
				out = append(out, ClaudeContentBlock{Type: "text", Text: text})
			}
		case "image_url":
			src := parseImageURLPart(p)
			if src != nil {
				out = append(out, ClaudeContentBlock{Type: "image", Source: src})
			}
		}
	}
	return out
}

func parseImageURLPart(p map[string]any) *ClaudeImageSource {
	iu, ok := p["image_url"].(map[string]any)
	if !ok {
		return nil
	}
	url, _ := iu["url"].(string)
	if strings.HasPrefix(url, "data:") {
		// data:<mime>;base64,<payload>
		meta, payload, found := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
		if !found {
			return nil
		}
		mediaType, _, _ := strings.Cut(meta, ";")
		return &ClaudeImageSource{Type: "base64", MediaType: mediaType, Data: payload}
	}
	if url == "" {
		return nil
	}
	return &ClaudeImageSource{Type: "url", URL: url}
}

// ClaudeRequestToOpenAI is the inverse, used when a Claude client targets an
// OpenAI-compatible provider.
func ClaudeRequestToOpenAI(in *ClaudeRequest, model string) *OpenAIRequest {
	out := &OpenAIRequest{Model: model, Stream: in.Stream, Temperature: in.Temperature, TopP: in.TopP}
	if in.MaxTokens > 0 {
		mt := in.MaxTokens
		out.MaxTokens = &mt
	}
	if len(in.System) > 0 {
		var s string
		if json.Unmarshal(in.System, &s) == nil {
			out.Messages = append(out.Messages, OpenAIMessage{Role: "system", Content: mustJSON(s)})
		} else {
			var parts []ClaudeContentBlock
			if json.Unmarshal(in.System, &parts) == nil {
				var texts []string
				for _, p := range parts {
					if p.Text != "" {
						texts = append(texts, p.Text)
					}
				}
				out.Messages = append(out.Messages, OpenAIMessage{Role: "system", Content: mustJSON(strings.Join(texts, "\n"))})
			}
		}
	}
	for _, m := range in.Messages {
		out.Messages = append(out.Messages, openAIMessagesFromClaude(m)...)
	}
	for _, t := range in.Tools {
		var tool OpenAITool
		tool.Type = "function"
		tool.Function.Name = t.Name
		tool.Function.Description = t.Description
		tool.Function.Parameters = t.InputSchema
		out.Tools = append(out.Tools, tool)
	}
	if len(in.StopSequences) > 0 {
		out.Stop = mustJSON(in.StopSequences)
	}
	return out
}

func openAIMessagesFromClaude(m ClaudeMessage) []OpenAIMessage {
	var blocks []ClaudeContentBlock
	var text string
	if json.Unmarshal(m.Content, &text) == nil {
		return []OpenAIMessage{{Role: m.Role, Content: mustJSON(text)}}
	}
	json.Unmarshal(m.Content, &blocks)

	var out []OpenAIMessage
	var parts []map[string]any
	var toolCalls []OpenAIToolCall
	var toolResults []OpenAIMessage
	for _, b := range blocks {
		switch b.Type {
		case "text":
			parts = append(parts, map[string]any{"type": "text", "text": b.Text})
		case "image":
			if b.Source != nil {
				url := b.Source.URL
				if b.Source.Type == "base64" {
					url = "data:" + b.Source.MediaType + ";base64," + b.Source.Data
				}
				parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
			}
		case "tool_use":
			args := "{}"
			if len(b.Input) > 0 {
				args = string(b.Input)
			}
			toolCalls = append(toolCalls, OpenAIToolCall{ID: b.ID, Type: "function",
				Function: OpenAIFunction{Name: b.Name, Arguments: args}})
		case "tool_result":
			toolResults = append(toolResults, OpenAIMessage{
				Role: "tool", ToolCallID: b.ToolUseID, Content: flattenToolResult(b.Content),
			})
		}
	}
	if len(toolResults) > 0 {
		out = append(out, toolResults...)
	}
	msg := OpenAIMessage{Role: m.Role}
	if len(parts) > 0 {
		if len(parts) == 1 {
			if t, ok := parts[0]["text"].(string); ok {
				msg.Content = mustJSON(t)
			} else {
				msg.Content = mustJSON(parts)
			}
		} else {
			msg.Content = mustJSON(parts)
		}
	}
	msg.ToolCalls = toolCalls
	if msg.Content != nil || len(msg.ToolCalls) > 0 {
		out = append(out, msg)
	}
	return out
}

func flattenToolResult(raw json.RawMessage) json.RawMessage {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return mustJSON(s)
	}
	var parts []ClaudeContentBlock
	if json.Unmarshal(raw, &parts) == nil {
		var texts []string
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		return mustJSON(strings.Join(texts, "\n"))
	}
	return mustJSON("")
}

func convertToolChoiceToClaude(raw json.RawMessage) json.RawMessage {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			return mustJSON(map[string]string{"type": "auto"})
		case "required":
			return mustJSON(map[string]string{"type": "any"})
		case "none":
			return nil
		}
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		return mustJSON(map[string]any{"type": "tool", "name": obj.Function.Name})
	}
	return nil
}

// contentToText flattens an OpenAI content value to a plain string.
func contentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []map[string]any
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if t, ok := p["text"].(string); ok && t != "" {
			texts = append(texts, t)
		}
	}
	return strings.Join(texts, "\n")
}

func parseStopSequences(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []string{s}
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}
