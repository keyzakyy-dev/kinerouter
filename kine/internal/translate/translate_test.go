package translate

import (
	"encoding/json"
	"testing"
)

// Self-check for the four translation paths that carry most traffic.
func TestOpenAIToClaudeRequest(t *testing.T) {
	in := &OpenAIRequest{
		Model:  "claude-sonnet-4",
		Stream: true,
		Messages: []OpenAIMessage{
			{Role: "system", Content: json.RawMessage(`"be terse"`)},
			{Role: "user", Content: json.RawMessage(`"hello"`)},
			{Role: "assistant", ToolCalls: []OpenAIToolCall{{
				ID: "t1", Type: "function",
				Function: OpenAIFunction{Name: "Read", Arguments: `{"file_path":"/x"}`},
			}}},
			{Role: "tool", ToolCallID: "t1", Content: json.RawMessage(`"contents"`)},
		},
	}
	out := OpenAIRequestToClaude(in, in.Model)
	if string(out.System) != `"be terse"` {
		t.Fatalf("system not hoisted: %s", out.System)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("want 3 messages (user, assistant, tool_result), got %d", len(out.Messages))
	}
	if out.Messages[1].Role != "assistant" {
		t.Fatalf("tool_use must stay on assistant turn, got %s", out.Messages[1].Role)
	}
	var toolUse []ClaudeContentBlock
	if err := json.Unmarshal(out.Messages[1].Content, &toolUse); err != nil {
		t.Fatal(err)
	}
	if len(toolUse) != 1 || toolUse[0].Type != "tool_use" || toolUse[0].Name != "Read" {
		t.Fatalf("bad tool_use block: %+v", toolUse)
	}
	var toolResult []ClaudeContentBlock
	if err := json.Unmarshal(out.Messages[2].Content, &toolResult); err != nil {
		t.Fatal(err)
	}
	if len(toolResult) != 1 || toolResult[0].ToolUseID != "t1" {
		t.Fatalf("tool_result not linked: %+v", toolResult)
	}
}

func TestClaudeStreamToOpenAI(t *testing.T) {
	c := &ClaudeToOpenAI{}
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{"id": "msg_9", "model": "m",
			"usage": map[string]any{"input_tokens": float64(11)}}},
		{"type": "content_block_delta", "index": float64(0), "delta": map[string]any{"type": "text_delta", "text": "ab"}},
		{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]any{"output_tokens": float64(2)}},
	}
	var contents []string
	var finish string
	for _, ev := range events {
		for _, ch := range c.Transform(ev) {
			choices := ch["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice := choices[0].(map[string]any)
			if fr, ok := choice["finish_reason"].(string); ok {
				finish = fr
			}
			delta, _ := choice["delta"].(map[string]any)
			if s, ok := delta["content"].(string); ok {
				contents = append(contents, s)
			}
		}
	}
	if len(contents) != 1 || contents[0] != "ab" {
		t.Fatalf("bad content stream: %v", contents)
	}
	if finish != "stop" {
		t.Fatalf("bad finish reason: %q", finish)
	}
	if c.Usage.PromptTokens != 11 || c.Usage.CompletionTokens != 2 {
		t.Fatalf("usage not captured: %+v", c.Usage)
	}
}

func TestOpenAIToClaudeStreamRoundTrip(t *testing.T) {
	o := &OpenAIToClaude{}
	var sawStart, sawText, sawStop bool
	feed := []map[string]any{
		{"id": "chatcmpl-1", "model": "m", "choices": []any{map[string]any{
			"index": float64(0), "delta": map[string]any{"role": "assistant"}}}},
		{"id": "chatcmpl-1", "model": "m", "choices": []any{map[string]any{
			"index": float64(0), "delta": map[string]any{"content": "hey"}}}},
		{"id": "chatcmpl-1", "model": "m", "choices": []any{map[string]any{
			"index": float64(0), "delta": map[string]any{}, "finish_reason": "stop"}}},
	}
	for _, ch := range feed {
		for _, ev := range o.Transform(ch) {
			switch ev["type"] {
			case "message_start":
				sawStart = true
			case "content_block_delta":
				if d, ok := ev["delta"].(map[string]any); ok && d["text"] == "hey" {
					sawText = true
				}
			case "message_delta":
				sawStop = true
			}
		}
	}
	if !sawStart || !sawText || !sawStop {
		t.Fatalf("stream shape lost: start=%v text=%v stop=%v", sawStart, sawText, sawStop)
	}
}

