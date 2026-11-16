package live

import (
	"math"
	"slices"
	"sort"
	"strconv"
	"time"
)

// LagBefore is how far back a "лагнуло" mark looks for the jump it was about.
const LagBefore = 10 * time.Second

const (
	lostRTT     = -1 // no answer within the timeout
	skipRTT     = -2 // not measured (the server's ticks before its stand-in took over)
	minSeconds  = 30
	maxEpisodes = 200
	maxPoints   = 900
	lagAfter    = time.Second
)

// recording is everything a session measured; analyze turns it into a Result.
type recording struct {
	links   []Link
	samples [][]int32 // per link, per tick: answer in µs, or lostRTT / skipRTT
	start   time.Time
	tick    time.Duration
	hops    []Hop
	wifi    []WiFi
	lags    []time.Time
	pathVPN bool
}

// nearer lists, for each link, the links between it and this computer.
func nearer(id string) []string {
	switch id {
	case Router:
		return nil
	case Beyond:
		return []string{Router}
	}
	return []string{Router, Beyond}
}

// farther lists the links behind a link: trouble on a link is real only if it reaches them.
func farther(id string) []string {
	switch id {
	case Router:
		return []string{Beyond, CtlRU, CtlWorld, Target}
	case Beyond:
		return []string{CtlRU, CtlWorld, Target}
	}
	return nil
}

