package gateway

import "testing"

// A proxy pool is normally a local or LAN proxy — the dashboard placeholder is
// http://127.0.0.1:7897 — so the SSRF guard must not reject loopback. Metadata
// and other link-local targets stay blocked.
func TestCheckProxyPoolURL(t *testing.T) {
	allowed := []string{
		"http://127.0.0.1:7897",
		"http://localhost:8080",
		"http://192.168.1.10:3128",
		"http://10.0.0.5:8080",
		"socks5://127.0.0.1:1080",
		"http://user:pass@127.0.0.1:7897",
	}
	for _, raw := range allowed {
		if err := checkProxyPoolURL(raw); err != nil {
			t.Errorf("%s: want allowed, got %v", raw, err)
		}
	}

	blocked := []string{
		"http://169.254.169.254/latest/meta-data",
		"ftp://127.0.0.1:21",
		"not-a-url",
		"http://",
	}
	for _, raw := range blocked {
		if err := checkProxyPoolURL(raw); err == nil {
			t.Errorf("%s: want blocked", raw)
		}
	}
}
