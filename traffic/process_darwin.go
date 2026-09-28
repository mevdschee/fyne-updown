//go:build darwin

package traffic

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The macOS monitor reads the output of nettop, which reports cumulative
// bytes per process and does not need root:
//
//	time,,bytes_in,bytes_out,
//	12:00:00.000000,Safari.123,4567,890,

type darwinMonitor struct {
	cmd   *exec.Cmd
	table *processTable
	// last holds the latest totals of each process, guarded by table.mu
	last map[int]totals
}

type totals struct {
	recv, sent uint64
	seen       time.Time
}

func startProcessMonitor() (ProcessMonitor, error) {
	cmd := exec.Command("/usr/bin/nettop", "-P", "-L", "0", "-s", "1", "-x", "-J", "bytes_in,bytes_out")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	m := &darwinMonitor{cmd: cmd, table: newProcessTable(), last: map[int]totals{}}
	go m.read(bufio.NewScanner(out))
	return m, nil
}

func (m *darwinMonitor) read(s *bufio.Scanner) {
	nameCol, inCol, outCol := -1, -1, -1
	for s.Scan() {
		fields := strings.Split(s.Text(), ",")
		if idx := indexOf(fields, "bytes_in"); idx >= 0 {
			nameCol, inCol, outCol = indexOf(fields, ""), idx, indexOf(fields, "bytes_out")
			continue
		}
		if nameCol < 0 || inCol < 0 || outCol < 0 || len(fields) <= max(nameCol, inCol, outCol) {
			continue
		}
		name, pidText, ok := cutLast(fields[nameCol], ".")
		if !ok {
			continue
		}
		pid, err1 := strconv.Atoi(pidText)
		recv, err2 := strconv.ParseUint(fields[inCol], 10, 64)
		sent, err3 := strconv.ParseUint(fields[outCol], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		// nettop reports totals, add the increase so that idle processes
		// expire, a decrease means the pid was reused
		m.table.mu.Lock()
		prev := m.last[pid]
		if recv < prev.recv || sent < prev.sent {
			prev = totals{}
		}
		m.last[pid] = totals{recv, sent, time.Now()}
		m.table.add(pid, func(int) string { return name }, recv-prev.recv, sent-prev.sent)
		m.table.mu.Unlock()
	}
}

func indexOf(fields []string, name string) int {
	for i, f := range fields {
		if f == name {
			return i
		}
	}
	return -1
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

func (m *darwinMonitor) Processes() []Counter {
	m.table.mu.Lock()
	for pid, t := range m.last {
		if time.Since(t.seen) > idleTimeout {
			delete(m.last, pid)
		}
	}
	m.table.mu.Unlock()
	return m.table.list()
}

func (m *darwinMonitor) Close() {
	_ = m.cmd.Process.Kill()
	_ = m.cmd.Wait()
}
