package health

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"whynet/internal/probe"
)

func TestHostsFixRequiresConfirmedPinFailures(t *testing.T) {
	const neighbor = "192.0.2.1 neighbor.example"
	const target = "192.0.2.2 target.example"
	const extra = "192.0.2.3 queued.example"
	const mixed = "192.0.2.2 target.example unchecked.example"
	secure := httptest.NewTLSServer(nil)
	defer secure.Close()
	roots := x509.NewCertPool()
	roots.AddCert(secure.Certificate())
	openPipe := func(serve func(net.Conn)) net.Conn {
		client, server := net.Pipe()
		go func() { defer server.Close(); serve(server) }()
		return client
	}
	traits := new(net.Dialer)
	for _, tc := range []struct {
		name         string
		stage        int // 1 = connect, 2 = requested name TLS, 3 = control TLS
		cause        string
		content      string
		cap, unknown int
		fix          []string
	}{
		{"canceled before start", 0, "cancel", neighbor + "\n" + target, 10, 2, nil},
		{"canceled in TCP with queue", 1, "cancel", neighbor + "\n" + target + "\n" + extra, 10, 2, []string{neighbor}},
		{"canceled in requested TLS", 2, "cancel", neighbor + "\n" + target, 10, 1, []string{neighbor}},
		{"canceled in control TLS", 3, "cancel", neighbor + "\n" + target, 10, 1, []string{neighbor}},
		{"own timeout in TCP", 1, "deadline", neighbor + "\n" + target, 10, 1, []string{neighbor}},
		{"own timeout in requested TLS", 2, "deadline", neighbor + "\n" + target, 10, 1, []string{neighbor}},
		{"own timeout in control TLS", 3, "deadline", neighbor + "\n" + target, 10, 1, []string{neighbor}},
		{"socket timeout", 2, "timeout", neighbor + "\n" + target, 10, 1, []string{neighbor}},
		{"worker panic", 1, "panic", neighbor + "\n" + target + "\n" + mixed, 10, 2, []string{neighbor}},
		{"socket refusal", 1, "refused", neighbor + "\n" + target, 10, 0, []string{neighbor, target}},
		{"mismatched certificate", 2, "cert", neighbor + "\n" + target, 10, 0, []string{neighbor, target}},
		{"certificate and capped name on the same line", 2, "cert", neighbor + "\n" + mixed, 2, 1, []string{neighbor}},
		{"both TLS handshakes explicitly failed", 3, "protocol", neighbor + "\n" + target, 10, 0, []string{neighbor, target}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.stage == 0 {
				cancel()
			}
			dials := 0
			pc := pinCheck{Roots: roots, Wait: 40 * time.Millisecond, Cap: tc.cap, Par: 1}
			if tc.cause != "deadline" {
				pc.Wait = time.Second
			}
			pc.Dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr == "192.0.2.1:443" || addr == "192.0.2.3:443" {
					return nil, &net.OpError{Err: syscall.Errno(10061)}
				}
				dials++
				if dials == tc.stage {
					switch tc.cause {
					case "cancel":
						cancel()
						return nil, syscall.Errno(10061)
					case "deadline":
						if tc.stage == 1 {
							<-ctx.Done()
							return nil, syscall.Errno(10061)
						}
						return openPipe(func(net.Conn) { <-ctx.Done() }), nil
					case "timeout":
						return nil, os.ErrDeadlineExceeded
					case "panic":
						panic("probe fixture")
					case "refused":
						return nil, syscall.Errno(10061)
					case "cert":
						return traits.DialContext(ctx, network, secure.Listener.Addr().String())
					}
				}
				if dials == 1 {
					return openPipe(func(net.Conn) {}), nil
				}
				return openPipe(func(server net.Conn) {
					buf := make([]byte, 64<<10)
					server.Read(buf)
					io.WriteString(server, "HTTP/1.0 200 OK\r\n\r\n")
				}), nil
			}
			path := filepath.Join(t.TempDir(), "hosts")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			c := checker{ctx: ctx}
			s := probe.Step{ID: "health_hosts", Status: "ok", Facts: map[string]string{}, Evidence: map[string]any{}}
			c.readHosts(&s, path, pc)
			if tc.cause == "deadline" && ctx.Err() != nil {
				t.Fatal("test also exhausted the parent context")
			}
			if got := s.Facts["health.hosts_unchecked"]; got != strconv.Itoa(tc.unknown) {
				t.Errorf("unchecked = %s, want %d (evidence %v)", got, tc.unknown, s.Evidence["pin_results"])
			}
			if got := s.Evidence["pin_results"].(map[string]int)["unchecked"]; got != tc.unknown || (s.Status == "warn") != (tc.unknown > 0) {
				t.Errorf("unchecked evidence = %v, status %s, want %d", s.Evidence["pin_results"], s.Status, tc.unknown)
			}
			var fixed []string
			for _, a := range MakePlan(&Result{Steps: []probe.Step{s}}, []string{"hosts_stale"}).Actions {
				if a.ID == "hosts_comment" {
					fixed = a.Lines
				}
			}
			if !slices.Equal(fixed, tc.fix) || s.Facts["health.hosts_stale_found"] != strconv.FormatBool(len(tc.fix) > 0) {
				t.Errorf("hosts_comment = %q, want %q, evidence %v", fixed, tc.fix, s.Evidence)
			}
		})
	}
}

