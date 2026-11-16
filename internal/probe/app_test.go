package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestPrograms(t *testing.T) {
	procs := []proc{
		{PID: 4, Exe: "svchost.exe", System: true},
		{PID: 10, PPID: 1, Exe: "explorer.exe", System: true, Window: true, Title: "Проводник"},
		{PID: 20, PPID: 10, Exe: "Discord.exe", Window: true, Title: "#general | Мой сервер"},
		{PID: 21, PPID: 20, Exe: "Discord.exe"},
		{PID: 22, PPID: 20, Exe: "Discord.exe"},
		{PID: 30, PPID: 10, Exe: "steam.exe", Window: true, Title: "Steam"},
		{PID: 31, PPID: 30, Exe: "steamwebhelper.exe"},          // a helper: counts for Steam
		{PID: 32, PPID: 30, Exe: "cs2.exe", Window: true},       // a game started by Steam: its own program
		{PID: 40, PPID: 10, Exe: "opera.exe", Window: true},     // a browser: last, marked
		{PID: 50, PPID: 4, Exe: "updater.exe"},                  // no window, no connections: not listed
		{PID: 60, PPID: 999, Exe: "Telegram.exe", Window: true}, // parent gone
	}
	conns := map[uint32]int{4: 9, 21: 3, 22: 2, 31: 4, 40: 20, 60: 1}
	got := programs(procs, conns, []string{"opera.exe"})
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	want := []string{"Discord", "steam", "Telegram", "cs2", "opera"}
	if len(names) != len(want) {
		t.Fatalf("programs %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("programs %v, want %v", names, want)
		}
	}
	if d := got[0]; d.Conns != 5 || len(d.PIDs) != 3 || d.Title != "#general | Мой сервер" {
		t.Errorf("Discord %+v", d)
	}
	if s := got[1]; s.Conns != 4 || len(s.PIDs) != 2 {
		t.Errorf("Steam should hold its helper: %+v", s)
	}
	if !got[4].Browser {
		t.Errorf("opera not marked as a browser")
	}
	// Recording finds a program's processes even with no window and no connections right now.
	found := false
	for _, g := range groupPrograms(procs, nil, nil) {
		found = found || g.Exe == "updater.exe"
	}
	if !found {
		t.Errorf("groupPrograms dropped a windowless program without connections")
	}
}

func TestNameServers(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})) // certificate: example.com
	defer tlsSrv.Close()
	plain, err := net.Listen("tcp", "127.0.0.1:0") // not TLS: answers with its own bytes (Telegram's MTProto)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	go func() {
		for {
			c, err := plain.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("hello, this is not TLS"))
			c.Close()
		}
	}()
	routes := map[string]string{
		"203.0.113.5:8443": tlsSrv.Listener.Addr().String(),
		"198.18.0.9:443":   tlsSrv.Listener.Addr().String(),
		"203.0.113.6:9000": plain.Addr().String(),
	}
	env := Env{
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, routes[addr])
		},
		DoH: func(_ context.Context, host string) (DoHAnswer, error) {
			return DoHAnswer{IPs: map[string][]string{"example.com": {"203.0.113.5"}}[host]}, nil
		},
		PTR: func(_ context.Context, ip string) ([]string, error) {
			return []string{"host-" + ip + ".isp.example."}, nil
		},
		ASN:     func(_ context.Context, ip string) (string, string) { return "AS64500", "Test net " + ip },
		Timeout: 2 * time.Second,
	}
	servers := []*AppServer{
		{IP: "203.0.113.5", Port: 8443},
		{IP: "198.18.0.9", Port: 443, FakeIP: true},
		{IP: "203.0.113.6", Port: 9000},
		{IP: "203.0.113.7", Port: 443}, // cached name, nothing listens
	}
	p := &prober{ctx: context.Background(), env: env}
	p.nameServers(servers, map[string]string{"203.0.113.7": "cdn.example.net"})
	want := []struct{ name, from, tls, real, owner string }{
		{"example.com", "cert", "yes", "", "Test net 203.0.113.5"},
		{"example.com", "cert", "yes", "203.0.113.5", "Test net 203.0.113.5"}, // the owner of the real address
		{"host-203.0.113.6.isp.example", "ptr", "no", "", "Test net 203.0.113.6"},
		{"cdn.example.net", "cache", "no", "", "Test net 203.0.113.7"},
	}
	for i, w := range want {
		s := servers[i]
		if s.Name != w.name || s.NameFrom != w.from || s.TLS != w.tls || s.RealIP != w.real || s.Owner != w.owner {
			t.Errorf("server %d: %+v, want %+v", i, *s, w)
		}
	}
	if got := certName([]string{"*.discord.gg", "discord.gg"}); got != "discord.gg" {
		t.Errorf("certName exact first: %s", got)
	}
	if got := certName([]string{"*.spotify.com"}); got != "spotify.com" {
		t.Errorf("certName wildcard: %s", got)
	}
}

