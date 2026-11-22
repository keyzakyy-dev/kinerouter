package translate

// Cursor wire format. Ported from open-sse/translator/request/openai-to-cursor.js,
// open-sse/utils/cursorProtobuf.js and open-sse/executors/cursor.js.
//
// Two surfaces:
//   - legacy POST api2.cursor.sh /aiserver.v1.ChatService/StreamUnifiedChatWithTools
//     (ConnectRPC protobuf over plain HTTP; any request shape incl. images).
//   - agent  POST agent.api5.cursor.sh /agent.v1.AgentService/Run (HTTP/2
//     duplex; text turns only) — required for auto/composer and thinking
//     variants, which ChatService silently answers with an empty turn.
//
// Tool outputs ride as <tool_result> text on both paths: Cursor loops when
// protobuf tool_results carry a partially matching schema.

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

const (
	cursorRoleUser      = 1
	cursorRoleAssistant = 2

	cursorModeChat  = 1
	cursorModeAgent = 2

	cursorToolMCP = 19
)

// CursorMessages flattens an OpenAI body into the user/assistant text turns
// Cursor accepts: system → "[System Instructions]" user turn, tool results →
// "<tool_result>" blocks, assistant tool calls kept on their message.
func CursorMessages(raw map[string]any) []map[string]any {
	msgs, _ := raw["messages"].([]any)
	names := map[string]string{}
	for _, m := range msgs {
		mm, _ := m.(map[string]any)
		if str(mm, "role") != "assistant" {
			continue
		}
		if calls, ok := mm["tool_calls"].([]any); ok {
			for _, c := range calls {
				cm, _ := c.(map[string]any)
				fn, _ := cm["function"].(map[string]any)
				id := cursorToolID(str(cm, "id"))
				if id != "" {
					names[id] = firstNonEmptyStr(str(fn, "name"), "tool")
				}
			}
		}
	}

	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		switch str(msg, "role") {
		case "system":
			out = append(out, map[string]any{"role": "user", "content": "[System Instructions]\n" + cursorText(msg["content"])})
		case "tool":
			id := str(msg, "tool_call_id")
			name := firstNonEmptyStr(str(msg, "name"), names[cursorToolID(id)], "tool")
			out = append(out, map[string]any{"role": "user", "content": cursorToolBlock(name, id, cursorText(msg["content"]))})
		case "user", "assistant":
			content := cursorText(msg["content"])
			if calls, ok := msg["tool_calls"].([]any); ok && len(calls) > 0 {
				out = append(out, map[string]any{"role": "assistant", "content": content, "tool_calls": calls})
				continue
			}
			if content != "" {
				out = append(out, map[string]any{"role": str(msg, "role"), "content": content})
			}
		}
	}
	return out
}

// cursorToolBlock is the XML fallback for a tool result.
func cursorToolBlock(name, id, result string) string {
	return strings.Join([]string{
		"<tool_result>",
		"<tool_name>" + cursorEscapeXML(name) + "</tool_name>",
		"<tool_call_id>" + cursorEscapeXML(id) + "</tool_call_id>",
		"<result>" + cursorEscapeXML(cursorStripControls(result)) + "</result>",
		"</tool_result>",
	}, "\n")
}

func cursorEscapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	return strings.ReplaceAll(s, ">", "&gt;")
}

// cursorStripControls drops the control bytes that make the backend reject a
// request body.
func cursorStripControls(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f:
			return -1
		}
		return r
	}, s)
}

func cursorToolID(id string) string {
	if i := strings.IndexByte(id, '\n'); i >= 0 {
		return id[:i]
	}
	return id
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// cursorText flattens an OpenAI content field to plain text.
func cursorText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, part := range c {
			p, ok := part.(map[string]any)
			if !ok || str(p, "type") != "text" {
				continue
			}
			if s, ok := p["text"].(string); ok {
				b.WriteString(s)
			}
		}
		return b.String()
	}
	return ""
}

// ---------------------------------------------------------------- legacy wire

// CursorBody builds the framed StreamUnifiedChatWithTools body.
func CursorBody(raw map[string]any, model string, forceAgent bool) []byte {
	var tools []any
	if t, ok := raw["tools"].([]any); ok {
		tools = t
	}
	effort, _ := raw["reasoning_effort"].(string)
	return wrapCursorFrame(cursorChatRequest(CursorMessages(raw), model, tools, effort, forceAgent))
}

