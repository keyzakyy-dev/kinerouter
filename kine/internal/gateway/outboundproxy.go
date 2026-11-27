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
// ".example.com", "*.example.com" and an optional ":port". Like undici's
// no_proxy handling in the reference, an entry that names a port only matches
// that port; an entry without a port matches the host on any port.
func noProxyMatch(host string, list []string) bool {
	if len(list) == 0 {
		return false
	}
	h, hport := splitHostPort(strings.ToLower(host))
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return false
	}
	for _, entry := range list {
		if entry == "*" {
			return true
		}
		e := strings.TrimPrefix(strings.TrimPrefix(entry, "*"), ".")
		ehost, eport := splitHostPort(e)
		ehost = strings.TrimSuffix(ehost, ".")
		if ehost == "" {
			continue
		}
		if h != ehost && !strings.HasSuffix(h, "."+ehost) {
			continue
		}
		if eport != "" && hport != "" && eport != hport {
			continue
		}
		return true
	}
	return false
}

// splitHostPort splits "host", "host:port", "[::1]" and "[::1]:port" without
// treating a bare IPv6 literal's colons as a port separator.
func splitHostPort(s string) (host, port string) {
	if strings.HasPrefix(s, "[") {
		if end := strings.IndexByte(s, ']'); end > 0 {
			host = s[1:end]
			if rest := s[end+1:]; strings.HasPrefix(rest, ":") && allDigits(rest[1:]) {
				port = rest[1:]
			}
			return host, port
		}
	}
	if strings.Count(s, ":") == 1 {
		i := strings.IndexByte(s, ':')
		if allDigits(s[i+1:]) {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
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

// requestHostPort returns "host:port" for a request URL, filling in the scheme's
// default port so a NO_PROXY entry that names a port can be compared.
func requestHostPort(u *url.URL) string {
	if u == nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
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
		if noProxyMatch(requestHostPort(req.URL), cfg.noProxy) {
			return nil, nil
		}
		return cfg.url, nil
	}
	// Disabled, or enabled with no URL ("inherit the env proxy"): the operator's
	// own HTTP_PROXY/HTTPS_PROXY/NO_PROXY still apply.
	return http.ProxyFromEnvironment(req)
}
