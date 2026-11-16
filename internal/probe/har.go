package probe

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// A person's own browser sees what whynet's clean one can't: its extensions, ad blocker, built-in VPN.
// Its network record (HAR, from DevTools: "Copy all as HAR") is read down to addresses, types,
// statuses, errors and timings, and compared with whynet's check of the same site: the "own" step.

var (
	ErrNotHAR     = errors.New("not a HAR record")
	ErrEmptyHAR   = errors.New("the HAR record has no requests")
	ErrOneRequest = errors.New("the HAR record has one request: one row was copied, not all")
)

// OwnRecord is a person's own browser record, without cookies, headers or bodies (never read).
type OwnRecord struct {
	Site     string     `json:"site"`     // host of the page the record was made on
	PageURL  string     `json:"page_url"` // without the query
	Creator  string     `json:"creator,omitempty"`
	Requests int        `json:"requests"`
	ShownMs  int64      `json:"shown_ms,omitempty"` // until the page was parsed and shown (onContentLoad)
	Files    []*OwnFile `json:"files,omitempty"`    // what was stopped, failed or came late: the ones to show
}

// OwnFile is one request of the record worth showing, and what whynet saw of the same file or server.
type OwnFile struct {
	URL    string           `json:"url"` // without the query: signed links carry tokens
	Host   string           `json:"host"`
	IP     string           `json:"ip,omitempty"`
	Kind   string           `json:"kind"`             // image, script, style, font, media, frame, data
	What   string           `json:"what"`             // blocked (by the browser itself), failed, none (no answer), late
	Ms     int64            `json:"ms,omitempty"`     // the request's whole time
	Phase  string           `json:"phase,omitempty"`  // late files: where most of the time went (PhaseOrder)
	Phases map[string]int64 `json:"phases,omitempty"` // ms per phase
	Error  string           `json:"error,omitempty"`  // the browser's error, or "HTTP 502"
	Ad     bool             `json:"ad,omitempty"`
	Whynet *OwnCompare      `json:"whynet,omitempty"` // nil: whynet's browser never touched this server
}

// OwnCompare is whynet's own browser on the same file, or failing that, on the same server.
type OwnCompare struct {
	File     bool  `json:"file"` // the very file; else only its server
	OK       bool  `json:"ok"`
	Ms       int64 `json:"ms,omitempty"`
	Loaded   int   `json:"loaded,omitempty"` // server: requests that came, of Requests
	Requests int   `json:"requests,omitempty"`
}

// harTimings are a HAR entry's phases in ms; -1 when a phase didn't happen.
type harTimings struct {
	Blocked, DNS, Connect, SSL, Send, Wait, Receive float64
}

type harEntry struct {
	Started string  `json:"startedDateTime"`
	Time    float64 `json:"time"`
	Request struct {
		URL string `json:"url"`
	} `json:"request"`
	Response struct {
		Status  int    `json:"status"`
		Error   string `json:"_error"` // Chromium: net::ERR_BLOCKED_BY_CLIENT and the like
		Content struct {
			MimeType string `json:"mimeType"`
		} `json:"content"`
	} `json:"response"`
	ServerIP     string     `json:"serverIPAddress"`
	ResourceType string     `json:"_resourceType"` // Chromium only
	Timings      harTimings `json:"timings"`
}

// ParseHAR reads a HAR text into an OwnRecord with every request kept for OwnStep.
// Only the fields above are decoded: cookies, headers and bodies are skipped by the decoder.
func ParseHAR(b []byte, m Markers) (*OwnRecord, error) {
	var h struct {
		Log *struct {
			Creator struct{ Name string } `json:"creator"`
			Pages   []struct {
				Title       string `json:"title"` // Chromium: the page's URL; Firefox: its title
				PageTimings struct {
					OnContentLoad float64 `json:"onContentLoad"`
				} `json:"pageTimings"`
			} `json:"pages"`
			Entries []harEntry `json:"entries"`
		} `json:"log"`
	}
	if json.Unmarshal(b, &h) != nil || h.Log == nil {
		return nil, ErrNotHAR
	}
	switch len(h.Log.Entries) {
	case 0:
		return nil, ErrEmptyHAR
	case 1:
		return nil, ErrOneRequest
	}
	rec := &OwnRecord{Creator: h.Log.Creator.Name, Requests: len(h.Log.Entries)}
	page := ""
	if len(h.Log.Pages) > 0 {
		page = h.Log.Pages[0].Title
		if t := h.Log.Pages[0].PageTimings.OnContentLoad; t > 0 {
			rec.ShownMs = int64(t)
		}
	}
	if u, err := url.Parse(page); err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		page = h.Log.Entries[0].Request.URL // the page itself comes first
	}
	u, err := url.Parse(page)
	if err != nil || u.Hostname() == "" {
		return nil, ErrNotHAR
	}
	rec.Site, rec.PageURL = strings.ToLower(u.Hostname()), noQuery(page)
	rec.Files = ownFiles(h.Log.Entries, m)
	return rec, nil
}

