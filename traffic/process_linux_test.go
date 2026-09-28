//go:build linux

package traffic

import (
	"net/netip"
	"testing"
)

func TestParseHexAddr(t *testing.T) {
	tests := map[string]string{
		"0100007F:0035":                         "127.0.0.1:53",
		"00000000:1F90":                         "0.0.0.0:8080",
		"0000000000000000FFFF00000100007F:01BB": "127.0.0.1:443",
		"00000000000000000000000001000000:0016": "[::1]:22",
	}
	for in, want := range tests {
		got, ok := parseHexAddr(in)
		if !ok || got.String() != want {
			t.Errorf("parseHexAddr(%q) = %v, %v, want %v", in, got, ok, want)
		}
	}
}

func TestParsePacket(t *testing.T) {
	// IPv4 TCP from 10.0.0.2:40000 to 1.2.3.4:443
	ipv4 := []byte{
		0x45, 0, 0, 40, 0, 0, 0x40, 0, 64, 6, 0, 0,
		10, 0, 0, 2, 1, 2, 3, 4,
		0x9c, 0x40, 0x01, 0xbb,
	}
	key, ok := parsePacket(ipv4, true)
	want := connKey{6, netip.MustParseAddrPort("10.0.0.2:40000"), netip.MustParseAddrPort("1.2.3.4:443")}
	if !ok || key != want {
		t.Errorf("outgoing ipv4 = %v, want %v", key, want)
	}
	key, _ = parsePacket(ipv4, false)
	if key.local != want.remote || key.remote != want.local {
		t.Errorf("incoming ipv4 = %v", key)
	}

	// IPv6 UDP with a hop-by-hop header, from ::1:5353 to ::2:53
	ipv6 := make([]byte, 40+8+4)
	ipv6[0] = 0x60
	ipv6[6] = 0 // hop-by-hop
	ipv6[23] = 1
	ipv6[39] = 2
	ipv6[40] = 17 // next header UDP, length 0 means 8 bytes
	copy(ipv6[48:], []byte{0x14, 0xe9, 0x00, 0x35})
	key, ok = parsePacket(ipv6, true)
	want = connKey{17, netip.MustParseAddrPort("[::1]:5353"), netip.MustParseAddrPort("[::2]:53")}
	if !ok || key != want {
		t.Errorf("ipv6 = %v, want %v", key, want)
	}
}
