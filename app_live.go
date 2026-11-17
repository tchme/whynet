package main

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whynet/internal/live"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

// The stability check in the window. The session sends "live-tick" about once a second; a
// session that stops by itself (1 hour) sends "live-done" with the result.

// liveTones colour the stability verdicts; anything not listed is "bad".
var liveTones = map[string]string{
	"live_stable": "ok", "live_too_short": "warn", "live_router_silent": "warn",
	"live_wifi": "warn", "live_lan": "warn", "live_vpn": "warn", "live_pc_cpu": "warn", "live_pc_frames": "warn",
}

type LiveLinkView struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Addr    string  `json:"addr"`
	StandIn string  `json:"stand_in,omitempty"`
	State   string  `json:"state"`
	Median  float64 `json:"median"`
	P95     float64 `json:"p95"`
	Worst   float64 `json:"worst"`
	LossPct float64 `json:"loss_pct"`
	Own     int     `json:"own"`
	Events  int     `json:"events"`
}

type HopView struct {
	TTL     int     `json:"ttl"`
	Addr    string  `json:"addr"`
	Network string  `json:"network"`
	Lost    int     `json:"lost"`
	Sent    int     `json:"sent"`
	Median  float64 `json:"median"`
	Worst   float64 `json:"worst"`
	Note    string  `json:"note"`
	Bad     bool    `json:"bad"` // its loss is real, or the delay jumps here
}

type LagView struct {
	At    float64 `json:"at"` // seconds from the start
	Time  string  `json:"time"`
	Spike bool    `json:"spike"` // the network jumped within 10 s before the mark
	Text  string  `json:"text"`  // a one-liner: the jump before it, or none
	Back  float64 `json:"back"`  // seconds the mark looks back (drawn on the chart)
}

type CompareRow struct {
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
	Better bool   `json:"better"`
	Worse  bool   `json:"worse"`
}

type CompareView struct {
	Link string       `json:"link"`
	Rows []CompareRow `json:"rows"`
}

type ChartView struct {
	Start  int64                       `json:"start"` // unix ms
	Step   float64                     `json:"step"`
	Series map[string]live.ChartSeries `json:"series"`
	CPU    []int                       `json:"cpu,omitempty"`   // CPU load % per point, -1 unknown
	FPS    []float64                   `json:"fps,omitempty"`   // the game's lowest fps per point, -1 not in front
	Stall  []int                       `json:"stall,omitempty"` // the game's stalls per point
}

type LiveView struct {
	Verdict    VerdictView         `json:"verdict"`
	Extras     []string            `json:"extras"`
	Seconds    int                 `json:"seconds"`
	Links      []LiveLinkView      `json:"links"`
	Trace      []HopView           `json:"trace"`
	TraceTitle string              `json:"trace_title"`
	WiFi       *live.WiFiSummary   `json:"wifi,omitempty"`
	CPU        *live.CPUSummary    `json:"cpu,omitempty"`
	Frames     *live.FramesSummary `json:"frames,omitempty"` // the game's frames; nil when not recorded or no game
	Lags       []LagView           `json:"lags"`
	Chart      ChartView           `json:"chart"`
	Compare    *CompareView        `json:"compare,omitempty"`
	Note       string              `json:"note,omitempty"` // the server was found through DoH
}

type LiveStartView struct {
	Links  []LiveLinkView `json:"links"`
	Hotkey bool           `json:"hotkey"` // Ctrl+Alt+L is ours; false: another program holds it
	Frames string         `json:"frames"` // the game's frames: off, recorded, declined, failed
}

