package probe

import (
	"net/netip"
	"testing"
	"time"
)

func TestUDPRecorder(t *testing.T) {
	t0 := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	r := NewUDPRecorder(t0)
	r.SetPIDs(map[uint32]bool{7: true})
	game := netip.MustParseAddrPort("185.25.182.77:27015")
	voice := netip.MustParseAddrPort("104.29.136.195:19300")
	dead := netip.MustParseAddrPort("162.159.130.234:50001")
	cut := netip.MustParseAddrPort("5.45.207.1:3074")
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

	for ms := 0; ms < 20000; ms += 16 { // a game: 60 packets a second each way for 20 s
		r.Add(at(ms), 7, game, 80, false)
		if ms < 10000 || ms > 10600 { // the server froze for 0.6 s while the game kept sending
			r.Add(at(ms+3), 7, game, 200, true)
		}
	}
	for ms := 0; ms < 20000; ms += 20 { // a call: both quiet from 5 to 8 s (nobody talks): no freeze
		if ms < 5000 || ms >= 8000 {
			r.Add(at(ms), 7, voice, 100, false)
			r.Add(at(ms+5), 7, voice, 100, true)
		}
	}
	for ms := 0; ms < 3000; ms += 300 { // a server that never answers
		r.Add(at(ms), 7, dead, 60, false)
	}
	for ms := 0; ms < 20000; ms += 50 { // answers until 15 s, then nothing while the program keeps trying
		r.Add(at(ms), 7, cut, 60, false)
		if ms < 15000 {
			r.Add(at(ms+10), 7, cut, 60, true)
		}
	}
	r.Add(at(100), 8, game, 60, true)                                      // another process
	r.Add(at(100), 7, netip.MustParseAddrPort("192.168.1.1:3074"), 60, true) // the LAN
	r.Add(at(100), 7, netip.MustParseAddrPort("77.88.8.8:53"), 60, true)    // DNS

	got := map[string]*UDPServer{}
	res := r.Result()
	for _, s := range res {
		got[s.Label()] = s
	}
	if len(res) != 4 || res[0].Label() != game.String() {
		t.Fatalf("servers: %d, first %s", len(res), res[0].Label())
	}
	g := got[game.String()]
	if g.Gaps != 1 || !g.Steady || g.WorstGapMs < 600 || g.WorstGapMs > 640 || g.IntervalMs < 16 || g.IntervalMs > 17 || g.GapAtS[0] != 10 {
		t.Errorf("game: %+v", g)
	}
	if v := got[voice.String()]; v.Gaps != 0 || v.Steady {
		t.Errorf("call with a silence: %+v", v)
	}
	if d := got[dead.String()]; !d.NoReply || d.Sent != 10 {
		t.Errorf("dead: %+v", d)
	}
	if c := got[cut.String()]; c.Gaps != 1 || c.WorstGapMs < 4900 || !c.Stopped {
		t.Errorf("stopped answering: %+v", c)
	}

	facts := map[string]string{}
	udpFacts(facts, UDPRecording{State: "recorded", Servers: res})
	want := map[string]string{"app.udp": "recorded", "app.udp_servers": "4", "app.udp_noreply": "1", "app.udp_main": game.String(),
		"app.udp_main_noreply": "false", "app.udp_freezes": "false", "app.udp_stopped": "1"}
	for k, v := range want {
		if facts[k] != v {
			t.Errorf("%s = %q, want %q", k, facts[k], v)
		}
	}
	facts = map[string]string{}
	udpFacts(facts, UDPRecording{State: "declined"})
	if facts["app.udp"] != "declined" || facts["app.udp_servers"] != "" {
		t.Errorf("declined: %v", facts)
	}
}
