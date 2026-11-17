// Package etw reads the Windows kernel's network events (Microsoft-Windows-Kernel-Network) in
// real time: every UDP datagram a process sends or receives, with its remote address. Windows
// keeps no remote address for UDP sockets, so this is the only way to see whom a game talks to
// over UDP without capturing packets. Needs admin rights; no dependency beyond x/sys.
package etw

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Packet is one UDP datagram of some process on this computer.
type Packet struct {
	Time   time.Time
	PID    uint32
	Size   uint32
	Recv   bool           // received from Remote; otherwise sent to it
	Remote netip.AddrPort //
	Local  netip.AddrPort //
}

// kernelNetwork is Microsoft-Windows-Kernel-Network.
var kernelNetwork = windows.GUID{Data1: 0x7dd42a49, Data2: 0x5329, Data3: 0x4832, Data4: [8]byte{0x8d, 0xfd, 0x43, 0xd9, 0x79, 0x15, 0x3a, 0x88}}

// Its UDP events: data sent / received over IPv4 and IPv6.
const (
	evUDPSend4 = 42
	evUDPRecv4 = 43
	evUDPSend6 = 58
	evUDPRecv6 = 59
)

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procStartTrace  = advapi32.NewProc("StartTraceW")
	procControl     = advapi32.NewProc("ControlTraceW")
	procEnable      = advapi32.NewProc("EnableTraceEx2")
	procOpenTrace   = advapi32.NewProc("OpenTraceW")
	procProcess     = advapi32.NewProc("ProcessTrace")
	procCloseTrace  = advapi32.NewProc("CloseTrace")
	callbackOnce    sync.Once
	callbackPtr     uintptr
	handlerMu       sync.RWMutex
	handler         func(Packet)
	errAccessDenied = windows.ERROR_ACCESS_DENIED
)

// ErrNotAdmin means the session could not start because whynet is not running as administrator.
var ErrNotAdmin = errors.New("etw: admin rights needed")

// EVENT_TRACE_PROPERTIES with room for the session name after it (x64 layout: 120 bytes).
type traceProps struct {
	WnodeBufferSize   uint32
	WnodeProviderID   uint32
	WnodeHistorical   uint64
	WnodeTimeStamp    int64
	WnodeGUID         windows.GUID
	WnodeClientCtx    uint32
	WnodeFlags        uint32
	BufferSize        uint32
	MinimumBuffers    uint32
	MaximumBuffers    uint32
	MaximumFileSize   uint32
	LogFileMode       uint32
	FlushTimer        uint32
	EnableFlags       uint32
	AgeLimit          int32
	NumberOfBuffers   uint32
	FreeBuffers       uint32
	EventsLost        uint32
	BuffersWritten    uint32
	LogBuffersLost    uint32
	RealTimeBufLost   uint32
	LoggerThreadID    uintptr
	LogFileNameOffset uint32
	LoggerNameOffset  uint32
	name              [128]uint16
}

// EVENT_TRACE_LOGFILEW (x64: 448 bytes); the nested CurrentEvent and LogfileHeader are opaque.
type traceLogfile struct {
	LogFileName         *uint16
	LoggerName          *uint16
	CurrentTime         int64
	BuffersRead         uint32
	ProcessTraceMode    uint32
	_                   [88]byte  // EVENT_TRACE
	_                   [280]byte // TRACE_LOGFILE_HEADER
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

// EVENT_RECORD (x64), only what is read.
type eventRecord struct {
	Size, HeaderType uint16
	Flags, Property  uint16
	ThreadID         uint32
	ProcessID        uint32
	TimeStamp        int64 // FILETIME
	ProviderID       windows.GUID
	ID               uint16
	Version          uint8
	Channel          uint8
	Level            uint8
	Opcode           uint8
	Task             uint16
	Keyword          uint64
	ProcessorTime    uint64
	ActivityID       windows.GUID
	BufferContext    uint32
	ExtendedCount    uint16
	UserDataLength   uint16
	ExtendedData     uintptr
	UserData         *byte
	UserContext      uintptr
}

func init() {
	// The layouts above must match Windows' or every field read is garbage.
	if unsafe.Sizeof(traceLogfile{}) != 448 || unsafe.Offsetof(traceProps{}.name) != 120 || unsafe.Offsetof(eventRecord{}.UserData) != 96 {
		panic("etw: struct layout")
	}
}

// Session is a running real-time trace.
type Session struct {
	name  string
	props *traceProps
	trace uint64
	done  chan struct{}
}

// Start begins a real-time session named name and calls onPacket (from one goroutine) for every
// UDP datagram on this computer. Only one session runs per process.
func Start(name string, onPacket func(Packet)) (*Session, error) {
	callbackOnce.Do(func() { callbackPtr = syscall.NewCallback(onEvent) })
	s := &Session{name: name, done: make(chan struct{})}
	var h uint64
	err := s.startTrace(&h)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) { // left over from a crash: stop it, start again
		s.stopTrace()
		err = s.startTrace(&h)
	}
	if errors.Is(err, errAccessDenied) {
		return nil, ErrNotAdmin
	}
	if err != nil {
		return nil, err
	}
	const levelVerbose, enable = 5, 1
	if r, _, _ := procEnable.Call(uintptr(h), uintptr(unsafe.Pointer(&kernelNetwork)), enable, levelVerbose,
		0x30, 0, 0, 0); r != 0 { // keywords: IPv4 | IPv6
		s.stopTrace()
		return nil, syscall.Errno(r)
	}

	handlerMu.Lock()
	handler = onPacket
	handlerMu.Unlock()
	namePtr, _ := windows.UTF16PtrFromString(name)
	lf := traceLogfile{LoggerName: namePtr, ProcessTraceMode: 0x100 | 0x10000000, EventRecordCallback: callbackPtr} // real time | event record
	r, _, e := procOpenTrace.Call(uintptr(unsafe.Pointer(&lf)))
	if r == ^uintptr(0) { // INVALID_PROCESSTRACE_HANDLE
		s.stopTrace()
		return nil, e
	}
	s.trace = uint64(r)
	go func() {
		runtime.LockOSThread()
		defer close(s.done)
		procProcess.Call(uintptr(unsafe.Pointer(&s.trace)), 1, 0, 0) // blocks until the session stops
	}()
	return s, nil
}

