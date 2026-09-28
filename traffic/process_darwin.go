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
// bytes per process and does not need root. A header starts each sample:
//
//	time,,bytes_in,bytes_out,
//	12:00:00.000000,Safari.123,4567,890,
//
// nettop runs in a pseudo terminal, as it buffers its output when writing to
// a pipe, which would make the samples arrive late and in bursts.

type darwinMonitor struct {
	cmd   *exec.Cmd
	out   *os.File
	table *processTable
	// last holds the latest totals of each process, guarded by table.mu
	last map[int]totals
	// samples counts the headers, the first sample only sets the baseline
	samples int
}

type totals struct {
	recv, sent uint64
	seen       time.Time
}

func startProcessMonitor() (ProcessMonitor, error) {
	cmd := exec.Command("/usr/bin/nettop", "-P", "-L", "0", "-s", "1", "-x", "-J", "bytes_in,bytes_out")
	out, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	m := &darwinMonitor{cmd: cmd, out: out, table: newProcessTable(), last: map[int]totals{}}
	go m.read(bufio.NewScanner(out))
	return m, nil
}

func (m *darwinMonitor) read(s *bufio.Scanner) {
	nameCol, inCol, outCol := -1, -1, -1
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
		// expire, a decrease means the pid was reused. The first sample holds
		// the traffic from before the start of the app, which is not counted.
		m.table.mu.Lock()
		prev, ok := m.last[pid]
		if !ok && m.samples <= 1 {
			prev = totals{recv: recv, sent: sent}
		}
		if recv < prev.recv || sent < prev.sent {
			prev = totals{}
		}
		m.last[pid] = totals{recv, sent, time.Now()}
		m.table.add(pid, func(int) string { return processName(pid, name) }, recv-prev.recv, sent-prev.sent)
		m.table.mu.Unlock()
	}
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
	_ = m.out.Close()
}
