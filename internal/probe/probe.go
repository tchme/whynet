// Package probe walks the path from this computer to a site and records
// facts and raw evidence. It never decides what is wrong: the rules do.
package probe

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// Step is one stage of the path. Facts feed the rules; Evidence is raw
// detail for the AI report and the "Details" view.
type Step struct {
	ID       string            `json:"id"`
	Status   string            `json:"status"` // ok, warn, fail, skip, error
	Ms       int64             `json:"ms"`
	Facts    map[string]string `json:"facts"`
	Evidence map[string]any    `json:"evidence,omitempty"`
}

// Result is one full check of one site.
type Result struct {
	Input   string    `json:"input"`
	Host    string    `json:"host"`
	Started time.Time `json:"started"`
	Steps   []Step    `json:"steps"`
	// Mask maps private values (public IP, computer name) to what the report shows instead.
	Mask map[string]string `json:"-"`
}

// Facts merges every step's facts into one map, plus "host".
func (r *Result) Facts() map[string]string {
	f := map[string]string{"host": r.Host}
	for _, s := range r.Steps {
		maps.Copy(f, s.Facts)
	}
	return f
}

// Markers are lowercase substrings that let probes label what they saw.
type Markers struct {
	Stubs []string `yaml:"stubs"`
	// Any ISP's stub: one word of the blocking law and one of "access restricted" on the same page.
	StubLaw      []string `yaml:"stub_law"`
	StubAction   []string `yaml:"stub_action"`
	Geo          []string `yaml:"geo"`
	Interceptors []string `yaml:"interceptors"`
	VPNAdapters  []string `yaml:"vpn_adapters"`
	// DPI-bypass tools: process exe name and Windows service name -> tool.
	DPIProcesses map[string]string `yaml:"dpi_processes"`
	DPIServices  map[string]string `yaml:"dpi_services"`
	WinDivert    []string          `yaml:"windivert_drivers"`
	// Servers a site loads content from by script, not named in its HTML: site -> hosts.
	Related map[string][]string `yaml:"related"`
	// Ads and counters: reported, but their failures don't make a site "broken".
	// Ads is the hand-made list; AdSet the big built-in one (rules/ads.txt.gz).
	Ads   []string        `yaml:"ads"`
	AdSet map[string]bool `yaml:"-"`
	// Anti-bot check pages, matched in the page title.
	Challenge []string `yaml:"challenge"`
	// A site's own "turn off your VPN" page, matched in the title and visible text.
	VPNBlock []string `yaml:"vpn_block"`
	// Files a page asks for that whynet's hidden browser sometimes never gets, on any network:
	// not counted as late (matched in the address).
	LateNoise []string `yaml:"late_noise"`
	// Browsers by exe name: the app check points them to the site check.
	Browsers []string `yaml:"browsers"`
	// The browser's own services seen in a page's traffic: dropped from the page's servers.
	BrowserOwn []string `yaml:"browser_own"`
	// The Windows network health check: virtual machines' adapters (not the PC's own link) and
	// programs known to throttle or reshape traffic, by lowercase exe name -> name to show.
	VirtualAdapters []string          `yaml:"virtual_adapters"`
	Throttlers      map[string]string `yaml:"throttlers"`
}

// IsAd reports whether host or one of its parent domains is an ad or tracker server.
func (m Markers) IsAd(host string) bool {
	if isUnder(host, m.Ads) {
		return true
	}
	for d := host; strings.Contains(d, "."); d = d[strings.Index(d, ".")+1:] {
		if m.AdSet[d] {
			return true
		}
	}
	return false
}

// IsVPN reports whether an adapter belongs to a VPN client (its name or description has a marker).
func (m Markers) IsVPN(a Adapter) bool {
	return firstIn(strings.ToLower(a.Name+" "+a.Description), m.VPNAdapters) != ""
}

