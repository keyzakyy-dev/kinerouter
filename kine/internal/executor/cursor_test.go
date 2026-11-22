package executor

import (
	"strings"
	"testing"
)

func TestCursorChecksumShape(t *testing.T) {
	a := cursorChecksum("machine-1")
	b := cursorChecksum("machine-1")
	if a != b {
		t.Fatal("checksum must be deterministic for a machine id")
	}
	if !strings.HasSuffix(a, "machine-1") {
		t.Fatalf("checksum must end with the machine id: %q", a)
	}
	body := strings.TrimSuffix(a, "machine-1")
	if strings.ContainsAny(body, "=+/") {
		t.Fatalf("checksum body must be URL-safe base64 without padding: %q", body)
	}
	// 6 timestamp bytes + overhead of the Jyh pass always yields >= 8 chars.
	if len(body) < 8 {
		t.Fatalf("checksum body too short: %q", body)
	}
}

func TestCursorSessionIDIsV5(t *testing.T) {
	if got := cursorSessionID("token"); len(got) != 36 || got[14] != '5' {
		t.Fatalf("session id must be a v5 UUID: %q", got)
	}
	if cursorSessionID("token") != cursorSessionID("token") {
		t.Fatal("session id must be derived, not random")
	}
}

func TestCursorHeadersFailClosed(t *testing.T) {
	if _, err := CursorHeaders(Credentials{AccessToken: "t"}); err == nil {
		t.Fatal("missing machine id must fail closed")
	}
	creds := Credentials{AccessToken: "prefix::secret", MachineID: "m1"}
	h, err := CursorHeaders(creds)
	if err != nil {
		t.Fatal(err)
	}
	// Lowercase keys: http.Header.Set canonicalises, a map does not.
	if h["authorization"] != "Bearer secret" {
		t.Fatalf("token prefix must be stripped: %q", h["authorization"])
	}
	if h["Content-Type"] != "application/connect+proto" {
		t.Fatalf("content type = %q", h["Content-Type"])
	}
	// x-client-key is sha256 of the *clean* token.
	if h["x-client-key"] != cursorHash("secret") {
		t.Fatal("x-client-key must hash the clean token")
	}
	if !strings.HasSuffix(h["x-cursor-checksum"], "m1") {
		t.Fatal("checksum must carry the machine id")
	}
	if h["connect-protocol-version"] != "1" || !strings.HasPrefix(h["User-Agent"], "connect-es/") {
		t.Fatalf("ConnectRPC headers missing: %#v", h)
	}
}

func TestCursorURLBothSurfaces(t *testing.T) {
	legacy, err := CursorURL(false)
	if err != nil || !strings.Contains(legacy, "/aiserver.v1.ChatService/StreamUnifiedChatWithTools") {
		t.Fatalf("legacy url = %q (%v)", legacy, err)
	}
	agent, err := CursorURL(true)
	if err != nil || !strings.HasSuffix(agent, "/agent.v1.AgentService/Run") {
		t.Fatalf("agent url = %q (%v)", agent, err)
	}
}
