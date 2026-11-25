package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/internal/report"
	"whynet/locales"
	"whynet/rules"
)

func TestHealthViewAndReportKeepJournalWarningWithPendingFix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LocalAppData", dir)
	path := filepath.Join(dir, "whynet", "health", "undo.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"saved":`), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	res := &health.Result{Steps: []probe.Step{{ID: "health_proxy", Status: "ok", Facts: map[string]string{"health.proxy": "dead", "health.proxy_server": "127.0.0.1:12334"}}}}
	finds := rules.MatchAll(a.healthRules, res.Facts())
	h := &healthState{res: res, finds: finds, plan: a.healthPlan(res, finds), after: res, afterF: finds}
	bt, prev := h.batch(res, finds, nil, false, false)
	v := a.healthView(h, bt, prev, "fix")
	md, _ := a.healthReport(h, true)
	for _, text := range []string{v.FixNote, md} {
		if !v.Fixable || v.Undo || !strings.Contains(text, "журнал отката") {
			t.Errorf("journal warning went missing alongside a remaining fix: %s", text)
		}
	}
}

func TestHealthViewAfterBatches(t *testing.T) {
	t.Setenv("LocalAppData", t.TempDir())
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	hrs, err := rules.LoadHealth()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{t: tx, healthRules: hrs}
	dead := &health.Result{Steps: []probe.Step{{ID: "health_proxy", Facts: map[string]string{
		"health.proxy": "dead", "health.proxy_server": "http://127.0.0.1:12334", "health.proxy_pac": "none", "health.winhttp": "direct"}}}}
	off := &health.Result{Steps: []probe.Step{{ID: "health_proxy", Facts: map[string]string{
		"health.proxy": "off", "health.proxy_pac": "none", "health.winhttp": "direct"}}}}
	h := &healthState{res: dead, finds: rules.MatchAll(hrs, dead.Facts())}
	h.plan = a.healthPlan(dead, h.finds)
	offF := rules.MatchAll(hrs, off.Facts())

	// A fix: the done line names the proxy from the check it was planned on, not the re-check.
	outs := []health.Outcome{{Action: h.plan.Actions[0], OK: true}}
	bt, prev := h.batch(off, offF, outs, false, false)
	h.after, h.afterF = off, offF
	v := a.healthView(h, bt, prev, "fix")
	if v.Head != "Починил 1 из 1" || v.Tone != "ok" || !strings.Contains(v.Done[0].Text, "Выключил прокси http://127.0.0.1:12334") {
		t.Errorf("fix: %q %q %q", v.Head, v.Tone, v.Done[0].Text)
	}

	// An undo that was half refused: says so, and the raw "declined" error stays out (the line below explains it).
	outs = []health.Outcome{{Action: health.Action{ID: "proxy_enable", Value: "1"}, OK: true},
		{Action: health.Action{ID: "hosts_uncomment", Lines: []string{"a"}}, Error: health.ErrDeclined.Error()}}
	bt, prev = h.batch(dead, h.finds, outs, true, true)
	h.after, h.afterF = dead, h.finds
	v = a.healthView(h, bt, prev, "undo")
	if v.Head != "Вернул 1 из 2" || v.Done[1].Error != "" || v.Done[1].Status != "fail" || !v.Declined || !v.Fixable {
		t.Errorf("undo: %+v", v)
	}
}

