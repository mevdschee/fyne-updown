//go:build linux

package traffic

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// The Linux monitor works like nethogs: it captures all packets on a raw
// AF_PACKET socket, groups them by connection and links each connection to a
// process by looking up the socket inode in /proc/net/{tcp,udp}[6] and the
// owner of that inode in /proc/<pid>/fd.

type connKey struct {
	proto  uint8
	local  netip.AddrPort
	remote netip.AddrPort
}

type conn struct {
	pid      int // -1 while unresolved
	recv     uint64
	sent     uint64
	lastSeen time.Time
}

type linuxMonitor struct {
	fd     int
	mu     sync.Mutex
	conns  map[connKey]*conn
	table  *processTable
	closed chan struct{}
}

func startProcessMonitor() (ProcessMonitor, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		if errors.Is(err, unix.EPERM) {
			exe, _ := os.Executable()
			return nil, fmt.Errorf("per-process traffic needs raw socket access, run: sudo setcap cap_net_raw,cap_sys_ptrace,cap_dac_read_search+ep %s", exe)
		}
		return nil, err
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 4<<20)
	// wake up regularly so Close can stop the capture loop
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Sec: 1})
	m := &linuxMonitor{
		fd:     fd,
		conns:  map[connKey]*conn{},
		table:  newProcessTable(),
		closed: make(chan struct{}),
	}
	go m.capture()
	return m, nil
}

func htons(v uint16) uint16 {
	return v<<8 | v>>8
}

func (m *linuxMonitor) Close() {
	close(m.closed)
}

func (m *linuxMonitor) capture() {
	defer unix.Close(m.fd)
	buf := make([]byte, 128)
	for {
		select {
		case <-m.closed:
			return
		default:
		}
		// MSG_TRUNC makes recvfrom return the real packet length
		n, from, err := unix.Recvfrom(m.fd, buf, unix.MSG_TRUNC)
		if err != nil {
			continue
		}
		ll, ok := from.(*unix.SockaddrLinklayer)
		if !ok || ll.Hatype == unix.ARPHRD_LOOPBACK {
			continue
		}
		var outgoing bool
		switch ll.Pkttype {
		case unix.PACKET_OUTGOING:
			outgoing = true
		case unix.PACKET_HOST, unix.PACKET_BROADCAST, unix.PACKET_MULTICAST:
		default:
			continue
		}
		key, ok := parsePacket(buf[:min(n, len(buf))], outgoing)
		if !ok {
			continue
		}
		m.count(key, uint64(n), outgoing)
	}
}

func (m *linuxMonitor) count(key connKey, size uint64, outgoing bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[key]
	if !ok {
		c = &conn{pid: -1}
		m.conns[key] = c
	}
	if outgoing {
		c.sent += size
	} else {
		c.recv += size
	}
	c.lastSeen = time.Now()
}

// parsePacket reads the protocol, addresses and ports from an IP packet.
func parsePacket(b []byte, outgoing bool) (connKey, bool) {
	var key connKey
	var src, dst netip.Addr
	var off int
	if len(b) < 1 {
		return key, false
	}
	switch b[0] >> 4 {
	case 4:
		if len(b) < 20 {
			return key, false
		}
		key.proto = b[9]
		src = netip.AddrFrom4([4]byte(b[12:16]))
		dst = netip.AddrFrom4([4]byte(b[16:20]))
		off = int(b[0]&0x0f) * 4
		// fragments other than the first carry no ports
		if binary.BigEndian.Uint16(b[6:8])&0x1fff != 0 {
			off = len(b)
		}
	case 6:
		if len(b) < 40 {
			return key, false
		}
		key.proto = b[6]
		src = netip.AddrFrom16([16]byte(b[8:24]))
		dst = netip.AddrFrom16([16]byte(b[24:40]))
		off = 40
		// skip extension headers
	ext:
		for off+8 <= len(b) {
			switch key.proto {
			case 0, 43, 60: // hop-by-hop, routing, destination options
				key.proto, off = b[off], off+(int(b[off+1])+1)*8
			case 44: // fragment
				key.proto, off = b[off], off+8
			default:
				break ext
			}
		}
	default:
		return key, false
	}
	var sport, dport uint16
	if (key.proto == unix.IPPROTO_TCP || key.proto == unix.IPPROTO_UDP) && off+4 <= len(b) {
		sport = binary.BigEndian.Uint16(b[off : off+2])
		dport = binary.BigEndian.Uint16(b[off+2 : off+4])
	}
	if outgoing {
		key.local, key.remote = netip.AddrPortFrom(src, sport), netip.AddrPortFrom(dst, dport)
	} else {
		key.local, key.remote = netip.AddrPortFrom(dst, dport), netip.AddrPortFrom(src, sport)
	}
	return key, true
}

