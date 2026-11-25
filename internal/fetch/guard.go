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
	return !cgnat.Contains(addr)
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

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
