package translate

// Kiro upstream answers with AWS EventStream binary frames (not SSE). Ported
// from open-sse/utils/awsEventStream.js + the Kiro branch of
// open-sse/translator/response/kiro-to-openai.js: each frame carries a
// `:event-type` header and a JSON payload, which becomes an OpenAI chunk.

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
)

const (
	eventStreamMaxMessage = 24 << 20
	eventStreamMaxHeaders = 128 << 10
)

var eventStreamCRC = crc32.MakeTable(crc32.IEEE)

// eventFrame is one decoded frame.
type eventFrame struct {
	Headers map[string]any
	Payload map[string]any
}

// parseEventFrame decodes exactly one frame. Integrity failures are errors: a
// silently truncated frame would become a silently truncated answer.
func parseEventFrame(data []byte) (eventFrame, error) {
	if len(data) < 16 {
		return eventFrame{}, fmt.Errorf("AWS EventStream frame is shorter than 16 bytes")
	}
	total := int(binary.BigEndian.Uint32(data[0:4]))
	headersLen := int(binary.BigEndian.Uint32(data[4:8]))
	if total != len(data) {
		return eventFrame{}, fmt.Errorf("AWS EventStream frame length does not match its prelude")
	}
	if total > eventStreamMaxMessage || headersLen > eventStreamMaxHeaders || headersLen > total-16 {
		return eventFrame{}, fmt.Errorf("AWS EventStream frame bounds are invalid")
	}
	if binary.BigEndian.Uint32(data[8:12]) != crc32.Checksum(data[0:8], eventStreamCRC) {
		return eventFrame{}, fmt.Errorf("AWS EventStream prelude CRC mismatch")
	}
	if binary.BigEndian.Uint32(data[total-4:]) != crc32.Checksum(data[:total-4], eventStreamCRC) {
		return eventFrame{}, fmt.Errorf("AWS EventStream message CRC mismatch")
	}

	headers := map[string]any{}
	off, end := 12, 12+headersLen
	for off < end {
		nameLen := int(data[off])
		off++
		if off+nameLen+1 > end {
			return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
		}
		name := string(data[off : off+nameLen])
		off += nameLen
		typ := data[off]
		off++
		switch typ {
		case 0, 1:
			headers[name] = typ == 0
		case 2:
			if off+1 > end {
				return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
			}
			headers[name] = int8(data[off])
			off++
		case 3:
			if off+2 > end {
				return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
			}
			headers[name] = int16(binary.BigEndian.Uint16(data[off:]))
			off += 2
		case 4:
			if off+4 > end {
				return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
			}
			headers[name] = int32(binary.BigEndian.Uint32(data[off:]))
			off += 4
		case 5, 8:
			off += 8
		case 6, 7:
			if off+2 > end {
				return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
			}
			vl := int(binary.BigEndian.Uint16(data[off:]))
			off += 2
			if off+vl > end {
				return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
			}
			if typ == 7 {
				headers[name] = string(data[off : off+vl])
			}
			off += vl
		case 9:
			off += 16
		default:
			return eventFrame{}, fmt.Errorf("AWS EventStream header %s has unknown type %d", name, typ)
		}
		if off > end {
			return eventFrame{}, fmt.Errorf("AWS EventStream header exceeds its declared bounds")
		}
	}

	payload := data[end : total-4]
	if len(strings.TrimSpace(string(payload))) == 0 {
		return eventFrame{Headers: headers}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return eventFrame{}, fmt.Errorf("AWS EventStream payload is not valid JSON (%v)", err)
	}
	return eventFrame{Headers: headers, Payload: m}, nil
}

// KiroToOpenAI converts Kiro EventStream frames into OpenAI chat chunks.
// Mirrors kiroToOpenAIResponse: one chunk per payload-bearing event, plus the
// final finish chunk once messageStopEvent arrives.
type KiroToOpenAI struct {
	id         string
	created    int64
	model      string
	toolIndex  int
	hadToolUse bool
	Usage      Usage
	restore    map[string]string // sanitized tool name → client name
}

// NewKiroToOpenAI builds a response translator seeded with the request's tool
// name restore map (Kiro sanitizes `mcp__a__b` to `mcp_a_b`).
func NewKiroToOpenAI(model string, restore map[string]string) *KiroToOpenAI {
	return &KiroToOpenAI{id: "chatcmpl-" + newKiroCallID(), created: nowUnix(), model: model, restore: restore}
}

