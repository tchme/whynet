package live

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"whynet/internal/probe"
)

var (
	ipRouter = netip.MustParseAddr("192.168.1.1")
	ipISP    = netip.MustParseAddr("100.64.0.1")
	ipCore   = netip.MustParseAddr("10.1.1.1")
	ipServer = netip.MustParseAddr("185.25.182.77")
)

// fakeNet is a route of four nodes: router, ISP edge, core, then whatever is pinged. Nodes in
// silent answer neither pings nor TTL probes (a game server that ignores ICMP).
type fakeNet struct {
	silent map[netip.Addr]bool
	panics map[netip.Addr]bool
}

func (f fakeNet) echo(ip netip.Addr, ttl int, _ time.Duration) (Reply, bool, error) {
	if f.panics[ip] {
		panic("echo broke")
	}
	route := []netip.Addr{ipRouter, ipISP, ipCore}
	if ttl > 0 && ttl <= len(route) && ip != route[ttl-1] {
		return Reply{From: route[ttl-1], RTT: time.Duration(ttl) * time.Millisecond, Kind: "ttl"}, true, nil
	}
	if f.silent[ip] {
		return Reply{}, false, nil
	}
	return Reply{From: ip, RTT: 5 * time.Millisecond, Kind: "reply"}, true, nil
}

func testEnv(f fakeNet, ads []probe.Adapter, best uint32) Env {
	return Env{
		Echo: f.echo,
		WiFi: func(string) (WiFi, bool, error) {
			return WiFi{Signal: 54, Channel: 6, Band: "2.4", SSID: "HomeNet"}, true, nil
		},
		Adapters: func() ([]probe.Adapter, error) { return ads, nil },
		RouteIf:  func(netip.Addr) (uint32, error) { return best, nil },
		Resolve: func(_ context.Context, in string) (probe.Server, error) {
			return probe.Server{IP: netip.MustParseAddr(in)}, nil
		},
		ASN: func(_ context.Context, ip string) (string, string) { return "AS1", "TEST-NET" },
		Local: func(context.Context) probe.Local {
			return probe.Local{Facts: map[string]string{"local.vpn": "false"}, Evidence: map[string]any{"os": "test"}, Mask: map[string]string{}}
		},
		Markers: probe.Markers{VPNAdapters: []string{"warp"}},
		Tick:    2 * time.Millisecond, Timeout: 4 * time.Millisecond,
		TraceEvery: 5, WiFiEvery: 5, StandInAfter: 10, MaxTicks: 100000,
	}
}

var wifiAdapter = []probe.Adapter{{Name: "Wi-Fi", Kind: "wifi", Gateways: []string{"fe80::1", "192.168.1.1"}, Index: 5, GUID: "{G}"}}

