package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"whynet/internal/health"
	"whynet/locales"
	"whynet/rules"
)

// HealthFileName is a file name for the Windows network health report.
func HealthFileName(res *health.Result) string {
	return fmt.Sprintf("whynet-windows-%s.md", res.Started.Format("2006-01-02-1504"))
}

// HealthBatch is one fix (or undo) batch as the report tells it.
type HealthBatch struct {
	Undo     bool             `json:"undo,omitempty"`
	Outcomes []health.Outcome `json:"outcomes"`
	Declined bool             `json:"declined,omitempty"` // the administrator prompt was refused
}

// AreaStatus is each area's status from its findings: fail (a problem), warn (advice), ok.
func AreaStatus(finds []rules.Rule) map[string]string {
	st := map[string]string{}
	for _, f := range finds {
		switch {
		case f.Kind == "problem":
			st[f.Area] = "fail"
		case f.Kind == "advice" && st[f.Area] != "fail":
			st[f.Area] = "warn"
		}
	}
	return st
}

// HealthAction is one action in plain words, as the window's list and the report show it.
func HealthAction(t *locales.Texts, a health.Action, facts map[string]string) string {
	h := t.Health
	v := map[string]string{"value": a.Value, "adapter": a.Adapter, "n": strconv.Itoa(len(a.Lines)), "date": time.Now().Format("2006-01-02")}
	key := "act_" + a.ID
	switch a.ID {
	case "proxy_enable":
		key, v["value"] = "act_proxy_on", ""
		if a.Value == "0" {
			key, v["value"] = "act_proxy_off", facts["health.proxy_server"]
		}
	case "pac_set":
		key = map[bool]string{true: "act_pac_off", false: "act_pac_on"}[a.Value == ""]
	case "winhttp_set":
		key = map[bool]string{true: "act_winhttp_reset", false: "act_winhttp_set"}[a.Value == ""]
	case "dns_source":
		key, v["value"] = "act_dns_"+a.Value, strings.Join(a.Values, ", ")
	case "tcp_global":
		k, val, _ := strings.Cut(a.Value, "=")
		key, v["value"] = "act_autotuning", val
		if k == "rss" {
			key = map[bool]string{true: "act_rss_on", false: "act_rss_off"}[val == "enabled"]
		}
	}
	if h[key] == "" {
		return a.ID + " " + a.Value
	}
	return strings.TrimSpace(locales.Fill(h[key], v))
}

// HealthMarkdown renders the Windows network health report: the findings, any fix batches with
// their outcomes and the re-check after them, every area's facts, the raw data. With hide, the
// private values in res.Mask are replaced across the whole text.
func HealthMarkdown(res *health.Result, finds []rules.Rule, batches []HealthBatch, after *health.Result, afterFinds []rules.Rule,
	t *locales.Texts, hide bool) string {
	facts := res.Facts()
	r, h := t.Report, t.Health
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# %s\n\n", h["report_title"])
	w("> **%s** %s\n\n", r["ai_label"], h["ai_prompt"])
	w("## %s\n\n", r["summary"])
	if osv, _ := res.Evidence("health_address", "os").(string); osv != "" {
		w("- %s: %s\n", r["computer"], osv)
	}
	if pub, _ := res.Evidence("health_address", "public").(map[string]string); pub["isp"] != "" {
		w("- %s: %s (%s, %s)\n", r["isp"], pub["isp"], pub["asn"], pub["country"])
	}
	w("- %s: %s, %.0f s\n", h["checked"], res.Started.Format("2006-01-02 15:04:05 -07:00"), res.Seconds)
	privacy := r["privacy_shown"]
	if hide {
		privacy = r["privacy_hidden"]
	}
	w("- %s: %s\n", r["privacy"], privacy)

	writeFindings := func(title string, finds []rules.Rule, facts map[string]string) {
		w("\n## %s\n\n", title)
		if len(finds) == 0 {
			w("%s\n", h["no_findings"])
			return
		}
		for _, f := range finds {
			d := t.HealthFindings[f.ID]
			w("- **%s** — %s (`%s`, %s). %s", locales.Fill(d.Title, facts), h["kind_"+f.Kind], f.ID, t.Confidence[f.Confidence], locales.Fill(d.Explain, facts))
			if d.Advice != "" {
				w(" %s %s", r["advice"]+":", locales.Fill(d.Advice, facts))
			}
			w("\n")
		}
	}
	writeFindings(h["findings_title"], finds, facts)

	for _, bt := range batches {
		title := h["fixes_title"]
		if bt.Undo {
			title = h["undo"]
		}
		w("\n## %s\n\n", title)
		for _, o := range bt.Outcomes {
			mark := "✓"
			if !o.OK {
				mark = "✗ " + o.Error
			}
			w("- %s — %s\n", HealthAction(t, o.Action, facts), mark)
		}
		if bt.Declined {
			w("\n%s\n", h["declined"])
		}
	}
	if after != nil {
		writeFindings(h["after_title"], afterFinds, after.Facts())
	}

	status := AreaStatus(finds)
	w("\n## %s\n", h["facts_title"])
	for i, s := range res.Steps {
		st := t.Steps[s.ID]
		mark := status[s.ID]
		if mark == "" {
			mark = s.Status
		}
		w("\n### %d. %s %s (%d %s)\n\n%s\n\n", i+1, st.Name, t.Status[mark], s.Ms, r["ms"], st.What)
		for _, k := range slices.Sorted(maps.Keys(s.Facts)) {
			w("- `%s`: %s\n", k, s.Facts[k])
		}
	}

	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(map[string]any{"findings": finds, "check": res, "fixes": batches, "after": after}); err != nil {
		raw.WriteString(err.Error())
	}
	w("\n## %s\n\n````json\n%s````\n\n%s %s\n", r["raw"], raw.String(), r["footer"], Version)

	out := b.String()
	if hide {
		for private, shown := range res.Mask {
			out = strings.ReplaceAll(out, private, shown)
		}
	}
	return out
}
