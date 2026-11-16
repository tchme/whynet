// Package live is the stability check: while the person plays, it pings every link of the
// chain (router, the node beyond it, the internet, the game server) at once and traces the
// route, so the analysis can tell where instability lives. Like package probe, it gathers
// facts; the rules (rules/live.yaml) decide.
package live

import (
	"net/netip"
	"time"
)

// Reply is one ICMP answer.
type Reply struct {
	From netip.Addr    // who answered: the target, or a router on the way when the TTL ran out
	RTT  time.Duration //
	Kind string        // reply (the target), ttl (a router on the way), unreachable
}
