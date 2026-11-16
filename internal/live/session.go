package live

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"runtime/debug"
	"sync"
	"time"

	"whynet/internal/probe"
)

// Env is everything a session touches outside its own logic; tests swap it.
type Env struct {
	Echo     EchoFunc
	WiFi     func(guid string) (WiFi, bool, error)
	CPUTimes func() (idle, total uint64, err error) // the computer's CPU time since boot; nil: not measured
	Adapters func() ([]probe.Adapter, error)
	RouteIf  func(ip netip.Addr) (uint32, error) // the interface the route to ip leaves through
	Resolve  func(ctx context.Context, input string) (probe.Server, error)
	ASN      func(ctx context.Context, ip string) (asn, name string)
	Local    func(ctx context.Context) probe.Local // the computer: OS, public address, adapters
	Markers  probe.Markers

	Tick         time.Duration // between pings of every link: 500 ms
	Timeout      time.Duration // per echo: 1 s
	TraceEvery   int           // ticks between trace rounds: 10 (5 s)
	WiFiEvery    int           // ticks between Wi-Fi readings: 10
	StandInAfter int           // ticks without a word from the server before a stand-in takes its place: 20 (10 s)
	MaxTicks     int           // the session stops by itself: 7200 (1 hour)
}

// LinkNow is one link as the window shows it while recording.
type LinkNow struct {
	ID   string  `json:"id"`
	Addr string  `json:"addr"`
	Ms   float64 `json:"ms"`  // the latest answer, -1 lost
	Own  int     `json:"own"` // its own trouble so far (a running estimate; the final count comes from the analysis)
}

// Progress is sent about once a second while recording.
type Progress struct {
	Seconds int                  `json:"seconds"`
	Links   []LinkNow            `json:"links"`
	New     map[string][]float64 `json:"new"` // answers since the previous progress, -1 lost
	Lags    int                  `json:"lags"`
}

// Session records one stability check.
type Session struct {
	env      Env
	input    string
	server   *probe.Server
	started  time.Time
	pathVPN  bool
	tracer   *tracer
	local    chan probe.Local
	onUpdate func(Progress)

	mu      sync.Mutex
	links   []Link
	pingTo  []netip.Addr // where each link is pinged: its address, or the server's stand-in
	samples [][]int32
	wifi    []WiFi
	wifiErr error
	cpu     []int8 // per tick: the computer's CPU load since the previous tick, %, or -1
	cpuErr  error
	lags    []time.Time
	errs    map[string]string
	running running

	stop     chan struct{}
	stopOnce sync.Once
	loopDone chan struct{}
	done     chan struct{} // closed when the session ended by itself (MaxTicks or ctx)
	inflight sync.WaitGroup
}

type running struct {
	emitted int            // ticks already sent to the window
	own     map[string]int // running estimate of each link's own trouble
}

// ErrNoRouter means no network adapter has a gateway: the computer is not on a network.
var ErrNoRouter = errors.New("no gateway")

// Start finds the chain and starts recording. input may be empty (no server), an IP or a name.
// onUpdate (may be nil) gets a Progress about once a second.
func Start(ctx context.Context, env Env, input string, onUpdate func(Progress)) (*Session, error) {
	s := &Session{env: env, input: input, onUpdate: onUpdate, errs: map[string]string{},
		stop: make(chan struct{}), loopDone: make(chan struct{}), done: make(chan struct{}), local: make(chan probe.Local, 1),
		running: running{own: map[string]int{}}}
	if input != "" {
		srv, err := env.Resolve(ctx, input)
		if err != nil {
			return nil, err
		}
		s.server = &srv
	}
	router, kind, err := s.findRouter()
	if err != nil {
		return nil, err
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.local <- probe.Local{Evidence: map[string]any{"panic": fmt.Sprint(r)}}
			}
		}()
		s.local <- env.Local(ctx)
	}()

	traceTo := Controls[CtlRU]
	if s.server != nil {
		traceTo = s.server.IP
	}
	s.tracer = newTracer(traceTo)
	s.tracer.round(env.Echo, env.Timeout) // the first round finds the node beyond the router

	s.links = []Link{{ID: Router, Addr: router, Kind: kind}}
	if b := s.tracer.firstBeyond(router); b.IsValid() {
		bk := "isp"
		if s.pathVPN {
			bk = "vpn"
		}
		s.links = append(s.links, Link{ID: Beyond, Addr: b, Kind: bk})
	}
	s.links = append(s.links, Link{ID: CtlRU, Addr: Controls[CtlRU]}, Link{ID: CtlWorld, Addr: Controls[CtlWorld]})
	if s.server != nil {
		s.links = append(s.links, Link{ID: Target, Addr: s.server.IP, Name: s.server.Name})
	}
	for _, l := range s.links {
		s.pingTo = append(s.pingTo, l.Addr)
	}
	s.samples = make([][]int32, len(s.links))
	s.started = time.Now()
	go s.loop(ctx)
	return s, nil
}

