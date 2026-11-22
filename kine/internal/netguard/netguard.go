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
	"net/netip"
	"net/url"
	"syscall"
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
