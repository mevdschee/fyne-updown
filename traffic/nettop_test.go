package traffic

import "testing"

func TestParseConnection(t *testing.T) {
	tests := []struct {
		in, proto, remote string
		ok                bool
	}{
		{"tcp4 192.168.1.2:52429<->34.117.65.55:443", "tcp", "34.117.65.55:443", true},
		{"udp6 fe80::1%en0.5353<->2001:db8::1.443", "udp", "[2001:db8::1]:443", true},
		{"udp4 *:5353<->*:*", "udp", "invalid AddrPort", true},
		{"Safari.123", "", "invalid AddrPort", false},
	}
	for _, tt := range tests {
		proto, remote, ok := parseConnection(tt.in)
		if proto != tt.proto || remote.String() != tt.remote || ok != tt.ok {
			t.Errorf("parseConnection(%q) = %q, %v, %v", tt.in, proto, remote, ok)
		}
	}
}