func TestUnreadableHostsIsAnError(t *testing.T) {
	c := checker{ctx: context.Background()}
	s := probe.Step{Status: "ok", Facts: map[string]string{}, Evidence: map[string]any{}}
	c.readHosts(&s, filepath.Join(t.TempDir(), "missing"), pinCheck{})
	if s.Status != "error" || s.Facts["health.hosts"] != "unreadable" || s.Evidence["error"] == nil {
		t.Errorf("unreadable hosts: %+v", s)
	}
}

// A zapret-pack style hosts file, as on the author's PC (2026-11-17), plus blocks and a LAN name.
const hostsSample = "# Copyright (c) Microsoft\r\n" +
	"127.0.0.1 localhost\r\n" +
	"149.154.167.220 telegram.me\r\n" +
	"149.154.167.220 telesco.pe   # pinned\r\n" +
	"104.25.158.178 a.example b.example\r\n" +
	"0.0.0.0 www.msftconnecttest.com\r\n" +
	"192.168.1.10 nas.lan\r\n" +
	"not-an-ip junk\r\n" +
	"   \r\n"

func TestParseHosts(t *testing.T) {
	lines := parseHosts(hostsSample)
	kinds := map[string]int{}
	for _, l := range lines {
		kinds[l.Kind]++
	}
	if len(lines) != 6 || kinds["pin"] != 3 || kinds["block"] != 2 || kinds["local"] != 1 {
		t.Fatalf("lines %d, kinds %v", len(lines), kinds)
	}
	if lines[2].Text != "149.154.167.220 telesco.pe   # pinned" {
		t.Errorf("the line must stay exactly as in the file: %q", lines[2].Text)
	}
	if !blocksNCSI(lines) {
		t.Error("a block of www.msftconnecttest.com breaks Windows' connectivity check")
	}
}

func TestStaleLines(t *testing.T) {
	lines := parseHosts(hostsSample)
	res := []pinResult{
		{"149.154.167.220", "telegram.me", "dead"},
		{"149.154.167.220", "telesco.pe", "cut"}, // cut by name: DPI, the pin may be wanted
		{"104.25.158.178", "a.example", "other_cert"},
		{"104.25.158.178", "b.example", "no_tls"},
	}
	got := staleLines(lines, res)
	if len(got) != 2 || got[0].Names[0] != "telegram.me" || got[1].Addr != "104.25.158.178" {
		t.Fatalf("stale: %+v", got)
	}
	// A name left unchecked (over the cap) keeps its line.
	if got := staleLines(lines, res[:3]); len(got) != 1 {
		t.Errorf("a line with an unchecked name must stay: %+v", got)
	}
}

func TestHostsCommentRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	off, n := commentHosts(hostsSample, []string{"149.154.167.220 telegram.me", "absent line"}, now)
	if n != 1 || !strings.Contains(off, "# [whynet 2026-11-17] 149.154.167.220 telegram.me\r\n") {
		t.Fatalf("comment: %d\n%s", n, off)
	}
	if parseHosts(off)[1].Names[0] == "telegram.me" {
		t.Error("the commented line must not be active")
	}
	back, n := uncommentHosts(off, []string{"149.154.167.220 telegram.me"})
	if n != 1 || back != hostsSample {
		t.Errorf("uncomment must give the file back byte for byte: %d\n%q", n, back)
	}
}

