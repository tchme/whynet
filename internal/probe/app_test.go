package probe

import (
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
