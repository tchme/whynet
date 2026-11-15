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
	dpi := dpiServer(t, cert, "img.example.com")

	run := func(html string) (map[string]string, rules.Verdict) {
		main := serveWith(t, cert, page(html))
		l := &lab{pool: pool, routes: map[string]string{
			"10.0.0.1:443": main, "10.0.0.2:443": dpi, "10.0.0.7:443": ok, "10.0.0.8:443": frozen,
		}}
		env := l.env(nil, nil, []string{"10.0.0.1"}, nil)
		dns := map[string]string{"example.com": "10.0.0.1", "img.example.com": "10.0.0.2", "cdn.example.com": "10.0.0.7", "slow.example.com": "10.0.0.8"}
		env.Lookup = func(_ context.Context, host string) ([]string, error) {
			if ip, ok := dns[host]; ok {
				return []string{ip}, nil
			}
			return nil, fmt.Errorf("no such host %s", host)
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
}
