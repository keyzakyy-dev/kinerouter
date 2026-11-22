package translate

// Response-side translation for the OpenAI Responses API, ported from
// open-sse/translator/response/openai-responses.js,
// open-sse/transformer/responsesTransformer.js and
// open-sse/handlers/chatCore/nonStreamingHandler.js.
//
// Chat Completions → Responses is a state machine over streamed chunks; the
// reverse direction maps each Responses event onto a Chat Completions chunk.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	thinkOpen  = "<" + "think>"
	thinkClose = "<" + "/think>"
)

// ResponsesEvent is one `event: X\ndata: {...}` frame.
type ResponsesEvent struct {
	Event string
	Data  map[string]any
}

// OpenAIToResponses converts Chat Completions chunks to Responses SSE events.
type OpenAIToResponses struct {
	seq      int
	respID   string
	created  int64
	started  bool
	inThink  bool
	closed   bool
	usageSet bool

	msgText      map[int]string
	msgItemAdded map[int]bool
	msgPartAdded map[int]bool
	msgItemDone  map[int]bool

	reasoningID        string
	reasoningIndex     int
	reasoningBuf       string
	reasoningPartAdded bool
	reasoningDone      bool

	funcArgs     map[int]string
	funcNames    map[int]string
	funcCallIDs  map[int]string
	funcItemDone map[int]bool

	completedSent bool
	usage         map[string]any
}

func (o *OpenAIToResponses) init() {
	o.respID = "resp_" + itoa(time.Now().UnixNano())
	o.created = time.Now().Unix()
	o.msgText = map[int]string{}
	o.msgItemAdded = map[int]bool{}
	o.msgPartAdded = map[int]bool{}
	o.msgItemDone = map[int]bool{}
	o.funcArgs = map[int]string{}
	o.funcNames = map[int]string{}
	o.funcCallIDs = map[int]string{}
	o.funcItemDone = map[int]bool{}
	o.reasoningIndex = -1
}

func (o *OpenAIToResponses) emit(out []ResponsesEvent, typ string, data map[string]any) []ResponsesEvent {
	o.seq++
	data["sequence_number"] = o.seq
	data["type"] = typ
	return append(out, ResponsesEvent{Event: typ, Data: data})
}

func (o *OpenAIToResponses) startReasoning(out []ResponsesEvent, idx int) []ResponsesEvent {
	if o.reasoningID != "" {
		return out
	}
	o.reasoningID = fmt.Sprintf("rs_%s_%d", o.respID, idx)
	o.reasoningIndex = idx
	out = o.emit(out, "response.output_item.added", map[string]any{
		"output_index": idx,
		"item":         map[string]any{"id": o.reasoningID, "type": "reasoning", "summary": []any{}},
	})
	out = o.emit(out, "response.reasoning_summary_part.added", map[string]any{
		"item_id": o.reasoningID, "output_index": idx, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	})
	o.reasoningPartAdded = true
	return out
}

func (o *OpenAIToResponses) emitReasoningDelta(out []ResponsesEvent, text string) []ResponsesEvent {
	if text == "" {
		return out
	}
	o.reasoningBuf += text
	return o.emit(out, "response.reasoning_summary_text.delta", map[string]any{
		"item_id": o.reasoningID, "output_index": o.reasoningIndex,
		"summary_index": 0, "delta": text,
	})
}

func (o *OpenAIToResponses) closeReasoning(out []ResponsesEvent) []ResponsesEvent {
	if o.reasoningID == "" || o.reasoningDone {
		return out
	}
	o.reasoningDone = true
	out = o.emit(out, "response.reasoning_summary_text.done", map[string]any{
		"item_id": o.reasoningID, "output_index": o.reasoningIndex,
		"summary_index": 0, "text": o.reasoningBuf,
	})
	out = o.emit(out, "response.reasoning_summary_part.done", map[string]any{
		"item_id": o.reasoningID, "output_index": o.reasoningIndex,
		"summary_index": 0,
		"part":          map[string]any{"type": "summary_text", "text": o.reasoningBuf},
	})
	out = o.emit(out, "response.output_item.done", map[string]any{
		"output_index": o.reasoningIndex,
		"item": map[string]any{
			"id": o.reasoningID, "type": "reasoning",
			"summary": []any{map[string]any{"type": "summary_text", "text": o.reasoningBuf}},
		},
	})
	return out
}