// SameSite reports whether two hosts belong to one site (dzen.ru and m.dzen.ru do).
func SameSite(a, b string) bool {
	sa, err1 := publicsuffix.EffectiveTLDPlusOne(a)
	sb, err2 := publicsuffix.EffectiveTLDPlusOne(b)
	return a == b || err1 == nil && err2 == nil && sa == sb
}

// ownFiles keeps the requests worth showing: stopped by the browser, failed, unanswered, or late.
func ownFiles(entries []harEntry, m Markers) []*OwnFile {
	type phase struct {
		name string
		ms   int64
	}
	slowNet := map[string]phase{} // a server's slowest DNS/connect/TLS: files queued behind it (HTTP/2) blame it
	var out []*OwnFile
	for _, e := range entries {
		u, err := url.Parse(e.Request.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			continue
		}
		host := strings.ToLower(u.Hostname())
		ph := e.Timings.phases()
		for _, k := range []string{"dns", "connect", "tls"} {
			if ph[k] > slowNet[host].ms {
				slowNet[host] = phase{k, ph[k]}
			}
		}
		f := &OwnFile{URL: noQuery(e.Request.URL), Host: host, IP: strings.Trim(e.ServerIP, "[]"), Kind: harKind(e, u),
			Ms: int64(max(e.Time, 0)), Ad: m.IsAd(host)}
		switch err := e.Response.Error; {
		case err == "net::ERR_BLOCKED_BY_CLIENT":
			f.What, f.Error, f.Ms = "blocked", err, 0
		case err == "net::ERR_ABORTED":
			continue // the page cancelled it
		case strings.HasPrefix(err, "net::"):
			f.What, f.Error = "failed", err
		case e.Response.Status == 0:
			if f.Kind == "data" {
				continue // beacons and long polls stay open by design
			}
			f.What = "none" // still open when the record was copied, or never sent (Firefox)
		case e.Response.Status >= 500:
			f.What, f.Error = "failed", "HTTP "+strconv.Itoa(e.Response.Status)
		case float64(f.Ms) >= lateAfter*1000 && slices.Contains(lateKinds, f.Kind) && f.Kind != "media":
			f.What, f.Phases = "late", ph
			f.Phase = mainPhase(ph)
		default:
			continue
		}
		out = append(out, f)
	}
	for _, f := range out {
		if f.Phase == "queue" && slowNet[f.Host].ms >= netSlowMs {
			f.Phase = slowNet[f.Host].name
		}
	}
	// The page's own servers stopped by the browser first: the likely culprits. Ads and counters last.
	rank := func(f *OwnFile) int {
		switch {
		case f.Ad:
			return 4
		case f.What == "blocked":
			return 0
		case f.What == "late":
			return 2
		}
		return 1
	}
	slices.SortStableFunc(out, func(x, y *OwnFile) int {
		if rank(x) != rank(y) {
			return rank(x) - rank(y)
		}
		return int(y.Ms - x.Ms)
	})
	return out
}

func (t harTimings) phases() map[string]int64 {
	ph := map[string]int64{}
	set := func(k string, ms float64) {
		if ms >= 1 {
			ph[k] = int64(ms)
		}
	}
	set("queue", t.Blocked)
	set("dns", t.DNS)
	if t.Connect >= 0 {
		set("connect", t.Connect-max(t.SSL, 0)) // HAR's connect includes TLS
	}
	set("tls", t.SSL)
	set("wait", max(t.Send, 0)+t.Wait)
	set("download", t.Receive)
	return ph
}

