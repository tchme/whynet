package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/websocket"
)

// BrowserResult is what a real browser saw while loading the page: every request and how it ended.
type BrowserResult struct {
	Status   string         `json:"status"` // ok, nav_failed, unavailable, error
	Browser  string         `json:"browser,omitempty"`
	Error    string         `json:"error,omitempty"`
	Title    string         `json:"title,omitempty"` // the page title the browser ended on
	text     string         // start of the page's visible text: matched against markers, never reported (may be personal)
	Visible  bool           `json:"visible,omitempty"`
	DomMs    int64          `json:"dom_ms,omitempty"`  // navigation to "page parsed and shown"; 0: never
	LoadMs   int64          `json:"load_ms,omitempty"` // navigation to the load event; 0: never
	Requests int            `json:"requests"`
	Failed   int            `json:"failed"`
	Hosts    []*BrowserHost `json:"hosts,omitempty"`
	Slow     []*SlowFile    `json:"slow,omitempty"` // failed first, then the slowest
}

// SlowFile is one file of the page that came late, never came or failed: the thing to name, not just its server.
type SlowFile struct {
	URL        string           `json:"url"` // without the query: signed links carry tokens
	Host       string           `json:"host"`
	IP         string           `json:"ip,omitempty"`
	Kind       string           `json:"kind"` // image, script, style, font, media
	Ms         int64            `json:"ms"`
	Unfinished bool             `json:"unfinished,omitempty"` // still going when whynet stopped watching: Ms is "at least"
	Phase      string           `json:"phase,omitempty"`      // where most of the time went (PhaseOrder), "none": no answer at all
	Phases     map[string]int64 `json:"phases,omitempty"`     // ms per phase, as DevTools splits them
	Error      string           `json:"error,omitempty"`
}

// BrowserHost sums up the requests to one server.
type BrowserHost struct {
	Host      string         `json:"host"`
	Requests  int            `json:"requests"`
	Failed    int            `json:"failed"`
	Errors    map[string]int `json:"errors,omitempty"` // Chromium error text -> count
	Bytes     int64          `json:"bytes"`
	SlowestMs int64          `json:"slowest_ms"`
	KBps      int64          `json:"kbps,omitempty"` // over requests big enough to judge speed
	Types     []string       `json:"types,omitempty"`
	Sample    string         `json:"sample,omitempty"` // a failed URL without its query
}

const (
	browserBudget = 20 * time.Second // from navigation to giving up on a page that never finishes loading
	afterLoad     = 5 * time.Second  // time for lazy images and video after the load event
	afterDOM      = 8 * time.Second  // the limit after the page is parsed, when the load event never comes
	slowDOM       = 8 * time.Second  // a page shown later than this is "very slow"
	maxVisible    = 10 * time.Minute // a visible window the person forgot
	lateAfter     = 3.0              // s: a file the page waits this long for shows as an empty block
	netSlowMs     = 1000             // a DNS, connect or TLS phase this long is a resent packet, not distance
	maxSlow       = 10               // slow files reported
)

// BrowserOpts tunes a browser run. The zero value is the hidden, automatic one (DevTools protocol).
type BrowserOpts struct {
	// Visible opens a plain window the person uses (passes an anti-bot check, logs in, plays a video),
	// recorded through Chromium's network log, not the debugging port (see netlog.go).
	// It ends when Done closes, when the person closes the window, or after maxVisible.
	Visible  bool
	Done     <-chan struct{}
	Progress func(BrowserProgress) // called twice a second while the window is open
}

// BrowserProgress is the live count shown while a visible window is open.
type BrowserProgress struct {
	Requests int `json:"requests"`
	Failed   int `json:"failed"`
	Seconds  int `json:"seconds"`
}

