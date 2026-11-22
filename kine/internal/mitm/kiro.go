package mitm

// AWS EventStream encoder + Kiro (CodeWhisperer) → OpenAI → EventStream bridge.
// Mirrors src/mitm/handlers/kiro.js. The decoder already lives in
// internal/translate/kiro_stream.go; this is the write side.

import (
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"regexp"
	"sort"
	"strings"
)

var crcTable = crc32.MakeTable(crc32.IEEE)

func crc32Sum(b []byte) uint32 { return crc32.Checksum(b, crcTable) }

// encodeHeader writes one Smithy header: [nameLen 1B][name][type=7][valLen 2B][value].
func encodeHeader(name, value string) []byte {
	nb := []byte(name)
	vb := []byte(value)
	out := make([]byte, 0, 1+len(nb)+1+2+len(vb))
	out = append(out, byte(len(nb)))
	out = append(out, nb...)
	out = append(out, 7) // string type
	out = append(out, byte(len(vb)>>8), byte(len(vb)))
	out = append(out, vb...)
	return out
}

// BuildEventStreamFrame assembles one frame:
//
//	[totalLen 4B][headersLen 4B][preludeCRC 4B][headers][payload][messageCRC 4B]
func BuildEventStreamFrame(eventType string, payload any, contentType string) []byte {
	var body []byte
	if s, ok := payload.(string); ok {
		body = []byte(s)
	} else {
		body, _ = json.Marshal(payload)
	}
	if contentType == "" {
		contentType = "application/json"
	}
	headers := append(encodeHeader(":message-type", "event"), encodeHeader(":event-type", eventType)...)
	headers = append(headers, encodeHeader(":content-type", contentType)...)

	total := 12 + len(headers) + len(body) + 4
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[0:4], uint32(total))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
	binary.BigEndian.PutUint32(frame[8:12], crc32Sum(frame[0:8]))
	copy(frame[12:], headers)
	copy(frame[12+len(headers):], body)
	binary.BigEndian.PutUint32(frame[total-4:], crc32Sum(frame[:total-4]))
	return frame
}

// buildInitialResponseFrame: Kiro Runtime always opens the stream with this.
func buildInitialResponseFrame() []byte {
	return BuildEventStreamFrame("initial-response",
		map[string]any{"conversationId": ""}, "application/x-amz-json-1.0")
}

// kiroState is mutable across SSE chunks.
type kiroState struct {
	modelID      string
	toolCallInit map[int]kiroTool
	hasToolCalls bool
	finishSent   bool
	usage        map[string]any
	inThink      bool
	thinkBuf     string
	initialSent  bool
}

type kiroTool struct {
	ID   string
	Name string
}

func newKiroState(model string) *kiroState {
	return &kiroState{modelID: model, toolCallInit: map[int]kiroTool{}}
}

func (s *kiroState) withInitial(frames [][]byte) [][]byte {
	if s.initialSent {
		return frames
	}
	s.initialSent = true
	return append([][]byte{buildInitialResponseFrame()}, frames...)
}

// ─── thinking-block extraction ───────────────────────────────────────────────

var (
	thinkOpenThinking = regexp.MustCompile(`(?i)<thinking>`)
	thinkOpenThink    = regexp.MustCompile(`(?i)<think>`)
)

// extractThinking splits an inline <thinking>/<think> block out of streamed
// text. A block split across chunks stays buffered in s until closed.
func extractThinking(text string, s *kiroState) (thinking, rest string) {
	if text == "" {
		return "", ""
	}
	working := text
	if s.inThink && s.thinkBuf != "" {
		working = s.thinkBuf + working
		s.thinkBuf = ""
		s.inThink = false
	}
	loc := thinkOpenThinking.FindStringIndex(working)
	closeTag := "</thinking>"
	if loc == nil {
		loc = thinkOpenThink.FindStringIndex(working)
		closeTag = "</think>"
	}
	if loc == nil {
		return "", working
	}
	start := loc[1]
	end := strings.Index(strings.ToLower(working[start:]), closeTag)
	if end < 0 {
		s.inThink = true
		s.thinkBuf = working[loc[0]:]
		return "", strings.TrimSpace(working[:loc[0]])
	}
	body := working[start : start+end]
	before := strings.TrimSpace(working[:loc[0]])
	after := strings.TrimSpace(working[start+end+len(closeTag):])
	return body, strings.TrimSpace(before + after)
}

// ─── CodeWhisperer request → OpenAI ──────────────────────────────────────────

var inlineImageMIME = map[string]string{
	"png": "image/png", "jpeg": "image/jpeg", "jpg": "image/jpeg",
	"gif": "image/gif", "webp": "image/webp",
}

func safeArgsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "{}"
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return "{}"
		}
		return string(b)
	}
}

