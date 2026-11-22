package gateway

// Tests for the v1 extras: model metadata lookup and the Ollama stream shim.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelInfo(t *testing.T) {
	g := adminGateway(t)
	var target string
	for _, e := range g.reg.Entries {
		if len(g.reg.Models[e.Alias]) > 0 {
			target = e.Alias + "/" + g.reg.Models[e.Alias][0].ID
			break
		}
	}
	if target == "" {
		t.Skip("no provider models in registry")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models/info?id="+target, nil)
	g.handleModelInfo(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"owned_by"`) || !strings.Contains(body, `"kind"`) {
		t.Fatalf("unexpected body: %s", body)
	}

	rec = httptest.NewRecorder()
	g.handleModelInfo(rec, httptest.NewRequest("GET", "/v1/models/info?id=no-such-provider/x", nil))
	if rec.Code != 404 {
		t.Fatalf("missing model should 404, got %d", rec.Code)
	}
}

func TestOllamaStream(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hi"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	rec := httptest.NewRecorder()
	writeOllamaStream(rec, []byte(sse), "llama3.2")
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	// finish_reason stop emits a done marker and [DONE] emits another, exactly
	// as transformToOllama does; a client stops at the first done:true.
	if len(lines) < 2 {
		t.Fatalf("want at least 2 NDJSON lines, got %d: %q", len(lines), rec.Body.String())
	}
	if !strings.Contains(lines[0], `"content":"hi"`) || !strings.Contains(lines[0], `"done":false`) {
		t.Fatalf("chunk: %s", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], `"done":true`) {
		t.Fatalf("end marker: %s", lines[len(lines)-1])
	}
}

func TestVoicesResponseLangFilter(t *testing.T) {
	voices := []ttsVoice{
		{ID: "a", Lang: "en", Name: "A"},
		{ID: "b", Lang: "vi", Name: "B"},
	}
	out := voicesResponse(voices, "vi")
	list, _ := out["voices"].([]ttsVoice)
	if len(list) != 1 || list[0].ID != "b" {
		t.Fatalf("filter: %v", out)
	}
	if _, ok := out["byLang"]; ok {
		t.Fatal("lang-filtered response must not carry byLang")
	}
}

// The debugger must only ever touch its eight fixed artifact names.
func TestTranslatorArtifactsAllowList(t *testing.T) {
	for _, n := range []string{"../../../etc/passwd", "logs/x", "1_req_client.json.bak", ""} {
		if translatorArtifacts[n] {
			t.Fatalf("%q must be rejected", n)
		}
	}
	if !translatorArtifacts["7_res_client.json"] {
		t.Fatal("canonical name rejected")
	}
}

func TestDetectClientFormat(t *testing.T) {
	cases := map[string]map[string]any{
		"claude":           {"system": "...", "messages": []any{}},
		"gemini":           {"contents": []any{}},
		"openai-responses": {"input": []any{}},
		"openai":           {"messages": []any{}},
	}
	for want, body := range cases {
		if got := detectClientFormat(body); got != want {
			t.Fatalf("%v: got %s want %s", body, got, want)
		}
	}
}
