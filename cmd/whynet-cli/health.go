package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

// runHealth is the Windows network health check from the console. It only checks: fixing is in the
// window, where the batch is seen before it runs (docs/health.md).
func runHealth(asJSON, save, hide bool) error {
	rs, err := rules.LoadHealth()
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var onStep func(probe.Step)
	if !asJSON {
		onStep = report.HealthProgress(rs, func(s probe.Step, finds []rules.Rule) {
			fmt.Printf("%s %s (%d %s)", t.Status[report.AreaStatus(s, finds)], t.Steps[s.ID].Name, s.Ms, t.Report["ms"])
			if issue := report.HealthAreaIssue(t, s); issue != "" {
				fmt.Printf(" %s", issue)
			}
			fmt.Println()
		})
	}
	res := health.Run(ctx, markers, onStep)
	facts := res.Facts()
	finds := rules.MatchAll(rs, facts)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"findings": finds, "check": res}); err != nil {
			return err
		}
	} else {
		writeHealthText(os.Stdout, t, res, finds)
	}
	if save {
		name := report.HealthFileName(res)
		if err := os.WriteFile(name, []byte(report.HealthMarkdown(res, finds, nil, nil, nil, t, hide)), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "\nreport:", name)
	}
	return nil
}

func writeHealthText(w io.Writer, t *locales.Texts, res *health.Result, finds []rules.Rule) {
	fmt.Fprintln(w)
	incomplete := false
	for _, s := range res.Steps {
		if issue := report.HealthAreaIssue(t, s); issue != "" {
			if !incomplete {
				fmt.Fprintln(w, t.Health["head_incomplete"])
				incomplete = true
			}
			fmt.Fprintf(w, "%s %s. %s\n", t.Status[report.AreaStatus(s, finds)], t.Steps[s.ID].Name, issue)
		}
	}
	if len(finds) == 0 && !incomplete {
		fmt.Fprintln(w, t.Health["head_ok"])
	}
	facts := res.Facts()
	for _, f := range finds {
		d := t.HealthFindings[f.ID]
		fmt.Fprintf(w, "[%s] %s\n  %s\n", t.Health["kind_"+f.Kind], locales.Fill(d.Title, facts), locales.Fill(d.Explain, facts))
		if d.Advice != "" {
			fmt.Fprintf(w, "  → %s\n", locales.Fill(d.Advice, facts))
		}
	}
}
