//go:build darwin

package traffic

import (
	"net"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linkSpeed reads the baud rate from the interface list of the routing
// sysctl, the same value that ifconfig shows as the media speed.
func linkSpeed(iface net.Interface) (down, up uint64) {
	b, err := unix.SysctlRaw("net.route", 0, 0, unix.NET_RT_IFLIST2, iface.Index)
	if err != nil {
		return 0, 0
	}
	for len(b) >= unix.SizeofIfMsghdr2 {
		m := (*unix.IfMsghdr2)(unsafe.Pointer(&b[0]))
		if m.Msglen == 0 || int(m.Msglen) > len(b) {
			break
		}
		if m.Type == unix.RTM_IFINFO2 && int(m.Index) == iface.Index {
			return m.Data.Baudrate, m.Data.Baudrate
		}
		b = b[m.Msglen:]
	}
	return 0, 0
}
