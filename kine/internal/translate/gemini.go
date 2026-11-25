package translate

import (
	"encoding/json"
	"strings"
	"time"
)

// GeminiContents is the wire shape shared by Gemini and Vertex.
type GeminiContents struct {
	Contents          []GeminiContent  `json:"contents"`
	SystemInstruction *GeminiContent   `json:"systemInstruction,omitempty"`
	GenerationConfig  map[string]any   `json:"generationConfig,omitempty"`
	Tools             []map[string]any `json:"tools,omitempty"`
}

type GeminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *GeminiFuncCall   `json:"functionCall,omitempty"`
	FunctionResponse *GeminiFuncResp   `json:"functionResponse,omitempty"`
	InlineData       *GeminiInlineData `json:"inlineData,omitempty"`
}

type GeminiFuncCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type GeminiFuncResp struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type GeminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

// ToGemini converts an OpenAI-format request body to the Gemini wire shape.
func ToGemini(raw map[string]any, model string, stream bool) (map[string]any, error) {
	var in OpenAIRequest
	if err := remarshalInto(raw, &in); err != nil {
		return nil, err
	}
	out := GeminiContents{}
	gen := map[string]any{}
	if mt := RequestedMaxTokens(&in); mt != nil {
		gen["maxOutputTokens"] = *mt
	}
	if in.Temperature != nil {
		gen["temperature"] = *in.Temperature
	}
	if in.TopP != nil {
		gen["topP"] = *in.TopP
	}
	if len(gen) > 0 {
		out.GenerationConfig = gen
	}

	var system strings.Builder
	// A tool result must carry the name of the function it answers, not the
	// tool_call_id: Gemini rejects `functionResponse.name:"call_abc"` because no
	// declaration is called that. An OpenAI tool_call_id is only unique within
	// its own assistant turn, so names are queued per id and consumed in
	// document order — a replayed id otherwise renames an earlier result.
	toolNames := map[string][]string{}
	toolCursor := map[string]int{}
	nextToolName := func(id string) string {
		queue := toolNames[id]
		if len(queue) > toolCursor[id] {
			name := queue[toolCursor[id]]
			toolCursor[id]++
			return name
		}
		if len(queue) > 0 {
			return queue[len(queue)-1]
		}
		return ""
	}
	for _, m := range in.Messages {
		if m.Role != "assistant" {
			continue
		}
		for _, tc := range m.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				toolNames[tc.ID] = append(toolNames[tc.ID], tc.Function.Name)
			}
		}
	}
	for _, m := range in.Messages {
		switch m.Role {
		case "system":
			if system.Len() > 0 {
				system.WriteString("\n")
			}
			system.WriteString(contentToText(m.Content))
		case "tool":
			name := m.Name
			if name == "" {
				name = nextToolName(m.ToolCallID)
			}
			if name == "" {
				name = m.ToolCallID
			}
			out.Contents = append(out.Contents, GeminiContent{Role: "user", Parts: []GeminiPart{{
				FunctionResponse: &GeminiFuncResp{Name: name, Response: map[string]any{"result": contentToText(m.Content)}},
			}}})
		default:
			role := "user"
			if m.Role == "assistant" {
				role = "model"
			}
			parts := geminiParts(m)
			if len(parts) > 0 {
				out.Contents = append(out.Contents, GeminiContent{Role: role, Parts: parts})
			}
		}
	}
	if system.Len() > 0 {
		out.SystemInstruction = &GeminiContent{Parts: []GeminiPart{{Text: system.String()}}}
	}
	if len(in.Tools) > 0 {
		decls := make([]map[string]any, 0, len(in.Tools))
		for _, t := range in.Tools {
			var schema any
			json.Unmarshal(t.Function.Parameters, &schema)
			decls = append(decls, map[string]any{
				"name": t.Function.Name, "description": t.Function.Description, "parameters": schema,
			})
		}
		out.Tools = []map[string]any{{"functionDeclarations": decls}}
	}
	body := map[string]any{
		"contents":          out.Contents,
		"systemInstruction": out.SystemInstruction,
		"generationConfig":  out.GenerationConfig,
		"tools":             out.Tools,
	}
	// Carried through untouched: Cloud Code reads it, and no client field
	// maps to it, so it can only have been set by prompt injection.
	if ss, ok := raw["safetySettings"]; ok {
		body["safetySettings"] = ss
	}
	return body, nil
}

