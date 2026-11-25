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
	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

// HealthFileName is a file name for the Windows network health report.
func HealthFileName(res *health.Result) string {
	return fmt.Sprintf("whynet-windows-%s.md", res.Started.Format("2006-01-02-1504"))
}

// HealthBatch is one fix (or undo) batch as the report tells it.
type HealthBatch struct {
	Undo        bool               `json:"undo,omitempty"`
	Outcomes    []health.Outcome   `json:"outcomes"`
	Declined    bool               `json:"declined,omitempty"` // the administrator prompt was refused
	States      map[string]string  `json:"finding_states,omitempty"`
	BeforeFacts map[string]string  `json:"-"`
	UndoIssues  []health.UndoIssue `json:"undo_issues,omitempty"`
}

// AreaStatus combines the check's own status with the findings for that area.
func AreaStatus(s probe.Step, finds []rules.Rule) string {
	st := s.Status
	switch st {
	case "error", "fail", "skip":
		return st
	case "":
		st = "ok"
	}
	for _, f := range finds {
		if f.Area == s.ID {
			switch f.Kind {
			case "problem":
				return "fail"
			case "advice":
				st = "warn"
			}
		}
	}
	return st
}

// HealthAreaIssue describes an incomplete check independently of the findings it made.
func HealthAreaIssue(t *locales.Texts, s probe.Step) string {
	if s.Status == "" || s.Status == "ok" {
		return ""
	}
	if n, _ := strconv.Atoi(s.Facts["health.hosts_unchecked"]); n > 0 && s.Status == "warn" {
		return locales.Fill(t.Health["area_hosts_unchecked"], map[string]string{"n": strconv.Itoa(n)})
	}
	return t.Health["area_incomplete"]
}

// HealthProgress emits the status of a completed area and updates it if later facts change it.
func HealthProgress(rs []rules.Rule, emit func(probe.Step, []rules.Rule)) func(probe.Step) {
	facts := map[string]string{}
	status := map[string]string{}
	var steps []probe.Step
	return func(s probe.Step) {
		maps.Copy(facts, s.Facts)
		steps = append(steps, s)
		finds := rules.MatchAll(rs, facts)
		for _, step := range steps {
			if st := AreaStatus(step, finds); st != status[step.ID] {
				status[step.ID] = st
				emit(step, finds)
			}
		}
	}
}

// HealthFixStates compares the findings immediately before and after one fix batch.
func HealthFixStates(before, after *health.Result, previous, current []rules.Rule, outs []health.Outcome) map[string]string {
	states := map[string]string{}
	facts := after.Facts()
	for _, f := range previous {
		if f.Kind == "info" {
			continue
		}
		states[f.ID] = "unconfirmed"
		i := slices.IndexFunc(after.Steps, func(s probe.Step) bool { return s.ID == f.Area })
		if i < 0 || !slices.Contains([]string{"", "ok", "warn"}, after.Steps[i].Status) {
			continue
		}
		known := true
		for key := range f.When {
			if slices.Contains([]string{"", "unknown", "unreadable", "unavailable", "error"}, facts[key]) {
				known = false
				break
			}
		}
		if !known {
			continue
		}
		var planned []health.Action
		if f.Fix != "" {
			for _, act := range health.MakePlan(before, []string{f.Fix}).Actions {
				if act.ID != "flush_dns" && act.ID != "flush_arp" {
					planned = append(planned, act)
				}
			}
		}
		verified := len(planned) > 0 && !slices.ContainsFunc(planned, func(act health.Action) bool { return !after.Confirms(act) })
		switch {
		case verified && !slices.ContainsFunc(planned, func(act health.Action) bool {
			return !slices.ContainsFunc(outs, func(o health.Outcome) bool { return o.OK && o.Action.Equal(act) })
		}):
			states[f.ID] = "fixed"
		case slices.ContainsFunc(current, func(r rules.Rule) bool { return r.ID == f.ID }):
			states[f.ID] = "left"
		case after.Steps[i].Status == "warn" && !verified:
			// An unchecked pin still present in hosts can make the stale rule disappear.
		default:
			states[f.ID] = "gone"
		}
	}
	return states
}

