package rules_test

import (
	"regexp"
	"testing"

	"whynet/locales"
	"whynet/rules"
)

// Stability verdicts: the nearest link that causes trouble by itself names the verdict.
func TestLiveMatch(t *testing.T) {
	rs, err := rules.LoadLive()
	if err != nil {
		t.Fatal(err)
	}
	calm := map[string]string{"live.seconds": "185", "live.router": "stable", "live.router_kind": "wifi",
		"live.beyond": "stable", "live.internet": "stable", "live.server": "stable"}
	with := func(kv ...string) map[string]string {
		f := map[string]string{}
		for k, v := range calm {
			f[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			f[kv[i]] = kv[i+1]
		}
		return f
	}
	cases := []struct {
		facts map[string]string
		want  string
	}{
		{calm, "live_stable"},
		{with("live.too_short", "true", "live.router", "unstable"), "live_too_short"},
		{with("live.router", "silent", "live.server", "unstable"), "live_router_silent"},
		{with("live.router", "none"), "live_router_silent"},
		{with("live.router", "unstable", "live.server", "unstable"), "live_wifi"}, // the friend's case
		{with("live.router", "unstable", "live.router_kind", "cable"), "live_lan"},
		{with("live.beyond", "unstable", "live.internet", "unstable"), "live_isp"},
		{with("live.beyond", "unstable", "live.path_vpn", "true"), "live_vpn"},
		{with("live.internet", "unstable", "live.path_vpn", "true"), "live_vpn"},
		{with("live.internet", "unstable", "live.server", "unstable"), "live_isp_network"},
		{with("live.server", "unstable"), "live_route"},
		{with("live.server", "none", "live.lag_calm", "true"), "live_not_network"},
		{with("live.router", "unstable", "live.lag_calm", "true"), "live_wifi"}, // a real cause outranks a calm mark
	}
	for _, c := range cases {
		if got := rules.Match(rs, c.facts); got.ID != c.want {
			t.Errorf("%v: got %s, want %s", c.facts, got.ID, c.want)
		}
	}
	// The site rules never fire on a stability check's facts.
	site, _, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := rules.Match(site, with("live.router", "unstable")); got.ID != "unknown" {
		t.Errorf("site rules matched stability facts: %s", got.ID)
	}
}

func TestLiveRulesAndTextsAgree(t *testing.T) {
	rs, err := rules.LoadLive()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, r := range rs {
		used[r.ID] = true
		if _, ok := tx.LiveDiagnoses[r.ID]; !ok {
			t.Errorf("live rule %s has no text in ru.yaml", r.ID)
		}
	}
	placeholder := regexp.MustCompile(`\{([a-z_.]+)\}`)
	known := regexp.MustCompile(`^live\.[a-z_]+$`)
	for id, d := range tx.LiveDiagnoses {
		if !used[id] {
			t.Errorf("live text %s has no rule", id)
		}
		if d.Title == "" || d.Explain == "" || d.Advice == "" {
			t.Errorf("live text %s is incomplete", id)
		}
		for _, m := range placeholder.FindAllStringSubmatch(d.Explain+d.Advice, -1) {
			if !known.MatchString(m[1]) {
				t.Errorf("live text %s: unknown placeholder {%s}", id, m[1])
			}
		}
	}
	if last := rs[len(rs)-1]; last.ID != "live_stable" || len(last.When) != 0 {
		t.Errorf("the last live rule must be the unconditional live_stable, got %+v", last)
	}
	if tx.Live["report_prompt"] == "" || tx.Live["done_in"] == "" {
		t.Error("live words missing")
	}
}
