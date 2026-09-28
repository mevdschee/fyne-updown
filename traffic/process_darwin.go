//go:build darwin

package traffic

/*
#include <sys/param.h>
#include <libproc.h>
*/
import "C"

import (
	"bufio"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/creack/pty"
)

// The macOS monitor reads the output of nettop, which reports cumulative
// bytes per process, followed by its connections, and does not need root. A
// header starts each sample:
//
//	time,,bytes_in,bytes_out,
//	12:00:00.000000,Safari.123,4567,890,
//	12:00:00.000000,tcp4 192.168.1.2:52429<->34.117.65.55:443,4000,800,
//
// nettop runs in a pseudo terminal, as it buffers its output when writing to
// a pipe, which would make the samples arrive late and in bursts.

type darwinMonitor struct {
	cmd   *exec.Cmd
	out   *os.File
	table *processTable
	// last holds the latest totals of each process and conns those of each
	// connection, both guarded by table.mu
	last  map[int]totals
	conns map[connKey]totals
	// samples counts the headers, the first sample only sets the baseline
	samples int
}

type connKey struct {
	pid  int
	conn string
}

type totals struct {
	recv, sent uint64
	seen       time.Time
}

func startProcessMonitor() (ProcessMonitor, error) {
	cmd := exec.Command("/usr/bin/nettop", "-n", "-L", "0", "-s", "1", "-x", "-J", "bytes_in,bytes_out")
	out, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	m := &darwinMonitor{cmd: cmd, out: out, table: newProcessTable(), last: map[int]totals{}, conns: map[connKey]totals{}}
	go m.read(bufio.NewScanner(out))
	return m, nil
}

func (m *darwinMonitor) read(s *bufio.Scanner) {
	nameCol, inCol, outCol := -1, -1, -1
	// the process that the following connections belong to
	pid, name := -1, ""
	for s.Scan() {
		// the terminal ends lines with a carriage return and a newline
		fields := strings.Split(strings.TrimRight(s.Text(), "\r"), ",")
		if idx := indexOf(fields, "bytes_in"); idx >= 0 {
			nameCol, inCol, outCol = indexOf(fields, ""), idx, indexOf(fields, "bytes_out")
			m.table.mu.Lock()
			m.samples++
			m.table.mu.Unlock()
			continue
		}
		if nameCol < 0 || inCol < 0 || outCol < 0 || len(fields) <= max(nameCol, inCol, outCol) {
			continue
		}
		recv, err1 := strconv.ParseUint(fields[inCol], 10, 64)
		sent, err2 := strconv.ParseUint(fields[outCol], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		field := fields[nameCol]
		if proto, remote, ok := parseConnection(field); ok {
			if pid < 0 {
				continue
			}
			names := func(int) string { return processName(pid, name) }
			m.table.mu.Lock()
			key := connKey{pid, field}
			prev, ok := m.conns[key]
			base := m.baseline(prev, ok, recv, sent)
			m.conns[key] = totals{recv, sent, time.Now()}
			m.table.addRemote(pid, names, proto, remote, recv-base.recv, sent-base.sent)
			m.table.mu.Unlock()
			continue
		}
		short, pidText, ok := cutLast(field, ".")
		p, err := strconv.Atoi(pidText)
		if !ok || err != nil {
			pid = -1
			continue
		}
		pid, name = p, short
		names := func(int) string { return processName(p, short) }
		m.table.mu.Lock()
		prev, ok := m.last[pid]
		base := m.baseline(prev, ok, recv, sent)
		m.last[pid] = totals{recv, sent, time.Now()}
		m.table.add(pid, names, recv-base.recv, sent-base.sent)
		m.table.mu.Unlock()
	}
}

// baseline returns the totals to subtract from the new totals of a process
// or connection. nettop reports totals, adding the increase makes idle
// processes expire. A decrease means the pid or connection was reused. The
// first sample holds the traffic from before the start of the app, which is
// not counted. It must be called with table.mu held.
func (m *darwinMonitor) baseline(prev totals, seen bool, recv, sent uint64) totals {
	if !seen && m.samples <= 1 {
		return totals{recv: recv, sent: sent}
	}
	if recv < prev.recv || sent < prev.sent {
		return totals{}
	}
	return prev
}

// processName returns the name of a process, nettop truncates it to the 15
// characters of the short command name, proc_name allows 32.
func processName(pid int, short string) string {
	var buf [2 * C.MAXCOMLEN]C.char
	n := C.proc_name(C.int(pid), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf)))
	if n <= 0 {
		return short
	}
	return C.GoStringN(&buf[0], n)
}

func indexOf(fields []string, name string) int {
	for i, f := range fields {
		if f == name {
			return i
		}
	}
	return -1
}

func (m *darwinMonitor) Processes() []Counter {
	m.table.mu.Lock()
	for pid, t := range m.last {
		if time.Since(t.seen) > idleTimeout {
			delete(m.last, pid)
		}
	}
	for key, t := range m.conns {
		if time.Since(t.seen) > idleTimeout {
			delete(m.conns, key)
		}
	}
	m.table.mu.Unlock()
	return m.table.list()
}

func (m *darwinMonitor) Remotes(pid int) []Counter {
	return m.table.remotes(pid)
}

func (m *darwinMonitor) Close() {
	_ = m.cmd.Process.Kill()
	_ = m.cmd.Wait()
	_ = m.out.Close()
}
