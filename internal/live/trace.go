package live

import (
	"context"
	"net/netip"
	"sync"
	"time"
)

const maxTTL = 30

// EchoFunc sends one ICMP echo with a TTL (0: the system default); ok false when nobody answered.
type EchoFunc func(ip netip.Addr, ttl int, timeout time.Duration) (r Reply, ok bool, err error)

// hopCounts is one TTL over every trace round.
type hopCounts struct {
	sent    int
	from    map[netip.Addr]int
	ms      []float64
	reached bool // the target itself answered at this TTL
}

// tracer runs trace rounds to one address and keeps what every TTL answered.
type tracer struct {
	target netip.Addr
	mu     sync.Mutex
	hops   [maxTTL]hopCounts
	limit  int // TTLs to probe: the target's TTL once known
}

func newTracer(target netip.Addr) *tracer { return &tracer{target: target, limit: maxTTL} }

// round sends one probe per TTL, all at once, and adds the answers. A TTL beyond the one where
// the target answered is not counted (the target answers there too).
func (tr *tracer) round(echo EchoFunc, timeout time.Duration) {
	tr.mu.Lock()
	limit := tr.limit
	tr.mu.Unlock()
	type answer struct {
		r  Reply
		ok bool
	}
	got := make([]answer, limit+1)
	var wg sync.WaitGroup
	for ttl := 1; ttl <= limit; ttl++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { recover() }() // a failing probe is a lost one
			r, ok, err := echo(tr.target, ttl, timeout)
			got[ttl] = answer{r, ok && err == nil}
		}()
	}
	wg.Wait()

	dest := 0
	for ttl := 1; ttl <= limit; ttl++ {
		if a := got[ttl]; a.ok && a.r.Kind == "reply" && a.r.From == tr.target {
			dest = ttl
			break
		}
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	last := limit
	if dest > 0 {
		last = dest
		tr.limit = dest
	}
	for ttl := 1; ttl <= last; ttl++ {
		h := &tr.hops[ttl-1]
		h.sent++
		if a := got[ttl]; a.ok {
			if h.from == nil {
				h.from = map[netip.Addr]int{}
			}
			h.from[a.r.From]++
			h.ms = append(h.ms, float64(a.r.RTT.Microseconds())/1000)
			h.reached = h.reached || ttl == dest
		}
	}
	if dest == 0 {
		// The target never answered: stop two TTLs past the last node that did.
		lastSeen := 0
		for ttl := 1; ttl <= limit; ttl++ {
			if got[ttl].ok {
				lastSeen = ttl
			}
		}
		tr.limit = min(maxTTL, max(lastSeen+2, 3))
	}
}

// addr is the node that answered most often at a TTL.
func (h *hopCounts) addr() netip.Addr {
	var best netip.Addr
	n := 0
	for a, c := range h.from {
		if c > n || c == n && a.Less(best) {
			best, n = a, c
		}
	}
	return best
}

// firstBeyond is the first node in TTL 1-3 that is not the router (or the target): the ISP's
// edge, or the VPN server when the default route goes through a VPN.
func (tr *tracer) firstBeyond(router netip.Addr) netip.Addr {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for ttl := 1; ttl <= min(3, tr.limit); ttl++ {
		if a := tr.hops[ttl-1].addr(); a.IsValid() && a != router && a != tr.target {
			return a
		}
	}
	return netip.Addr{}
}

// lastAnswering is the farthest node before the target that answers: the target's stand-in.
func (tr *tracer) lastAnswering() netip.Addr {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for ttl := tr.limit; ttl >= 1; ttl-- {
		if a := tr.hops[ttl-1].addr(); a.IsValid() && a != tr.target {
			return a
		}
	}
	return netip.Addr{}
}

// result reads the route: who each node is, its loss and delay, whether that loss is real, and
// where the delay jumps and stays.
func (tr *tracer) result(ctx context.Context, asn func(ctx context.Context, ip string) (string, string)) []Hop {
	tr.mu.Lock()
	var hops []Hop
	for ttl := 1; ttl <= tr.limit; ttl++ {
		c := tr.hops[ttl-1]
		if c.sent == 0 {
			break
		}
		h := Hop{TTL: ttl, Addr: "*", Sent: c.sent, Lost: c.sent - len(c.ms)}
		if a := c.addr(); a.IsValid() {
			h.Addr = a.String()
			h.Median, h.Worst = median(c.ms), percentile(c.ms, 100)
		}
		switch {
		case c.reached:
			h.Note = "target"
		case len(c.ms) == 0:
			h.Note = "silent"
		}
		hops = append(hops, h)
	}
	tr.mu.Unlock()
	readRoute(hops)

	if asn != nil {
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for i := range hops {
			if hops[i].Addr == "*" {
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { recover() }()
				hops[i].ASN, hops[i].Network = asn(ctx, hops[i].Addr)
			}()
		}
		wg.Wait()
	}
	return hops
}

// readRoute applies the rules of reading a trace: loss at a node counts only when every later
// node that answers loses at least about as much (routers rate-limit answers to pings meant for
// them while forwarding traffic fine); the delay "jumps" at the first node where it grows by
// ≥ 30 ms over the previous answering node and no later node comes back down.
func readRoute(hops []Hop) {
	loss := func(h Hop) float64 { return 100 * float64(h.Lost) / float64(max(h.Sent, 1)) }
	for i := range hops {
		h := &hops[i]
		if h.Addr == "*" || loss(*h) <= 2 {
			continue
		}
		counts := true
		for _, later := range hops[i+1:] {
			if later.Addr != "*" && loss(later) < loss(*h)-2 {
				counts = false
			}
		}
		h.LossCounts = counts
		if !counts && h.Note == "" {
			h.Note = "rate_limited"
		}
	}
	prev := -1.0
	for i := range hops {
		h := &hops[i]
		if h.Addr == "*" {
			continue
		}
		if prev >= 0 && h.Median-prev >= 30 {
			stays := true
			for _, later := range hops[i+1:] {
				if later.Addr != "*" && later.Median < h.Median-10 {
					stays = false
				}
			}
			if stays {
				h.Jump = true
				return
			}
		}
		prev = h.Median
	}
}
