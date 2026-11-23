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
