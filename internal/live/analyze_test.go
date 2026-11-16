package live

import (
	"encoding/json"
	"math/rand"
	"net/netip"
	"strings"
	"testing"
	"time"

	"whynet/rules"
)

var t0 = time.Date(2026, 11, 16, 12, 38, 2, 0, time.Local)

// synth builds a recording of n ticks (500 ms): base answers per link with a little noise,
// then trouble painted on top. trouble returns extra ms for a link at a tick, or -1 for a loss.
func synth(n int, seed int64, links []Link, base map[string]float64, trouble func(id string, t int) float64) recording {
	rnd := rand.New(rand.NewSource(seed))
	r := recording{links: links, start: t0, tick: 500 * time.Millisecond}
	for _, l := range links {
		s := make([]int32, n)
		for t := range n {
			v := base[l.ID] + rnd.Float64()*base[l.ID]*0.2
			if trouble != nil {
				switch x := trouble(l.ID, t); {
				case x < 0:
					s[t] = lostRTT
					continue
				default:
					v += x
				}
			}
			s[t] = int32(v * 1000)
		}
		r.samples = append(r.samples, s)
	}
	return r
}

func chainLinks(routerKind string, server bool) []Link {
	ls := []Link{
		{ID: Router, Addr: netip.MustParseAddr("192.168.1.1"), Kind: routerKind},
		{ID: Beyond, Addr: netip.MustParseAddr("100.64.0.1"), Kind: "isp"},
		{ID: CtlRU, Addr: Controls[CtlRU]},
		{ID: CtlWorld, Addr: Controls[CtlWorld]},
	}
	if server {
		ls = append(ls, Link{ID: Target, Addr: netip.MustParseAddr("185.25.182.77")})
	}
	return ls
}

var bases = map[string]float64{Router: 2, Beyond: 4, CtlRU: 6, CtlWorld: 7, Target: 38}

func stats(res Result, id string) LinkStats {
	for _, s := range res.Links {
		if s.ID == id {
			return s
		}
	}
	return LinkStats{}
}

func wantFacts(t *testing.T, res Result, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if res.Facts[k] != v {
			t.Errorf("%s = %q, want %q (facts %v)", k, res.Facts[k], v, res.Facts)
		}
	}
}

// The friend's case: Wi-Fi to the router jumps to 250-480 ms for ~1.5 s every 60 s; every link
// behind repeats it. Only the router is to blame, and the rhythm is the background scan.
func TestAnalyzeFriendWiFi(t *testing.T) {
	spike := func(t int) bool { return t%120 >= 30 && t%120 < 33 }
	r := synth(370, 1, chainLinks("wifi", true), bases, func(id string, t int) float64 {
		if spike(t) {
			return 250 + float64(t%5)*55
		}
		return 0
	})
	// On a spike; 2 s after one (a late press, the usual case); with no jump in the 10 s before.
	r.lags = []time.Time{t0.Add(31 * 500 * time.Millisecond), t0.Add(78 * time.Second), t0.Add(90 * time.Second)}
	res := analyze(r)
	wantFacts(t, res, map[string]string{"live.router": "unstable", "live.router_kind": "wifi", "live.beyond": "stable",
		"live.internet": "stable", "live.server": "stable", "live.router_periodic_s": "60", "live.seconds": "185",
		"live.lag_marks": "3", "live.lag_calm": ""})
	if s := stats(res, Target); s.Own != 0 || s.Events == 0 {
		t.Errorf("server: own %d events %d; it should only repeat the router", s.Own, s.Events)
	}
	if s := stats(res, Router); s.Own != 9 || s.Worst < 250 { // 3 spells of 3 ticks in 3 minutes
		t.Errorf("router: own %d worst %.0f", s.Own, s.Worst)
	}
	if res.Total != 3 || res.Spikes[0].Link != Router || res.Spikes[0].Seconds != 1.5 {
		t.Errorf("episodes: %d %+v", res.Total, res.Spikes)
	}
	if m := res.Lags[0]; !m.Spike || m.Link != Router || m.AgoS != 0 || m.WorstMs < 250 {
		t.Errorf("mark on a spike: %+v", m)
	}
	if m := res.Lags[1]; !m.Spike || m.Link != Router || m.AgoS != 2 {
		t.Errorf("late mark: %+v", m)
	}
	if m := res.Lags[2]; m.Spike {
		t.Errorf("mark with no jump before it: %+v", m)
	}
	if s := res.Chart.Series[Target]; len(s.Ms) != 370 || s.State[31] != 2 || res.Chart.Series[Router].State[31] != 1 {
		t.Errorf("chart: server state %d, router state %d", s.State[31], res.Chart.Series[Router].State[31])
	}
}