func TestToGemini(t *testing.T) {
	raw := map[string]any{
		"model": "gemini-x",
		"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": "hi"},
		},
		"temperature": 0.2,
	}
	out, err := ToGemini(raw, "gemini-x", false)
	if err != nil {
		t.Fatal(err)
	}
	if out["systemInstruction"] == nil {
		t.Fatal("system instruction missing")
	}
	contents := out["contents"].([]GeminiContent)
	if len(contents) != 1 || contents[0].Parts[0].Text != "hi" {
		t.Fatalf("bad contents: %+v", contents)
	}
}

// Gemini marks thinking parts with thought:true and tool calls with
// functionCall; both must surface as OpenAI delta fields.
func TestGeminiStreamToOpenAI(t *testing.T) {
	g := &GeminiToOpenAI{}
	mk := func(parts []any, finish string) map[string]any {
		cand := map[string]any{"content": map[string]any{"parts": parts}}
		if finish != "" {
			cand["finishReason"] = finish
		}
		return map[string]any{"candidates": []any{cand},
			"usageMetadata": map[string]any{"promptTokenCount": float64(5),
				"candidatesTokenCount": float64(2), "thoughtsTokenCount": float64(3),
				"totalTokenCount": float64(10)}}
	}
	var chunks []map[string]any
	chunks = append(chunks, g.Transform(mk([]any{map[string]any{"text": "pl", "thought": true}}, ""))...)
	chunks = append(chunks, g.Transform(mk([]any{map[string]any{"text": "hi"}}, ""))...)
	chunks = append(chunks, g.Transform(mk([]any{map[string]any{"functionCall": map[string]any{
		"name": "ls", "args": map[string]any{"p": "/"}}}}, "STOP"))...)

	var text, reasoning string
	var toolName, toolArgs string
	var finish any
	var usage map[string]any
	for _, ch := range chunks {
		choice := ch["choices"].([]any)[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if s, ok := delta["content"].(string); ok {
			text += s
		}
		if s, ok := delta["reasoning_content"].(string); ok {
			reasoning += s
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			fn := tcs[0].(map[string]any)["function"].(map[string]any)
			toolName, _ = fn["name"].(string)
			toolArgs, _ = fn["arguments"].(string)
		}
		if choice["finish_reason"] != nil {
			finish = choice["finish_reason"]
			if u, ok := ch["usage"].(map[string]any); ok {
				usage = u
			}
		}
	}
	if text != "hi" || reasoning != "pl" {
		t.Fatalf("text=%q reasoning=%q", text, reasoning)
	}
	if toolName != "ls" || toolArgs != `{"p":"/"}` {
		t.Fatalf("tool call lost: %s %s", toolName, toolArgs)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish_reason: %v", finish)
	}
	if usage["prompt_tokens"] != int64(5) || usage["completion_tokens"] != int64(5) {
		t.Fatalf("usage: %v", usage)
	}
}

// Non-stream Gemini responses fold into one chat.completion body.
func TestGeminiNonStreamToOpenAI(t *testing.T) {
	out := GeminiResponseToOpenAI(map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": "yo"}}},
			"finishReason": "MAX_TOKENS",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": float64(1),
			"candidatesTokenCount": float64(2), "totalTokenCount": float64(3)},
	})
	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "length" {
		t.Fatalf("finish not mapped: %v", choice["finish_reason"])
	}
	if choice["message"].(map[string]any)["content"] != "yo" {
		t.Fatalf("text lost: %v", choice["message"])
	}
}

