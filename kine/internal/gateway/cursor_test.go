package gateway

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

func TestPrepareCursorRequestSurfaceChoice(t *testing.T) {
	text := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": "hi"},
		map[string]any{"role": "assistant", "content": nil},
	}}
	if !prepareCursorRequest(text, "claude-4.5", false).agent {
		t.Fatal("text-only turn should use the agent surface")
	}
	if prepareCursorRequest(text, "claude-4.5", true).agent != true {
		t.Fatal("a text turn stays on the agent surface")
	}
	img := map[string]any{"messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "what is this"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AA"}},
	}}}}
	plan := prepareCursorRequest(img, "claude-4.5", true)
	if plan.agent {
		t.Fatal("image parts must stay on the legacy protobuf surface")
	}
	// Body bytes carry random ids, so only the framing and mode are asserted.
	if plan.body[0] != 0x00 || binary.BigEndian.Uint32(plan.body[1:]) != uint32(len(plan.body)-5) {
		t.Fatal("plan body must be one legacy ConnectRPC frame")
	}
	if !bytes.Contains(plan.body, []byte("Agent")) {
		t.Fatal("a claude-code UA must set the legacy unified mode to Agent")
	}
	if prepareCursorRequest(map[string]any{}, "claude-4.5", false).agent {
		t.Fatal("an empty message list cannot use the agent surface")
	}
}

// cursorFrame builds one uncompressed ConnectRPC frame.
func cursorFrame(payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(out[1:], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

func TestStreamCursorLegacyEmitsOpenAIChunks(t *testing.T) {
	resp := func(text string) []byte { return cursorFrame(pbBytesGW(2, pbStringGW(1, text))) }
	upstream := bytes.NewReader(append(resp("hel"), resp("lo")...))

	conv := newCursorConv("claude-4.5", translate.FormatOpenAI, nil, nil)
	if err := conv.runLegacy(upstream); err != nil {
		t.Fatal(err)
	}
	conv.finish()
	var content string
	var finish any
	toolCalls := 0
	for _, ch := range conv.sink {
		choice := ch["choices"].([]any)[0].(map[string]any)
		delta := choice["delta"].(map[string]any)
		if s, ok := delta["content"].(string); ok {
			content += s
		}
		if _, ok := delta["tool_calls"]; ok {
			toolCalls++
		}
		finish = choice["finish_reason"]
	}
	if content != "hello" {
		t.Fatalf("content = %q", content)
	}
	if finish != "stop" || toolCalls != 0 {
		t.Fatalf("finish = %v, toolCalls = %d", finish, toolCalls)
	}
}

func TestCollectCursorLegacyFold(t *testing.T) {
	call := pbConcatGW(pbStringGW(3, "call-1"), pbStringGW(9, "Write"), pbStringGW(10, `{"a":1}`), pbUintGW(11, 1))
	upstream := bytes.NewReader(cursorFrame(pbBytesGW(1, call)))

	out, err := collectCursor(upstream, nil, "claude-4.5")
	if err != nil {
		t.Fatal(err)
	}
	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	tcs := msg["tool_calls"].([]map[string]any)
	if len(tcs) != 1 || tcs[0]["id"] != "call-1" {
		t.Fatalf("tool calls = %#v", tcs)
	}
}

func TestStreamCursorLegacySurfacesUpstreamError(t *testing.T) {
	upstream := bytes.NewReader(cursorFrame([]byte(`{"error":{"message":"quota exhausted"}}`)))
	conv := newCursorConv("claude-4.5", translate.FormatOpenAI, nil, nil)
	err := conv.runLegacy(upstream)
	if err == nil || err.Error() != "cursor: quota exhausted" {
		t.Fatalf("err = %v", err)
	}
}

// Small protobuf helpers so the test does not import unexported codec names.
func pbVarintGW(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
func pbStringGW(num int, s string) []byte {
	return append(pbVarintGW(uint64(num)<<3|2), append(pbVarintGW(uint64(len(s))), s...)...)
}
func pbBytesGW(num int, b []byte) []byte {
	return append(pbVarintGW(uint64(num)<<3|2), append(pbVarintGW(uint64(len(b))), b...)...)
}
func pbUintGW(num int, v uint64) []byte {
	return append(pbVarintGW(uint64(num)<<3), pbVarintGW(v)...)
}
func pbConcatGW(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
