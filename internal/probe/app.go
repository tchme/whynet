package probe

import (
	"crypto/tls"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The app check (docs/appcheck.md): pick a running program, record its TCP connections from the
// Windows table, then check each server it talked to.

const (
	stuckAfter = 3 * time.Second // a connect sent and unanswered this long: the server, or the way to it, is silent
	cutWithin  = 2 * time.Second // established and gone this soon, without a closing handshake: reset
)

// Program is one running program as the picker shows it: every process of one exe.
type Program struct {
	Exe     string   `json:"exe"`             // file name, e.g. Discord.exe
	Name    string   `json:"name"`            // the exe without ".exe"
	Title   string   `json:"title,omitempty"` // a window title: the picker only, never reported (it can be personal)
	Window  bool     `json:"window"`
	Browser bool     `json:"browser"` // a browser: the site check is the right tool
	Conns   int      `json:"conns"`   // TCP connections to the internet right now
	PIDs    []uint32 `json:"-"`
}

// proc is one process as Windows lists it.
type proc struct {
	PID, PPID uint32
	Exe       string // file name
	System    bool   // in the Windows folder, or its path is hidden from us: not a program to pick
	Window    bool
	Title     string
}

// tcpRow is one row of the Windows TCP table.
type tcpRow struct {
	PID           uint32
	State         int
	Local, Remote netip.AddrPort
}

// TCP states (MIB_TCP_STATE).
const (
	tcpListen   = 2
	tcpEstab    = 5
	tcpTimeWait = 11
)

// closing states: a connection seen in one was closed by a handshake, not reset.
func closing(state int) bool { return state > tcpEstab && state <= tcpTimeWait }

// internet: a remote address worth checking (not this PC, not the home network).
func internet(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsValid() && !a.IsUnspecified() && !a.IsLoopback() && !a.IsPrivate() && !a.IsLinkLocalUnicast() && !a.IsMulticast()
}

// programs is the picker's list: programs with a window or with connections now; windowed ones
// first, then by connections, browsers last.
func programs(procs []proc, conns map[uint32]int, browsers []string) []Program {
	var out []Program
	for _, g := range groupPrograms(procs, conns, browsers) {
		if g.Window || g.Conns > 0 {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Browser != b.Browser {
			return !a.Browser
		}
		if a.Window != b.Window {
			return a.Window
		}
		if a.Conns != b.Conns {
			return a.Conns > b.Conns
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return out
}

// findProgram finds a program by name, exe name or PID among every running one, with no window
// and no connections too. A named exe that counts under another program (a console tool started
// from a terminal inside VS Code) becomes a program of its own: the person asked for it by name.
func findProgram(procs []proc, target string, browsers []string) *Program {
	pid, _ := strconv.ParseUint(target, 10, 32)
	groups := groupPrograms(procs, nil, browsers)
	for i, g := range groups {
		if strings.EqualFold(g.Name, target) || strings.EqualFold(g.Exe, target) {
			return &groups[i]
		}
		for _, id := range g.PIDs {
			if pid != 0 && uint64(id) == pid && strings.EqualFold(exeOf(procs, id), g.Exe) {
				return &groups[i] // the program's own process, not one of its helpers
			}
		}
	}
	var g *Program
	for _, p := range procs {
		if p.System || !(strings.EqualFold(p.Exe, target) || strings.EqualFold(p.Exe, target+".exe") || pid != 0 && uint64(p.PID) == pid) {
			continue
		}
		if g == nil {
			g = &Program{Exe: p.Exe, Name: strings.TrimSuffix(strings.TrimSuffix(p.Exe, ".exe"), ".EXE"),
				Browser: slices.Contains(browsers, strings.ToLower(p.Exe))}
		}
		if strings.EqualFold(p.Exe, g.Exe) {
			g.PIDs = append(g.PIDs, p.PID)
			g.Window = g.Window || p.Window
		}
	}
	return g
}

func exeOf(procs []proc, pid uint32) string {
	for _, p := range procs {
		if p.PID == pid {
			return p.Exe
		}
	}
	return ""
}

// groupPrograms groups processes by program. A helper without a window whose ancestor is a
// windowed program (Steam's steamwebhelper) belongs to that program; a game started by a launcher
// has its own window and stays its own program.
func groupPrograms(procs []proc, conns map[uint32]int, browsers []string) []Program {
	byPID := map[uint32]*proc{}
	windowed := map[string]bool{}
	for i := range procs {
		p := &procs[i]
		byPID[p.PID] = p
		if p.Window && !p.System {
			windowed[strings.ToLower(p.Exe)] = true
		}
	}
	owner := func(p *proc) string { // the program a process counts for
		if p.System {
			return ""
		}
		key := strings.ToLower(p.Exe)
		if windowed[key] {
			return key
		}
		// shortcut: PIDs are reused, so a dead parent's PID may now be another process; rare, and
		// the cost is a helper listed under the wrong program. Check creation times if it shows up.
		seen := map[uint32]bool{p.PID: true}
		for q := byPID[p.PPID]; q != nil && !seen[q.PID]; q = byPID[q.PPID] {
			seen[q.PID] = true
			if k := strings.ToLower(q.Exe); windowed[k] && !q.System {
				return k
			}
		}
		return key
	}
	list := map[string]*Program{}
	for i := range procs {
		p := &procs[i]
		key := owner(p)
		if key == "" {
			continue
		}
		g := list[key]
		if g == nil {
			g = &Program{Browser: slices.Contains(browsers, key)}
			list[key] = g
		}
		if strings.EqualFold(p.Exe, key) {
			g.Exe, g.Name = p.Exe, strings.TrimSuffix(strings.TrimSuffix(p.Exe, ".exe"), ".EXE")
			if p.Window && g.Title == "" {
				g.Title, g.Window = p.Title, true
			}
		}
		g.PIDs = append(g.PIDs, p.PID)
		g.Conns += conns[p.PID]
	}
	var out []Program
	for _, g := range list {
		if g.Exe != "" {
			out = append(out, *g)
		}
	}
	return out
}

// AppServer is one remote address and port the program talked to, as recorded.
type AppServer struct {
	IP     string `json:"ip"`
	Port   uint16 `json:"port"`
	FakeIP bool   `json:"fake_ip,omitempty"` // a fake-ip router's address: the real server is behind its proxy
	Conns  int    `json:"conns"`             // separate connections seen
	Stuck  int    `json:"stuck"`             // connects that waited for an answer over stuckAfter
	Cut    int    `json:"cut"`               // established, then gone within cutWithin without a closing handshake
	Open   int    `json:"open"`              // established and fine (still open, or closed by a handshake, or long-lived)
	// LongestS: the longest connection, in seconds.
	LongestS float64 `json:"longest_s"`

	// Filled after the recording.
	Name     string `json:"name,omitempty"`
	NameFrom string `json:"name_from,omitempty"` // cache (Windows DNS cache), cert (the server's certificate), ptr
	TLS      string `json:"tls,omitempty"`       // yes: answers TLS (a certificate or an alert); no: something else or silent
	RealIP   string `json:"real_ip,omitempty"`   // behind a fake-ip router: the name's real address (DoH)
	ASN      string `json:"asn,omitempty"`
	Owner    string `json:"owner,omitempty"` // the network's name

	// whynet's own check of the server (app_check.go).
	Ad      bool   `json:"ad,omitempty"`     // an ad or analytics server: listed, never blamed
	Checked bool   `json:"checked"`          // false: over the limit of servers checked
	Result  string `json:"result,omitempty"` // ok, open (answers; the name can't be tested), sni, closed, ip
	Detail  string `json:"detail,omitempty"`
	PingMs  int64  `json:"ping_ms,omitempty"` // connect time on its own port (a handshake behind fake-ip)
	PingTLS bool   `json:"ping_tls,omitempty"`
	Lost    int    `json:"lost,omitempty"`
	Tries   int    `json:"tries,omitempty"`
	// zapret: the name in its lists, the address in its ipset ("true", "false", "excluded"), the port
	// in its Game Filter's TCP range.
	DPIName  string `json:"dpi_name,omitempty"`
	DPIIP    string `json:"dpi_ip,omitempty"`
	GamePort bool   `json:"game_port,omitempty"`
	Ms       int64  `json:"ms,omitempty"`
}

// Troubled: the program's connects to this server went unanswered or were reset at least as often
// as they worked. One reset among working connections is a program dropping a spare parallel try
// (Telegram races its data centres), not a block: live 2026-11-16, 1 of 6 to 149.154.167.41.
func (s *AppServer) Troubled() bool {
	bad := s.Stuck + s.Cut
	return bad > 0 && bad >= s.Open
}

// Blocked: whynet's check found it cut, or the program had trouble with a server whose name
// can't be tested.
func (s *AppServer) Blocked() bool {
	return s.Result == "sni" || s.Result == "closed" || s.Result == "ip" || s.Result == "open" && s.Troubled()
}

// Label is how texts name the server: its name, or its address and port.
func (s *AppServer) Label() string {
	if s.Name != "" {
		return s.Name
	}
	return netip.AddrPortFrom(netip.MustParseAddr(s.IP), s.Port).String()
}

// conn is one connection being followed across reads of the table.
type conn struct {
	srv          *AppServer
	first, estab time.Time // first seen; first seen established
	last         time.Time
	closed       bool // seen in a closing state: ended by a handshake
	done         bool
}

// recorder follows a program's connections across reads of the TCP table.
type recorder struct {
	conns   map[[2]netip.AddrPort]*conn
	servers map[netip.AddrPort]*AppServer
	order   []netip.AddrPort
}

func newRecorder() *recorder {
	return &recorder{conns: map[[2]netip.AddrPort]*conn{}, servers: map[netip.AddrPort]*AppServer{}}
}

// add takes one read of the whole table; pids are the program's processes now.
func (r *recorder) add(now time.Time, rows []tcpRow, pids map[uint32]bool) {
	present := map[[2]netip.AddrPort]int{} // every row by its two ends, any owner: TIME_WAIT rows have PID 0
	for _, row := range rows {
		present[[2]netip.AddrPort{row.Local, row.Remote}] = row.State
	}
	for _, row := range rows {
		if !pids[row.PID] || row.State == tcpListen || !internet(row.Remote.Addr()) {
			continue
		}
		key := [2]netip.AddrPort{row.Local, row.Remote}
		c := r.conns[key]
		if c == nil || c.done {
			c = &conn{srv: r.server(row.Remote), first: now}
			c.srv.Conns++
			r.conns[key] = c
		}
		c.last = now
		if row.State >= tcpEstab && c.estab.IsZero() {
			c.estab = now
		}
		if closing(row.State) {
			c.closed = true
		}
	}
	for key, c := range r.conns {
		if c.done || c.last.Equal(now) {
			continue
		}
		if st, ok := present[key]; ok && closing(st) { // closed by a handshake, now owned by nobody
			c.closed = true
			continue
		}
		r.finish(c, now)
	}
}

func (r *recorder) server(remote netip.AddrPort) *AppServer {
	remote = netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port())
	s := r.servers[remote]
	if s == nil {
		ip := remote.Addr().String()
		s = &AppServer{IP: ip, Port: remote.Port(), FakeIP: fakeIP([]string{ip})}
		r.servers[remote] = s
		r.order = append(r.order, remote)
	}
	return s
}

// finish classifies a connection that is gone (or still there when the recording ends, gone=end).
func (r *recorder) finish(c *conn, gone time.Time) {
	c.done = true
	s := c.srv
	if life := gone.Sub(c.first).Seconds(); life > s.LongestS {
		s.LongestS = life
	}
	switch {
	case c.estab.IsZero():
		if gone.Sub(c.first) >= stuckAfter {
			s.Stuck++
		}
	case !c.closed && gone.Sub(c.estab) < cutWithin:
		s.Cut++
	default:
		s.Open++
	}
}

// result ends the recording: connections still open are judged as of end.
func (r *recorder) result(end time.Time) []*AppServer {
	for _, c := range r.conns {
		if !c.done {
			if c.estab.IsZero() {
				r.finish(c, end) // still waiting: stuck if it waited long enough
				continue
			}
			c.closed = true // still open at the end: alive, not cut
			r.finish(c, end)
		}
	}
	out := make([]*AppServer, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.servers[k])
	}
	return out
}

// snapshot is the servers so far for the recording screen: a connect still waiting past
// stuckAfter shows as stuck already.
func (r *recorder) snapshot(now time.Time) []AppServer {
	waiting := map[*AppServer]int{}
	for _, c := range r.conns {
		if !c.done && c.estab.IsZero() && now.Sub(c.first) >= stuckAfter {
			waiting[c.srv]++
		}
	}
	out := make([]AppServer, 0, len(r.order))
	for _, k := range r.order {
		s := *r.servers[k]
		s.Stuck += waiting[r.servers[k]]
		out = append(out, s)
	}
	return out
}

// nameServers fills each server's name, whether it speaks TLS, and who owns its network. The
// first source that knows wins: the Windows DNS cache (cache, ip -> name; empty when Windows
// refuses it without admin rights), the certificate the server shows without a name, its PTR.
func (p *prober) nameServers(servers []*AppServer, cache map[string]string) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)
	for _, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() { recover() }() // a name is a bonus: never stops the check
			if n := cache[s.IP]; n != "" {
				s.Name, s.NameFrom = n, "cache"
			}
			names, speaks := p.certNames(s.IP, s.Port)
			s.TLS = map[bool]string{true: "yes", false: "no"}[speaks]
			if s.Name == "" && len(names) > 0 {
				s.Name, s.NameFrom = certName(names), "cert"
			}
			if s.Name == "" && !s.FakeIP && p.env.PTR != nil {
				ctx, cancel := p.timeout()
				if ptr, err := p.env.PTR(ctx, s.IP); err == nil && len(ptr) > 0 {
					s.Name, s.NameFrom = strings.TrimSuffix(ptr[0], "."), "ptr"
				}
				cancel()
			}
			ip := s.IP
			if s.FakeIP {
				ip = ""
				if s.Name != "" {
					if a, err := p.env.DoH(p.ctx, s.Name); err == nil && len(a.IPs) > 0 {
						s.RealIP, ip = a.IPs[0], a.IPs[0]
					}
				}
			}
			if ip != "" && p.env.ASN != nil {
				s.ASN, s.Owner = p.env.ASN(p.ctx, ip)
			}
		}()
	}
	wg.Wait()
}

// certNames asks the server for its certificate without naming a site (no SNI): many show the
// names they serve (Discord: discord.gg, discord.media). speaks: it answered TLS at all, with a
// certificate or with an alert (Cloudflare wants a name first).
func (p *prober) certNames(ip string, port uint16) (names []string, speaks bool) {
	ctx, cancel := p.timeout()
	defer cancel()
	raw, err := p.env.Dial(ctx, "tcp", netip.AddrPortFrom(netip.MustParseAddr(ip), port).String())
	if err != nil {
		return nil, false
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true}) // no ServerName: no SNI
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, tlsClass(err) == "alert"
	}
	cert := c.ConnectionState().PeerCertificates[0]
	names = cert.DNSNames
	if len(names) == 0 && cert.Subject.CommonName != "" {
		names = []string{cert.Subject.CommonName}
	}
	return names, true
}

// certName picks the name to test with: the first exact one, else a wildcard's domain.
func certName(names []string) string {
	for _, n := range names {
		if !strings.HasPrefix(n, "*") {
			return strings.ToLower(n)
		}
	}
	return strings.ToLower(strings.TrimPrefix(names[0], "*."))
}