func analyze(r recording) Result {
	res := Result{Started: r.start, Facts: map[string]string{}}
	n := 0
	if len(r.samples) > 0 {
		n = len(r.samples[0])
	}
	res.Seconds = int((time.Duration(n) * r.tick).Seconds())
	idx := map[string]int{}
	for i, l := range r.links {
		idx[l.ID] = i
	}

	// Spikes and losses per link: a spike is an answer over max(3 × median, median + 50 ms).
	event := make([][]bool, len(r.links))
	answered := make([]int, len(r.links))
	for li := range r.links {
		ms := answers(r.samples[li])
		answered[li] = len(ms)
		med := median(ms)
		limit := math.Max(3*med, med+50)
		event[li] = make([]bool, n)
		for t, v := range r.samples[li] {
			event[li][t] = v == lostRTT || v >= 0 && float64(v)/1000 > limit
		}
	}
	measured := func(id string, t int) bool {
		li, ok := idx[id]
		return ok && answered[li] > 0 && r.samples[li][t] != skipRTT
	}
	// Trouble on a link counts only if it reaches some link behind it: a router that answers its
	// own pings slowly while it forwards traffic fine is not a problem (the trace rule, on the chain).
	confirmed := make([][]bool, len(r.links))
	for li, l := range r.links {
		confirmed[li] = make([]bool, n)
		for t := range n {
			if !event[li][t] {
				continue
			}
			behind, hit := false, false
			for _, f := range farther(l.ID) {
				if measured(f, t) {
					behind = true
					hit = hit || event[idx[f]][t]
				}
			}
			confirmed[li][t] = !behind || hit
		}
	}
	// "По его вине": confirmed trouble while every nearer link was calm.
	own := make([][]bool, len(r.links))
	for li, l := range r.links {
		own[li] = make([]bool, n)
		for t := range n {
			if !confirmed[li][t] {
				continue
			}
			calm := true
			for _, id := range nearer(l.ID) {
				if j, ok := idx[id]; ok && confirmed[j][t] {
					calm = false
				}
			}
			own[li][t] = calm
		}
	}
	nearerTrouble := func(id string, t int) bool {
		for _, nid := range nearer(id) {
			if j, ok := idx[nid]; ok && confirmed[j][t] {
				return true
			}
		}
		return false
	}

	for li, l := range r.links {
		s := LinkStats{Link: l}
		var all, ownView []float64
		prev, jsum, jn := -1.0, 0.0, 0
		for t, v := range r.samples[li] {
			if v == skipRTT {
				continue
			}
			s.Sent++
			if v == lostRTT {
				s.Lost++
			} else {
				ms := float64(v) / 1000
				all = append(all, ms)
				if prev >= 0 {
					jsum += math.Abs(ms - prev)
					jn++
				}
				prev = ms
				// Its own view: nearer links calm, and not a slow answer to its own ping that
				// nothing behind it felt.
				if !nearerTrouble(l.ID, t) && (!event[li][t] || confirmed[li][t]) {
					ownView = append(ownView, ms)
				}
			}
			if event[li][t] {
				s.Events++
			}
			if own[li][t] {
				s.Own++
			}
		}
		s.Median, s.P95, s.Worst = median(all), percentile(all, 95), slices.Max(append([]float64{0}, all...))
		s.OwnMed, s.OwnP95 = median(ownView), percentile(ownView, 95)
		if jn > 0 {
			s.Jitter = jsum / float64(jn)
		}
		if s.Sent > 0 {
			s.LossPct = pct(s.Lost, s.Sent)
			s.OwnPct = pct(s.Own, s.Sent)
		}
		// Unstable: its own trouble on ≥ 2 % of the moments, or answers that wander (p95 far above
		// the median) while nearer links were calm. An intermediate router (beyond) answers pings
		// to itself slowly by design, so only its confirmed trouble counts.
		jitterLimit := 50.0
		if l.ID == Router {
			jitterLimit = 20 // a healthy local link answers in 1-3 ms
		}
		switch {
		case s.Sent > 0 && len(all) == 0:
			s.State = "silent"
		case s.OwnPct >= 2, l.ID != Beyond && len(ownView) > 0 && s.OwnP95-s.OwnMed >= jitterLimit:
			s.State = "unstable"
		default:
			s.State = "stable"
		}
		res.Links = append(res.Links, s)
	}

	res.Facts["live.seconds"] = strconv.Itoa(res.Seconds)
	if res.Seconds < minSeconds {
		res.Facts["live.too_short"] = "true"
	}
	if r.pathVPN {
		res.Facts["live.path_vpn"] = "true"
	}
	state := map[string]string{}
	for _, s := range res.Links {
		state[s.ID] = s.State
		f := "live." + s.ID
		res.Facts[f+"_addr"] = s.Addr.String()
		res.Facts[f+"_median_ms"] = fmtMs(s.Median)
		res.Facts[f+"_p95_ms"] = fmtMs(s.P95)
		res.Facts[f+"_loss_pct"] = fmtPct(s.LossPct)
		res.Facts[f+"_own_pct"] = fmtPct(s.OwnPct)
		if s.ID == Router && s.Kind != "" {
			res.Facts["live.router_kind"] = s.Kind
		}
		if s.ID == Target && s.StandIn.IsValid() {
			res.Facts["live.server_stand_in"] = s.StandIn.String()
		}
	}
	for _, id := range []string{Router, Beyond, Target} {
		res.Facts["live."+id] = cmpOr(state[id], "none")
	}
	switch {
	case state[CtlRU] == "unstable" && state[CtlWorld] == "unstable":
		res.Facts["live.internet"] = "unstable"
	case state[CtlRU] == "silent" && state[CtlWorld] == "silent":
		res.Facts["live.internet"] = "silent"
	default:
		res.Facts["live.internet"] = "stable"
	}

	// Spells of trouble, in time order.
	var eps []Episode
	for li, l := range r.links {
		for t := 0; t < n; t++ {
			if !own[li][t] {
				continue
			}
			e := Episode{At: r.start.Add(time.Duration(t) * r.tick), Link: l.ID}
			for ; t < n && own[li][t]; t++ {
				e.Seconds += r.tick.Seconds()
				if v := r.samples[li][t]; v == lostRTT {
					e.Lost++
				} else {
					e.WorstMs = math.Max(e.WorstMs, float64(v)/1000)
				}
			}
			eps = append(eps, e)
		}
	}
	sort.SliceStable(eps, func(i, j int) bool { return eps[i].At.Before(eps[j].At) })
	res.Total = len(eps)
	res.Spikes = eps[:min(len(eps), maxEpisodes)]
	if p := periodic(eps, Router); p > 0 {
		res.Facts["live.router_periodic_s"] = strconv.Itoa(p)
	}

	// "Лагнуло" marks are bookmarks: a person presses the keys a second or three after the lag,
	// so each mark looks back LagBefore for the nearest moment the network jumped (and lagAfter
	// ahead, for an early press). A mark with no jump near it proves nothing on its own: no verdict
	// is drawn from marks.
	for _, at := range r.lags {
		m := LagMark{At: at}
		mark := int(at.Sub(r.start) / r.tick)
		best := -1
		for t := max(mark-int(LagBefore/r.tick), 0); t <= min(mark+int(lagAfter/r.tick), n-1); t++ {
			for li := range r.links {
				if confirmed[li][t] && (best < 0 || abs(t-mark) < abs(best-mark)) {
					best = t
				}
			}
		}
		if best >= 0 {
			m.Spike = true
			m.AgoS = math.Round(float64(mark-best)*r.tick.Seconds()*10) / 10
			for li, l := range r.links {
				if v := r.samples[li][best]; v >= 0 {
					m.WorstMs = math.Max(m.WorstMs, float64(v)/1000)
				}
				if own[li][best] && m.Link == "" {
					m.Link = l.ID // the nearest link that caused it
				}
			}
		}
		res.Lags = append(res.Lags, m)
	}
	if len(r.lags) > 0 {
		res.Facts["live.lag_marks"] = strconv.Itoa(len(r.lags))
	}

	if w := summarizeWiFi(r.wifi); w != nil {
		res.WiFi = w
		res.Facts["live.wifi_signal"] = strconv.Itoa(w.AvgSignal)
		if w.Last.Band != "" {
			res.Facts["live.wifi_band"] = w.Last.Band
		}
		if w.Last.RSSI != 0 {
			res.Facts["live.wifi_rssi"] = strconv.Itoa(w.Last.RSSI)
		}
	}
	res.Trace = r.hops
	for _, h := range r.hops {
		if h.Jump {
			res.Facts["live.route_jump_hop"] = strconv.Itoa(h.TTL)
		}
	}
	res.Buckets = buckets(r, n)
	res.Chart = chart(r, n, own, event)
	return res
}

