package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

const (
	MaxHosts     = 20        // servers checked per page
	parallel     = 6         // checked at once
	sampleLimit  = 512 << 10 // bytes read from one sample file
	speedMinRead = 256 << 10 // enough data to judge speed
	slowKBps     = 64        // below this a server counts as throttled
	stallMax     = 64 << 10  // stopping before this much is the "freezes after ~16 KB" pattern
	pingTries    = 5
	resentMs     = 800 // a try this much slower than the best one had a packet resent (Windows waits 1 s)
	minLost      = 2   // lost tries out of pingTries that make a path lossy rather than unlucky
)

// HostCheck is one server the page loads content from, checked on its own.
type HostCheck struct {
	Host   string   `json:"host"`
	Kinds  []string `json:"kinds"`            // image, script, style, media, frame, font, known, browser
	Sample string   `json:"sample,omitempty"` // the file tried, without query
	IP     string   `json:"ip,omitempty"`
	Result string   `json:"result"` // ok, gone, dns, ip, sni, tls, cert, http, stall, slow
	Detail string   `json:"detail,omitempty"`
	Bytes  int64    `json:"bytes,omitempty"`
	KBps   int64    `json:"kbps,omitempty"`
	PingMs int64    `json:"ping_ms,omitempty"` // TCP connect time: the delay a browser feels (ICMP is often filtered)
	// PingTLS: behind a fake-ip router, which answers a connect itself, the ping is a TLS handshake.
	PingTLS bool `json:"ping_tls,omitempty"`
	Lost    int  `json:"lost,omitempty"` // tries out of Tries that lost a packet (resent) or failed
	Tries   int  `json:"tries,omitempty"`
	// BrowserOK: whynet's own check failed, but the browser loaded from this server, so it doesn't count.
	BrowserOK bool   `json:"browser_ok,omitempty"`
	Ms        int64  `json:"ms"`
	sample    string // full URL, never reported (signed CDN links carry tokens)
}

// failedResults are the outcomes that mean "this content won't load".
var failedResults = []string{"dns", "ip", "sni", "tls", "cert", "http", "stall"}

// pageResources lists the servers a page pulls content from, in page order, one sample file each.
func pageResources(body []byte, base *url.URL) []*HostCheck {
	var out []*HostCheck
	seen := map[string]*HostCheck{}
	add := func(raw, kind string) {
		if raw = strings.TrimSpace(raw); raw == "" {
			return // a missing attribute would resolve to the page itself
		}
		u, err := base.Parse(raw)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return
		}
		host := strings.ToLower(u.Hostname())
		h := seen[host]
		if h == nil {
			h = &HostCheck{Host: host, sample: u.String()}
			seen[host] = h
			out = append(out, h)
		}
		if !slices.Contains(h.Kinds, kind) {
			h.Kinds = append(h.Kinds, kind)
		}
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, more := z.TagName()
		attrs := map[string]string{}
		for more {
			var k, v []byte
			k, v, more = z.TagAttr()
			attrs[string(k)] = string(v)
		}
		switch string(name) {
		case "img":
			add(attrs["src"], "image")
			addSrcset(attrs["srcset"], "image", add)
		case "script":
			add(attrs["src"], "script")
		case "link":
			switch rel := strings.ToLower(attrs["rel"]); {
			case strings.Contains(rel, "stylesheet"):
				add(attrs["href"], "style")
			case strings.Contains(rel, "icon"):
				add(attrs["href"], "image")
			case strings.Contains(rel, "preload"):
				kind := map[string]string{"script": "script", "style": "style", "image": "image", "font": "font", "video": "media"}[attrs["as"]]
				if kind == "" {
					kind = "script"
				}
				add(attrs["href"], kind)
			}
		case "video", "audio":
			add(attrs["src"], "media")
			add(attrs["poster"], "image")
		case "source":
			add(attrs["src"], "media")
			addSrcset(attrs["srcset"], "image", add)
		case "iframe":
			add(attrs["src"], "frame")
		case "meta":
			if p := attrs["property"]; p == "og:image" || p == "og:video" {
				add(attrs["content"], map[bool]string{true: "image", false: "media"}[p == "og:image"])
			}
		}
	}
}

