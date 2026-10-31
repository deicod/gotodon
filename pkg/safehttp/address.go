package safehttp

import "net/netip"

var (
	// nonPublicIPv4 lists the IPv4 special-purpose blocks (IANA registry,
	// RFC 6890 and successors) that are not globally reachable unicast.
	nonPublicIPv4 = mustPrefixes(
		"0.0.0.0/8",       // "this network"
		"10.0.0.0/8",      // private
		"100.64.0.0/10",   // shared address space (carrier-grade NAT)
		"127.0.0.0/8",     // loopback
		"169.254.0.0/16",  // link-local, including cloud metadata endpoints
		"172.16.0.0/12",   // private
		"192.0.0.0/24",    // IETF protocol assignments
		"192.0.2.0/24",    // documentation (TEST-NET-1)
		"192.88.99.0/24",  // deprecated 6to4 relay anycast
		"192.168.0.0/16",  // private
		"198.18.0.0/15",   // benchmarking
		"198.51.100.0/24", // documentation (TEST-NET-2)
		"203.0.113.0/24",  // documentation (TEST-NET-3)
		"224.0.0.0/4",     // multicast
		"240.0.0.0/4",     // reserved, including the limited broadcast address
	)

	// globalUnicastIPv6 is the only IPv6 block allocated for global unicast;
	// everything outside it (loopback, unique local, link-local, multicast,
	// IPv4-compatible, discard, ...) is refused.
	globalUnicastIPv6 = netip.MustParsePrefix("2000::/3")

	// nonPublicIPv6 lists the special-purpose blocks inside 2000::/3 that are
	// not globally reachable.
	nonPublicIPv6 = mustPrefixes(
		"2001::/23",     // IETF protocol assignments, including Teredo and benchmarking
		"2001:db8::/32", // documentation
		"3fff::/20",     // documentation
	)

	// nat64Prefix (RFC 6052) and sixToFourPrefix (RFC 3056) embed an IPv4
	// address that a gateway forwards to, so they are only as public as it.
	nat64Prefix     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFourPrefix = netip.MustParsePrefix("2002::/16")
)

// IsPublicAddr reports whether addr is a globally reachable unicast address,
// the only kind of destination a client from this package connects to.
// IPv4-mapped IPv6 addresses are judged as the IPv4 address they carry.
func IsPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	switch {
	case !addr.IsValid():
		return false
	case addr.Is4():
		return isPublicIPv4(addr)
	default:
		return isPublicIPv6(addr)
	}
}

func isPublicIPv4(addr netip.Addr) bool {
	for _, prefix := range nonPublicIPv4 {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func isPublicIPv6(addr netip.Addr) bool {
	raw := addr.As16()
	switch {
	case nat64Prefix.Contains(addr):
		return isPublicIPv4(netip.AddrFrom4([4]byte(raw[12:16])))
	case sixToFourPrefix.Contains(addr):
		return isPublicIPv4(netip.AddrFrom4([4]byte(raw[2:6])))
	case !globalUnicastIPv6.Contains(addr):
		return false
	}
	for _, prefix := range nonPublicIPv6 {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

func mustPrefixes(prefixes ...string) []netip.Prefix {
	parsed := make([]netip.Prefix, len(prefixes))
	for i, prefix := range prefixes {
		parsed[i] = netip.MustParsePrefix(prefix)
	}
	return parsed
}
