package gateway

// Cursor upstream driver. Two surfaces (see internal/translate/cursor.go):
// the legacy ConnectRPC stream on api2, and the HTTP/2 duplex AgentService on
// agent.api5 that also needs interaction acks written while the response
// streams. Both decode into Chat Completions chunks and then ride the normal
// client-format hop, or fold into one chat.completion for a non-stream client.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// cursorConv accumulates one upstream turn. With w nil it collects chunks for
// a non-stream fold instead of writing them.
type cursorConv struct {
	id        string
	created   int64
	model     string
	composer  bool
	content   strings.Builder
	thinking  strings.Builder
	visible   int
	tools     []map[string]any
	toolIndex map[string]int
	sent      map[string]bool
	emitted   bool
	sink      []map[string]any
	o2c       *translate.OpenAIToClaude
	o2r       *translate.OpenAIToResponses
	w         http.ResponseWriter
	flusher   http.Flusher
}

func newCursorConv(model, clientFormat string, w http.ResponseWriter, flusher http.Flusher) *cursorConv {
	c := &cursorConv{
		id:        "chatcmpl-" + translate.CursorUUID(),
		created:   time.Now().Unix(),
		model:     model,
		composer:  translate.IsCursorComposer(model),
		toolIndex: map[string]int{},
		sent:      map[string]bool{},
		w:         w,
		flusher:   flusher,
	}
	if w != nil {
		switch clientFormat {
		case translate.FormatClaude:
			c.o2c = &translate.OpenAIToClaude{}
		case translate.FormatResponses:
			c.o2r = &translate.OpenAIToResponses{}
		}
	}
	return c
}

