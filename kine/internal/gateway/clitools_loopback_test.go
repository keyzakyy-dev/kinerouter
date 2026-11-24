package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The gateway listens on 0.0.0.0 (IPv4 only). Tools that resolve "localhost"
// to ::1 first never reach it, so their configs get the literal 127.0.0.1.
// KeyRouter made the same call; these tests pin the behaviour.
func TestIPv4Loopback(t *testing.T) {
	cases := map[string]string{
		"http://localhost:20128":        "http://127.0.0.1:20128",
		"http://localhost:20128/v1":     "http://127.0.0.1:20128/v1",
		"https://localhost/v1":          "https://127.0.0.1/v1",
		"http://LOCALHOST:20128/v1":     "http://127.0.0.1:20128/v1",
		"localhost:20128":               "127.0.0.1:20128",
		"localhost:20128/v1":            "127.0.0.1:20128/v1",
		"http://127.0.0.1:20128/v1":     "http://127.0.0.1:20128/v1",
		"http://192.168.1.20:20128/v1":  "http://192.168.1.20:20128/v1",
		"https://router.example.com/v1": "https://router.example.com/v1",
		"http://localhost.example:1/v1": "http://localhost.example:1/v1",
		"":                              "",
	}
	for in, want := range cases {
		if got := ipv4Loopback(in); got != want {
			t.Errorf("ipv4Loopback(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCliToolBaseOnlyRewritesFragileTools(t *testing.T) {
	for _, tool := range []string{"openclaw", "hermes", "grok-build", "jcode", "deepseek-tui"} {
		if got := cliToolBase(tool, "http://localhost:20128"); got != "http://127.0.0.1:20128/v1" {
			t.Errorf("%s: got %q", tool, got)
		}
	}
	// Tools outside the fragile set keep whatever the dashboard sent.
	if got := cliToolBase("claude", "http://localhost:20128"); got != "http://localhost:20128/v1" {
		t.Errorf("claude must keep localhost, got %q", got)
	}
	// A remote base URL is never rewritten, even for a fragile tool.
	if got := cliToolBase("jcode", "https://router.example.com"); got != "https://router.example.com/v1" {
		t.Errorf("remote base rewritten: %q", got)
	}
}

func TestCliToolPostsWriteIPv4Loopback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	cases := []struct {
		name    string
		handler func(*Gateway, http.ResponseWriter, *http.Request)
		body    string
		path    func() string
	}{
		{
			name:    "openclaw",
			handler: (*Gateway).handleCliOpenclawPost,
			body:    `{"baseUrl":"http://localhost:20128","model":"cc/claude-sonnet-4-6"}`,
			path:    openclawPath,
		},
		{
			name:    "hermes",
			handler: (*Gateway).handleCliHermesPost,
			body:    `{"baseUrl":"http://localhost:20128","model":"cc/claude-sonnet-4-6"}`,
			path:    func() string { return filepath.Join(hermesRoot(), "config.yaml") },
		},
		{
			name:    "grok-build",
			handler: (*Gateway).handleCliGrokPost,
			body:    `{"baseUrl":"http://localhost:20128","model":"cc/claude-sonnet-4-6","contextWindow":128000}`,
			path:    grokConfigPath,
		},
		{
			name:    "jcode",
			handler: (*Gateway).handleCliJcodePost,
			body:    `{"baseUrl":"http://localhost:20128","apiKey":"sk_test"}`,
			path:    jcodeConfigPath,
		},
		{
			name:    "deepseek-tui",
			handler: (*Gateway).handleCliDeepseekPost,
			body:    `{"baseUrl":"http://localhost:20128","model":"cc/claude-sonnet-4-6"}`,
			path:    deepseekPath,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := testGateway(t)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/cli-tools/"+tc.name+"-settings", strings.NewReader(tc.body))
			tc.handler(g, rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
			}
			content, err := os.ReadFile(tc.path())
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			if !strings.Contains(string(content), "127.0.0.1:20128") {
				t.Fatalf("config misses IPv4 loopback:\n%s", content)
			}
			if strings.Contains(string(content), "localhost") {
				t.Fatalf("config still carries localhost:\n%s", content)
			}
		})
	}
}

// Status detection must keep recognising configs written before this change
// (localhost) as well as the new ones (127.0.0.1).
func TestLoopbackDetectionAcceptsBothHosts(t *testing.T) {
	if !jcodeHasKineRouter(map[string]any{
		"providers": map[string]any{"proxy": map[string]any{"base_url": "http://127.0.0.1:20128/v1"}},
	}) {
		t.Fatal("jcodeHasKineRouter must accept 127.0.0.1")
	}
	if !jcodeHasKineRouter(map[string]any{
		"providers": map[string]any{"proxy": map[string]any{"base_url": "http://localhost:20128/v1"}},
	}) {
		t.Fatal("jcodeHasKineRouter must keep accepting localhost")
	}
	if !ompHasKineRouter("baseUrl: http://127.0.0.1:20128/v1") {
		t.Fatal("ompHasKineRouter must accept 127.0.0.1")
	}
}