func TestAnalyzeWhereItLives(t *testing.T) {
	every := func(t int) bool { return t%25 == 7 } // 4 % of the moments
	cases := []struct {
		name    string
		trouble func(id string, t int) float64
		want    map[string]string
	}{
		{"calm", nil, map[string]string{"live.router": "stable", "live.beyond": "stable", "live.internet": "stable", "live.server": "stable"}},
		{"isp", func(id string, t int) float64 {
			if every(t) && id != Router {
				return 200
			}
			return 0
		}, map[string]string{"live.router": "stable", "live.beyond": "unstable", "live.internet": "stable", "live.server": "stable"}},
		{"internet", func(id string, t int) float64 {
			if every(t) && id != Router && id != Beyond {
				return -1
			}
			return 0
		}, map[string]string{"live.router": "stable", "live.beyond": "stable", "live.internet": "unstable"}},
		{"route", func(id string, t int) float64 {
			if every(t) && id == Target {
				return 300
			}
			return 0
		}, map[string]string{"live.router": "stable", "live.internet": "stable", "live.server": "unstable"}},
		// The node beyond the router ignores 40 % of pings to itself, everything behind it is fine:
		// rate limiting, not loss.
		{"beyond rate-limits", func(id string, t int) float64 {
			if id == Beyond && t%5 < 2 {
				return -1
			}
			return 0
		}, map[string]string{"live.beyond": "stable"}},
		// The router answers its own pings slowly now and then, traffic through it is fine.
		{"router slow to answer", func(id string, t int) float64 {
			if id == Router && every(t) {
				return 150
			}
			return 0
		}, map[string]string{"live.router": "stable"}},
		{"router silent", func(id string, t int) float64 {
			if id == Router {
				return -1
			}
			return 0
		}, map[string]string{"live.router": "silent"}},
	}
	for _, c := range cases {
		res := analyze(synth(240, 2, chainLinks("cable", true), bases, c.trouble))
		for k, v := range c.want {
			if res.Facts[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, res.Facts[k], v)
			}
		}
	}
}

func TestAnalyzeShortAndNoServer(t *testing.T) {
	res := analyze(synth(40, 3, chainLinks("cable", false), bases, nil))
	wantFacts(t, res, map[string]string{"live.too_short": "true", "live.server": "none", "live.seconds": "20"})
	if len(res.Buckets) != 4 {
		t.Errorf("20 s in 5 s buckets: %d", len(res.Buckets))
	}
}

// A VPN's TUN on this PC answers the internet's pings itself (a friend's Throne, 2026-11-17): the
// controls come back faster than the router. A real path, even a fast one, does not.
func TestAnalyzeFakeAnswers(t *testing.T) {
	fake := map[string]float64{Router: 0.5, Beyond: 4, CtlRU: 0.05, CtlWorld: 0.05}
	res := analyze(synth(400, 5, chainLinks("cable", false), fake, nil))
	wantFacts(t, res, map[string]string{"live.fake_answers": "true"})
	res = analyze(synth(400, 5, chainLinks("cable", false), bases, nil))
	wantFacts(t, res, map[string]string{"live.fake_answers": ""})
}