func convertUserInputMessage(uim map[string]any) []map[string]any {
	var out []map[string]any
	ctx, _ := uim["userInputMessageContext"].(map[string]any)
	results, _ := ctx["toolResults"].([]any)
	for _, r := range results {
		rm, _ := r.(map[string]any)
		if rm == nil {
			continue
		}
		var texts []string
		if cs, ok := rm["content"].([]any); ok {
			for _, c := range cs {
				if cm, ok := c.(map[string]any); ok {
					if t, ok := cm["text"].(string); ok {
						texts = append(texts, t)
					}
				}
			}
		}
		id, _ := rm["toolUseId"].(string)
		out = append(out, map[string]any{"role": "tool", "tool_call_id": id, "content": strings.Join(texts, "\n")})
	}

	images, _ := uim["images"].([]any)
	var imageParts []any
	for _, im := range images {
		img, ok := im.(map[string]any)
		if !ok {
			continue
		}
		format, _ := img["format"].(string)
		mime, ok := inlineImageMIME[format]
		if !ok {
			continue
		}
		src, _ := img["source"].(map[string]any)
		bytesStr, _ := src["bytes"].(string)
		if bytesStr == "" {
			continue
		}
		imageParts = append(imageParts, map[string]any{
			"type":      "image_url",
			"image_url": map[string]any{"url": "data:" + mime + ";base64," + bytesStr},
		})
	}

	text, _ := uim["content"].(string)
	text = strings.TrimSpace(text)
	switch {
	case len(imageParts) > 0:
		content := []any{}
		if text != "" {
			content = append(content, map[string]any{"type": "text", "text": text})
		}
		content = append(content, imageParts...)
		out = append(out, map[string]any{"role": "user", "content": content})
	case text != "" || len(results) == 0:
		out = append(out, map[string]any{"role": "user", "content": text})
	}
	return out
}

func convertAssistantResponseMessage(arm map[string]any) map[string]any {
	if uses, ok := arm["toolUses"].([]any); ok && len(uses) > 0 {
		var calls []any
		for i, u := range uses {
			um, _ := u.(map[string]any)
			if um == nil {
				continue
			}
			id, _ := um["toolUseId"].(string)
			if id == "" {
				id = "call_" + itoa(i)
			}
			name, _ := um["name"].(string)
			calls = append(calls, map[string]any{
				"id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": safeArgsString(um["input"])},
			})
		}
		return map[string]any{"role": "assistant", "content": arm["content"], "tool_calls": calls}
	}
	content, _ := arm["content"].(string)
	return map[string]any{"role": "assistant", "content": content}
}

func codeWhispererToMessages(body map[string]any) []map[string]any {
	cs, _ := body["conversationState"].(map[string]any)
	var out []map[string]any
	if history, ok := cs["history"].([]any); ok {
		for _, h := range history {
			hm, _ := h.(map[string]any)
			if hm == nil {
				continue
			}
			if uim, ok := hm["userInputMessage"].(map[string]any); ok {
				out = append(out, convertUserInputMessage(uim)...)
			} else if arm, ok := hm["assistantResponseMessage"].(map[string]any); ok {
				out = append(out, convertAssistantResponseMessage(arm))
			}
		}
	}
	if cur, ok := cs["currentMessage"].(map[string]any); ok {
		if uim, ok := cur["userInputMessage"].(map[string]any); ok {
			out = append(out, convertUserInputMessage(uim)...)
		}
	}
	return out
}

func extractTools(body map[string]any) []any {
	cs, _ := body["conversationState"].(map[string]any)
	var cwTools []any
	if cur, ok := cs["currentMessage"].(map[string]any); ok {
		if uim, ok := cur["userInputMessage"].(map[string]any); ok {
			if ctx, ok := uim["userInputMessageContext"].(map[string]any); ok {
				cwTools, _ = ctx["tools"].([]any)
			}
		}
	}
	if len(cwTools) == 0 {
		if history, ok := cs["history"].([]any); ok {
			for _, h := range history {
				hm, _ := h.(map[string]any)
				if hm == nil {
					continue
				}
				uim, _ := hm["userInputMessage"].(map[string]any)
				ctx, _ := uim["userInputMessageContext"].(map[string]any)
				if t, ok := ctx["tools"].([]any); ok && len(t) > 0 {
					cwTools = t
					break
				}
			}
		}
	}
	var out []any
	for _, item := range cwTools {
		im, _ := item.(map[string]any)
		spec, _ := im["toolSpecification"].(map[string]any)
		if spec == nil {
			spec = im
		}
		name, _ := spec["name"].(string)
		desc, _ := spec["description"].(string)
		if desc == "" {
			desc = "Tool: " + name
		}
		params := map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
		if is, ok := spec["inputSchema"].(map[string]any); ok {
			if j, ok := is["json"].(map[string]any); ok {
				params = j
			}
		}
		out = append(out, map[string]any{"type": "function", "function": map[string]any{
			"name": name, "description": desc, "parameters": params,
		}})
	}
	return out
}

