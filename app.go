package main

import (
	"context"
	"errors"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

// App is what the window calls. One check runs at a time; the last one feeds the report.
type App struct {
	ctx     context.Context
	rules   []rules.Rule
	markers probe.Markers
	t       *locales.Texts

	mu          sync.Mutex
	running     bool
	browserDone chan struct{} // closed by FinishBrowser to end a visible browser check
	last        *probe.Result
	verdict     rules.Verdict
}

func NewApp() (*App, error) {
	rs, m, err := rules.Load()
	if err != nil {
		return nil, err
	}
	t, err := locales.Load("ru")
	if err != nil {
		return nil, err
	}
	return &App{rules: rs, markers: m, t: t}, nil
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

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
}

// HostView is one server the page loads from, for the "Details" table.
type HostView struct {
	Host     string `json:"host"`
	Kinds    string `json:"kinds"`
	PingMs   int64  `json:"ping_ms"` // 0: not measured
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
	for _, c := range checks {
		kinds := make([]string, len(c.Kinds))
		for i, k := range c.Kinds {
			kinds[i] = a.t.Kinds[k]
		}
		v := HostView{Host: c.Host, Kinds: strings.Join(kinds, ", "), PingMs: c.PingMs, KBps: c.KBps,
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
	for _, b := range browser { // the servers beyond the check limit, with what the browser saw
		if !seen[b.Host] {
			out = append(out, HostView{Host: b.Host, Requests: b.Requests, Failed: b.Failed, KBps: b.KBps})
		}
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
	Steps   []StepView        `json:"steps"`
	UI      map[string]string `json:"ui"`
	Version string            `json:"version"`
}

// tones colour the verdict; anything not listed is "bad".
var tones = map[string]string{
	"all_ok": "ok", "bot_check": "ok", "browser_ok": "ok",
	"site_blocks_you": "warn", "hosts_entry": "warn", "no_such_site": "warn", "isp_dns_down": "warn", "clock_wrong": "warn",
	"proxy_in_way": "warn", "https_intercepted": "warn", "proxy_path_broken": "warn", "unknown": "warn",
	"content_stall": "warn", "content_blocked": "warn", "content_slow": "warn", "content_flaky": "warn", "page_slow": "warn",
	"dpi_conflict": "warn", "dpi_not_listed": "warn", "dpi_strategy_fails": "warn", "dpi_off": "warn",
}

// Info is what the window shows before the first check.
func (a *App) Info() InfoView {
	steps := make([]StepView, len(probe.StepIDs))
	for i, id := range probe.StepIDs {
		steps[i] = StepView{ID: id, Name: a.t.Steps[id].Name, Status: "wait"}
	}
	return InfoView{Steps: steps, UI: a.t.UI, Version: report.Version}
}

// Check runs one check, sending each finished step as a "step" event.
func (a *App) Check(site string) (*CheckView, error) {
	return a.check(site, probe.DefaultEnv(a.markers), 2*time.Minute)
}

// CheckInBrowser runs the check with the browser step in a visible window the person uses
// (to pass an anti-bot check, log in, play a video). The window sends "browser-progress"
// events; it ends with FinishBrowser or when the person closes it.
func (a *App) CheckInBrowser(site string) (*CheckView, error) {
	done := make(chan struct{})
	a.mu.Lock()
	a.browserDone = done
	a.mu.Unlock()
	env := probe.DefaultEnv(a.markers)
	env.BrowserAlways = true
	env.Browser = func(ctx context.Context, url string) probe.BrowserResult {
		return probe.BrowserSession(ctx, url, probe.BrowserOpts{Visible: true, Done: done,
			Progress: func(p probe.BrowserProgress) { runtime.EventsEmit(a.ctx, "browser-progress", p) }})
	}
	return a.check(site, env, 15*time.Minute)
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
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, errors.New(a.t.UI["busy"])
	}
	a.running = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.running = false
		a.browserDone = nil
		a.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(a.ctx, limit)
	defer cancel()
	res, err := probe.Run(ctx, env, site, func(s probe.Step) {
		runtime.EventsEmit(a.ctx, "step", a.stepView(s))
	})
	if errors.Is(err, probe.ErrBadInput) {
		return nil, errors.New(a.t.UI["bad_input"])
	}
	if err != nil {
		return nil, err
	}
	v := rules.Match(a.rules, res.Facts())
	a.mu.Lock()
	a.last, a.verdict = res, v
	a.mu.Unlock()

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
	return view, nil
}

func (a *App) stepView(s probe.Step) StepView {
	v := StepView{ID: s.ID, Name: a.t.Steps[s.ID].Name, Status: s.Status, Ms: s.Ms}
	for _, k := range slices.Sorted(maps.Keys(s.Facts)) {
		v.Facts = append(v.Facts, [2]string{k, s.Facts[k]})
	}
	if s.Status == "fail" || s.Status == "warn" || s.Status == "error" {
		v.Note = a.t.Note(s.Facts)
	}
	return v
}

// report renders the last check; "" before the first one.
func (a *App) report(hide bool) (md, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		return "", ""
	}
	return report.Markdown(a.last, a.verdict, a.t, hide), report.FileName(a.last)
}

// Report is the AI report text for the preview.
func (a *App) Report(hide bool) string {
	md, _ := a.report(hide)
	return md
}

// SaveReport asks where to save the AI report and writes it. "" means the person cancelled.
func (a *App) SaveReport(hide bool) (string, error) {
	md, name := a.report(hide)
	if md == "" {
		return "", nil
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
func (a *App) CopyReport(hide bool) error {
	md, _ := a.report(hide)
	return runtime.ClipboardSetText(a.ctx, md)
}
