package etw

import "testing"

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