func (o *OpenAIToResponses) msgID(idx int) string { return fmt.Sprintf("msg_%s_%d", o.respID, idx) }

func (o *OpenAIToResponses) closeMessage(out []ResponsesEvent, idx int) []ResponsesEvent {
	if !o.msgItemAdded[idx] || o.msgItemDone[idx] {
		return out
	}
	o.msgItemDone[idx] = true
	text := o.msgText[idx]
	id := o.msgID(idx)
	out = o.emit(out, "response.output_text.done", map[string]any{
		"item_id": id, "output_index": idx, "content_index": 0,
		"text": text, "logprobs": []any{},
	})
	out = o.emit(out, "response.content_part.done", map[string]any{
		"item_id": id, "output_index": idx, "content_index": 0,
		"part": map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": text},
	})
	out = o.emit(out, "response.output_item.done", map[string]any{
		"output_index": idx,
		"item": map[string]any{
			"id": id, "type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": text}},
		},
	})
	return out
}

func (o *OpenAIToResponses) closeToolCall(out []ResponsesEvent, idx int) []ResponsesEvent {
	callID := o.funcCallIDs[idx]
	if callID == "" || o.funcItemDone[idx] {
		return out
	}
	args := o.funcArgs[idx]
	if args == "" {
		args = "{}"
	}
	out = o.emit(out, "response.function_call_arguments.done", map[string]any{
		"item_id": "fc_" + callID, "output_index": idx, "arguments": args,
	})
	out = o.emit(out, "response.output_item.done", map[string]any{
		"output_index": idx,
		"item": map[string]any{
			"id": "fc_" + callID, "type": "function_call",
			"arguments": args, "call_id": callID, "name": o.funcNames[idx],
		},
	})
	o.funcItemDone[idx] = true
	return out
}

func (o *OpenAIToResponses) sendCompleted(out []ResponsesEvent) []ResponsesEvent {
	if o.completedSent {
		return out
	}
	o.completedSent = true
	resp := map[string]any{
		"id": o.respID, "object": "response", "created_at": o.created,
		"status": "completed", "background": false, "error": nil,
	}
	if o.usage != nil {
		resp["usage"] = o.usage
	}
	return o.emit(out, "response.completed", map[string]any{"response": resp})
}

// toResponsesUsage maps a Chat Completions usage object to the Responses shape.
// Placeholder zeroed usage is ignored so response.completed cannot freeze it.
func toResponsesUsage(u map[string]any) map[string]any {
	if u == nil {
		return nil
	}
	in, ok1 := intVal(u["input_tokens"], u["prompt_tokens"])
	ou, ok2 := intVal(u["output_tokens"], u["completion_tokens"])
	if !ok1 || !ok2 || in+ou <= 0 {
		return nil
	}
	r := map[string]any{"input_tokens": in, "output_tokens": ou, "total_tokens": in + ou}
	cached, okc := intVal(nested(u, "input_tokens_details", "cached_tokens"), nested(u, "prompt_tokens_details", "cached_tokens"))
	reasoning, okr := intVal(nested(u, "output_tokens_details", "reasoning_tokens"), nested(u, "completion_tokens_details", "reasoning_tokens"))
	if okc {
		r["input_tokens_details"] = map[string]any{"cached_tokens": cached}
	}
	if okr {
		r["output_tokens_details"] = map[string]any{"reasoning_tokens": reasoning}
	}
	return r
}