func (p *prober) browser(s *Step) {
	if (!p.pageOK && !p.env.BrowserAlways) || p.env.Browser == nil {
		s.Facts["br.status"] = "off"
		s.Status = "skip"
		return
	}
	// The address the person typed; the hidden run follows whynet's own redirects, a plain
	// window starts clean (those redirects may be an anti-bot's answer to whynet itself).
	target := "https://" + p.host + p.path
	if p.finalURL != nil && !p.env.BrowserAlways {
		target = p.finalURL.String()
	}
	r := p.env.Browser(p.ctx, target)
	s.Facts["br.status"] = r.Status
	s.Evidence["browser"] = r
	switch r.Status {
	case "unavailable":
		s.Status = "skip"
		return
	case "ok":
	default:
		s.Facts["br.error"] = r.Error
		s.Status = "fail"
		return
	}
	// The browser's own services (Edge's shopping helper and the like) are not the page's content.
	r.Hosts = slices.DeleteFunc(slices.Clone(r.Hosts), func(h *BrowserHost) bool {
		if isUnder(h.Host, p.env.Markers.BrowserOwn) {
			r.Requests, r.Failed = r.Requests-h.Requests, r.Failed-h.Failed
			return true
		}
		return false
	})
	r.Slow = slices.DeleteFunc(slices.Clone(r.Slow), func(f *SlowFile) bool {
		return isUnder(f.Host, p.env.Markers.BrowserOwn) || p.env.Markers.IsAd(f.Host)
	})
	r.Slow = r.Slow[:min(len(r.Slow), maxSlow)]
	s.Evidence["browser"] = r
	p.browserHosts = r.Hosts
	s.Facts["br.requests"] = strconv.Itoa(r.Requests)
	// Files that came late or never, while the rest of their server's files came (one image stuck
	// in a feed): the worst one is named. Other errors are the failures below.
	var late []string
	for _, f := range r.Slow {
		if f.Error != "" && f.Error != hungError || slices.Contains(late, f.Host) {
			continue
		}
		if len(late) == 0 {
			s.Facts["br.late_file"] = shortURL(f.URL)
			s.Facts["br.late_s"] = strconv.FormatFloat(float64(f.Ms)/1000, 'f', 1, 64) + map[bool]string{true: "+"}[f.Unfinished]
			if f.Phase != "" {
				s.Facts["br.late_phase"] = f.Phase
			}
		}
		late = append(late, f.Host)
	}
	if len(late) > 0 {
		p.browserLate = late
		s.Facts["br.late"] = "true"
		s.Facts["br.late_hosts"] = shortList(late)
		s.Status = "warn"
	}
	switch {
	case r.Visible:
		s.Facts["br.visible"] = "true" // a plain window has no page timings: the person saw them
	case r.DomMs == 0:
		s.Facts["br.loaded"] = "false"
		s.Facts["br.dom_s"] = strconv.Itoa(int(browserBudget.Seconds())) + "+"
		s.Status = "warn"
	case time.Duration(r.DomMs)*time.Millisecond > slowDOM:
		s.Facts["br.slow_load"] = "true"
		s.Facts["br.dom_s"] = strconv.FormatFloat(float64(r.DomMs)/1000, 'f', 1, 64)
		s.Status = "warn"
	default:
		s.Facts["br.dom_s"] = strconv.FormatFloat(float64(r.DomMs)/1000, 'f', 1, 64)
	}
	// A site's block page ("not a robot?", "turn off your VPN") means different things:
	// shown to whynet's automated browser, it is about automation (ozon.ru turns away any
	// browser with a debugging port); shown to a plain window, it is about the connection.
	page := strings.ToLower(r.Title + "\n" + r.text)
	if firstIn(page, p.env.Markers.VPNBlock) != "" || firstIn(strings.ToLower(r.Title), p.env.Markers.Challenge) != "" {
		if r.Visible {
			s.Facts["br.blocked_page"] = "true"
			s.Facts["br.page_title"] = r.Title
		} else {
			s.Facts["br.antibot"] = "true" // what lies behind the check stays unseen
		}
		s.Status = "warn"
	}
	s.Facts["br.failed"] = strconv.Itoa(r.Failed)
	var failed []string
	worst := ""
	for _, h := range r.Hosts {
		if h.KBps > 0 && h.KBps < slowKBps {
			if p.browserSlow == nil {
				p.browserSlow = map[string]int64{}
			}
			p.browserSlow[h.Host] = h.KBps
		}
		if h.Failed == 0 || p.env.Markers.IsAd(h.Host) {
			continue
		}
		// A blocked server is silent on every request; one slow answer from a working server is not a block.
		if h.Failed == h.Errors[hungError] && h.Failed < h.Requests {
			continue
		}
		failed = append(failed, h.Host)
		for e := range h.Errors {
			if c := chromeErrClass(e); worst == "" || slices.Index(chromeOrder, c) < slices.Index(chromeOrder, worst) {
				worst = c
			}
		}
	}
	if len(failed) > 0 {
		p.browserFailed = failed
		s.Facts["br.problem"] = "true"
		s.Facts["br.failed_hosts"] = shortList(failed)
		s.Facts["br.worst"] = worst
		s.Status = "warn"
	}
}