func addSrcset(srcset, kind string, add func(raw, kind string)) {
	for _, part := range strings.Split(srcset, ",") {
		if f := strings.Fields(part); len(f) > 0 {
			add(f[0], kind)
		}
	}
}

// BrowserKinds maps Chromium resource types to the kinds the report names.
func BrowserKinds(types []string) []string {
	var out []string
	for _, t := range types {
		k, ok := map[string]string{"image": "image", "script": "script", "stylesheet": "style", "font": "font",
			"media": "media", "document": "frame"}[t]
		if !ok {
			k = "data" // xhr, fetch, ping, websocket: data the page's scripts exchange
		}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out
}

// relatedHosts are servers a site is known to use beyond its HTML (video, images loaded by scripts).
func relatedHosts(host string, related map[string][]string) []string {
	for site, hosts := range related {
		if host == site || strings.HasSuffix(host, "."+site) {
			return hosts
		}
	}
	return nil
}

func (p *prober) content(s *Step) {
	if !p.pageOK && len(p.browserHosts) == 0 {
		s.Facts["res.checked"] = "0"
		s.Status = "skip"
		return
	}
	var hosts []*HostCheck
	seen := map[string]bool{p.host: true}
	add := func(h *HostCheck) {
		if !seen[h.Host] && len(hosts) < MaxHosts {
			seen[h.Host] = true
			hosts = append(hosts, h)
		}
	}
	// Servers the browser stumbled on or waited for come first: they are the ones to explain.
	// Then what the HTML names, known servers of the site, and the rest the browser used.
	for _, b := range p.browserHosts {
		if slices.Contains(p.browserFailed, b.Host) || slices.Contains(p.browserLate, b.Host) {
			add(&HostCheck{Host: b.Host, Kinds: BrowserKinds(b.Types)})
		}
	}
	if p.body != nil && p.finalURL != nil {
		for _, h := range pageResources(p.body, p.finalURL) {
			add(h)
		}
	}
	for _, h := range relatedHosts(p.host, p.env.Markers.Related) {
		add(&HostCheck{Host: h, Kinds: []string{"known"}})
	}
	// Ads last: their failures never count, so they shouldn't take the places of the page's own servers.
	for _, ads := range []bool{false, true} {
		for _, b := range p.browserHosts {
			if p.env.Markers.IsAd(b.Host) == ads {
				add(&HostCheck{Host: b.Host, Kinds: BrowserKinds(b.Types)})
			}
		}
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, parallel)
	for _, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					h.Result, h.Detail = "error", "panic"
				}
			}()
			p.checkHost(h)
		}()
	}
	wg.Wait()

	// What the real browser loaded fine outranks whynet's own request to the same server.
	browserOK := map[string]bool{}
	for _, b := range p.browserHosts {
		browserOK[b.Host] = b.Requests > b.Failed
	}
	var failed, stalled, slow, lossy []string
	worst, minKBps, mostLost := "", int64(-1), 0
	for _, h := range hosts {
		if p.env.Markers.IsAd(h.Host) {
			h.Kinds = append(h.Kinds, "ads")
			continue
		}
		if h.Lost >= minLost {
			lossy = append(lossy, h.Host)
			if h.Lost > mostLost {
				mostLost = h.Lost
				s.Facts["res.lossy_lost"], s.Facts["res.lossy_tries"] = strconv.Itoa(h.Lost), strconv.Itoa(h.Tries)
			}
			if slices.Contains(p.browserLate, h.Host) {
				s.Facts["res.late_lossy"] = "true"
			}
		}
		if browserOK[h.Host] && slices.Contains(failedResults, h.Result) {
			h.BrowserOK = true
			continue
		}
		switch {
		case h.Result == "stall":
			stalled = append(stalled, h.Host)
		case h.Result == "slow":
			slow = append(slow, h.Host)
			if minKBps < 0 || h.KBps < minKBps {
				minKBps = h.KBps
			}
		}
		if slices.Contains(failedResults, h.Result) {
			failed = append(failed, h.Host)
			if worst == "" || slices.Index(failedResults, h.Result) < slices.Index(failedResults, worst) {
				worst = h.Result
			}
		}
	}
	// The browser's own timings of big files count too: that is where throttled video shows.
	for _, host := range slices.Sorted(maps.Keys(p.browserSlow)) {
		if p.env.Markers.IsAd(host) {
			continue
		}
		if !slices.Contains(slow, host) {
			slow = append(slow, host)
		}
		if kbps := p.browserSlow[host]; minKBps < 0 || kbps < minKBps {
			minKBps = kbps
		}
	}
	// Every server the browser failed on is a name no DNS knows: dead links in the page, nothing
	// to fix on this side (a youtube.com page named a googlevideo node that doesn't exist, live).
	result := map[string]string{}
	for _, h := range hosts {
		result[h.Host] = h.Result
	}
	dead := len(p.browserFailed) > 0
	for _, host := range p.browserFailed {
		dead = dead && result[host] == "gone"
	}
	if dead {
		s.Facts["res.browser_dead"] = "true"
	}
	s.Facts["res.checked"] = strconv.Itoa(len(hosts))
	s.Facts["res.failed"] = strconv.Itoa(len(failed))
	if len(failed) > 0 {
		s.Facts["res.problem"] = "true"
		s.Facts["res.worst"] = worst
		s.Facts["res.failed_hosts"] = shortList(failed)
		s.Status = "warn"
	}
	if len(stalled) > 0 {
		s.Facts["res.stall"] = "true"
		s.Facts["res.stall_hosts"] = shortList(stalled)
	}
	if len(lossy) > 0 {
		s.Facts["res.lossy"] = "true"
		s.Facts["res.lossy_hosts"] = shortList(lossy)
		s.Status = "warn"
	}
	if len(slow) > 0 {
		s.Facts["res.slow"] = "true"
		s.Facts["res.slow_hosts"] = shortList(slow)
		s.Facts["res.slow_kbps"] = strconv.FormatInt(minKBps, 10)
		s.Status = "warn"
	}
	if len(hosts) > 0 {
		s.Evidence["hosts"] = hosts
	}
}