// isUnder reports whether host is one of the domains or their subdomain.
func isUnder(host string, domains []string) bool {
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Local is what the "local" step learns about this computer.
type Local struct {
	Facts    map[string]string
	Evidence map[string]any
	Mask     map[string]string
	DPILists map[string]string // a DPI-bypass bundle's list files by name; nil when none was found
}

// DoHAnswer is an answer from an independent DNS-over-HTTPS resolver.
type DoHAnswer struct {
	Server string   `json:"server"`
	IPs    []string `json:"ips"`
}

// ErrNXDomain is returned by Env.DoH when the domain does not exist.
var ErrNXDomain = errors.New("no such domain")

// ErrBadInput means the input does not look like a site address.
var ErrBadInput = errors.New("not a site address")

// Env is everything a check touches outside its own logic; tests swap it.
type Env struct {
	Lookup  func(ctx context.Context, host string) ([]string, error) // system resolver, IPv4
	DoH     func(ctx context.Context, host string) (DoHAnswer, error)
	Dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	Local   func(ctx context.Context, host string, m Markers) Local
	Browser func(ctx context.Context, url string) BrowserResult // nil: skip the browser step
	// BrowserAlways runs the browser step even when the direct request got no page
	// (a visible window can pass what whynet's own request could not).
	BrowserAlways bool
	RootCAs       *x509.CertPool // nil: the system store
	Timeout       time.Duration  // per network operation
	Markers       Markers
	// The app check's names and owners; nil skips them (the lab has no internet).
	PTR func(ctx context.Context, ip string) ([]string, error)
	ASN func(ctx context.Context, ip string) (asn, name string)
	// Echo is one ICMP ping (the app check's UDP servers); nil skips it. The caller sets it
	// (package live has the ICMP code and imports this package).
	Echo func(ip netip.Addr, timeout time.Duration) (time.Duration, bool)
}

// DefaultEnv is the real network of this computer.
func DefaultEnv(m Markers) Env {
	return Env{
		Lookup: func(ctx context.Context, host string) ([]string, error) {
			// shortcut: IPv4 only, add IPv6 when a diagnosis needs it
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", host)
			out := make([]string, len(ips))
			for i, ip := range ips {
				out[i] = ip.Unmap().String()
			}
			return out, err
		},
		DoH:     dohLookup,
		Dial:    (&net.Dialer{}).DialContext,
		Local:   localInfo,
		Browser: browserRun,
		Timeout: 6 * time.Second,
		Markers: m,
		PTR:     net.DefaultResolver.LookupAddr,
		ASN:     ASN,
	}
}

// Target turns what the person typed into an ASCII host and a path.
func Target(input string) (host, path string, err error) {
	s := strings.TrimSpace(input)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || strings.ContainsAny(u.Hostname(), " \t") {
		return "", "", ErrBadInput
	}
	host, err = idna.Lookup.ToASCII(strings.TrimSuffix(strings.ToLower(u.Hostname()), "."))
	if err != nil || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return "", "", ErrBadInput
	}
	path = u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return host, path, nil
}

// Run checks one site step by step. onStep (may be nil) sees each step as it ends.
// It always returns a full Result: a step that fails or panics is recorded, never fatal.
func Run(ctx context.Context, env Env, input string, onStep func(Step)) (*Result, error) {
	host, path, err := Target(input)
	if err != nil {
		return nil, err
	}
	p := &prober{ctx: ctx, env: env, host: host, path: path, onStep: onStep,
		res: &Result{Input: input, Host: host, Started: time.Now(), Mask: map[string]string{}}}
	fns := map[string]func(*Step){"local": p.local, "dns": p.dns, "tcp": p.tcp, "tls": p.tls, "http": p.http, "browser": p.browser, "res": p.content}
	for _, id := range StepIDs {
		p.step(id, fns[id])
	}
	return p.res, nil
}

// StepIDs is the order of the steps in every check.
var StepIDs = []string{"local", "dns", "tcp", "tls", "http", "browser", "res"}

type prober struct {
	ctx        context.Context
	env        Env
	host, path string
	onStep     func(Step)
	res        *Result

	ips       []string // candidates for the connection, best first
	ip        string   // the address the connection uses
	dohIP     string   // independent answer, kept when it differs
	dnsDiffer bool
	tcpOK     bool
	tlsOK     bool
	dpiLists  map[string]string // from the local step

	pageOK        bool             // the page itself loaded: content steps have something to check
	httpDowngrade bool             // the HTTP probe stopped at an HTTPS-to-HTTP redirect
	body          []byte           // the page's HTML
	finalURL      *url.URL         // after redirects
	browserHosts  []*BrowserHost   // every server the browser used, failed first
	browserFailed []string         // servers the browser could not load from
	browserLate   []string         // servers whose files came late or never, worst first
	browserSlow   map[string]int64 // servers the browser loaded big files from slowly -> KB/s
}