func (s *Session) startTrace(h *uint64) error {
	p := &traceProps{}
	p.WnodeBufferSize = uint32(unsafe.Sizeof(*p))
	p.WnodeFlags = 0x00020000 // WNODE_FLAG_TRACED_GUID
	p.WnodeClientCtx = 1      // QPC timestamps
	p.LogFileMode = 0x00000100 // EVENT_TRACE_REAL_TIME_MODE
	p.FlushTimer = 1           // seconds
	p.BufferSize = 64 // KB
	p.LoggerNameOffset = uint32(unsafe.Offsetof(p.name))
	s.props = p
	namePtr, _ := windows.UTF16PtrFromString(s.name)
	r, _, _ := procStartTrace.Call(uintptr(unsafe.Pointer(h)), uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(p)))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func (s *Session) stopTrace() {
	p := &traceProps{}
	p.WnodeBufferSize = uint32(unsafe.Sizeof(*p))
	p.LoggerNameOffset = uint32(unsafe.Offsetof(p.name))
	namePtr, _ := windows.UTF16PtrFromString(s.name)
	procControl.Call(0, uintptr(unsafe.Pointer(namePtr)), uintptr(unsafe.Pointer(p)), 1) // EVENT_TRACE_CONTROL_STOP
}

// Stop ends the session; no packet arrives after it returns.
func (s *Session) Stop() {
	s.stopTrace()
	procCloseTrace.Call(uintptr(s.trace))
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
	}
	handlerMu.Lock()
	handler = nil
	handlerMu.Unlock()
}

func onEvent(rec *eventRecord) uintptr {
	if rec.ProviderID != kernelNetwork {
		return 0
	}
	var p Packet
	var ok bool
	data := unsafe.Slice(rec.UserData, rec.UserDataLength)
	switch rec.ID {
	case evUDPSend4, evUDPRecv4:
		p, ok = parse(data, 4, rec.ID == evUDPRecv4)
	case evUDPSend6, evUDPRecv6:
		p, ok = parse(data, 16, rec.ID == evUDPRecv6)
	}
	if !ok {
		return 0
	}
	p.Time = time.Unix(0, (rec.TimeStamp-116444736000000000)*100)
	handlerMu.RLock()
	h := handler
	handlerMu.RUnlock()
	if h != nil {
		h(p)
	}
	return 0
}

// parse reads PID, size, daddr, saddr, dport, sport (ports in network order): the packet's own
// destination and source, so the remote end is daddr when sending and saddr when receiving
// (checked live 2026-11-17: DNS answers from 77.88.8.8:53 came with it as saddr).
func parse(b []byte, n int, recv bool) (Packet, bool) {
	if len(b) < 8+2*n+4 {
		return Packet{}, false
	}
	addr := func(x []byte) netip.Addr {
		if n == 4 {
			return netip.AddrFrom4([4]byte(x))
		}
		return netip.AddrFrom16([16]byte(x))
	}
	d, s := addr(b[8:8+n]), addr(b[8+n:8+2*n])
	dp, sp := binary.BigEndian.Uint16(b[8+2*n:]), binary.BigEndian.Uint16(b[10+2*n:])
	p := Packet{PID: binary.LittleEndian.Uint32(b), Size: binary.LittleEndian.Uint32(b[4:]), Recv: recv,
		Remote: netip.AddrPortFrom(d, dp), Local: netip.AddrPortFrom(s, sp)}
	if recv {
		p.Remote, p.Local = p.Local, p.Remote
	}
	return p, true
}