func TestHealthIncompleteAreasAndBatchVerification(t *testing.T) {
	t.Setenv("LocalAppData", t.TempDir())
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	hrs, err := rules.LoadHealth()
	if err != nil {
		t.Fatal(err)
	}
	a := &App{t: tx, healthRules: hrs}
	proxy := func(state, value, addr string) probe.Step {
		return probe.Step{ID: "health_proxy", Status: state, Facts: map[string]string{"health.proxy": value, "health.proxy_server": addr}}
	}
	snap := func(steps ...probe.Step) *health.Result { return &health.Result{Steps: steps} }
	finds := func(res *health.Result) []rules.Rule { return rules.MatchAll(hrs, res.Facts()) }
	unreadable := probe.Step{ID: "health_hosts", Status: "error", Facts: map[string]string{"health.hosts": "unreadable"}}
	healthy := probe.Step{ID: "health_clock", Status: "ok", Facts: map[string]string{"health.clock_off": "false"}}
	res := snap(unreadable, healthy)
	v := a.healthView(&healthState{res: res}, report.HealthBatch{}, nil, "")
	if v.Tone != "warn" || v.Head != tx.Health["head_incomplete"] || v.Areas[0].Status != "error" || v.Areas[0].Note == "" ||
		!slices.Equal(v.OKAreas, []string{tx.Steps[healthy.ID].Name}) {
		t.Fatalf("initial error was lost: %+v", v)
	}
	before := snap(proxy("ok", "dead", "second-proxy.example"))
	h := &healthState{res: snap(healthy), after: before, afterF: finds(before)}
	done := health.Outcome{Action: health.Action{ID: "proxy_enable", Value: "0"}, OK: true}
	for _, tc := range []struct {
		name, state, tone, count string
		after                    *health.Result
		outs                     []health.Outcome
		declined                 bool
	}{
		{"second batch succeeded", "fixed", "ok", "1", snap(proxy("ok", "off", "")), []health.Outcome{done}, false},
		{"other area incomplete", "fixed", "warn", "1", snap(proxy("ok", "off", ""), unreadable), []health.Outcome{done}, false},
		{"operation, same area failed", "unconfirmed", "warn", "0", snap(proxy("error", "off", "")), []health.Outcome{done}, false},
		{"missing area", "unconfirmed", "warn", "0", snap(healthy), []health.Outcome{done}, false},
		{"missing fact", "unconfirmed", "warn", "0", snap(probe.Step{ID: "health_proxy", Status: "ok"}), []health.Outcome{done}, false},
		{"unknown fact", "unconfirmed", "warn", "0", snap(proxy("ok", "unknown", "")), []health.Outcome{done}, false},
		{"different operation", "gone", "warn", "0", snap(proxy("ok", "off", "")), []health.Outcome{{Action: health.Action{ID: "flush_dns"}, OK: true}}, false},
		{"operation refused", "gone", "warn", "0", snap(proxy("ok", "off", "")), []health.Outcome{{Action: done.Action, Error: health.ErrDeclined.Error()}}, true},
		{"symptom left", "left", "bad", "0", snap(proxy("ok", "dead", "third-proxy.example"), unreadable), []health.Outcome{done}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			afterF := finds(tc.after)
			bt, prev := h.batch(tc.after, afterF, tc.outs, false, tc.declined)
			cur := &healthState{res: h.res, after: tc.after, afterF: afterF, plan: a.healthPlan(tc.after, afterF)}
			v := a.healthView(cur, bt, prev, "fix")
			if v.Head != "Починил "+tc.count+" из 1" || v.Tone != tc.tone || len(v.Finds) != 1 || v.Finds[0].State != tc.state {
				t.Fatalf("incorrect outcome: %+v", v)
			}
			if tc.state == "fixed" && !strings.Contains(v.Done[0].Text, "second-proxy.example") {
				t.Errorf("action lost the preceding snapshot: %v", v.Done)
			}
			if tc.state == "left" && (!v.Fixable || len(v.Plan) < 2 || !strings.Contains(v.Finds[0].Text, "third-proxy.example")) {
				t.Errorf("remaining fix went missing: %+v", v)
			}
		})
	}
}

func TestHealthHostsPartialRecheckUsesTheActualLines(t *testing.T) {
	t.Setenv("LocalAppData", t.TempDir())
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	const line = "192.0.2.1 stale.example"
	const newLine = "192.0.2.2 different.example"
	makeHosts := func(status, found, unchecked string, active map[string]bool, stale []string) *health.Result {
		return &health.Result{ActiveHostLines: active, Steps: []probe.Step{{ID: "health_hosts", Status: status,
			Facts: map[string]string{"health.hosts": "ok", "health.hosts_stale_found": found, "health.hosts_stale": "1", "health.hosts_unchecked": unchecked,
				"health.hosts_stale_addrs": "192.0.2.1", "health.hosts_stale_names": "stale.example"}, Evidence: map[string]any{"stale_lines": stale}}}}
	}
	before := makeHosts("ok", "true", "0", map[string]bool{line: true}, []string{line})
	beforeFinds := rules.MatchAll(a.healthRules, before.Facts())
	h := &healthState{res: before, finds: beforeFinds}
	outs := []health.Outcome{{Action: health.Action{ID: "hosts_comment", Lines: []string{line}}, OK: true}}
	for _, tc := range []struct {
		name, want string
		after      *health.Result
		fixable    bool
	}{
		{"unchecked target still present", "unconfirmed", makeHosts("warn", "false", "1", map[string]bool{line: true}, nil), false},
		{"target absent and unrelated pins unchecked", "fixed", makeHosts("warn", "false", "1", map[string]bool{newLine: true}, nil), false},
		{"target absent and new stale line", "fixed", makeHosts("warn", "true", "1", map[string]bool{newLine: true}, []string{newLine}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := rules.MatchAll(a.healthRules, tc.after.Facts())
			batch, prev := h.batch(tc.after, current, outs, false, false)
			v := a.healthView(&healthState{res: before, finds: beforeFinds, after: tc.after, afterF: current, plan: a.healthPlan(tc.after, current)}, batch, prev, "fix")
			if len(v.Finds) == 0 || v.Finds[0].State != tc.want || v.Tone == "ok" || v.Fixable != tc.fixable || v.Areas[0].Note == "" {
				t.Errorf("partial hosts: %+v", v)
			}
		})
	}
}
