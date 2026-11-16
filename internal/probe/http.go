package probe

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// A browser-like agent: some sites refuse unknown clients, which would look like a failure.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func (p *prober) http(s *Step) {
	if p.ip == "" {
		s.Facts["http.status"] = "skip"
		s.Status = "skip"
		return
	}
	// Over HTTPS when TLS works; otherwise plain HTTP, where ISP stub pages show up.
	scheme := "https"
	if !p.tlsOK {
		scheme = "http"
	}
	s.Facts["http.scheme"] = scheme

	var chain []map[string]any
	jar, _ := cookiejar.New(nil) // sites like ozon.ru set a cookie and redirect; without it they loop
	cl := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			Proxy: nil, // direct: a system proxy is a separate fact
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if h, port, _ := net.SplitHostPort(addr); h == p.host {
					addr = net.JoinHostPort(p.ip, port)
				}
				return p.env.Dial(ctx, network, addr)
			},
			TLSClientConfig:   &tls.Config{RootCAs: p.env.RootCAs},
			DisableKeepAlives: true,
		},
		Timeout: 2 * p.env.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			chain = append(chain, map[string]any{"status": req.Response.StatusCode, "to": req.URL.String()})
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(p.ctx, "GET", scheme+"://"+p.host+p.path, nil)
	if err != nil {
		s.Facts["http.status"] = "error"
		s.Evidence["error"] = err.Error()
		s.Status = "fail"
		return
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "ru,en;q=0.8")
	resp, err := cl.Do(req)
	if len(chain) > 0 {
		s.Evidence["redirects"] = chain
	}
	if err != nil {
		s.Facts["http.status"] = netClass(err)
		s.Facts["http.class"] = "fail"
		s.Evidence["error"] = err.Error()
		s.Status = "fail"
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // whole pages: the content step reads their resources
	resp.Body.Close()

	code := resp.StatusCode
	s.Facts["http.status"] = strconv.Itoa(code)
	s.Facts["http.class"] = strconv.Itoa(code/100) + "xx"
	// stub and geo texts are looked for near the top only: a big page mentions anything somewhere
	page := strings.ToLower(resp.Request.URL.Host + " " + string(body[:min(len(body), 64<<10)]))
	if m := firstIn(page, p.env.Markers.Stubs); m != "" {
		s.Facts["http.stub"] = "true"
		s.Evidence["stub_marker"] = m
	}
	// An unlisted ISP's stub: on the plain-HTTP fallback (the real name was cut), a page citing the
	// blocking law and saying access is restricted; Russian ISPs must cite it (Beeline, ER-Telecom).
	// The whole page, not its top: ER-Telecom's text starts after ~70 KB of inline styles.
	// shortcut: UTF-8 pages only; decode windows-1251 when a stub in it shows up.
	if scheme == "http" && s.Facts["http.stub"] != "true" {
		text := strings.NewReplacer("&nbsp;", " ", string(rune(0xa0)), " ").Replace(strings.ToLower(string(body)))
		if law, act := firstIn(text, p.env.Markers.StubLaw), firstIn(text, p.env.Markers.StubAction); law != "" && act != "" {
			s.Facts["http.stub"] = "true"
			s.Evidence["stub_marker"] = law + " + " + act
		}
	}
	// The fallback ended on another site's plain-HTTP page: most likely an ISP's stub nobody listed.
	if u := resp.Request.URL; scheme == "http" && u.Scheme == "http" && !sameSite(u.Hostname(), p.host) {
		s.Facts["http.other_site"] = "true"
		s.Facts["http.other_host"] = u.Hostname()
	}
	if code == 451 || code >= 400 && firstIn(page, p.env.Markers.Geo) != "" {
		s.Facts["http.geo"] = "true"
	}
	title := ""
	if m := titleRe.FindSubmatch(body); m != nil {
		title = strings.TrimSpace(string(m[1]))
		s.Evidence["title"] = title
	}
	// An anti-bot check answers scripts, not people: a real browser passes it, so the site is up.
	if strings.EqualFold(resp.Header.Get("Cf-Mitigated"), "challenge") || firstIn(strings.ToLower(title), p.env.Markers.Challenge) != "" {
		s.Facts["http.challenge"] = "true"
	}

	s.Evidence["final_url"] = resp.Request.URL.String()
	s.Evidence["proto"] = resp.Proto
	s.Evidence["body_bytes_read"] = len(body)
	headers := map[string]string{}
	for _, h := range []string{"Server", "Content-Type", "Location", "Via", "Cf-Ray", "Cf-Mitigated", "X-Cache"} {
		if v := resp.Header.Get(h); v != "" {
			headers[h] = v
		}
	}
	s.Evidence["headers"] = headers
	if scheme == "https" && resp.Header.Get("Cf-Ray") != "" {
		p.seenFrom(s, cl)
	}
	if (code >= 400 && s.Facts["http.challenge"] != "true") || s.Facts["http.stub"] == "true" || s.Facts["http.other_site"] == "true" {
		s.Status = "fail"
	}
	if scheme == "https" && s.Facts["http.stub"] != "true" && (code < 400 || s.Facts["http.challenge"] == "true") {
		p.pageOK, p.body, p.finalURL = true, body, resp.Request.URL
	}
}

// seenFrom asks a Cloudflare-fronted site where it sees the person from (/cdn-cgi/trace, on every
// such site) and compares it with what an independent server sees: a different country means
// the way to this site goes through a VPN or proxy (a router's included).
func (p *prober) seenFrom(s *Step, cl *http.Client) {
	req, err := http.NewRequestWithContext(p.ctx, "GET", "https://"+p.host+"/cdn-cgi/trace", nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := cl.Do(req)
	if err != nil {
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	kv := parseTrace(string(body))
	if resp.StatusCode != 200 || kv["loc"] == "" {
		return
	}
	s.Facts["http.seen_country"] = kv["loc"]
	s.Facts["http.seen_colo"] = kv["colo"]
	if kv["ip"] != "" {
		s.Evidence["seen_ip"] = kv["ip"]
		p.res.Mask[kv["ip"]] = maskIP(kv["ip"]) // may be the person's own VPS
	}
	if home := p.res.Facts()["local.country"]; home != "" {
		s.Facts["http.via_vpn"] = strconv.FormatBool(home != kv["loc"])
	}
}

// sameSite: both names belong to one registered domain (www.instagram.com and instagram.com).
func sameSite(a, b string) bool {
	if a == b {
		return true
	}
	ra, err := publicsuffix.EffectiveTLDPlusOne(a)
	if err != nil {
		return false
	}
	rb, err := publicsuffix.EffectiveTLDPlusOne(b)
	return err == nil && ra == rb
}