// findRouter picks the gateway of the physical adapter that carries traffic. With a VPN client on
// this PC the default route goes through the VPN adapter; the router is still the physical
// adapter's gateway, reachable on the LAN, and the path beyond it is the VPN's.
func (s *Session) findRouter() (router netip.Addr, kind string, err error) {
	ads, err := s.env.Adapters()
	if err != nil {
		return router, "", err
	}
	best, _ := s.env.RouteIf(Controls[CtlRU])
	var pick *probe.Adapter
	for i := range ads {
		a := &ads[i]
		if a.Index == best && s.env.Markers.IsVPN(*a) {
			s.pathVPN = true
		}
	}
	for i := range ads {
		a := &ads[i]
		if s.env.Markers.IsVPN(*a) || ipv4Gateway(*a) == "" {
			continue
		}
		if pick == nil || a.Index == best {
			pick = a
		}
	}
	if pick == nil {
		return router, "", ErrNoRouter
	}
	router, err = netip.ParseAddr(ipv4Gateway(*pick))
	return router, pick.Kind, err
}

func ipv4Gateway(a probe.Adapter) string {
	for _, g := range a.Gateways {
		if ip, err := netip.ParseAddr(g); err == nil && ip.Is4() && !ip.IsUnspecified() {
			return g
		}
	}
	return ""
}

func (s *Session) guard(part string) {
	if r := recover(); r != nil {
		s.mu.Lock()
		s.errs[part] = fmt.Sprintf("panic: %v\n%s", r, debug.Stack())
		s.mu.Unlock()
	}
}

func (s *Session) loop(ctx context.Context) {
	defer close(s.loopDone)
	defer s.guard("loop")
	tick := time.NewTicker(s.env.Tick)
	defer tick.Stop()
	routerGUID := ""
	if s.links[0].Kind == "wifi" {
		if ads, err := s.env.Adapters(); err == nil {
			for _, a := range ads {
				if g := ipv4Gateway(a); g == s.links[0].Addr.String() {
					routerGUID = a.GUID
				}
			}
		}
	}
	tracing := false
	var traceMu sync.Mutex
	var cpuPrev [2]uint64
	for t := 0; ; t++ {
		s.ping(t)
		s.readCPU(&cpuPrev)
		if s.env.TraceEvery > 0 && t > 0 && t%s.env.TraceEvery == 0 {
			traceMu.Lock()
			if !tracing {
				tracing = true
				s.inflight.Add(1)
				go func() {
					defer s.inflight.Done()
					defer func() { traceMu.Lock(); tracing = false; traceMu.Unlock() }()
					defer s.guard("trace")
					s.tracer.round(s.env.Echo, s.env.Timeout)
				}()
			}
			traceMu.Unlock()
		}
		if s.links[0].Kind == "wifi" && s.env.WiFiEvery > 0 && t%s.env.WiFiEvery == 0 {
			s.inflight.Add(1)
			go func() {
				defer s.inflight.Done()
				defer s.guard("wifi")
				w, ok, err := s.env.WiFi(routerGUID)
				s.mu.Lock()
				defer s.mu.Unlock()
				if err != nil {
					s.wifiErr = err
				} else if ok {
					s.wifi = append(s.wifi, w)
				}
			}()
		}
		if t == s.env.StandInAfter {
			s.standIn(t)
		}
		if t%2 == 1 && s.onUpdate != nil {
			s.update(t)
		}
		if s.env.MaxTicks > 0 && t+1 >= s.env.MaxTicks {
			close(s.done)
			return
		}
		select {
		case <-s.stop:
			return
		case <-ctx.Done():
			close(s.done)
			return
		case <-tick.C:
		}
	}
}

// ping sends this tick's echo to every link at once; answers land in their slots as they come.
func (s *Session) ping(t int) {
	s.mu.Lock()
	for li := range s.samples {
		s.samples[li] = append(s.samples[li], lostRTT)
	}
	targets := append([]netip.Addr(nil), s.pingTo...)
	s.mu.Unlock()
	for li, ip := range targets {
		s.inflight.Add(1)
		go func() {
			defer s.inflight.Done()
			defer s.guard("ping")
			r, ok, err := s.env.Echo(ip, 0, s.env.Timeout)
			if err != nil || !ok || r.Kind != "reply" {
				return // stays lost
			}
			s.mu.Lock()
			s.samples[li][t] = int32(r.RTT.Microseconds())
			s.mu.Unlock()
		}()
	}
}

// readCPU records the computer's CPU load since the previous tick; -1 for the first tick or a
// failed read. prev holds the previous idle and total times.
func (s *Session) readCPU(prev *[2]uint64) {
	if s.env.CPUTimes == nil {
		return
	}
	pct := int8(-1)
	idle, total, err := s.env.CPUTimes()
	if err == nil {
		if d := total - prev[1]; prev[1] > 0 && total > prev[1] {
			busy := d - min(idle-prev[0], d)
			pct = int8((100*busy + d/2) / d)
		}
		*prev = [2]uint64{idle, total}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.cpuErr = err
	}
	s.cpu = append(s.cpu, pct)
}

