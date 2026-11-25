package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestHTTPKeepsPageAddressOnTLS(t *testing.T) {
	type visit struct{ host, target, referer, authorization string }
	seenHTTP := make(chan visit, 8)
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHTTP <- visit{r.Host, r.URL.RequestURI(), r.Referer(), r.Header.Get("Authorization")}
		if r.Host == "127.0.0.1" && r.URL.Path == "/" {
			http.Redirect(w, r, "http://filter.example/stub", http.StatusFound)
			return
		}
		w.Write([]byte("<title>ISP filter</title>"))
	}))
	defer plain.Close()

	seenTLS := make(chan string, 12)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenTLS <- r.URL.RequestURI()
		switch r.URL.Path {
		case "/original-private-path":
			w.Header().Set("Referrer-Policy", "unsafe-url")
			http.Redirect(w, r, "/second?key=redirect-marker", http.StatusFound)
		case "/second":
			w.Header().Set("Referrer-Policy", "unsafe-url")
			http.Redirect(w, r, "http://user:password@filter.example/never?key=downgrade-marker#fragment-marker", http.StatusTemporaryRedirect)
		case "/ordinary":
			http.Redirect(w, r, "/ready?key=ordinary-marker", http.StatusFound)
		case "/ready":
			w.Write([]byte("<title>Ready</title>"))
		}
	}))
	defer secure.Close()
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())
	env := Env{Timeout: time.Second, RootCAs: roots, Markers: Markers{Stubs: []string{"filter.example"}},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			listener := plain.Listener
			if port == "443" {
				listener = secure.Listener
			}
			return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
		},
	}
	check := func(path string, tlsOK bool) (Step, *prober) {
		p := &prober{ctx: context.Background(), host: "127.0.0.1", path: path, ip: "127.0.0.1", env: env, tlsOK: tlsOK, res: &Result{}}
		s := Step{Facts: map[string]string{}, Evidence: map[string]any{}}
		p.http(&s)
		return s, p
	}
	assertTLS := func(t *testing.T, want ...string) {
		t.Helper()
		for _, target := range want {
			select {
			case got := <-seenTLS:
				if got != target {
					t.Errorf("HTTPS target = %q, want %q", got, target)
				}
			default:
				t.Fatalf("did not reach HTTPS target %q", target)
			}
		}
	}
	t.Run("fallback", func(t *testing.T) {
		s, _ := check("/original-private-path?key=input-marker", false)
		if s.Facts["http.stub"] != "true" || s.Facts["http.scheme"] != "http" {
			t.Fatalf("fallback lost the ISP stub: %v", s.Facts)
		}
		for _, want := range []visit{{host: "127.0.0.1", target: "/"}, {host: "filter.example", target: "/stub"}} {
			select {
			case got := <-seenHTTP:
				if got != want {
					t.Errorf("HTTP visit = %+v, want %+v", got, want)
				}
			default:
				t.Fatalf("expected HTTP visit %+v", want)
			}
		}
	})
	t.Run("downgrade", func(t *testing.T) {
		s, p := check("/original-private-path?key=input-marker", true)
		assertTLS(t, "/original-private-path?key=input-marker", "/second?key=redirect-marker")
		select {
		case got := <-seenHTTP:
			t.Errorf("unsafe HTTP visit: %+v", got)
		default:
		}
		if s.Facts["http.status"] != "307" || s.Facts["http.redirect_blocked"] != "true" || p.pageOK {
			t.Errorf("blocked redirect looked like a loaded page: %v, pageOK=%v", s.Facts, p.pageOK)
		}
		redirects, _ := s.Evidence["redirects"].([]map[string]any)
		if len(redirects) != 2 || redirects[1]["to"] != "http://user:password@filter.example/never?key=downgrade-marker#fragment-marker" {
			t.Errorf("lost raw redirect evidence: %v", redirects)
		}
		var browserTarget string
		p.env.Browser = func(_ context.Context, target string) BrowserResult {
			browserTarget = target
			return BrowserResult{Status: "nav_failed"}
		}
		p.browser(&Step{Facts: map[string]string{}, Evidence: map[string]any{}})
		if browserTarget != "https://127.0.0.1/original-private-path?key=input-marker" {
			t.Errorf("browser target = %q, want the original HTTPS address", browserTarget)
		}
	})
	t.Run("secure redirect", func(t *testing.T) {
		s, p := check("/ordinary", true)
		assertTLS(t, "/ordinary", "/ready?key=ordinary-marker")
		if s.Facts["http.status"] != "200" || !p.pageOK || p.finalURL.RequestURI() != "/ready?key=ordinary-marker" {
			t.Errorf("HTTPS redirect did not reach the page: %v, final URL=%v", s.Facts, p.finalURL)
		}
	})
}