// Gemini-native clients (Gemini CLI) send contents/systemInstruction plus
// functionDeclarations; all of it must reach the Chat Completions pivot.
func TestGeminiRequestToChatNative(t *testing.T) {
	raw := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": "be terse"}}},
		"contents": []any{
			map[string]any{"role": "user", "parts": []any{map[string]any{"text": "hi"}}},
			map[string]any{"role": "model", "parts": []any{map[string]any{"text": "yo"}}},
		},
		"generationConfig": map[string]any{"maxOutputTokens": float64(64), "temperature": float64(0.2)},
		"tools": []any{map[string]any{"functionDeclarations": []any{map[string]any{
			"name": "ls", "description": "list",
			"parameters": map[string]any{"type": "object"},
		}}}},
	}
	out := GeminiRequestToChat(raw, "gemini-2.5-pro", true)
	msgs := out["messages"].([]any)
	if len(msgs) != 3 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages: %v", msgs)
	}
	if msgs[2].(map[string]any)["role"] != "assistant" {
		t.Fatalf("model role not mapped to assistant: %v", msgs[2])
	}
	if out["max_tokens"] != float64(64) || out["temperature"] != float64(0.2) {
		t.Fatalf("generationConfig not mapped: %v", out)
	}
	tools := out["tools"].([]any)
	if tools[0].(map[string]any)["function"].(map[string]any)["name"] != "ls" {
		t.Fatalf("tools: %v", tools)
	}
}

// Gemini SSE out: reasoning parts carry thought:true, tool calls become
// functionCall parts, and no [DONE] sentinel is expected.
func TestOpenAIToGeminiStream(t *testing.T) {
	c := &OpenAIToGemini{}
	var parts []any
	var finish any
	for _, chunk := range []map[string]any{
		{"model": "gemini-2.5-pro", "choices": []any{map[string]any{
			"delta": map[string]any{"content": "hi"}}}},
		{"choices": []any{map[string]any{"delta": map[string]any{},
			"finish_reason": "tool_calls"}},
			"usage": map[string]any{"prompt_tokens": float64(1),
				"completion_tokens": float64(2), "total_tokens": float64(3)}},
	} {
		for _, ev := range c.Transform(chunk) {
			cand := ev["candidates"].([]any)[0].(map[string]any)
			parts = append(parts, cand["content"].(map[string]any)["parts"].([]any)[0])
			if cand["finishReason"] != nil {
				finish = cand["finishReason"]
				if _, ok := ev["usageMetadata"]; !ok {
					t.Fatalf("usage missing on final chunk: %v", ev)
				}
			}
		}
	}
	if finish != "STOP" {
		t.Fatalf("finishReason: %v", finish)
	}
	if parts[0].(map[string]any)["text"] != "hi" {
		t.Fatalf("text lost: %v", parts[0])
	}
}

// Non-stream rewrite must produce a Gemini GenerateContentResponse.
func TestOpenAIToGeminiResponse(t *testing.T) {
	out := OpenAIToGeminiResponse(map[string]any{
		"id": "chatcmpl-1", "model": "gemini-2.5-pro",
		"choices": []any{map[string]any{"finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": "yo",
				"tool_calls": []any{map[string]any{"id": "t1", "type": "function",
					"function": map[string]any{"name": "ls", "arguments": `{"p":"/"}`}}}}}},
		"usage": map[string]any{"prompt_tokens": float64(1), "completion_tokens": float64(2),
			"total_tokens": float64(3)},
	})
	cand := out["candidates"].([]any)[0].(map[string]any)
	parts := cand["content"].(map[string]any)["parts"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["functionCall"].(map[string]any)["name"] != "ls" {
		t.Fatalf("parts: %v", parts)
	}
	if cand["finishReason"] != "STOP" {
		t.Fatalf("finish: %v", cand["finishReason"])
	}
}
