package gateway

// Tool support on the wire. A request that carries `tools` plus tool rounds is
// rejected with 400 by providers whose model has no function calling, so the
// gateway flattens the tool history into prose before forwarding (upstream:
// stripToolsForNonToolModel in handlers/chat/combo_fusion.go, applied from
// fallback.go). The retrieved context survives; the model just cannot emit a
// tool call.
//
// The capability table here is deliberately small: an unknown provider/model is
// left alone, because stripping tools from a capable model silently disables
// the client's tools. Only the models 9router-go documents as declaring no tool
// support are listed.

import "strings"

// declaredToolModels lists, per provider, the models the catalogue declares
// tool-capable. A model missing from its provider's list is treated as
// non-tool-capable; a provider missing from the map is never touched.
var declaredToolModels = map[string]map[string]bool{
	// Cloudflare Workers AI rejects tool rounds for models it does not declare
	// tool-capable ("tool_calls are not supported for this model").
	"cloudflare-ai": {
		"mistral-small-3.1-24b-instruct":  true, // @cf/mistralai/…
		"kimi-k2.6":                       true, // @cf/moonshotai/…
		"llama-3.3-70b-instruct-fp8-fast": true, // @cf/meta/…
		"gemma-4-26b-a4b-it":              true, // @cf/google/…
	},
}

// modelWithoutToolSupport reports whether the provider declared the model as
// carrying no function calling.
func modelWithoutToolSupport(provider, model string) bool {
	declared, tracked := declaredToolModels[provider]
	if !tracked {
		return false
	}
	return !declared[baseModelName(model)]
}

// baseModelName is the vendor-relative id: Cloudflare spells its models
// "@cf/meta/llama-3.3-70b" while a combo may carry the bare last segment.
func baseModelName(model string) string {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		return model[i+1:]
	}
	return model
}

// stripToolsAndFlatten removes every tool definition and rewrites tool rounds as
// prose. It reports whether the body carried any tool content.
func stripToolsAndFlatten(body map[string]any) bool {
	if body == nil {
		return false
	}
	touched := false
	for _, k := range []string{"tools", "tool_choice", "functions"} {
		if _, ok := body[k]; ok {
			delete(body, k)
			touched = true
		}
	}
	if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
		flat, changed := flattenToolTurns(msgs)
		if changed {
			body["messages"] = flat
			touched = true
		}
	}
	if items, ok := body["input"].([]any); ok && len(items) > 0 {
		flat, changed := flattenToolTurns(items)
		if changed {
			body["input"] = flat
			touched = true
		}
	}
	return touched
}

// flattenToolTurns converts tool calls and tool results into assistant/user
// prose. A tool result is environment output, so it is labelled as a user turn
// rather than being attributed to the model.
func flattenToolTurns(items []any) ([]any, bool) {
	out := make([]any, 0, len(items))
	changed := false
	for _, it := range items {
		m := objMap(it)
		if m == nil {
			out = append(out, it)
			continue
		}
		switch str(m, "role") {
		case "tool", "function":
			out = append(out, map[string]any{
				"role":    "user",
				"content": toolResultPrefix + contentText(m["content"]) + "]",
			})
			changed = true
			continue
		case "assistant":
			if calls, ok := m["tool_calls"].([]any); ok && len(calls) > 0 {
				cp := cloneMap(m)
				delete(cp, "tool_calls")
				names := make([]string, 0, len(calls))
				for _, c := range calls {
					cm := objMap(c)
					names = append(names, firstNonEmpty(strAny(objMap(cm["function"])["name"]), strAny(cm["name"]), "tool"))
				}
				base := contentText(m["content"])
				if base != "" {
					base += "\n"
				}
				cp["content"] = base + toolCallPrefix + strings.Join(names, ", ") + "]"
				out = append(out, cp)
				changed = true
				continue
			}
		}
		if t, _ := m["type"].(string); t == "function_call_output" {
			out = append(out, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": toolResultPrefix + contentText(m["output"]) + "]",
			})
			changed = true
			continue
		}
		if blocks, ok := m["content"].([]any); ok {
			var texts, toolNames, toolResults []string
			hit := false
			for _, b := range blocks {
				bm := objMap(b)
				switch str(bm, "type") {
				case "text":
					if s, _ := bm["text"].(string); s != "" {
						texts = append(texts, s)
					}
				case "tool_use":
					toolNames = append(toolNames, firstNonEmpty(str(bm, "name"), "tool"))
					hit = true
				case "tool_result":
					toolResults = append(toolResults, contentText(bm["content"]))
					hit = true
				}
			}
			if hit {
				cp := cloneMap(m)
				content := strings.Join(texts, "\n")
				if len(toolNames) > 0 {
					if content != "" {
						content += "\n"
					}
					content += toolCallPrefix + strings.Join(toolNames, ", ") + "]"
				}
				if len(toolResults) > 0 {
					if content != "" {
						content += "\n"
					}
					content += toolResultPrefix + strings.Join(toolResults, "\n") + "]"
				}
				cp["content"] = content
				out = append(out, cp)
				changed = true
				continue
			}
		}
		out = append(out, it)
	}
	return out, changed
}