// HealthAction is one action in plain words, as the window's list and the report show it: what
// whynet will do, or with done what it did (only for an action that went through).
func HealthAction(t *locales.Texts, a health.Action, facts map[string]string, done bool) string {
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
	if done {
		key = "did_" + strings.TrimPrefix(key, "act_")
	}
	if h[key] == "" {
		return a.ID + " " + a.Value
	}
	return strings.TrimSpace(locales.Fill(h[key], v))
}

func HealthOutcomeText(t *locales.Texts, o health.Outcome, facts map[string]string) string {
	text := HealthAction(t, o.Action, facts, o.OK)
	if o.Already {
		text = locales.Fill(t.Health["already_restored"], map[string]string{"action": HealthAction(t, o.Action, facts, false)})
	}
	if o.Backup != "" {
		text += " " + locales.Fill(t.Health["hosts_backup"], map[string]string{"path": o.Backup})
	}
	return text
}

func HealthUndoNotes(t *locales.Texts, facts map[string]string, issues []health.UndoIssue) []string {
	var out []string
	for _, issue := range issues {
		key := "undo_failed"
		v := map[string]string{"error": issue.Reason}
		if issue.Reason == health.UndoLegacyUnknown {
			v["error"] = t.Health["undo_legacy_unknown"]
		}
		if issue.Action.ID != "" {
			key, v["action"] = "undo_manual", HealthAction(t, issue.Action, facts, false)
		}
		note := locales.Fill(t.Health[key], v)
		if issue.Backup != "" {
			note += " " + locales.Fill(t.Health["hosts_backup"], map[string]string{"path": issue.Backup})
		}
		if !slices.Contains(out, note) {
			out = append(out, note)
		}
	}
	return out
}

// HealthMarkdown renders the Windows network health report: the findings, any fix batches with
// their outcomes and the re-check after them, every area's facts, the raw data. With hide, the
// private values in res.Mask are replaced across the whole text.
func HealthMarkdown(res *health.Result, finds []rules.Rule, batches []HealthBatch, after *health.Result, afterFinds []rules.Rule,
	t *locales.Texts, hide bool, undoIssues ...health.UndoIssue) string {
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
	for _, note := range HealthUndoNotes(t, facts, undoIssues) {
		w("- %s\n", note)
	}

	writeFindings := func(title string, check *health.Result, finds []rules.Rule) {
		w("\n## %s\n\n", title)
		facts := check.Facts()
		incomplete := false
		for _, s := range check.Steps {
			if issue := HealthAreaIssue(t, s); issue != "" {
				incomplete = true
				w("- **%s** %s %s\n", t.Steps[s.ID].Name, t.Status[AreaStatus(s, finds)], issue)
			}
		}
		if len(finds) == 0 && !incomplete {
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
	writeFindings(h["findings_title"], res, finds)

	for _, bt := range batches {
		title := h["fixes_title"]
		if bt.Undo {
			title = h["undo"]
		}
		w("\n## %s\n\n", title)
		before := bt.BeforeFacts
		if before == nil {
			before = facts
		}
		for _, o := range bt.Outcomes {
			mark := "✓"
			if !o.OK {
				mark = "✗ " + o.Error
			}
			w("- %s — %s\n", HealthOutcomeText(t, o, before), mark)
		}
		if bt.Declined {
			w("\n%s\n", h["declined"])
		}
		for _, note := range HealthUndoNotes(t, before, bt.UndoIssues) {
			w("- %s\n", note)
		}
		for _, id := range slices.Sorted(maps.Keys(bt.States)) {
			w("- %s — %s\n", locales.Fill(t.HealthFindings[id].Title, before), h["tag_"+bt.States[id]])
		}
	}
	if after != nil {
		writeFindings(h["after_title"], after, afterFinds)
	}

	w("\n## %s\n", h["facts_title"])
	for i, s := range res.Steps {
		st := t.Steps[s.ID]
		mark := AreaStatus(s, finds)
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