// ─── OpenAI SSE chunk → EventStream frames ───────────────────────────────────

// convertOpenAIToKiro turns one parsed Chat Completions chunk into frames.
// A nil chunk means flush.
func convertOpenAIToKiro(chunk map[string]any, s *kiroState) [][]byte {
	if chunk == nil {
		if s.finishSent {
			return nil
		}
		if s.inThink && s.thinkBuf != "" {
			s.inThink = false
			buf := s.thinkBuf
			s.thinkBuf = ""
			s.finishSent = true
			return s.withInitial([][]byte{BuildEventStreamFrame("reasoningContentEvent", map[string]any{
				"content": buf, "modelId": s.modelOr("kiro-unknown"),
			}, "")})
		}
		s.finishSent = true
		return s.withInitial([][]byte{BuildEventStreamFrame("messageStopEvent", map[string]any{}, "")})
	}

	var frames [][]byte
	choice := firstChoice(chunk)
	delta, _ := choice["delta"].(map[string]any)
	if s.modelID == "" {
		if m, ok := chunk["model"].(string); ok {
			s.modelID = m
		}
	}
	if u, ok := chunk["usage"].(map[string]any); ok {
		s.usage = u
	}

	if tcs, ok := delta["tool_calls"].([]any); ok {
		s.hasToolCalls = true
		for _, t := range tcs {
			tm, _ := t.(map[string]any)
			if tm == nil {
				continue
			}
			idx := intOf(tm["index"])
			fn, _ := tm["function"].(map[string]any)
			id, _ := tm["id"].(string)
			name, _ := fn["name"].(string)
			if id != "" && name != "" {
				if _, seen := s.toolCallInit[idx]; !seen {
					s.toolCallInit[idx] = kiroTool{ID: id, Name: name}
					frames = append(frames, BuildEventStreamFrame("toolUseEvent", map[string]any{
						"name": name, "toolUseId": id,
					}, ""))
				}
			}
			if args, ok := fn["arguments"].(string); ok && args != "" {
				init := s.toolCallInit[idx]
				frames = append(frames, BuildEventStreamFrame("toolUseEvent", map[string]any{
					"input": args, "name": init.Name, "toolUseId": init.ID,
				}, ""))
			}
		}
	}

	if rc, ok := delta["reasoning_content"].(string); ok && rc != "" {
		frames = append(frames, BuildEventStreamFrame("reasoningContentEvent", map[string]any{
			"content": rc, "modelId": s.modelOr("unknown"),
		}, ""))
	}

	if content, ok := delta["content"].(string); ok && content != "" {
		thinking, rest := extractThinking(content, s)
		if thinking != "" {
			frames = append(frames, BuildEventStreamFrame("reasoningContentEvent", map[string]any{
				"content": thinking, "modelId": s.modelOr("unknown"),
			}, ""))
		}
		if rest != "" {
			frames = append(frames, BuildEventStreamFrame("assistantResponseEvent", map[string]any{
				"content": rest, "modelId": s.modelOr("unknown"),
			}, ""))
		}
	}

	if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
		frames = append(frames, s.emitFinish()...)
	}

	if len(frames) == 0 {
		if !s.initialSent {
			return s.withInitial(nil)
		}
		return nil
	}
	return s.withInitial(frames)
}

func (s *kiroState) emitFinish() [][]byte {
	var frames [][]byte
	if s.hasToolCalls {
		keys := make([]int, 0, len(s.toolCallInit))
		for k := range s.toolCallInit {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			t := s.toolCallInit[k]
			frames = append(frames, BuildEventStreamFrame("toolUseEvent", map[string]any{
				"name": t.Name, "stop": true, "toolUseId": t.ID,
			}, ""))
		}
	} else {
		frames = append(frames, BuildEventStreamFrame("messageStopEvent", map[string]any{}, ""))
	}
	s.finishSent = true
	if s.usage != nil {
		frames = append(frames, BuildEventStreamFrame("usageEvent", map[string]any{
			"inputTokens": intOf(s.usage["prompt_tokens"]), "outputTokens": intOf(s.usage["completion_tokens"]),
		}, ""))
	}
	s.toolCallInit = map[int]kiroTool{}
	return frames
}

func (s *kiroState) modelOr(def string) string {
	if s.modelID != "" {
		return s.modelID
	}
	return def
}

func firstChoice(chunk map[string]any) map[string]any {
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return map[string]any{}
	}
	m, _ := choices[0].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return 0
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		buf[p] = '-'
	}
	return string(buf[p:])
}
