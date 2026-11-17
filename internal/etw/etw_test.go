package etw

import (
	"encoding/json"
	"net/netip"
	"testing"
	"time"
)

// The helper's command line: anything that isn't ours starts the app as usual; ours is checked
// before any connection is made.
func TestHelperMain(t *testing.T) {
	if ran, _ := HelperMain([]string{"-live"}); ran {
		t.Error("ran on someone else's flags")
	}
	for _, args := range [][]string{
		{HelperFlag, "127.0.0.1:1", "k", "frames", "0"},
		{HelperFlag, "127.0.0.1:1", "k", "frames", "x"},
		{HelperFlag, "127.0.0.1:1", "k", "tcp", "60"},
		{HelperFlag, "10.0.0.1:1", "k", "udp", "60"},
	} {
		if ran, err := HelperMain(args); !ran || err == nil {
			t.Errorf("%v: ran %v, err %v", args, ran, err)
		}
	}
	if j := jobs(Handlers{Packet: func(Packet) {}, Frame: func(Frame) {}}); j != "udp,frames" {
		t.Errorf("jobs: %q", j)
	}
}

// A stream line survives the trip from the helper to the window.
func TestLineRoundTrip(t *testing.T) {
	at := time.Date(2026, 11, 17, 18, 24, 10, 798_000_000, time.Local)
	p := Packet{Time: at, PID: 7, Size: 86, Recv: true, Remote: netip.MustParseAddrPort("77.88.8.8:53"), Local: netip.MustParseAddrPort("192.168.1.248:58140")}
	f := Frame{Time: at, PID: 36936}
	for _, in := range []line{{U: &p}, {F: &f}} {
		b, _ := json.Marshal(in)
		var out line
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		same := func(a, b Packet) bool { a.Time, b.Time = time.Time{}, time.Time{}; return a == b }
		if (in.U != nil && (out.U == nil || !out.U.Time.Equal(at) || !same(*out.U, p))) || (in.F != nil && (out.F == nil || !out.F.Time.Equal(at) || out.F.PID != f.PID)) {
			t.Errorf("%s came back as %+v", b, out)
		}
	}
}

// A UDP event's payload: PID, size, daddr, saddr, dport, sport (network order), then more fields.
func TestParse(t *testing.T) {
	b := []byte{0x04, 0x01, 0, 0, 86, 0, 0, 0, // PID 260, size 86
		192, 168, 1, 248, 77, 88, 8, 8, // daddr, saddr
		0xe3, 0x1c, 0, 53, // dport 58140, sport 53
		0, 0, 0, 0}
	p, ok := parse(b, 4, true) // a DNS answer: the remote end is the source
	if !ok || p.PID != 260 || p.Size != 86 || p.Remote.String() != "77.88.8.8:53" || p.Local.String() != "192.168.1.248:58140" {
		t.Errorf("recv: %+v", p)
	}
	p, _ = parse(b, 4, false)
	if p.Remote.String() != "192.168.1.248:58140" {
		t.Errorf("send: %+v", p)
	}
	if _, ok := parse(b[:10], 4, false); ok {
		t.Error("short payload parsed")
	}
}
