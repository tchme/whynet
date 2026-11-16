// Command whynet-cli checks a site from the console: the same core as the app.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

func main() {
	asJSON := flag.Bool("json", false, "print facts and raw evidence as JSON")
	save := flag.Bool("save", false, "save the AI report (.md) in the current folder")
	noHide := flag.Bool("no-hide", false, "keep the public IP and other private values in the report")
	noBrowser := flag.Bool("no-browser", false, "skip loading the page in a hidden Edge/Chrome")
	stability := flag.Bool("live", false, "stability check: ping the chain (router, ISP, internet, server) while you play")
	app := flag.Bool("app", false, "app check: record a running program's TCP connections and check its servers (no name: list programs)")
	dur := flag.Duration("for", 2*time.Minute, "with -live: how long to record (Ctrl+C ends it early); at most 1h. With -app: 30s by default, at most 2m")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "whynet-cli %s — why doesn't a site open?\n\nusage: whynet-cli [flags] <site>\n       whynet-cli -live [-for 2m] [game server]\n       whynet-cli -app [-for 30s] [program or PID]\n\n", report.Version)
		flag.PrintDefaults()
	}
	flag.Parse()
	durSet := false
	flag.Visit(func(f *flag.Flag) { durSet = durSet || f.Name == "for" })
	var err error
	switch {
	case *app && flag.NArg() <= 1:
		d := 30 * time.Second
		if durSet {
			d = min(max(*dur, 5*time.Second), 2*time.Minute)
		}
		err = runApp(flag.Arg(0), d, *asJSON, *save, !*noHide)
	case *stability && flag.NArg() <= 1:
		err = runLive(flag.Arg(0), min(*dur, time.Hour), *asJSON, *save, !*noHide)
	case !*stability && !*app && flag.NArg() == 1:
		err = run(flag.Arg(0), *asJSON, *save, !*noHide, !*noBrowser)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "whynet:", err)
		os.Exit(1)
	}
}

func run(site string, asJSON, save, hide, browser bool) error {
	rs, markers, err := rules.Load()
	if err != nil {
		return err
	}
	t, err := locales.Load("ru")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	onStep := func(s probe.Step) {
		if !asJSON {
			fmt.Printf("%s %s (%d %s)\n", t.Status[s.Status], t.Steps[s.ID].Name, s.Ms, t.Report["ms"])
		}
	}
	env := probe.DefaultEnv(markers)
	if !browser {
		env.Browser = nil
	}
	res, err := probe.Run(ctx, env, site, onStep)
	if errors.Is(err, probe.ErrBadInput) {
		return errors.New(t.UI["bad_input"])
	}
	if err != nil {
		return err
	}
	v := rules.Match(rs, res.Facts())

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"verdict": v, "check": res}); err != nil {
			return err
		}
	} else {
		d := t.Diagnoses[v.ID]
		facts := res.Facts()
		fmt.Printf("\n%s (%s)\n%s\n→ %s\n", d.Title, t.Confidence[v.Confidence], locales.Fill(d.Explain, facts), locales.Fill(d.Advice, facts))
	}
	if save {
		name := report.FileName(res)
		if err := os.WriteFile(name, []byte(report.Markdown(res, v, t, hide)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "\nreport:", name)
	}
	return nil
}