// The computer's lane: a calm network while the CPU sat at 100 % is measured, and a lag mark
// carries the CPU load of the 10 s it looks back over.
func TestAnalyzeCPU(t *testing.T) {
	r := synth(400, 6, chainLinks("cable", false), bases, nil)
	r.cpu = make([]int8, 400)
	for i := range r.cpu {
		r.cpu[i] = 20
	}
	r.cpu[0] = -1
	for i := 300; i < 340; i++ { // 20 s at 100 %
		r.cpu[i] = 100
	}
	r.lags = []time.Time{r.start.Add(165 * time.Second), r.start.Add(50 * time.Second)} // tick 330, tick 100
	res := analyze(r)
	if res.CPU == nil || res.CPU.Max != 100 || res.CPU.Readings != 399 || res.CPU.BusyPct != 10 || res.CPU.Avg != 28 {
		t.Fatalf("summary %+v", res.CPU)
	}
	wantFacts(t, res, map[string]string{"live.cpu_max": "100", "live.cpu_busy_pct": "10.0", "live.internet": "stable"})
	if res.Lags[0].CPU != 100 || res.Lags[1].CPU != 20 {
		t.Errorf("lag marks: %d, %d", res.Lags[0].CPU, res.Lags[1].CPU)
	}
	if b := res.Buckets[30]; b.CPU != 100 { // 150-155 s
		t.Errorf("bucket at 150 s: %d", b.CPU)
	}
	if c := res.Chart.CPU; len(c) != 400 || c[0] != -1 || c[310] != 100 {
		t.Errorf("chart: %d points", len(c))
	}
	// Not measured: no summary, no lane, -1 in marks and buckets.
	r.cpu = nil
	res = analyze(r)
	if res.CPU != nil || res.Chart.CPU != nil || res.Lags[0].CPU != -1 || res.Buckets[0].CPU != -1 {
		t.Errorf("without CPU: %+v %v %d", res.CPU, res.Chart.CPU, res.Lags[0].CPU)
	}
}

// A long session stays small: about 60 buckets, at most maxPoints chart points.
func TestAnalyzeLongSession(t *testing.T) {
	res := analyze(synth(7200, 4, chainLinks("cable", true), bases, nil))
	if len(res.Buckets) < 55 || len(res.Buckets) > 65 {
		t.Errorf("1 hour: %d buckets", len(res.Buckets))
	}
	if n := len(res.Chart.Series[Router].Ms); n > maxPoints {
		t.Errorf("1 hour: %d chart points", n)
	}
}

func TestPercentile(t *testing.T) {
	v := []float64{5, 1, 4, 2, 3}
	if median(v) != 3 || percentile(v, 95) != 5 || percentile(nil, 50) != 0 {
		t.Error("percentile")
	}
}

// From the recording to the verdict: the analysis and rules/live.yaml speak the same facts.
func TestAnalyzeToVerdict(t *testing.T) {
	rs, err := rules.LoadLive()
	if err != nil {
		t.Fatal(err)
	}
	every := func(t int) bool { return t%25 == 7 }
	cases := []struct {
		kind    string
		trouble func(id string, t int) float64
		want    string
	}{
		{"wifi", func(id string, t int) float64 {
			if every(t) {
				return 300
			}
			return 0
		}, "live_wifi"},
		{"cable", func(id string, t int) float64 {
			if every(t) {
				return -1
			}
			return 0
		}, "live_lan"},
		{"cable", func(id string, t int) float64 {
			if every(t) && id != Router {
				return 200
			}
			return 0
		}, "live_isp"},
		{"cable", func(id string, t int) float64 {
			if every(t) && id == Target {
				return 300
			}
			return 0
		}, "live_route"},
		{"wifi", nil, "live_stable"},
	}
	for _, c := range cases {
		res := analyze(synth(240, 5, chainLinks(c.kind, true), bases, c.trouble))
		if got := rules.Match(rs, res.Facts); got.ID != c.want {
			t.Errorf("%s: got %s, want %s (facts %v)", c.want, got.ID, c.want, res.Facts)
		}
	}
}

// The window reads the chart from JSON: states must be a list of numbers, not a base64 string.
func TestChartJSON(t *testing.T) {
	res := analyze(synth(40, 6, chainLinks("cable", false), bases, nil))
	b, err := json.Marshal(res.Chart.Series[Router])
	if err != nil || !strings.Contains(string(b), `"state":[0,`) {
		t.Errorf("chart series JSON: %s (%v)", b[:min(len(b), 120)], err)
	}
}
