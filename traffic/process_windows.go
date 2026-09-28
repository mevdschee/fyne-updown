//go:build windows

package traffic

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows monitor consumes the Microsoft-Windows-Kernel-Network ETW
// provider in a real-time trace session. Its TCP and UDP send and receive
// events carry the process id and the payload size, which is also what the
// Resource Monitor uses. Starting a trace session requires administrator
// rights.

var (
	advapi32             = windows.NewLazySystemDLL("advapi32.dll")
	procStartTraceW      = advapi32.NewProc("StartTraceW")
	procControlTraceW    = advapi32.NewProc("ControlTraceW")
	procEnableTraceEx2   = advapi32.NewProc("EnableTraceEx2")
	procOpenTraceW       = advapi32.NewProc("OpenTraceW")
	procProcessTrace     = advapi32.NewProc("ProcessTrace")
	procCloseTrace       = advapi32.NewProc("CloseTrace")
	kernelNetworkGUID    = windows.GUID{Data1: 0x7dd42a49, Data2: 0x5329, Data3: 0x4832, Data4: [8]byte{0x8d, 0xfd, 0x43, 0xd9, 0x79, 0x15, 0x3a, 0x88}}
	sessionGUID          = windows.GUID{Data1: 0x5e1b9f0a, Data2: 0x3c2d, Data3: 0x4f6e, Data4: [8]byte{0x9a, 0x61, 0x2b, 0x7c, 0x0d, 0x4e, 0x8f, 0x13}}
	eventRecordCallback  = syscall.NewCallback(onEvent)
	activeMonitor        *windowsMonitor
	errAdministratorOnly = errors.New("per-process traffic needs administrator rights, run the app as administrator")
)

const (
	sessionName = "fyne-updown"

	wnodeFlagTracedGUID          = 0x00020000
	eventTraceRealTimeMode       = 0x00000100
	eventTraceControlStop        = 1
	eventControlCodeEnable       = 1
	traceLevelInformation        = 4
	keywordIPv4                  = 0x10
	keywordIPv6                  = 0x20
	processTraceModeRealTime     = 0x00000100
	processTraceModeEventRecord  = 0x10000000
	invalidProcessTraceHandle    = ^uint64(0)
	errorAlreadyExists           = 183
	errorAccessDenied            = 5
	processQueryLimitedInfo      = 0x1000
	eventTracePropertiesSize     = 120
	eventTracePropertiesNameSize = 2 * 1024
)

// eventTraceProperties is EVENT_TRACE_PROPERTIES followed by room for the
// logger name and log file name.
type eventTraceProperties struct {
	// WNODE_HEADER
	BufferSize        uint32
	ProviderID        uint32
	HistoricalContext uint64
	TimeStamp         int64
	GUID              windows.GUID
	ClientContext     uint32
	Flags             uint32
	// EVENT_TRACE_PROPERTIES
	BufferSizeKB        uint32
	MinimumBuffers      uint32
	MaximumBuffers      uint32
	MaximumFileSize     uint32
	LogFileMode         uint32
	FlushTimer          uint32
	EnableFlags         uint32
	AgeLimit            int32
	NumberOfBuffers     uint32
	FreeBuffers         uint32
	EventsLost          uint32
	BuffersWritten      uint32
	LogBuffersLost      uint32
	RealTimeBuffersLost uint32
	LoggerThreadID      uintptr
	LogFileNameOffset   uint32
	LoggerNameOffset    uint32
	names               [eventTracePropertiesNameSize]byte
}

// eventTraceLogfile is EVENT_TRACE_LOGFILEW, the nested structures that are
// not used are kept as byte arrays of the right size.
type eventTraceLogfile struct {
	LogFileName         *uint16
	LoggerName          *uint16
	CurrentTime         int64
	BuffersRead         uint32
	ProcessTraceMode    uint32
	CurrentEvent        [88]byte
	LogfileHeader       [280]byte
	BufferCallback      uintptr
	BufferSize          uint32
	Filled              uint32
	EventsLost          uint32
	_                   uint32
	EventRecordCallback uintptr
	IsKernelTrace       uint32
	_                   uint32
	Context             uintptr
}

// eventRecord is EVENT_RECORD.
type eventRecord struct {
	Size           uint16
	HeaderType     uint16
	Flags          uint16
	EventProperty  uint16
	ThreadID       uint32
	ProcessID      uint32
	TimeStamp      int64
	ProviderID     windows.GUID
	ID             uint16
	Version        uint8
	Channel        uint8
	Level          uint8
	Opcode         uint8
	Task           uint16
	Keyword        uint64
	ProcessorTime  uint64
	ActivityID     windows.GUID
	BufferContext  uint32
	ExtendedCount  uint16
	UserDataLength uint16
	ExtendedData   uintptr
	UserData       unsafe.Pointer
	UserContext    uintptr
}

