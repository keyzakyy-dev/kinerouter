package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

// /v1/models/{kind} (the SDK form without /api) must resolve the kind slug
// instead of treating the whole path as a model id.
func TestModelsByPathKindSlugWithoutAPIPrefix(t *testing.T) {
	g := testGateway(t)
	g.reg = providerdata.Get()
	if err := g.store.UpsertConnection(&store.Connection{
		ID: "c1", Provider: "codex", AuthType: "oauth", IsActive: true,
		Data: map[string]any{"accessToken": "t"},
	}); err != nil {
		t.Fatalf("conn: %v", err)
	}
	for _, path := range []string{"/v1/models/llm", "/api/v1/models/llm"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetPathValue("path", strings.TrimPrefix(strings.TrimPrefix(path, "/api"), "/v1/models/"))
		g.handleModelsByPath(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"object":"list"`) {
			t.Errorf("%s: status %d body %q", path, rec.Code, rec.Body.String())
		}
	}
}

// The composer's /api/models list also publishes custom node models.
func TestHandleListModelsIncludesCustomModels(t *testing.T) {
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
		Data: map[string]any{"apiKey": "k", "prefix": "tst"},
	}); err != nil {
		t.Fatalf("conn: %v", err)
	}
	if _, err := g.store.AddCustomModel(id, "custom-a", "llm", "custom-a", nil, ""); err != nil {
		t.Fatalf("custom model: %v", err)
	}
	rec := httptest.NewRecorder()
	g.handleListModels(rec, httptest.NewRequest(http.MethodGet, "/api/models", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"tst/custom-a"`) {
		t.Fatalf("status %d body %.400s", rec.Code, rec.Body.String())
	}
}
