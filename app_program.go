package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

// The app check in the window (docs/appcheck.md): pick a program, record its connections while the
// person repeats what fails, check each server.

const appMaxRecord = 2 * time.Minute

// AppServerView is one server of the program, for the recording screen and the "Details" table.
type AppServerView struct {
	Label    string `json:"label"`     // its name, or its address and port
	NameFrom string `json:"name_from"` // where the name came from; "" while recording
	Addr     string `json:"addr"`      // address:port
	Owner    string `json:"owner"`
	Ad       bool   `json:"ad"`
	Conns    int    `json:"conns"`
	Seen     string `json:"seen"` // what the program went through
	SeenTone string `json:"seen_tone"`
	Check    string `json:"check"` // whynet's own check; "" while recording
	Tone     string `json:"tone"`
	Ping     string `json:"ping"`
	Zapret   string `json:"zapret"`
}

// AppTick is the recording screen's state, sent once a second as "app-tick".
type AppTick struct {
	Seconds int             `json:"seconds"`
	Conns   int             `json:"conns"`
	Cut     int             `json:"cut"`
	Stuck   int             `json:"stuck"`
	Servers []AppServerView `json:"servers"`
}

// AppView is a finished app check as the window shows it.
type AppView struct {
	Program string          `json:"program"`
	Summary string          `json:"summary"`
	Verdict VerdictView     `json:"verdict"`
	Steps   []StepView      `json:"steps"`
	Servers []AppServerView `json:"servers"`
	Zapret  bool            `json:"zapret"` // a zapret column is worth showing
}

// appTones colour the app verdicts; anything not listed is "bad".
var appTones = map[string]string{
	"app_ok": "ok", "app_no_traffic": "warn", "app_flaky": "warn", "app_lossy": "warn", "app_router_path": "warn",
	"app_game_filter_none": "warn", "app_dpi_conflict": "warn", "app_dpi_not_listed": "warn", "app_dpi_fails": "warn", "unknown": "warn",
}

// ListPrograms is the picker's list: programs with a window or with connections right now.
func (a *App) ListPrograms() ([]probe.Program, error) {
	return probe.ListPrograms(a.markers.Browsers)
}

// RecordApp records the program's TCP connections until FinishApp (or 2 minutes), sending
// "app-tick" once a second, then checks its servers, sending each step as "app-step".
// CancelApp ends it without a check: the result is then nil.
func (a *App) RecordApp(exe string) (*AppView, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, errors.New(a.t.UI["busy"])
	}
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(a.ctx, appMaxRecord)
	a.running, a.appDone, a.appCancel, a.appCanceled = true, done, cancel, false
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.running, a.appDone, a.appCancel = false, nil, nil
		a.mu.Unlock()
	}()

	start := time.Now()
	servers, recorded, err := probe.RecordApp(ctx, exe, a.markers.Browsers, done, func(s []probe.AppServer) {
		runtime.EventsEmit(a.ctx, "app-tick", a.appTick(s, time.Since(start)))
	})
	if err != nil {
		return nil, fmt.Errorf("%s", locales.Fill(a.t.App["not_running"], map[string]string{"name": exe}))
	}
	a.mu.Lock()
	canceled := a.appCanceled
	a.mu.Unlock()
	if canceled {
		return nil, nil
	}

	checkCtx, cancelCheck := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancelCheck()
	res := probe.CheckApp(checkCtx, probe.DefaultEnv(a.markers), exe, servers, recorded, probe.DNSCacheNames(), func(s probe.Step) {
		runtime.EventsEmit(a.ctx, "app-step", a.stepView(s))
	})
	v := rules.Match(a.appRules, res.Facts())
	a.mu.Lock()
	a.lastApp, a.appVerdict, a.lastKind = res, v, "app"
	a.mu.Unlock()
	return a.appView(res, v), nil
}

// FinishApp ends the recording: whynet checks what it saw so far.
func (a *App) FinishApp() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appDone != nil {
		close(a.appDone)
		a.appDone = nil
	}
}

// CancelApp ends the recording without a check.
func (a *App) CancelApp() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.appCancel != nil {
		a.appCanceled = true
		a.appCancel()
	}
}

