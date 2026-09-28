//go:build darwin

package traffic

import (
	"bufio"
	"os/exec"
	"strconv"
	"strings"
)

// The macOS monitor reads the output of nettop, which reports cumulative
// bytes per process and does not need root:
//
//	time,,bytes_in,bytes_out,
//	12:00:00.000000,Safari.123,4567,890,

type darwinMonitor struct {
	cmd   *exec.Cmd
	table *processTable
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
	m := &darwinMonitor{cmd: cmd, table: newProcessTable()}
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
		// nettop reports totals, store them instead of adding
		m.table.mu.Lock()
		m.table.add(pid, func(int) string { return name }, 0, 0)
		c := m.table.procs[pid]
		c.Recv, c.Sent = recv, sent
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
	return m.table.list()
}

func (m *darwinMonitor) Close() {
	_ = m.cmd.Process.Kill()
	_ = m.cmd.Wait()
}