var chromeOrder = []string{"reset", "timeout", "dns", "refused", "eof", "cert", "tls", "quic", "other"}

// chromeErrClass sorts a Chromium net error into the words the rules use.
func chromeErrClass(e string) string {
	switch {
	case strings.Contains(e, "CONNECTION_RESET"), strings.Contains(e, "CONNECTION_ABORTED"):
		return "reset"
	case strings.Contains(e, "TIMED_OUT"):
		return "timeout"
	case strings.Contains(e, "NAME_NOT_RESOLVED"), strings.Contains(e, "NAME_RESOLUTION_FAILED"):
		return "dns"
	case strings.Contains(e, "CONNECTION_REFUSED"):
		return "refused"
	case strings.Contains(e, "CONNECTION_CLOSED"), strings.Contains(e, "EMPTY_RESPONSE"):
		return "eof"
	case strings.Contains(e, "CERT"):
		return "cert"
	case strings.Contains(e, "SSL"):
		return "tls"
	case strings.Contains(e, "QUIC"):
		return "quic"
	}
	return "other"
}

// cdpAgg collects Network events of one page into per-server totals.
type cdpAgg struct {
	reqs   map[string]*cdpReq
	order  []string
	lastTs float64 // the latest CDP timestamp seen: "now" for requests still open
}

type cdpReq struct {
	url, host, kind, ip string
	start, end          float64 // CDP timestamps, seconds
	headers             float64 // when the answer's headers came; 0: not yet
	timing              *cdpTiming
	bytes               int64
	failed              string
	done, responded     bool
	aborted             bool // cancelled by the page (or the person closing the window): its wait proves nothing
}

// cdpTiming is DevTools' ResourceTiming: RequestTime in seconds on the event clock,
// the rest in ms after it, -1 when a phase didn't happen (a reused connection has no connect).
type cdpTiming struct {
	RequestTime                                float64
	ProxyStart, DNSStart, DNSEnd               float64
	ConnectStart, ConnectEnd, SSLStart, SSLEnd float64
	SendStart, SendEnd, ReceiveHeadersEnd      float64
}

// PhaseOrder: the phases of a request, in order. "answer" stands for everything up to the
// answer where only the visible window's network log is there (no DevTools timings).
var PhaseOrder = []string{"queue", "dns", "connect", "tls", "wait", "answer", "download"}

// phases splits a request ending at end (seconds) the way DevTools does, in ms.
func (r *cdpReq) phases(end float64) map[string]int64 {
	ph := map[string]int64{}
	set := func(k string, ms float64) {
		if ms >= 1 {
			ph[k] = int64(ms)
		}
	}
	t := r.timing
	switch {
	case t != nil:
		first := 0.0 // where the network work began: the rest before it is waiting in the browser
		for _, v := range []float64{t.ProxyStart, t.DNSStart, t.ConnectStart, t.SendStart} {
			if v >= 0 {
				first = v
				break
			}
		}
		set("queue", (t.RequestTime-r.start)*1000+first)
		if t.DNSStart >= 0 {
			set("dns", t.DNSEnd-t.DNSStart)
		}
		if t.ConnectStart >= 0 {
			connected := t.ConnectEnd // Chromium's connect includes TLS
			if t.SSLStart >= 0 {
				connected = t.SSLStart
				set("tls", t.SSLEnd-t.SSLStart)
			}
			set("connect", connected-t.ConnectStart)
		}
		if t.SendEnd >= 0 {
			set("wait", t.ReceiveHeadersEnd-t.SendEnd)
		}
		set("download", (end-t.RequestTime)*1000-t.ReceiveHeadersEnd)
	case r.headers > 0:
		set("answer", (r.headers-r.start)*1000)
		set("download", (end-r.headers)*1000)
	}
	return ph
}

