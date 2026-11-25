package fetch

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// PublicIP reports whether ip is a globally routable unicast address.
// Loopback, private, link-local, CGNAT, unspecified and multicast ranges are refused.
func PublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range denied {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

// denied lists ranges IsGlobalUnicast accepts but that are not public hosts.
// It includes the IPv6 forms that embed an IPv4 address (NAT64, 6to4,
// IPv4-compatible), which could otherwise reach internal IPv4 hosts.
var denied = mustPrefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15", "240.0.0.0/4",
	"192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24",
	"2001:db8::/32", "fec0::/10",
	"64:ff9b::/96", "64:ff9b:1::/48", "2002::/16", "::/96",
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// guardControl is a net.Dialer.Control hook. It sees the resolved address of
// every connection attempt, so DNS rebinding and redirects cannot get around it.
func guardControl(allow func(net.IP) bool) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: bad address %q", ErrBlocked, address)
		}
		ip := net.ParseIP(host)
		if ip == nil || !allow(ip) {
			return fmt.Errorf("%w: %s", ErrBlocked, host)
		}
		return nil
	}
}
