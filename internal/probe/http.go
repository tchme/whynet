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
	if (code >= 400 && s.Facts["http.challenge"] != "true") || s.Facts["http.stub"] == "true" {
		s.Status = "fail"
	}
	if scheme == "https" && s.Facts["http.stub"] != "true" && (code < 400 || s.Facts["http.challenge"] == "true") {
		p.pageOK, p.body, p.finalURL = true, body, resp.Request.URL
	}
}