// Transform maps one Chat Completions chunk to zero or more Responses events.
func (o *OpenAIToResponses) Transform(chunk map[string]any) []ResponsesEvent {
	if o.msgText == nil {
		o.init()
	}
	// Usage may arrive in a trailer whose choices array is empty.
	if u, ok := chunk["usage"].(map[string]any); ok {
		if conv := toResponsesUsage(u); conv != nil {
			o.usage = conv
			o.usageSet = true
		}
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return nil
	}
	idx := int(numAny(choice["index"]))
	delta, _ := choice["delta"].(map[string]any)

	var out []ResponsesEvent
	if !o.started {
		o.started = true
		if id, ok := chunk["id"].(string); ok && id != "" {
			o.respID = "resp_" + id
		}
		out = o.emit(out, "response.created", map[string]any{
			"response": map[string]any{
				"id": o.respID, "object": "response", "created_at": o.created,
				"status": "in_progress", "background": false, "error": nil, "output": []any{},
			},
		})
		out = o.emit(out, "response.in_progress", map[string]any{
			"response": map[string]any{
				"id": o.respID, "object": "response", "created_at": o.created, "status": "in_progress",
			},
		})
	}
	if delta == nil {
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			out = o.finishAll(out)
		}
		return out
	}

	if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
		out = o.startReasoning(out, idx)
		out = o.emitReasoningDelta(out, rc)
	}

	if content, ok := delta["content"].(string); ok && content != "" {
		if i := strings.Index(content, thinkOpen); i >= 0 {
			o.inThink = true
			content = content[:i] + content[i+len(thinkOpen):]
			out = o.startReasoning(out, idx)
			if content != "" {
				out = o.emitReasoningDelta(out, content)
			}
			return out
		}
		if i := strings.Index(content, thinkClose); i >= 0 {
			rest := content[i+len(thinkClose):]
			if head := content[:i]; head != "" {
				out = o.emitReasoningDelta(out, head)
			}
			out = o.closeReasoning(out)
			o.inThink = false
			content = rest
		}
		if o.inThink && content != "" {
			return o.emitReasoningDelta(out, content)
		}
		if content != "" {
			out = o.closeReasoning(out)
			if !o.msgItemAdded[idx] {
				o.msgItemAdded[idx] = true
				out = o.emit(out, "response.output_item.added", map[string]any{
					"output_index": idx,
					"item":         map[string]any{"id": o.msgID(idx), "type": "message", "content": []any{}, "role": "assistant"},
				})
			}
			if !o.msgPartAdded[idx] {
				o.msgPartAdded[idx] = true
				out = o.emit(out, "response.content_part.added", map[string]any{
					"item_id": o.msgID(idx), "output_index": idx, "content_index": 0,
					"part": map[string]any{"type": "output_text", "annotations": []any{}, "logprobs": []any{}, "text": ""},
				})
			}
			out = o.emit(out, "response.output_text.delta", map[string]any{
				"item_id": o.msgID(idx), "output_index": idx, "content_index": 0,
				"delta": content, "logprobs": []any{},
			})
			o.msgText[idx] += content
		}
	}

	if tcs, ok := delta["tool_calls"].([]any); ok && len(tcs) > 0 {
		out = o.closeReasoning(out)
		out = o.closeMessage(out, idx)
		for _, raw := range tcs {
			tc, _ := raw.(map[string]any)
			if tc == nil {
				continue
			}
			tcIdx := int(numAny(tc["index"]))
			fn, _ := tc["function"].(map[string]any)
			name, _ := fn["name"].(string)
			if name != "" {
				o.funcNames[tcIdx] = name
			}
			callID, _ := tc["id"].(string)
			if o.funcCallIDs[tcIdx] == "" && callID != "" {
				o.funcCallIDs[tcIdx] = callID
				out = o.emit(out, "response.output_item.added", map[string]any{
					"output_index": tcIdx,
					"item": map[string]any{
						"id": "fc_" + callID, "type": "function_call",
						"arguments": "", "call_id": callID, "name": o.funcNames[tcIdx],
					},
				})
			}
			args, _ := fn["arguments"].(string)
			if args != "" {
				ref := o.funcCallIDs[tcIdx]
				if ref == "" {
					ref = callID
				}
				if ref != "" {
					out = o.emit(out, "response.function_call_arguments.delta", map[string]any{
						"item_id": "fc_" + ref, "output_index": tcIdx, "delta": args,
					})
				}
				o.funcArgs[tcIdx] += args
			}
		}
	}

	if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
		out = o.finishAll(out)
	}
	return out
}