// harKind is a request's kind in the report's words: Chromium's type, else the MIME type, else the URL.
func harKind(e harEntry, u *url.URL) string {
	if e.ResourceType != "" {
		return BrowserKinds([]string{strings.ToLower(e.ResourceType)})[0]
	}
	mime := strings.ToLower(e.Response.Content.MimeType)
	for _, p := range [][2]string{{"image/", "image"}, {"text/css", "style"}, {"javascript", "script"}, {"font", "font"},
		{"video/", "media"}, {"audio/", "media"}, {"text/html", "frame"}} {
		if strings.Contains(mime, p[0]) {
			return p[1]
		}
	}
	if k := urlKind(u, ""); k != "" {
		return BrowserKinds([]string{k})[0]
	}
	return "data"
}

// OwnStep is the "own" step: the person's record against whynet's check in res.
func OwnStep(rec *OwnRecord, res *Result) Step {
	s := Step{ID: "own", Status: "ok", Facts: map[string]string{}, Evidence: map[string]any{"record": rec}}
	var br *BrowserResult
	for _, st := range res.Steps {
		if b, ok := st.Evidence["browser"].(BrowserResult); ok {
			br = &b
		}
	}
	if br != nil {
		hosts := map[string]*BrowserHost{}
		for _, h := range br.Hosts {
			hosts[h.Host] = h
		}
		for _, f := range rec.Files {
			if seen, ok := br.files[f.URL]; ok {
				f.Whynet = &OwnCompare{File: true, OK: seen.ok, Ms: seen.ms}
			} else if h := hosts[f.Host]; h != nil {
				f.Whynet = &OwnCompare{OK: h.Requests > h.Failed, Loaded: h.Requests - h.Failed, Requests: h.Requests}
			}
		}
	}
	s.Facts["own.site"] = rec.Site
	s.Facts["own.requests"] = strconv.Itoa(rec.Requests)
	if rec.ShownMs > 0 {
		s.Facts["own.shown_s"] = strconv.FormatFloat(float64(rec.ShownMs)/1000, 'f', 1, 64)
	}
	var blocked, site, failed, late []string
	nBlocked, nFailed := 0, 0
	for _, f := range rec.Files {
		switch {
		case f.What == "blocked":
			nBlocked++
			blocked = appendNew(blocked, f.Host)
			if !f.Ad {
				site = appendNew(site, f.Host)
			}
		case f.Ad: // an ad server failing or slow is not the page's trouble
		case f.What == "late":
			if len(late) == 0 {
				s.Facts["own.late_file"] = shortURL(f.URL)
				s.Facts["own.late_s"] = strconv.FormatFloat(float64(f.Ms)/1000, 'f', 1, 64)
				if f.Phase != "" {
					s.Facts["own.late_phase"] = f.Phase
				}
			}
			late = appendNew(late, f.Host)
		default:
			nFailed++
			failed = appendNew(failed, f.Host)
		}
	}
	if nBlocked > 0 {
		s.Facts["own.blocked"], s.Facts["own.blocked_n"], s.Facts["own.blocked_hosts"] = "true", strconv.Itoa(nBlocked), shortList(blocked)
	}
	if len(site) > 0 {
		s.Facts["own.blocked_site"], s.Facts["own.blocked_site_hosts"] = "true", shortList(site)
	}
	if nFailed > 0 {
		s.Facts["own.failed"], s.Facts["own.failed_n"], s.Facts["own.failed_hosts"] = "true", strconv.Itoa(nFailed), shortList(failed)
	}
	if len(late) > 0 {
		s.Facts["own.late"], s.Facts["own.late_hosts"] = "true", shortList(late)
	}
	if nBlocked+nFailed+len(late) > 0 {
		s.Status = "warn"
	} else {
		s.Facts["own.clean"] = "true"
	}
	return s
}

func appendNew(list []string, s string) []string {
	if slices.Contains(list, s) {
		return list
	}
	return append(list, s)
}