// StartLive starts a stability check; target may be empty, an IP or a name. With compare, the
// previous stability check is kept to show before / after. With frames, the game's frames are
// recorded too: Windows asks for admin rights first (UAC), and StartLive returns after the answer.
func (a *App) StartLive(target string, compare, frames bool) (*LiveStartView, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, errors.New(a.t.UI["busy"])
	}
	a.running = true
	a.mu.Unlock()

	env := live.DefaultEnv(a.markers)
	if frames {
		env.Frames = live.SystemFrames
	}
	s, err := live.Start(a.ctx, env, target, func(p live.Progress) {
		runtime.EventsEmit(a.ctx, "live-tick", p)
	})
	if err != nil {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
		switch {
		case errors.Is(err, probe.ErrBadInput):
			return nil, errors.New(a.t.UI["bad_input"])
		case errors.Is(err, probe.ErrIPv6):
			return nil, errors.New(a.t.Live["placeholder"])
		}
		return nil, err
	}
	hot, herr := live.ListenHotkey(func() {
		s.Mark()
		runtime.EventsEmit(a.ctx, "live-lag")
	})
	a.mu.Lock()
	a.session, a.hotkey = s, hot
	if compare {
		a.prevLive = a.lastLive
	} else {
		a.prevLive = nil
	}
	a.mu.Unlock()

	go func() { // an hour has passed, or the app is closing: finish it for the window
		<-s.Ended()
		select {
		case <-s.Done():
			if v, err := a.FinishLive(); err == nil {
				runtime.EventsEmit(a.ctx, "live-done", v)
			}
		default: // stopped by "Готово" or "Отменить"
		}
	}()

	view := &LiveStartView{Hotkey: herr == nil, Frames: s.FramesState()}
	for _, l := range s.Links() {
		view.Links = append(view.Links, LiveLinkView{ID: l.ID, Name: report.LinkName(a.t, l), Addr: l.Addr.String()})
	}
	return view, nil
}

// takeSession ends the window's hold on the running session, if there is one.
func (a *App) takeSession() *live.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, hot := a.session, a.hotkey
	a.session, a.hotkey = nil, nil
	a.running = a.running && s == nil
	hot.Close()
	return s
}

// FinishLive stops recording and returns the verdict.
func (a *App) FinishLive() (*LiveView, error) {
	s := a.takeSession()
	if s == nil {
		return nil, errors.New("no recording")
	}
	res := s.Finish(a.ctx)
	v := rules.Match(a.liveRules, res.Facts)
	a.mu.Lock()
	a.lastLive, a.liveVerdict, a.lastKind = &res, v, "live"
	prev := a.prevLive
	a.mu.Unlock()
	return a.liveView(&res, v, prev), nil
}

// CancelLive stops recording without a verdict.
func (a *App) CancelLive() {
	if s := a.takeSession(); s != nil {
		s.Cancel()
	}
}

// MarkLag records "лагнуло" now (the button, for when Ctrl+Alt+L is taken).
func (a *App) MarkLag() {
	a.mu.Lock()
	s := a.session
	a.mu.Unlock()
	if s != nil {
		s.Mark()
	}
}

