package translate

import (
	"bytes"
	"testing"
)

// A Responses client with tools + a prior tool turn must land as Chat
// Completions messages with tool_calls and tool results in order.
func TestResponsesRequestToChat(t *testing.T) {
	raw := map[string]any{
		"model":        "gpt-5",
		"instructions": "be terse",
		"input": []any{
			map[string]any{"type": "message", "role": "user",
				"content": []any{map[string]any{"type": "input_text", "text": "list files"}}},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "ls", "arguments": `{"path":"/"}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "a.txt"},
		},
		"tools": []any{map[string]any{
			"type": "function", "name": "ls", "description": "list",
			"parameters": map[string]any{"type": "object"},
		}},
		"max_output_tokens": float64(100),
	}
	out := ResponsesRequestToChat(raw)

	msgs, _ := out["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("want system,user,assistant,tool; got %d: %v", len(msgs), msgs)
	}
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("instructions not hoisted: %v", msgs[0])
	}
	assistant := msgs[2].(map[string]any)
	tcs, _ := assistant["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("tool_call not built: %v", assistant)
	}
	fn := tcs[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "ls" || fn["arguments"] != `{"path":"/"}` {
		t.Fatalf("bad tool call: %v", fn)
	}
	tool := msgs[3].(map[string]any)
	if tool["tool_call_id"] != "call_1" || tool["content"] != "a.txt" {
		t.Fatalf("bad tool result: %v", tool)
	}
	tools, _ := out["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools not converted: %v", out["tools"])
	}
	if _, ok := tools[0].(map[string]any)["function"]; !ok {
		t.Fatalf("tool not nested under function: %v", tools[0])
	}
	if out["max_tokens"] != float64(100) {
		t.Fatalf("max_output_tokens not remapped: %v", out["max_tokens"])
	}
}

// Chat Completions → Responses must produce top-level function tools and
// function_call input items.
func TestChatRequestToResponses(t *testing.T) {
	raw := map[string]any{
		"model": "gpt-5",
		"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": "hi"},
		},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "ls", "parameters": map[string]any{"type": "object"},
		}}},
		"max_tokens": float64(50),
	}
	out := ChatRequestToResponses(raw, "gpt-5", true)
	if out["instructions"] != "sys" {
		t.Fatalf("instructions missing: %v", out["instructions"])
	}
	if out["stream"] != true || out["store"] != false {
		t.Fatalf("stream/store wrong: %v %v", out["stream"], out["store"])
	}
	tools, _ := out["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "ls" {
		t.Fatalf("tools not flattened: %v", out["tools"])
	}
	if out["max_output_tokens"] != float64(50) {
		t.Fatalf("max_tokens not remapped: %v", out["max_output_tokens"])
	}
}

// Streaming Chat Completions → Responses must emit created, text deltas, item
// done and completed, in order.
func TestChatStreamToResponsesEvents(t *testing.T) {
	o := &OpenAIToResponses{}
	var events []ResponsesEvent
	chunks := []map[string]any{
		{"id": "c1", "choices": []any{map[string]any{"index": 0,
			"delta": map[string]any{"role": "assistant", "content": "he"}}}},
		{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "llo"}}}},
		{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": float64(3), "completion_tokens": float64(2)}},
	}
	for _, c := range chunks {
		events = append(events, o.Transform(c)...)
	}
	events = append(events, o.Flush()...)

	var types []string
	var text string
	for _, e := range events {
		types = append(types, e.Event)
		if e.Event == "response.output_text.delta" {
			text += e.Data["delta"].(string)
		}
	}
	if types[0] != "response.created" || types[len(types)-1] != "response.completed" {
		t.Fatalf("event order wrong: %v", types)
	}
	if text != "hello" {
		t.Fatalf("text deltas lost: %q", text)
	}
	done := 0
	for _, ty := range types {
		if ty == "response.output_item.done" {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("want exactly one item.done, got %d (%v)", done, types)
	}
	var completed map[string]any
	for _, e := range events {
		if e.Event == "response.completed" {
			completed = e.Data["response"].(map[string]any)
		}
	}
	u, _ := completed["usage"].(map[string]any)
	if u["input_tokens"] != int64(3) || u["output_tokens"] != int64(2) {
		t.Fatalf("usage not carried: %v", u)
	}
}

// Non-stream Chat Completions → Responses must surface tool calls as
// function_call items.
func TestNonStreamChatToResponses(t *testing.T) {
	body := map[string]any{
		"id": "chatcmpl-abc", "created": float64(1), "model": "gpt-5",
		"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls",
			"message": map[string]any{"role": "assistant", "content": "",
				"tool_calls": []any{map[string]any{"id": "t1", "type": "function",
					"function": map[string]any{"name": "ls", "arguments": `{"p":"/"}`}}}}}},
		"usage": map[string]any{"prompt_tokens": float64(1), "completion_tokens": float64(2)},
	}
	out := OpenAINonStreamToResponses(body)
	if out["id"] != "resp_abc" {
		t.Fatalf("id not rewritten: %v", out["id"])
	}
	items, _ := out["output"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["type"] != "function_call" {
		t.Fatalf("tool call missing from output: %v", out["output"])
	}
}

// Responses SSE → Chat Completions chunks, then folded back for a non-stream
// client. Covers text, reasoning and a tool call.
func TestCollectResponsesStream(t *testing.T) {
	sse := "" +
		"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"think\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"ls\"}}\n\n" +
		"event: response.function_call_arguments.delta\ndata: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"fc_1\",\"delta\":\"{\\\"p\\\":1}\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":7}}}\n\n" +
		"data: [DONE]\n\n"
	out := CollectResponsesStream(bytes.NewReader([]byte(sse)))
	if out["object"] != "chat.completion" {
		t.Fatalf("not folded to chat.completion: %v", out)
	}
	choices, _ := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hi" || msg["reasoning_content"] != "think" {
		t.Fatalf("text/reasoning lost: %v", msg)
	}
	if choices[0].(map[string]any)["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason wrong: %v", choices[0])
	}
	tcs, _ := msg["tool_calls"].([]any)
	fn := tcs[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "ls" || fn["arguments"] != `{"p":1}` {
		t.Fatalf("tool args lost: %v", fn)
	}
	u, _ := out["usage"].(map[string]any)
	if u["prompt_tokens"] != int64(5) {
		t.Fatalf("usage lost: %v", u)
	}
}
