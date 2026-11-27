package gateway

// Outbound proxy from the dashboard settings, ported from
// src/lib/network/outboundProxy.js.
//
// The reference writes HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY into
// process.env and leaves environment variables it did not manage alone. That
// cannot be reproduced in Go: http.ProxyFromEnvironment reads the environment
// once and caches it, so a saved setting would never reach an upstream call.
// The executor therefore asks this resolver per request instead, and a disabled
// or empty setting falls through to the environment, which is exactly what the
// reference does when it never managed the variable.

import (
	"net/http"
	"net/url"
	"strings"
)

// outboundProxySchemes are the schemes validateProxyUrl accepts.
var outboundProxySchemes = map[string]bool{
	"http": true, "https": true, "socks5": true, "socks4": true,
	"socks5h": true, "socks4a": true,
}

// outboundProxyConfig is the immutable snapshot the transport reads.
type outboundProxyConfig struct {
	enabled bool
	url     *url.URL
	noProxy []string
}

// parseOutboundProxy validates the three settings the way the reference does: a
// disallowed scheme, a malformed URL or a control character makes the proxy
// setting inert instead of failing the request.
func parseOutboundProxy(settings map[string]any) outboundProxyConfig {
	cfg := outboundProxyConfig{}
	if v, ok := settings["outboundProxyEnabled"].(bool); ok {
		cfg.enabled = v
	}
	if raw := strings.TrimSpace(str(settings, "outboundProxyUrl")); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Host != "" &&
			outboundProxySchemes[strings.ToLower(u.Scheme)] && !strings.ContainsAny(raw, "\n\r`$") {
			cfg.url = u
		}
	}
	cfg.noProxy = splitNoProxy(str(settings, "outboundNoProxy"))
	return cfg
}

func splitNoProxy(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// noProxyMatch reports whether host is covered by the NO_PROXY list, for the
// entry shapes the reference and Go both accept: "*", "example.com",
// ".example.com", "*.example.com" and an optional ":port" (ignored here, since
// only the host is known at dial time).
func noProxyMatch(host string, list []string) bool {
	if len(list) == 0 {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "" {
		return false
	}
	for _, entry := range list {
		if entry == "*" {
			return true
		}
		e := entry
		// Strip a trailing port only when it is unambiguous (no IPv6 literal).
		if i := strings.IndexByte(e, ':'); i > 0 && !strings.Contains(e, "]") && allDigits(e[i+1:]) {
			e = e[:i]
		}
		e = strings.TrimPrefix(e, "*")
		e = strings.TrimPrefix(e, ".")
		if e == "" {
			continue
		}
		if h == e || strings.HasSuffix(h, "."+e) {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// refreshOutboundProxy re-reads the settings snapshot. Called at startup and
// whenever PATCH /api/settings touches one of the three keys, so a saved proxy
// applies to the next upstream request without a restart.
func (g *Gateway) refreshOutboundProxy() {
	settings, err := g.store.GetSettings()
	if err != nil {
		return
	}
	g.outboundProxy.Store(parseOutboundProxy(settings))
}

// proxyForRequest is installed on the executor's transport.
func (g *Gateway) proxyForRequest(req *http.Request) (*url.URL, error) {
	cfg, _ := g.outboundProxy.Load().(outboundProxyConfig)
	if cfg.enabled && cfg.url != nil {
		if noProxyMatch(req.URL.Hostname(), cfg.noProxy) {
			return nil, nil
		}
		return cfg.url, nil
	}
	// Disabled, or enabled with no URL ("inherit the env proxy"): the operator's
	// own HTTP_PROXY/HTTPS_PROXY/NO_PROXY still apply.
	return http.ProxyFromEnvironment(req)
}