func geminiParts(m OpenAIMessage) []GeminiPart {
	var parts []GeminiPart
	var single string
	var raw []map[string]any
	if len(m.Content) > 0 {
		if json.Unmarshal(m.Content, &single) == nil {
			if single != "" {
				parts = append(parts, GeminiPart{Text: single})
			}
		} else if json.Unmarshal(m.Content, &raw) == nil {
			for _, p := range raw {
				typ, _ := p["type"].(string)
				switch typ {
				case "text", "input_text":
					if t, ok := p["text"].(string); ok && t != "" {
						parts = append(parts, GeminiPart{Text: t})
					}
				case "image_url":
					if iu, ok := p["image_url"].(map[string]any); ok {
						if url, ok := iu["url"].(string); ok && strings.HasPrefix(url, "data:") {
							meta, payload, _ := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
							mime, _, _ := strings.Cut(meta, ";")
							parts = append(parts, GeminiPart{InlineData: &GeminiInlineData{MimeType: mime, Data: payload}})
						}
					}
				}
			}
		}
	}
	for _, tc := range m.ToolCalls {
		args := map[string]any{}
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &args)
		}
		parts = append(parts, GeminiPart{FunctionCall: &GeminiFuncCall{Name: tc.Function.Name, Args: args}})
	}
	return parts
}

func remarshalInto(raw map[string]any, dst any) error {
	b, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// ---------------------------------------------------------------- Gemini → OpenAI response
//
// Ported from open-sse/translator/response/gemini-to-openai.js. Gemini marks
// model-internal thinking with `thought: true` on a part; that becomes
// reasoning_content, everything else is assistant text.

// GeminiToOpenAI converts Gemini SSE chunks into OpenAI chat.completion.chunk
// payloads. One instance per stream.
type GeminiToOpenAI struct {
	id           string
	model        string
	started      bool
	toolIdx      int
	toolCalls    int
	reasoning    strings.Builder
	usage        map[string]any
	usageTokens  Usage
	reasoningTok int64
	finishReason string
}

func (g *GeminiToOpenAI) chunk(delta map[string]any, finish *string) map[string]any {
	id := g.id
	if id == "" {
		id = "chatcmpl-" + randomSuffix()
	}
	model := g.model
	if model == "" {
		model = "gemini"
	}
	return map[string]any{
		"id": id, "object": "chat.completion.chunk",
		"created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{
			"index": 0, "delta": delta, "finish_reason": finishValue(finish),
		}},
	}
}

// Transform returns OpenAI chunks for one Gemini SSE payload.
func (g *GeminiToOpenAI) Transform(chunk map[string]any) []map[string]any {
	if chunk == nil {
		return nil
	}
	resp := chunk
	if inner, ok := chunk["response"].(map[string]any); ok {
		resp = inner
	}
	cands, _ := resp["candidates"].([]any)
	if len(cands) == 0 {
		return nil
	}
	cand, _ := cands[0].(map[string]any)
	if cand == nil {
		return nil
	}
	var out []map[string]any
	if !g.started {
		g.started = true
		g.id, _ = resp["responseId"].(string)
		if g.id == "" {
			g.id = "chatcmpl-" + randomSuffix()
		}
		g.model, _ = resp["modelVersion"].(string)
		out = append(out, g.chunk(map[string]any{"role": "assistant"}, nil))
	}

	if content, ok := cand["content"].(map[string]any); ok {
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			thought, _ := pm["thought"].(bool)
			if txt, ok := pm["text"].(string); ok && txt != "" {
				if thought {
					g.reasoning.WriteString(txt)
					out = append(out, g.chunk(map[string]any{"reasoning_content": txt}, nil))
				} else {
					out = append(out, g.chunk(map[string]any{"content": txt}, nil))
				}
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args := fc["args"]
				if args == nil {
					args = map[string]any{}
				}
				callID, _ := fc["id"].(string)
				if callID == "" {
					callID = name + "-" + randomSuffix()
				}
				idx := g.toolIdx
				g.toolIdx++
				g.toolCalls++
				out = append(out, g.chunk(map[string]any{"tool_calls": []any{map[string]any{
					"index": idx, "id": callID, "type": "function",
					"function": map[string]any{"name": name, "arguments": mustJSONString(args)},
				}}}, nil))
			}
			if inline, ok := pm["inlineData"].(map[string]any); ok {
				if data, ok := inline["data"].(string); ok && data != "" {
					mime, _ := inline["mimeType"].(string)
					if mime == "" {
						mime = "image/png"
					}
					out = append(out, g.chunk(map[string]any{
						"content": "![image](data:" + mime + ";base64," + data + ")",
					}, nil))
				}
			}
		}
	}

	if um, ok := resp["usageMetadata"].(map[string]any); ok {
		if u := geminiUsage(um); u != nil {
			g.usage = u.toMap()
			g.usageTokens = Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens}
			if u.PromptTokensDetails != nil {
				g.usageTokens.CacheRead = u.PromptTokensDetails.CachedTokens
			}
			if u.CompletionTokensDetails != nil {
				g.reasoningTok = u.CompletionTokensDetails.ReasoningTokens
			}
		}
	}

	if fr, ok := cand["finishReason"].(string); ok && fr != "" {
		finish := geminiFinishToOpenAI(fr)
		if finish == "stop" && g.toolCalls > 0 {
			finish = "tool_calls"
		}
		g.finishReason = finish
		final := g.chunk(map[string]any{}, &finish)
		if g.usage != nil {
			final["usage"] = g.usage
		}
		out = append(out, final)
	}
	return out
}

