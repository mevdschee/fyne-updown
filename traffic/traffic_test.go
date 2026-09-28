package traffic

import (
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