// mainPhase is the phase that took the most time; "" when none is known.
func mainPhase(ph map[string]int64) string {
	best := ""
	for _, k := range PhaseOrder {
		if ph[k] > 0 && (best == "" || ph[k] > ph[best]) {
			best = k
		}
	}
	return best
}

// shortURL is a file's address for one line of text: no scheme, a long path cut in the middle.
func shortURL(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if r := []rune(u); len(r) > 70 {
		return string(r[:42]) + "…" + string(r[len(r)-25:])
	}
	return u
}

// noQuery drops the query and fragment: signed CDN links carry tokens.
func noQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

// hungAfter: a request with no response at all for this long counts as timed out.
// One that got a response and is still downloading (video, a stream) is fine.
const hungAfter = 5.0

// hangKinds are the request types that never wait on purpose. XHR, fetch, pings and
// event streams may hang by design (long polling), so their silence proves nothing.
var hangKinds = []string{"document", "stylesheet", "script", "image", "font", "media"}

// speedKinds are the request types whose timing measures the network, not the server.
var speedKinds = []string{"image", "media", "font", "script", "stylesheet"}

const hungError = "net::ERR_TIMED_OUT (no answer while whynet watched)"

func newAgg() *cdpAgg { return &cdpAgg{reqs: map[string]*cdpReq{}} }

