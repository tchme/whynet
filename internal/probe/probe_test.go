package probe

import (
	"errors"
	"net"
	"os"
	"reflect"
	"syscall"
	"testing"
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
