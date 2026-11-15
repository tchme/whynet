// Package report builds the AI report: one Markdown file with everything a
// check learned, readable by a person and ready to drop into a chat with an AI.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

const Version = "0.1.0"

// cell makes text safe inside a Markdown table cell.
func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ", "\r", "").Replace(s)
}

// FileName is a file name for the report on res, safe on Windows.
func FileName(res *probe.Result) string {
	host := strings.NewReplacer(":", "_", "/", "_", `\`, "_").Replace(res.Host)
	return fmt.Sprintf("whynet-%s-%s.md", host, res.Started.Format("2006-01-02-1504"))
}

// Markdown renders the report. With hide, the private values in res.Mask are
// replaced across the whole text, raw data included.
func Markdown(res *probe.Result, v rules.Verdict, t *locales.Texts, hide bool) string {
	facts := res.Facts()
	d := t.Diagnoses[v.ID]
	r := t.Report
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	w("# %s\n\n", r["title"])
	w("> **%s** %s\n\n", r["ai_label"], r["ai_prompt"])

	w("## %s\n\n", r["summary"])
	w("- %s: %s (`%s`)\n", r["site"], strings.TrimSpace(res.Input), res.Host)
	w("- %s: %s\n", r["time"], res.Started.Format("2006-01-02 15:04:05 -07:00"))
	w("- %s: **%s** (%s). %s\n", r["verdict"], d.Title, t.Confidence[v.Confidence], locales.Fill(d.Explain, facts))
	w("- %s: %s\n", r["advice"], locales.Fill(d.Advice, facts))
	if len(res.Steps) > 0 && res.Steps[0].ID == "local" {
		ev := res.Steps[0].Evidence
		if osv, _ := ev["os"].(string); osv != "" {
			w("- %s: %s\n", r["computer"], osv)
		}
		if pub, _ := ev["public"].(map[string]string); pub["isp"] != "" {
			w("- %s: %s (%s, %s)\n", r["isp"], pub["isp"], pub["asn"], pub["country"])
		}
	}
	if facts["local.dpi_tool"] != "" && (facts["local.dpi_tool"] != "none" || facts["local.dpi_installed"] == "true") {
		w("- %s: %s\n", r["dpi"], locales.Fill(r["dpi_line"], t.Plain(facts)))
	}
	privacy := r["privacy_shown"]
	if hide {
		privacy = r["privacy_hidden"]
	}
	w("- %s: %s\n", r["privacy"], privacy)

	w("\n## %s\n", r["steps"])
	for i, s := range res.Steps {
		st := t.Steps[s.ID]
		w("\n### %d. %s %s (%d %s)\n\n%s\n\n", i+1, st.Name, t.Status[s.Status], s.Ms, r["ms"], st.What)
		for _, k := range slices.Sorted(maps.Keys(s.Facts)) {
			w("- `%s`: %s\n", k, s.Facts[k])
		}
	}

	for _, s := range res.Steps {
		if br, ok := s.Evidence["browser"].(probe.BrowserResult); ok {
			w("\n## %s\n\n", r["browser"])
			if len(br.Hosts) == 0 {
				w("%s\n", locales.Fill(r["browser_none"], map[string]string{"status": br.Status + " " + br.Error}))
				continue
			}
			w("%s\n|---|---|---|---|\n", r["browser_cols"])
			for _, h := range br.Hosts[:min(25, len(br.Hosts))] {
				var errs []string
				for _, e := range slices.Sorted(maps.Keys(h.Errors)) {
					errs = append(errs, fmt.Sprintf("%s ×%d", e, h.Errors[e]))
				}
				w("| %s | %d | %d | %s |\n", cell(h.Host), h.Requests, h.Failed, cell(strings.Join(errs, ", ")))
			}
		}
		if hosts, ok := s.Evidence["hosts"].([]*probe.HostCheck); ok {
			w("\n## %s\n\n%s\n|---|---|---|---|---|\n", r["content"], r["content_cols"])
			for _, h := range hosts {
				kinds := make([]string, len(h.Kinds))
				for i, k := range h.Kinds {
					kinds[i] = t.Kinds[k]
				}
				detail := h.Detail
				if h.BrowserOK {
					detail += " (the browser loaded from it fine: not counted)"
				}
				if h.KBps > 0 {
					detail += fmt.Sprintf(", %d KB/s", h.KBps)
				}
				ping := "—"
				if h.PingMs > 0 {
					ping = fmt.Sprint(h.PingMs)
				}
				w("| %s | %s | %s | %s | %s |\n", cell(h.Host), cell(strings.Join(kinds, ", ")), ping, cell(t.Results[h.Result]), cell(detail))
			}
		}
	}

	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"verdict": v, "check": res}); err != nil {
		raw.WriteString(err.Error())
	}
	// four backticks: the raw data may itself contain a ``` fence
	w("\n## %s\n\n````json\n%s````\n\n%s %s\n", r["raw"], raw.String(), r["footer"], Version)

	out := b.String()
	if hide {
		for private, shown := range res.Mask {
			out = strings.ReplaceAll(out, private, shown)
		}
	}
	return out
}
