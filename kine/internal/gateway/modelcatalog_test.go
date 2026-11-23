package gateway

import (
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// Custom model rows are advertised under the node's user prefix, and a
// disabled model is not offered at all.
func TestBuildModelsListIncludesCustomModels(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	id := "openai-compatible-chat-test"
	if err := g.store.SaveProviderNode(&store.ProviderNode{
		ID: id, Type: "openai-compatible", Name: "Test",
		Data: map[string]any{"prefix": "tst", "baseUrl": "https://example.com"},
	}); err != nil {
		t.Fatalf("node: %v", err)
	}
	if err := g.store.UpsertConnection(&store.Connection{
		ID: "c1", Provider: id, AuthType: "apikey", IsActive: true,
		Data: map[string]any{"apiKey": "k", "baseUrl": "https://example.com", "prefix": "tst"},
	}); err != nil {
		t.Fatalf("connection: %v", err)
	}
	for _, model := range []string{"model-a", "model-b"} {
		if _, err := g.store.AddCustomModel(id, model, "llm", model, nil, ""); err != nil {
			t.Fatalf("custom model: %v", err)
		}
	}
	if err := g.store.DisableModels(id, []string{"model-b"}); err != nil {
		t.Fatalf("disable: %v", err)
	}

	ids := map[string]bool{}
	for _, m := range g.buildModelsList(nil) {
		ids[m["id"].(string)] = true
	}
	if !ids["tst/model-a"] {
		t.Errorf("custom model missing from list: %v", ids)
	}
	if ids["tst/model-b"] {
		t.Errorf("disabled custom model listed")
	}
	if ids["tst/model-a"] && ids["tst/model-b"] {
		t.Errorf("unexpected: both listed")
	}
}

// A custom row whose provider has no active connection is an orphan and must
// not be offered.
func TestBuildModelsListSkipsOrphanCustomModel(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	if _, err := g.store.AddCustomModel("gone", "ghost", "llm", "ghost", nil, ""); err != nil {
		t.Fatalf("custom model: %v", err)
	}
	for _, m := range g.buildModelsList(nil) {
		if m["id"] == "gone/ghost" {
			t.Fatalf("orphan custom model listed")
		}
	}
}