func (o *OpenAIToResponses) finishAll(out []ResponsesEvent) []ResponsesEvent {
	for i := range o.msgItemAdded {
		out = o.closeMessage(out, i)
	}
	out = o.closeReasoning(out)
	for i := range o.funcCallIDs {
		out = o.closeToolCall(out, i)
	}
	out = o.sendCompleted(out)
	o.closed = true
	return out
}

// Flush emits any remaining closing events (upstream ended without finish_reason).
func (o *OpenAIToResponses) Flush() []ResponsesEvent {
	if o.msgText == nil || o.closed {
		return nil
	}
	return o.finishAll(nil)
}

// OpenAINonStreamToResponses converts a Chat Completions JSON body to a
// Responses API JSON body (non-streaming Responses clients).
func OpenAINonStreamToResponses(body map[string]any) map[string]any {
	choices, _ := body["choices"].([]any)
	if len(choices) == 0 {
		return body
	}
	choice, _ := choices[0].(map[string]any)
	msg, _ := choice["message"].(map[string]any)
	var output []any
	if msg != nil {
		if rc, ok := msg["reasoning_content"].(string); ok && rc != "" {
			output = append(output, map[string]any{
				"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": rc}},
			})
		} else if rc, ok := msg["reasoning"].(string); ok && rc != "" {
			output = append(output, map[string]any{
				"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": rc}},
			})
		}
		if txt, ok := msg["content"].(string); ok && txt != "" {
			output = append(output, map[string]any{
				"type": "message", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": txt, "annotations": []any{}}},
			})
		}
		if tcs, ok := msg["tool_calls"].([]any); ok {
			for _, raw := range tcs {
				tc, _ := raw.(map[string]any)
				if tc == nil {
					continue
				}
				fn, _ := tc["function"].(map[string]any)
				name, _ := fn["name"].(string)
				callID, _ := tc["id"].(string)
				output = append(output, map[string]any{
					"type": "function_call", "id": "fc_" + callID, "call_id": callID,
					"name": name, "arguments": coerceResponsesArguments(fn["arguments"]),
				})
			}
		}
	}
	status := "completed"
	id, _ := body["id"].(string)
	if strings.HasPrefix(id, "chatcmpl-") {
		id = "resp_" + strings.TrimPrefix(id, "chatcmpl-")
	} else if id != "" {
		id = "resp_" + id
	}
	usage, _ := body["usage"].(map[string]any)
	var outUsage map[string]any
	if usage != nil {
		in, _ := intVal(usage["prompt_tokens"], usage["input_tokens"])
		ou, _ := intVal(usage["completion_tokens"], usage["output_tokens"])
		outUsage = map[string]any{
			"input_tokens": in, "output_tokens": ou,
			"total_tokens": in + ou,
		}
	}
	if output == nil {
		output = []any{}
	}
	if outUsage == nil {
		outUsage = map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	}
	return map[string]any{
		"id": id, "object": "response", "created_at": body["created"],
		"model": stringOr(body["model"], ""), "status": status,
		"background": false, "error": nil, "output": output, "usage": outUsage,
	}
}

// ---------------------------------------------------------------- responses → openai

