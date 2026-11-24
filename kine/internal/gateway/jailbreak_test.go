package gateway

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/executor"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func jailbreakMux(g *Gateway) (*http.ServeMux, func(string, string, string) (int, string)) {
	mux := http.NewServeMux()
	g.registerJailbreakRoutes(mux)
	return mux, func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("x-kine-cli-token", cliTok(g.cliMachineID))
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}
}

func TestJailbreakPresetAndBindingRoutes(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	_, do := jailbreakMux(g)

	code, body := do("PUT", "/api/jailbreak/presets", `{"name":"Auditor","prompt":"You are a security reviewer."}`)
	if code != 200 || !strings.Contains(body, `"preset"`) {
		t.Fatalf("save preset: %d %s", code, body)
	}
	if code, body := do("PUT", "/api/jailbreak/models", `{"id":"p/m","enabled":true,"presetId":"auditor"}`); code != 200 {
		t.Fatalf("save binding: %d %s", code, body)
	}
	code, body = do("POST", "/api/jailbreak/preview", `{"id":"p/m","messages":[{"role":"user","content":"hi"}]}`)
	if code != 200 || !strings.Contains(body, `"injected":true`) {
		t.Fatalf("preview: %d %s", code, body)
	}
	if !strings.Contains(body, "You are a security reviewer.") {
		t.Fatalf("preview did not carry the prompt: %s", body)
	}
	// A bad rule must be rejected at the edge, not stored.
	if code, _ := do("PUT", "/api/jailbreak/presets", `{"name":"bad","prompt":"x","keywords":[{"text":"(","mode":"regex"}]}`); code != 400 {
		t.Fatalf("invalid regex keyword: %d, want 400", code)
	}
	if code, _ := do("PUT", "/api/jailbreak/presets", `{"name":"bad","prompt":"x","mode":"chat","position":"append"}`); code != 400 {
		t.Fatalf("chat+append: %d, want 400", code)
	}
}

func TestJailbreakResolutionPrecedence(t *testing.T) {
	g := testGateway(t)
	g.cliMachineID = "m1"
	s := g.store

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "wide", Name: "wide", Prompt: "wide", Keywords: []store.JailbreakKeyword{{Text: "deepseek"}}}))
	must(s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "narrow", Name: "narrow", Prompt: "narrow", Keywords: []store.JailbreakKeyword{{Text: "deepseek-v4"}}}))
	must(s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "exact", Name: "exact", Prompt: "exact"}))
	must(s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "scan", Name: "scan", Prompt: "scan"}))

	// Longest keyword wins on a bare model id.
	match, doc := s.ResolveJailbreak("", "deepseek-v4-pro")
	if doc == nil || doc.ID != "narrow" {
		t.Fatalf("keyword resolution = %+v (%+v), want narrow", match, doc)
	}
	// A word keyword must not match inside a token.
	if _, doc := s.ResolveJailbreak("", "mydeepseekia"); doc != nil {
		t.Fatalf("word keyword matched inside a token: %+v", doc)
	}
	// An exact binding outranks every keyword.
	must(s.SetJailbreakBinding("deepseek-v4-pro", "exact", true))
	if _, doc := s.ResolveJailbreak("", "deepseek-v4-pro"); doc == nil || doc.ID != "exact" {
		t.Fatalf("exact binding lost to a keyword: %+v", doc)
	}
	// A key-scoped binding outranks the global binding.
	must(s.SetJailbreakBinding("key-1:deepseek-v4-pro", "scan", true))
	if _, doc := s.ResolveJailbreak("key-1", "deepseek-v4-pro"); doc == nil || doc.ID != "scan" {
		t.Fatalf("key binding did not outrank global: %+v", doc)
	}
	// The global exclude list is absolute.
	must(s.SaveJailbreakExcludes([]store.JailbreakKeyword{{Text: "deepseek-v4-pro"}}))
	if match, doc := s.ResolveJailbreak("key-1", "deepseek-v4-pro"); doc != nil {
		t.Fatalf("global exclude did not win: %+v %+v", match, doc)
	}
}

