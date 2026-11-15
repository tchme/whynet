package probe

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// A visible window runs as a plain browser: no debugging port, which turns on navigator.webdriver
// and gets it turned away by anti-bot checks (ozon.ru does). Chromium's own network log
// (--log-net-log) records what the page loads instead.

// netlogParser reads a Chromium NetLog stream line by line into the same totals as the DevTools path.
type netlogParser struct {
	ready                                 bool
	urlRequest, start, alive, headers, rd int // source and event type ids from the log's constants
	errNames                              map[int]string
	site                                  string       // registrable domain of the page under check
	ignored                               map[int]bool // requests of other top sites: the browser's own traffic
	agg                                   *cdpAgg
}

func newNetlogParser(pageHost string) *netlogParser {
	site, err := publicsuffix.EffectiveTLDPlusOne(pageHost)
	if err != nil {
		site = pageHost
	}
	return &netlogParser{site: site, ignored: map[int]bool{}, agg: newAgg()}
}

func (n *netlogParser) line(b []byte) {
	b = bytes.TrimSpace(b)
	b = bytes.TrimSuffix(b, []byte(","))
	if len(b) == 0 || b[0] != '{' {
		return // `"events": [` and the closing lines
	}
	if !n.ready {
		var c struct {
			Constants struct {
				LogEventTypes map[string]int `json:"logEventTypes"`
				LogSourceType map[string]int `json:"logSourceType"`
				NetError      map[string]int `json:"netError"`
			} `json:"constants"`
		}
		if json.Unmarshal(append(b, '}'), &c) != nil || c.Constants.LogEventTypes == nil {
			return
		}
		e := c.Constants.LogEventTypes
		n.urlRequest = c.Constants.LogSourceType["URL_REQUEST"]
		n.start, n.alive = e["URL_REQUEST_START_JOB"], e["REQUEST_ALIVE"]
		n.headers, n.rd = e["HTTP_TRANSACTION_READ_RESPONSE_HEADERS"], e["URL_REQUEST_JOB_FILTERED_BYTES_READ"]
		n.errNames = map[int]string{}
		for name, code := range c.Constants.NetError {
			n.errNames[code] = "net::" + name
		}
		n.ready = true
		return
	}
	var ev struct {
		Params json.RawMessage
		Phase  int
		Source struct{ ID, Type int }
		Time   string
		Type   int
	}
	if json.Unmarshal(b, &ev) != nil || ev.Source.Type != n.urlRequest || n.ignored[ev.Source.ID] {
		return
	}
	ms, _ := strconv.ParseFloat(ev.Time, 64)
	ts := ms / 1000
	n.agg.lastTs = max(n.agg.lastTs, ts)
	var p struct {
		URL         string `json:"url"`
		NIK         string `json:"network_isolation_key"`
		RequestType string `json:"request_type"`
		NetError    int    `json:"net_error"`
		ByteCount   int64  `json:"byte_count"`
	}
	json.Unmarshal(ev.Params, &p)
	id := strconv.Itoa(ev.Source.ID)
	r := n.agg.reqs[id]

	if ev.Type == n.start && ev.Phase == 1 && p.URL != "" {
		u, err := url.Parse(p.URL)
		top, _, _ := strings.Cut(p.NIK, " ")
		topURL, _ := url.Parse(top)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || topURL == nil || !n.sameSite(topURL.Hostname()) {
			if r == nil {
				n.ignored[ev.Source.ID] = true
			}
			return
		}
		if r == nil {
			r = &cdpReq{start: ts}
			n.agg.reqs[id] = r
			n.agg.order = append(n.agg.order, id)
		}
		r.url, r.host, r.kind = p.URL, strings.ToLower(u.Hostname()), urlKind(u, p.RequestType) // a redirect moves on to the new URL
		return
	}
	if r == nil {
		return
	}
	switch {
	case p.NetError == -3: // ERR_ABORTED: the page cancelled it
		r.done, r.end = true, ts
	case p.NetError < 0:
		r.failed, r.done, r.end = n.errNames[p.NetError], true, ts
		if r.failed == "" {
			r.failed = "net::ERR_" + strconv.Itoa(p.NetError)
		}
	}
	switch ev.Type {
	case n.alive:
		if ev.Phase == 2 {
			r.done, r.end = true, ts
		}
	case n.headers:
		r.responded = true
	case n.rd:
		r.bytes += p.ByteCount
	}
}

