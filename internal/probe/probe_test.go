package probe

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"
	"time"
)

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
