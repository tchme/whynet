package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// hostsLine is one active line of the hosts file.
type hostsLine struct {
	Text  string   // the line exactly as in the file (without the line break): the fix matches it
	Addr  string   // the address the names are sent to
	Names []string // lowercase
	Kind  string   // pin (a public address), block (0.0.0.0, loopback), local (LAN, link-local)
}

// ncsiNames are what Windows asks to decide "connected to the internet": blocking them makes the
// tray say "no internet" and some programs refuse to go online.
var ncsiNames = []string{"msftconnecttest.com", "msftncsi.com"}

// parseHosts reads the active lines of a hosts file; comments, blanks and malformed lines are skipped.
func parseHosts(content string) []hostsLine {
	var out []hostsLine
	for _, raw := range strings.Split(content, "\n") {
		raw = strings.TrimRight(raw, "\r")
		l, _, _ := strings.Cut(raw, "#")
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		ip, err := netip.ParseAddr(f[0])
		if err != nil {
			continue
		}
		h := hostsLine{Text: raw, Addr: ip.String(), Kind: "pin"}
		for _, n := range f[1:] {
			h.Names = append(h.Names, strings.ToLower(n))
		}
		switch {
		case ip.IsUnspecified() || ip.IsLoopback():
			h.Kind = "block"
		case ip.IsPrivate() || ip.IsLinkLocalUnicast():
			h.Kind = "local"
		}
		out = append(out, h)
	}
	return out
}

// blocksNCSI reports whether a block line sends Windows' connectivity check names nowhere.
func blocksNCSI(lines []hostsLine) bool {
	for _, l := range lines {
		if l.Kind != "block" {
			continue
		}
		for _, n := range l.Names {
			for _, d := range ncsiNames {
				if n == d || strings.HasSuffix(n, "."+d) {
					return true
				}
			}
		}
	}
	return false
}

// Pin results: the name is served there (ok), cut by its name only (cut: DPI, the pin may still be
// wanted), the certificate can't be trusted at all (untrusted: an interceptor or the clock);
// the stale ones: the address doesn't answer (dead), it answers as another site (other_cert),
// it answers TLS for no name (no_tls).
var staleResults = []string{"dead", "other_cert", "no_tls"}

// pinCheck tests a pinned address: one TCP connect per address, then a TLS handshake per name.
type pinCheck struct {
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)
	Wait time.Duration // per connect / handshake
	Cap  int           // names tested at most; the rest are "unchecked"
	Par  int           // at once
}

// pinResult is what pinCheck learned about one name.
type pinResult struct {
	Addr, Name, Result string
}

// run checks every pinned name (up to Cap) and returns a result per name it tested.
func (c pinCheck) run(ctx context.Context, lines []hostsLine) (res []pinResult, unchecked int) {
	type job struct{ addr, name string }
	var jobs []job
	seen := map[job]bool{}
	for _, l := range lines {
		if l.Kind != "pin" {
			continue
		}
		for _, n := range l.Names {
			j := job{l.Addr, n}
			if !seen[j] {
				seen[j] = true
				jobs = append(jobs, j)
			}
		}
	}
	if len(jobs) > c.Cap {
		unchecked, jobs = len(jobs)-c.Cap, jobs[:c.Cap]
	}
	// Dead addresses first, once each: a dead one makes every name on it stale without a handshake.
	alive := map[string]bool{}
	var addrs []string
	for _, j := range jobs {
		if _, ok := alive[j.addr]; !ok {
			alive[j.addr] = false
			addrs = append(addrs, j.addr)
		}
	}
	var mu sync.Mutex
	parallel(c.Par, len(addrs), func(i int) {
		ok := c.connect(ctx, addrs[i])
		mu.Lock()
		alive[addrs[i]] = ok
		mu.Unlock()
	})
	res = make([]pinResult, len(jobs))
	parallel(c.Par, len(jobs), func(i int) {
		j := jobs[i]
		r := "dead"
		if alive[j.addr] {
			r = c.handshake(ctx, j.addr, j.name)
		}
		res[i] = pinResult{j.addr, j.name, r}
	})
	return res, unchecked
}

func (c pinCheck) connect(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, c.Wait)
	defer cancel()
	conn, err := c.Dial(ctx, "tcp", net.JoinHostPort(addr, "443"))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// handshake: ok, other_cert, untrusted, cut, no_tls (see staleResults).
func (c pinCheck) handshake(ctx context.Context, addr, name string) string {
	err := c.tls(ctx, addr, &tls.Config{ServerName: name})
	var host x509.HostnameError
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &host):
		return "other_cert"
	case errors.As(err, &unknown), errors.As(err, &invalid):
		return "untrusted"
	}
	// The handshake broke: with a neutral name too, or only with this one (cut by name: DPI)?
	if c.tls(ctx, addr, &tls.Config{ServerName: "example.org", InsecureSkipVerify: true}) == nil {
		return "cut"
	}
	return "no_tls"
}

func (c pinCheck) tls(ctx context.Context, addr string, cfg *tls.Config) error {
	ctx, cancel := context.WithTimeout(ctx, c.Wait)
	defer cancel()
	raw, err := c.Dial(ctx, "tcp", net.JoinHostPort(addr, "443"))
	if err != nil {
		return err
	}
	conn := tls.Client(raw, cfg)
	defer conn.Close()
	return conn.HandshakeContext(ctx)
}

// staleLines are the pin lines whose every name came back stale; names with no result (unchecked) keep a line.
func staleLines(lines []hostsLine, res []pinResult) []hostsLine {
	got := map[[2]string]string{}
	for _, r := range res {
		got[[2]string{r.Addr, r.Name}] = r.Result
	}
	var out []hostsLine
	for _, l := range lines {
		if l.Kind != "pin" {
			continue
		}
		stale := true
		for _, n := range l.Names {
			if !slices.Contains(staleResults, got[[2]string{l.Addr, n}]) {
				stale = false
				break
			}
		}
		if stale {
			out = append(out, l)
		}
	}
	return out
}

// parallel runs f(0..n-1), at most par at a time. A panic in one call is dropped (that item keeps
// its zero result): the area's own recover can't catch it in another goroutine, and it would end the app.
func parallel(par, n int, f func(int)) {
	sem := make(chan struct{}, max(par, 1))
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { recover(); <-sem; wg.Done() }()
			f(i)
		}()
	}
	wg.Wait()
}
