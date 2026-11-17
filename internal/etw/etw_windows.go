// Package etw reads Windows events in real time: every UDP datagram a process sends or receives,
// with its remote address (Microsoft-Windows-Kernel-Network; Windows keeps no remote address for
// UDP sockets, so this is the only way to see whom a game talks to over UDP without capturing
// packets), and every frame a program sends to the screen (Microsoft-Windows-DXGI and DxgKrnl
// present events). Needs admin rights; no dependency beyond x/sys.
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

// Frame is one present call: a program handed a finished frame to Windows.
type Frame struct {
	Time time.Time
	PID  uint32
}

// Handlers says what to record: a nil func leaves its provider off. Both are called from one
// goroutine at a time.
type Handlers struct {
	Packet func(Packet)
	Frame  func(Frame)
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

// Frames (checked live 2026-11-17, docs/stability.md): DXGI's Present_Start (42) for DirectX
// 10-12, and DxgKrnl's Present_Info (184), which comes 1:1 with it and also from programs that
// don't go through DXGI (D3D9, OpenGL, Vulkan). Only the header's PID and time are read.
var (
	dxgi    = windows.GUID{Data1: 0xca11c036, Data2: 0x0102, Data3: 0x4a2d, Data4: [8]byte{0xa6, 0xad, 0xf0, 0x3c, 0xfe, 0xd5, 0xd3, 0xc9}}
	dxgkrnl = windows.GUID{Data1: 0x802ec45a, Data2: 0x1e99, Data3: 0x4b83, Data4: [8]byte{0x99, 0x20, 0x87, 0xc9, 0x82, 0x77, 0xba, 0x9d}}
)

const (
	evDXGIPresent   = 42
	evKernelPresent = 184
	kwDXGIEvents    = 0x2       // DXGI's "Events" keyword (Present_Start has it)
	kwKernelPresent = 0x8000000 // DxgKrnl's "Present" keyword
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
	handler         Handlers
	viaDXGI         map[uint32]bool // processes whose frames come through DXGI: their kernel present events are the same frames
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

// Start begins a real-time session named name and calls h's funcs (from one goroutine) for every
// UDP datagram and / or every frame on this computer. Only one session runs per process.
func Start(name string, hs Handlers) (*Session, error) {
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
	type provider struct {
		guid     *windows.GUID
		keywords uintptr
	}
	var on []provider
	if hs.Packet != nil {
		on = append(on, provider{&kernelNetwork, 0x30}) // IPv4 | IPv6
	}
	if hs.Frame != nil {
		on = append(on, provider{&dxgi, kwDXGIEvents}, provider{&dxgkrnl, kwKernelPresent})
	}
	for _, p := range on {
		if r, _, _ := procEnable.Call(uintptr(h), uintptr(unsafe.Pointer(p.guid)), enable, levelVerbose,
			p.keywords, 0, 0, 0); r != 0 {
			s.stopTrace()
			return nil, syscall.Errno(r)
		}
	}

	handlerMu.Lock()
	handler, viaDXGI = hs, map[uint32]bool{}
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
	handler = Handlers{}
	handlerMu.Unlock()
}

func onEvent(rec *eventRecord) uintptr {
	switch rec.ProviderID {
	case kernelNetwork:
		onPacket(rec)
	case dxgi, dxgkrnl:
		onFrame(rec)
	}
	return 0
}

// onFrame passes on one present per frame: DXGI's when the process has them, the kernel's
// otherwise. The first kernel event of a DXGI process may still slip through once.
func onFrame(rec *eventRecord) {
	if rec.ProcessID <= 4 { // idle and System
		return
	}
	handlerMu.Lock() // viaDXGI is written here; ProcessTrace calls back from one thread anyway
	h := handler.Frame
	switch {
	case rec.ProviderID == dxgi && rec.ID == evDXGIPresent:
		viaDXGI[rec.ProcessID] = true
	case rec.ProviderID == dxgkrnl && rec.ID == evKernelPresent && !viaDXGI[rec.ProcessID]:
	default:
		h = nil
	}
	handlerMu.Unlock()
	if h != nil {
		h(Frame{Time: fileTime(rec.TimeStamp), PID: rec.ProcessID})
	}
}

func fileTime(ft int64) time.Time { return time.Unix(0, (ft-116444736000000000)*100) }

func onPacket(rec *eventRecord) {
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
		return
	}
	p.Time = fileTime(rec.TimeStamp)
	handlerMu.RLock()
	h := handler.Packet
	handlerMu.RUnlock()
	if h != nil {
		h(p)
	}
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
