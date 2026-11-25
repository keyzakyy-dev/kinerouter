package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeHome points HOME at a temp dir so the writer touches a scratch
// ~/.claude/settings.json, and returns the parsed file afterwards.
func claudeHome(t *testing.T) func() map[string]any {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	return func() map[string]any {
		b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
		if err != nil {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("settings.json is not JSON: %v\n%s", err, b)
		}
		return m
	}
}

func postClaude(t *testing.T, g *Gateway, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/cli-tools/claude-settings", strings.NewReader(string(raw)))
	rec := httptest.NewRecorder()
	g.handleCliClaudePost(rec, req)
	return rec
}

// 9router-go's request shape writes the same file it does: env for the endpoint,
// the token and the four tiers, plus the top-level model and autoCompactWindow.
func TestClaudePost9RouterShape(t *testing.T) {
	read := claudeHome(t)
	g := testGateway(t)

	rec := postClaude(t, g, map[string]any{
		"baseUrl":           "http://localhost:20128",
		"apiKey":            "sk-test",
		"model":             "cc/claude-sonnet-4-5",
		"tiers":             map[string]any{"sonnet": "cc/claude-sonnet-4-5", "haiku": "oc/fast-free"},
		"autoCompactWindow": "330000",
		"exaMcpEnabled":     false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	m := read()
	if m["hasCompletedOnboarding"] != true {
		t.Fatalf("hasCompletedOnboarding: %#v", m["hasCompletedOnboarding"])
	}
	if got := m["model"]; got != "cc/claude-sonnet-4-5" {
		t.Fatalf("model: %#v", got)
	}
	if got := m["autoCompactWindow"]; got != float64(330000) {
		t.Fatalf("autoCompactWindow: %#v", got)
	}
	env := objMap(m["env"])
	if got := str(env, "ANTHROPIC_BASE_URL"); got != "http://localhost:20128/v1" {
		t.Fatalf("base url: %q", got)
	}
	if got := str(env, "ANTHROPIC_AUTH_TOKEN"); got != "sk-test" {
		t.Fatalf("token: %q", got)
	}
	if got := str(env, "ANTHROPIC_DEFAULT_SONNET_MODEL"); got != "cc/claude-sonnet-4-5" {
		t.Fatalf("sonnet tier: %q", got)
	}
	if got := str(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL"); got != "oc/fast-free" {
		t.Fatalf("haiku tier: %q", got)
	}
	if _, ok := env["ANTHROPIC_DEFAULT_OPUS_MODEL"]; ok {
		t.Fatal("an unnamed tier must be left alone, not written empty")
	}
}

// An empty API key keeps the token already in the file, so re-applying an
// unchanged form cannot break a working setup.
func TestClaudePostKeepsSavedToken(t *testing.T) {
	read := claudeHome(t)
	g := testGateway(t)
	postClaude(t, g, map[string]any{"baseUrl": "http://localhost:20128", "apiKey": "sk-keep"})

	postClaude(t, g, map[string]any{"baseUrl": "http://localhost:20128", "apiKey": ""})
	if got := str(objMap(read()["env"]), "ANTHROPIC_AUTH_TOKEN"); got != "sk-keep" {
		t.Fatalf("token overwritten: %q", got)
	}

	// A supplied token replaces the old one.
	postClaude(t, g, map[string]any{"apiKey": "sk-new"})
	if got := str(objMap(read()["env"]), "ANTHROPIC_AUTH_TOKEN"); got != "sk-new" {
		t.Fatalf("token not replaced: %q", got)
	}
}

// The TUI still posts {env: ...}; those exact keys must keep landing in the file
// and the auto-compact env value must also set the top-level window.
func TestClaudePostLegacyEnvShape(t *testing.T) {
	read := claudeHome(t)
	g := testGateway(t)

	postClaude(t, g, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":             "http://localhost:20128/v1",
			"ANTHROPIC_AUTH_TOKEN":           "sk-tui",
			"API_TIMEOUT_MS":                 "600000",
			"ANTHROPIC_DEFAULT_SONNET_MODEL": "cc/claude-sonnet-4-5",
		},
		"autoCompactWindow": "198000",
	})

	m := read()
	env := objMap(m["env"])
	if got := str(env, "API_TIMEOUT_MS"); got != "600000" {
		t.Fatalf("legacy key dropped: %q", got)
	}
	if got := str(env, "ANTHROPIC_DEFAULT_SONNET_MODEL"); got != "cc/claude-sonnet-4-5" {
		t.Fatalf("sonnet tier: %q", got)
	}
	if got := m["autoCompactWindow"]; got != float64(198000) {
		t.Fatalf("autoCompactWindow: %#v", got)
	}
}

// Reset removes every managed env key (fable included), the top-level window,
// and the model only when the file pointed at us.
func TestClaudeDeleteResetsManagedKeys(t *testing.T) {
	read := claudeHome(t)
	g := testGateway(t)
	postClaude(t, g, map[string]any{
		"baseUrl": "http://localhost:20128", "apiKey": "sk-x", "model": "cc/x",
		"tiers":             map[string]any{"fable": "cc/fable", "opus": "cc/opus", "sonnet": "cc/sonnet", "haiku": "cc/haiku"},
		"autoCompactWindow": "200000",
	})

	req := httptest.NewRequest(http.MethodDelete, "/api/cli-tools/claude-settings", nil)
	rec := httptest.NewRecorder()
	g.handleCliClaudeDelete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	m := read()
	if env := objMap(m["env"]); len(env) != 0 {
		t.Fatalf("env not cleared: %#v", env)
	}
	if _, ok := m["autoCompactWindow"]; ok {
		t.Fatal("autoCompactWindow must be removed")
	}
	if _, ok := m["model"]; ok {
		t.Fatal("model must be removed when the file pointed at the gateway")
	}
	if m["hasCompletedOnboarding"] != true {
		t.Fatal("reset must keep hasCompletedOnboarding")
	}
}

// A model the user chose for the official API is not ours to delete.
func TestClaudeDeleteKeepsForeignModel(t *testing.T) {
	read := claudeHome(t)
	home := os.Getenv("HOME")
	_ = home
	path := filepath.Join(os.Getenv("HOME"), ".claude", "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"claude-opus-4-6","env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com/v1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	g := testGateway(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/cli-tools/claude-settings", nil)
	g.handleCliClaudeDelete(httptest.NewRecorder(), req)
	if got := read()["model"]; got != "claude-opus-4-6" {
		t.Fatalf("foreign model deleted: %#v", got)
	}
}
