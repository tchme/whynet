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
	Requests int            `json:"requests"`
	Failed   int            `json:"failed"`
	Hosts    []*BrowserHost `json:"hosts,omitempty"`
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
)

func (p *prober) browser(s *Step) {
	if !p.pageOK || p.env.Browser == nil {
		s.Facts["br.status"] = "off"
		s.Status = "skip"
		return
	}
	r := p.env.Browser(p.ctx, p.finalURL.String())
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
	p.browserHosts = r.Hosts
	s.Facts["br.requests"] = strconv.Itoa(r.Requests)
	if firstIn(strings.ToLower(r.Title), p.env.Markers.Challenge) != "" {
		s.Facts["br.antibot"] = "true" // what lies behind the check stays unseen
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
	url, host, kind string
	start, end      float64 // CDP timestamps, seconds
	bytes           int64
	failed          string
	done, responded bool
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
			r.responded = true
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
			r.end, r.done = e.Timestamp, true
		}
	}
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
				if u, err := url.Parse(r.url); err == nil {
					h.Sample = u.Scheme + "://" + u.Host + u.EscapedPath()
				}
			}
		}
	}
	for host, secs := range bigSecs {
		if secs > 0 {
			byHost[host].KBps = int64(float64(bigBytes[host]) / 1024 / secs)
		}
	}
	slices.SortStableFunc(res.Hosts, func(x, y *BrowserHost) int { return y.Failed - x.Failed })
	return res
}

type cdpMsg struct {
	ID        int                       `json:"id,omitempty"`
	Method    string                    `json:"method,omitempty"`
	Params    json.RawMessage           `json:"params,omitempty"`
	Result    json.RawMessage           `json:"result,omitempty"`
	Error     *struct{ Message string } `json:"error,omitempty"`
	SessionID string                    `json:"sessionId,omitempty"`
}

// browserRun loads target in a hidden Edge or Chrome with a fresh profile and records its network.
// The browser uses the system proxy, like the person's own browser does.
func browserRun(ctx context.Context, target string) (res BrowserResult) {
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

	ctx, cancel := context.WithTimeout(ctx, browserBudget+15*time.Second)
	defer cancel()
	// The debugging port lives on localhost for the few seconds of the check, in a throwaway profile.
	// Chromium 111+ refuses a debugging client whose Origin is not allowed explicitly.
	cmd := exec.CommandContext(ctx, exe, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+dir,
		"--remote-allow-origins=http://127.0.0.1",
		"--no-first-run", "--no-default-browser-check", "--disable-extensions", "--mute-audio",
		"--window-size=1280,900", "about:blank")
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

	raw, err := call("Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		return fail(err)
	}
	var tgt struct{ TargetID string }
	json.Unmarshal(raw, &tgt)
	raw, err = call("Target.attachToTarget", map[string]any{"targetId": tgt.TargetID, "flatten": true})
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
				loaded, loadedAt = true, time.Now()
			case m.Method == "Page.domContentEventFired":
				domAt = time.Now()
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
	title := ""
	if navErr == "" {
		if raw, err := call("Runtime.evaluate", map[string]any{"expression": "document.title", "returnByValue": true}); err == nil {
			var ev struct{ Result struct{ Value any } }
			json.Unmarshal(raw, &ev)
			title, _ = ev.Result.Value.(string)
		}
	}
	session = ""
	send("Browser.close", nil)

	res = agg.result()
	res.Browser = filepath.Base(exe)
	res.Title = title
	if navErr != "" {
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
