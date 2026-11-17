package live

import (
	"math"
	"testing"
	"time"
)

// gameFrames is a program presenting at fps for secs, with a freeze of ms[i] at second at[i].
func gameFrames(secs, fps float64, at, ms []float64) []time.Duration {
	var out []time.Duration
	i := 0
	for t := 0.0; t < secs; {
		out = append(out, time.Duration(t*float64(time.Second)))
		if i < len(at) && t >= at[i] {
			t += ms[i] / 1000
			i++
		} else {
			t += 1 / fps
		}
	}
	return out
}

// framed adds frames to a calm recording of n ticks: the game in front from tick from on, the
// person's browser in front before it and presenting in the background all along.
func framed(n, from int, game []time.Duration) recording {
	r := synth(n, 7, chainLinks("cable", false), bases, nil)
	r.framesState = "recorded"
	r.fg = make([]string, n)
	for t := range n {
		r.fg[t] = "game.exe"
		if t < from {
			r.fg[t] = "opera.exe"
		}
	}
	r.frames = map[string][]time.Duration{"game.exe": game, "opera.exe": gameFrames(float64(n)/2, 170, nil, nil)}
	return r
}

func TestFramesStalls(t *testing.T) {
	// 144 fps for 200 s: a 200 ms hitch at 30 s with a 150 ms one right after it (one stall), 120 ms at
	// 80 s, 500 ms at 120 s with the CPU full, a 3 s loading screen at 150 s (a pause, not a stall).
	r := framed(400, 10, gameFrames(200, 144, []float64{30, 30.4, 80, 120, 150}, []float64{200, 150, 120, 500, 3000}))
	r.cpu = make([]int8, 400)
	for i := range r.cpu {
		r.cpu[i] = 30
	}
	r.cpu[240] = 100
	r.lags = []time.Time{t0.Add(31500 * time.Millisecond), t0.Add(60 * time.Second)}
	res := analyze(r)
	f := res.Frames
	if f == nil {
		t.Fatalf("no frames: %v", res.Facts)
	}
	wantFacts(t, res, map[string]string{"live.frames": "recorded", "live.frames_exe": "game.exe", "live.frames_stalls": "3",
		"live.frames_stalls_cpu": "1", "live.frames_stalls_net": "0", "live.frames_stalled": "true", "live.frames_cause": "",
		"live.frames_fps_median": "144", "live.frames_front_pct": "97.5"})
	if f.StallsTotal != 3 || f.Stalls[0].Ms != 200 || f.Stalls[2].Ms != 500 || f.Stalls[2].CPU != 100 || len(f.Pauses) != 1 {
		t.Errorf("stalls %+v, pauses %+v", f.Stalls, f.Pauses)
	}
	if got := f.Stalls[0].At.Sub(t0).Seconds(); math.Abs(got-30) > 0.01 {
		t.Errorf("first stall at %.3f s", got)
	}
	if f.WorstMs != 500 || f.FPSLow1 > f.FPSMedian { // 3 stalls in 27 000 frames don't reach the 1 % low
		t.Errorf("worst %.1f, 1%% low %.1f, median %.1f", f.WorstMs, f.FPSLow1, f.FPSMedian)
	}
	if m := res.Lags[0]; !m.Stall || m.StallAgoS != 1.5 || m.StallMs != 200 {
		t.Errorf("mark after the hitch: %+v", m)
	}
	if m := res.Lags[1]; m.Stall {
		t.Errorf("mark with no stall before it: %+v", m)
	}
	if c := res.Chart; len(c.FPS) != 400 || c.FPS[0] != -1 || c.FPS[100] < 140 || c.Stall[60] != 1 {
		t.Errorf("chart: %d points, fps[0] %.0f, fps[100] %.0f, stall[60] %d", len(c.FPS), c.FPS[0], c.FPS[100], c.Stall[60])
	}
	if b := res.Buckets[6]; b.Stalls != 1 || b.FPSMin <= 0 { // 30-35 s
		t.Errorf("bucket at 30 s: %+v", b)
	}

	// The same stalls with the CPU full at each: the processor is named.
	for _, s := range []int{60, 160, 240} {
		r.cpu[s] = 100
	}
	wantFacts(t, analyze(r), map[string]string{"live.frames_stalls_cpu": "3", "live.frames_cause": "cpu"})
}

func TestFramesNoGame(t *testing.T) {
	// The game was in front for 20 s only, the desktop (no frames) before it: no game, no frame
	// facts beyond the state.
	r := framed(400, 360, gameFrames(200, 60, []float64{190}, []float64{300}))
	for t := range 360 {
		r.fg[t] = "explorer.exe"
	}
	res := analyze(r)
	if res.Frames != nil {
		t.Errorf("a game from 20 s in front: %+v", res.Frames)
	}
	wantFacts(t, res, map[string]string{"live.frames": "no_game", "live.frames_stalled": ""})

	r.framesState = "declined"
	wantFacts(t, analyze(r), map[string]string{"live.frames": "declined", "live.frames_exe": ""})
	r.framesState = ""
	if res := analyze(r); res.Facts["live.frames"] != "off" || res.Chart.FPS != nil || res.Buckets[0].Stalls != 0 {
		t.Errorf("off: %v", res.Facts["live.frames"])
	}
}

// A browser draws in its GPU process while its window belongs to the main one: frames are matched
// by exe name, and kept only for exes that have been in front.
func TestSessionFramesByExe(t *testing.T) {
	names := map[uint32]string{100: "msedge.exe", 200: "msedge.exe", 300: "discord.exe"}
	s := &Session{env: Env{ExeName: func(pid uint32) string { return names[pid] }, Foreground: func() string { return "msedge.exe" }},
		frames: map[string][]time.Duration{}, runFrames: map[string]*runFrames{}, pidExe: map[uint32]string{},
		started: t0, stopFrames: func() {}, errs: map[string]string{}}
	s.onFrame(200, t0.Add(time.Second)) // before the first tick: nobody was in front yet
	s.readForeground()
	for i := range 100 {
		s.onFrame(200, t0.Add(time.Second+time.Duration(i)*16*time.Millisecond))
		s.onFrame(300, t0.Add(time.Second+time.Duration(i)*16*time.Millisecond))
	}
	s.onFrame(200, t0.Add(time.Second+99*16*time.Millisecond+400*time.Millisecond)) // a 400 ms hitch
	if len(s.frames["msedge.exe"]) != 101 || s.frames["discord.exe"] != nil {
		t.Errorf("kept: msedge %d, discord %d", len(s.frames["msedge.exe"]), len(s.frames["discord.exe"]))
	}
	if rf := s.runFrames["msedge.exe"]; rf == nil || rf.stalls != 1 || math.Abs(rf.avgMs-16) > 1 {
		t.Errorf("running estimate: %+v", rf)
	}
}
