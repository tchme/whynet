package probe

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi           = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtTCPTable = iphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	tcpTableOwnerPIDAll = 5   // TCP_TABLE_OWNER_PID_ALL
	errInsufficient     = 122 // ERROR_INSUFFICIENT_BUFFER
	appPoll             = 250 * time.Millisecond
	appProcsEvery       = 2 * time.Second // re-read the program's processes: helpers start and stop
)

// extTable reads one address family's TCP table with owning processes (no admin rights needed).
func extTable(af uint32) ([]byte, error) {
	size := uint32(64 << 10)
	for range 4 { // the table may grow between the size query and the read
		buf := make([]byte, size)
		r, _, _ := procGetExtTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDAll, 0)
		switch r {
		case 0:
			return buf, nil
		case errInsufficient:
			size += 16 << 10
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, errors.New("GetExtendedTcpTable: the table kept growing")
}

// port turns a table's port (network byte order in the low 16 bits) into a number.
func port(v uint32) uint16 { return uint16(v&0xff)<<8 | uint16(v>>8&0xff) }

// tcpTable reads every TCP connection of this PC, IPv4 and IPv6, with its process.
func tcpTable() ([]tcpRow, error) {
	var rows []tcpRow
	b4, err := extTable(windows.AF_INET)
	if err != nil {
		return nil, err
	}
	type row4 struct{ State, LocalAddr, LocalPort, RemoteAddr, RemotePort, PID uint32 }
	n := *(*uint32)(unsafe.Pointer(&b4[0]))
	for i := range uintptr(n) {
		r := (*row4)(unsafe.Pointer(&b4[4+i*unsafe.Sizeof(row4{})]))
		rows = append(rows, tcpRow{PID: r.PID, State: int(r.State),
			Local:  netip.AddrPortFrom(netip.AddrFrom4(*(*[4]byte)(unsafe.Pointer(&r.LocalAddr))), port(r.LocalPort)),
			Remote: netip.AddrPortFrom(netip.AddrFrom4(*(*[4]byte)(unsafe.Pointer(&r.RemoteAddr))), port(r.RemotePort))})
	}
	b6, err := extTable(windows.AF_INET6)
	if err != nil {
		return rows, nil // IPv4 is enough to go on
	}
	type row6 struct {
		LocalAddr             [16]byte
		LocalScope, LocalPort uint32
		RemoteAddr            [16]byte
		RemoteScope           uint32
		RemotePort, State     uint32
		PID                   uint32
	}
	n = *(*uint32)(unsafe.Pointer(&b6[0]))
	for i := range uintptr(n) {
		r := (*row6)(unsafe.Pointer(&b6[4+i*unsafe.Sizeof(row6{})]))
		rows = append(rows, tcpRow{PID: r.PID, State: int(r.State),
			Local:  netip.AddrPortFrom(netip.AddrFrom16(r.LocalAddr), port(r.LocalPort)),
			Remote: netip.AddrPortFrom(netip.AddrFrom16(r.RemoteAddr), port(r.RemotePort))})
	}
	return rows, nil
}

// listProcs reads every process with its parent, whether it is a system one, and its window title.
func listProcs() ([]proc, error) {
	winDir, _ := windows.GetWindowsDirectory()
	winDir = strings.ToLower(winDir) + `\`
	titles := map[uint32]string{}
	for _, h := range browserWindows(0) {
		var pid uint32
		windows.GetWindowThreadProcessId(h, &pid)
		if _, ok := titles[pid]; !ok {
			buf := make([]uint16, 512)
			if n, _, _ := procGetWindowT.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf))); n > 0 {
				titles[pid] = windows.UTF16ToString(buf[:n])
			}
		}
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var out []proc
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		p := proc{PID: e.ProcessID, PPID: e.ParentProcessID, Exe: windows.UTF16ToString(e.ExeFile[:])}
		path := procPath(p.PID)
		p.System = path == "" || strings.HasPrefix(strings.ToLower(path), winDir)
		p.Title, p.Window = titles[p.PID], titles[p.PID] != ""
		out = append(out, p)
	}
	return out, nil
}

// procPath is a process's exe path, "" when Windows hides it (system and protected processes).
func procPath(pid uint32) string {
	if pid <= 4 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// internetConns counts each process's live connections to the internet.
func internetConns(rows []tcpRow) map[uint32]int {
	out := map[uint32]int{}
	for _, r := range rows {
		if r.State != tcpListen && r.State <= tcpEstab && internet(r.Remote.Addr()) {
			out[r.PID]++
		}
	}
	return out
}

// ListPrograms is the picker's list: running programs with a window or with connections now.
func ListPrograms(browsers []string) ([]Program, error) {
	procs, err := listProcs()
	if err != nil {
		return nil, err
	}
	rows, _ := tcpTable() // without the table the list still works, only without counts
	// Not whynet itself (its WebView helpers count under it).
	self := uint32(os.Getpid())
	return slices.DeleteFunc(programs(procs, internetConns(rows), browsers), func(p Program) bool { return slices.Contains(p.PIDs, self) }), nil
}

// FindProgram finds a running program by name, exe name or PID, the picker's filter off (see
// findProgram); nil when nothing matches.
func FindProgram(target string, browsers []string) (*Program, error) {
	procs, err := listProcs()
	if err != nil {
		return nil, err
	}
	return findProgram(procs, target, browsers), nil
}

// programPIDs finds the processes of the program with this exe right now.
func programPIDs(exe string, browsers []string) map[uint32]bool {
	procs, err := listProcs()
	if err != nil {
		return nil
	}
	p := findProgram(procs, exe, browsers)
	if p == nil {
		return nil
	}
	pids := map[uint32]bool{}
	for _, id := range p.PIDs {
		pids[id] = true
	}
	return pids
}

// RecordApp follows the program's TCP connections until done closes or ctx ends, reading the
// table every 250 ms; progress (may be nil) gets the servers so far about once a second.
func RecordApp(ctx context.Context, exe string, browsers []string, done <-chan struct{}, progress func([]AppServer)) ([]*AppServer, time.Duration, error) {
	start := time.Now()
	pids := programPIDs(exe, browsers)
	if pids == nil {
		return nil, 0, errors.New("the program is not running: " + exe)
	}
	r := newRecorder()
	tick := time.NewTicker(appPoll)
	defer tick.Stop()
	lastProcs, lastProgress := start, start
	for {
		now := time.Now()
		if now.Sub(lastProcs) >= appProcsEvery {
			if p := programPIDs(exe, browsers); p != nil {
				pids = p
			}
			lastProcs = now
		}
		if rows, err := tcpTable(); err == nil {
			r.add(now, rows, pids)
		}
		if progress != nil && now.Sub(lastProgress) >= time.Second {
			progress(r.snapshot(now))
			lastProgress = now
		}
		select {
		case <-ctx.Done():
			return r.result(time.Now()), time.Since(start), nil
		case <-done:
			return r.result(time.Now()), time.Since(start), nil
		case <-tick.C:
		}
	}
}

var (
	dnsapi                   = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsGetCacheDataTable = dnsapi.NewProc("DnsGetCacheDataTable")
	procDnsFree              = dnsapi.NewProc("DnsFree")
)

const dnsQueryNoWire = 0x10 // DNS_QUERY_NO_WIRE_QUERY: answer from the cache only

type dnsCacheEntry struct {
	Next       *dnsCacheEntry
	Name       *uint16
	Type, Size uint16
	Flags      uint32
}

// DNSCacheNames maps addresses to the names the Windows DNS cache holds them for (ip -> name).
// shortcut: newer Windows 11 refuses the cache without admin rights (the author's 26H2 gives
// nothing; `ipconfig /displaydns` says "requires elevation"); the certificate names the server
// then. With a CNAME chain the first name read wins; prefer the asked name if that misleads.
func DNSCacheNames() map[string]string {
	out := map[string]string{}
	var first *dnsCacheEntry
	if r, _, _ := procDnsGetCacheDataTable.Call(uintptr(unsafe.Pointer(&first))); r == 0 {
		return out
	}
	for e := first; e != nil; {
		next := e.Next
		if e.Name != nil && (e.Type == windows.DNS_TYPE_A || e.Type == windows.DNS_TYPE_AAAA) {
			name := strings.ToLower(windows.UTF16PtrToString(e.Name))
			var rec *windows.DNSRecord
			if windows.DnsQuery(name, e.Type, dnsQueryNoWire, nil, &rec, nil) == nil {
				for r := rec; r != nil; r = r.Next {
					var ip netip.Addr
					switch r.Type {
					case windows.DNS_TYPE_A:
						ip = netip.AddrFrom4([4]byte(r.Data[:4]))
					case windows.DNS_TYPE_AAAA:
						ip = netip.AddrFrom16([16]byte(r.Data[:16]))
					}
					if k := ip.String(); ip.IsValid() && out[k] == "" {
						out[k] = name
					}
				}
				windows.DnsRecordListFree(rec, 1) // DnsFreeRecordList
			}
		}
		if e.Name != nil {
			procDnsFree.Call(uintptr(unsafe.Pointer(e.Name)), 0) // DnsFreeFlat
		}
		procDnsFree.Call(uintptr(unsafe.Pointer(e)), 0)
		e = next
	}
	return out
}