func TestRecorder(t *testing.T) {
	ap := netip.MustParseAddrPort
	me := map[uint32]bool{7: true}
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	row := func(local, remote string, state int) tcpRow {
		return tcpRow{PID: 7, State: state, Local: ap(local), Remote: ap(remote)}
	}
	r := newRecorder()
	// 0 ms: a connection that gets reset, one closed by a handshake, one waiting for an answer,
	// one to the home network, one of another program.
	r.add(at(0), []tcpRow{
		row("192.168.1.5:5001", "162.159.135.232:443", tcpEstab),
		row("192.168.1.5:5002", "162.159.135.232:443", tcpEstab),
		row("192.168.1.5:5003", "10.255.255.1:443", 3), // SYN_SENT, but a private address: ignored
		row("192.168.1.5:5004", "203.0.113.9:2053", 3),
		row("192.168.1.5:5005", "192.168.1.1:80", tcpEstab),
		{PID: 8, State: tcpEstab, Local: ap("192.168.1.5:5006"), Remote: ap("8.8.8.8:443")},
		row("192.168.1.5:5007", "198.18.0.95:443", tcpEstab), // through a fake-ip router, open to the end
	}, me)
	// 250 ms: 5001 is gone without a trace (reset); 5002 ends by a handshake: TIME_WAIT, no owner.
	r.add(at(250), []tcpRow{
		{PID: 0, State: tcpTimeWait, Local: ap("192.168.1.5:5002"), Remote: ap("162.159.135.232:443")},
		row("192.168.1.5:5004", "203.0.113.9:2053", 3),
		row("192.168.1.5:5007", "198.18.0.95:443", tcpEstab),
		row("192.168.1.5:5008", "162.159.135.232:443", tcpEstab), // a second try to the same server
	}, me)
	// 4 s: 5008 lived long and closes without a trace: not a cut. 5004 still waiting.
	r.add(at(4000), []tcpRow{
		row("192.168.1.5:5004", "203.0.113.9:2053", 3),
		row("192.168.1.5:5007", "198.18.0.95:443", tcpEstab),
		row("192.168.1.5:5008", "162.159.135.232:443", tcpEstab),
	}, me)
	r.add(at(4250), []tcpRow{
		row("192.168.1.5:5004", "203.0.113.9:2053", 3),
		row("192.168.1.5:5007", "198.18.0.95:443", tcpEstab),
	}, me)
	got := r.result(at(5000))
	if len(got) != 3 {
		t.Fatalf("%d servers, want 3: %+v", len(got), got)
	}
	cf, stuck, fake := got[0], got[1], got[2]
	if cf.IP != "162.159.135.232" || cf.Conns != 3 || cf.Cut != 1 || cf.Open != 2 || cf.Stuck != 0 {
		t.Errorf("cloudflare %+v: want 3 conns, 1 cut, 2 open", cf)
	}
	if stuck.Port != 2053 || stuck.Stuck != 1 || stuck.Conns != 1 {
		t.Errorf("silent server %+v: want 1 stuck", stuck)
	}
	if !fake.FakeIP || fake.Open != 1 || fake.LongestS != 5 {
		t.Errorf("fake-ip server %+v", fake)
	}
}