// ResponsesToOpenAIStream maps Responses events back onto Chat Completions
// chunks (a Chat Completions client routed to a Responses-only upstream).
type ResponsesToOpenAIStream struct {
	chatID    string
	created   int64
	toolIndex int
	toolIDs   map[string]int
	argsSent  map[int]bool
	finished  bool
	usage     map[string]any
	model     string
}

func (r *ResponsesToOpenAIStream) init() {
	if r.toolIDs != nil {
		return
	}
	r.chatID = "chatcmpl-" + itoa(time.Now().UnixNano())
	r.created = time.Now().Unix()
	r.toolIDs = map[string]int{}
	r.argsSent = map[int]bool{}
}

func (r *ResponsesToOpenAIStream) chunk(delta map[string]any, finish string) map[string]any {
	choice := map[string]any{"index": 0, "delta": delta}
	if finish != "" {
		choice["finish_reason"] = finish
	} else {
		choice["finish_reason"] = nil
	}
	return map[string]any{
		"id": r.chatID, "object": "chat.completion.chunk", "created": r.created,
		"model": r.model, "choices": []any{choice},
	}
}

func (r *ResponsesToOpenAIStream) finishReason() string {
	if r.toolIndex > 0 {
		return "tool_calls"
	}
	return "stop"
}

// Transform maps one Responses event to zero or more Chat Completions chunks.
func (r *ResponsesToOpenAIStream) Transform(event string, data map[string]any) []map[string]any {
	r.init()
	if event == "" {
		event, _ = data["type"].(string)
	}
	if m, ok := data["model"].(string); ok && m != "" && r.model == "" {
		r.model = m
	}
	switch event {
	case "response.output_text.delta":
		d, _ := data["delta"].(string)
		if d == "" {
			return nil
		}
		return []map[string]any{r.chunk(map[string]any{"content": d}, "")}
	case "response.reasoning_summary_text.delta":
		d, _ := data["delta"].(string)
		if d == "" {
			return nil
		}
		return []map[string]any{r.chunk(map[string]any{"reasoning_content": d}, "")}
	case "response.output_item.added":
		item, _ := data["item"].(map[string]any)
		if item == nil {
			return nil
		}
		if t, _ := item["type"].(string); t != "function_call" && t != "custom_tool_call" {
			return nil
		}
		callID, _ := item["call_id"].(string)
		if callID == "" {
			callID = "call_" + itoa(time.Now().UnixNano())
		}
		key := stringOr(item["id"], callID)
		idx, seen := r.toolIDs[key]
		if !seen {
			idx = r.toolIndex
			r.toolIndex++
			r.toolIDs[key] = idx
		}
		return []map[string]any{r.chunk(map[string]any{
			"tool_calls": []any{map[string]any{
				"index": idx, "id": callID, "type": "function",
				"function": map[string]any{"name": stringOr(item["name"], ""), "arguments": ""},
			}},
		}, "")}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		d, _ := data["delta"].(string)
		if d == "" {
			return nil
		}
		idx := r.toolIndex - 1
		if idx < 0 {
			idx = 0
		}
		if id, ok := data["item_id"].(string); ok {
			if v, ok := r.toolIDs[id]; ok {
				idx = v
			}
		}
		r.argsSent[idx] = true
		return []map[string]any{r.chunk(map[string]any{
			"tool_calls": []any{map[string]any{"index": idx, "function": map[string]any{"arguments": d}}},
		}, "")}
	case "response.output_item.done":
		item, _ := data["item"].(map[string]any)
		if item == nil {
			return nil
		}
		if t, _ := item["type"].(string); t != "function_call" && t != "custom_tool_call" {
			return nil
		}
		idx := r.toolIndex - 1
		if idx < 0 {
			idx = 0
		}
		if id, ok := item["id"].(string); ok {
			if v, ok := r.toolIDs[id]; ok {
				idx = v
			}
		}
		args, _ := item["arguments"].(string)
		if args == "" || r.argsSent[idx] {
			return nil
		}
		r.argsSent[idx] = true
		return []map[string]any{r.chunk(map[string]any{
			"tool_calls": []any{map[string]any{"index": idx, "function": map[string]any{"arguments": args}}},
		}, "")}
	case "response.completed", "response.done":
		resp, _ := data["response"].(map[string]any)
		if resp != nil {
			if u, ok := resp["usage"].(map[string]any); ok {
				in, _ := intVal(u["input_tokens"], u["prompt_tokens"])
				ou, _ := intVal(u["output_tokens"], u["completion_tokens"])
				r.usage = map[string]any{
					"prompt_tokens": in, "completion_tokens": ou, "total_tokens": in + ou,
				}
				if d, ok := u["input_tokens_details"].(map[string]any); ok {
					if c, ok := intVal(d["cached_tokens"]); ok {
						r.usage["prompt_tokens_details"] = map[string]any{"cached_tokens": c}
					}
				}
			}
		}
		return []map[string]any{r.finalChunk()}
	case "error", "response.failed":
		if r.finished {
			return nil
		}
		errObj, _ := data["error"].(map[string]any)
		msg := "upstream error"
		if errObj != nil {
			msg = stringOr(errObj["message"], msg)
		} else if resp, ok := data["response"].(map[string]any); ok {
			if e, ok := resp["error"].(map[string]any); ok {
				msg = stringOr(e["message"], msg)
			}
		}
		r.finished = true
		return []map[string]any{r.chunkFinal(map[string]any{"content": "[Error] " + msg})}
	}
	return nil
}

