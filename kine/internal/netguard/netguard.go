// Package netguard blocks server-side requests to internal hosts (SSRF).
// Mirrors src/shared/utils/ssrfGuard.js: literal checks plus DNS resolution, and
// a dialer control so a hostname cannot pass validation then resolve to a
// private address at connect time (DNS rebinding).
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// ErrBlocked is returned for private, loopback, link-local and metadata targets.
var ErrBlocked = errors.New("blocked: target address is not public")

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
}

// BlockedIP reports whether ip is private, loopback, link-local or reserved.
// IPv4-mapped IPv6 addresses are unmapped first.
func BlockedIP(ip netip.Addr) bool {
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	if !ip.IsValid() {
		return true
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// Dialer is a net.Dialer whose Control hook rejects blocked addresses at connect
// time. CheckURL alone leaves a gap: it resolves once, and the dial that follows
// resolves again, so a rebinding DNS answer can swap in 127.0.0.1 between the
// two. Every client that fetches an untrusted URL must dial through this.
var Dialer = &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: Control}

// NewTransport returns a transport that refuses to connect to a blocked address,
// whatever DNS answered. proxy, when non-nil, is used as the transport proxy.
func NewTransport(proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:                 proxy,
		DialContext:           Dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// Client returns an http.Client that dials only public addresses. Redirects are
// checked too, so a public URL cannot bounce into a private one.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewTransport(nil),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return CheckURL(req.Context(), req.URL.String())
		},
	}
}

// CheckURL validates scheme and, for hostnames, every resolved address.
func CheckURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid URL protocol: %s", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("invalid URL: missing host")
	}
	if host == "localhost" || host == "ip6-localhost" || host == "ip6-loopback" {
		return ErrBlocked
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if BlockedIP(ip) {
			return ErrBlocked
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("cannot resolve host %q", host)
	}
	for _, a := range addrs {
		if BlockedIP(a) {
			return ErrBlocked
		}
	}
	return nil
}

// Control is a net.Dialer Control hook that rejects connections to blocked
// addresses, defeating a rebinding DNS answer between CheckURL and dial.
func Control(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if BlockedIP(ip) {
		return ErrBlocked
	}
	return nil
}
