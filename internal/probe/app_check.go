package probe

import (
	"context"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxAppServers = 20 // checked; the rest are listed

// AppResult is one finished app check: the recording, then whynet's own look at each server.
type AppResult struct {
	Result               // the steps and their facts; Host is the program's name
	Exe     string       `json:"exe"`
	Seconds float64      `json:"seconds"` // how long it was recorded
	Servers []*AppServer `json:"servers"` // in check order: troubled first, ads last
}

// AppStepIDs is the order of the app check's steps.
var AppStepIDs = []string{"local", "record", "names", "check"}

// CheckApp names and checks the servers a recording of exe found. cache is the Windows DNS
// cache (DNSCacheNames; empty when Windows refuses it).
func CheckApp(ctx context.Context, env Env, exe string, servers []*AppServer, recorded time.Duration, cache map[string]string, onStep func(Step)) *AppResult {
	name := strings.TrimSuffix(strings.TrimSuffix(exe, ".exe"), ".EXE")
	p := &prober{ctx: ctx, env: env, onStep: onStep, // no host: the local step checks no site
		res: &Result{Input: exe, Host: name, Started: time.Now().Add(-recorded), Mask: map[string]string{}}}
	p.step("local", p.local)
	p.step("record", func(s *Step) { recordFacts(s, servers) })
	p.step("names", func(s *Step) {
		if len(servers) == 0 {
			s.Status = "skip"
			return
		}
		p.nameServers(servers, cache)
		named := 0
		for _, srv := range servers {
			if srv.Name != "" {
				named++
			}
		}
		s.Facts["app.named"] = strconv.Itoa(named)
		s.Facts["app.dns_cache"] = map[bool]string{true: "on", false: "off"}[len(cache) > 0]
	})
	p.step("check", func(s *Step) { p.checkApp(s, servers) })
	return &AppResult{Result: *p.res, Exe: exe, Seconds: recorded.Seconds(), Servers: servers}
}

// recordFacts sums up what the program itself went through.
func recordFacts(s *Step, servers []*AppServer) {
	conns, stuck, cut, fake := 0, 0, 0, 0
	for _, srv := range servers {
		conns += srv.Conns
		if srv.Stuck > 0 {
			stuck++
		}
		if srv.Cut > 0 {
			cut++
		}
		if srv.FakeIP {
			fake++
		}
	}
	s.Facts["app.servers"] = strconv.Itoa(len(servers))
	s.Facts["app.conns"] = strconv.Itoa(conns)
	s.Facts["app.stuck"] = strconv.Itoa(stuck)
	s.Facts["app.cut"] = strconv.Itoa(cut)
	switch {
	case len(servers) > 0 && fake == len(servers):
		s.Facts["app.fakeip"] = "all"
	case fake > 0:
		s.Facts["app.fakeip"] = "some"
	default:
		s.Facts["app.fakeip"] = "none"
	}
	if len(servers) == 0 || stuck+cut > 0 {
		s.Status = "warn"
	}
}

// checkApp checks the servers, troubled ones first, ads last, and sums up what it found.
func (p *prober) checkApp(s *Step, servers []*AppServer) {
	if len(servers) == 0 {
		s.Status = "skip"
		return
	}
	for _, srv := range servers {
		srv.Ad = srv.Name != "" && p.env.Markers.IsAd(srv.Name)
	}
	sort.SliceStable(servers, func(i, j int) bool {
		a, b := servers[i], servers[j]
		if a.Ad != b.Ad {
			return !a.Ad
		}
		if a.Troubled() != b.Troubled() {
			return a.Troubled()
		}
		return a.Conns > b.Conns
	})
	gameTCP := ""
	if len(p.res.Steps) > 0 {
		if ev, ok := p.res.Steps[0].Evidence["dpi_bypass"].(map[string]any); ok {
			if gf, ok := ev["game_filter"].(map[string]string); ok {
				gameTCP = gf["tcp"]
			}
		}
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)
	for _, srv := range servers[:min(len(servers), maxAppServers)] {
		srv.Checked = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					srv.Result, srv.Detail = "error", "panic"
				}
			}()
			p.checkAppServer(srv, gameTCP)
		}()
	}
	wg.Wait()

	var blocked, troubled []string
	f := map[string]bool{}
	allFake := true
	for _, srv := range servers {
		if srv.Ad || !srv.Checked {
			continue
		}
		if srv.Lost >= minLost {
			f["app.lossy"] = true
		}
		if srv.Troubled() {
			f["app.program_trouble"] = true
			troubled = append(troubled, srv.Label())
		}
		if !srv.Blocked() {
			continue
		}
		blocked = append(blocked, srv.Label())
		allFake = allFake && srv.FakeIP
		switch srv.Result {
		case "sni":
			f["app.sni"] = true
		case "ip":
			f["app.unreachable"] = true
		default: // closed, or the program's own trouble on a server whose name can't be tested
			f["app.maybe"] = true
		}
		if p.dpiLists != nil && srv.DPIName != "true" && srv.DPIIP != "true" {
			f["app.dpi_unlisted"] = true
		}
		if srv.GamePort {
			f["app.game_ports"] = true
		}
	}
	for k, v := range f {
		if v {
			s.Facts[k] = "true"
		}
	}
	s.Facts["app.checked"] = strconv.Itoa(min(len(servers), maxAppServers))
	if len(troubled) > 0 {
		s.Facts["app.trouble_hosts"] = shortList(troubled)
	}
	switch {
	case len(blocked) > 0:
		s.Facts["app.blocked"] = "true"
		s.Facts["app.blocked_hosts"] = shortList(blocked)
		if allFake {
			s.Facts["app.blocked_fakeip"] = "true"
		}
		s.Status = "fail"
	case f["app.lossy"]:
		s.Status = "warn"
	}
}

