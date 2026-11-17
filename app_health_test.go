package main

import (
	"strings"
	"testing"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

func TestHealthViewAfterBatches(t *testing.T) {
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
	h.after, h.afterF = off, rules.MatchAll(hrs, off.Facts())

	// A fix: the done line names the proxy from the check it was planned on, not the re-check.
	outs := []health.Outcome{{Action: h.plan.Actions[0], OK: true}}
	v := a.healthView(h, outs, dead.Facts(), "fix", false)
	if v.Head != "Починил 1 из 1" || v.Tone != "ok" || !strings.Contains(v.Done[0].Text, "Выключил прокси http://127.0.0.1:12334") {
		t.Errorf("fix: %q %q %q", v.Head, v.Tone, v.Done[0].Text)
	}

	// An undo that was half refused: says so, and the raw "declined" error stays out (the line below explains it).
	outs = []health.Outcome{{Action: health.Action{ID: "proxy_enable", Value: "1"}, OK: true},
		{Action: health.Action{ID: "hosts_uncomment", Lines: []string{"a"}}, Error: health.ErrDeclined.Error()}}
	h.after, h.afterF = dead, h.finds
	v = a.healthView(h, outs, off.Facts(), "undo", true)
	if v.Head != "Вернул 1 из 2" || v.Done[1].Error != "" || v.Done[1].Status != "fail" || !v.Declined || !v.Fixable {
		t.Errorf("undo: %+v", v)
	}
}
