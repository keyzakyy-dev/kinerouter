package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// nodeGateway wires one custom node at an upstream handler plus a combo that
// points at it.
func nodeGateway(t *testing.T, upstream http.HandlerFunc) *Gateway {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	g := New(&config.Config{DataDir: t.TempDir()}, testGateway(t).store, nil)
	node := &store.ProviderNode{
		ID: "openai-compatible-chat-test", Type: "openai-compatible",
		Data: map[string]any{"prefix": "tst", "baseUrl": srv.URL + "/v1"},
	}
	if err := g.store.SaveProviderNode(node); err != nil {
		t.Fatalf("save node: %v", err)
	}
	if err := g.store.UpsertConnection(&store.Connection{
		ID: "c1", Provider: node.ID, AuthType: "apikey", IsActive: true,
		Data: map[string]any{"apiKey": "k", "baseUrl": srv.URL + "/v1", "prefix": "tst"},
	}); err != nil {
		t.Fatalf("save connection: %v", err)
	}
	if _, err := g.store.CreateAPIKey("test", "m", "sk-test"); err != nil {
		t.Fatalf("save key: %v", err)
	}
	if _, err := g.store.SaveCombo(&store.Combo{Name: "Test Combo", Models: []string{node.ID + "/foo"}}); err != nil {
		t.Fatalf("save combo: %v", err)
	}
	return g
}

func postChat(g *Gateway, model string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	g.handleChat(rec, req, "openai")
	return rec
}

// A solo call and a combo reach the client on both the success and the failure
// path: attempts only write on success, so dispatch owns the failure response.
func TestChatWritesSoloAndComboResponses(t *testing.T) {
	g := nodeGateway(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	})
	for _, model := range []string{"openai-compatible-chat-test/foo", "tst/foo", "Test Combo"} {
		if rec := postChat(g, model); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") {
			t.Fatalf("%s: status %d body %q", model, rec.Code, rec.Body.String())
		}
	}
}

