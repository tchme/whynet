package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"time"

	"whynet/internal/live"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

// runApp is the app check from the console: pick the program by name or PID, record its TCP
// connections (and with withUDP its UDP, admin rights) for dur (Ctrl+C ends early), check its
// servers, print the verdict.
func runApp(target string, dur time.Duration, withUDP, asJSON, save, hide bool) error {
	rs, err := rules.LoadApp()
	if err != nil {
		return err
	}
	_, markers, err := rules.Load()
	if err != nil {
		return err
	}
	t, err := locales.Load("ru")
	if err != nil {
		return err
	}
	list, err := probe.ListPrograms(markers.Browsers)
	if err != nil {
		return err
	}
	var prog *probe.Program
	if target != "" {
		if prog, err = probe.FindProgram(target, markers.Browsers); err != nil {
			return err
		}
	}
	if prog == nil {
		if target != "" {
			fmt.Println(locales.Fill(t.App["not_running"], map[string]string{"name": target}))
		}
		fmt.Println(t.App["running"])
		for _, p := range list {
			fmt.Printf("  %-28s %d\n", p.Exe, p.Conns)
		}
		if target != "" {
			os.Exit(1)
		}
		return nil
	}
	if prog.Browser {
		return errors.New(locales.Fill(t.App["browser"], map[string]string{"name": prog.Name}))
	}

	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, cancelRec := context.WithTimeout(stop, dur)
	defer cancelRec()
	if !asJSON {
		fmt.Println(locales.Fill(t.App["recording"], map[string]string{"name": prog.Name, "seconds": strconv.Itoa(int(dur.Seconds()))}))
	}
	start := time.Now()
	progress := func(s []probe.AppServer) {
		if asJSON {
			return
		}
		conns, cut, stuck := 0, 0, 0
		for _, x := range s {
			conns, cut, stuck = conns+x.Conns, cut+x.Cut, stuck+x.Stuck
		}
		secs := int(time.Since(start).Seconds())
		fmt.Println(locales.Fill(t.App["progress"], map[string]string{"time": fmt.Sprintf("%d:%02d", secs/60, secs%60),
			"servers": strconv.Itoa(len(s)), "conns": strconv.Itoa(conns), "cut": strconv.Itoa(cut), "stuck": strconv.Itoa(stuck)}))
	}
	udp := probe.UDPRecording{State: "off"}
	var udpRec *probe.UDPRecorder
	stopUDP := func() probe.UDPRecording { return udp }
	if withUDP {
		udpRec, stopUDP = probe.StartUDP(start)
	}
	servers, recorded, err := probe.RecordApp(ctx, prog.Exe, markers.Browsers, udpRec, nil, progress)
	udp = stopUDP()
	if err != nil {
		return err
	}
	if withUDP && udp.State != "recorded" && !asJSON {
		fmt.Println("UDP:", udp.State, udp.Error)
	}
	env := probe.DefaultEnv(markers)
	env.Echo = live.Ping

	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelCheck()
	onStep := func(s probe.Step) {
		if !asJSON {
			fmt.Printf("%s %s (%d %s)\n", t.Status[s.Status], t.Steps[s.ID].Name, s.Ms, t.Report["ms"])
		}
	}
	res := probe.CheckApp(checkCtx, env, prog.Exe, servers, udp, recorded, probe.DNSCacheNames(), onStep)
	v := rules.Match(rs, res.Facts())

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"verdict": v, "check": res}); err != nil {
			return err
		}
	} else {
		d := t.AppDiagnoses[v.ID]
		facts := res.Facts()
		fmt.Printf("\n%s (%s)\n%s\n→ %s\n", d.Title, t.Confidence[v.Confidence], locales.Fill(d.Explain, facts), locales.Fill(d.Advice, facts))
	}
	if save {
		name := report.AppFileName(res)
		if err := os.WriteFile(name, []byte(report.AppMarkdown(res, v, t, hide)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "\nreport:", name)
	}
	return nil
}