func wrapCursorFrame(payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

func cursorChatRequest(msgs []map[string]any, model string, tools []any, effort string, forceAgent bool) []byte {
	hasTools := len(tools) > 0
	agentic := hasTools || forceAgent
	normalized := splitMixedAssistant(msgs)

	var fields, ids []byte
	for i, m := range normalized {
		role := cursorRoleAssistant
		if str(m, "role") == "user" {
			role = cursorRoleUser
		}
		id := CursorUUID()
		ids = append(ids, pbBytes(30, pbConcat(pbString(1, id), pbUint(3, uint64(role))))...)
		fields = append(fields, pbBytes(1, cursorMessage(m, role, id, i == len(normalized)-1, hasTools))...)
	}

	mode := cursorModeChat
	if agentic {
		mode = cursorModeAgent
	}
	out := pbConcat(
		fields,
		pbUint(2, 1),
		pbBytes(3, nil), // instruction
		pbUint(4, 1),
		pbBytes(5, pbConcat(pbString(1, model), pbBytes(4, nil))),
		pbBytes(8, nil), // web tool
		pbUint(13, 1),
		pbBytes(15, cursorSetting()),
		pbUint(19, 1),
		pbString(23, CursorUUID()),
		pbBytes(26, cursorMetadata()),
		pbUint(27, uint64(boolInt(agentic))),
	)
	if agentic {
		out = append(out, pbBytes(29, pbVarint(1))...)
	}
	out = append(out, ids...)
	for _, t := range tools {
		out = append(out, pbBytes(34, cursorMCPTool(t))...)
	}
	thinking := uint64(0)
	switch effort {
	case "medium":
		thinking = 1
	case "high":
		thinking = 2
	}
	out = pbConcat(out,
		pbUint(35, 0),
		pbUint(38, 0),
		pbUint(46, uint64(mode)),
		pbBytes(47, nil),
		pbUint(48, uint64(boolInt(!agentic))),
		pbUint(49, thinking),
		pbUint(51, 0),
		pbUint(53, 1),
		pbString(54, map[bool]string{true: "Agent", false: "Ask"}[agentic]),
	)
	return pbBytes(1, out)
}

// splitMixedAssistant moves an assistant message's tool results into their own
// message: one protobuf message cannot carry both halves safely.
func splitMixedAssistant(msgs []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for i, m := range msgs {
		calls, _ := m["tool_calls"].([]any)
		results, _ := m["tool_results"].([]any)
		if str(m, "role") != "assistant" || len(calls) == 0 || len(results) == 0 {
			out = append(out, m)
			continue
		}
		dup := map[string]any{}
		for k, v := range m {
			dup[k] = v
		}
		delete(dup, "tool_results")
		out = append(out, dup)
		if i+1 < len(msgs) && sameToolIDs(results, msgs[i+1]["tool_results"]) {
			continue
		}
		out = append(out, map[string]any{"role": "assistant", "content": "", "tool_results": results})
	}
	return out
}

func sameToolIDs(a, b any) bool {
	la, _ := a.([]any)
	lb, _ := b.([]any)
	if len(la) == 0 || len(la) != len(lb) {
		return false
	}
	seen := map[string]bool{}
	for _, v := range la {
		if m, ok := v.(map[string]any); ok {
			seen[str(m, "tool_call_id")] = true
		}
	}
	for _, v := range lb {
		if m, ok := v.(map[string]any); ok {
			delete(seen, str(m, "tool_call_id"))
		}
	}
	return len(seen) == 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func cursorMessage(m map[string]any, role int, id string, isLast, hasTools bool) []byte {
	out := pbConcat(
		pbString(1, str(m, "content")),
		pbUint(2, uint64(role)),
		pbString(13, id),
	)
	if trs, ok := m["tool_results"].([]any); ok {
		for _, tr := range trs {
			if tm, ok := tr.(map[string]any); ok {
				out = append(out, pbBytes(18, cursorToolResult(tm))...)
			}
		}
	}
	mode := cursorModeChat
	if hasTools {
		mode = cursorModeAgent
	}
	out = pbConcat(out, pbUint(29, uint64(boolInt(hasTools))), pbUint(47, uint64(mode)))
	if isLast && hasTools {
		out = append(out, pbBytes(51, pbVarint(1))...)
	}
	return out
}

func cursorSetting() []byte {
	return pbConcat(
		pbString(1, `cursor\aisettings`),
		pbBytes(3, nil),
		pbBytes(6, pbConcat(pbBytes(1, nil), pbBytes(2, nil))),
		pbUint(8, 1),
		pbUint(9, 1),
	)
}

func cursorMetadata() []byte {
	return pbConcat(
		pbString(1, "linux"),
		pbString(2, "x64"),
		pbString(3, "v20.0.0"),
		pbString(4, "/"),
		pbString(5, "1970-01-01T00:00:00.000Z"),
	)
}

// cursorMCPTool encodes a declared tool for the legacy request.
func cursorMCPTool(tool any) []byte {
	m, _ := tool.(map[string]any)
	fn, _ := m["function"].(map[string]any)
	name := firstNonEmptyStr(str(fn, "name"), str(m, "name"))
	desc := firstNonEmptyStr(str(fn, "description"), str(m, "description"))
	schema, _ := fn["parameters"].(map[string]any)
	if schema == nil {
		schema, _ = m["parameters"].(map[string]any)
	}
	var out []byte
	if name != "" {
		out = append(out, pbString(1, name)...)
	}
	if desc != "" {
		out = append(out, pbString(2, desc)...)
	}
	if len(schema) > 0 {
		out = append(out, pbString(3, mustJSONString(schema))...)
	}
	return append(out, pbString(4, "custom")...)
}

// cursorToolResult encodes ConversationMessage.ToolResult for the defensive
// mixed-message path (never produced by CursorMessages).
func cursorToolResult(tr map[string]any) []byte {
	name := CursorFormatToolName(firstNonEmptyStr(str(tr, "tool_name"), str(tr, "name")))
	rawArgs := firstNonEmptyStr(str(tr, "raw_args"), "{}")
	result := firstNonEmptyStr(str(tr, "result_content"), str(tr, "result"))
	toolCallID, modelCallID := cursorParseToolID(str(tr, "tool_call_id"))
	server, selected := cursorParseToolName(name)
	idx := uint64(1)
	if n, ok := tr["tool_index"].(float64); ok && n > 0 {
		idx = uint64(n)
	}
	cv2 := pbConcat(
		pbUint(1, cursorToolMCP),
		pbBytes(28, pbConcat(pbString(1, selected), pbString(2, result))),
		pbString(35, toolCallID),
		pbUint(49, idx),
	)
	if modelCallID != "" {
		cv2 = append(cv2, pbString(48, modelCallID)...)
	}
	call := pbConcat(
		pbUint(1, cursorToolMCP),
		pbBytes(27, pbBytes(1, pbConcat(pbString(1, selected), pbString(3, rawArgs), pbString(4, server)))),
		pbString(3, toolCallID),
		pbString(9, name),
		pbString(10, rawArgs),
		pbUint(48, idx),
	)
	if modelCallID != "" {
		call = append(call, pbString(49, modelCallID)...)
	}
	out := pbConcat(
		pbString(1, toolCallID),
		pbString(2, name),
		pbUint(3, idx),
		pbString(5, rawArgs),
		pbBytes(8, cv2),
		pbBytes(11, call),
	)
	if modelCallID != "" {
		out = append(out, pbString(12, modelCallID)...)
	}
	return out
}

// CursorFormatToolName maps "mcp__server__tool" → "mcp_server_tool" and every
// plain name to "mcp_custom_<name>", the IDE's MCP namespace.
func CursorFormatToolName(name string) string {
	if name == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(name, "mcp__"); ok {
		if i := strings.Index(rest, "__"); i >= 0 {
			return "mcp_" + firstNonEmptyStr(rest[:i], "custom") + "_" + firstNonEmptyStr(rest[i+2:], "tool")
		}
		return "mcp_custom_" + firstNonEmptyStr(rest, "tool")
	}
	if strings.HasPrefix(name, "mcp_") {
		return name
	}
	return "mcp_custom_" + name
}

func cursorParseToolName(formatted string) (server, tool string) {
	rest, ok := strings.CutPrefix(formatted, "mcp_")
	if !ok {
		return "custom", firstNonEmptyStr(formatted, "tool")
	}
	if i := strings.IndexByte(rest, '_'); i >= 0 {
		return firstNonEmptyStr(rest[:i], "custom"), firstNonEmptyStr(rest[i+1:], "tool")
	}
	return "custom", firstNonEmptyStr(rest, "tool")
}

// cursorParseToolID splits the model call id Cursor appends after "\nmc_".
func cursorParseToolID(id string) (toolCallID, modelCallID string) {
	if i := strings.Index(id, "\nmc_"); i >= 0 {
		return id[:i], id[i+len("\nmc_"):]
	}
	return id, ""
}

// ---------------------------------------------------------------- agent wire

// CursorAgentBody builds the framed AgentService Run body.
func CursorAgentBody(raw map[string]any, model string) []byte {
	var tools []any
	if t, ok := raw["tools"].([]any); ok {
		tools = t
	}
	effort, _ := raw["reasoning_effort"].(string)
	return wrapCursorFrame(cursorAgentRun(CursorMessages(raw), model, tools, effort))
}

func cursorAgentRun(msgs []map[string]any, model string, tools []any, effort string) []byte {
	// RunRequest.custom_system_prompt (field 8) makes AgentService answer with
	// an empty turn; fold system text into the current user turn instead.
	var sys []string
	chat := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if str(m, "role") == "system" {
			if s := str(m, "content"); s != "" {
				sys = append(sys, s)
			}
			continue
		}
		chat = append(chat, m)
	}
	current := len(chat) - 1
	for i := len(chat) - 1; i >= 0; i-- {
		if str(chat[i], "role") == "user" {
			current = i
			break
		}
	}
	var history [][]byte
	for _, m := range chat[:max(current, 0)] {
		if h := cursorHistoryMessage(m); h != nil {
			history = append(history, pbBytes(1, h))
		}
	}
	userText := "Continue."
	if current >= 0 && current < len(chat) {
		if s := str(chat[current], "content"); s != "" {
			userText = s
		}
	}
	if len(sys) > 0 {
		userText = strings.Join(sys, "\n\n") + "\n\n" + userText
	}
	userMessage := pbConcat(
		pbString(1, userText),
		pbString(2, CursorUUID()),
		pbBytes(3, nil), // selected_context
		pbUint(4, 1),    // mode: agent
	)
	userAction := pbBytes(1, userMessage)
	if len(history) > 0 {
		userAction = append(userAction, pbBytes(7, pbConcat(history...))...)
	}

	e := strings.ToLower(effort)
	switch e {
	case "ultra":
		e = "max"
	case "minimal":
		e = "low"
	}
	var param []byte
	if e != "" && e != "none" {
		key := "effort"
		if strings.Contains(strings.ToLower(model), "gpt-") {
			key = "reasoning"
		}
		param = pbBytes(3, pbConcat(pbString(1, key), pbString(2, e)))
	}
	requested := pbConcat(pbString(1, model), pbUint(7, 1))
	if param != nil {
		requested = append(requested, param...)
	}
	modelDetails := pbConcat(pbString(1, model), pbString(3, model), pbString(4, model))

	run := pbConcat(
		pbBytes(1, nil), // empty ConversationStateStructure: fresh local session
		pbBytes(2, pbBytes(1, userAction)),
		pbBytes(3, modelDetails),
	)
	if mcp := cursorAgentMCPTools(tools); len(mcp) > 0 {
		run = append(run, pbBytes(4, mcp)...)
	}
	run = append(run, pbBytes(9, requested)...)
	return pbBytes(1, run) // AgentClientMessage.run_request
}

// cursorHistoryMessage encodes one ConversationHistoryMessage.
func cursorHistoryMessage(m map[string]any) []byte {
	var extras []string
	if str(m, "role") == "assistant" {
		if calls, ok := m["tool_calls"].([]any); ok {
			for _, c := range calls {
				cm, _ := c.(map[string]any)
				fn, _ := cm["function"].(map[string]any)
				extras = append(extras, fmt.Sprintf("[tool_call id=%s name=%s args=%s]",
					str(cm, "id"), firstNonEmptyStr(str(fn, "name"), "tool"), firstNonEmptyStr(str(fn, "arguments"), "{}")))
			}
		}
	}
	if str(m, "role") == "tool" {
		extras = append(extras, "[tool_result id="+str(m, "tool_call_id")+"]")
	}
	body := strings.TrimSpace(strings.Join(append([]string{str(m, "content")}, extras...), "\n"))
	if body == "" {
		return nil
	}
	inner := pbBytes(1, pbBytes(1, pbString(1, body)))
	if str(m, "role") == "assistant" {
		return pbBytes(2, inner)
	}
	return pbBytes(1, inner)
}

// cursorAgentMCPTools encodes AgentRunRequest.mcp_tools.
func cursorAgentMCPTools(tools []any) []byte {
	var out []byte
	for _, raw := range tools {
		m, _ := raw.(map[string]any)
		fn, _ := m["function"].(map[string]any)
		name := firstNonEmptyStr(str(fn, "name"), str(m, "name"))
		if name == "" {
			continue
		}
		desc := firstNonEmptyStr(str(fn, "description"), str(m, "description"))
		schema, _ := fn["parameters"].(map[string]any)
		if schema == nil {
			schema, _ = m["parameters"].(map[string]any)
		}
		def := pbConcat(
			pbString(1, name),
			pbString(2, desc),
			pbBytes(3, pbEncodeValue(map[string]any(schema))),
			pbString(4, "kinerouter"),
			pbString(5, name),
		)
		out = append(out, pbBytes(1, def)...)
	}
	return out
}

// ------------------------------------------------------------- agent acks

// CursorKVAck answers a KvServerMessage blob get/set so the stream continues.
// resultField is 2 (get) or 3 (set).
func CursorKVAck(kvID uint64, resultField int, metadata []byte) []byte {
	payload := []byte{}
	if resultField == 2 {
		payload = pbBytes(1, nil)
	}
	parts := pbConcat(pbUint(1, kvID), pbBytes(resultField, payload))
	if len(metadata) > 0 {
		parts = append(parts, pbBytes(4, metadata)...)
	}
	return wrapCursorFrame(pbBytes(3, parts)) // AgentClientMessage.kv_client_message
}

// CursorExecAck answers an exec request. request_context (field 10) gets an
// empty success; a rejected IDE builtin reports the same field as a message.
func CursorExecAck(msgID uint64, execID string, resultField int, payload []byte) []byte {
	parts := pbConcat(pbUint(1, msgID), pbString(15, execID), pbBytes(resultField, payload))
	return wrapCursorFrame(pbBytes(2, parts)) // AgentClientMessage.exec_client_message
}

// CursorRequestContextAck builds the empty RequestContextSuccess reply. The
// tools already ride on mcp_tools; echoing them here stalls the stream.
func CursorRequestContextAck(msgID uint64, execID string) []byte {
	return CursorExecAck(msgID, execID, 10, pbBytes(1, pbBytes(1, nil)))
}

// CursorRejectExec refuses an IDE builtin the gateway cannot run.
func CursorRejectExec(msgID uint64, execID string, field int) []byte {
	if field == 9 { // diagnostics has no rejected variant
		return CursorExecAck(msgID, execID, 9, nil)
	}
	msg := "Tool not available in this environment. Use the MCP tools provided instead."
	return CursorExecAck(msgID, execID, field, pbBytes(2, pbString(2, msg)))
}

// cursorExecResultField maps an ExecServerMessage variant to its ack field.
func CursorExecField(field int) (int, bool) {
	switch field {
	case 2, 3, 4, 5, 7, 8, 9, 16, 20, 23:
		return field, true
	}
	return 0, false
}

// CursorAgentServerEvent classifies one AgentServerMessage.
type CursorAgentEvent struct {
	Text       string
	Thinking   string
	Ended      bool
	KV         bool
	KVID       uint64
	KVResult   int
	KVMetadata []byte
	Exec       bool
	ExecMsgID  uint64
	ExecID     string
	ExecFields []int
	MCPName    string
	MCPID      string
	MCPArgs    map[string]any
}

// DecodeCursorAgent decodes one agent frame payload.
func DecodeCursorAgent(payload []byte) CursorAgentEvent {
	f := pbDecode(payload)
	var ev CursorAgentEvent
	if upd := f[1]; len(upd) > 0 {
		u := pbDecode(upd[0].bytes)
		if td := u[1]; len(td) > 0 {
			ev.Text = pbStringField(pbDecode(td[0].bytes), 1)
		}
		if th := u[4]; len(th) > 0 {
			ev.Thinking = pbStringField(pbDecode(th[0].bytes), 1)
		}
		if len(u[14]) > 0 {
			ev.Ended = true
		}
	}
	if kf := f[4]; len(kf) > 0 {
		kv := pbDecode(kf[0].bytes)
		ev.KV = true
		ev.KVID = pbUintField(kv, 1)
		if md := kv[4]; len(md) > 0 {
			ev.KVMetadata = md[0].bytes
		}
		switch {
		case len(kv[2]) > 0:
			ev.KVResult = 2
		case len(kv[3]) > 0:
			ev.KVResult = 3
		}
	}
	if ef := f[2]; len(ef) > 0 {
		ex := pbDecode(ef[0].bytes)
		ev.Exec = true
		ev.ExecMsgID = pbUintField(ex, 1)
		ev.ExecID = pbStringField(ex, 15)
		for num := range ex {
			if num != 1 && num != 15 {
				ev.ExecFields = append(ev.ExecFields, num)
			}
		}
		if mcp := ex[11]; len(mcp) > 0 {
			m := pbDecode(mcp[0].bytes)
			ev.MCPName = firstNonEmptyStr(pbStringField(m, 5), pbStringField(m, 1))
			ev.MCPID = pbStringField(m, 3)
			ev.MCPArgs = map[string]any{}
			for _, entry := range m[2] {
				pair := pbDecode(entry.bytes)
				if key := pbStringField(pair, 1); key != "" {
					ev.MCPArgs[key] = pbDecodeValue(pbStringFieldBytes(pair, 2))
				}
			}
		}
	}
	return ev
}

// ------------------------------------------------------------- response wire

// CursorFrame is one decoded ConnectRPC frame.
type CursorFrame struct {
	Flags   byte
	Payload []byte
}

// ReadCursorFrame reads one frame, gunzipping a compressed payload.
func ReadCursorFrame(r io.Reader) (CursorFrame, error) {
	const maxFrame = 32 << 20
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return CursorFrame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrame {
		return CursorFrame{}, io.ErrUnexpectedEOF
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return CursorFrame{}, err
	}
	f := CursorFrame{Flags: hdr[0], Payload: payload}
	if f.Flags&0x01 != 0 {
		if zr, err := gzip.NewReader(byteReader(payload)); err == nil {
			if b, err := io.ReadAll(io.LimitReader(zr, maxFrame)); err == nil {
				f.Payload = b
			}
			zr.Close()
		}
	}
	return f, nil
}

// CursorLegacyEvent is one decoded legacy upstream frame.
type CursorLegacyEvent struct {
	Text     string
	Thinking string
	Tool     *CursorToolCall
}

// CursorToolCall is a (possibly partial) upstream tool call.
type CursorToolCall struct {
	ID     string
	Name   string
	Args   string
	IsLast bool
}

// DecodeCursorLegacy decodes one StreamUnifiedChatResponseWithTools payload.
func DecodeCursorLegacy(payload []byte) CursorLegacyEvent {
	f := pbDecode(payload)
	if tf := f[1]; len(tf) > 0 {
		if call := decodeCursorToolCall(tf[0].bytes); call != nil {
			return CursorLegacyEvent{Tool: call}
		}
	}
	if rf := f[2]; len(rf) > 0 {
		resp := pbDecode(rf[0].bytes)
		ev := CursorLegacyEvent{Text: pbStringField(resp, 1)}
		if tk := resp[25]; len(tk) > 0 {
			ev.Thinking = pbStringField(pbDecode(tk[0].bytes), 1)
		}
		return ev
	}
	return CursorLegacyEvent{}
}

func decodeCursorToolCall(data []byte) *CursorToolCall {
	f := pbDecode(data)
	id := cursorToolID(pbStringField(f, 3))
	name := pbStringField(f, 9)
	args := ""
	if mp := f[27]; len(mp) > 0 {
		mcp := pbDecode(mp[0].bytes)
		if list := mcp[1]; len(list) > 0 {
			tool := pbDecode(list[0].bytes)
			if n := pbStringField(tool, 1); n != "" {
				name = n
			}
			args = pbStringField(tool, 3)
		}
	}
	if args == "" {
		args = pbStringField(f, 10)
	}
	if args == "" {
		args = "{}"
	}
	if id == "" || name == "" {
		return nil
	}
	return &CursorToolCall{ID: id, Name: name, Args: args, IsLast: pbUintField(f, 11) != 0}
}

// CursorVisibleFromThinking returns the answer Composer emits after a
// "</think>" marker inside the thinking channel.
func CursorVisibleFromThinking(thinking string) string {
	const endTag = "</think>"
	i := strings.LastIndex(thinking, endTag)
	if i < 0 {
		return ""
	}
	return strings.TrimLeft(thinking[i+len(endTag):], " \t\r\n")
}

// IsCursorComposer reports whether model is a composer variant.
func IsCursorComposer(model string) bool {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	m := strings.ToLower(model)
	return m == "composer" || strings.HasPrefix(m, "composer-") || strings.HasPrefix(m, "composer.")
}

// CursorUUID returns a random RFC 4122 v4 id (message ids, synthetic tool call ids).
func CursorUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func byteReader(b []byte) io.Reader { return strings.NewReader(string(b)) }
