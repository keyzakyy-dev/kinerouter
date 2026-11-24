package executor

import (
	"encoding/json"
	"testing"
)

func TestZcodeShapeBody(t *testing.T) {
	in := []byte(`{"model":"glm-5.3","system":"be nice","messages":[]}`)
	out, err := zcodeShapeBody(in, "session_abc", "dev-1", "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	sys, _ := m["system"].([]any)
	if len(sys) != 3 {
		t.Fatalf("system blocks = %d, want 3", len(sys))
	}
	first, _ := sys[0].(map[string]any)
	if first["text"] != zcodeID.CliPrefix {
		t.Fatalf("block 0 is not the CLI prefix")
	}
	if m["max_tokens"] != float64(zcodeDefaultMaxTokens) {
		t.Fatalf("max_tokens = %v", m["max_tokens"])
	}
	meta, _ := m["metadata"].(map[string]any)
	if meta["user_id"] == "" {
		t.Fatal("metadata.user_id missing")
	}
}

func TestFreePiSessionIDStable(t *testing.T) {
	if freePiSessionID("tok") != freePiSessionID("tok") {
		t.Fatal("session id not deterministic per token")
	}
	if freePiSessionID("tok") == freePiSessionID("other") {
		t.Fatal("two tokens share a session id")
	}
}

func TestFreebuffShape(t *testing.T) {
	body := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	if err := freebuffShape(body, "run-1", freebuffSession{InstanceID: "cli:x"}, "fp"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := body["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["content"] != freebuffMarker {
		t.Fatal("marker system message not prepended")
	}
	meta, _ := body["codebuff_metadata"].(map[string]any)
	if meta["run_id"] != "run-1" || meta["freebuff_instance_id"] != "cli:x" {
		t.Fatalf("metadata = %v", meta)
	}
}
