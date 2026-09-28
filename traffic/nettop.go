package traffic

import (
	"net/netip"
	"strconv"
	"strings"
)

// The parsing of the nettop output of the macOS monitor lives here, so that
// it can be tested on all platforms.

// parseConnection reads a connection like "tcp4 10.0.0.2:52429<->1.2.3.4:443"
// or "tcp6 fe80::1%en0.52429<->2001:db8::1.443", unconnected sockets have
// "*:*" as the remote end.
func parseConnection(s string) (proto string, remote netip.AddrPort, ok bool) {
	kind, addrs, ok := strings.Cut(s, " ")
	if !ok {
		return "", remote, false
	}
	_, remoteText, ok := strings.Cut(addrs, "<->")
	if !ok {
		return "", remote, false
	}
	proto = strings.TrimRight(kind, "46")
	if r, err := netip.ParseAddrPort(remoteText); err == nil {
		return proto, r, true
	}
	addrText, portText, _ := cutLast(remoteText, ".")
	addr, err1 := netip.ParseAddr(addrText)
	port, err2 := strconv.ParseUint(portText, 10, 16)
	if err1 != nil || err2 != nil {
		return proto, netip.AddrPort{}, true
	}
	return proto, netip.AddrPortFrom(addr.WithZone(""), uint16(port)), true
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}
