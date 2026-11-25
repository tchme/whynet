package main

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whynet/internal/live"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/internal/update"
	"whynet/locales"
	"whynet/rules"
)

// App is what the window calls. One check runs at a time and each mode keeps its result.
type App struct {
	ctx     context.Context
	rules   []rules.Rule
	markers probe.Markers
	t       *locales.Texts

	mu          sync.Mutex
	running     bool
	browserDone chan struct{} // closed by FinishBrowser to end a visible browser check
	pending     *update.Release
	last        *probe.Result
	ownRec      *probe.OwnRecord // read from the window, waiting for CompareHAR
	verdict     rules.Verdict

	liveRules   []rules.Rule
	session     *live.Session // a stability check being recorded
	hotkey      *live.Hotkey
	lastLive    *live.Result
	prevLive    *live.Result // the check "Проверить ещё раз и сравнить" compares with
	liveVerdict rules.Verdict

	appRules    []rules.Rule
	appDone     chan struct{}      // closed by FinishApp to end a recording
	appCancel   context.CancelFunc // CancelApp: end the recording without a check
	appCanceled bool
	lastApp     *probe.AppResult
	appVerdict  rules.Verdict

	healthRules []rules.Rule
	health      *healthState // the last Windows network health check and the fixes since

}

func NewApp() (*App, error) {
	rs, m, err := rules.Load()
	if err != nil {
		return nil, err
	}
	lrs, err := rules.LoadLive()
	if err != nil {
		return nil, err
	}
	ars, err := rules.LoadApp()
	if err != nil {
		return nil, err
	}
	hrs, err := rules.LoadHealth()
	if err != nil {
		return nil, err
	}
	t, err := locales.Load("ru")
	if err != nil {
		return nil, err
	}
	return &App{rules: rs, liveRules: lrs, appRules: ars, healthRules: hrs, markers: m, t: t}, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// CheckUpdate returns a newer release from GitHub, or nil when there's none or GitHub can't be asked.
func (a *App) CheckUpdate() *update.Release {
	rel, err := update.Latest(a.ctx)
	if err != nil || !update.Newer(rel.Version, report.Version) {
		return nil
	}
	a.mu.Lock()
	a.pending = rel
	a.mu.Unlock()
	return rel
}

// Update installs the release CheckUpdate found and restarts into it. When that fails, it opens
// the release page for a manual download and returns the error; the old exe keeps working.
func (a *App) Update() error {
	a.mu.Lock()
	rel := a.pending
	a.mu.Unlock()
	if rel == nil {
		return errors.New("no update found")
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err == nil {
		err = update.Apply(a.ctx, rel, exe)
	}
	if err != nil {
		switch {
		case errors.Is(err, update.ErrBusy):
			return errors.New(a.t.UI["update_already_running"])
		case errors.Is(err, update.ErrCurrent):
			return errors.New(a.t.UI["update_already_installed"])
		case errors.Is(err, update.ErrDigest):
			err = errors.New(a.t.UI["update_missing_digest"])
		}
		if rel.Page != "" {
			runtime.BrowserOpenURL(a.ctx, rel.Page)
		}
		return err
	}
	runtime.Quit(a.ctx)
	return nil
}

type StepView struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Ms     int64       `json:"ms"`
	Note   string      `json:"note,omitempty"`
	Facts  [][2]string `json:"facts"`
}

type VerdictView struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Explain    string `json:"explain"`
	Advice     string `json:"advice"`
	Confidence string `json:"confidence"`
	Tone       string `json:"tone"` // ok, warn (fixable on this PC or by the person), bad
}

type CheckView struct {
	Host    string      `json:"host"`
	Verdict VerdictView `json:"verdict"`
	Steps   []StepView  `json:"steps"`
	Hosts   []HostView  `json:"hosts"`
	Slow    []SlowView  `json:"slow"`
	Own     *OwnView    `json:"own,omitempty"` // the person's own browser record, once compared
}

// SlowView is one file that came late or never, for the "Details" table.
type SlowView struct {
	File       string `json:"file"` // full address without the query
	Kind       string `json:"kind"`
	Ms         int64  `json:"ms"`
	Unfinished bool   `json:"unfinished"`
	Where      string `json:"where"` // where the time went, or the browser's error
	PingMs     int64  `json:"ping_ms"`
	Lost       int    `json:"lost"`
	Tries      int    `json:"tries"`
}