func shortList(hosts []string) string {
	if len(hosts) > 3 {
		return strings.Join(hosts[:3], ", ") + " +" + strconv.Itoa(len(hosts)-3)
	}
	return strings.Join(hosts, ", ")
}

// checkHost walks one content server: DNS, TCP, TLS (with the control name on a cut), then one file.
func (p *prober) checkHost(h *HostCheck) {
	start := time.Now()
	defer func() { h.Ms = time.Since(start).Milliseconds() }()
	if h.sample != "" {
		if u, err := url.Parse(h.sample); err == nil {
			h.Sample = u.Scheme + "://" + u.Host + u.EscapedPath()
		}
	}

	ctx, cancel := p.timeout()
	ips, err := p.env.Lookup(ctx, h.Host)
	cancel()
	if dnsClass(ips, err) != "ok" {
		if doh, derr := p.env.DoH(p.ctx, h.Host); derr == nil && len(doh.IPs) > 0 {
			h.Result, h.Detail = "dns", "system DNS: "+dnsClass(ips, err)+"; DoH: "+doh.IPs[0]
		} else {
			h.Result, h.Detail = "gone", errText(err) // a dead link, not a network problem
		}
		return
	}
	h.IP = ips[0]

	connect := func() error {
		ctx, cancel := p.timeout()
		defer cancel()
		c, err := p.env.Dial(ctx, "tcp", net.JoinHostPort(h.IP, "443"))
		if err == nil {
			c.Close()
		}
		return err
	}
	if err := ping(h, connect); err != nil {
		h.Result, h.Detail = "ip", "connect: "+netClass(err)
		return
	}

	if _, err := p.handshake(h.IP, h.Host, false); err != nil {
		switch cls := tlsClass(err); cls {
		case "reset", "timeout", "eof", "error", "refused", "unreachable":
			_, cerr := p.handshake(h.IP, controlSNI, true)
			h.Result = "ip"
			if cerr == nil || tlsClass(cerr) == "alert" {
				h.Result = "sni" // the server answers another name: the cut is on this one
			}
			h.Detail = cls + ": " + err.Error()
		case "cert_expired", "cert_name", "cert_untrusted", "cert_invalid":
			h.Result, h.Detail = "cert", cls
		default:
			h.Result, h.Detail = "tls", cls+": "+err.Error()
		}
		return
	}
	// A fake-ip router answers the connect itself in no time: the way to the server shows in the handshake.
	if fakeIP([]string{h.IP}) {
		h.PingTLS = true
		ping(h, func() error {
			_, err := p.handshake(h.IP, h.Host, false)
			return err
		})
	}

	target := h.sample
	if target == "" || strings.HasPrefix(target, "http://") {
		target = "https://" + h.Host + "/"
	}
	// Speed counts only for a real file: a server's root or an embedded page is generated, not sent.
	p.fetchSample(h, target, target == h.sample && !(len(h.Kinds) == 1 && h.Kinds[0] == "frame"))
}

