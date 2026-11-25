package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Emitters and detectors used to bake in the reference product's 20128. They
// must follow whatever port this gateway serves on.
func TestCliPortFollowsConfig(t *testing.T) {
	tempHome(t)
	g := testGateway(t)
	g.cfg.Port = 41234

	entries := g.coworkLocalBridgeEntries([]string{coworkLocalStdioPlugins[0].Name})
	if len(entries) != 1 {
		t.Fatalf("no bridge entry: %#v", entries)
	}
	if u := str(map[string]any(entries[0]), "url"); !strings.Contains(u, ":41234/") {
		t.Fatalf("bridge url %q does not follow cfg.Port", u)
	}

	settings := map[string]any{"env": map[string]any{"ANTHROPIC_BASE_URL": "http://localhost:41234/v1"}}
	if !g.claudePointsAtGateway(settings, "") {
		t.Fatal("claude detector missed this gateway's own port")
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:41234/", nil)
	req.Host = ""
	if got := g.publicOrigin(req, map[string]any{}, false); got != "http://localhost:41234" {
		t.Fatalf("publicOrigin fallback = %q", got)
	}
}
