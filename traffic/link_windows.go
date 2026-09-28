//go:build windows

package traffic

import (
	"net"

	"golang.org/x/sys/windows"
)

// linkSpeed reads the receive and transmit speeds, which differ for
// instance on wireless adapters.
func linkSpeed(iface net.Interface) (down, up uint64) {
	row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
	// level 0 is MibIfEntryNormal
	if err := windows.GetIfEntry2Ex(0, &row); err != nil {
		return 0, 0
	}
	// unknown speeds are reported as all ones
	if row.ReceiveLinkSpeed == ^uint64(0) || row.TransmitLinkSpeed == ^uint64(0) {
		return 0, 0
	}
	return row.ReceiveLinkSpeed, row.TransmitLinkSpeed
}