// ping times pingTries tries of try into h: the typical time and how many tries lost a packet.
// The first try's error is returned (the server doesn't answer); a later one counts as lost and ends the tries.
func ping(h *HostCheck, try func() error) error {
	var times []int64
	failed := 0
	for i := range pingTries {
		if i > 0 {
			time.Sleep(100 * time.Millisecond) // a burst of connects can trip a server's rate limit
		}
		start := time.Now()
		if err := try(); err != nil {
			if i == 0 {
				return err
			}
			failed = 1
			break
		}
		times = append(times, time.Since(start).Milliseconds())
	}
	h.PingMs, h.Lost = pingStats(times)
	h.Lost += failed
	h.Tries = len(times) + failed
	return nil
}

// pingStats: a try resentMs slower than the best one had its first packet lost and resent;
// the ping is the median of the others.
func pingStats(times []int64) (ping int64, lost int) {
	best := slices.Min(times)
	var ok []int64
	for _, t := range times {
		if t >= best+resentMs {
			lost++
		} else {
			ok = append(ok, t)
		}
	}
	slices.Sort(ok)
	return ok[len(ok)/2], lost
}

// fetchSample downloads up to sampleLimit of one file, watching for a stall and measuring speed.
func (p *prober) fetchSample(h *HostCheck, target string, measureSpeed bool) {
	ctx, cancel := context.WithTimeout(p.ctx, 3*p.env.Timeout)
	defer cancel()
	cl := &http.Client{
		// Any answer, a redirect too, shows the server works; following redirects without the
		// site's cookies can loop (ozon.ru) and would look like a dead server.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if host, port, _ := net.SplitHostPort(addr); host == h.Host {
					addr = net.JoinHostPort(h.IP, port)
				}
				return p.env.Dial(ctx, network, addr)
			},
			TLSClientConfig:   &tls.Config{RootCAs: p.env.RootCAs},
			DisableKeepAlives: true,
		}}
	req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		h.Result, h.Detail = "http", err.Error()
		return
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := cl.Do(req)
	if err != nil {
		h.Result, h.Detail = "ip", netClass(err)+": "+err.Error()
		return
	}
	defer resp.Body.Close()

	// A watchdog cancels the read when no byte has come for stallAfter.
	stallAfter := p.env.Timeout * 2 / 3
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	stalled := atomic.Bool{}
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(stallAfter / 8)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) > stallAfter {
					stalled.Store(true)
					cancel()
					return
				}
			}
		}
	}()
	readStart := time.Now()
	buf := make([]byte, 32<<10)
	for h.Bytes < sampleLimit {
		n, err := resp.Body.Read(buf)
		h.Bytes += int64(n)
		if n > 0 {
			last.Store(time.Now().UnixNano())
		}
		if err != nil {
			if err != io.EOF && !stalled.Load() {
				h.Result, h.Detail = "ip", "read: "+netClass(err)
				return
			}
			break
		}
	}
	elapsed := time.Since(readStart)

	switch {
	case stalled.Load() && h.Bytes < stallMax:
		h.Result, h.Detail = "stall", "no data after "+strconv.FormatInt(h.Bytes/1024, 10)+" KB"
	case resp.StatusCode >= 500:
		h.Result, h.Detail = "http", "HTTP "+strconv.Itoa(resp.StatusCode)
	default:
		h.Result, h.Detail = "ok", "HTTP "+strconv.Itoa(resp.StatusCode)
		if measureSpeed && h.Bytes >= speedMinRead && elapsed > 0 {
			h.KBps = int64(float64(h.Bytes) / 1024 / elapsed.Seconds())
			if h.KBps < slowKBps {
				h.Result = "slow"
			}
		}
	}
}
