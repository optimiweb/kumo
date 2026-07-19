package httpx

import (
	"fmt"
	"net"
	"net/netip"
)

// ValidateAddrs rejects empty sets and any unsafe address.
func ValidateAddrs(addrs []netip.Addr) error {
	if len(addrs) == 0 {
		return fmt.Errorf("dns_failed")
	}
	for _, a := range addrs {
		if IsUnsafeAddr(a) {
			return fmt.Errorf("address_denied")
		}
	}
	return nil
}

// IsUnsafeAddr reports whether addr is disallowed for public crawl egress.
func IsUnsafeAddr(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	if a.Is4In6() {
		a = a.Unmap()
	}
	return isUnsafeIP(net.IP(a.AsSlice()))
}

func isUnsafeIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2 {
			return true
		}
		if ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100 {
			return true
		}
		if ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 {
			return true
		}
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
	} else {
		if len(ip) >= 1 && (ip[0]&0xfe) == 0xfc {
			return true
		}
		if len(ip) >= 4 && ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8 {
			return true
		}
	}
	return false
}

// PickFirstSafe returns the first address after validating the full set.
func PickFirstSafe(addrs []netip.Addr) (netip.Addr, error) {
	if err := ValidateAddrs(addrs); err != nil {
		return netip.Addr{}, err
	}
	return addrs[0], nil
}
