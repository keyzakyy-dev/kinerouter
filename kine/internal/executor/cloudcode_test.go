package executor

import (
	"encoding/json"
	"strings"
	"testing"
)

// Cloud Code wrap: the Gemini body survives, project/model ride on top, and an
// empty projectId must not reach Google as "".
func TestWrapCloudCode(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	out, err := wrapCloudCode("gemini-cli", "gemini-2.5-pro", body, Credentials{ProjectID: "proj-1", SessionID: "sess-1"})
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	if env["project"] != "proj-1" || env["model"] != "gemini-2.5-pro" || env["userAgent"] != "gemini-cli" {
		t.Fatalf("envelope wrong: %v", env)
	}
	req, _ := env["request"].(map[string]any)
	if req["sessionId"] != "sess-1" || req["contents"] == nil {
		t.Fatalf("request body lost: %v", req)
	}

	// antigravity: no safetySettings, /v1internal base gets the suffix once.
	ant, err := wrapCloudCode("antigravity", "claude-sonnet-4", body, Credentials{ProjectID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ant), "safetySettings") || strings.Contains(string(ant), `"userAgent":"gemini-cli"`) {
		t.Fatalf("antigravity envelope kept gemini-cli fields: %s", ant)
	}
	if u := cloudCodeURL("https://daily-cloudcode-pa.googleapis.com", "antigravity", true); u !=
		"https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse" {
		t.Fatalf("antigravity url wrong: %s", u)
	}
	if u := cloudCodeURL("https://cloudcode-pa.googleapis.com/v1internal", "gemini-cli", false); u !=
		"https://cloudcode-pa.googleapis.com/v1internal:generateContent" {
		t.Fatalf("gemini-cli url wrong: %s", u)
	}
}
