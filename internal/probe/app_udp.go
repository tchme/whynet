package probe

import (
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The app check's UDP part (stage 2, docs/appcheck.md): the program's UDP datagrams from the
// Windows kernel's network events (package etw, admin rights), grouped by server. TCP shows
// connects and resets; UDP has none, so what counts is the stream itself: does the server answer
// at all, and does its stream freeze while the program keeps talking.

const (
	udpGapMin     = 250 * time.Millisecond // a freeze is a pause of at least this...
	udpGapTimes   = 5                      // ...and this many times the server's usual interval
	udpGapSends   = 3                      // the program sent at least this many packets during the pause
	udpNoReplyMin = 5                      // sent at least this many with no answer: the server never replied
	udpSteadyPct  = 90                     // a stream with incoming packets in this share of its seconds is steady (a game, a call)
	udpStoppedMin = 2 * time.Second        // quiet to the end of the recording this long while the program sent: the server stopped answering
	maxGapTimes   = 20
)

// UDPServer is one remote address and port the program exchanged UDP with.
type UDPServer struct {
	IP        string `json:"ip"`
	Port      uint16 `json:"port"`
	FakeIP    bool   `json:"fake_ip,omitempty"`
	Sent      int    `json:"sent"`
	Recv      int    `json:"recv"`
	SentBytes int64  `json:"sent_bytes"`
	RecvBytes int64  `json:"recv_bytes"`
	// FirstS, LastS: seconds from the start of the recording.
	FirstS     float64   `json:"first_s"`
	LastS      float64   `json:"last_s"`
	IntervalMs float64   `json:"interval_ms,omitempty"` // the usual pause between incoming packets (average)
	ActivePct  float64   `json:"active_pct"`            // share of its seconds with an incoming packet
	Steady     bool      `json:"steady"`                // incoming packets nearly every second: a game or a call
	Gaps       int       `json:"gaps"`                  // freezes: the server went quiet while the program kept sending
	WorstGapMs float64   `json:"worst_gap_ms,omitempty"`
	GapAtS     []float64 `json:"gap_at_s,omitempty"` // when each freeze started, seconds from the start (up to maxGapTimes)
	NoReply    bool      `json:"no_reply,omitempty"` // the program sent, the server never answered
	Stopped    bool      `json:"stopped,omitempty"`  // it answered, then went quiet for good while the program kept sending (udpStoppedMin+)

	// Filled after the recording.
	Name     string `json:"name,omitempty"`
	NameFrom string `json:"name_from,omitempty"` // cache, ptr
	RealIP   string `json:"real_ip,omitempty"`
	ASN      string `json:"asn,omitempty"`
	Owner    string `json:"owner,omitempty"`
	PingMs   int64  `json:"ping_ms,omitempty"` // ICMP echo, median of the answers
	Lost     int    `json:"lost,omitempty"`
	Tries    int    `json:"tries,omitempty"`
}

// Label is how texts name the server: its name, or its address and port.
func (s *UDPServer) Label() string {
	if s.Name != "" {
		return s.Name
	}
	return netip.AddrPortFrom(netip.MustParseAddr(s.IP), s.Port).String()
}

// UDPRecording is the UDP side of an app check.
type UDPRecording struct {
	State   string       `json:"state"` // off, recorded, declined (the person refused admin rights), failed
	Error   string       `json:"error,omitempty"`
	Servers []*UDPServer `json:"servers,omitempty"` // the busiest first
}

type udpFlow struct {
	srv        *UDPServer
	sent, recv []time.Duration // since start
}

// UDPRecorder collects the program's datagrams as they come (from any goroutine).
type UDPRecorder struct {
	mu      sync.Mutex
	start   time.Time
	pids    map[uint32]bool
	flows   map[netip.AddrPort]*udpFlow
	order   []netip.AddrPort
	packets int
}

func NewUDPRecorder(start time.Time) *UDPRecorder {
	return &UDPRecorder{start: start, flows: map[netip.AddrPort]*udpFlow{}}
}

// SetPIDs replaces the program's processes; datagrams of other processes are dropped.
func (r *UDPRecorder) SetPIDs(pids map[uint32]bool) {
	r.mu.Lock()
	r.pids = pids
	r.mu.Unlock()
}

// Add takes one datagram of any process. DNS (port 53) is not a server of the program.
func (r *UDPRecorder) Add(at time.Time, pid uint32, remote netip.AddrPort, size uint32, recv bool) {
	remote = netip.AddrPortFrom(remote.Addr().Unmap(), remote.Port())
	if !internet(remote.Addr()) || remote.Port() == 53 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.pids[pid] || at.Before(r.start) {
		return
	}
	f := r.flows[remote]
	if f == nil {
		ip := remote.Addr().String()
		f = &udpFlow{srv: &UDPServer{IP: ip, Port: remote.Port(), FakeIP: fakeIP([]string{ip})}}
		r.flows[remote] = f
		r.order = append(r.order, remote)
	}
	r.packets++
	d := at.Sub(r.start)
	if recv {
		f.recv = append(f.recv, d)
		f.srv.RecvBytes += int64(size)
	} else {
		f.sent = append(f.sent, d)
		f.srv.SentBytes += int64(size)
	}
}

// Snapshot is the recording screen's view: each server's packets so far, nothing judged yet.
func (r *UDPRecorder) Snapshot() []UDPServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]UDPServer, 0, len(r.order))
	for _, k := range r.order {
		f := r.flows[k]
		s := *f.srv
		s.Sent, s.Recv = len(f.sent), len(f.recv)
		out = append(out, s)
	}
	return out
}

