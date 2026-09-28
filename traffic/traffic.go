// Package traffic reads network traffic counters per adapter and per process.
package traffic

import (
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

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
	// LinkDown and LinkUp are the link speeds of an adapter in bits per
	// second, zero when unknown.
	LinkDown uint64
	LinkUp   uint64
}

// Adapters returns the cumulative counters of all network adapters.
func Adapters() ([]Counter, error) {
	stats, err := psnet.IOCounters(true)
	if err != nil {
		return nil, err
	}
	ifaces := map[string]net.Interface{}
	if list, err := net.Interfaces(); err == nil {
		for _, iface := range list {
			ifaces[iface.Name] = iface
		}
	}
	counters := make([]Counter, 0, len(stats))
	for _, s := range stats {
		c := Counter{Key: s.Name, Name: s.Name, Recv: s.BytesRecv, Sent: s.BytesSent}
		if iface, ok := ifaces[s.Name]; ok {
			c.Loopback = iface.Flags&net.FlagLoopback != 0
			c.LinkDown, c.LinkUp = linkSpeed(iface)
		}
		counters = append(counters, c)
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

// idleTimeout is how long a process is listed after its last traffic.
const idleTimeout = time.Hour

// processTable accumulates byte counts per process id.
type processTable struct {
	mu    sync.Mutex
	procs map[int]*process
}

type process struct {
	Counter
	lastActive time.Time
}

func newProcessTable() *processTable {
	return &processTable{procs: map[int]*process{}}
}

// add must be called with mu held, name is only called for new processes.
func (t *processTable) add(pid int, name func(pid int) string, recv, sent uint64) {
	if recv+sent == 0 {
		return
	}
	p, ok := t.procs[pid]
	if !ok {
		p = &process{Counter: Counter{PID: pid, Name: name(pid)}}
		p.Key = processKey(p.PID, p.Name)
		t.procs[pid] = p
	}
	p.Recv += recv
	p.Sent += sent
	p.lastActive = time.Now()
}

// list drops the processes that have been idle for idleTimeout, so that the
// table does not grow with every short lived process.
func (t *processTable) list() []Counter {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	counters := make([]Counter, 0, len(t.procs))
	for pid, p := range t.procs {
		if now.Sub(p.lastActive) > idleTimeout {
			delete(t.procs, pid)
			continue
		}
		counters = append(counters, p.Counter)
	}
	return counters
}

func processKey(pid int, name string) string {
	return name + "/" + strconv.Itoa(pid)
}

// unknownName is used for traffic that could not be linked to a process.
const unknownName = "(unknown)"