func (a *cdpAgg) event(method string, params json.RawMessage) {
	var e struct {
		RequestID string  `json:"requestId"`
		Timestamp float64 `json:"timestamp"`
		Type      string  `json:"type"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
		Response struct {
			RemoteIPAddress string     `json:"remoteIPAddress"`
			Timing          *cdpTiming `json:"timing"`
		} `json:"response"`
		EncodedDataLength float64 `json:"encodedDataLength"`
		ErrorText         string  `json:"errorText"`
		Canceled          bool    `json:"canceled"`
	}
	if json.Unmarshal(params, &e) != nil {
		return
	}
	a.lastTs = max(a.lastTs, e.Timestamp)
	r := a.reqs[e.RequestID]
	switch method {
	case "Network.responseReceived":
		if r != nil {
			r.responded, r.headers = true, e.Timestamp
			r.ip, r.timing = e.Response.RemoteIPAddress, e.Response.Timing
		}
	case "Network.requestWillBeSent":
		u, err := url.Parse(e.Request.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return // data:, blob: and the like never touch the network
		}
		if r == nil { // a redirect reuses the id; keep the first start
			r = &cdpReq{start: e.Timestamp}
			a.reqs[e.RequestID] = r
			a.order = append(a.order, e.RequestID)
		}
		r.url, r.host, r.kind = e.Request.URL, strings.ToLower(u.Hostname()), strings.ToLower(e.Type)
	case "Network.loadingFinished":
		if r != nil {
			r.end, r.bytes, r.done = e.Timestamp, int64(e.EncodedDataLength), true
		}
	case "Network.loadingFailed":
		// A cancelled request (navigation away, media seek) is the page's choice, not a failure.
		if r != nil && !e.Canceled && e.ErrorText != "net::ERR_ABORTED" && e.ErrorText != "net::ERR_BLOCKED_BY_CLIENT" {
			r.end, r.failed, r.done = e.Timestamp, e.ErrorText, true
		} else if r != nil {
			r.end, r.done, r.aborted = e.Timestamp, true, true
		}
	}
}

// liveFailed counts failures so far, hung requests included, for the live counter.
func (a *cdpAgg) liveFailed() int {
	n := 0
	for _, r := range a.reqs {
		if r.failed != "" || !r.done && !r.responded && a.lastTs-r.start > hungAfter && slices.Contains(hangKinds, r.kind) {
			n++
		}
	}
	return n
}

func (a *cdpAgg) inflight() int {
	n := 0
	for _, r := range a.reqs {
		if !r.done {
			n++
		}
	}
	return n
}

func (a *cdpAgg) result() BrowserResult {
	res := BrowserResult{Status: "ok"}
	byHost := map[string]*BrowserHost{}
	bigBytes, bigSecs := map[string]int64{}, map[string]float64{}
	for _, id := range a.order {
		r := a.reqs[id]
		if !r.done && !r.responded && a.lastTs-r.start > hungAfter && slices.Contains(hangKinds, r.kind) {
			r.failed, r.end = hungError, a.lastTs
		}
		h := byHost[r.host]
		if h == nil {
			h = &BrowserHost{Host: r.host}
			byHost[r.host] = h
			res.Hosts = append(res.Hosts, h)
		}
		h.Requests++
		res.Requests++
		h.Bytes += r.bytes
		if r.kind != "" && !slices.Contains(h.Types, r.kind) {
			h.Types = append(h.Types, r.kind)
		}
		if r.end > r.start {
			h.SlowestMs = max(h.SlowestMs, int64((r.end-r.start)*1000))
			// Only files sent as they are: a page or an API answer includes the server's thinking time.
			if r.bytes >= speedMinRead && slices.Contains(speedKinds, r.kind) {
				bigBytes[r.host] += r.bytes
				bigSecs[r.host] += r.end - r.start
			}
		}
		if r.failed != "" {
			h.Failed++
			res.Failed++
			if h.Errors == nil {
				h.Errors = map[string]int{}
			}
			h.Errors[r.failed]++
			if h.Sample == "" {
				h.Sample = noQuery(r.url)
			}
		}
	}
	for host, secs := range bigSecs {
		if secs > 0 {
			byHost[host].KBps = int64(float64(bigBytes[host]) / 1024 / secs)
		}
	}
	slices.SortStableFunc(res.Hosts, func(x, y *BrowserHost) int { return y.Failed - x.Failed })
	res.Slow = a.slow()
	return res
}

// lateKinds are the files a person sees missing: an empty block, a page without its styles or fonts.
var lateKinds = []string{"image", "script", "style", "font", "media"}

// slow lists the files that failed, never answered or took lateAfter or longer, failed first.
// Call it after result() has marked the hung requests.
func (a *cdpAgg) slow() []*SlowFile {
	// HTTP/2 sends every file of a server over one connection: the files queued behind
	// a connect that took seconds show the wait as "queue". Blame the server's slowest connect.
	type phase struct {
		name string
		ms   int64
	}
	slowNet := map[string]phase{}
	for _, r := range a.reqs {
		ph := r.phases(r.end)
		for _, k := range []string{"dns", "connect", "tls"} {
			if ph[k] > slowNet[r.host].ms {
				slowNet[r.host] = phase{k, ph[k]}
			}
		}
	}
	var out []*SlowFile
	for _, id := range a.order {
		r := a.reqs[id]
		kind := BrowserKinds([]string{r.kind})[0]
		end := r.end
		if !r.done {
			end = a.lastTs
		}
		secs := end - r.start
		// Video streams for as long as it plays: its speed is judged elsewhere, only a failure counts.
		if !slices.Contains(lateKinds, kind) || r.aborted || r.failed == "" && (kind == "media" || secs < lateAfter) {
			continue
		}
		f := &SlowFile{URL: noQuery(r.url), Host: r.host, IP: r.ip, Kind: kind, Ms: int64(secs * 1000),
			Unfinished: !r.done, Error: r.failed, Phases: r.phases(end)}
		f.Phase = mainPhase(f.Phases)
		switch {
		case !r.responded && (r.failed == "" || r.failed == hungError):
			f.Phase = "none"
		case f.Phase == "queue" && slowNet[r.host].ms >= netSlowMs:
			f.Phase = slowNet[r.host].name
		}
		if len(f.Phases) == 0 {
			f.Phases = nil
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(x, y *SlowFile) int {
		if (x.Error != "") != (y.Error != "") {
			return map[bool]int{true: -1, false: 1}[x.Error != ""]
		}
		return int(y.Ms - x.Ms)
	})
	return out[:min(len(out), 3*maxSlow)] // room for the ads the step drops
}

type cdpMsg struct {
	ID        int                       `json:"id,omitempty"`
	Method    string                    `json:"method,omitempty"`
	Params    json.RawMessage           `json:"params,omitempty"`
	Result    json.RawMessage           `json:"result,omitempty"`
	Error     *struct{ Message string } `json:"error,omitempty"`
	SessionID string                    `json:"sessionId,omitempty"`
}

// browserRun is the hidden, automatic browser step.
func browserRun(ctx context.Context, target string) BrowserResult {
	return BrowserSession(ctx, target, BrowserOpts{})
}

// BrowserSession loads target in Edge or Chrome with a fresh throwaway profile and records its network.
// The browser uses the system proxy, like the person's own browser does.
func BrowserSession(ctx context.Context, target string, opts BrowserOpts) (res BrowserResult) {
	exe := findBrowser()
	if exe == "" {
		return BrowserResult{Status: "unavailable"}
	}
	res.Browser = filepath.Base(exe)
	fail := func(err error) BrowserResult {
		return BrowserResult{Status: "error", Browser: filepath.Base(exe), Error: err.Error()}
	}
	dir, err := os.MkdirTemp("", "whynet-browser-*")
	if err != nil {
		return fail(err)
	}
	defer func() { // the browser may hold its profile for a moment after exit
		for i := 0; i < 10 && os.RemoveAll(dir) != nil; i++ {
			time.Sleep(300 * time.Millisecond)
		}
	}()

	if opts.Visible {
		ctx, cancel := context.WithTimeout(ctx, maxVisible+30*time.Second)
		defer cancel()
		host := target
		if u, err := url.Parse(target); err == nil {
			host = u.Hostname()
		}
		return netlogSession(ctx, exe, dir, host, target, opts)
	}

	ctx, cancel := context.WithTimeout(ctx, browserBudget+15*time.Second)
	defer cancel()
	// The debugging port lives on localhost while the check runs, in a throwaway profile.
	// Chromium 111+ refuses a debugging client whose Origin is not allowed explicitly.
	// Edge signs a fresh profile into the Windows Microsoft account and starts sync on its own,
	// which would send the checked pages to the person's account history: both are switched off.
	cmd := exec.CommandContext(ctx, exe, "--headless=new", "--mute-audio",
		"--remote-debugging-port=0", "--user-data-dir="+dir, "--remote-allow-origins=http://127.0.0.1",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "--window-size=1280,900",
		"--disable-sync", "--disable-features=msImplicitSignin", "about:blank")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	wsURL, err := devtoolsURL(ctx, dir)
	if err != nil {
		return fail(err)
	}
	ws, err := websocket.Dial(wsURL, "", "http://127.0.0.1") // must equal --remote-allow-origins exactly
	if err != nil {
		return fail(err)
	}
	defer ws.Close()
	msgs := make(chan cdpMsg, 512)
	go func() {
		defer close(msgs)
		for {
			var m cdpMsg
			if websocket.JSON.Receive(ws, &m) != nil {
				return
			}
			msgs <- m
		}
	}()

	agg := newAgg()
	nextID, session := 0, ""
	send := func(method string, params any) int {
		nextID++
		m := map[string]any{"id": nextID, "method": method}
		if params != nil {
			m["params"] = params
		}
		if session != "" {
			m["sessionId"] = session
		}
		websocket.JSON.Send(ws, m)
		return nextID
	}
	call := func(method string, params any) (json.RawMessage, error) {
		id := send(method, params)
		for {
			select {
			case m, ok := <-msgs:
				if !ok {
					return nil, errors.New("browser closed the connection")
				}
				if m.ID == id {
					if m.Error != nil {
						return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
					}
					return m.Result, nil
				}
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	// Use the tab the browser opened with.
	raw, err := call("Target.getTargets", nil)
	if err != nil {
		return fail(err)
	}
	var tl struct {
		TargetInfos []struct{ TargetID, Type string }
	}
	json.Unmarshal(raw, &tl)
	targetID := ""
	for _, t := range tl.TargetInfos {
		if t.Type == "page" {
			targetID = t.TargetID
			break
		}
	}
	if targetID == "" {
		if raw, err = call("Target.createTarget", map[string]any{"url": "about:blank"}); err != nil {
			return fail(err)
		}
		var tgt struct{ TargetID string }
		json.Unmarshal(raw, &tgt)
		targetID = tgt.TargetID
	}
	raw, err = call("Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true})
	if err != nil {
		return fail(err)
	}
	var att struct{ SessionID string }
	json.Unmarshal(raw, &att)
	session = att.SessionID
	for _, c := range []struct {
		m string
		p any
	}{
		{"Network.enable", nil},
		{"Page.enable", nil},
		// a plain browser agent: some sites turn away "HeadlessChrome"
		{"Network.setUserAgentOverride", map[string]any{"userAgent": userAgent, "acceptLanguage": "ru,en;q=0.8"}},
	} {
		if _, err := call(c.m, c.p); err != nil {
			return fail(err)
		}
	}

	navStart := time.Now()
	navID := send("Page.navigate", map[string]any{"url": target})
	deadline := time.After(browserBudget)
	quiet := time.NewTicker(500 * time.Millisecond)
	defer quiet.Stop()
	var loadedAt, domAt time.Time
	loaded, lastEvent := false, time.Now()
	navErr := ""
loop:
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				break loop
			}
			switch {
			case m.ID == navID:
				var nav struct{ ErrorText string }
				json.Unmarshal(m.Result, &nav)
				if navErr = nav.ErrorText; navErr != "" {
					break loop
				}
			case m.SessionID != session:
			case m.Method == "Page.loadEventFired":
				if !loaded {
					loaded, loadedAt = true, time.Now()
				}
			case m.Method == "Page.domContentEventFired":
				if domAt.IsZero() {
					domAt = time.Now()
				}
			case strings.HasPrefix(m.Method, "Network."):
				agg.event(m.Method, m.Params)
				lastEvent = time.Now()
			}
		case <-quiet.C:
			// Done once the page has loaded and the network is quiet, or 5 s after load:
			// counters and ads keep a page "loading" forever.
			if loaded && (agg.inflight() == 0 && time.Since(lastEvent) > time.Second || time.Since(lastEvent) > 3*time.Second || time.Since(loadedAt) > afterLoad) {
				break loop
			}
			// A hung request (a blocked server) holds back the load event; the page itself is long parsed.
			if !domAt.IsZero() && time.Since(domAt) > afterDOM {
				break loop
			}
		case <-deadline:
			break loop
		case <-ctx.Done():
			break loop
		}
	}
	// The title and the start of the visible text, to recognise a site's own block pages.
	title, text := "", ""
	if raw, err := call("Runtime.evaluate", map[string]any{"returnByValue": true,
		"expression": "[document.title, (document.body && document.body.innerText || '').slice(0, 3000)]"}); err == nil {
		var ev struct{ Result struct{ Value []string } }
		if json.Unmarshal(raw, &ev) == nil && len(ev.Result.Value) == 2 {
			title, text = ev.Result.Value[0], ev.Result.Value[1]
		}
	}
	session = ""
	send("Browser.close", nil)

	// The event clock stops when the network goes quiet; whynet kept waiting for what was still open.
	agg.lastTs += time.Since(lastEvent).Seconds()
	res = agg.result()
	res.Browser = filepath.Base(exe)
	res.Title, res.text = title, text
	if !domAt.IsZero() {
		res.DomMs = domAt.Sub(navStart).Milliseconds()
	}
	if loaded {
		res.LoadMs = loadedAt.Sub(navStart).Milliseconds()
	}
	if navErr != "" && domAt.IsZero() {
		res.Status, res.Error = "nav_failed", navErr
	}
	return res
}

// devtoolsURL waits for the browser to write its debugging port into the profile folder.
func devtoolsURL(ctx context.Context, dir string) (string, error) {
	for {
		if f, err := os.Open(filepath.Join(dir, "DevToolsActivePort")); err == nil {
			sc := bufio.NewScanner(f)
			var lines []string
			for sc.Scan() {
				lines = append(lines, strings.TrimSpace(sc.Text()))
			}
			f.Close()
			if len(lines) >= 2 && lines[0] != "" {
				return "ws://127.0.0.1:" + lines[0] + lines[1], nil
			}
		}
		select {
		case <-ctx.Done():
			return "", errors.New("the browser did not open its debugging port")
		case <-time.After(200 * time.Millisecond):
		}
	}
}