// checkAppServer checks one server on its own port: does it answer, does it lose packets, is it
// cut by name (when its name is known and it speaks TLS), does zapret cover it.
func (p *prober) checkAppServer(s *AppServer, gameTCP string) {
	start := time.Now()
	defer func() { s.Ms = time.Since(start).Milliseconds() }()
	addr := netip.AddrPortFrom(netip.MustParseAddr(s.IP), s.Port).String()
	if p.dpiLists != nil {
		if s.Name != "" {
			s.DPIName, _ = hostListed(p.dpiLists, s.Name)
		}
		ip := s.IP
		if s.FakeIP {
			ip = s.RealIP // a fake-ip address is in no list
		}
		if ip != "" {
			s.DPIIP, _ = ipListed(p.dpiLists, ip)
		}
		s.GamePort = inPorts(gameTCP, s.Port)
	}

	h := &HostCheck{}
	connect := func() error {
		ctx, cancel := p.timeout()
		defer cancel()
		c, err := p.env.Dial(ctx, "tcp", addr)
		if err == nil {
			c.Close()
		}
		return err
	}
	if err := ping(h, connect); err != nil {
		s.Result, s.Detail = "ip", "connect: "+netClass(err)
		return
	}
	s.PingMs, s.Lost, s.Tries = h.PingMs, h.Lost, h.Tries
	if s.TLS != "yes" || s.Name == "" {
		s.Result = "open" // it answers; without a name and TLS a cut by name can't be tested
		return
	}
	if _, err := p.handshakeAt(addr, s.Name, true); err != nil {
		switch cls := tlsClass(err); cls {
		case "reset", "timeout", "eof", "error":
			_, cerr := p.handshakeAt(addr, controlSNI, true)
			switch c := tlsClass(cerr); {
			case cerr == nil || c == "alert":
				s.Result = "sni" // the server answers another name: the cut is on this one
			case c == "eof":
				s.Result = "closed" // the server closes names it doesn't serve: nothing to compare
			default:
				s.Result = "ip" // cut with any name: blocked by address, or broken
			}
			s.Detail = cls + ": " + err.Error()
		default:
			s.Result, s.Detail = "open", cls // the server itself refuses that name: it answers
		}
		return
	}
	// A fake-ip router answers the connect itself in no time: the way to the server shows in the handshake.
	if s.FakeIP {
		hs := &HostCheck{}
		if ping(hs, func() error { _, err := p.handshakeAt(addr, s.Name, true); return err }) == nil {
			s.PingMs, s.Lost, s.Tries, s.PingTLS = hs.PingMs, hs.Lost, hs.Tries, true
		}
	}
	s.Result = "ok"
}

// inPorts: port is in a zapret port list ("1024-65535", "80,443,1000-2000"); false for "".
func inPorts(list string, port uint16) bool {
	for _, part := range strings.Split(list, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		if !isRange {
			hi = lo
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 == nil && err2 == nil && int(port) >= a && int(port) <= b {
			return true
		}
	}
	return false
}