func TestTarget(t *testing.T) {
	cases := []struct{ in, host, path string }{
		{"youtube.com", "youtube.com", "/"},
		{"  HTTPS://YouTube.com./watch?v=1 ", "youtube.com", "/watch?v=1"},
		{"http://example.com:8080/a b", "example.com", "/a%20b"},
		{"президент.рф", "xn--d1abbgf6aiiy.xn--p1ai", "/"},
		{"1.2.3.4", "1.2.3.4", "/"},
	}
	for _, c := range cases {
		host, path, err := Target(c.in)
		if err != nil || host != c.host || path != c.path {
			t.Errorf("Target(%q) = %q, %q, %v; want %q, %q", c.in, host, path, err, c.host, c.path)
		}
	}
	for _, bad := range []string{"", "   ", "hello", "http://", "a b.com"} {
		if _, _, err := Target(bad); !errors.Is(err, ErrBadInput) {
			t.Errorf("Target(%q) err = %v, want ErrBadInput", bad, err)
		}
	}
}

func TestNetClass(t *testing.T) {
	op := func(code syscall.Errno) error {
		return &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("wsarecv", code)}
	}
	cases := map[error]string{
		nil:             "ok",
		op(10054):       "reset",
		op(10061):       "refused",
		op(10060):       "timeout",
		op(10051):       "unreachable",
		errors.New("x"): "error",
	}
	for err, want := range cases {
		if got := netClass(err); got != want {
			t.Errorf("netClass(%v) = %s, want %s", err, got, want)
		}
	}
}

func TestHostsLines(t *testing.T) {
	hosts := "# 1.2.3.4 youtube.com\n127.0.0.1 localhost\n0.0.0.0 ads.example YouTube.com # blocked\r\n"
	got := hostsLines(hosts, "youtube.com")
	if want := []string{"0.0.0.0 ads.example YouTube.com # blocked"}; !reflect.DeepEqual(got, want) {
		t.Errorf("hostsLines = %q, want %q", got, want)
	}
}

