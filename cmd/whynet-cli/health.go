package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	res := health.Run(ctx, markers, func(s probe.Step) {
		if !asJSON {
			fmt.Printf("· %s (%d %s)\n", t.Steps[s.ID].Name, s.Ms, t.Report["ms"])
		}
	})
	facts := res.Facts()
	finds := rules.MatchAll(rs, facts)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"findings": finds, "check": res}); err != nil {
			return err
		}
	} else {
		fmt.Println()
		if len(finds) == 0 {
			fmt.Println(t.Health["head_ok"])
		}
		for _, f := range finds {
			d := t.HealthFindings[f.ID]
			fmt.Printf("[%s] %s\n  %s\n", t.Health["kind_"+f.Kind], locales.Fill(d.Title, facts), locales.Fill(d.Explain, facts))
			if d.Advice != "" {
				fmt.Printf("  → %s\n", locales.Fill(d.Advice, facts))
			}
		}
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
