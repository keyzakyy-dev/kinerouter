package gateway

import "testing"

// Round-robin combo: sticky budget then rotate; fallback keeps the order.
func TestRotatedMembers(t *testing.T) {
	models := []string{"a", "b", "c"}

	got := rotateMembers("combo-x", models, "round-robin", 1)
	if want := []string{"a", "b", "c"}; !equal(got, want) {
		t.Fatalf("first rotation: %v", got)
	}
	got = rotateMembers("combo-x", models, "round-robin", 1)
	if want := []string{"b", "c", "a"}; !equal(got, want) {
		t.Fatalf("second rotation: %v", got)
	}
	got = rotateMembers("combo-x", models, "fallback", 1)
	if want := []string{"a", "b", "c"}; !equal(got, want) {
		t.Fatalf("fallback reordered: %v", got)
	}
	resetComboRotation("combo-x")
	if got := rotateMembers("combo-x", models, "round-robin", 3); got[0] != "a" {
		t.Fatalf("reset did not clear state: %v", got)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