// HostView is one server the page loads from, for the "Details" table.
type HostView struct {
	Host     string `json:"host"`
	Kinds    string `json:"kinds"`
	PingMs   int64  `json:"ping_ms"` // 0: not measured
	Lost     int    `json:"lost"`    // tries that lost a packet, out of Tries
	Tries    int    `json:"tries"`
	KBps     int64  `json:"kbps"`
	Requests int    `json:"requests"` // made by the browser
	Failed   int    `json:"failed"`
	Result   string `json:"result"`
	Tone     string `json:"tone"` // ok, warn, bad, "" (not checked)
}

// hostViews joins the content check and the browser's view of the same servers.
func (a *App) hostViews(res *probe.Result) []HostView {
	var checks []*probe.HostCheck
	var browser []*probe.BrowserHost
	for _, s := range res.Steps {
		if h, ok := s.Evidence["hosts"].([]*probe.HostCheck); ok {
			checks = h
		}
		if b, ok := s.Evidence["browser"].(probe.BrowserResult); ok {
			browser = b.Hosts
		}
	}
	byHost := map[string]*probe.BrowserHost{}
	for _, b := range browser {
		byHost[b.Host] = b
	}
	var out []HostView
	seen := map[string]bool{}
	words := func(kinds []string) string {
		out := make([]string, len(kinds))
		for i, k := range kinds {
			out[i] = a.t.Kinds[k]
		}
		return strings.Join(out, ", ")
	}
	for _, c := range checks {
		v := HostView{Host: c.Host, Kinds: words(c.Kinds), PingMs: c.PingMs, Lost: c.Lost, Tries: c.Tries, KBps: c.KBps,
			Result: a.t.Results[c.Result], Tone: hostTone(c.Result)}
		if c.BrowserOK {
			v.Result, v.Tone = a.t.Results[c.Result]+" · "+a.t.UI["browser_ok"], "warn"
		}
		if b := byHost[c.Host]; b != nil {
			v.Requests, v.Failed = b.Requests, b.Failed
			if v.KBps == 0 {
				v.KBps = b.KBps
			}
		}
		seen[c.Host] = true
		out = append(out, v)
	}
	for _, b := range browser { // the site itself and the servers beyond the check limit, with what the browser saw
		if seen[b.Host] {
			continue
		}
		kinds := probe.BrowserKinds(b.Types)
		if a.markers.IsAd(b.Host) {
			kinds = append(kinds, "ads")
		}
		v := HostView{Host: b.Host, Kinds: words(kinds), Requests: b.Requests, Failed: b.Failed, KBps: b.KBps}
		switch {
		case b.Host == res.Host:
			v.Result, v.PingMs = a.t.UI["site_itself"], sitePing(res)
		case len(checks) >= probe.MaxHosts:
			v.Result = locales.Fill(a.t.UI["over_limit"], map[string]string{"n": strconv.Itoa(probe.MaxHosts)})
		}
		out = append(out, v)
	}
	return out
}

// sitePing is the site's own connect time, from the TCP step (the content step skips the site).
func sitePing(res *probe.Result) int64 {
	for _, s := range res.Steps {
		tries, _ := s.Evidence["tries"].([]map[string]any)
		for _, t := range tries {
			if ms, ok := t["ms"].(int64); ok && t["result"] == "ok" {
				return ms
			}
		}
	}
	return 0
}

// slowViews lists the files that came late or never, with their server's ping.
func (a *App) slowViews(res *probe.Result) []SlowView {
	var files []*probe.SlowFile
	checks := map[string]*probe.HostCheck{}
	for _, s := range res.Steps {
		if b, ok := s.Evidence["browser"].(probe.BrowserResult); ok {
			files = b.Slow
		}
		if hs, ok := s.Evidence["hosts"].([]*probe.HostCheck); ok {
			for _, h := range hs {
				checks[h.Host] = h
			}
		}
	}
	var out []SlowView
	for _, f := range files {
		v := SlowView{File: f.URL, Kind: a.t.Kinds[f.Kind], Ms: f.Ms, Unfinished: f.Unfinished, Where: a.t.Phases[f.Phase]}
		if f.Error != "" && f.Phase != "none" {
			v.Where = f.Error
		}
		if c := checks[f.Host]; c != nil {
			v.PingMs, v.Lost, v.Tries = c.PingMs, c.Lost, c.Tries
		}
		out = append(out, v)
	}
	return out
}

