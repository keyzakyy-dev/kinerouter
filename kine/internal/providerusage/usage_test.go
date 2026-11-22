package providerusage

import (
	"testing"
	"time"
)

// Self-check for the pure parsers ported from open-sse/services/usage/*.
func TestParseResetTime(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{1700000000, "2023-11-14T22:13:20Z"},             // seconds
		{float64(1700000000000), "2023-11-14T22:13:20Z"}, // ms
		{"1700000000", "2023-11-14T22:13:20Z"},           // numeric string
		{"", ""},
		{nil, ""},
		{0, ""},
	}
	for _, c := range cases {
		if got := parseResetTime(c.in); got != c.want {
			t.Errorf("parseResetTime(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := parseResetTime("2030-01-01T00:00:00Z"); got != "2030-01-01T00:00:00Z" {
		t.Errorf("iso passthrough = %q", got)
	}
}

func TestParseGroqDuration(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"2m59.56s", 2*time.Minute + 59*time.Second + 560*time.Millisecond, true},
		{"7.66s", 7*time.Second + 660*time.Millisecond, true},
		{"100ms", 100 * time.Millisecond, true},
		{"", 0, false},
		{"garbage", 0, false},
	}
	for _, c := range cases {
		got, ok := parseGroqDuration(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseGroqDuration(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestValidGrantID(t *testing.T) {
	for _, ok := range []string{"abc-123_x", "a"} {
		if !validGrantID(ok) {
			t.Errorf("validGrantID(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "ABC", "a b", "x/y", string(make([]byte, 41))} {
		if validGrantID(bad) {
			t.Errorf("validGrantID(%q) = true", bad)
		}
	}
}

func TestParseClaudeResetGrants(t *testing.T) {
	in := map[string]any{
		"eligible":      true,
		"next_grant_id": "g2",
		"grants": []any{
			map[string]any{"id": "g1", "resets_left": 2.0, "clears": []any{"session"}},
			map[string]any{"id": "g2", "resets_left": 1.0, "ends_at": "2030-01-01T00:00:00Z"},
			map[string]any{"id": "g3", "resets_left": 5.0, "paused": true},
		},
	}
	out, ok := parseClaudeResetGrants(in).(map[string]any)
	if !ok {
		t.Fatal("expected object")
	}
	if out["availableCount"] != 3.0 {
		t.Errorf("availableCount = %v", out["availableCount"])
	}
	if out["nextGrantId"] != "g2" {
		t.Errorf("nextGrantId = %v", out["nextGrantId"])
	}
	if parseClaudeResetGrants(map[string]any{"eligible": false}) != nil {
		t.Error("ineligible should be nil")
	}
}
