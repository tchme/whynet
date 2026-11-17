package live

import (
	"math"
	"slices"
	"strconv"
	"time"
)

// Frame times (stage 3, docs/stability.md): every frame each program sends to the screen comes
// from package etw through the elevated helper; the game is the program with the most frames
// while its window was in front. A stall is a frame far longer than the ones just before it.
const (
	stallFactor  = 3                // a stall: a frame over max(3 × the usual, usual + 30 ms)
	stallExtraMs = 30               //
	stallBase    = 2 * time.Second  // "the usual": the median frame of the 2 s before
	stallMerge   = time.Second      // stalls this close together are one
	pauseMs      = 2000             // a gap this long is a pause (loading, a menu), not a stall
	minFront     = 30 * time.Second // less time in front with frames: no game was seen
	stallsBar    = 3                // this many stalls make a verdict
	dwmExe       = "dwm.exe"        // the desktop compositor presents all the time; never the game
)

// FramesSummary is the game's frames over the session.
type FramesSummary struct {
	Exe         string  `json:"exe"`
	FrontPct    float64 `json:"front_pct"`  // share of the recording the game's window was in front
	FPSMedian   float64 `json:"fps_median"` // from the median frame time
	FPSAvg      float64 `json:"fps_avg"`
	FPSLow1     float64 `json:"fps_low1"` // from the 99th percentile frame time
	WorstMs     float64 `json:"worst_ms"` // the longest frame, pauses left out
	Frames      int     `json:"frames"`   // frames counted (the window in front)
	Stalls      []Stall `json:"stalls,omitempty"`
	StallsTotal int     `json:"stalls_total"`
	StallsNet   int     `json:"stalls_net"` // at a moment the network jumped too
	StallsCPU   int     `json:"stalls_cpu"` // at CPU ≥ CPUBusy
	Pauses      []Stall `json:"pauses,omitempty"`
}

// Stall is a moment the game's picture froze (or a pause: a gap over 2 s).
type Stall struct {
	At  time.Time `json:"at"`
	Ms  float64   `json:"ms"`          // the longest frame in it
	CPU int       `json:"cpu_max_pct"` // the highest CPU load around it; -1 not measured
	Net bool      `json:"net"`         // the network jumped at the same moment
}

// frameTicks is the game's frames per tick, for the chart, the buckets and the lag marks.
type frameTicks struct {
	fps    []float64 // per tick, -1 when the game was not in front
	stall  []bool    // a stall began in this tick
	stalls []stallAt // every stall, not only the first maxEpisodes
}

type stallAt struct {
	tick int
	from time.Duration // since the start: the last frame before the freeze
	ms   float64
}

