package gateway

import (
	"testing"

	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

// Round-robin must stick to the current account for `sticky` calls, then move
// to the least recently used one — never the just-used account.
func TestRRPickStickyRotation(t *testing.T) {
	a := &store.Connection{ID: "a", Data: map[string]any{"lastUsedAt": "2026-01-02T00:00:00Z", "consecutiveUseCount": float64(3)}}
	b := &store.Connection{ID: "b", Data: map[string]any{"lastUsedAt": "2026-01-01T00:00:00Z", "consecutiveUseCount": float64(0)}}
	c := &store.Connection{ID: "c", Data: map[string]any{}}
	avail := []*store.Connection{a, b, c}

	// a spent its sticky budget, and c was never used → rotate to c.
	got, stay := rrPick(avail, 3)
	if got != c || stay {
		t.Fatalf("rotation should pick the never-used account: %v stay=%v", got.ID, stay)
	}
	c.Data["lastUsedAt"] = "2026-01-03T00:00:00Z" // caller patches after use
	c.Data["consecutiveUseCount"] = float64(2)
	if got, stay := rrPick(avail, 3); got != c || !stay {
		t.Fatalf("expected sticky stay on c: %v stay=%v", got.ID, stay)
	}
	// Budget spent → rotate to the oldest used one (b).
	c.Data["consecutiveUseCount"] = float64(3)
	if got, stay := rrPick(avail, 3); got != b || stay {
		t.Fatalf("expected rotation to b: %v stay=%v", got.ID, stay)
	}
}