func (p *prober) step(id string, fn func(*Step)) {
	s := Step{ID: id, Facts: map[string]string{}, Evidence: map[string]any{}}
	start := time.Now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				s.Status = "error"
				s.Evidence["panic"] = fmt.Sprintf("%v\n%s", r, debug.Stack())
			}
		}()
		fn(&s)
	}()
	s.Ms = time.Since(start).Milliseconds()
	if s.Status == "" {
		s.Status = "ok"
	}
	if len(s.Evidence) == 0 {
		s.Evidence = nil
	}
	p.res.Steps = append(p.res.Steps, s)
	if p.onStep != nil {
		p.onStep(s)
	}
}

func (p *prober) timeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(p.ctx, p.env.Timeout)
}

func (p *prober) local(s *Step) {
	l := p.env.Local(p.ctx, p.host, p.env.Markers)
	maps.Copy(s.Facts, l.Facts)
	maps.Copy(s.Evidence, l.Evidence)
	maps.Copy(p.res.Mask, l.Mask)
	p.dpiLists = l.DPILists
	switch {
	case s.Facts["local.net"] == "none":
		s.Status = "fail"
	case s.Facts["local.proxy"] == "on", s.Facts["local.hosts_entry"] == "true", s.Facts["local.clock"] == "wrong":
		s.Status = "warn"
	}
}

func (p *prober) dns(s *Step) {
	if net.ParseIP(p.host) != nil {
		s.Facts["dns.system"] = "literal"
		s.Status = "skip"
		p.ips, p.ip = []string{p.host}, p.host
		return
	}
	ctx, cancel := p.timeout()
	sys, err := p.env.Lookup(ctx, p.host)
	cancel()
	s.Facts["dns.system"] = dnsClass(sys, err)
	s.Evidence["system"] = answer(sys, err)

	doh, err := p.env.DoH(p.ctx, p.host)
	switch {
	case errors.Is(err, ErrNXDomain):
		s.Facts["dns.doh"] = "nxdomain"
	case err != nil:
		s.Facts["dns.doh"] = "unavailable"
	case len(doh.IPs) == 0:
		s.Facts["dns.doh"] = "empty"
	default:
		s.Facts["dns.doh"] = "ok"
	}
	s.Evidence["doh"] = map[string]any{"server": doh.Server, "answer": answer(doh.IPs, err)}

	sysOK, dohOK := s.Facts["dns.system"] == "ok", s.Facts["dns.doh"] == "ok"
	switch {
	case sysOK && fakeIP(sys):
		// A proxy (often on the router) answers with stand-in addresses and carries the traffic itself.
		s.Facts["dns.match"] = "fakeip"
	case sysOK && dohOK && !overlap(sys, doh.IPs):
		s.Facts["dns.match"] = "differ"
		p.dnsDiffer, p.dohIP = true, doh.IPs[0]
	case sysOK && dohOK:
		s.Facts["dns.match"] = "same"
	default:
		s.Facts["dns.match"] = "unknown"
	}
	switch {
	case sysOK:
		p.ips = sys
		s.Facts["dns.used"] = "system"
	case dohOK:
		p.ips = doh.IPs
		s.Facts["dns.used"] = "doh"
	}
	switch {
	case len(p.ips) == 0:
		s.Status = "fail"
	case !sysOK || p.dnsDiffer:
		s.Status = "warn"
	}
}

func (p *prober) tcp(s *Step) {
	if len(p.ips) == 0 {
		s.Facts["tcp.status"] = "skip"
		s.Status = "skip"
		return
	}
	var tries []map[string]any
	status := ""
	for _, ip := range p.ips[:min(3, len(p.ips))] {
		ctx, cancel := p.timeout()
		start := time.Now()
		c, err := p.env.Dial(ctx, "tcp", net.JoinHostPort(ip, "443"))
		cancel()
		cls := netClass(err)
		tries = append(tries, map[string]any{"ip": ip, "port": 443, "result": cls, "error": errText(err), "ms": time.Since(start).Milliseconds()})
		if err == nil {
			c.Close()
			p.ip, p.tcpOK, status = ip, true, "ok"
			break
		}
		if status == "" {
			status = cls
		}
	}
	if p.ip == "" {
		p.ip = p.ips[0] // HTTP on port 80 still tries it
	}
	s.Facts["tcp.status"] = status
	s.Facts["tcp.ip"] = p.ip
	s.Evidence["tries"] = tries
	// zapret's address lists: a site can get the bypass by its address alone (Cloudflare, Google ranges).
	if p.dpiLists != nil {
		v, entry := ipListed(p.dpiLists, p.ip)
		s.Facts["tcp.dpi_ipset"] = v
		if entry != "" {
			s.Evidence["dpi_ipset_entry"] = entry
		}
	}
	if !p.tcpOK {
		s.Status = "fail"
	}
}
