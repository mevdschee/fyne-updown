//go:build linux

package traffic

import (
	"net"
	"os"
	"strconv"
	"strings"
)

// linkSpeed reads the negotiated speed in Mbit/s from sysfs. Wireless and
// disconnected adapters do not report one.
func linkSpeed(iface net.Interface) (down, up uint64) {
	b, err := os.ReadFile("/sys/class/net/" + iface.Name + "/speed")
	if err != nil {
		return 0, 0
	}
	mbit, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || mbit <= 0 {
		return 0, 0
	}
	return uint64(mbit) * 1e6, uint64(mbit) * 1e6
}