func waitTicks(s *Session, n int) {
	for {
		s.mu.Lock()
		got := len(s.samples[0])
		s.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// A server that ignores pings gets its stand-in: the last node of its route that answers.
func TestSessionStandIn(t *testing.T) {
	var mu sync.Mutex
	updates := 0
	s, err := Start(context.Background(), testEnv(fakeNet{silent: map[netip.Addr]bool{ipServer: true}}, wifiAdapter, 5), ipServer.String(),
		func(p Progress) { mu.Lock(); updates++; mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	waitTicks(s, 30)
	s.Mark()
	waitTicks(s, 60)
	res := s.Finish(context.Background())

	if res.Facts["live.server_stand_in"] != ipCore.String() || res.Facts["live.server"] != "stable" {
		t.Errorf("stand-in: facts %v", res.Facts)
	}
	if res.Facts["live.router_kind"] != "wifi" || res.Facts["live.router"] != "stable" || res.Facts["local.vpn"] != "false" {
		t.Errorf("router: facts %v", res.Facts)
	}
	if b := stats(res, Beyond); b.Addr != ipISP || b.Kind != "isp" {
		t.Errorf("beyond: %+v", b.Link)
	}
	if len(res.Trace) < 3 || res.Trace[0].Addr != ipRouter.String() || res.Trace[2].Network != "TEST-NET" {
		t.Errorf("trace: %+v", res.Trace)
	}
	if res.WiFi == nil || res.WiFi.AvgSignal != 54 || res.Mask["HomeNet"] == "" {
		t.Errorf("wifi: %+v mask %v", res.WiFi, res.Mask)
	}
	if len(res.Lags) != 1 || res.Local["os"] != "test" || res.Errors != nil {
		t.Errorf("lags %v local %v errors %v", res.Lags, res.Local, res.Errors)
	}
	mu.Lock()
	defer mu.Unlock()
	if updates == 0 {
		t.Error("no progress sent")
	}
}

func TestSessionStopsByItself(t *testing.T) {
	env := testEnv(fakeNet{}, wifiAdapter, 5)
	env.MaxTicks = 20
	s, err := Start(context.Background(), env, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop after MaxTicks")
	}
	res := s.Finish(context.Background())
	if n := len(res.Chart.Series[Router].Ms); n != 20 || res.Facts["live.server"] != "none" {
		t.Errorf("%d points, facts %v", n, res.Facts)
	}
}

// A ping that panics is a lost answer and a note in the report; the session goes on.
func TestSessionPanicContained(t *testing.T) {
	s, err := Start(context.Background(), testEnv(fakeNet{panics: map[netip.Addr]bool{Controls[CtlWorld]: true}}, wifiAdapter, 5), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitTicks(s, 20)
	res := s.Finish(context.Background())
	if !strings.Contains(res.Errors["ping"], "echo broke") || stats(res, CtlWorld).State != "silent" || res.Facts["live.router"] != "stable" {
		t.Errorf("errors %v facts %v", res.Errors, res.Facts)
	}
}

// A VPN client on this PC: the router is still the physical adapter's gateway; the path beyond is the VPN's.
func TestSessionVPNOnPC(t *testing.T) {
	ads := []probe.Adapter{
		{Name: "CloudflareWARP", Kind: "other", Gateways: []string{"172.16.0.1"}, Index: 9},
		{Name: "Ethernet", Kind: "cable", Gateways: []string{"192.168.1.1"}, Index: 5},
	}
	s, err := Start(context.Background(), testEnv(fakeNet{}, ads, 9), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitTicks(s, 10)
	res := s.Finish(context.Background())
	if r := stats(res, Router); r.Addr != ipRouter || r.Kind != "cable" || res.Facts["live.path_vpn"] != "true" || stats(res, Beyond).Kind != "vpn" {
		t.Errorf("router %+v facts %v beyond %+v", r.Link, res.Facts, stats(res, Beyond).Link)
	}
}

func TestSessionNoRouterAndCancel(t *testing.T) {
	if _, err := Start(context.Background(), testEnv(fakeNet{}, nil, 0), "", nil); !errors.Is(err, ErrNoRouter) {
		t.Errorf("no adapters: %v", err)
	}
	s, err := Start(context.Background(), testEnv(fakeNet{}, wifiAdapter, 5), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitTicks(s, 5)
	s.Cancel()
	s.Cancel() // twice is fine
}

func TestReadRoute(t *testing.T) {
	hops := []Hop{
		{TTL: 1, Addr: "192.168.1.1", Sent: 100, Median: 2},
		{TTL: 2, Addr: "*", Sent: 100, Lost: 100, Note: "silent"},
		{TTL: 3, Addr: "10.48.3.1", Sent: 100, Lost: 40, Median: 5}, // ignores pings to itself
		{TTL: 4, Addr: "188.254.12.33", Sent: 100, Lost: 0, Median: 9},
		{TTL: 5, Addr: "80.1.1.1", Sent: 100, Lost: 10, Median: 48}, // the route goes abroad here
		{TTL: 6, Addr: "80.1.1.2", Sent: 100, Lost: 10, Median: 47},
		{TTL: 7, Addr: "155.133.226.70", Sent: 100, Lost: 11, Median: 52, Note: "target"},
	}
	readRoute(hops)
	if hops[2].LossCounts || hops[2].Note != "rate_limited" {
		t.Errorf("rate limited hop: %+v", hops[2])
	}
	if !hops[4].LossCounts || !hops[4].Jump || hops[3].Jump || hops[5].Jump {
		t.Errorf("loss from hop 5 on, jump at 5: %+v", hops[3:6])
	}
}
