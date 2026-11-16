package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"whynet/internal/live"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

// runLive records a stability check for dur (or until Ctrl+C), then prints the verdict.
func runLive(server string, dur time.Duration, asJSON, save, hide bool) error {
	rs, err := rules.LoadLive()
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
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	last := -1
	onUpdate := func(p live.Progress) {
		if asJSON || p.Seconds%10 != 0 || p.Seconds == last {
			return
		}
		last = p.Seconds
		fmt.Printf("%d:%02d", p.Seconds/60, p.Seconds%60)
		for _, l := range p.Links {
			now := "—"
			if l.Ms >= 0 {
				now = fmt.Sprintf("%.1f", l.Ms)
			}
			fmt.Printf("  %s %s ms (%d)", l.ID, now, l.Own)
		}
		fmt.Println()
	}
	if !asJSON {
		fmt.Println(t.Live["starting"])
	}
	s, err := live.Start(context.Background(), live.DefaultEnv(markers), server, onUpdate)
	switch {
	case errors.Is(err, probe.ErrIPv6):
		return errors.New("IPv6: " + t.Live["placeholder"])
	case errors.Is(err, probe.ErrBadInput):
		return errors.New(t.UI["bad_input"])
	case err != nil:
		return err
	}
	select {
	case <-time.After(dur):
	case <-stop.Done():
	case <-s.Done():
	}
	res := s.Finish(context.Background())
	v := rules.Match(rs, res.Facts)

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"verdict": v, "check": res}); err != nil {
			return err
		}
	} else {
		d := t.LiveDiagnoses[v.ID]
		fmt.Printf("\n%s (%s)\n%s\n", d.Title, t.Confidence[v.Confidence], locales.Fill(d.Explain, res.Facts))
		for _, x := range report.LiveExtras(t, &res) {
			fmt.Println(x)
		}
		fmt.Printf("→ %s\n", locales.Fill(d.Advice, res.Facts))
	}
	if save {
		name := report.LiveFileName(&res)
		if err := os.WriteFile(name, []byte(report.LiveMarkdown(&res, v, t, hide, nil)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "\nreport:", name)
	}
	return nil
}
