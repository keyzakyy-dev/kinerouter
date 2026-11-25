package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/config"
	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
)

// These tests pin the per-tool write shape to 9router-go's clientconfig
// adapters (internal/clientconfig/{codex,opencode,droid,crush_pi_omp}.go):
// paths, keys, the resolved context window, and what Reset is allowed to touch.

// resolvedModel is a registry model whose ContextLength is declared, so the
// window a writer must state is a known number rather than a default.
const (
	resolvedModel    = "kiro/gpt-5.6-sol"
	resolvedWindow   = int64(272000)
	resolvedCompact  = resolvedWindow - resolvedWindow/10
	unresolvedModel  = "no-such-provider/model-x"
	piDefaultWindow  = int64(128000)
	piDefaultMaxTok  = int64(16384)
	crushDefaultWind = int64(128000)
)

func testGatewayLimits(t *testing.T) *Gateway {
	t.Helper()
	g := testGateway(t)
	g.cfg = &config.Config{DataDir: t.TempDir()}
	g.reg = providerdata.Get()
	if w := g.contextWindowFor(resolvedModel); w != resolvedWindow {
		t.Fatalf("test model window: got %d want %d", w, resolvedWindow)
	}
	return g
}

// tomlInt reads a go-toml integer (int64) out of a decoded map.
func tomlInt(v any) int64 {
	if n, ok := v.(int64); ok {
		return n
	}
	return 0
}

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Several tools resolve their config through XDG_CONFIG_HOME, which some CI
	// runners export. Leaving it pointing at the real home would make the writer
	// and the assertion below disagree about where the file went.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// jsonList reads a JSON array without panicking on an unexpected shape: a
// missing key means the writer never wrote, which needs a readable message.
func jsonList(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	list, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%q is not an array: %#v", key, m)
	}
	return list
}

func postCli(t *testing.T, g *Gateway, url string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	switch {
	case strings.Contains(url, "codex-settings"):
		g.handleCliCodexPost(rec, req)
	case strings.Contains(url, "opencode-settings"):
		g.handleCliOpencodePost(rec, req)
	case strings.Contains(url, "droid-settings"):
		g.handleCliDroidPost(rec, req)
	case strings.Contains(url, "crush-settings"):
		g.handleCliCrushPost(rec, req)
	case strings.Contains(url, "pi-settings"):
		g.handleCliPiPost(rec, req)
	case strings.Contains(url, "omp-settings"):
		g.handleCliOmpPost(rec, req)
	default:
		t.Fatalf("no handler for %s", url)
	}
	return rec
}

func deleteCli(t *testing.T, g *Gateway, url string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, url, nil)
	rec := httptest.NewRecorder()
	switch {
	case strings.Contains(url, "codex-settings"):
		g.handleCliCodexDelete(rec, req)
	case strings.Contains(url, "opencode-settings"):
		g.handleCliOpencodeDelete(rec, req)
	case strings.Contains(url, "droid-settings"):
		g.handleCliDroidDelete(rec, req)
	case strings.Contains(url, "crush-settings"):
		g.handleCliCrushDelete(rec, req)
	case strings.Contains(url, "pi-settings"):
		g.handleCliPiDelete(rec, req)
	case strings.Contains(url, "omp-settings"):
		g.handleCliOmpDelete(rec, req)
	default:
		t.Fatalf("no handler for %s", url)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("delete %s: %d %s", url, rec.Code, rec.Body)
	}
}

// codex: the provider table plus the three limit keys codex cannot derive from
// its own registry, and auth.json left alone on both apply and reset.
func TestCliParityCodex(t *testing.T) {
	home := tempHome(t)
	g := testGatewayLimits(t)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"OPENAI_API_KEY":"sk-user","auth_mode":"apikey"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if rec := postCli(t, g, "/api/cli-tools/codex-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "model": resolvedModel,
	}); rec.Code != http.StatusOK {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}

	cfg := parseTomlMap(readTextOrEmpty(filepath.Join(dir, "config.toml")))
	if got := tomlInt(cfg["model_context_window"]); got != resolvedWindow {
		t.Fatalf("model_context_window: %#v", cfg["model_context_window"])
	}
	if got := tomlInt(cfg["model_auto_compact_token_limit"]); got != resolvedCompact {
		t.Fatalf("model_auto_compact_token_limit: %#v", cfg["model_auto_compact_token_limit"])
	}
	if _, ok := cfg["model_max_output_tokens"]; !ok {
		t.Fatal("model_max_output_tokens missing")
	}
	prov := objMap(objMap(cfg["model_providers"])["kinerouter"])
	if str(prov, "base_url") != "http://localhost:20128/v1" || str(prov, "wire_api") != "responses" {
		t.Fatalf("provider table: %#v", prov)
	}
	if str(cfg, "model_provider") != "kinerouter" || str(cfg, "model") != resolvedModel {
		t.Fatalf("selection: %#v", cfg)
	}

	deleteCli(t, g, "/api/cli-tools/codex-settings")
	cfg = parseTomlMap(readTextOrEmpty(filepath.Join(dir, "config.toml")))
	for _, k := range []string{"model_context_window", "model_auto_compact_token_limit", "model_max_output_tokens", "model", "model_provider"} {
		if _, ok := cfg[k]; ok {
			t.Fatalf("%s left behind: %#v", k, cfg[k])
		}
	}
	if _, ok := objMap(cfg["model_providers"])["kinerouter"]; ok {
		t.Fatal("provider table left behind")
	}
	// The user's own codex login is not ours to delete.
	auth := readJSONMap(authPath)
	if str(auth, "OPENAI_API_KEY") != "sk-user" || str(auth, "auth_mode") != "apikey" {
		t.Fatalf("auth.json touched: %#v", auth)
	}
}