// standIn puts the last answering node of the route in place of a server that ignores pings.
func (s *Session) standIn(t int) {
	si := len(s.links) - 1
	if s.server == nil || s.links[si].ID != Target {
		return
	}
	s.mu.Lock()
	answered := false
	for _, v := range s.samples[si][:t] {
		answered = answered || v >= 0
	}
	s.mu.Unlock()
	if answered {
		return
	}
	alt := s.tracer.lastAnswering()
	if !alt.IsValid() || alt == s.links[0].Addr {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links[si].StandIn = alt
	s.pingTo[si] = alt
	for i := range s.samples[si][:t+1] {
		s.samples[si][i] = skipRTT // those pings went to the silent server: not counted
	}
}

// update sends the window what is settled: answers older than the echo timeout.
func (s *Session) update(t int) {
	settled := t + 1 - int(s.env.Timeout/s.env.Tick) // ticks whose answers had their full timeout
	s.mu.Lock()
	p := Progress{Seconds: int(time.Duration(t+1) * s.env.Tick / time.Second), Lags: len(s.lags), New: map[string][]float64{}}
	from := s.running.emitted
	if settled > from {
		trouble := make([]bool, len(s.links))
		for tt := from; tt < settled; tt++ {
			for li, l := range s.links {
				v := s.samples[li][tt]
				trouble[li] = s.troubleNow(li, tt)
				if v != skipRTT {
					p.New[l.ID] = append(p.New[l.ID], msOf(v))
				}
			}
			// A rough "по его вине" for the live view: trouble while every nearer link was calm.
			for li, l := range s.links {
				if !trouble[li] {
					continue
				}
				calm := true
				for _, id := range nearer(l.ID) {
					for lj, n := range s.links {
						if n.ID == id && trouble[lj] {
							calm = false
						}
					}
				}
				if calm {
					s.running.own[l.ID]++
				}
			}
		}
		s.running.emitted = settled
	}
	for li, l := range s.links {
		now := -1.0
		if settled > 0 {
			now = msOf(s.samples[li][settled-1])
		}
		addr := s.pingTo[li].String()
		p.Links = append(p.Links, LinkNow{ID: l.ID, Addr: addr, Ms: now, Own: s.running.own[l.ID]})
	}
	s.mu.Unlock()
	s.onUpdate(p)
}

// troubleNow judges one answer against the link's last two minutes (caller holds mu).
func (s *Session) troubleNow(li, t int) bool {
	v := s.samples[li][t]
	if v == skipRTT {
		return false
	}
	if v == lostRTT {
		return true
	}
	var recent []float64
	for tt := max(0, t-240); tt < t; tt++ {
		if x := s.samples[li][tt]; x >= 0 {
			recent = append(recent, float64(x)/1000)
		}
	}
	if len(recent) < 10 {
		return false
	}
	m := median(recent)
	return float64(v)/1000 > max(3*m, m+50)
}

func msOf(v int32) float64 {
	if v < 0 {
		return -1
	}
	return float64(v/100) / 10
}

// Mark records "лагнуло" now.
func (s *Session) Mark() {
	s.mu.Lock()
	s.lags = append(s.lags, time.Now())
	s.mu.Unlock()
}

// Done is closed when the session ended by itself (after an hour, or when its context ended).
func (s *Session) Done() <-chan struct{} { return s.done }

// Ended is closed when recording stopped, for any reason.
func (s *Session) Ended() <-chan struct{} { return s.loopDone }

// Links are the chain's links as found at the start.
func (s *Session) Links() []Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Link(nil), s.links...)
}

// Cancel stops recording; nothing is analyzed.
func (s *Session) Cancel() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.loopDone
	s.inflight.Wait()
}

// Finish stops recording and reads it.
func (s *Session) Finish(ctx context.Context) Result {
	s.Cancel()
	hops := s.tracer.result(ctx, s.env.ASN)
	s.mu.Lock()
	rec := recording{links: s.links, samples: s.samples, start: s.started, tick: s.env.Tick,
		hops: hops, wifi: s.wifi, cpu: s.cpu, lags: s.lags, pathVPN: s.pathVPN}
	errs := maps.Clone(s.errs)
	if s.wifiErr != nil {
		errs["wifi"] = s.wifiErr.Error()
	}
	if s.cpuErr != nil {
		errs["cpu"] = s.cpuErr.Error()
	}
	s.mu.Unlock()

	res := analyze(rec)
	res.Input, res.Server = s.input, s.server
	res.Mask = map[string]string{}
	select {
	case l := <-s.local:
		res.Local = l.Evidence
		maps.Copy(res.Mask, l.Mask)
		for k, v := range l.Facts {
			if k == "local.vpn" || k == "local.proxy" || k == "local.country" || k == "local.dpi_tool" {
				res.Facts[k] = v
			}
		}
	case <-time.After(3 * time.Second):
		errs["computer"] = "the computer's details did not come in time"
	}
	if res.WiFi != nil && len(res.WiFi.Last.SSID) >= 3 { // a shorter name would mask unrelated text
		res.Mask[res.WiFi.Last.SSID] = "[wifi-name]"
	}
	if len(errs) > 0 {
		res.Errors = errs
	}
	return res
}