func (a *App) appTick(servers []probe.AppServer, since time.Duration) AppTick {
	t := AppTick{Seconds: int(since.Seconds())}
	for i := range servers {
		s := &servers[i]
		t.Conns, t.Cut, t.Stuck = t.Conns+s.Conns, t.Cut+s.Cut, t.Stuck+s.Stuck
		t.Servers = append(t.Servers, a.serverView(s))
	}
	return t
}

func (a *App) appView(res *probe.AppResult, v rules.Verdict) *AppView {
	facts := res.Facts()
	d := a.t.AppDiagnoses[v.ID]
	tone := appTones[v.ID]
	if tone == "" {
		tone = "bad"
	}
	view := &AppView{Program: res.Host, Verdict: VerdictView{
		ID: v.ID, Title: d.Title, Tone: tone, Confidence: a.t.Confidence[v.Confidence],
		Explain: locales.Fill(d.Explain, facts), Advice: locales.Fill(d.Advice, facts),
	}}
	view.Summary = locales.Fill(a.t.App["summary"], map[string]string{"seconds": strconv.Itoa(int(res.Seconds + 0.5)),
		"conns": facts["app.conns"], "servers": facts["app.servers"], "checked": facts["app.checked"]})
	for _, s := range res.Steps {
		view.Steps = append(view.Steps, a.stepView(s))
	}
	for _, s := range res.Servers {
		sv := a.serverView(s)
		if sv.Zapret != "" && facts["local.dpi_tool"] == "none" {
			sv.Zapret = a.t.App["zapret_off"] // installed, not running: its lists change nothing now
		}
		view.Zapret = view.Zapret || sv.Zapret != ""
		view.Servers = append(view.Servers, sv)
	}
	return view
}

// serverView words one server for the window: what the program saw and, once checked, what whynet found.
func (a *App) serverView(s *probe.AppServer) AppServerView {
	w := a.t.App
	n := func(i int) string { return strconv.Itoa(i) }
	v := AppServerView{Label: s.Label(), Addr: fmt.Sprintf("%s:%d", s.IP, s.Port), Ad: s.Ad, Conns: s.Conns,
		Owner: s.Owner, SeenTone: "ok", Seen: w["seen_ok"]}
	if s.Name == "" && s.Result == "" {
		v.Label = v.Addr
	}
	switch {
	case s.Troubled() && s.Cut >= s.Stuck:
		v.Seen, v.SeenTone = locales.Fill(w["seen_cut"], map[string]string{"n": n(s.Cut), "of": n(s.Conns)}), "bad"
	case s.Troubled():
		v.Seen, v.SeenTone = locales.Fill(w["seen_stuck"], map[string]string{"n": n(s.Stuck), "of": n(s.Conns)}), "warn"
	case s.Cut+s.Stuck > 0:
		v.Seen = locales.Fill(w["seen_some"], map[string]string{"n": n(s.Cut + s.Stuck), "of": n(s.Conns)})
	}
	if s.Result == "" && s.NameFrom == "" {
		return v // still recording
	}
	v.NameFrom = w["name_"+s.NameFrom]
	if s.Name == "" {
		v.NameFrom = w["name_none"]
	}
	if !s.Checked {
		v.Check, v.Tone = w["not_checked"], ""
		return v
	}
	v.Check = w["result_"+s.Result]
	switch {
	case s.Ad:
		v.Tone = ""
	case s.Blocked() && s.Result != "open":
		v.Tone = "bad"
	case s.Blocked():
		v.Tone = "warn"
	default:
		v.Tone = "ok"
	}
	switch {
	case s.Tries == 0:
		v.Ping = "—"
	case s.PingMs == 0:
		v.Ping = "<1 " + a.t.Report["ms"]
	default:
		v.Ping = fmt.Sprintf("%d %s", s.PingMs, a.t.Report["ms"])
	}
	if s.Lost > 0 {
		v.Ping += " · " + locales.Fill(a.t.Report["lost"], map[string]string{"lost": n(s.Lost), "tries": n(s.Tries)})
	}
	if s.DPIName != "" || s.DPIIP != "" {
		yes := func(x string) bool { return x == "true" }
		switch {
		case yes(s.DPIName) || yes(s.DPIIP):
			v.Zapret = w["zapret_yes"]
		default:
			v.Zapret = w["zapret_no"]
		}
	}
	return v
}
