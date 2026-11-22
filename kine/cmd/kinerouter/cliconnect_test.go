package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Self-check for the connect/show CLI: argument parsing, URL normalisation, the
// per-tool config writers, the reset paths and the saved-password server bind.
// Runs entirely under a temp HOME — nothing touches the real ~/.claude etc.
func TestConnectCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("DATA_DIR", "")
	t.Setenv("KINEROUTER_PASSWORD", "")

	// --- arg parsing / URL normalisation
	o, err := parseConnectArgs([]string{"10.0.0.5:20128/api/v1", "--tools", "claude-code,factory", "--opus", "cc/x", "--reset"})
	if err != nil {
		t.Fatal(err)
	}
	if o.url != "10.0.0.5:20128/api/v1" || o.models["opus"] != "cc/x" || !o.reset {
		t.Fatalf("parse: %+v", o)
	}
	srv, err := normalizeServerURL(o.url)
	if err != nil || srv != "http://10.0.0.5:20128" {
		t.Fatalf("normalize %q: %v", srv, err)
	}
	if _, err := parseConnectArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown option must fail")
	}

	tools, err := resolveTools([]string{"claude-code", "factory", "all"}, "claude")
	if err != nil || len(tools) != len(cliToolIDs) {
		t.Fatalf("resolve all: %v %v", tools, err)
	}
	if ids, _ := resolveTools(nil, "claude"); len(ids) != 1 || ids[0] != "claude" {
		t.Fatalf("default tool: %v", ids)
	}
	if _, err := resolveTools([]string{"nope"}, ""); err == nil {
		t.Fatal("unknown tool must fail")
	}

	if got := string(stripTrailingCommas(`{"a":1,,"b":[2,],}`)); strings.Contains(got, ",}") || strings.Contains(got, ",]") {
		t.Fatalf("stripTrailingCommas %q", got)
	}
	if maskKey("short") != "" || maskKey("sk-1234567890abcdef") != "sk-123…cdef" {
		t.Fatalf("maskKey: %q %q", maskKey("short"), maskKey("sk-1234567890abcdef"))
	}

	// --- apply every tool into the temp home
	claudeModels := map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "cc/opus"}
	for _, id := range cliToolIDs {
		files, err := applyTool(id, "https://router.example", "sk-abcdefghijkl", "oc/model", claudeModels)
		if err != nil {
			t.Fatalf("apply %s: %v", id, err)
		}
		for _, f := range files {
			st, err := os.Stat(f)
			if err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Fatalf("%s wrote %s as %o, want 0600 (holds the API key)", id, f, st.Mode().Perm())
			}
		}
	}

	// Claude: required env keys present, unset tier deleted.
	env := objMap(readJSONOrNil(filepath.Join(home, ".claude", "settings.json"))["env"])
	if env["ANTHROPIC_BASE_URL"] != "https://router.example/v1" || env["ANTHROPIC_AUTH_TOKEN"] == "" {
		t.Fatalf("claude env %v", env)
	}
	if _, ok := env["ANTHROPIC_DEFAULT_SONNET_MODEL"]; ok {
		t.Fatal("unset claude tier must not be written")
	}

	// Codex: provider block with bearer header.
	codexCfg := readTOMLOr(filepath.Join(home, ".codex", "config.toml"))
	if codexCfg["model_provider"] != "kinerouter" || codexCfg["model"] != "oc/model" {
		t.Fatalf("codex cfg %v", codexCfg)
	}
	prov := objMap(codexCfg["model_providers"])["kinerouter"].(map[string]any)
	if prov["wire_api"] != "responses" {
		t.Fatalf("codex provider %v", prov)
	}

	// omp: YAML provider block present and re-applying does not duplicate it.
	ompPath := filepath.Join(home, ".omp", "agent", "models.yml")
	if _, err := applyTool("omp", "https://router.example", "sk-abcdefghijkl", "", nil); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(readText(ompPath), "kinerouter:"); n != 1 {
		t.Fatalf("omp block duplicated (%d): %s", n, readText(ompPath))
	}

	// --- show reads every tool back
	for _, id := range cliToolIDs {
		r := inspectTool(id)
		if r.Error != "" {
			t.Fatalf("inspect %s: %s", id, r.Error)
		}
		if !r.Configured {
			t.Fatalf("%s should read back as configured", id)
		}
		if r.APIKey == "sk-abcdefghijkl" {
			t.Fatalf("%s show leaked the full key", id)
		}
	}

	// --- reset removes router settings again, idempotently
	for _, id := range cliToolIDs {
		if _, err := resetTool(id); err != nil {
			t.Fatalf("reset %s: %v", id, err)
		}
		if _, err := resetTool(id); err != nil {
			t.Fatalf("second reset %s: %v", id, err)
		}
		if r := inspectTool(id); r.BaseURL != "" || r.APIKey != "" {
			t.Fatalf("%s still configured after reset: %+v", id, r)
		}
	}

	// --- saved password is bound to one server
	t.Setenv("DATA_DIR", filepath.Join(home, "data"))
	if err := savePassword("https://a.example", "pw-1"); err != nil {
		t.Fatal(err)
	}
	if p, ok := loadSavedPassword("https://a.example"); !ok || p != "pw-1" {
		t.Fatalf("saved password round-trip: %q %v", p, ok)
	}
	if _, ok := loadSavedPassword("https://other.example"); ok {
		t.Fatal("saved password must not be released for a different server")
	}
	if st, _ := os.Stat(savedEnvPath()); st.Mode().Perm() != 0o600 {
		t.Fatalf("connect.env mode %o", st.Mode().Perm())
	}

	// --- server-side model extraction (codex TOML + opencode JSON)
	if m := toolServerModel("codex", map[string]any{
		"config": "model_provider = \"kinerouter\"\nmodel = \"cc/opus\"\n",
	}); m != "cc/opus" {
		t.Fatalf("codex server model %q", m)
	}
	if m := toolServerModel("opencode", map[string]any{
		"config": map[string]any{"model": "kinerouter/oc/x"},
	}); m != "oc/x" {
		t.Fatalf("opencode server model %q", m)
	}

	// show --json is valid JSON.
	out, _ := json.Marshal([]showRecord{{ID: "claude", Name: "Claude Code", Files: []string{"x"}}})
	if !json.Valid(out) {
		t.Fatal("show record must marshal")
	}
}
