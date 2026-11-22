package gateway

import (
	"sort"
	"strings"
	"testing"
)

// Self-check for the sidecar helpers that need no network and no real process:
// URL classification (the loopback gate and the SSRF-relevant scheme check),
// the extras whitelist, the dashboard rewrite and the PXpipe stats math.
func TestSidecarHelpers(t *testing.T) {
	for _, u := range []string{
		"http://localhost:8787", "http://127.0.0.1:8787", "http://[::1]:8787", "http://0.0.0.0:8787",
	} {
		if !isLoopbackHeadroomURL(u) {
			t.Fatalf("%s should count as loopback", u)
		}
	}
	for _, u := range []string{
		"http://10.0.0.5:8787", "https://headroom.example.com", "http://localhost.evil.com", "",
	} {
		if isLoopbackHeadroomURL(u) {
			t.Fatalf("%s must not count as loopback", u)
		}
	}
	if !isLoopback("::1") || !isLoopback("127.0.0.1") || isLoopback("10.1.2.3") {
		t.Fatal("isLoopback peer classification")
	}

	s := map[string]any{"headroomUrl": "https://prod.headroom.example.com"}
	if _, err := headroomTarget(s); err != nil {
		t.Fatalf("https URL should be accepted: %v", err)
	}
	s = map[string]any{"headroomUrl": "file:///etc/passwd"}
	if _, err := headroomTarget(s); err == nil {
		t.Fatal("non-http scheme must be rejected (SSRF guard)")
	}
	if got := parsePortFromURL("http://localhost:9999"); got != 9999 {
		t.Fatalf("port parse %d", got)
	}
	if got := parsePortFromURL("http://localhost"); got != 0 {
		t.Fatalf("missing port must be 0, got %d", got)
	}

	got := filterExtras([]any{"code", "ml", "../../evil", 7})
	sort.Strings(got)
	if strings.Join(got, ",") != "code,ml" {
		t.Fatalf("extras whitelist %v", got)
	}

	html := `<script>fetch('/stats');fetch('/health');fetch('/transformations/feed')</script>`
	out := rewriteHeadroomDashboard(html)
	if strings.Contains(out, "fetch('/stats'") || !strings.Contains(out, "/api/headroom/proxy/stats") {
		t.Fatalf("dashboard rewrite: %s", out)
	}

	// Stats math: applied events aggregate tokens, error reasons count as errors.
	win := emptyTotals()
	accumulateTotals(win, map[string]any{"applied": true, "tokensBeforeEst": 100.0, "tokensAfterEst": 40.0, "tokensSavedEst": 60.0, "imageCount": 1.0, "durationMs": 20.0})
	accumulateTotals(win, map[string]any{"applied": false, "reason": "timeout"})
	accumulateTotals(win, map[string]any{"applied": false, "reason": "below_threshold"})
	finalizeTotals(win)
	if win["compressed"].(int64) != 1 || win["errors"].(int64) != 1 || win["bypassed"].(int64) != 1 {
		t.Fatalf("classification %v", win)
	}
	if win["savedPct"].(float64) != 60 {
		t.Fatalf("savedPct %v", win["savedPct"])
	}
	if win["avgCompressionMs"].(int64) != 20 {
		t.Fatalf("avgCompressionMs %v", win["avgCompressionMs"])
	}
	if round2(12.345) != 12.35 {
		t.Fatal("round2")
	}
}
