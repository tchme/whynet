package health

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
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
	for _, raw := range strings.Split(strings.TrimPrefix(content, "\ufeff"), "\n") {
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

// A refused address (dead), a different certificate name (other_cert) and two explicit TLS
// failures (no_tls) can mark a pin stale. A timeout, cancellation or unknown result is unchecked.
var staleResults = []string{"dead", "other_cert", "no_tls"}

// pinCheck tests a pinned address: one TCP connect per address, then a TLS handshake per name.
type pinCheck struct {
	Dial  func(ctx context.Context, network, addr string) (net.Conn, error)
	Roots *x509.CertPool // nil uses the system roots
	Wait  time.Duration  // per connect / handshake
	Cap   int            // names tested at most; the rest are "unchecked"
	Par   int            // at once
}

// pinResult is what pinCheck learned about one name.
type pinResult struct {
	Addr, Name, Result string
}

// run checks every unique pinned pair up to Cap and counts pairs without a conclusive check.
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
	// Check TCP once per address. A missing worker result leaves each of its names unchecked.
	connections := map[string]string{}
	var addrs []string
	for _, j := range jobs {
		if _, ok := connections[j.addr]; !ok {
			connections[j.addr] = ""
			addrs = append(addrs, j.addr)
		}
	}
	var mu sync.Mutex
	parallel(c.Par, len(addrs), func(i int) {
		status := c.connect(ctx, addrs[i])
		mu.Lock()
		connections[addrs[i]] = status
		mu.Unlock()
	})
	res = make([]pinResult, len(jobs))
	for i, j := range jobs {
		res[i] = pinResult{j.addr, j.name, "unchecked"}
	}
	parallel(c.Par, len(jobs), func(i int) {
		r := &res[i]
		switch connections[r.Addr] {
		case "dead":
			r.Result = "dead"
		case "ok":
			r.Result = c.handshake(ctx, r.Addr, r.Name)
		}
	})
	for _, r := range res {
		if r.Result == "unchecked" {
			unchecked++
		}
	}
	return res, unchecked
}

func (c pinCheck) connect(ctx context.Context, addr string) string {
	ctx, cancel := context.WithTimeout(ctx, c.Wait)
	defer cancel()
	if ctx.Err() != nil {
		return "unchecked"
	}
	conn, err := c.Dial(ctx, "tcp", net.JoinHostPort(addr, "443"))
	if conn != nil {
		defer conn.Close()
	}
	switch {
	case ctx.Err() != nil:
		return "unchecked"
	case err == nil && conn != nil:
		return "ok"
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.Errno(10061)): // WSAECONNREFUSED
		return "dead"
	default:
		return "unchecked"
	}
}

func (c pinCheck) handshake(ctx context.Context, addr, name string) string {
	err := c.tls(ctx, addr, &tls.Config{ServerName: name, RootCAs: c.Roots})
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
	if !pinTLSFailure(err) {
		return "unchecked"
	}
	// The handshake broke: with a neutral name too, or only with this one (cut by name: DPI)?
	control := c.tls(ctx, addr, &tls.Config{ServerName: "example.org", InsecureSkipVerify: true})
	if control == nil {
		return "cut"
	}
	if pinTLSFailure(control) {
		return "no_tls"
	}
	return "unchecked"
}

func pinTLSFailure(err error) bool {
	var ne net.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	var header tls.RecordHeaderError
	var alert tls.AlertError
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &header) || errors.As(err, &alert) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.Errno(10054)) // WSAECONNRESET
}

func (c pinCheck) tls(ctx context.Context, addr string, cfg *tls.Config) error {
	ctx, cancel := context.WithTimeout(ctx, c.Wait)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := c.Dial(ctx, "tcp", net.JoinHostPort(addr, "443"))
	if raw != nil {
		defer raw.Close()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	err = tls.Client(raw, cfg).HandshakeContext(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
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
