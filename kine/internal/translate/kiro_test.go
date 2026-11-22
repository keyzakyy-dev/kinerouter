package translate

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"strings"
	"testing"
)

// buildKiroFrame encodes one AWS EventStream frame: headers then JSON payload.
func buildKiroFrame(headers map[string]string, payload map[string]any) []byte {
	var hb bytes.Buffer
	for k, v := range headers {
		hb.WriteByte(byte(len(k)))
		hb.WriteString(k)
		hb.WriteByte(7) // string
		binary.Write(&hb, binary.BigEndian, uint16(len(v)))
		hb.WriteString(v)
	}
	pb, _ := json.Marshal(payload)
	total := 12 + hb.Len() + len(pb) + 4
	out := make([]byte, 12)
	binary.BigEndian.PutUint32(out[0:4], uint32(total))
	binary.BigEndian.PutUint32(out[4:8], uint32(hb.Len()))
	binary.BigEndian.PutUint32(out[8:12], crc32.Checksum(out[0:8], eventStreamCRC))
	out = append(out, hb.Bytes()...)
	out = append(out, pb...)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.Checksum(out, eventStreamCRC))
	return append(out, crc[:]...)
}

func TestKiroFrameRoundTrip(t *testing.T) {
	frame := buildKiroFrame(map[string]string{":event-type": "assistantResponseEvent", ":message-type": "event"},
		map[string]any{"content": "hi"})
	got, err := parseEventFrame(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Headers[":event-type"] != "assistantResponseEvent" {
		t.Fatalf("headers: %+v", got.Headers)
	}
	if got.Payload["content"] != "hi" {
		t.Fatalf("payload: %+v", got.Payload)
	}

	// A flipped payload byte must fail the message CRC, not decode silently.
	bad := append([]byte{}, frame...)
	bad[len(bad)-5] ^= 0xff
	if _, err := parseEventFrame(bad); err == nil {
		t.Fatal("corrupt frame decoded without error")
	}
}

func TestKiroStreamChunks(t *testing.T) {
	k := NewKiroToOpenAI("claude-sonnet-4.5", map[string]string{"mcp_a_b": "mcp__a__b"})
	var out []map[string]any
	for _, f := range []eventFrame{
		{Headers: map[string]any{":event-type": "assistantResponseEvent"}, Payload: map[string]any{"content": "he"}},
		{Headers: map[string]any{":event-type": "assistantResponseEvent"}, Payload: map[string]any{"content": "llo"}},
		{Headers: map[string]any{":event-type": "toolUseEvent"}, Payload: map[string]any{
			"toolUseEvent": map[string]any{"toolUseId": "t1", "name": "mcp_a_b", "input": map[string]any{"x": 1}},
		}},
		{Headers: map[string]any{":event-type": "messageStopEvent"}, Payload: map[string]any{}},
	} {
		out = append(out, k.Transform(f)...)
	}
	body := CollectKiroEvents(strings.NewReader(""), "m", nil) // empty body still yields one completion
	if body["object"] != "chat.completion" {
		t.Fatalf("object: %v", body["object"])
	}
	msg := body["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	_ = msg

	coll := CollectKiroEvents(bytes.NewReader(collectFrames()), "m", map[string]string{"mcp_a_b": "mcp__a__b"})
	choice := coll["choices"].([]any)[0].(map[string]any)
	m := choice["message"].(map[string]any)
	if m["content"] != "hello" {
		t.Fatalf("content: %v", m["content"])
	}
	calls, _ := m["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls: %v", calls)
	}
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "mcp__a__b" {
		t.Fatalf("tool name was not restored: %v", fn["name"])
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish: %v", choice["finish_reason"])
	}
}

func collectFrames() []byte {
	var b []byte
	b = append(b, buildKiroFrame(map[string]string{":event-type": "assistantResponseEvent"}, map[string]any{"content": "hel"})...)
	b = append(b, buildKiroFrame(map[string]string{":event-type": "assistantResponseEvent"}, map[string]any{"content": "lo"})...)
	b = append(b, buildKiroFrame(map[string]string{":event-type": "toolUseEvent"}, map[string]any{
		"toolUseEvent": map[string]any{"toolUseId": "t1", "name": "mcp_a_b", "input": map[string]any{"x": 1}},
	})...)
	b = append(b, buildKiroFrame(map[string]string{":event-type": "messageStopEvent"}, map[string]any{})...)
	return b
}

func TestToKiroPayloadShape(t *testing.T) {
	raw := map[string]any{
		"messages": []any{
			map[string]any{"role": "system", "content": "be brief"},
			map[string]any{"role": "user", "content": "hi"},
			map[string]any{"role": "assistant", "content": "yo"},
			map[string]any{"role": "user", "content": "again"},
		},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "mcp.a/b", "description": "d", "parameters": map[string]any{"type": "object"},
		}}},
	}
	payload, restore, ok := ToKiro(raw, "claude-sonnet-4-5-20250929", KiroOptions{ProfileARN: "arn:x", SessionID: "s1"})
	if !ok {
		t.Fatal("conversion failed")
	}
	if payload["profileArn"] != "arn:x" {
		t.Fatalf("profileArn: %v", payload["profileArn"])
	}
	cs, _ := payload["conversationState"].(map[string]any)
	if cs["chatTriggerType"] != "MANUAL" {
		t.Fatalf("chatTriggerType: %v", cs["chatTriggerType"])
	}
	cur, _ := cs["currentMessage"].(map[string]any)
	uim, _ := cur["userInputMessage"].(map[string]any)
	content, _ := uim["content"].(string)
	if content != "again" {
		t.Fatalf("current turn: %q", content)
	}
	// The system prompt rides inside the *first* user turn, never top-level.
	first := cs["history"].([]map[string]any)[0]["userInputMessage"].(map[string]any)
	firstContent, _ := first["content"].(string)
	if !strings.Contains(firstContent, "be brief") {
		t.Fatalf("system prompt must ride in the first user turn: %q", firstContent)
	}
	if uim["modelId"] != "claude-sonnet-4.5" {
		t.Fatalf("modelId: %v", uim["modelId"])
	}
	if restore["mcp_a_b"] != "mcp.a/b" {
		t.Fatalf("restore map: %v", restore)
	}
	if strings.Contains(firstContent, "systemPrompt") {
		t.Fatal("top-level systemPrompt is rejected by Kiro")
	}
}

func TestKiroProfileARN(t *testing.T) {
	for _, m := range []string{"api_key", "idc", "external_idp"} {
		if got := KiroProfileARN(m, ""); got != "" {
			t.Fatalf("%s must not fall back to a shared ARN, got %q", m, got)
		}
	}
	if got := KiroProfileARN("api_key", "arn:conn"); got != "arn:conn" {
		t.Fatalf("connection ARN must win: %q", got)
	}
}