// analyzeFrames picks the game and reads its frames. netTrouble marks the ticks some link had
// trouble of its own. It sets the live.frames* facts; nil when there is nothing to show.
func analyzeFrames(r recording, n int, netTrouble []bool, facts map[string]string) (*FramesSummary, *frameTicks) {
	facts["live.frames"] = cmpOr(r.framesState, "off")
	if r.framesState != "recorded" || n == 0 || r.tick <= 0 {
		return nil, nil
	}
	tickOf := func(d time.Duration) int { return int(d / r.tick) }
	front := func(exe string, t int) bool { return t >= 0 && t < n && t < len(r.fg) && r.fg[t] == exe }

	// The game: the most frames presented while its own window was in front.
	game, best := "", 0
	for exe, fr := range r.frames {
		c := 0
		for _, d := range fr {
			if front(exe, tickOf(d)) {
				c++
			}
		}
		if exe != dwmExe && c > best {
			game, best = exe, c
		}
	}
	frontTicks, frameTicksN := 0, 0
	if game != "" {
		seen := map[int]bool{}
		for _, d := range r.frames[game] {
			if t := tickOf(d); front(game, t) {
				seen[t] = true
			}
		}
		frameTicksN = len(seen)
		for t := range n {
			if front(game, t) {
				frontTicks++
			}
		}
	}
	if game == "" || time.Duration(frameTicksN)*r.tick < minFront {
		facts["live.frames"] = "no_game"
		return nil, nil
	}

	fr := slices.Clone(r.frames[game])
	slices.Sort(fr) // events from several CPUs can come out of order
	ft := &frameTicks{fps: make([]float64, n), stall: make([]bool, n)}
	count := make([]int, n)
	for _, d := range fr {
		if t := tickOf(d); t < n {
			count[t]++
		}
	}
	for t := range n {
		ft.fps[t] = -1
		if front(game, t) {
			ft.fps[t] = math.Round(float64(count[t])/r.tick.Seconds()*10) / 10
		}
	}

	// Frame times while the game was in front all the way; gaps over pauseMs are pauses.
	type gap struct {
		from, to time.Duration
		ms       float64
	}
	var gaps, pauses []gap
	perTick := make([][]float64, n)
	for i := 1; i < len(fr); i++ {
		a, b := fr[i-1], fr[i]
		ta, tb := tickOf(a), tickOf(b)
		inFront := tb < n
		for t := ta; inFront && t <= tb; t++ {
			inFront = front(game, t)
		}
		if !inFront {
			continue
		}
		g := gap{a, b, float64(b-a) / float64(time.Millisecond)}
		if g.ms > pauseMs {
			pauses = append(pauses, g)
			continue
		}
		gaps = append(gaps, g)
		perTick[tb] = append(perTick[tb], g.ms)
	}
	all := make([]float64, len(gaps))
	for i, g := range gaps {
		all[i] = g.ms
	}
	usual := median(all)
	back := max(1, int(stallBase/r.tick))
	baseOf := make([]float64, n)
	for t := range n {
		var w []float64
		for tt := max(0, t-back); tt < t; tt++ {
			w = append(w, perTick[tt]...)
		}
		baseOf[t] = usual
		if len(w) >= 10 {
			baseOf[t] = median(w)
		}
	}

	s := &FramesSummary{Exe: game, FrontPct: pct(frontTicks, n), Frames: len(gaps)}
	if len(all) > 0 {
		sum := 0.0
		for _, v := range all {
			sum += v
		}
		s.FPSMedian = fps(usual)
		s.FPSAvg = math.Round(float64(len(all))/(sum/1000)*10) / 10
		s.FPSLow1 = fps(percentile(all, 99))
		s.WorstMs = math.Round(slices.Max(all)*10) / 10
	}
	stallOf := func(g gap) Stall {
		t := tickOf(g.from)
		st := Stall{At: r.start.Add(g.from), Ms: math.Round(g.ms*10) / 10, CPU: cpuMax(r.cpu, t-1, t+2)}
		for tt := max(0, t-1); tt <= min(t+1, n-1) && tt < len(netTrouble); tt++ {
			st.Net = st.Net || netTrouble[tt]
		}
		return st
	}
	var lastEnd time.Duration = -1
	for _, g := range gaps {
		base := baseOf[tickOf(g.to)]
		if g.ms <= math.Max(stallFactor*base, base+stallExtraMs) {
			continue
		}
		if lastEnd >= 0 && g.from-lastEnd <= stallMerge { // the same hitch, a few slow frames long
			last := &ft.stalls[len(ft.stalls)-1]
			last.ms = math.Max(last.ms, g.ms)
			lastEnd = g.to
			continue
		}
		ft.stalls = append(ft.stalls, stallAt{tickOf(g.from), g.from, g.ms})
		ft.stall[tickOf(g.from)] = true
		lastEnd = g.to
	}
	for _, sa := range ft.stalls {
		st := stallOf(gap{from: sa.from, ms: sa.ms})
		if st.Net {
			s.StallsNet++
		}
		if st.CPU >= CPUBusy {
			s.StallsCPU++
		}
		if len(s.Stalls) < maxEpisodes {
			s.Stalls = append(s.Stalls, st)
		}
	}
	s.StallsTotal = len(ft.stalls)
	for _, p := range pauses[:min(len(pauses), maxEpisodes)] {
		st := stallOf(p)
		st.Net, st.CPU = false, -1
		s.Pauses = append(s.Pauses, st)
	}

	facts["live.frames_exe"] = game
	facts["live.frames_front_pct"] = fmtPct(s.FrontPct)
	facts["live.frames_fps_median"] = strconv.Itoa(int(math.Round(s.FPSMedian)))
	facts["live.frames_fps_low1"] = strconv.Itoa(int(math.Round(s.FPSLow1)))
	facts["live.frames_worst_ms"] = fmtMs(s.WorstMs)
	facts["live.frames_stalls"] = strconv.Itoa(s.StallsTotal)
	facts["live.frames_stalls_net"] = strconv.Itoa(s.StallsNet)
	facts["live.frames_stalls_cpu"] = strconv.Itoa(s.StallsCPU)
	if mins := (time.Duration(frontTicks) * r.tick).Minutes(); mins > 0 {
		facts["live.frames_stalls_per_min"] = strconv.FormatFloat(math.Round(float64(s.StallsTotal)/mins*10)/10, 'f', 1, 64)
	}
	if s.StallsTotal >= stallsBar {
		facts["live.frames_stalled"] = "true"
		if 2*s.StallsCPU > s.StallsTotal {
			facts["live.frames_cause"] = "cpu"
		}
	}
	return s, ft
}

func fps(frameMs float64) float64 {
	if frameMs <= 0 {
		return 0
	}
	return math.Round(1000/frameMs*10) / 10
}
