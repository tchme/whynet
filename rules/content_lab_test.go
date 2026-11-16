package rules_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"whynet/internal/probe"
	"whynet/rules"
)

// labCert is a self-signed certificate (its own root) for the given names.
func labCert(t *testing.T, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

func serveWith(t *testing.T, cert tls.Certificate, h http.HandlerFunc) string {
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// The page opens; what it loads is blocked by name, frozen after 16 KB, or fine.
func TestContentLab(t *testing.T) {
	cert, pool := labCert(t, "example.com", "*.example.com")
	page := func(html string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, html) }
	}
	ok := serveWith(t, cert, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "console.log(1)") })
	frozen := serveWith(t, cert, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "200000")
		w.Write([]byte(strings.Repeat("a", 16<<10)))
		w.(http.Flusher).Flush()
		select { // then nothing, like the "freezes after ~16 KB" block
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	dpi := dpiServer(t, cert, "img.example.com", false)

	var browser func(context.Context, string) probe.BrowserResult // nil: no browser step
	run := func(html string) (map[string]string, rules.Verdict) {
		main := serveWith(t, cert, page(html))
		l := &lab{pool: pool, routes: map[string]string{
			"10.0.0.1:443": main, "10.0.0.2:443": dpi, "10.0.0.7:443": ok, "10.0.0.8:443": frozen,
		}}
		env := l.env(nil, nil, nil, nil)
		env.Browser = browser
		dns := map[string]string{"example.com": "10.0.0.1", "img.example.com": "10.0.0.2", "cdn.example.com": "10.0.0.7", "slow.example.com": "10.0.0.8"}
		env.Lookup = func(_ context.Context, host string) ([]string, error) {
			if ip, ok := dns[host]; ok {
				return []string{ip}, nil
			}
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		env.DoH = func(_ context.Context, host string) (probe.DoHAnswer, error) {
			if ip, ok := dns[host]; ok {
				return probe.DoHAnswer{Server: "lab", IPs: []string{ip}}, nil
			}
			return probe.DoHAnswer{Server: "lab"}, probe.ErrNXDomain
		}
		res, err := probe.Run(context.Background(), env, "example.com", nil)
		if err != nil {
			t.Fatal(err)
		}
		rs, _, _ := rules.Load()
		f := res.Facts()
		return f, rules.Match(rs, f)
	}

	f, v := run(`<img src="https://img.example.com/a.png" srcset="https://img.example.com/b.png 2x">
		<script src="//cdn.example.com/app.js"></script>
		<link rel="stylesheet" href="https://slow.example.com/s.css">`)
	if v.ID != "content_stall" || f["res.checked"] != "3" || f["res.failed"] != "2" || f["res.worst"] != "sni" || f["res.stall_hosts"] != "slow.example.com" {
		t.Errorf("blocked + frozen: verdict %s, facts %v", v.ID, f)
	}

	f, v = run(`<img src="https://img.example.com/a.png"><script src="https://cdn.example.com/app.js"></script>`)
	if v.ID != "content_blocked" || f["res.failed_hosts"] != "img.example.com" {
		t.Errorf("blocked by name: verdict %s, facts %v", v.ID, f)
	}

	f, v = run(`<script src="https://cdn.example.com/app.js"></script>`)
	if v.ID != "all_ok" || f["res.checked"] != "1" || f["res.failed"] != "0" {
		t.Errorf("all fine: verdict %s, facts %v", v.ID, f)
	}

	// The browser's only failure is a name no DNS knows: a dead link in the page, not a problem
	// (youtube.com named a googlevideo node that doesn't exist; it was "content_flaky", live).
	browser = func(context.Context, string) probe.BrowserResult {
		return probe.BrowserResult{Status: "ok", DomMs: 500, LoadMs: 600, Requests: 3, Failed: 1, Hosts: []*probe.BrowserHost{
			{Host: "dead.example.com", Requests: 1, Failed: 1, Errors: map[string]int{"net::ERR_NAME_NOT_RESOLVED": 1}, Types: []string{"fetch"}},
			{Host: "cdn.example.com", Requests: 2, Types: []string{"script"}},
		}}
	}
	f, v = run(`<script src="https://cdn.example.com/app.js"></script>`)
	if v.ID != "all_ok" || f["res.browser_dead"] != "true" || f["br.worst"] != "dns" {
		t.Errorf("dead link: verdict %s, facts %v", v.ID, f)
	}

	// One image of a feed never came while the rest of its server's files did: no longer ignored.
	// A counter that came late doesn't count.
	hung := "net::ERR_TIMED_OUT (no answer while whynet watched)"
	browser = func(context.Context, string) probe.BrowserResult {
		return probe.BrowserResult{Status: "ok", DomMs: 500, LoadMs: 600, Requests: 6, Failed: 1, Hosts: []*probe.BrowserHost{
			{Host: "cdn.example.com", Requests: 5, Failed: 1, Errors: map[string]int{hung: 1}, Types: []string{"image"}},
			{Host: "mc.yandex.ru", Requests: 1, Types: []string{"script"}},
		}, Slow: []*probe.SlowFile{
			{URL: "https://mc.yandex.ru/metrika/tag.js", Host: "mc.yandex.ru", Kind: "script", Ms: 9000, Phase: "wait"},
			{URL: "https://cdn.example.com/feed/1.jpg", Host: "cdn.example.com", Kind: "image", Ms: 8000, Unfinished: true, Phase: "none", Error: hung},
		}}
	}
	f, v = run(`<script src="https://cdn.example.com/app.js"></script>`)
	if v.ID != "content_late" || f["br.late_hosts"] != "cdn.example.com" || f["br.late_file"] != "cdn.example.com/feed/1.jpg" ||
		f["br.late_s"] != "8.0+" || f["br.late_phase"] != "none" || f["br.problem"] != "" || f["res.lossy"] != "" {
		t.Errorf("one stuck image: verdict %s, facts %v", v.ID, f)
	}

	// The same, but the stuck file is one the hidden browser misses on every network (YouTube's
	// search keyboard icon, live 2026-11-17): not late.
	browser = func(context.Context, string) probe.BrowserResult {
		return probe.BrowserResult{Status: "ok", DomMs: 500, LoadMs: 600, Requests: 15, Failed: 1, Hosts: []*probe.BrowserHost{
			{Host: "www.gstatic.com", Requests: 15, Failed: 1, Errors: map[string]int{hung: 1}, Types: []string{"image"}},
		}, Slow: []*probe.SlowFile{
			{URL: "https://www.gstatic.com/inputtools/images/tia.png", Host: "www.gstatic.com", Kind: "image", Ms: 7000, Unfinished: true, Phase: "none", Error: hung},
		}}
	}
	f, v = run(`<script src="https://cdn.example.com/app.js"></script>`)
	if v.ID != "all_ok" || f["br.late"] != "" {
		t.Errorf("a known noisy file: verdict %s, facts %v", v.ID, f)
	}
}
