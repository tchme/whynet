package live

import (
	"errors"
	"net/netip"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ICMP through iphlpapi: no admin rights needed (ping.exe and tracert.exe use the same calls).
var (
	iphlpapi        = windows.NewLazySystemDLL("iphlpapi.dll")
	procIcmpCreate  = iphlpapi.NewProc("IcmpCreateFile")
	procIcmpSend    = iphlpapi.NewProc("IcmpSendEcho")
	procIcmpClose   = iphlpapi.NewProc("IcmpCloseHandle")
	errIcmpHandle   = errors.New("IcmpCreateFile failed")
	echoPayload     = make([]byte, 32) // the size ping.exe sends
	replyBufferSize = 256              // one reply, its echoed data and room for an ICMP error
)

// Windows IP_STATUS codes an echo can end with.
const (
	ipSuccess             = 0
	ipDestNetUnreachable  = 11002
	ipDestHostUnreachable = 11003
	ipDestProtUnreachable = 11004
	ipDestPortUnreachable = 11005
	ipReqTimedOut         = 11010
	ipTTLExpiredTransit   = 11013
)

// ipOptionInformation is IP_OPTION_INFORMATION (the TTL of the request).
type ipOptionInformation struct {
	TTL         uint8
	Tos         uint8
	Flags       uint8
	OptionsSize uint8
	OptionsData *byte
}

// icmpEchoReply is ICMP_ECHO_REPLY as a 64-bit process gets it.
type icmpEchoReply struct {
	Address       uint32
	Status        uint32
	RoundTripTime uint32
	DataSize      uint16
	Reserved      uint16
	Data          unsafe.Pointer
	Options       ipOptionInformation
}

// systemEcho sends one ICMP echo request to ip with the given TTL (0: the system default) and
// waits up to timeout. ok is false when nobody answered in time.
// Ping is one echo to ip: its round trip, or false when nothing came back.
func Ping(ip netip.Addr, timeout time.Duration) (time.Duration, bool) {
	r, ok, err := systemEcho(ip, 0, timeout)
	return r.RTT, err == nil && ok && r.Kind == "reply"
}

func systemEcho(ip netip.Addr, ttl int, timeout time.Duration) (r Reply, ok bool, err error) {
	if !ip.Is4() {
		return r, false, errors.New("IPv4 only")
	}
	h, _, callErr := procIcmpCreate.Call()
	if windows.Handle(h) == windows.InvalidHandle {
		return r, false, errors.Join(errIcmpHandle, callErr)
	}
	defer procIcmpClose.Call(h)

	var opts *ipOptionInformation
	if ttl > 0 {
		opts = &ipOptionInformation{TTL: uint8(min(ttl, 255))}
	}
	a := ip.As4()
	dest := uint32(a[0]) | uint32(a[1])<<8 | uint32(a[2])<<16 | uint32(a[3])<<24 // IPAddr: network order in memory
	buf := make([]byte, replyBufferSize)
	start := time.Now()
	n, _, callErr := procIcmpSend.Call(h, uintptr(dest),
		uintptr(unsafe.Pointer(&echoPayload[0])), uintptr(len(echoPayload)),
		uintptr(unsafe.Pointer(opts)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)),
		uintptr(timeout.Milliseconds()))
	elapsed := time.Since(start)

	// A router's "TTL expired" may come back as a reply (n = 1) or as the call's error with the
	// reply still filled in; read the reply either way.
	status := uint32(0)
	if n == 0 {
		var errno windows.Errno
		if !errors.As(callErr, &errno) {
			return r, false, nil
		}
		status = uint32(errno)
	}
	rep := (*icmpEchoReply)(unsafe.Pointer(&buf[0]))
	if n > 0 {
		status = rep.Status
	}
	switch status {
	case ipSuccess:
		r.Kind = "reply"
	case ipTTLExpiredTransit:
		r.Kind = "ttl"
	case ipDestNetUnreachable, ipDestHostUnreachable, ipDestProtUnreachable, ipDestPortUnreachable:
		r.Kind = "unreachable"
	default: // timed out, or nothing usable
		return r, false, nil
	}
	if rep.Address == 0 {
		return r, false, nil
	}
	r.From = netip.AddrFrom4([4]byte{byte(rep.Address), byte(rep.Address >> 8), byte(rep.Address >> 16), byte(rep.Address >> 24)})
	// The API rounds to whole milliseconds; the wall clock keeps the fraction for a sub-ms LAN.
	r.RTT = time.Duration(rep.RoundTripTime) * time.Millisecond
	if rep.RoundTripTime <= 1 && elapsed < 2*time.Millisecond {
		r.RTT = elapsed
	}
	return r, true, nil
}