// opencode: an explicit per-model limit, and no doubled provider prefix.
func TestCliParityOpencode(t *testing.T) {
	home := tempHome(t)
	g := testGatewayLimits(t)

	postCli(t, g, "/api/cli-tools/opencode-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "models": []any{resolvedModel},
	})
	cfg := readJSONMap(filepath.Join(home, ".config", "opencode", "opencode.json"))
	entry := objMap(objMap(cfg["provider"])["kinerouter"])
	model := objMap(objMap(entry["models"])[resolvedModel])
	limit := objMap(model["limit"])
	if numString(limit["context"]) != "272000" {
		t.Fatalf("limit.context: %#v", limit["context"])
	}
	if got := str(cfg, "model"); got != "kinerouter/"+resolvedModel {
		t.Fatalf("model: %q", got)
	}

	// A model id copied out of opencode already carries the prefix.
	postCli(t, g, "/api/cli-tools/opencode-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "models": []any{"kinerouter/" + resolvedModel},
	})
	cfg = readJSONMap(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if got := str(cfg, "model"); got != "kinerouter/"+resolvedModel {
		t.Fatalf("double prefix: %q", got)
	}
	if objMap(objMap(objMap(cfg["provider"])["kinerouter"])["models"])["kinerouter/"+resolvedModel] != nil {
		t.Fatal("prefixed model key written")
	}
}

// droid: every entry is renumbered from zero, ours first, the user's kept.
func TestCliParityDroid(t *testing.T) {
	home := tempHome(t)
	g := testGatewayLimits(t)
	dir := filepath.Join(home, ".factory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	seed := `{"customModels":[{"id":"custom:mine","model":"x","index":7}]}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(seed), 0o600); err != nil {
		t.Fatal(err)
	}

	postCli(t, g, "/api/cli-tools/droid-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "models": []any{resolvedModel},
	})
	cfg := readJSONMap(filepath.Join(dir, "settings.json"))
	models := jsonList(t, cfg, "customModels")
	if len(models) != 2 {
		t.Fatalf("entries: %#v", models)
	}
	ours := objMap(models[0])
	if str(ours, "id") != "custom:KineRouter-0" || ours["index"] != 0.0 {
		t.Fatalf("ours first: %#v", ours)
	}
	mine := objMap(models[1])
	if str(mine, "id") != "custom:mine" || mine["index"] != 1.0 {
		t.Fatalf("user entry renumbered: %#v", mine)
	}
}

// crush: the resolved window replaces the 128000 guess.
func TestCliParityCrush(t *testing.T) {
	home := tempHome(t)
	g := testGatewayLimits(t)
	postCli(t, g, "/api/cli-tools/crush-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "model": resolvedModel,
	})
	cfg := readJSONMap(filepath.Join(home, ".config", "crush", "crush.json"))
	entry := objMap(objMap(cfg["providers"])["kinerouter"])
	models := jsonList(t, entry, "models")
	if got := objMap(models[0])["context_window"]; got != float64(resolvedWindow) {
		t.Fatalf("context_window: %#v", got)
	}

	// An unresolvable model keeps the documented fallback.
	postCli(t, g, "/api/cli-tools/crush-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "model": unresolvedModel,
	})
	cfg = readJSONMap(filepath.Join(home, ".config", "crush", "crush.json"))
	models = jsonList(t, objMap(objMap(cfg["providers"])["kinerouter"]), "models")
	if got := objMap(models[0])["context_window"]; got != float64(crushDefaultWind) {
		t.Fatalf("fallback context_window: %#v", got)
	}
}

// pi: 9router-go's 128000/16384 defaults when nothing resolves, the resolved
// window when it does.
func TestCliParityPi(t *testing.T) {
	home := tempHome(t)
	g := testGatewayLimits(t)
	path := filepath.Join(home, ".pi", "agent", "models.json")

	postCli(t, g, "/api/cli-tools/pi-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "model": unresolvedModel,
	})
	cfg := readJSONMap(path)
	models := jsonList(t, objMap(objMap(cfg["providers"])["kinerouter"]), "models")
	entry := objMap(models[0])
	if entry["contextWindow"] != float64(piDefaultWindow) || entry["maxTokens"] != float64(piDefaultMaxTok) {
		t.Fatalf("defaults: %#v", entry)
	}

	postCli(t, g, "/api/cli-tools/pi-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "model": resolvedModel,
	})
	cfg = readJSONMap(path)
	models = jsonList(t, objMap(objMap(cfg["providers"])["kinerouter"]), "models")
	if got := objMap(models[0])["contextWindow"]; got != float64(resolvedWindow) {
		t.Fatalf("resolved window: %#v", got)
	}
}

// omp: the provider block carries a models entry, because omp cannot compact a
// model with no window.
func TestCliParityOmp(t *testing.T) {
	home := tempHome(t)
	g := testGatewayLimits(t)
	postCli(t, g, "/api/cli-tools/omp-settings", map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-gw", "model": resolvedModel,
	})
	raw := readTextOrEmpty(filepath.Join(home, ".omp", "agent", "models.yml"))
	for _, want := range []string{
		"  kinerouter:", "    models:", `      - id: "kiro/gpt-5.6-sol"`,
		"        contextWindow: 272000", "        maxTokens:",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("models.yml missing %q:\n%s", want, raw)
		}
	}
	if !strings.Contains(raw, "discovery:\n      type: proxy") {
		t.Fatalf("discovery block:\n%s", raw)
	}
}