// Result sums up each server, the busiest first.
func (r *UDPRecorder) Result() []*UDPServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*UDPServer, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, r.flows[k].summary())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sent+out[i].Recv > out[j].Sent+out[j].Recv })
	return out
}

func (f *udpFlow) summary() *UDPServer {
	s := f.srv
	// Packets of one flow can come out of order across CPUs: sort before reading pauses.
	slices.Sort(f.sent)
	slices.Sort(f.recv)
	s.Sent, s.Recv = len(f.sent), len(f.recv)
	first, last := time.Duration(-1), time.Duration(0)
	for _, t := range [][]time.Duration{f.sent, f.recv} {
		if len(t) > 0 {
			if first < 0 || t[0] < first {
				first = t[0]
			}
			last = max(last, t[len(t)-1])
		}
	}
	s.FirstS, s.LastS = sec(first), sec(last)
	s.NoReply = s.Recv == 0 && s.Sent >= udpNoReplyMin
	if s.Recv < 2 {
		return s
	}

	// The average, not the median: a call mixes several speakers' packets into bursts, and the
	// median pause inside a burst is near zero (Discord, live 2026-11-17: 0.1 ms).
	s.IntervalMs = round1(float64(f.recv[len(f.recv)-1]-f.recv[0]) / float64(time.Millisecond) / float64(len(f.recv)-1))
	limit := max(float64(udpGapMin/time.Millisecond), udpGapTimes*s.IntervalMs)
	sentIn := func(a, b time.Duration) int { // sends strictly between a and b
		i, _ := slices.BinarySearch(f.sent, a+1)
		j, _ := slices.BinarySearch(f.sent, b)
		return j - i
	}
	freeze := func(a, b time.Duration) {
		ms := float64(b-a) / float64(time.Millisecond)
		if ms <= limit || sentIn(a, b) < udpGapSends {
			return
		}
		s.Gaps++
		s.WorstGapMs = max(s.WorstGapMs, round1(ms))
		if len(s.GapAtS) < maxGapTimes {
			s.GapAtS = append(s.GapAtS, sec(a))
		}
	}
	for i := 1; i < len(f.recv); i++ {
		freeze(f.recv[i-1], f.recv[i])
	}
	if lr, ls := f.recv[len(f.recv)-1], last; ls > lr { // it stopped answering and never came back
		n := s.Gaps
		freeze(lr, ls+1)
		s.Stopped = s.Gaps > n && ls-lr >= udpStoppedMin
	}

	// Steady: incoming packets in nearly every second of the stream's life.
	secs := map[int]bool{}
	for _, t := range f.recv {
		secs[int(t/time.Second)] = true
	}
	span := int(last/time.Second) - int(first/time.Second) + 1
	s.ActivePct = round1(100 * float64(len(secs)) / float64(span))
	s.Steady = span >= 5 && s.ActivePct >= udpSteadyPct
	return s
}