func (r *ResponsesToOpenAIStream) chunkFinal(delta map[string]any) map[string]any {
	ch := r.chunk(delta, r.finishReason())
	if r.usage != nil {
		ch["usage"] = r.usage
	}
	return ch
}

func (r *ResponsesToOpenAIStream) finalChunk() map[string]any {
	if r.finished {
		return nil
	}
	r.finished = true
	return r.chunkFinal(map[string]any{})
}

// Flush emits the final finish-reason chunk when the upstream never sent one.
func (r *ResponsesToOpenAIStream) Flush() []map[string]any {
	if r.toolIDs == nil || r.finished {
		return nil
	}
	return []map[string]any{r.finalChunk()}
}

// ResponsesNonStreamToOpenAI folds a collected Responses body into a Chat
// Completions body (non-streaming client, Responses upstream).
func ResponsesNonStreamToOpenAI(body map[string]any) map[string]any {
	var text, reasoning, toolCalls []any
	var sb strings.Builder
	if output, ok := body["output"].([]any); ok {
		for _, raw := range output {
			item, _ := raw.(map[string]any)
			if item == nil {
				continue
			}
			switch item["type"] {
			case "message":
				if parts, ok := item["content"].([]any); ok {
					for _, p := range parts {
						if pm, ok := p.(map[string]any); ok {
							if t, ok := pm["text"].(string); ok {
								sb.WriteString(t)
							}
						}
					}
				}
			case "reasoning":
				if ss, ok := item["summary"].([]any); ok {
					for _, s := range ss {
						if sm, ok := s.(map[string]any); ok {
							if t, ok := sm["text"].(string); ok {
								reasoning = append(reasoning, t)
							}
						}
					}
				}
			case "function_call", "custom_tool_call":
				args := stringOr(item["arguments"], "")
				if item["type"] == "custom_tool_call" {
					args = mustJSONString(map[string]any{"input": stringOr(item["input"], "")})
				}
				toolCalls = append(toolCalls, map[string]any{
					"id": stringOr(item["call_id"], ""), "type": "function",
					"function": map[string]any{"name": stringOr(item["name"], ""), "arguments": args},
				})
			}
		}
	}
	text = nil
	_ = text
	msg := map[string]any{"role": "assistant", "content": sb.String()}
	if len(reasoning) > 0 {
		var parts []string
		for _, r := range reasoning {
			if s, ok := r.(string); ok {
				parts = append(parts, s)
			}
		}
		msg["reasoning_content"] = strings.Join(parts, "\n")
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	usage, _ := body["usage"].(map[string]any)
	outUsage := map[string]any{}
	if usage != nil {
		in, _ := intVal(usage["input_tokens"], usage["prompt_tokens"])
		ou, _ := intVal(usage["output_tokens"], usage["completion_tokens"])
		outUsage["prompt_tokens"] = in
		outUsage["completion_tokens"] = ou
		outUsage["total_tokens"] = in + ou
	}
	return map[string]any{
		"id": stringOr(body["id"], "resp_unknown"), "object": "chat.completion",
		"created": body["created_at"], "model": stringOr(body["model"], ""),
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage":   outUsage,
	}
}

// ---------------------------------------------------------------- small helpers

func intVal(vals ...any) (int64, bool) {
	for _, v := range vals {
		switch n := v.(type) {
		case float64:
			return int64(n), true
		case int:
			return int64(n), true
		case int64:
			return n, true
		case json.Number:
			if i, err := n.Int64(); err == nil {
				return i, true
			}
		}
	}
	return 0, false
}

func nested(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		cm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = cm[p]
	}
	return cur
}

