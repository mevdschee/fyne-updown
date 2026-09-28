// Package traffic reads network traffic counters per adapter and per process.
package traffic

import (
	"net"
	"sort"
	"strconv"
	"sync"

	psnet "github.com/shirou/gopsutil/v4/net"
)

// Counter holds cumulative byte counts for an adapter or a process.
type Counter struct {
	Key  string
	Name string
	PID  int
	Recv uint64
	Sent uint64
	// Loopback is set for loopback adapters.
	Loopback bool
}

// Adapters returns the cumulative counters of all network adapters.
func Adapters() ([]Counter, error) {
	stats, err := psnet.IOCounters(true)
	if err != nil {
		return nil, err
	}
	loopback := map[string]bool{}
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			loopback[iface.Name] = iface.Flags&net.FlagLoopback != 0
		}
	}
	counters := make([]Counter, 0, len(stats))
	for _, s := range stats {
		counters = append(counters, Counter{
			Key:      s.Name,
			Name:     s.Name,
			Recv:     s.BytesRecv,
			Sent:     s.BytesSent,
			Loopback: loopback[s.Name],
		})
	}
	sort.Slice(counters, func(i, j int) bool { return counters[i].Name < counters[j].Name })
	return counters, nil
}

// ProcessMonitor collects cumulative traffic counters per process.
type ProcessMonitor interface {
	// Processes returns the cumulative counters of all processes that had
	// traffic since the monitor was started.
	Processes() []Counter
	Close()
}

// StartProcessMonitor starts the platform specific per-process monitor. It
// usually needs elevated privileges, the error explains what is missing.
func StartProcessMonitor() (ProcessMonitor, error) {
	return startProcessMonitor()
}

// processTable accumulates byte counts per process id.
type processTable struct {
	mu    sync.Mutex
	procs map[int]*Counter
}

func newProcessTable() *processTable {
	return &processTable{procs: map[int]*Counter{}}
}

// add must be called with mu held, name is only called for new processes.
func (t *processTable) add(pid int, name func(pid int) string, recv, sent uint64) {
	c, ok := t.procs[pid]
	if !ok {
		c = &Counter{PID: pid, Name: name(pid)}
		c.Key = processKey(c.PID, c.Name)
		t.procs[pid] = c
	}
	c.Recv += recv
	c.Sent += sent
}

func (t *processTable) list() []Counter {
	t.mu.Lock()
	defer t.mu.Unlock()
	counters := make([]Counter, 0, len(t.procs))
	for _, c := range t.procs {
		counters = append(counters, *c)
	}
	return counters
}

func processKey(pid int, name string) string {
	return name + "/" + strconv.Itoa(pid)
}

// unknownName is used for traffic that could not be linked to a process.
const unknownName = "(unknown)"
