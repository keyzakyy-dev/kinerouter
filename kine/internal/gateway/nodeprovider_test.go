package gateway

import (
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// A custom node routes by its user prefix and by its stored id (custom models
// and the dashboard's model test use the id as the provider alias).
func TestNodeByPrefixMatchesIDAndPrefix(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	n := &store.ProviderNode{
		ID:   "openai-compatible-chat-test",
		Type: "openai-compatible",
		Name: "Test",
		Data: map[string]any{"prefix": "tst", "baseUrl": "https://example.com/v1"},
	}
	if err := g.store.SaveProviderNode(n); err != nil {
		t.Fatalf("save node: %v", err)
	}
	if got := g.nodeByPrefix(n.ID); got == nil || got.ID != n.ID {
		t.Fatalf("id lookup: %#v", got)
	}
	if got := g.nodeByPrefix("tst"); got == nil || got.ID != n.ID {
		t.Fatalf("prefix lookup: %#v", got)
	}
	if got := g.nodeByPrefix("codex"); got != nil {
		t.Fatalf("registry id must not resolve as a node: %#v", got)
	}
}
