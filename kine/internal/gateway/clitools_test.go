package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func testGateway(t *testing.T) *Gateway {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return &Gateway{store: s}
}

// Caller key wins, then the first active dashboard key, then "".
// The "sk_kinerouter" placeholder is never handed out (#4399).
func TestResolveCliAPIKey(t *testing.T) {
	g := testGateway(t)
	if got := g.resolveCliAPIKey(" sk_caller "); got != "sk_caller" {
		t.Fatalf("caller key: %q", got)
	}
	if got := g.resolveCliAPIKey("sk_kinerouter"); got != "" {
		t.Fatalf("placeholder leaked: %q", got)
	}
	if _, err := g.store.CreateAPIKey("dash", "m", "sk-first"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := g.resolveCliAPIKey(""); got != "sk-first" {
		t.Fatalf("fallback: %q", got)
	}
}

// JSONC writers tolerate a trailing comma; an unparseable file reads as empty.
func TestJSONCTolerance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	if err := os.WriteFile(path, []byte(`{"a":1,"b":[1,2,],}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m := readJSONMap(path)
	if m == nil || m["a"] != 1.0 {
		t.Fatalf("jsonc map: %#v", m)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{not json`), 0o600)
	if readJSONMap(bad) != nil {
		t.Fatal("unparseable file must read as nil")
	}
}

// The managed MCP policy allows both the bare and the prefixed tool name.
func TestCoworkManagedServers(t *testing.T) {
	out := coworkManagedServers([]coworkPlugin{
		{Name: "exa", URL: "https://mcp.exa.ai/mcp", ToolNames: []string{"exa-web_search_exa", "web_fetch_exa"}},
		{Name: "exa", URL: "https://dup.example/mcp"}, // deduped by name
		{Name: "nourl"},
	})
	if len(out) != 1 {
		t.Fatalf("dedupe: %#v", out)
	}
	e := out[0]
	if e["transport"] != "http" {
		t.Fatalf("transport: %v", e["transport"])
	}
	policy, _ := e["toolPolicy"].(map[string]any)
	for _, k := range []string{"web_search_exa", "exa-web_search_exa", "web_fetch_exa", "exa-web_fetch_exa"} {
		if policy[k] != "allow" {
			t.Fatalf("policy missing %s: %#v", k, policy)
		}
	}
}

// Cowork stores the bare origin: Claude Desktop appends /v1/messages itself.
func TestCoworkGatewayBaseURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:20128/v1":      "http://127.0.0.1:20128",
		"http://127.0.0.1:20128/v1/":     "http://127.0.0.1:20128",
		"http://127.0.0.1:20128/":        "http://127.0.0.1:20128",
		"http://127.0.0.1:20128":         "http://127.0.0.1:20128",
		"  https://kine.example.com/v1 ": "https://kine.example.com",
		"https://gw.example.com/ki/v1":   "https://gw.example.com/ki",
		"https://gw.example.com/v1/alt":  "https://gw.example.com/v1/alt",
	}
	for in, want := range cases {
		if got := coworkGatewayBaseURL(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}
