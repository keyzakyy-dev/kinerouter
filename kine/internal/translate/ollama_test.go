package translate

import "testing"

// Ollama wants string content, tool arguments as objects, and raw-base64
// images in message.images[].
func TestToOllama(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "sys"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "look"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
			}},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
				map[string]any{"id": "c1", "function": map[string]any{"name": "run", "arguments": `{"a":1}`}},
			}},
			map[string]any{"role": "tool", "tool_call_id": "c1", "content": "ok"},
			map[string]any{"role": "user", "content": []any{}},
		},
		"temperature": 0.2,
		"max_tokens":  64,
	}
	out, err := ToOllama(raw, "llama3", true)
	if err != nil {
		t.Fatal(err)
	}
	msgs := out["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("empty message not dropped: %d", len(msgs))
	}
	user := msgs[1].(map[string]any)
	if user["content"] != "look" {
		t.Fatalf("content not flattened: %v", user["content"])
	}
	if imgs, _ := user["images"].([]string); len(imgs) != 1 || imgs[0] != "AAAA" {
		t.Fatalf("image not extracted: %v", user["images"])
	}
	asst := msgs[2].(map[string]any)
	args := asst["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["arguments"]
	if _, ok := args.(map[string]any); !ok {
		t.Fatalf("arguments should be an object, got %T", args)
	}
	toolMsg := msgs[3].(map[string]any)
	if toolMsg["tool_name"] != "run" {
		t.Fatalf("tool_name not mapped: %v", toolMsg["tool_name"])
	}
	if out["options"].(map[string]any)["num_predict"] != 64 {
		t.Fatalf("max_tokens not mapped to num_predict")
	}
}

func TestOllamaToOpenAI(t *testing.T) {
	o := &OllamaToOpenAI{}
	ch := o.Transform(map[string]any{"model": "llama3", "message": map[string]any{"role": "assistant", "content": "hi"}})
	if ch == nil || o.Model != "llama3" {
		t.Fatal("content chunk missing")
	}
	choice := ch["choices"].([]any)[0].(map[string]any)
	if choice["delta"].(map[string]any)["content"] != "hi" {
		t.Fatalf("delta wrong: %v", choice["delta"])
	}
	final := o.Transform(map[string]any{"done": true, "done_reason": "stop", "prompt_eval_count": 7, "eval_count": 3})
	usage := final["usage"].(map[string]any)
	if usage["prompt_tokens"].(int64) != 7 || usage["total_tokens"].(int64) != 10 {
		t.Fatalf("usage wrong: %v", usage)
	}
	if got := o.Usage.PromptTokens; got != 7 {
		t.Fatalf("usage not recorded: %d", got)
	}

	body := OllamaBodyToOpenAI(map[string]any{
		"model": "llama3", "done_reason": "length",
		"message": map[string]any{"content": "x", "tool_calls": []any{
			map[string]any{"function": map[string]any{"name": "run", "arguments": map[string]any{"a": 1}}},
		}},
	})
	sel := body["choices"].([]any)[0].(map[string]any)
	if sel["finish_reason"] != "tool_calls" {
		t.Fatalf("tool calls should win the finish reason: %v", sel["finish_reason"])
	}
	call := sel["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if call["function"].(map[string]any)["arguments"] != `{"a":1}` {
		t.Fatalf("arguments not stringified: %v", call)
	}
}
