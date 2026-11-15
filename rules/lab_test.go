package rules_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"

	"whynet/internal/probe"
	"whynet/rules"
)

// The lab is a fake network: each "ip:port" leads to a real local server or a scripted failure.
// httptest certificates are valid for example.com, so that is the site under test.
type lab struct {
	routes map[string]string // addr -> real local addr | "refused"; unknown addrs time out
	pool   *x509.CertPool
}

func (l *lab) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	to, ok := l.routes[addr]
	switch {
	case !ok:
		<-ctx.Done() // a black hole, like a dropped SYN
		return nil, ctx.Err()
	case to == "refused":
		// a real refused connection on Windows loopback takes ~2 s of SYN retries, so it is scripted
		return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connectex", syscall.Errno(10061))}
	}
	return (&net.Dialer{}).DialContext(ctx, network, to)
}

func (l *lab) env(sys []string, sysErr error, doh []string, dohErr error) probe.Env {
	_, markers, _ := rules.Load()
	return probe.Env{
		Lookup: func(context.Context, string) ([]string, error) { return sys, sysErr },
		DoH: func(context.Context, string) (probe.DoHAnswer, error) {
			return probe.DoHAnswer{Server: "lab", IPs: doh}, dohErr
		},
		Dial: l.dial,
		Local: func(context.Context, string, probe.Markers) probe.Local {
			return probe.Local{Facts: map[string]string{"local.net": "ok"}}
		},
		RootCAs: l.pool,
		Timeout: 300 * time.Millisecond,
		Markers: markers,
	}
}

func serve(t *testing.T, code int, body string) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// dpiServer resets the connection when the ClientHello names blocked, like an ISP's DPI.
func dpiServer(t *testing.T, cert tls.Certificate, blocked string) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert},
					GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
						if h.ServerName == blocked {
							c.(*net.TCPConn).SetLinger(0) // close with RST
							c.Close()
						}
						return nil, nil
					}})
				tc.Handshake()
				tc.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

func TestLab(t *testing.T) {
	good := serve(t, 200, "<title>ok</title>")
	broken := serve(t, 502, "bad gateway")
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Доступ ограничен, см. eais.rkn.gov.ru")
	}))
	t.Cleanup(stub.Close)
	pool := x509.NewCertPool()
	pool.AddCert(good.Certificate())
	pool.AddCert(broken.Certificate())
	dpi := dpiServer(t, good.TLS.Certificates[0], "example.com")

	l := &lab{pool: pool, routes: map[string]string{
		"10.0.0.1:443": good.Listener.Addr().String(),
		"10.0.0.2:443": dpi,
		"10.0.0.3:443": dpi,
		"10.0.0.3:80":  stub.Listener.Addr().String(),
		"10.0.0.4:443": "refused",
		"10.0.0.5:443": broken.Listener.Addr().String(),
	}}
	ip := func(s ...string) []string { return s }
	notFound := &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}

	cases := []struct {
		name, site string
		env        probe.Env
		want       string
	}{
		{"works", "example.com", l.env(ip("10.0.0.1"), nil, ip("10.0.0.1"), nil), "all_ok"},
		{"DPI cuts the name", "example.com", l.env(ip("10.0.0.2"), nil, ip("10.0.0.2"), nil), "sni_blocked"},
		{"ISP stub over HTTP", "example.com", l.env(ip("10.0.0.3"), nil, ip("10.0.0.3"), nil), "isp_stub"},
		{"spoofed DNS", "example.com", l.env(ip("10.0.0.9"), nil, ip("10.0.0.1"), nil), "dns_spoofed"},
		{"black hole", "example.com", l.env(ip("10.0.0.9"), nil, ip("10.0.0.9"), nil), "ip_unreachable"},
		{"refused", "example.com", l.env(ip("10.0.0.4"), nil, ip("10.0.0.4"), nil), "server_broken"},
		{"HTTP 502", "example.com", l.env(ip("10.0.0.5"), nil, ip("10.0.0.5"), nil), "server_broken"},
		{"no such site", "example.com", l.env(nil, notFound, nil, probe.ErrNXDomain), "no_such_site"},
		{"ISP DNS down", "example.com", l.env(nil, &net.DNSError{Err: "timeout", IsTimeout: true}, ip("10.0.0.1"), nil), "isp_dns_down"},
		{"wrong certificate name", "other.example.net", l.env(ip("10.0.0.1"), nil, ip("10.0.0.1"), nil), "bad_certificate"},
		{"fake-ip proxy path broken", "example.com", l.env(ip("198.18.0.5"), nil, ip("10.0.0.1"), nil), "proxy_path_broken"},
	}
	rs, _, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := probe.Run(context.Background(), c.env, c.site, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := rules.Match(rs, res.Facts()); got.ID != c.want {
				t.Errorf("verdict %s, want %s; facts %v", got.ID, c.want, res.Facts())
			}
		})
	}
}

// A step that panics is recorded, and the check goes on.
func TestPanicIsContained(t *testing.T) {
	good := serve(t, 200, "ok")
	pool := x509.NewCertPool()
	pool.AddCert(good.Certificate())
	l := &lab{pool: pool, routes: map[string]string{"10.0.0.1:443": good.Listener.Addr().String()}}
	env := l.env([]string{"10.0.0.1"}, nil, []string{"10.0.0.1"}, nil)
	env.Local = func(context.Context, string, probe.Markers) probe.Local { panic("boom") }

	res, err := probe.Run(context.Background(), env, "example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Status != "error" || res.Steps[0].Evidence["panic"] == nil {
		t.Errorf("local step = %+v, want status error with the panic in evidence", res.Steps[0])
	}
	if f := res.Facts(); f["http.status"] != "200" {
		t.Errorf("later steps did not run: %v", f)
	}
}