func hostTone(result string) string {
	switch result {
	case "ok":
		return "ok"
	case "slow", "gone", "error":
		return "warn"
	}
	return "bad"
}

type InfoView struct {
	Steps    []StepView        `json:"steps"`
	AppSteps []StepView        `json:"app_steps"` // the app check's steps
	UI       map[string]string `json:"ui"`
	Live     map[string]string `json:"live"`
	App      map[string]string `json:"app"`
	Version  string            `json:"version"`
	Browser  string            `json:"browser"` // chromium or firefox: the default browser, for the record instructions
	Health   map[string]string `json:"health"`
	// The Windows network health check's areas, all waiting.
	HealthAreas []StepView `json:"health_areas"`
}

// tones colour the verdict; anything not listed is "bad".
var tones = map[string]string{
	"all_ok": "ok", "bot_check": "ok", "browser_ok": "ok", "http_reached": "warn",
	"site_blocks_you": "warn", "hosts_entry": "warn", "no_such_site": "warn", "isp_dns_down": "warn", "clock_wrong": "warn",
	"proxy_in_way": "warn", "https_intercepted": "warn", "proxy_path_broken": "warn", "sni_blocked_router": "warn", "unknown": "warn",
	"content_stall": "warn", "content_blocked": "warn", "content_slow": "warn", "content_flaky": "warn", "page_slow": "warn",
	"content_late_loss": "warn", "content_late_server": "warn", "content_late": "warn", "content_lossy": "warn",
	"own_blocked": "warn", "own_blocked_ads": "warn", "own_failed": "warn", "own_late": "warn", "own_clean": "warn",
	"dpi_conflict": "warn", "dpi_not_listed": "warn", "dpi_strategy_fails": "warn", "dpi_off": "warn",
}

// Info is what the window shows before the first check.
func (a *App) Info() InfoView {
	steps := make([]StepView, len(probe.StepIDs))
	for i, id := range probe.StepIDs {
		steps[i] = StepView{ID: id, Name: a.t.Steps[id].Name, Status: "wait"}
	}
	appSteps := make([]StepView, len(probe.AppStepIDs))
	for i, id := range probe.AppStepIDs {
		appSteps[i] = StepView{ID: id, Name: a.t.Steps[id].Name, Status: "wait"}
	}
	return InfoView{Steps: steps, AppSteps: appSteps, UI: a.t.UI, Live: a.t.Live, App: a.t.App, Version: report.Version, Browser: probe.DefaultBrowserFamily(),
		Health: a.t.Health, HealthAreas: a.healthAreas()}
}

// Check runs one check, sending each finished step as a "step" event.
func (a *App) Check(site string) (*CheckView, error) {
	return a.check(site, probe.DefaultEnv(a.markers), 2*time.Minute)
}

// RepeatSiteCheck uses the exact input of the last completed site check.
func (a *App) RepeatSiteCheck() (*CheckView, error) {
	a.mu.Lock()
	last := a.last
	a.mu.Unlock()
	if last == nil {
		return nil, errors.New(a.t.UI["bad_input"])
	}
	return a.Check(last.Input)
}

// CheckInBrowser runs the check with the browser step in a visible window the person uses
// (to pass an anti-bot check, log in, play a video). The window sends "browser-progress"
// events; it ends with FinishBrowser or when the person closes it.
func (a *App) CheckInBrowser(site string) (*CheckView, error) {
	if !a.begin() {
		return nil, errors.New(a.t.UI["busy"])
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.browserDone = done
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.browserDone = nil
		a.running = false
		a.mu.Unlock()
	}()
	env := probe.DefaultEnv(a.markers)
	env.BrowserAlways = true
	env.Browser = func(ctx context.Context, url string) probe.BrowserResult {
		return probe.BrowserSession(ctx, url, probe.BrowserOpts{Visible: true, Done: done,
			Progress: func(p probe.BrowserProgress) { runtime.EventsEmit(a.ctx, "browser-progress", p) }})
	}
	res, err := a.collectSite(site, env, 15*time.Minute)
	if err != nil {
		return nil, err
	}
	return a.finishSite(res), nil
}