func (m *linuxMonitor) Processes() []Counter {
	m.mu.Lock()
	unresolved := false
	for _, c := range m.conns {
		if c.pid < 0 && c.recv+c.sent > 0 {
			unresolved = true
			break
		}
	}
	m.mu.Unlock()

	// read the socket tables outside the lock, it touches many files
	var sockets *socketTable
	var owners map[uint64]int
	if unresolved {
		sockets = readSocketTables()
		owners = socketOwners()
	}

	m.mu.Lock()
	m.table.mu.Lock()
	now := time.Now()
	for key, c := range m.conns {
		if c.pid < 0 && sockets != nil {
			if pid, ok := owners[sockets.lookup(key)]; ok {
				c.pid = pid
			}
		}
		if c.recv+c.sent > 0 {
			// unresolved traffic is kept until the connection expires
			pid := c.pid
			if pid >= 0 || now.Sub(c.lastSeen) > 10*time.Second {
				if pid < 0 {
					pid = 0
				}
				m.table.add(pid, processName, c.recv, c.sent)
				c.recv, c.sent = 0, 0
			}
		}
		if now.Sub(c.lastSeen) > 2*time.Minute {
			delete(m.conns, key)
		}
	}
	m.table.mu.Unlock()
	m.mu.Unlock()
	return m.table.list()
}

func processName(pid int) string {
	if pid == 0 {
		return unknownName
	}
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "pid " + strconv.Itoa(pid)
	}
	return strings.TrimSpace(string(comm))
}

type localKey struct {
	proto uint8
	local netip.AddrPort
}

type portKey struct {
	proto uint8
	port  uint16
}

// socketTable maps connections to socket inodes.
type socketTable struct {
	conns  map[connKey]uint64
	locals map[localKey]uint64
	ports  map[portKey]uint64
}

// lookup returns the inode of the socket that handles the connection, trying
// connected sockets first, then bound sockets and finally wildcard sockets.
func (t *socketTable) lookup(key connKey) uint64 {
	key.local = netip.AddrPortFrom(key.local.Addr().Unmap(), key.local.Port())
	key.remote = netip.AddrPortFrom(key.remote.Addr().Unmap(), key.remote.Port())
	if inode, ok := t.conns[key]; ok {
		return inode
	}
	if inode, ok := t.locals[localKey{key.proto, key.local}]; ok {
		return inode
	}
	return t.ports[portKey{key.proto, key.local.Port()}]
}

func readSocketTables() *socketTable {
	t := &socketTable{
		conns:  map[connKey]uint64{},
		locals: map[localKey]uint64{},
		ports:  map[portKey]uint64{},
	}
	for _, f := range []struct {
		name  string
		proto uint8
	}{
		{"tcp", unix.IPPROTO_TCP}, {"tcp6", unix.IPPROTO_TCP},
		{"udp", unix.IPPROTO_UDP}, {"udp6", unix.IPPROTO_UDP},
	} {
		readSocketTable("/proc/net/"+f.name, f.proto, t)
	}
	return t
}

// readSocketTable parses lines like:
// sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode
// 0: 0100007F:0035 00000000:0000 0A 00000000:00000000 00:00000000 00000000 0 0 12345
func readSocketTable(path string, proto uint8, t *socketTable) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Scan() // header
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 10 {
			continue
		}
		local, ok1 := parseHexAddr(fields[1])
		remote, ok2 := parseHexAddr(fields[2])
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if !ok1 || !ok2 || err != nil || inode == 0 {
			continue
		}
		if remote.Port() != 0 {
			t.conns[connKey{proto, local, remote}] = inode
		}
		if local.Addr().IsUnspecified() {
			t.ports[portKey{proto, local.Port()}] = inode
		} else {
			t.locals[localKey{proto, local}] = inode
		}
	}
}

// parseHexAddr parses "0100007F:0035", the address is stored as 32 bit words
// in host (little endian) byte order.
func parseHexAddr(s string) (netip.AddrPort, bool) {
	addr, port, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	b, err := hex.DecodeString(addr)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	ip, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(ip.Unmap(), uint16(p)), true
}

// socketOwners maps socket inodes to process ids by scanning /proc/*/fd.
func socketOwners() map[uint64]int {
	owners := map[uint64]int{}
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join("/proc", p.Name(), "fd")
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(dir, fd.Name()))
			if err != nil || !strings.HasPrefix(link, "socket:[") {
				continue
			}
			inode, err := strconv.ParseUint(link[8:len(link)-1], 10, 64)
			if err == nil {
				owners[inode] = pid
			}
		}
	}
	return owners
}