func (c *cursorConv) chunk(delta map[string]any, finish any) map[string]any {
	if _, ok := delta["content"]; ok {
		c.emitted = true
	}
	return map[string]any{
		"id": c.id, "object": "chat.completion.chunk", "created": c.created, "model": c.model,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
}

func (c *cursorConv) emit(ch map[string]any) {
	if c.w == nil {
		c.sink = append(c.sink, ch)
		return
	}
	emitChat(c.w, ch, c.o2c, c.o2r)
}

// tail flushes the visible part of composer thinking once the think marker
// closes; composer never sends text deltas of its own.
func (c *cursorConv) tail() {
	if !c.composer {
		return
	}
	visible := translate.CursorVisibleFromThinking(c.thinking.String())
	if len(visible) <= c.visible {
		return
	}
	delta := visible[c.visible:]
	c.visible = len(visible)
	c.content.WriteString(delta)
	c.emit(c.chunk(map[string]any{"content": delta}, nil))
}

// ---------------------------------------------------------------- legacy

func (c *cursorConv) runLegacy(upstream interface{ Read([]byte) (int, error) }) error {
	br := bufio.NewReaderSize(upstream, 64<<10)
	for {
		frame, err := translate.ReadCursorFrame(br)
		if err != nil {
			return nil
		}
		if frame.Flags&0x02 != 0 { // trailer
			continue
		}
		if msg := cursorErrorFrame(frame.Payload); msg != "" {
			return fmt.Errorf("cursor: %s", msg)
		}
		ev := translate.DecodeCursorLegacy(frame.Payload)
		if ev.Tool != nil {
			c.legacyTool(ev.Tool)
		} else {
			c.text(ev.Text, ev.Thinking)
		}
		if c.flusher != nil {
			c.flusher.Flush()
		}
	}
}

func (c *cursorConv) legacyTool(tc *translate.CursorToolCall) {
	idx, seen := c.toolIndex[tc.ID]
	if !seen {
		idx = len(c.tools)
		c.toolIndex[tc.ID] = idx
		c.tools = append(c.tools, map[string]any{
			"id": tc.ID, "type": "function",
			"function": map[string]any{"name": tc.Name, "arguments": tc.Args},
		})
		c.sent[tc.ID] = true
	} else if tc.Args != "" {
		fn := c.tools[idx]["function"].(map[string]any)
		fn["arguments"] = fn["arguments"].(string) + tc.Args
	}
	c.emit(c.chunk(map[string]any{"tool_calls": []any{map[string]any{
		"index": idx, "id": tc.ID, "type": "function",
		"function": map[string]any{"name": tc.Name, "arguments": tc.Args},
	}}}, nil))
}

// ---------------------------------------------------------------- agent

func (c *cursorConv) runAgent(session *executor.CursorSession) error {
	br := bufio.NewReaderSize(session.Body, 64<<10)
	for {
		frame, err := translate.ReadCursorFrame(br)
		if err != nil {
			return nil
		}
		if frame.Flags&0x02 != 0 {
			continue
		}
		ev := translate.DecodeCursorAgent(frame.Payload)
		if ev.KV && ev.KVResult != 0 {
			session.Write(translate.CursorKVAck(ev.KVID, ev.KVResult, ev.KVMetadata))
		}
		if ev.Exec {
			switch {
			case len(ev.ExecFields) == 1 && ev.ExecFields[0] == 10:
				session.Write(translate.CursorRequestContextAck(ev.ExecMsgID, ev.ExecID))
			case len(ev.ExecFields) == 1 && ev.ExecFields[0] == 11:
				id := firstNonEmpty(ev.MCPID, "call_"+translate.CursorUUID())
				// The name is already the raw client tool name (mcp.toolName || mcp.name).
				c.toolCall(id, ev.MCPName, ev.MCPArgs)
				return nil
			default:
				// IDE builtins the gateway cannot run are refused one by one;
				// the server then continues with the MCP tools it was given.
				acked := false
				for _, f := range ev.ExecFields {
					if field, ok := translate.CursorExecField(f); ok {
						session.Write(translate.CursorRejectExec(ev.ExecMsgID, ev.ExecID, field))
						acked = true
						break
					}
				}
				if !acked {
					return fmt.Errorf("cursor: unsupported IDE tool request (fields %v)", ev.ExecFields)
				}
			}
		}
		c.text(ev.Text, ev.Thinking)
		if c.flusher != nil {
			c.flusher.Flush()
		}
		if ev.Ended {
			return nil
		}
	}
}

// ---------------------------------------------------------------- shared

func (c *cursorConv) text(text, thinking string) {
	if text != "" {
		c.content.WriteString(text)
		c.emit(c.chunk(map[string]any{"content": text}, nil))
	}
	if thinking != "" {
		c.thinking.WriteString(thinking)
		c.tail()
	}
}

func (c *cursorConv) toolCall(id, name string, args map[string]any) {
	b, err := json.Marshal(args)
	if err != nil || len(b) == 0 || string(b) == "null" {
		b = []byte("{}")
	}
	idx := len(c.tools)
	c.tools = append(c.tools, map[string]any{"id": id, "type": "function",
		"function": map[string]any{"name": name, "arguments": string(b)}})
	c.emit(c.chunk(map[string]any{"tool_calls": []any{map[string]any{
		"index": idx, "id": id, "type": "function",
		"function": map[string]any{"name": name, "arguments": string(b)},
	}}}, nil))
}

// finish closes the turn: a legacy tool call the stream never flagged as last
// still has to reach the client.
func (c *cursorConv) finish() {
	for id, idx := range c.toolIndex {
		if c.sent[id] {
			delete(c.sent, id)
			continue
		}
		tc := c.tools[idx]
		c.emit(c.chunk(map[string]any{"tool_calls": []any{map[string]any{
			"index": idx, "id": tc["id"], "type": "function", "function": tc["function"],
		}}}, nil))
		delete(c.sent, id)
	}
	reason := "stop"
	if len(c.tools) > 0 {
		reason = "tool_calls"
	}
	u := c.usage()
	last := c.chunk(map[string]any{}, reason)
	last["usage"] = map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
		"total_tokens": u.PromptTokens + u.CompletionTokens,
	}
	c.emit(last)
	if c.w != nil {
		finishClientHop(c.w, c.o2c, c.o2r)
		writeDone(c.w)
		if c.flusher != nil {
			c.flusher.Flush()
		}
	}
}