func TestProxyAddrs(t *testing.T) {
	for in, want := range map[string][]string{
		"127.0.0.1:10809":                          {"127.0.0.1:10809"},
		"http=127.0.0.1:8080;https=127.0.0.1:8080": {"127.0.0.1:8080"},
		"socks=10.0.0.1:1080":                      {"10.0.0.1:1080"},
		"http://proxy.lan":                         {"proxy.lan:80"},
		"":                                         nil,
	} {
		if got := proxyAddrs(in); !slices.Equal(got, want) {
			t.Errorf("proxyAddrs(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseTCPGlobal(t *testing.T) {
	// Russian labels, English values: the author's Windows 11 (2026-11-17).
	ru := "Запрос активного состояния...\r\n\r\nГлобальные параметры TCP\r\n------\r\n" +
		"Состояние масштабирования на стороне приема          : enabled \r\n" +
		"Уровень автонастройки окна получения    : disabled \r\n" +
		"Мощность ECN                      : disabled \r\n"
	if rss, at := parseTCPGlobal(ru); rss != "enabled" || at != "disabled" {
		t.Errorf("ru: %s %s", rss, at)
	}
	if rss, at := parseTCPGlobal("garbage"); rss != "unknown" || at != "unknown" {
		t.Errorf("garbage: %s %s", rss, at)
	}
}

func TestForeignWinsock(t *testing.T) {
	got := foreignWinsock([]string{`%SystemRoot%\system32\mswsock.dll`, `%SystemRoot%\system32\rsvpsp.dll`,
		`C:\Program Files\Proxifier\PrxerNsp.dll`, `C:\Program Files\Proxifier\PrxerNsp.dll`})
	if !slices.Equal(got, []string{"prxernsp.dll"}) {
		t.Errorf("foreign: %v", got)
	}
}

func TestDNSVerdict(t *testing.T) {
	ok, dead, slow := dnsServer{Ms: 4}, dnsServer{Error: "timeout"}, dnsServer{Ms: 900}
	for want, servers := range map[string][]dnsServer{
		"none": nil, "ok": {ok, slow}, "dead_all": {dead, dead}, "dead_some": {ok, dead}, "slow": {slow},
	} {
		if got := dnsVerdict(servers); got != want {
			t.Errorf("%v: %s, want %s", servers, got, want)
		}
	}
}

func TestFirstOf(t *testing.T) {
	if f, m := firstOf([]string{"a", "b", "c", "d"}, 2); f != "a, b" || m != " (+2)" {
		t.Errorf("%q %q", f, m)
	}
	if f, m := firstOf([]string{"a"}, 2); f != "a" || m != "" {
		t.Errorf("%q %q", f, m)
	}
}

// fakeResult is a check that found a dead proxy, stale hosts lines and disabled autotuning.
func fakeResult() *Result {
	return &Result{Steps: []probe.Step{
		{ID: "health_proxy", Facts: map[string]string{"health.proxy": "dead"}},
		{ID: "health_hosts", Facts: map[string]string{"health.hosts_stale": "1"}, Evidence: map[string]any{"stale_lines": []string{"149.154.167.220 telegram.me"}}},
		{ID: "health_tcp", Facts: map[string]string{"health.autotuning": "disabled"}},
	}}
}

func TestMakePlan(t *testing.T) {
	p := MakePlan(fakeResult(), []string{"autotuning", "hosts_stale", "proxy_off"})
	var ids []string
	for _, a := range p.Actions {
		ids = append(ids, a.ID)
	}
	// Fixed order whatever the findings' order; ARP only because the batch needs admin anyway.
	if !slices.Equal(ids, []string{"proxy_enable", "hosts_comment", "tcp_global", "flush_dns", "flush_arp"}) {
		t.Fatalf("order: %v", ids)
	}
	if p.Undo[2] == nil || p.Undo[2].Value != "autotuninglevel=disabled" || p.Undo[3] != nil {
		t.Errorf("undo: %+v %+v", p.Undo[2], p.Undo[3])
	}
	// Proxy only: no admin, so no ARP flush and no prompt.
	if p := MakePlan(fakeResult(), []string{"proxy_off"}); len(p.Actions) != 2 || slices.ContainsFunc(p.Actions, Action.Admin) {
		t.Errorf("proxy only: %+v", p.Actions)
	}
	// Undo takes back only what went through, newest first.
	outs := []Outcome{{Action: p.Actions[0], OK: true}, {Action: p.Actions[1], OK: false}, {Action: p.Actions[2], OK: true}}
	undo := UndoPlan(p, outs)
	if len(undo) != 2 || undo[0].ID != "tcp_global" || undo[1].ID != "proxy_enable" || undo[1].Value != "1" {
		t.Errorf("undo plan: %+v", undo)
	}
}

func TestValidate(t *testing.T) {
	own := func(name string, index uint32) bool { return name == "Ethernet" || index == 7 }
	for _, a := range []Action{
		{ID: "proxy_enable", Value: "0"}, {ID: "tcp_global", Value: "autotuninglevel=normal"},
		{ID: "dns_source", Index: 7, Value: "dhcp"}, {ID: "dns_source", Index: 7, Value: "static", Values: []string{"1.1.1.1"}},
		{ID: "service_start", Value: "Dnscache"}, {ID: "hosts_comment", Lines: []string{"1.2.3.4 a.example"}},
		{ID: "dhcp_renew", Adapter: "Ethernet"}, {ID: "winhttp_set"}, {ID: "winhttp_set", Value: "10.0.0.1:3128", Values: []string{"<local>"}},
	} {
		if err := Validate(a, own); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	for _, a := range []Action{
		{ID: "format_disk"}, {ID: "tcp_global", Value: "autotuninglevel=normal & del x"},
		{ID: "dns_source", Index: 99, Value: "dhcp"}, // not this PC's adapter (or a VPN one)
		{ID: "dns_source", Index: 7, Value: "static", Values: []string{"evil"}},
		{ID: "service_stop", Value: "Dnscache"}, {ID: "service_start", Value: "WinDefend"},
		{ID: "hosts_comment", Lines: []string{"a\nb"}}, {ID: "dhcp_renew", Adapter: "WireGuard"},
		{ID: "winhttp_set", Value: "x\" & calc"}, {ID: "pac_set", Value: "javascript:1"},
	} {
		if err := Validate(a, own); err == nil {
			t.Errorf("%+v must be refused", a)
		}
	}
}
