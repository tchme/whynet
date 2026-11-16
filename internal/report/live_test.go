package report

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"whynet/internal/live"
	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

// liveResult is the friend's case as the analysis would give it, sized like a 1-hour session.
func liveResult(t0 time.Time) *live.Result {
	res := &live.Result{Input: "185.25.182.77", Server: &probe.Server{IP: netip.MustParseAddr("185.25.182.77")}, Started: t0, Seconds: 3600,
		Facts: map[string]string{"live.router": "unstable", "live.router_kind": "wifi", "live.router_median_ms": "2.0",
			"live.router_p95_ms": "310", "live.router_loss_pct": "3.0", "live.router_periodic_s": "60", "live.seconds": "3600"},
		WiFi:   &live.WiFiSummary{Last: live.WiFi{Signal: 54, RSSI: -71, Channel: 6, Band: "2.4", SSID: "Ivanov_Home"}, AvgSignal: 54, MinSignal: 41, Readings: 360},
		Lags:   []live.LagMark{{At: t0.Add(69 * time.Second), Spike: true, AgoS: 2.5, Link: live.Router, WorstMs: 412}, {At: t0.Add(170 * time.Second)}},
		Local:  map[string]any{"os": "Windows 10 Home 22H2", "public": map[string]string{"ip": "95.24.13.7", "isp": "FLEX-AS", "asn": "AS21453", "country": "RU"}},
		Mask:   map[string]string{"95.24.13.7": "95.24.x.x", "Ivanov_Home": "[wifi-name]"},
		Total:  450,
		Errors: map[string]string{"computer": "late"},
	}
	ids := []string{live.Router, live.Beyond, live.CtlRU, live.CtlWorld, live.Target}
	for i, id := range ids {
		l := live.LinkStats{Link: live.Link{ID: id, Addr: netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})}, State: "stable", Median: 4}
		if id == live.Router {
			l.Kind, l.State, l.Own, l.OwnPct = "wifi", "unstable", 40, 6
		}
		res.Links = append(res.Links, l)
	}
	for ttl := 1; ttl <= 30; ttl++ {
		res.Trace = append(res.Trace, live.Hop{TTL: ttl, Addr: "10.1.1." + string(rune('0'+ttl%10)), Sent: 720, Median: float64(ttl)})
	}
	res.Trace[2].Note = "rate_limited"
	for i := range 200 {
		res.Spikes = append(res.Spikes, live.Episode{At: t0.Add(time.Duration(i) * 18 * time.Second), Link: live.Router, Seconds: 1.5, WorstMs: 400})
	}
	for i := range 60 {
		bk := live.Bucket{At: t0.Add(time.Duration(i) * time.Minute), Links: map[string]live.BucketStats{}}
		for _, id := range ids {
			bk.Links[id] = live.BucketStats{Min: 1.2, Median: 2.5, Max: 480, Lost: 2}
		}
		res.Buckets = append(res.Buckets, bk)
	}
	return res
}

func TestLiveMarkdown(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 11, 16, 12, 38, 2, 0, time.Local)
	res := liveResult(t0)
	v := rules.Verdict{ID: "live_wifi", Confidence: "high"}
	md := LiveMarkdown(res, v, tx, true, liveResult(t0.Add(-time.Hour)))
	for _, want := range []string{tx.Live["report_title"], tx.Live["report_prompt"], tx.LiveDiagnoses["live_wifi"].Title,
		"обычно 2.0 мс", "Роутер · Wi-Fi", "раз в 60 секунд", "Отметка в 12:39:11 — за 2,5 с до неё скакала сеть (Роутер · Wi-Fi, до 412 мс).", "Отметка в 12:40:52 — за 10 секунд до неё сеть не скакала.",
		tx.Live["note_rate_limited"], "…и ещё 250.", tx.Live["report_compare"], "[wifi-name]", "95.24.x.x", "````json"} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	for _, private := range []string{"Ivanov_Home", "95.24.13.7"} {
		if strings.Contains(md, private) {
			t.Errorf("hidden report leaks %q", private)
		}
	}
	if !strings.Contains(LiveMarkdown(res, v, tx, false, nil), "Ivanov_Home") {
		t.Error("hide=false lost the Wi-Fi name")
	}
	// A 1-hour session with the comparison stays small enough to drop into a chat.
	if n := len(md); n > 150<<10 {
		t.Errorf("1-hour report with a comparison is %d KB", n>>10)
	}
	if got := LiveFileName(res); got != "whynet-stability-185.25.182.77-2026-11-16-1238.md" {
		t.Errorf("LiveFileName = %s", got)
	}
	if got := LiveFileName(&live.Result{Started: t0}); got != "whynet-stability-chain-2026-11-16-1238.md" {
		t.Errorf("LiveFileName without a server = %s", got)
	}
}