// FinishBrowser ends a visible browser check: whynet takes what the page loaded so far.
func (a *App) FinishBrowser() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.browserDone != nil {
		close(a.browserDone)
		a.browserDone = nil
	}
}

func (a *App) check(site string, env probe.Env, limit time.Duration) (*CheckView, error) {
	if !a.begin() {
		return nil, errors.New(a.t.UI["busy"])
	}
	defer a.end()
	res, err := a.collectSite(site, env, limit)
	if err != nil {
		return nil, err
	}
	return a.finishSite(res), nil
}

func (a *App) collectSite(site string, env probe.Env, limit time.Duration) (*probe.Result, error) {
	ctx, cancel := context.WithTimeout(a.ctx, limit)
	defer cancel()
	res, err := probe.Run(ctx, env, site, func(s probe.Step) {
		runtime.EventsEmit(a.ctx, "step", a.stepView(s))
	})
	if errors.Is(err, probe.ErrBadInput) {
		return nil, errors.New(a.t.UI["bad_input"])
	}
	return res, err
}

func (a *App) finishSite(res *probe.Result) *CheckView {
	v := rules.Match(a.rules, res.Facts())
	a.mu.Lock()
	a.last, a.verdict = res, v
	a.mu.Unlock()
	return a.view(res, v)
}

// view is a finished check as the window shows it.
func (a *App) view(res *probe.Result, v rules.Verdict) *CheckView {
	facts := res.Facts()
	d := a.t.Diagnoses[v.ID]
	tone := tones[v.ID]
	if tone == "" {
		tone = "bad"
	}
	view := &CheckView{Host: res.Host, Verdict: VerdictView{
		ID: v.ID, Title: d.Title, Tone: tone, Confidence: a.t.Confidence[v.Confidence],
		Explain: locales.Fill(d.Explain, facts), Advice: locales.Fill(d.Advice, facts),
	}}
	for _, s := range res.Steps {
		view.Steps = append(view.Steps, a.stepView(s))
	}
	view.Hosts = a.hostViews(res)
	view.Slow = a.slowViews(res)
	view.Own = a.ownView(res)
	return view
}

func (a *App) stepView(s probe.Step) StepView {
	v := StepView{ID: s.ID, Name: a.t.Steps[s.ID].Name, Status: s.Status, Ms: s.Ms}
	for _, k := range slices.Sorted(maps.Keys(s.Facts)) {
		v.Facts = append(v.Facts, [2]string{k, s.Facts[k]})
	}
	v.Note = a.t.Note(s.Facts, s.Status == "fail" || s.Status == "warn" || s.Status == "error")
	return v
}

// report renders the result of the requested mode.
func (a *App) report(kind string, hide bool) (md, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case kind == "health" && a.health != nil && a.health.res != nil:
		h := a.health
		return a.healthReport(h, hide)
	case kind == "app" && a.lastApp != nil:
		return report.AppMarkdown(a.lastApp, a.appVerdict, a.t, hide), report.AppFileName(a.lastApp)
	case kind == "live" && a.lastLive != nil:
		return report.LiveMarkdown(a.lastLive, a.liveVerdict, a.t, hide, a.prevLive), report.LiveFileName(a.lastLive)
	case kind == "site" && a.last != nil:
		return report.Markdown(a.last, a.verdict, a.t, hide), report.FileName(a.last)
	}
	return "", ""
}

// Report is the AI report text for the preview.
func (a *App) Report(kind string, hide bool) (string, error) {
	md, _ := a.report(kind, hide)
	if md == "" {
		return "", errors.New(a.t.UI["no_report"])
	}
	return md, nil
}

// SaveReport asks where to save the AI report and writes it. "" means the person cancelled.
func (a *App) SaveReport(kind string, hide bool) (string, error) {
	md, name := a.report(kind, hide)
	if md == "" {
		return "", errors.New(a.t.UI["no_report"])
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           a.t.UI["save_title"],
		DefaultFilename: name,
		Filters:         []runtime.FileFilter{{DisplayName: "Markdown (*.md)", Pattern: "*.md"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, []byte(md), 0o644)
}

// CopyReport puts the AI report on the clipboard.
func (a *App) CopyReport(kind string, hide bool) error {
	md, err := a.Report(kind, hide)
	if err != nil {
		return err
	}
	return runtime.ClipboardSetText(a.ctx, md)
}
