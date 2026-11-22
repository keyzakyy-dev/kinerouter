package gateway

import "testing"

func TestPricingDefaultsAndOverride(t *testing.T) {
	pt := &pricingTable{user: map[string]map[string]map[string]float64{
		"gh": {"gpt-5.3-codex": {"output": 99}},
	}}
	m := pt.merged()
	gh := m["gh"]["gpt-5.3-codex"]
	if gh.Output != 99 || gh.Input != 1.75 {
		t.Fatalf("merge: got %+v, want output override only", gh)
	}
	if len(m) != len(defaultPricing()) {
		t.Fatalf("merged providers %d != defaults %d", len(m), len(defaultPricing()))
	}
}