func (n *netlogParser) sameSite(host string) bool {
	site, err := publicsuffix.EffectiveTLDPlusOne(host)
	return err == nil && site == n.site
}

// urlKind guesses a request's type from its URL: NetLog has no resource type.
func urlKind(u *url.URL, requestType string) string {
	if requestType == "main frame" || requestType == "subframe" {
		return "document"
	}
	if strings.Contains(u.Path, "videoplayback") {
		return "media"
	}
	switch strings.ToLower(path.Ext(u.Path)) {
	case ".js", ".mjs":
		return "script"
	case ".css":
		return "stylesheet"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif", ".svg", ".ico":
		return "image"
	case ".woff", ".woff2", ".ttf", ".otf":
		return "font"
	case ".mp4", ".webm", ".m3u8", ".m4s", ".ts", ".mp3", ".aac", ".ogg":
		return "media"
	}
	return "" // unknown: no hang detection, no speed
}

// netlogTail feeds the parser whatever the browser appended to its log since the last call.
type netlogTail struct {
	path    string
	offset  int64
	partial []byte
	p       *netlogParser
}

func (t *netlogTail) read() {
	f, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		chunk, err := r.ReadBytes('\n')
		t.offset += int64(len(chunk))
		if err != nil { // no newline yet: keep the piece for next time
			t.partial = append(t.partial, chunk...)
			return
		}
		t.p.line(append(t.partial, chunk...))
		t.partial = t.partial[:0]
	}
}

// netlogSession opens target in a normal window and waits for the person: "Готово" (opts.Done),
// closing the window, or maxVisible. Then it reads what the page loaded from the network log.
func netlogSession(ctx context.Context, exe, dir, host, target string, opts BrowserOpts) BrowserResult {
	res := BrowserResult{Browser: filepath.Base(exe), Visible: true}
	logPath := filepath.Join(dir, "net.json")
	// --test-type hides the "unsupported command-line flag" bar that --log-net-log brings up.
	cmd := exec.CommandContext(ctx, exe, "--user-data-dir="+dir, "--no-first-run", "--no-default-browser-check",
		"--disable-sync", "--disable-features=msImplicitSignin", "--window-size=1280,900",
		"--log-net-log="+logPath, "--test-type", target)
	if err := cmd.Start(); err != nil {
		res.Status, res.Error = "error", err.Error()
		return res
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()

	tail := &netlogTail{path: logPath, p: newNetlogParser(host)}
	pid := uint32(cmd.Process.Pid)
	started := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	limit := time.After(maxVisible)
	title := ""
wait:
	for {
		select {
		case <-tick.C:
			tail.read()
			if t := windowTitle(pid); t != "" {
				title = t
			}
			if opts.Progress != nil {
				opts.Progress(BrowserProgress{Requests: len(tail.p.agg.order), Failed: tail.p.agg.liveFailed(), Seconds: int(time.Since(started).Seconds())})
			}
		case <-exited:
			break wait // the person closed the window
		case <-opts.Done:
			break wait
		case <-limit:
			break wait
		case <-ctx.Done():
			break wait
		}
	}
	// Close like the person would (the log is finished on a normal exit), then make sure.
	closeWindows(pid)
	select {
	case <-exited:
	case <-time.After(8 * time.Second):
		cmd.Process.Kill()
		<-exited
	}
	tail.read()
	res = tail.p.agg.result()
	res.Browser, res.Visible, res.Title = filepath.Base(exe), true, pageTitle(title)
	return res
}

// pageTitle drops the browser's own part of a window title: "Page — Профиль 1: Microsoft Edge".
func pageTitle(windowTitle string) string {
	for _, sep := range []string{" — ", " - "} {
		if i := strings.LastIndex(windowTitle, sep); i > 0 {
			return windowTitle[:i]
		}
	}
	return windowTitle
}
