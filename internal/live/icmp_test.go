package live

import (
	"net/netip"
	"testing"
	"time"
)

// The real ICMP calls, against loopback: no network or admin rights needed.
func TestSystemEchoLoopback(t *testing.T) {
	lo := netip.MustParseAddr("127.0.0.1")
	r, ok, err := systemEcho(lo, 0, time.Second)
	if err != nil || !ok || r.Kind != "reply" || r.From != lo || r.RTT < 0 || r.RTT > 100*time.Millisecond {
		t.Fatalf("ping 127.0.0.1: %+v ok=%v err=%v", r, ok, err)
	}
	// TTL 1 still reaches loopback: the target itself answers.
	if r, ok, err := systemEcho(lo, 1, time.Second); err != nil || !ok || r.Kind != "reply" {
		t.Fatalf("ttl 1 to 127.0.0.1: %+v ok=%v err=%v", r, ok, err)
	}
	if _, _, err := systemEcho(netip.MustParseAddr("::1"), 0, time.Second); err == nil {
		t.Error("IPv6 should be refused")
	}
}
