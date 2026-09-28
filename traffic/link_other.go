//go:build !linux && !windows && !darwin

package traffic

import "net"

func linkSpeed(iface net.Interface) (down, up uint64) {
	return 0, 0
}
