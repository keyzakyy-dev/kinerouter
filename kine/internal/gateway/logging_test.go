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
	if quietPath("/v1/chat/completions") || quietPath("/api/connections/abc") {
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

// The completion row is the KeyRouter shape: total, TTFT, token split with the
// cache figures, output count.
func TestDoneLogLine(t *testing.T) {
	u := translate.Usage{PromptTokens: 100, CompletionTokens: 20, CacheRead: 30, CacheCreation: 5}
	stats := &streamStats{}
	stats.note()
	time.Sleep(2 * time.Millisecond)
	stats.note()

	line := doneLogLine(u, time.Now().Add(-time.Second), stats)
	for _, want := range []string{"DONE ", "ms · TTFT ", " · IN 135 (CACHE ↻30 +5) · OUT 20"} {
		if !strings.Contains(line, want) {
			t.Fatalf("done line missing %q: %s", want, line)
		}
	}

	plain := doneLogLine(translate.Usage{PromptTokens: 7, CompletionTokens: 3}, time.Now(), nil)
	if strings.Contains(plain, "TTFT") || strings.Contains(plain, "CACHE") {
		t.Fatalf("zero-value line should omit TTFT/CACHE: %s", plain)
	}
	if !strings.HasSuffix(plain, " · IN 7 · OUT 3") {
		t.Fatalf("plain line = %s", plain)
	}
}

// The start row names the account, counts the turn, and reports the thinking
// mode only when the client asked for one.
func TestChatStartParts(t *testing.T) {
	raw := map[string]any{
		"messages":         []any{map[string]any{}, map[string]any{}, map[string]any{}},
		"tools":            []any{map[string]any{}, map[string]any{}},
		"reasoning_effort": "high",
	}
	if n := messageCount(raw); n != 3 {
		t.Fatalf("messageCount = %d", n)
	}
	if n := toolCount(raw); n != 2 {
		t.Fatalf("toolCount = %d", n)
	}
	if level := thinkLevel(raw); level != "high" {
		t.Fatalf("thinkLevel = %q", level)
	}
	if level := thinkLevel(map[string]any{}); level != "" {
		t.Fatalf("thinkLevel(empty) = %q", level)
	}
	if got := accountLabel(&store.Connection{Name: "Work"}); got != "Work" {
		t.Fatalf("accountLabel name = %q", got)
	}
	if got := accountLabel(&store.Connection{ID: "063cd40e-aaaa"}); got != "063cd40e" {
		t.Fatalf("accountLabel id = %q", got)
	}
}