// checkUDP names the UDP servers (the DNS cache, PTR; certificates need TCP), finds who owns
// their network and pings them (ICMP: a game's own protocol can't be asked), then sums up.
func (p *prober) checkUDP(s *Step, u UDPRecording, cache map[string]string) {
	if u.State != "recorded" || len(u.Servers) == 0 {
		udpFacts(s.Facts, u)
		s.Status = "skip"
		if u.State == "recorded" {
			s.Status = "warn" // recorded, and the program sent no UDP at all
		}
		return
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)
	for _, srv := range u.Servers[:min(len(u.Servers), maxAppServers)] {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() { recover() }() // a name or a ping is evidence: never stops the check
			if n := cache[srv.IP]; n != "" {
				srv.Name, srv.NameFrom = n, "cache"
			}
			if srv.Name == "" && !srv.FakeIP && p.env.PTR != nil {
				ctx, cancel := p.timeout()
				if ptr, err := p.env.PTR(ctx, srv.IP); err == nil && len(ptr) > 0 {
					srv.Name, srv.NameFrom = strings.TrimSuffix(ptr[0], "."), "ptr"
				}
				cancel()
			}
			ip := srv.IP
			if srv.FakeIP {
				ip = ""
				if srv.Name != "" {
					if a, err := p.env.DoH(p.ctx, srv.Name); err == nil && len(a.IPs) > 0 {
						srv.RealIP, ip = a.IPs[0], a.IPs[0]
					}
				}
			}
			if ip != "" && p.env.ASN != nil {
				srv.ASN, srv.Owner = p.env.ASN(p.ctx, ip)
			}
			if p.env.Echo != nil {
				var rtts []float64
				for range pingTries {
					srv.Tries++
					if d, ok := p.env.Echo(netip.MustParseAddr(srv.IP), time.Second); ok {
						rtts = append(rtts, float64(d.Milliseconds()))
					} else {
						srv.Lost++
					}
				}
				if len(rtts) > 0 {
					slices.Sort(rtts)
					srv.PingMs = int64(rtts[len(rtts)/2])
				}
			}
		}()
	}
	wg.Wait()
	udpFacts(s.Facts, u)
	m := u.Servers[0]
	if m.Tries > 0 {
		s.Facts["app.udp_main_ping"] = map[bool]string{true: "silent", false: "answers"}[m.Lost == m.Tries]
		s.Facts["app.udp_main_lost"] = strconv.Itoa(m.Lost)
	}
	switch {
	case s.Facts["app.udp_main_noreply"] == "true":
		s.Status = "fail"
	case s.Facts["app.udp_freezes"] == "true" || s.Facts["app.udp_stopped"] != "0":
		s.Status = "warn"
	}
}

// udpFacts sums up the UDP side for the rules: app.udp is off, recorded, declined or failed.
func udpFacts(facts map[string]string, u UDPRecording) {
	facts["app.udp"] = cmpOr(u.State, "off")
	if u.State != "recorded" {
		return
	}
	noReply, stopped, gaps, worst := 0, 0, 0, 0.0
	freezes := false
	for _, s := range u.Servers {
		if s.NoReply {
			noReply++
		}
		if s.Stopped {
			stopped++
		}
		if s.Steady {
			gaps += s.Gaps
			worst = max(worst, s.WorstGapMs)
			freezes = freezes || s.Gaps >= 2
		}
	}
	facts["app.udp_servers"] = strconv.Itoa(len(u.Servers))
	facts["app.udp_noreply"] = strconv.Itoa(noReply)
	facts["app.udp_stopped"] = strconv.Itoa(stopped)
	facts["app.udp_gaps"] = strconv.Itoa(gaps)
	facts["app.udp_worst_gap_ms"] = strconv.Itoa(int(worst + 0.5))
	facts["app.udp_freezes"] = strconv.FormatBool(freezes)
	if len(u.Servers) > 0 {
		m := u.Servers[0] // the busiest: the game server, the voice server (named by now)
		facts["app.udp_main"] = m.Label()
		facts["app.udp_main_noreply"] = strconv.FormatBool(m.NoReply)
	}
}

func sec(d time.Duration) float64 { return round1(d.Seconds()) }

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