func TestDPIRoot(t *testing.T) {
	cases := map[string]string{
		`\??\C:\Users\D\Desktop\zapret-discord-youtube-1.9.9d\bin\WinDivert64.sys`:     `C:\Users\D\Desktop\zapret-discord-youtube-1.9.9d`,
		`"C:\zapret\bin\winws.exe" --wf-tcp=80,443 --hostlist="C:\zapret\lists\x.txt"`: `C:\zapret`,
		`C:\Program Files\GoodbyeDPI\x86_64\goodbyedpi.exe -9 --blacklist C:\l.txt`:    `C:\Program Files\GoodbyeDPI\x86_64`,
		`C:\z\bin\winws.exe`: `C:\z`,
		``:                   ``,
	}
	for in, want := range cases {
		if got := dpiRoot(in); got != want {
			t.Errorf("dpiRoot(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHostListed(t *testing.T) {
	files := map[string]string{
		"list-general.txt":      "discord.com\n^dns.google\r\n# youtube.com\n",
		"list-general-user.txt": "rutracker.org # mine\n",
		"list-exclude.txt":      "pay.discord.com\n",
		"ipset-all.txt":         "example.org\n",
	}
	cases := []struct{ host, want string }{
		{"discord.com", "true"},
		{"cdn.discord.com", "true"}, // subdomain of a plain entry
		{"x.dns.google", "false"},   // ^ means the domain alone
		{"dns.google", "true"},
		{"youtube.com", "false"}, // commented out
		{"rutracker.org", "true"},
		{"pay.discord.com", "excluded"}, // the exclude list wins
		{"example.org", "false"},        // ipset files are addresses, not names
		{"notdiscord.com", "false"},     // a suffix but not a subdomain
	}
	for _, c := range cases {
		if got, _ := hostListed(files, c.host); got != c.want {
			t.Errorf("hostListed(%s) = %s, want %s", c.host, got, c.want)
		}
	}
}

func TestIPListed(t *testing.T) {
	files := map[string]string{
		"ipset-all.txt":          "172.67.128.0/17\r\n104.16.0.0/12 # cloudflare\n1.2.3.4\nnot an address\n",
		"ipset-exclude.txt":      "104.21.0.0/16\n",
		"list-general.txt":       "10.0.0.0/8\n", // a name list: addresses there mean nothing
		"ipset-exclude-user.txt": "",
	}
	cases := []struct{ ip, want, entry string }{
		{"172.67.182.196", "true", "172.67.128.0/17"}, // rutracker.org behind Cloudflare
		{"104.18.33.206", "true", "104.16.0.0/12"},
		{"1.2.3.4", "true", "1.2.3.4"},
		{"104.21.32.39", "excluded", "104.21.0.0/16"}, // the exclude list wins
		{"130.211.32.14", "false", ""},
		{"10.1.1.1", "false", ""},
		{"", "unknown", ""},
	}
	for _, c := range cases {
		if got, entry := ipListed(files, c.ip); got != c.want || entry != c.entry {
			t.Errorf("ipListed(%s) = %s %q, want %s %q", c.ip, got, entry, c.want, c.entry)
		}
	}
	// Flowseal's "any" mode: an empty ipset-all.txt applies the bypass to every address
	if got, entry := ipListed(map[string]string{"ipset-all.txt": "", "ipset-exclude.txt": "192.168.0.0/16\n"}, "130.211.32.14"); got != "true" || entry != "any" {
		t.Errorf("any mode: %s %q", got, entry)
	}
	if got, _ := ipListed(map[string]string{"ipset-all.txt": "\n", "ipset-exclude.txt": "192.168.0.0/16\n"}, "192.168.1.1"); got != "excluded" {
		t.Errorf("any mode, excluded address: %s", got)
	}
}

// The clock fallback must accept a good certificate whatever this computer's clock says.
func TestVerifyAnyTime(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"},
		NotBefore: time.Now().AddDate(-3, 0, 0), NotAfter: time.Now().AddDate(-2, 0, 0), // long expired
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	cert, _ := x509.ParseCertificate(der)
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	if err := verifyAnyTime([]*x509.Certificate{cert}, "example.com", roots); err != nil {
		t.Errorf("a good chain outside its validity: %v", err)
	}
	if verifyAnyTime([]*x509.Certificate{cert}, "other.com", roots) == nil {
		t.Error("a wrong name passed")
	}
	if verifyAnyTime([]*x509.Certificate{cert}, "example.com", x509.NewCertPool()) == nil {
		t.Error("an untrusted chain passed")
	}
}

func TestFlowsealSettings(t *testing.T) {
	for content, want := range map[string]string{"": "any", "\r\n": "any", "203.0.113.113/32\r\n": "none", "104.16.0.0/12\n": "loaded"} {
		if got := ipsetFilter(map[string]string{"IPSET-ALL.txt": content}); got != want {
			t.Errorf("ipsetFilter(%q) = %s, want %s", content, got, want)
		}
	}
	if got := ipsetFilter(map[string]string{"list-general.txt": ""}); got != "" {
		t.Errorf("no ipset-all.txt: %q", got)
	}

	cases := []struct{ content, mode, tcp, udp string }{
		{"", "disabled", "", ""}, // no file
		{"mode=all\r\ntcp=1024-65535\r\nudp=1024-65535\r\n", "all", "1024-65535", "1024-65535"}, // the author's file
		{"mode=UDP\ntcp=443\nudp=27000-27100, 3478\n", "udp", "", "27000-27100,3478"},
		{"mode=tcp\ntcp=abc\n", "tcp", "1024-65535", ""}, // a bad range keeps the default
		{"udp\n", "udp", "", "1024-65535"},               // the old one-word file
		{"all\n", "all", "1024-65535", "1024-65535"},
		{"mode=disabled\ntcp=1-2\n", "disabled", "", ""},
		{"mode=games\n", "disabled", "", ""},
	}
	for _, c := range cases {
		if mode, tcp, udp := gameFilter(c.content); mode != c.mode || tcp != c.tcp || udp != c.udp {
			t.Errorf("gameFilter(%q) = %s %q %q, want %s %q %q", c.content, mode, tcp, udp, c.mode, c.tcp, c.udp)
		}
	}
}

func TestOriginASN(t *testing.T) {
	for in, want := range map[string]string{
		"12389 | 95.24.0.0/14 | RU | ripencc | 2001-06-08": "12389",
		"13238 208398 | 77.88.8.0/24 | RU | ripencc |":     "13238", // several origins: it broke the name lookup
		"": "",
	} {
		if got := originASN(in); got != want {
			t.Errorf("originASN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTraceAndMask(t *testing.T) {
	kv := parseTrace("fl=1\nip=95.24.13.7\nloc=RU\nwarp=off\n")
	if kv["ip"] != "95.24.13.7" || kv["loc"] != "RU" {
		t.Errorf("parseTrace = %v", kv)
	}
	if got := maskIP("95.24.13.7"); got != "95.24.x.x" {
		t.Errorf("maskIP = %s", got)
	}
	if !fakeIP([]string{"198.18.0.194", "198.19.255.1"}) || fakeIP([]string{"198.18.0.1", "8.8.8.8"}) || fakeIP(nil) {
		t.Error("fakeIP misjudges the 198.18.0.0/15 range")
	}
}

func TestSameSite(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"www.instagram.com", "instagram.com", true},
		{"m.vk.com", "vk.com", true},
		{"lawfilter.ertelecom.ru", "linkedin.com", false},
		{"blackhole.beeline.ru", "rutracker.org", false},
		{"a.co.uk", "b.co.uk", false},
		{"194.67.1.14", "rutracker.org", false},
	} {
		if got := SameSite(c.a, c.b); got != c.want {
			t.Errorf("SameSite(%s, %s) = %v", c.a, c.b, got)
		}
	}
}