func init() {
	// guard against layout mistakes, these are the sizes on 64 bit Windows
	if unsafe.Sizeof(uintptr(0)) == 8 {
		if unsafe.Offsetof(eventTraceProperties{}.names) != eventTracePropertiesSize ||
			unsafe.Sizeof(eventTraceLogfile{}) != 448 ||
			unsafe.Sizeof(eventRecord{}) != 112 {
			panic("traffic: unexpected ETW structure layout")
		}
	}
}

type windowsMonitor struct {
	session uint64
	trace   uint64
	props   *eventTraceProperties
	table   *processTable
}

func newProperties() *eventTraceProperties {
	p := &eventTraceProperties{}
	p.BufferSize = uint32(unsafe.Sizeof(*p))
	p.GUID = sessionGUID
	p.ClientContext = 1 // query performance counter timestamps
	p.Flags = wnodeFlagTracedGUID
	p.LogFileMode = eventTraceRealTimeMode
	p.FlushTimer = 1
	p.LoggerNameOffset = eventTracePropertiesSize
	return p
}

func startProcessMonitor() (ProcessMonitor, error) {
	name, _ := windows.UTF16PtrFromString(sessionName)
	m := &windowsMonitor{props: newProperties(), table: newProcessTable()}
	r, _, _ := procStartTraceW.Call(uintptr(unsafe.Pointer(&m.session)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(m.props)))
	if r == errorAlreadyExists {
		// left over from a previous run that did not exit cleanly
		stopSession()
		m.props = newProperties()
		r, _, _ = procStartTraceW.Call(uintptr(unsafe.Pointer(&m.session)), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(m.props)))
	}
	if r == errorAccessDenied {
		return nil, errAdministratorOnly
	}
	if r != 0 {
		return nil, fmt.Errorf("StartTrace: %w", syscall.Errno(r))
	}
	r, _, _ = procEnableTraceEx2.Call(uintptr(m.session), uintptr(unsafe.Pointer(&kernelNetworkGUID)),
		eventControlCodeEnable, traceLevelInformation, keywordIPv4|keywordIPv6, 0, 0, 0)
	if r != 0 {
		stopSession()
		return nil, fmt.Errorf("EnableTraceEx2: %w", syscall.Errno(r))
	}
	logfile := &eventTraceLogfile{
		LoggerName:          name,
		ProcessTraceMode:    processTraceModeRealTime | processTraceModeEventRecord,
		EventRecordCallback: eventRecordCallback,
	}
	r, _, _ = procOpenTraceW.Call(uintptr(unsafe.Pointer(logfile)))
	if uint64(r) == invalidProcessTraceHandle {
		stopSession()
		return nil, errors.New("OpenTrace failed")
	}
	m.trace = uint64(r)
	activeMonitor = m
	go func() {
		runtime.LockOSThread()
		// blocks until the session is stopped
		procProcessTrace.Call(uintptr(unsafe.Pointer(&m.trace)), 1, 0, 0)
	}()
	return m, nil
}

func stopSession() {
	name, _ := windows.UTF16PtrFromString(sessionName)
	procControlTraceW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(newProperties())), eventTraceControlStop)
}

func (m *windowsMonitor) Close() {
	procCloseTrace.Call(uintptr(m.trace))
	stopSession()
}

// onEvent handles TcpIp and UdpIp events, their payload starts with the
// process id and the size as 32 bit integers.
func onEvent(rec *eventRecord) uintptr {
	m := activeMonitor
	if m == nil || rec.UserDataLength < 8 {
		return 0
	}
	var sent, ipv6 bool
	switch rec.ID {
	case 10, 42: // TCP and UDP IPv4 send
		sent = true
	case 26, 58: // TCP and UDP IPv6 send
		sent, ipv6 = true, true
	case 11, 43: // TCP and UDP IPv4 receive
	case 27, 59: // TCP and UDP IPv6 receive
		ipv6 = true
	default:
		return 0
	}
	data := unsafe.Slice((*byte)(rec.UserData), rec.UserDataLength)
	// the destination address follows, skip loopback traffic like the
	// adapter counters on the other platforms do
	if !ipv6 && len(data) >= 12 && data[8] == 127 {
		return 0
	}
	if ipv6 && len(data) >= 24 && string(data[8:24]) == string(net.IPv6loopback) {
		return 0
	}
	pid := int(uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24)
	size := uint64(uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24)
	m.table.mu.Lock()
	if sent {
		m.table.add(pid, processName, 0, size)
	} else {
		m.table.add(pid, processName, size, 0)
	}
	m.table.mu.Unlock()
	return 0
}

func (m *windowsMonitor) Processes() []Counter {
	return m.table.list()
}

func processName(pid int) string {
	switch pid {
	case 0:
		return unknownName
	case 4:
		return "System"
	}
	h, err := windows.OpenProcess(processQueryLimitedInfo, false, uint32(pid))
	if err != nil {
		return "pid " + fmt.Sprint(pid)
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "pid " + fmt.Sprint(pid)
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}
