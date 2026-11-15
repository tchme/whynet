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
	Stubs        []string `yaml:"stubs"`
	Geo          []string `yaml:"geo"`
	Interceptors []string `yaml:"interceptors"`
	VPNAdapters  []string `yaml:"vpn_adapters"`
}

// Local is what the "local" step learns about this computer.
type Local struct {
	Facts    map[string]string
	Evidence map[string]any
	Mask     map[string]string
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
	RootCAs *x509.CertPool // nil: the system store
	Timeout time.Duration  // per network operation
	Markers Markers
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
		Timeout: 6 * time.Second,
		Markers: m,
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
	p.step("local", p.local)
	p.step("dns", p.dns)
	p.step("tcp", p.tcp)
	p.step("tls", p.tls)
	p.step("http", p.http)
	return p.res, nil
}

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
	if !p.tcpOK {
		s.Status = "fail"
	}
}
