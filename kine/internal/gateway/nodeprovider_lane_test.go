package gateway

import (
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// A node baseUrl is a prefix: bare hosts gain /v1, paths gain only the lane,
// and a base that already names a lane is left alone.
func TestNodeEndpointLanes(t *testing.T) {
	g := testGateway(t)
	tests := []struct {
		name     string
		provider string
		apiType  string
		base     string
		want     string
	}{
		{"bare host gains v1", "openai-compatible-chat-x", "chat", "https://api.example.com", "https://api.example.com/v1/chat/completions"},
		{"path gains only the lane", "openai-compatible-chat-x", "chat", "https://developer.amd.com.cn/radeon/api/v1", "https://developer.amd.com.cn/radeon/api/v1/chat/completions"},
		{"trailing slash is not doubled", "openai-compatible-chat-x", "chat", "https://api.example.com/v1/", "https://api.example.com/v1/chat/completions"},
		{"stored chat route is kept", "openai-compatible-chat-x", "chat", "https://api.example.com/v1/chat/completions", "https://api.example.com/v1/chat/completions"},
		{"stored completions route is kept", "openai-compatible-chat-x", "chat", "https://api.example.com/v1/completions", "https://api.example.com/v1/completions"},
		{"ollama chat route is kept", "openai-compatible-chat-x", "chat", "http://192.168.1.10:11434/api/chat", "http://192.168.1.10:11434/api/chat"},
		{"responses apiType", "openai-compatible-responses-x", "responses", "https://api.example.com/v1", "https://api.example.com/v1/responses"},
		{"responses from the id", "openai-compatible-responses-x", "", "https://api.example.com/v1", "https://api.example.com/v1/responses"},
		{"anthropic messages", "anthropic-compatible-x", "", "https://api.example.com/proxy/v1", "https://api.example.com/proxy/v1/messages"},
		{"anthropic stored messages kept", "anthropic-compatible-x", "", "https://api.example.com/v1/messages", "https://api.example.com/v1/messages"},
		{"embedding node chats", "custom-embedding-x", "", "https://api.example.com/v1", "https://api.example.com/v1/chat/completions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := map[string]any{"baseUrl": tt.base}
			if tt.apiType != "" {
				sp["apiType"] = tt.apiType
			}
			got, err := g.nodeEndpoint(tt.provider, sp)
			if err != nil || got != tt.want {
				t.Fatalf("nodeEndpoint = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

func TestNodeEmbeddingURL(t *testing.T) {
	g := testGateway(t)
	tests := []struct{ provider, base, want string }{
		{"custom-embedding-x", "https://api.example.com", "https://api.example.com/v1/embeddings"},
		{"custom-embedding-x", "https://api.example.com/v1", "https://api.example.com/v1/embeddings"},
		{"custom-embedding-x", "https://api.example.com/v1/embeddings", "https://api.example.com/v1/embeddings"},
	}
	for _, tt := range tests {
		if got := g.nodeEmbeddingURL(tt.provider, map[string]any{"baseUrl": tt.base}); got != tt.want {
			t.Errorf("nodeEmbeddingURL(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}

// The node's own routing prefix must not reach the upstream gateway.
func TestNodeWireModelStripsPrefix(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	id := "openai-compatible-chat-test"
	if err := g.store.SaveProviderNode(&store.ProviderNode{
		ID: id, Type: "openai-compatible", Name: "Test",
		Data: map[string]any{"prefix": "tst", "baseUrl": "https://example.com"},
	}); err != nil {
		t.Fatalf("save node: %v", err)
	}
	for model, want := range map[string]string{
		"tst/foo":   "foo",
		"foo":       "foo",
		"tstx/foo":  "tstx/foo",
		"other/foo": "other/foo",
	} {
		if got := g.nodeWireModel(id, model); got != want {
			t.Errorf("nodeWireModel(%q) = %q, want %q", model, got, want)
		}
	}
}

// A model alias may point at a custom node prefix or node id, not only at a
// registry provider.
func TestResolveTargetsModelAliasToNode(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	id := "openai-compatible-chat-test"
	if err := g.store.SaveProviderNode(&store.ProviderNode{
		ID: id, Type: "openai-compatible", Name: "Test",
		Data: map[string]any{"prefix": "tst", "baseUrl": "https://example.com"},
	}); err != nil {
		t.Fatalf("save node: %v", err)
	}
	if err := g.store.SetModelAlias("fast", "tst/tst/foo"); err != nil {
		t.Fatalf("alias: %v", err)
	}
	got, err := g.resolveTargets("fast")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 || got[0].Provider != id || got[0].Model != "foo" {
		t.Fatalf("targets = %#v", got)
	}
}

// A custom node prefix wins over a registry alias of the same name, matching
// resolvePrefixProvider running before alias resolution.
func TestNodePrefixShadowsRegistryAlias(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	id := "openai-compatible-chat-oa"
	if err := g.store.SaveProviderNode(&store.ProviderNode{
		ID: id, Type: "openai-compatible", Name: "Custom OA",
		Data: map[string]any{"prefix": "oa", "baseUrl": "https://example.com"},
	}); err != nil {
		t.Fatalf("save node: %v", err)
	}
	got, err := g.resolveTargets("oa/some-model")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(got) != 1 || got[0].Provider != id || got[0].Model != "some-model" {
		t.Fatalf("targets = %#v", got)
	}
}

func TestInferProviderPrefersConnectedLane(t *testing.T) {
	g := testGateway(t)
	if err := g.store.UpsertConnection(&store.Connection{ID: "c1", Provider: "openai", AuthType: "apikey", IsActive: true, Data: map[string]any{"apiKey": "k"}}); err != nil {
		t.Fatalf("openai conn: %v", err)
	}
	if err := g.store.UpsertConnection(&store.Connection{ID: "c2", Provider: "claude", AuthType: "oauth", IsActive: true, Data: map[string]any{"accessToken": "t"}}); err != nil {
		t.Fatalf("claude conn: %v", err)
	}
	for model, want := range map[string]string{
		"gpt-4o":              "openai",
		"gpt-5.2-codex":       "codex",
		"claude-sonnet-4-6":   "claude",
		"some-deepseek-model": "openai",
	} {
		if got := g.inferProvider(model); got != want {
			t.Errorf("inferProvider(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestNodeModelsURL(t *testing.T) {
	g := testGateway(t)
	tests := []struct{ provider, base, want string }{
		{"openai-compatible-chat-x", "https://api.example.com", "https://api.example.com/v1/models"},
		{"openai-compatible-chat-x", "https://api.example.com/v1", "https://api.example.com/v1/models"},
		{"openai-compatible-chat-x", "https://api.example.com/v1/chat/completions", "https://api.example.com/v1/models"},
		{"anthropic-compatible-x", "https://api.example.com/v1", "https://api.example.com/v1/models"},
		{"anthropic-compatible-x", "https://api.example.com/v1/messages", "https://api.example.com/v1/models"},
	}
	for _, tt := range tests {
		if got := g.nodeModelsURL(tt.provider, map[string]any{"baseUrl": tt.base}); got != tt.want {
			t.Errorf("nodeModelsURL(%q, %q) = %q, want %q", tt.provider, tt.base, got, tt.want)
		}
	}
}

func TestStripContextMarker(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5[1m]":   "claude-opus-5",
		"claude-opus-5[1M]":   "claude-opus-5",
		"claude-opus-5[beta]": "claude-opus-5[beta]",
		"claude-opus-5":       "claude-opus-5",
		"weird[1m]suffix[1m]": "weird[1m]suffix",
	} {
		if got := stripContextMarker(in); got != want {
			t.Errorf("stripContextMarker(%q) = %q, want %q", in, got, want)
		}
	}
}