func TestChatWritesUpstreamFailure(t *testing.T) {
	for _, model := range []string{"openai-compatible-chat-test/foo", "Test Combo"} {
		g := nodeGateway(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"message":"blocked by upstream"}}`))
		})
		rec := postChat(g, model)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "blocked by upstream") {
			t.Fatalf("%s: status %d body %q", model, rec.Code, rec.Body.String())
		}
	}
}

// A combo member that names another combo is flattened to its leaves.
func TestFlattenNestedCombo(t *testing.T) {
	g := testGateway(t)
	if _, err := g.store.SaveCombo(&store.Combo{Name: "inner", Models: []string{"a/x", "b/y"}}); err != nil {
		t.Fatalf("inner: %v", err)
	}
	if _, err := g.store.SaveCombo(&store.Combo{Name: "outer", Models: []string{"c/z", "inner", "outer"}}); err != nil {
		t.Fatalf("outer: %v", err)
	}
	got := g.flattenComboModels("outer", []string{"c/z", "inner", "outer"})
	want := []string{"c/z", "a/x", "b/y", "outer"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("flatten = %v, want %v", got, want)
	}
}

// x-connection-id pins the request to that account; an id of another provider
// is ignored.
func TestChatHonoursConnectionPin(t *testing.T) {
	reply := func(text string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"` + text + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		}
	}
	first := httptest.NewServer(reply("first"))
	defer first.Close()
	second := httptest.NewServer(reply("second"))
	defer second.Close()

	g := New(&config.Config{DataDir: t.TempDir()}, testGateway(t).store, nil)
	node := &store.ProviderNode{ID: "openai-compatible-chat-test", Type: "openai-compatible",
		Data: map[string]any{"prefix": "tst", "baseUrl": first.URL + "/v1"}}
	if err := g.store.SaveProviderNode(node); err != nil {
		t.Fatalf("node: %v", err)
	}
	for id, base := range map[string]string{"c1": first.URL + "/v1", "c2": second.URL + "/v1"} {
		if err := g.store.UpsertConnection(&store.Connection{
			ID: id, Provider: node.ID, AuthType: "apikey", IsActive: true,
			Data: map[string]any{"apiKey": "k", "providerSpecificData": map[string]any{"baseUrl": base, "prefix": "tst"}},
		}); err != nil {
			t.Fatalf("conn: %v", err)
		}
	}
	if _, err := g.store.CreateAPIKey("test", "m", "sk-test"); err != nil {
		t.Fatalf("key: %v", err)
	}

	for pin, want := range map[string]string{"c2": "second", "other": "first"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
			strings.NewReader(`{"model":"tst/foo","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer sk-test")
		req.Header.Set("x-connection-id", pin)
		g.handleChat(rec, req, "openai")
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("pin %q: status %d body %q, want %q", pin, rec.Code, rec.Body.String(), want)
		}
	}
}

// A dedicated search provider reuses its chat provider's key when it has no
// connection of its own.
func TestSearchConnectionsFallback(t *testing.T) {
	g := testGateway(t)
	if err := g.store.UpsertConnection(&store.Connection{
		ID: "clinepass-1", Provider: "clinepass", AuthType: "oauth", IsActive: true,
		Data: map[string]any{"accessToken": "t"},
	}); err != nil {
		t.Fatalf("conn: %v", err)
	}
	conns := g.searchConnections("cline")
	if len(conns) != 1 || conns[0].ID != "clinepass-1" {
		t.Fatalf("fallback conns = %#v", conns)
	}
	if conns := g.searchConnections("searxng"); len(conns) != 0 {
		t.Fatalf("unexpected conns: %#v", conns)
	}
}

// A codex account whose enabledModels pin excludes the model is skipped; an
// account without the pin, or another provider, is not filtered.
func TestFilterByEnabledModels(t *testing.T) {
	pinned := &store.Connection{ID: "a", Provider: "codex", Data: map[string]any{
		"providerSpecificData": map[string]any{"enabledModels": []any{"gpt-5.2-codex"}},
	}}
	topLevel := &store.Connection{ID: "b", Provider: "codex", Data: map[string]any{
		"enabledModels": []any{"gpt-5.2-codex"},
	}}
	free := &store.Connection{ID: "c", Provider: "codex", Data: map[string]any{}}
	other := &store.Connection{ID: "d", Provider: "openai", Data: map[string]any{
		"providerSpecificData": map[string]any{"enabledModels": []any{"gpt-5.2-codex"}},
	}}
	conns := []*store.Connection{pinned, topLevel, free, other}

	keep := map[string]bool{}
	for _, c := range filterByEnabledModels("codex", conns, "gpt-5.2-codex") {
		keep[c.ID] = true
	}
	if !keep["a"] || !keep["b"] || !keep["c"] || !keep["d"] {
		t.Fatalf("all should serve gpt-5.2-codex: %v", keep)
	}
	keep = map[string]bool{}
	for _, c := range filterByEnabledModels("codex", conns, "gpt-6-sol") {
		keep[c.ID] = true
	}
	if keep["a"] || keep["b"] || !keep["c"] {
		t.Fatalf("filter = %v, want only c", keep)
	}
	if got := filterByEnabledModels("openai", conns, "gpt-6-sol"); len(got) != len(conns) {
		t.Fatalf("non-codex provider filtered: %d", len(got))
	}
}

// strictModelAssignment keeps only accounts pinned to the requested model.
func TestFilterByStrictAssignment(t *testing.T) {
	g := testGateway(t)
	pinned := &store.Connection{ID: "a", Provider: "antigravity", Data: map[string]any{"assignedModel": "gemini-3-pro"}}
	viaSpecific := &store.Connection{ID: "b", Provider: "antigravity", Data: map[string]any{
		"providerSpecificData": map[string]any{"assignedModel": "gemini-3-pro"},
	}}
	free := &store.Connection{ID: "c", Provider: "antigravity", Data: map[string]any{}}
	freebuff := &store.Connection{ID: "d", Provider: "antigravity", Data: map[string]any{"freebuffModel": "gemini-3-flash"}}
	conns := []*store.Connection{pinned, viaSpecific, free, freebuff}

	if got := g.filterByStrictAssignment("antigravity", conns, "gemini-3-pro"); len(got) != 4 {
		t.Fatalf("off by default, got %d", len(got))
	}
	if _, err := g.store.UpdateSettings(map[string]any{
		"providerStrategies": map[string]any{
			"antigravity": map[string]any{"strictModelAssignment": true},
		},
	}); err != nil {
		t.Fatalf("settings: %v", err)
	}
	ids := map[string]bool{}
	for _, c := range g.filterByStrictAssignment("antigravity", conns, "gemini-3-pro") {
		ids[c.ID] = true
	}
	if !ids["a"] || !ids["b"] || ids["c"] || ids["d"] {
		t.Fatalf("strict filter = %v", ids)
	}
	ids = map[string]bool{}
	for _, c := range g.filterByStrictAssignment("antigravity", conns, "gemini-3-flash") {
		ids[c.ID] = true
	}
	if !ids["d"] || ids["a"] || ids["b"] || ids["c"] {
		t.Fatalf("freebuff filter = %v", ids)
	}
}

// An alias may chain to another alias or name a combo, matching upstream
// resolveModel's alias-then-combo order.
func TestResolveAliasChain(t *testing.T) {
	g := testGateway(t)
	for alias, target := range map[string]string{
		"one":   "two",
		"two":   "three",
		"three": "prov/model",
		"loop":  "loop",
		"combo": "mycombo",
	} {
		if err := g.store.SetModelAlias(alias, target); err != nil {
			t.Fatalf("alias %s: %v", alias, err)
		}
	}
	for in, want := range map[string]string{
		"one":   "prov/model",
		"two":   "prov/model",
		"three": "prov/model",
		"loop":  "loop",
		"nope":  "nope",
		"combo": "mycombo",
	} {
		if got := g.resolveAliasChain(in); got != want {
			t.Errorf("resolveAliasChain(%q) = %q, want %q", in, got, want)
		}
	}
}

// A 200 with no completion fails over to the next account instead of reaching
// the client as an empty success.
func TestChatFailsOverOnEmptyCompletion(t *testing.T) {
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","choices":[]}`))
	}))
	defer empty.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"y","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer good.Close()

	g := New(&config.Config{DataDir: t.TempDir()}, testGateway(t).store, nil)
	node := &store.ProviderNode{ID: "openai-compatible-chat-test", Type: "openai-compatible",
		Data: map[string]any{"prefix": "tst", "baseUrl": empty.URL + "/v1"}}
	if err := g.store.SaveProviderNode(node); err != nil {
		t.Fatalf("node: %v", err)
	}
	// Priority decides order: the empty account is tried first.
	for id, base := range map[string]string{"a-empty": empty.URL + "/v1", "b-good": good.URL + "/v1"} {
		if err := g.store.UpsertConnection(&store.Connection{
			ID: id, Provider: node.ID, AuthType: "apikey", IsActive: true,
			Data: map[string]any{"apiKey": "k", "providerSpecificData": map[string]any{"baseUrl": base, "prefix": "tst"}},
		}); err != nil {
			t.Fatalf("conn: %v", err)
		}
	}
	if _, err := g.store.CreateAPIKey("test", "m", "sk-test"); err != nil {
		t.Fatalf("key: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"tst/foo","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer sk-test")
	g.handleChat(rec, req, "openai")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
}