// Flush emits the finish chunk when the upstream closed without a finishReason.
func (g *GeminiToOpenAI) Flush() []map[string]any {
	if g.finishReason != "" {
		return nil
	}
	finish := "stop"
	if g.toolCalls > 0 {
		finish = "tool_calls"
	}
	g.finishReason = finish
	final := g.chunk(map[string]any{}, &finish)
	if g.usage != nil {
		final["usage"] = g.usage
	}
	return []map[string]any{final}
}

// Usage reports the tokens seen so far.
func (g *GeminiToOpenAI) UsageTokens() (Usage, int64) {
	return g.usageTokens, g.reasoningTok
}

// geminiUsage maps usageMetadata → OpenAI usage (thoughts count as completion,
// cachedContentTokenCount as cache read, mirroring concerns/usage.js).
func geminiUsage(raw map[string]any) *OpenAIUsage {
	prompt := num(raw["promptTokenCount"])
	thoughts := num(raw["thoughtsTokenCount"])
	cands := num(raw["candidatesTokenCount"])
	total := num(raw["totalTokenCount"])
	if cands == 0 && total > 0 {
		cands = total - prompt - thoughts
		if cands < 0 {
			cands = 0
		}
	}
	u := &OpenAIUsage{
		PromptTokens:     prompt,
		CompletionTokens: cands + thoughts,
		TotalTokens:      total,
	}
	if cached := num(raw["cachedContentTokenCount"]); cached > 0 {
		u.PromptTokensDetails = &struct {
			CachedTokens        int64 `json:"cached_tokens"`
			CacheCreationTokens int64 `json:"cache_creation_tokens"`
		}{CachedTokens: cached}
	}
	if thoughts > 0 {
		u.CompletionTokensDetails = &struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		}{ReasoningTokens: thoughts}
	}
	return u
}

// toMap flattens usage for JSON embedding.
func (u *OpenAIUsage) toMap() map[string]any {
	m := map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
		"total_tokens": u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		m["prompt_tokens_details"] = map[string]any{"cached_tokens": u.PromptTokensDetails.CachedTokens}
	}
	if u.CompletionTokensDetails != nil {
		m["completion_tokens_details"] = map[string]any{"reasoning_tokens": u.CompletionTokensDetails.ReasoningTokens}
	}
	return m
}

func geminiFinishToOpenAI(reason string) string {
	switch strings.ToUpper(reason) {
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT":
		return "content_filter"
	}
	return "stop"
}

// GeminiResponseToOpenAI folds a non-stream Gemini response into a
// chat.completion body (nonStreamingHandler.js).
func GeminiResponseToOpenAI(body map[string]any) map[string]any {
	resp := body
	if inner, ok := body["response"].(map[string]any); ok {
		resp = inner
	}
	cands, _ := resp["candidates"].([]any)
	if len(cands) == 0 {
		return body
	}
	cand, _ := cands[0].(map[string]any)
	var text, reasoning strings.Builder
	var toolCalls []any
	if content, ok := cand["content"].(map[string]any); ok {
		parts, _ := content["parts"].([]any)
		for _, p := range parts {
			pm, _ := p.(map[string]any)
			if pm == nil {
				continue
			}
			thought, _ := pm["thought"].(bool)
			if txt, ok := pm["text"].(string); ok {
				if thought {
					reasoning.WriteString(txt)
				} else {
					text.WriteString(txt)
				}
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok {
				name, _ := fc["name"].(string)
				args := fc["args"]
				if args == nil {
					args = map[string]any{}
				}
				callID, _ := fc["id"].(string)
				if callID == "" {
					callID = "call_" + name + "_" + randomSuffix()
				}
				toolCalls = append(toolCalls, map[string]any{
					"id": callID, "type": "function",
					"function": map[string]any{"name": name, "arguments": mustJSONString(args)},
				})
			}
			if inline, ok := pm["inlineData"].(map[string]any); ok {
				if data, ok := inline["data"].(string); ok && data != "" {
					mime, _ := inline["mimeType"].(string)
					if mime == "" {
						mime = "image/png"
					}
					text.WriteString("\n![image](data:" + mime + ";base64," + data + ")\n")
				}
			}
		}
	}
	msg := map[string]any{"role": "assistant"}
	if text.Len() > 0 {
		msg["content"] = text.String()
	}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	if msg["content"] == nil && msg["tool_calls"] == nil {
		msg["content"] = ""
	}
	finish := "stop"
	if fr, ok := cand["finishReason"].(string); ok && fr != "" {
		finish = geminiFinishToOpenAI(fr)
	}
	if finish == "stop" && len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	id, _ := resp["responseId"].(string)
	if id == "" {
		id = randomSuffix()
	}
	model, _ := resp["modelVersion"].(string)
	if model == "" {
		model = "gemini"
	}
	result := map[string]any{
		"id": "chatcmpl-" + id, "object": "chat.completion",
		"created": time.Now().Unix(), "model": model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
	}
	if um, ok := resp["usageMetadata"].(map[string]any); ok {
		if u := geminiUsage(um); u != nil {
			result["usage"] = u.toMap()
		}
	}
	return result
}
