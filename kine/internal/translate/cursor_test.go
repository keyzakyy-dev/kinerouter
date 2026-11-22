package translate

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestCursorFormatToolNameRoundTrip(t *testing.T) {
	cases := map[string]string{
		"Write":         "mcp_custom_Write",
		"mcp__fs__read": "mcp_fs_read",
		"mcp__fs":       "mcp_custom_fs",
		"mcp_already":   "mcp_already",
	}
	for in, want := range cases {
		if got := CursorFormatToolName(in); got != want {
			t.Fatalf("CursorFormatToolName(%q) = %q, want %q", in, got, want)
		}
	}
	server, tool := cursorParseToolName("mcp_fs_read_file")
	if server != "fs" || tool != "read_file" {
		t.Fatalf("cursorParseToolName() = %q/%q", server, tool)
	}
}

func TestCursorToolBlockEscapesAndStrips(t *testing.T) {
	block := cursorToolBlock("Write", "call-1", "a < b && c\x01d")
	if !strings.Contains(block, "a &lt; b &amp;&amp; cd") {
		t.Fatalf("tool result not escaped/stripped: %q", block)
	}
	if strings.ContainsRune(block, 0x01) {
		t.Fatal("control byte survived")
	}
	if !strings.HasPrefix(block, "<tool_result>\n<tool_name>Write</tool_name>") {
		t.Fatalf("unexpected block shape: %q", block)
	}
}

func TestCursorMessagesFlatten(t *testing.T) {
	raw := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "be terse"},
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": "", "tool_calls": []any{
			map[string]any{"id": "call-1", "function": map[string]any{"name": "Write", "arguments": "{}"}},
		}},
		map[string]any{"role": "tool", "tool_call_id": "call-1", "content": "ok"},
	}}
	msgs := CursorMessages(raw)
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4: %#v", len(msgs), msgs)
	}
	if c, _ := msgs[0]["content"].(string); !strings.HasPrefix(c, "[System Instructions]") {
		t.Fatalf("system not folded: %q", c)
	}
	if c, _ := msgs[3]["content"].(string); !strings.Contains(c, "<tool_name>Write</tool_name>") {
		t.Fatalf("tool result missing tool name from the assistant turn: %q", c)
	}
}

// frameOf pulls the payload back out of a wrapped frame.
func frameOf(t *testing.T, framed []byte) []byte {
	t.Helper()
	if len(framed) < 5 {
		t.Fatal("frame too short")
	}
	return framed[5:]
}

func TestCursorBodiesAndLegacyDecode(t *testing.T) {
	raw := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "mcp__fs__read", "description": "d", "parameters": map[string]any{"type": "object"},
		}}},
	}
	legacy := CursorBody(raw, "claude-4.5", false)
	if len(legacy) < 6 || legacy[0] != 0x00 {
		t.Fatalf("legacy frame flags = %#x", legacy[0])
	}
	agent := CursorAgentBody(raw, "claude-4.5")
	if len(agent) < 6 || agent[0] != 0x00 {
		t.Fatalf("agent frame flags = %#x", agent[0])
	}
	if bytes.Equal(frameOf(t, legacy), frameOf(t, agent)) {
		t.Fatal("legacy and agent bodies must differ")
	}

	// Legacy response: RESPONSE(2){RESPONSE_TEXT(1), THINKING(25){1}}.
	resp := pbConcat(pbString(1, "hello"), pbBytes(25, pbString(1, "pondering")))
	ev := DecodeCursorLegacy(pbBytes(2, resp))
	if ev.Text != "hello" || ev.Thinking != "pondering" {
		t.Fatalf("legacy decode = %+v", ev)
	}

	// Legacy tool call: CV2Call{3 call id, 9 name, 10 raw args, 11 is last}.
	call := pbConcat(pbString(3, "call-1"), pbString(9, "Write"), pbString(10, `{"a":1}`), pbUint(11, 1))
	tev := DecodeCursorLegacy(pbBytes(1, call))
	if tev.Tool == nil || tev.Tool.ID != "call-1" || tev.Tool.Name != "Write" || !tev.Tool.IsLast {
		t.Fatalf("tool decode = %+v", tev.Tool)
	}
}