func (a *App) liveView(res *live.Result, v rules.Verdict, prev *live.Result) *LiveView {
	d := a.t.LiveDiagnoses[v.ID]
	tone := liveTones[v.ID]
	if tone == "" {
		tone = "bad"
	}
	view := &LiveView{
		Verdict: VerdictView{ID: v.ID, Title: d.Title, Tone: tone, Confidence: a.t.Confidence[v.Confidence],
			Explain: locales.Fill(d.Explain, res.Facts), Advice: locales.Fill(d.Advice, res.Facts)},
		Extras:  report.LiveExtras(a.t, res, v.ID),
		Seconds: res.Seconds,
		Chart: ChartView{Start: res.Chart.Start.UnixMilli(), Step: res.Chart.Step, Series: res.Chart.Series, CPU: res.Chart.CPU,
			FPS: res.Chart.FPS, Stall: res.Chart.Stall},
		Frames: res.Frames,
		CPU:    res.CPU,
	}
	if res.Server != nil && res.Server.Via == "doh" {
		view.Note = a.t.Live["doh"]
	}
	var standIn = res.Links[len(res.Links)-1].StandIn
	for _, l := range res.Links {
		lv := LiveLinkView{ID: l.ID, Name: report.LinkName(a.t, l.Link), Addr: l.Addr.String(), State: l.State,
			Median: l.Median, P95: l.P95, Worst: l.Worst, LossPct: l.LossPct, Own: l.Own, Events: l.Events}
		if l.StandIn.IsValid() {
			lv.StandIn = l.StandIn.String()
		}
		view.Links = append(view.Links, lv)
	}
	view.TraceTitle = a.t.Live["trace_title_ctl"]
	if res.Server != nil {
		view.TraceTitle = a.t.Live["trace_title"]
	}
	for _, h := range res.Trace {
		network := h.Network
		if h.ASN != "" {
			network += " (" + h.ASN + ")"
		}
		if h.TTL == 1 && network == "" && h.Addr != "*" {
			network = a.t.Live["home_network"]
		}
		view.Trace = append(view.Trace, HopView{TTL: h.TTL, Addr: h.Addr, Network: network, Lost: h.Lost, Sent: h.Sent,
			Median: h.Median, Worst: h.Worst, Note: report.HopNote(a.t, h, standIn), Bad: h.LossCounts || h.Jump})
	}
	if res.WiFi != nil {
		w := *res.WiFi
		w.Last.SSID = "" // the window shows "hidden", as the report does
		view.WiFi = &w
	}
	names := map[string]string{}
	for _, l := range res.Links {
		names[l.ID] = report.LinkName(a.t, l.Link)
	}
	for _, m := range res.Lags {
		view.Lags = append(view.Lags, LagView{At: m.At.Sub(res.Started).Seconds(), Time: m.At.Format("15:04:05"), Spike: m.Spike,
			Text: report.LagText(a.t, m, names, true), Back: live.LagBefore.Seconds()})
	}
	if prev != nil {
		view.Compare = a.compare(prev, res)
	}
	return view
}

// compare puts the farthest link both checks share side by side: the server, or the internet.
func (a *App) compare(before, after *live.Result) *CompareView {
	pick := func(r *live.Result) *live.LinkStats {
		var ctl *live.LinkStats
		for i := range r.Links {
			switch r.Links[i].ID {
			case live.Target:
				return &r.Links[i]
			case live.CtlRU:
				ctl = &r.Links[i]
			}
		}
		return ctl
	}
	b, n := pick(before), pick(after)
	if b == nil || n == nil || b.ID != n.ID {
		return nil
	}
	ms := func(v float64) string {
		return comma(strconv.FormatFloat(math.Round(v*10)/10, 'f', -1, 64)) + " " + a.t.Live["ms"]
	}
	row := func(label string, x, y float64, f func(float64) string) CompareRow {
		return CompareRow{Label: a.t.Live[label], Before: f(x), After: f(y), Better: y < x, Worse: y > x}
	}
	cv := &CompareView{Link: report.LinkName(a.t, n.Link), Rows: []CompareRow{
		row("row_usual", b.Median, n.Median, ms),
		row("row_worst", b.Worst, n.Worst, ms),
		row("row_spikes", float64(b.Events), float64(n.Events), func(v float64) string { return strconv.Itoa(int(v)) }),
		row("row_loss", b.LossPct, n.LossPct, func(v float64) string { return comma(strconv.FormatFloat(v, 'f', 1, 64)) + " %" }),
	}}
	if before.Frames != nil && after.Frames != nil {
		cv.Rows = append(cv.Rows, row("row_stalls", float64(before.Frames.StallsTotal), float64(after.Frames.StallsTotal),
			func(v float64) string { return strconv.Itoa(int(v)) }))
	}
	return cv
}

// shutdown stops a recording when the window closes.
func (a *App) shutdown(context.Context) {
	if s := a.takeSession(); s != nil {
		s.Cancel()
	}
}

// comma writes a decimal number the Russian way.
func comma(s string) string { return strings.Replace(s, ".", ",", 1) }