func (c *cursorConv) usage() translate.Usage {
	return translate.Usage{CompletionTokens: int64((c.content.Len() + c.thinking.Len()) / 4)}
}

// folded collapses the collected chunks into one chat.completion.
func (c *cursorConv) folded() map[string]any {
	msg := map[string]any{"role": "assistant", "content": c.content.String()}
	if c.thinking.Len() > 0 {
		msg["reasoning_content"] = c.thinking.String()
	}
	finish := "stop"
	if len(c.tools) > 0 {
		msg["tool_calls"] = c.tools
		finish = "tool_calls"
	}
	u := c.usage()
	return map[string]any{
		"id": c.id, "object": "chat.completion", "created": c.created, "model": c.model,
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": finish}},
		"usage": map[string]any{
			"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens,
			"total_tokens": u.PromptTokens + u.CompletionTokens,
		},
	}
}

// cursorErrorFrame extracts a JSON error the upstream sent in place of frames.
func cursorErrorFrame(payload []byte) string {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(trimmed, &m) != nil {
		return ""
	}
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		return ""
	}
	msg := str(errObj, "message")
	if details, ok := errObj["details"].([]any); ok && len(details) > 0 {
		if d, ok := details[0].(map[string]any); ok {
			if dbg, ok := d["debug"].(map[string]any); ok {
				if det, ok := dbg["details"].(map[string]any); ok {
					msg = firstNonEmpty(msg, str(det, "title"), str(det, "detail"))
				}
			}
		}
	}
	return msg
}

// ---------------------------------------------------------------- gateway entry

// cursorRequest is the per-attempt cursor plan: which surface, and the framed
// protobuf body to send.
type cursorRequest struct {
	agent bool
	body  []byte
}

// prepareCursorRequest picks the surface on capability alone (JS dispatches on
// isAgentCapableRequest only); forceAgent reaches the legacy frame as its
// unified_mode, as generateCursorBody does.
func prepareCursorRequest(body map[string]any, model string, forceAgent bool) cursorRequest {
	if cursorAgentCapable(body) {
		return cursorRequest{agent: true, body: translate.CursorAgentBody(body, model)}
	}
	return cursorRequest{body: translate.CursorBody(body, model, forceAgent)}
}

// cursorAgentCapable mirrors isAgentCapableRequest: AgentService answers text
// turns (with tools) but not image parts, which stay on the legacy protobuf.
func cursorAgentCapable(body map[string]any) bool {
	msgs, _ := body["messages"].([]any)
	if len(msgs) == 0 {
		return false
	}
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if mm == nil {
			return false
		}
		if parts, ok := mm["content"].([]any); ok {
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if pm == nil {
					continue
				}
				if t, _ := pm["type"].(string); t != "" && t != "text" {
					return false
				}
			}
			continue
		}
		if mm["content"] != nil {
			if _, ok := mm["content"].(string); !ok {
				return false
			}
		}
	}
	return true
}

// streamCursor drives one upstream turn and writes the client's SSE.
func streamCursor(up interface{ Read([]byte) (int, error) }, session *executor.CursorSession, clientFormat, model string, w http.ResponseWriter, flusher http.Flusher) (translate.Usage, error) {
	conv := newCursorConv(model, clientFormat, w, flusher)
	var err error
	if session != nil {
		err = conv.runAgent(session)
	} else {
		err = conv.runLegacy(up)
	}
	conv.finish()
	return conv.usage(), err
}

// collectCursor folds one upstream turn into a Chat Completions object.
func collectCursor(up interface{ Read([]byte) (int, error) }, session *executor.CursorSession, model string) (map[string]any, error) {
	conv := newCursorConv(model, translate.FormatOpenAI, nil, nil)
	var err error
	if session != nil {
		err = conv.runAgent(session)
	} else {
		err = conv.runLegacy(up)
	}
	if err != nil {
		return nil, err
	}
	return conv.folded(), nil
}
