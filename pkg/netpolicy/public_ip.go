// Package netpolicy contains shared outbound-network trust decisions.
package netpolicy

import (
	"net"
	"net/netip"
)

// nonPublicIPv4Prefixes covers the IANA IPv4 Special-Purpose Address
// Registry entries that are not ordinary public destinations. Narrow anycast
// exceptions inside a larger non-public aggregate (notably 192.0.0.0/24) are
// blocked conservatively; distinct globally reachable service prefixes such as
// AS112 and AMT are intentionally not included.
var nonPublicIPv4Prefixes = mustPrefixes(
	"0.0.0.0/8",       // This network.
	"10.0.0.0/8",      // RFC 1918 private use.
	"100.64.0.0/10",   // RFC 6598 shared address space.
	"127.0.0.0/8",     // Loopback.
	"169.254.0.0/16",  // Link local.
	"172.16.0.0/12",   // RFC 1918 private use.
	"192.0.0.0/24",    // IETF protocol assignments.
	"192.0.2.0/24",    // Documentation (TEST-NET-1).
	"192.88.99.0/24",  // Deprecated 6to4 relay space.
	"192.168.0.0/16",  // RFC 1918 private use.
	"198.18.0.0/15",   // Benchmarking.
	"198.51.100.0/24", // Documentation (TEST-NET-2).
	"203.0.113.0/24",  // Documentation (TEST-NET-3).
	"224.0.0.0/4",     // Multicast.
	"240.0.0.0/4",     // Reserved and limited broadcast.
)

var (
	globalUnicastIPv6 = netip.MustParsePrefix("2000::/3")
	nonPublicIPv6     = mustPrefixes(
		// Most IPv6 special-use space is outside 2000::/3 and is rejected by
		// the global-unicast gate. These exceptions lie within that aggregate.
		"2001::/23",     // IETF protocol assignments, including Teredo/benchmarking.
		"2001:db8::/32", // Documentation.
		"2002::/16",     // 6to4 transition addresses.
		"3ffe::/16",     // Returned 6bone space.
		"3fff::/20",     // Documentation.
	)
)

// IsPublicIP reports whether ip is suitable as an unrestricted outbound
// destination. It is deliberately stricter than net.IP.IsGlobalUnicast:
// several private, shared, documentation, transition, and reserved ranges are
// classified as global unicast by Go even though they are commonly routed only
// inside an administrative domain.
//
// IPv4-mapped IPv6 addresses are unwrapped before policy evaluation so a
// mapped private address cannot bypass the IPv4 denylist.
func IsPublicIP(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	return IsPublicAddr(addr)
}

// IsPublicAddr is the netip.Addr form of IsPublicIP.
func IsPublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return !prefixContains(nonPublicIPv4Prefixes, addr)
	}
	if !globalUnicastIPv6.Contains(addr) {
		return false
	}
	return !prefixContains(nonPublicIPv6, addr)
}

func prefixContains(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

func mustPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, len(values))
	for i, value := range values {
		prefixes[i] = netip.MustParsePrefix(value)
	}
	return prefixes
}