func numAny(v any) int64 {
	n, _ := intVal(v)
	return n
}

// CollectResponsesStream folds a Responses SSE stream into one Chat Completions
// body. Used when a non-streaming client is routed to an upstream that only
// emits SSE (Codex / forceStream providers).
func CollectResponsesStream(r io.Reader) map[string]any {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	rs := &ResponsesToOpenAIStream{}
	var text, reasoning strings.Builder
	tools := map[int]map[string]any{}
	var order []int
	var usage map[string]any
	finish := "stop"
	var event string

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			event = ""
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			event = string(bytes.TrimSpace(line[len("event:"):]))
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var payload map[string]any
		if json.Unmarshal(data, &payload) != nil {
			continue
		}
		for _, ch := range rs.Transform(event, payload) {
			choices, _ := ch["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				finish = fr
			}
			if u, ok := ch["usage"].(map[string]any); ok {
				usage = u
			}
			if delta == nil {
				continue
			}
			if c, ok := delta["content"].(string); ok {
				text.WriteString(c)
			}
			if c, ok := delta["reasoning_content"].(string); ok {
				reasoning.WriteString(c)
			}
			if tcs, ok := delta["tool_calls"].([]any); ok {
				for _, raw := range tcs {
					tc, _ := raw.(map[string]any)
					if tc == nil {
						continue
					}
					idx := int(numAny(tc["index"]))
					cur, seen := tools[idx]
					if !seen {
						cur = map[string]any{"id": "", "type": "function", "function": map[string]any{"name": "", "arguments": ""}}
						tools[idx] = cur
						order = append(order, idx)
					}
					if id, ok := tc["id"].(string); ok && id != "" {
						cur["id"] = id
					}
					fn, _ := tc["function"].(map[string]any)
					curFn := cur["function"].(map[string]any)
					if name, ok := fn["name"].(string); ok && name != "" {
						curFn["name"] = name
					}
					if args, ok := fn["arguments"].(string); ok {
						curFn["arguments"] = curFn["arguments"].(string) + args
					}
				}
			}
		}
	}

	msg := map[string]any{"role": "assistant", "content": text.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(order) > 0 {
		var list []any
		for _, i := range order {
			list = append(list, tools[i])
		}
		msg["tool_calls"] = list
	}
	out := map[string]any{
		"id": rs.chatID, "object": "chat.completion", "created": rs.created,
		"model":   rs.model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
	}
	if usage != nil {
		out["usage"] = usage
	}
	return out
}
