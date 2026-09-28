package traffic

import (
	"net/netip"
	"testing"
	"time"
)

func TestProcessTableDropsIdle(t *testing.T) {
	table := newProcessTable()
	name := func(int) string { return "test" }
	table.mu.Lock()
	table.add(1, name, 10, 20)
	table.add(2, name, 30, 40)
	table.add(3, name, 0, 0)
	table.procs[1].lastActive = time.Now().Add(-idleTimeout - time.Second)
	table.mu.Unlock()
	counters := table.list()
	if len(counters) != 1 || counters[0].PID != 2 {
		t.Fatalf("list() = %+v, want only pid 2", counters)
	}
	if len(table.procs) != 1 {
		t.Fatalf("table has %d processes, want 1", len(table.procs))
	}
}

func TestProcessTableRemotes(t *testing.T) {
	table := newProcessTable()
	name := func(int) string { return "test" }
	a := netip.MustParseAddrPort("1.2.3.4:443")
	mapped := netip.MustParseAddrPort("[::ffff:1.2.3.4]:443")
	table.mu.Lock()
	table.add(1, name, 30, 3)
	table.addRemote(1, name, "tcp", a, 10, 1)
	table.addRemote(1, name, "tcp", mapped, 20, 2)
	table.addRemote(1, name, "udp", a, 0, 0)
	table.addRemote(1, name, "udp", netip.AddrPort{}, 5, 5)
	table.mu.Unlock()
	remotes := table.remotes(1)
	if len(remotes) != 1 || remotes[0].Remote != a || remotes[0].Recv != 30 || remotes[0].Sent != 3 {
		t.Fatalf("remotes(1) = %+v, want one tcp remote with 30 and 3 bytes", remotes)
	}
	if procs := table.list(); len(procs) != 1 || procs[0].Recv != 30 || procs[0].Sent != 3 {
		t.Fatalf("list() = %+v, remotes must not change the process totals", procs)
	}
}