func TestDecodeCursorAgent(t *testing.T) {
	// interaction_update{1 text_delta{1 "hi"}, 4 thinking_delta{1 "hmm"}, 14 finish}
	upd := pbConcat(pbBytes(1, pbString(1, "hi")), pbBytes(4, pbString(1, "hmm")), pbBytes(14, nil))
	ev := DecodeCursorAgent(pbBytes(1, upd))
	if ev.Text != "hi" || ev.Thinking != "hmm" || !ev.Ended {
		t.Fatalf("agent text decode = %+v", ev)
	}

	// MCP exec: exec_request{1 msg id, 15 exec id, 11 McpArgs{5 tool name, 3 call id, 2 entries}}
	entry := pbConcat(pbString(1, "path"), pbBytes(2, pbEncodedValueString("/tmp")))
	mcp := pbConcat(pbString(5, "Write"), pbString(3, "call-9"),
		pbBytes(2, entry), pbBytes(2, pbConcat(pbString(1, "n"), pbBytes(2, pbEncodedValueNumber(2)))))
	exec := pbConcat(pbUint(1, 7), pbString(15, "exec-1"), pbBytes(11, mcp))
	xev := DecodeCursorAgent(pbBytes(2, exec))
	if !xev.Exec || xev.ExecMsgID != 7 || xev.ExecID != "exec-1" {
		t.Fatalf("exec decode = %+v", xev)
	}
	if xev.MCPName != "Write" || xev.MCPID != "call-9" {
		t.Fatalf("mcp decode = %+v", xev)
	}
	if got, _ := xev.MCPArgs["path"].(string); got != "/tmp" {
		t.Fatalf("mcp arg = %#v", xev.MCPArgs)
	}

	// KV get: kv_server_message{1 id, 2 get}
	kv := pbConcat(pbUint(1, 3), pbBytes(2, nil))
	kev := DecodeCursorAgent(pbBytes(4, kv))
	if !kev.KV || kev.KVID != 3 || kev.KVResult != 2 {
		t.Fatalf("kv decode = %+v", kev)
	}
}

// pbEncodedValueString/Number build a google.protobuf.Value payload.
func pbEncodedValueString(s string) []byte { return pbString(3, s) }
func pbEncodedValueNumber(n float64) []byte {
	return pbDouble(2, n)
}

func TestCursorAcksAreFramed(t *testing.T) {
	for name, frame := range map[string][]byte{
		"kv":  CursorKVAck(1, 2, nil),
		"rc":  CursorRequestContextAck(1, "e"),
		"rej": CursorRejectExec(1, "e", 2),
		"ex":  CursorExecAck(1, "e", 2, nil),
	} {
		if len(frame) < 6 || frame[0] != 0x00 {
			t.Fatalf("%s ack is not a plain frame: %x", name, frame)
		}
	}
	// 11 is MCP (handled as a tool call), never an echoed field.
	if _, ok := CursorExecField(11); ok {
		t.Fatal("CursorExecField(11) must stay unmapped")
	}
	if field, ok := CursorExecField(20); !ok || field != 20 {
		t.Fatal("CursorExecField(20) should map to field 20")
	}
}

func TestCursorVisibleFromThinking(t *testing.T) {
	if got := CursorVisibleFromThinking("reasoning</think>  answer"); got != "answer" {
		t.Fatalf("visible = %q", got)
	}
	if got := CursorVisibleFromThinking("still thinking"); got != "" {
		t.Fatalf("visible without marker = %q", got)
	}
	if !IsCursorComposer("cursor/composer-1") || IsCursorComposer("gpt-5.2") {
		t.Fatal("composer detection wrong")
	}
}

func TestReadCursorFrameGunzips(t *testing.T) {
	var buf bytes.Buffer
	payload := pbString(1, "hello")
	zw := gzip.NewWriter(&buf)
	zw.Write(payload)
	zw.Close()
	var raw bytes.Buffer
	raw.WriteByte(0x01)
	raw.Write([]byte{byte(buf.Len() >> 24), byte(buf.Len() >> 16), byte(buf.Len() >> 8), byte(buf.Len())})
	raw.Write(buf.Bytes())

	frame, err := ReadCursorFrame(&raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Fatalf("payload = %x, want %x", frame.Payload, payload)
	}
}