// Transform returns zero or more OpenAI chunks for one frame.
func (k *KiroToOpenAI) Transform(frame eventFrame) []map[string]any {
	msgType, _ := frame.Headers[":message-type"].(string)
	if msgType == "error" || msgType == "exception" {
		msg, _ := frame.Payload["message"].(string)
		if msg == "" {
			msg = "Kiro upstream sent an EventStream " + msgType
		}
		return []map[string]any{{"error": map[string]any{"message": msg, "type": "api_error"}}}
	}
	eventType, _ := frame.Headers[":event-type"].(string)
	p := frame.Payload
	switch eventType {
	case "assistantResponseEvent":
		content, _ := p["content"].(string)
		if content == "" {
			return nil
		}
		return []map[string]any{k.chunk(map[string]any{"content": content}, nil)}
	case "reasoningContentEvent":
		text := contentText(p["reasoningContentEvent"])
		if text == "" {
			text, _ = p["content"].(string)
		}
		if text == "" {
			return nil
		}
		return []map[string]any{k.chunk(map[string]any{"reasoning_content": text}, nil)}
	case "codeEvent":
		content, _ := p["content"].(string)
		if content == "" {
			return nil
		}
		return []map[string]any{k.chunk(map[string]any{"content": content}, nil)}
	case "toolUseEvent":
		k.hadToolUse = true
		values, ok := p["toolUseEvent"].([]any)
		if !ok {
			values = []any{p}
			if inner, ok := p["toolUseEvent"]; ok {
				values = []any{inner}
			}
		}
		var out []map[string]any
		for _, v := range values {
			call, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name := k.restoreName(str(call, "name"))
			id := str(call, "toolUseId")
			if id == "" {
				id = "call_" + strconv.Itoa(k.toolIndex+1)
			}
			idx := k.toolIndex
			k.toolIndex++
			args := "{}"
			if b, err := json.Marshal(call["input"]); err == nil {
				args = string(b)
			}
			out = append(out, k.chunk(map[string]any{"tool_calls": []any{map[string]any{
				"index": idx, "id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": args},
			}}}, nil))
		}
		return out
	case "messageStopEvent":
		reason := "stop"
		if k.hadToolUse {
			reason = "tool_calls"
		}
		return []map[string]any{k.chunk(map[string]any{}, &reason)}
	case "metadataEvent", "MetadataEvent":
		return nil
	}
	return nil
}

func (k *KiroToOpenAI) restoreName(name string) string {
	if orig, ok := k.restore[name]; ok {
		return orig
	}
	return name
}

func (k *KiroToOpenAI) chunk(delta map[string]any, finish *string) map[string]any {
	if k.id == "" {
		k.id = "chatcmpl-" + newKiroCallID()
		k.created = nowUnix()
	}
	var fr any
	if finish != nil {
		fr = *finish
	}
	return map[string]any{
		"id": k.id, "object": "chat.completion.chunk", "created": k.created, "model": k.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": fr}},
	}
}

// CollectKiroEvents folds a whole EventStream body into one Chat Completions
// object for a non-stream client.
func CollectKiroEvents(body io.Reader, model string, restore map[string]string) map[string]any {
	k := NewKiroToOpenAI(model, restore)
	var content, reasoning strings.Builder
	toolCalls := []any{}
	var toolBuf []map[string]any
	var finish any
	br := bufio.NewReaderSize(body, 64<<10)
	for {
		frame, err := ReadKiroFrame(br)
		if err != nil {
			break
		}
		for _, ch := range k.Transform(frame) {
			choices, _ := ch["choices"].([]any)
			if len(choices) == 0 {
				continue
			}
			choice, _ := choices[0].(map[string]any)
			delta, _ := choice["delta"].(map[string]any)
			if c, ok := delta["content"].(string); ok {
				content.WriteString(c)
			}
			if r, ok := delta["reasoning_content"].(string); ok {
				reasoning.WriteString(r)
			}
			if tcs, ok := delta["tool_calls"].([]any); ok {
				for _, tc := range tcs {
					m, _ := tc.(map[string]any)
					if m == nil {
						continue
					}
					idx := len(toolBuf)
					if i, ok := m["index"].(float64); ok {
						idx = int(i)
					}
					for len(toolBuf) <= idx {
						toolBuf = append(toolBuf, map[string]any{})
					}
					toolBuf[idx] = m
				}
			}
			if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
				finish = fr
			}
		}
	}
	for i, tc := range toolBuf {
		if tc == nil {
			continue
		}
		tc["index"] = i
		toolCalls = append(toolCalls, tc)
	}
	msg := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	if finish == nil {
		finish = "stop"
		if len(toolCalls) > 0 {
			finish = "tool_calls"
		}
	}
	out := map[string]any{
		"id": k.id, "object": "chat.completion", "created": k.created, "model": model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
	}
	u := map[string]any{
		"prompt_tokens":     0,
		"completion_tokens": (content.Len() + reasoning.Len()) / 4,
		"total_tokens":      int64((content.Len() + reasoning.Len()) / 4),
	}
	out["usage"] = u
	return out
}

// ReadKiroFrame reads one complete frame from the stream.
func ReadKiroFrame(r *bufio.Reader) (eventFrame, error) {
	var prelude [12]byte
	if _, err := io.ReadFull(r, prelude[:]); err != nil {
		return eventFrame{}, err
	}
	total := int(binary.BigEndian.Uint32(prelude[0:4]))
	if total < 16 || total > eventStreamMaxMessage {
		return eventFrame{}, fmt.Errorf("AWS EventStream frame bounds are invalid")
	}
	frame := make([]byte, total)
	copy(frame, prelude[:])
	if _, err := io.ReadFull(r, frame[12:]); err != nil {
		return eventFrame{}, err
	}
	return parseEventFrame(frame)
}