// periodic finds spells of trouble on one link that repeat about every 60 s (the Windows
// background Wi-Fi scan): at least 3 spells, two thirds of the gaps within 55-65 s.
func periodic(eps []Episode, link string) int {
	var starts []time.Time
	for _, e := range eps {
		if e.Link == link {
			starts = append(starts, e.At)
		}
	}
	if len(starts) < 3 {
		return 0
	}
	hit := 0
	for i := 1; i < len(starts); i++ {
		if d := starts[i].Sub(starts[i-1]); d >= 55*time.Second && d <= 65*time.Second {
			hit++
		}
	}
	if hit >= 2 && 3*hit >= 2*(len(starts)-1) {
		return 60
	}
	return 0
}

func summarizeWiFi(ws []WiFi) *WiFiSummary {
	if len(ws) == 0 {
		return nil
	}
	s := &WiFiSummary{Last: ws[len(ws)-1], MinSignal: 101, Readings: len(ws)}
	sum := 0
	for _, w := range ws {
		sum += w.Signal
		s.MinSignal = min(s.MinSignal, w.Signal)
	}
	s.AvgSignal = sum / len(ws)
	return s
}

// buckets slices the session: 5 s up to 5 minutes, then about 60 rows whatever the length.
func buckets(r recording, n int) []Bucket {
	if n == 0 || r.tick <= 0 {
		return nil
	}
	total := time.Duration(n) * r.tick
	size := 5 * time.Second
	if total > 5*time.Minute {
		size = (total/60 + time.Second - 1).Truncate(time.Second)
	}
	per := max(1, int(size/r.tick))
	var out []Bucket
	for from := 0; from < n; from += per {
		b := Bucket{At: r.start.Add(time.Duration(from) * r.tick), Links: map[string]BucketStats{}}
		for li, l := range r.links {
			var ms []float64
			lost := 0
			for t := from; t < min(from+per, n); t++ {
				switch v := r.samples[li][t]; {
				case v == lostRTT:
					lost++
				case v >= 0:
					ms = append(ms, float64(v)/1000)
				}
			}
			if len(ms) == 0 && lost == 0 {
				continue
			}
			bs := BucketStats{Lost: lost, Median: median(ms)}
			if len(ms) > 0 {
				bs.Min, bs.Max = slices.Min(ms), slices.Max(ms)
			}
			b.Links[l.ID] = bs
		}
		out = append(out, b)
	}
	return out
}

// chart thins the session to at most maxPoints per link, keeping the worst of each slice.
func chart(r recording, n int, own, event [][]bool) Chart {
	per := max(1, (n+maxPoints-1)/maxPoints)
	c := Chart{Start: r.start, Step: (time.Duration(per) * r.tick).Seconds(), Series: map[string]ChartSeries{}}
	for li, l := range r.links {
		var s ChartSeries
		for from := 0; from < n; from += per {
			worst, lost, st := -1.0, false, 0
			for t := from; t < min(from+per, n); t++ {
				switch v := r.samples[li][t]; {
				case v == lostRTT:
					lost = true
				case v >= 0:
					worst = math.Max(worst, float64(v)/1000)
				}
				switch {
				case own[li][t]:
					st = 1
				case event[li][t] && st == 0:
					st = 2
				}
			}
			if lost {
				worst = -1
			}
			s.Ms = append(s.Ms, math.Round(worst*10)/10)
			s.State = append(s.State, st)
		}
		c.Series[l.ID] = s
	}
	return c
}

func answers(samples []int32) []float64 {
	var ms []float64
	for _, v := range samples {
		if v >= 0 {
			ms = append(ms, float64(v)/1000)
		}
	}
	return ms
}

func median(v []float64) float64 { return percentile(v, 50) }

// percentile by the nearest rank; 0 for no values.
func percentile(v []float64, p float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	i := int(math.Ceil(p/100*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

func abs(x int) int { return max(x, -x) }

func pct(a, b int) float64 { return math.Round(1000*float64(a)/float64(b)) / 10 }

func fmtMs(v float64) string {
	if v < 10 {
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.Itoa(int(math.Round(v)))
}

func fmtPct(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
