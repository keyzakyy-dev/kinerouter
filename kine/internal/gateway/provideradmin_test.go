package gateway

// Tests for the provider-admin routes: override validation (the security-
// sensitive boundary), suggested-models filters, and the auth grouping the
// batch tester relies on.

import (
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/providerdata"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

func adminGateway(t *testing.T) *Gateway {
	t.Helper()
	g := testGateway(t)
	g.reg = providerdata.Get()
	return g
}

func TestNormalizeOverride(t *testing.T) {
	cases := []struct {
		name    string
		in      map[string]any
		wantErr bool
		len     int
	}{
		{"ok", map[string]any{"X-Foo": "bar"}, false, 1},
		{"blocked auth", map[string]any{"Authorization": "Bearer x"}, true, 0},
		{"blocked host", map[string]any{"host": "evil"}, true, 0},
		{"crlf injection", map[string]any{"X-Foo": "a\r\nX-Evil: 1"}, true, 0},
		{"bad name", map[string]any{"X F oo": "v"}, true, 0},
		{"non-string", map[string]any{"X-Foo": 5}, true, 0},
		{"empty dropped", map[string]any{"X-Foo": ""}, false, 0},
		{"too long", map[string]any{"X-Foo": string(make([]byte, maxOverrideHeaderValue+1))}, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clean, msg := normalizeOverride(c.in)
			if c.wantErr && msg == "" {
				t.Fatalf("expected error, got clean=%v", clean)
			}
			if !c.wantErr && msg != "" {
				t.Fatalf("unexpected error: %s", msg)
			}
			if len(clean) != c.len {
				t.Fatalf("len=%d want %d", len(clean), c.len)
			}
		})
	}
}

func TestOverrideRoundTrip(t *testing.T) {
	g := adminGateway(t)
	if _, err := g.store.UpdateSettings(map[string]any{"providerOverrides": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.store.UpdateSettings(map[string]any{
		"providerOverrides": map[string]any{
			"openai": map[string]any{"headers": map[string]any{"X-Beta": "1"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	got := g.providerOverrideHeaders("openai")
	if got["X-Beta"] != "1" {
		t.Fatalf("override not read: %v", got)
	}
	if v := g.providerOverrideHeaders("nope"); v != nil {
		t.Fatalf("unknown provider returned %v", v)
	}
}

func TestSuggestedFilterOpenRouterFree(t *testing.T) {
	list := []any{
		map[string]any{"id": "free-big", "name": "Free Big", "context_length": 200000.0,
			"pricing": map[string]any{"prompt": "0", "completion": "0"}},
		map[string]any{"id": "paid", "name": "Paid", "context_length": 200000.0,
			"pricing": map[string]any{"prompt": "0.0001", "completion": "0"}},
		map[string]any{"id": "small", "name": "Small", "context_length": 8000.0,
			"pricing": map[string]any{"prompt": "0", "completion": "0"}},
	}
	out := suggestedFilter("openrouter-free", list)
	if len(out) != 1 || out[0]["id"] != "free-big" {
		t.Fatalf("got %v", out)
	}
}

func TestSuggestedFilterOpenCode(t *testing.T) {
	list := []any{
		map[string]any{"id": "x-free"}, map[string]any{"id": "big-pickle"},
		map[string]any{"id": "deepseek-v4-flash-free"}, map[string]any{"id": "paid"},
	}
	out := suggestedFilter("opencode-free", list)
	if len(out) != 2 {
		t.Fatalf("want 2 free, got %v", out)
	}
	goOut := suggestedFilter("opencode-go", list)
	if len(goOut) != 4 {
		t.Fatalf("opencode-go should keep all 4, got %v", goOut)
	}
}

func TestAuthGroup(t *testing.T) {
	g := adminGateway(t)
	conn := &store.Connection{Provider: "openai", AuthType: "apikey"}
	if got := authGroup("openai", conn, g); got != "apikey" {
		t.Fatalf("got %q", got)
	}
	// A stored authType always wins, matching getAuthGroup; the compatible
	// group is only the fallback for a connection with no authType.
	if got := authGroup("openai-compatible-custom-x", &store.Connection{}, g); got != "compatible" {
		t.Fatalf("node group: got %q", got)
	}
}
