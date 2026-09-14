package netcfg

import (
	"fmt"
	"net"
)

// netmaskToPrefix converts a dotted-decimal netmask ("255.255.255.0") to a
// CIDR prefix length (24), the form NetworkManager's ipv4.address-data
// stores. The UI works in netmasks because that's what the brief's mockup
// shows; NetworkManager works in prefixes, so this conversion is centred
// here with its own tests rather than duplicated at each call site.
func netmaskToPrefix(netmask string) (uint32, error) {
	ip := net.ParseIP(netmask).To4()
	if ip == nil {
		return 0, fmt.Errorf("netcfg: invalid netmask %q", netmask)
	}
	mask := net.IPv4Mask(ip[0], ip[1], ip[2], ip[3])
	ones, bits := mask.Size()
	// mask.Size returns bits==0 for a non-contiguous mask (e.g.
	// 255.0.255.0); a legitimate "all zero" mask still reports bits==32,
	// so checking bits alone correctly distinguishes the two cases.
	if bits == 0 {
		return 0, fmt.Errorf("netcfg: netmask %q is not a valid contiguous mask", netmask)
	}
	return uint32(ones), nil
}

// prefixToNetmask is netmaskToPrefix's inverse.
func prefixToNetmask(prefix uint32) (string, error) {
	if prefix > 32 {
		return "", fmt.Errorf("netcfg: invalid prefix length %d", prefix)
	}
	mask := net.CIDRMask(int(prefix), 32)
	return net.IPv4(mask[0], mask[1], mask[2], mask[3]).String(), nil
}
