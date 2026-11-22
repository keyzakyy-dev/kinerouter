package gateway

import (
	"strings"
	"testing"
)

// Self-check for the tunnel helpers that do not need a network or a store:
// short-id generation, the per-platform cloudflared download URL, the
// trycloudflare log parser and the public-URL shape.
func TestTunnelHelpers(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id := generateShortID()
		if len(id) != tunnelShortIDLen {
			t.Fatalf("short id length %d: %q", len(id), id)
		}
		for _, r := range id {
			if !strings.ContainsRune(tunnelShortIDChars, r) {
				t.Fatalf("short id char %q not in alphabet", r)
			}
		}
		seen[id] = true
	}
	if len(seen) < 190 {
		t.Fatalf("short ids look non-random: %d unique of 200", len(seen))
	}

	url, archive, err := cloudflaredDownloadURL()
	if err != nil {
		t.Skipf("platform unsupported: %v", err)
	}
	if !strings.HasPrefix(url, "https://github.com/cloudflare/cloudflared/releases/") {
		t.Fatalf("download url %q", url)
	}
	if archive != strings.HasSuffix(url, ".tgz") {
		t.Fatalf("archive flag does not match url %q (archive=%v)", url, archive)
	}

	// The log parser keeps the host and drops api.trycloudflare.com.
	line := "INF |  https://api.trycloudflare.com  |  https://brave-lion-fox.trycloudflare.com"
	m := tryCloudflareRe.FindAllStringSubmatch(line, -1)
	if len(m) != 2 || m[0][1] != "api" || m[1][1] != "brave-lion-fox" {
		t.Fatalf("cloudflared url parse: %v", m)
	}

	tm := &tunnelManager{}
	if got := tm.publicURL("abc123"); got != "https://rabc123.abc-tunnel.us" {
		t.Fatalf("public url %q", got)
	}
	if got := tm.publicURL(""); got != "" {
		t.Fatalf("empty short id must give no public url, got %q", got)
	}
	if truncate("abcdef", 3) != "def" || truncate("ab", 5) != "ab" {
		t.Fatal("truncate should keep the log tail")
	}
	if probeURLAlive("", 0, 0) {
		t.Fatal("empty url must not be alive")
	}
	if parseHostname("https://r1.abc-tunnel.us/api/health") != "r1.abc-tunnel.us" {
		t.Fatal("parseHostname")
	}
}
