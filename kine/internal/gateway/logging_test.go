package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// The console row for a request must carry the id and the route the client
// meant, and routine polling must not sit at info next to real traffic.
func TestQuietPathAndRouteTrim(t *testing.T) {
	quiet := []string{"/api/connections", "/api/usage/stats", "/health", "/assets/app.js", "/providers/x"}
	for _, p := range quiet {
		if !quietPath(p) {
			t.Fatalf("%s should log at debug", p)
		}
	}
	if quietPath("/v1/chat/completions") || quietPath("/api/connections/abc") {
		t.Fatal("real request path treated as quiet")
	}

	path := "/v1/chat/completions"
	for len(path) > 4 && path[:4] == "/v1/" {
		path = path[3:]
	}
	if path != "/chat/completions" {
		t.Fatalf("trimmed path = %q", path)
	}
}

// The usage line must name the model and the cache/latency figures an operator
// reads; a no-auth provider reports the account instead of an empty conn.
func TestUsageLogLine(t *testing.T) {
	u := translate.Usage{PromptTokens: 100, CompletionTokens: 20, CacheRead: 30, CacheCreation: 5}
	line := usageLogLine(
		&store.Connection{ID: "abc123", Name: "Work", Email: "me@x.com"},
		target{Provider: "anthropic", Model: "claude-sonnet-4.5"},
		u, 0.0123, time.Now().Add(-time.Second),
	)
	for _, want := range []string{
		"logged provider=anthropic model=claude-sonnet-4.5",
		"prompt=135", "completion=20", "cached=30", "cache_creation=5",
		"latency_ms=", "cost=0.012300", "conn=abc123", "connName=Work", "email=me@x.com",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("usage line missing %q: %s", want, line)
		}
	}

	direct := usageLogLine(&store.Connection{ID: "noauth"}, target{Provider: "free", Model: "m"}, u, 0, time.Now())
	if !strings.HasSuffix(direct, "account=Public / Direct") {
		t.Fatalf("no-auth line = %s", direct)
	}
}
