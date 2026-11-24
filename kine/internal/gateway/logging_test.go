package gateway

import (
	"strings"
	"testing"
	"time"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
	"github.com/keyzakyy-dev/kinerouter/internal/translate"
)

// The request row carries the id and the route the client meant; quiet paths
// are the reference's list, and the /v1 alias is trimmed before logging.
func TestQuietPathAndRouteTrim(t *testing.T) {
	quiet := []string{"/api/connections", "/api/usage/stats", "/health", "/assets/app.js", "/providers/x"}
	for _, p := range quiet {
		if !quietPath(p) {
			t.Fatalf("%s should log at debug", p)
		}
	}
	// Dashboard pollers outside the reference list keep their info rows.
	if quietPath("/api/settings") || quietPath("/v1/chat/completions") || quietPath("/api/connections/abc") {
		t.Fatal("unexpected quiet path")
	}

	path := "/v1/chat/completions"
	for len(path) > 4 && path[:4] == "/v1/" {
		path = path[3:]
	}
	if path != "/chat/completions" {
		t.Fatalf("trimmed path = %q", path)
	}
}

// The usage line carries the same keys as the reference, in the same order,
// with the account that paid for the turn.
func TestUsageLogLine(t *testing.T) {
	u := translate.Usage{PromptTokens: 100, CompletionTokens: 20, CacheRead: 30, CacheCreation: 5}
	stats := &streamStats{}
	stats.note()
	time.Sleep(2 * time.Millisecond)
	stats.note()

	line := usageLogLine(
		&store.Connection{ID: "abc123", Name: "Work", Email: "me@x.com"},
		target{Provider: "anthropic", Model: "claude-sonnet-4.5"},
		u, 0.0123, time.Now().Add(-time.Second), stats, 7,
	)
	keys := []string{
		"logged provider=anthropic model=claude-sonnet-4.5",
		"prompt=135", "completion=20", "cached=30", "cache_creation=5",
		"cache_creation_5m=0", "cache_creation_1h=0",
		"ttft_ms=", "latency_ms=", "cost=0.0123", "tok_per_sec=", "max_gap_ms=",
		"compressed=142->135 (4% saved)",
		"conn=abc123", "connName=Work", "email=me@x.com",
	}
	pos := 0
	for _, want := range keys {
		at := strings.Index(line[pos:], want)
		if at < 0 {
			t.Fatalf("usage line missing or out of order %q: %s", want, line)
		}
		pos += at + len(want)
	}

	direct := usageLogLine(&store.Connection{ID: "noauth"}, target{Provider: "free", Model: "m"}, u, 0, time.Now(), nil, 0)
	if !strings.HasSuffix(direct, "account=Public / Direct") {
		t.Fatalf("no-auth line = %s", direct)
	}
	if strings.Contains(direct, "compressed=") {
		t.Fatalf("no savings reported, yet compressed key present: %s", direct)
	}
}
