package translate

// Gemini-native client surface (/v1beta/models/{m}:generateContent|:streamGenerateContent).
// A Gemini client pivots through Chat Completions like every other source format,
// mirroring src/app/api/v1beta/models/[...path]/route.js.

import (
	"encoding/json"
	"strings"
	"time"
)

// GeminiRequestToChat converts a Gemini generateContent body into Chat Completions.
func GeminiRequestToChat(raw map[string]any, model string, stream bool) map[string]any {
	var messages []any
	if si, ok := raw["systemInstruction"].(map[string]any); ok {
		if txt := geminiPartsText(si["parts"]); txt != "" {
			messages = append(messages, map[string]any{"role": "system", "content": txt})
		}
	}
	contents, _ := raw["contents"].([]any)
	for _, c := range contents {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		role := "user"
		if cm["role"] == "model" {
			role = "assistant"
		}
		txt := geminiPartsText(cm["parts"])
		if txt == "" {
			continue
		}
		messages = append(messages, map[string]any{"role": role, "content": txt})
	}
	out := map[string]any{"model": model, "messages": messages, "stream": stream}
	if gc, ok := raw["generationConfig"].(map[string]any); ok {
		for src, dst := range map[string]string{
			"maxOutputTokens": "max_tokens", "temperature": "temperature",
			"topP": "top_p", "topK": "top_k", "stopSequences": "stop",
		} {
			if v, ok := gc[src]; ok {
				out[dst] = v
			}
		}
	}
	// functionDeclarations → OpenAI tools. Not every Gemini client sends tools,
	// but Gemini CLI does and dropping them silently breaks tool calling.
	var tools []any
	groups, _ := raw["tools"].([]any)
	for _, gr := range groups {
		gm, _ := gr.(map[string]any)
		if gm == nil {
			continue
		}
		decls, _ := gm["functionDeclarations"].([]any)
		for _, d := range decls {
			dm, _ := d.(map[string]any)
			if dm == nil {
				continue
			}
			fn := map[string]any{"name": dm["name"]}
			if v, ok := dm["description"]; ok {
				fn["description"] = v
			}
			if v, ok := dm["parameters"]; ok {
				fn["parameters"] = v
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	return out
}

func geminiPartsText(parts any) string {
	list, _ := parts.([]any)
	var sb strings.Builder
	for _, p := range list {
		pm, _ := p.(map[string]any)
		if pm == nil {
			continue
		}
		if txt, ok := pm["text"].(string); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(txt)
		}
	}
	return sb.String()
}

// geminiChunk builds one Gemini SSE payload.
func geminiChunk(model string, parts []any, finish string, usage map[string]any) map[string]any {
	cand := map[string]any{
		"content": map[string]any{"role": "model", "parts": parts},
		"index":   0,
	}
	if finish != "" {
		cand["finishReason"] = finish
	}
	out := map[string]any{"candidates": []any{cand}}
	if finish != "" {
		if usage != nil {
			out["usageMetadata"] = usage
		}
		if model != "" {
			out["modelVersion"] = model
		}
	}
	return out
}

func geminiFinishName(reason string) string {
	switch reason {
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	}
	return "STOP"
}

// OpenAIToGemini converts Chat Completions chunks into Gemini SSE payloads.
// Gemini SSE ends by stream close — there is no [DONE] sentinel.
type OpenAIToGemini struct {
	model string
	usage map[string]any
}

// Transform returns Gemini chunks for one Chat Completions chunk.
func (o *OpenAIToGemini) Transform(chunk map[string]any) []map[string]any {
	if m, _ := chunk["model"].(string); m != "" {
		o.model = m
	}
	if u, ok := chunk["usage"].(map[string]any); ok {
		o.usage = map[string]any{
			"promptTokenCount":     num(u["prompt_tokens"]),
			"candidatesTokenCount": num(u["completion_tokens"]),
			"totalTokenCount":      num(u["total_tokens"]),
		}
		if d, ok := u["completion_tokens_details"].(map[string]any); ok {
			if r := num(d["reasoning_tokens"]); r > 0 {
				o.usage["thoughtsTokenCount"] = r
			}
		}
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	delta, _ := choice["delta"].(map[string]any)
	var parts []any
	if s, ok := delta["reasoning_content"].(string); ok && s != "" {
		parts = append(parts, map[string]any{"text": s, "thought": true})
	}
	if s, ok := delta["content"].(string); ok && s != "" {
		parts = append(parts, map[string]any{"text": s})
	}
	finish, _ := choice["finish_reason"].(string)
	if len(parts) == 0 && finish == "" {
		return nil
	}
	if len(parts) == 0 {
		parts = []any{map[string]any{"text": ""}}
	}
	if finish != "" {
		return []map[string]any{geminiChunk(o.model, parts, geminiFinishName(finish), o.usage)}
	}
	return []map[string]any{geminiChunk(o.model, parts, "", nil)}
}

// OpenAIToGeminiResponse folds a chat.completion body into a Gemini response.
func OpenAIToGeminiResponse(body map[string]any) map[string]any {
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		return body
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	var parts []any
	if s, ok := msg["reasoning_content"].(string); ok && s != "" {
		parts = append(parts, map[string]any{"text": s, "thought": true})
	}
	if s, ok := msg["content"].(string); ok && s != "" {
		parts = append(parts, map[string]any{"text": s})
	}
	tcs, _ := msg["tool_calls"].([]any)
	for _, t := range tcs {
		tm, _ := t.(map[string]any)
		fn, _ := tm["function"].(map[string]any)
		if fn == nil {
			continue
		}
		name, _ := fn["name"].(string)
		args := map[string]any{}
		if s, ok := fn["arguments"].(string); ok && s != "" {
			json.Unmarshal([]byte(s), &args)
		}
		parts = append(parts, map[string]any{"functionCall": map[string]any{"name": name, "args": args}})
	}
	if len(parts) == 0 {
		parts = []any{map[string]any{"text": ""}}
	}
	finish, _ := choice["finish_reason"].(string)
	model, _ := body["model"].(string)
	var usage map[string]any
	if u, ok := body["usage"].(map[string]any); ok {
		usage = map[string]any{
			"promptTokenCount":     num(u["prompt_tokens"]),
			"candidatesTokenCount": num(u["completion_tokens"]),
			"totalTokenCount":      num(u["total_tokens"]),
		}
	}
	out := geminiChunk(model, parts, geminiFinishName(finish), usage)
	out["responseId"] = body["id"]
	out["createTime"] = time.Now().UTC().Format(time.RFC3339)
	return out
}