func TestJailbreakInjectionShapes(t *testing.T) {
	g := testGateway(t)
	s := g.store
	if err := s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "sys", Prompt: "SYS"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJailbreakBinding("p/m", "sys", true); err != nil {
		t.Fatal(err)
	}

	raw := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	g.applyJailbreak(raw, "", []string{"p/m"}, "openai")
	msgs := raw["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("prepend shape: %#v", msgs)
	}

	// Claude's top-level system field is prepended, not replaced.
	raw = map[string]any{"system": "operator", "messages": []any{}}
	g.applyJailbreak(raw, "", []string{"p/m"}, "claude")
	if got := fmt.Sprint(raw["system"]); !strings.HasPrefix(got, "SYS\n\noperator") {
		t.Fatalf("claude system field: %q", got)
	}

	// Chat mode appends a user turn instead of a system one.
	if err := s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "chat", Prompt: "CHAT", Mode: "chat"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJailbreakBinding("p/m", "chat", true); err != nil {
		t.Fatal(err)
	}
	raw = map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	g.applyJailbreak(raw, "", []string{"p/m"}, "openai")
	msgs = raw["messages"].([]any)
	if len(msgs) != 2 || msgs[1].(map[string]any)["role"] != "user" {
		t.Fatalf("chat shape: %#v", msgs)
	}

	// Gemini safety switch rides along as safetySettings.
	if err := s.SaveJailbreakPreset(&store.JailbreakPreset{ID: "safe", Prompt: "SAFE", GeminiSafety: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJailbreakBinding("p/m", "safe", true); err != nil {
		t.Fatal(err)
	}
	raw = map[string]any{"messages": []any{}}
	g.applyJailbreak(raw, "", []string{"p/m"}, "gemini")
	if _, ok := raw["safetySettings"]; !ok {
		t.Fatal("geminiSafety did not set safetySettings")
	}
}

func TestJailbreakUsageCounter(t *testing.T) {
	g := testGateway(t)
	if err := g.store.SaveJailbreakPreset(&store.JailbreakPreset{ID: "p", Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetJailbreakBinding("m", "p", true); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{"messages": []any{}}
	g.applyJailbreak(raw, "", []string{"m"}, "openai")
	g.applyJailbreak(raw, "", []string{"m"}, "openai")
	usage, err := g.store.JailbreakUsage()
	if err != nil {
		t.Fatal(err)
	}
	if usage["p"] != 2 {
		t.Fatalf("usage = %v, want 2 for p", usage)
	}
}

// End-to-end: a chat request whose model is armed to a preset reaches the
// upstream with the prompt already prepended, and nothing when it is not armed.
func TestJailbreakReachesUpstreamBody(t *testing.T) {
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"x","object":"chat.completion","created":1,"model":"m1","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	g := testGateway(t)
	g.cliMachineID = "m1"
	key, err := g.store.CreateAPIKey("test", "m1", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	conn := &store.Connection{Provider: "openrouter", AuthType: "apikey", IsActive: true,
		Data: map[string]any{"apiKey": "up", "baseUrl": upstream.URL}}
	if err := g.store.UpsertConnection(conn); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SaveJailbreakPreset(&store.JailbreakPreset{ID: "persona", Name: "persona", Prompt: "PERSONA-PROMPT"}); err != nil {
		t.Fatal(err)
	}

	g.static = http.NotFoundHandler()
	g.maxBody = 1 << 20
	g.reg = providerdata.Get()
	g.exec = executor.New()
	g.pricing = loadPricing(g.store)
	handler := g.Handler()
	send := func() map[string]any {
		t.Helper()
		gotBody = nil
		body := `{"model":"openrouter/m1","messages":[{"role":"user","content":"hi"}]}`
		req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer sk-test")
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != 200 {
			t.Fatalf("chat: %d %s", rr.Code, rr.Body.String())
		}
		if gotBody == nil {
			t.Fatal("upstream saw no body")
		}
		return gotBody
	}

	// Unarmed means untouched.
	if b := send(); len(b["messages"].([]any)) != 1 {
		t.Fatalf("injection happened while nothing was armed: %#v", b["messages"])
	}

	if err := g.store.SetJailbreakBinding("openrouter/m1", "persona", true); err != nil {
		t.Fatal(err)
	}
	b := send()
	msgs := b["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["content"] != "PERSONA-PROMPT" {
		t.Fatalf("armed request body: %#v", msgs)
	}
	if usage, _ := g.store.JailbreakUsage(); usage["persona"] != 1 {
		t.Fatalf("usage = %v, want 1", usage)
	}

	// A key-scoped binding outranks the global one on the real path.
	if err := g.store.SaveJailbreakPreset(&store.JailbreakPreset{ID: "keyonly", Prompt: "KEY-PROMPT"}); err != nil {
		t.Fatal(err)
	}
	if err := g.store.SetJailbreakBinding(key.ID+":openrouter/m1", "keyonly", true); err != nil {
		t.Fatal(err)
	}
	msgs = send()["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["content"] != "KEY-PROMPT" {
		t.Fatalf("key-scoped binding body: %#v", msgs)
	}
}
