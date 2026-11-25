package rules_test

import (
	"maps"
	"slices"
	"testing"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

// healthyPC: the facts of the author's PC on 2026-11-17 (cable at 1 Gbps, router DNS, hosts pins
// that still work, MTU 1400, zapret off): only info findings.
var healthyPC = map[string]string{
	"health.adapter": "cable", "health.link_mbps": "1000", "health.link_slow": "false", "health.errors_growing": "false",
	"health.gateway": "ok", "health.apipa": "false", "health.connectivity": "internet", "health.ncsi_wrong": "false",
	"local.net": "ok", "local.vpn": "false", "health.dns": "ok", "health.dns_manual_dead": "false", "health.dnscache": "running",
	"health.proxy": "off", "health.proxy_pac": "none", "health.winhttp": "direct",
	"health.hosts": "ok", "health.hosts_pins": "289", "health.hosts_stale": "0", "health.hosts_ncsi": "false",
	"health.autotuning": "normal", "health.rss": "enabled", "health.winsock": "clean", "health.ipv6": "none",
	"health.mtu": "1400", "health.mtu_low": "true", "health.clock_off": "false", "health.w32time": "stopped",
	"health.throttler_found": "false", "local.dpi_tool": "none",
}

func TestHealthMatch(t *testing.T) {
	rs, err := rules.LoadHealth()
	if err != nil {
		t.Fatal(err)
	}
	ids := func(facts map[string]string) []string {
		var out []string
		for _, r := range rules.MatchAll(rs, facts) {
			out = append(out, r.ID)
		}
		return out
	}
	with := func(kv ...string) map[string]string {
		f := maps.Clone(healthyPC)
		for i := 0; i < len(kv); i += 2 {
			f[kv[i]] = kv[i+1]
		}
		return f
	}
	cases := []struct {
		facts map[string]string
		want  []string
	}{
		{healthyPC, []string{"health_hosts_pins", "health_mtu_low"}},
		// Several independent problems are all listed, in file order.
		{with("health.proxy", "dead", "health.hosts_stale", "36", "health.hosts_stale_found", "true", "health.hosts_unchecked", "2", "health.link_slow", "true"),
			[]string{"health_link_slow", "health_proxy_dead", "health_hosts_stale", "health_hosts_pins", "health_mtu_low"}},
		// A dead manual DNS with a working router DNS is the fixable case, not "DNS is dead".
		{with("health.dns", "dead_all", "health.dns_manual_dead", "true"), []string{"health_dns_manual_dead", "health_hosts_pins", "health_mtu_low"}},
		{with("health.dns", "dead_all"), []string{"health_dns_dead", "health_hosts_pins", "health_mtu_low"}},
		// hosts unreadable: no stale finding from a missing count.
		{with("health.hosts", "unreadable", "health.hosts_pins", "", "health.hosts_stale", ""), []string{"health_mtu_low"}},
		{with("health.hosts_stale", "", "health.hosts_unchecked", "2"), []string{"health_hosts_pins", "health_mtu_low"}},
		{with("health.autotuning", "disabled", "health.rss", "disabled"), []string{"health_hosts_pins", "health_autotuning", "health_rss_off", "health_mtu_low"}},
		{with("health.clock_off", "true", "local.dpi_tool", "zapret"), []string{"health_hosts_pins", "health_mtu_low", "health_clock_off", "health_dpi"}},
	}
	for i, c := range cases {
		delete(c.facts, "") // with() of an empty key
		for k, v := range c.facts {
			if v == "" {
				delete(c.facts, k)
			}
		}
		if got := ids(c.facts); !slices.Equal(got, c.want) {
			t.Errorf("case %d: %v, want %v", i, got, c.want)
		}
	}
}

func TestHealthRulesAndTextsAgree(t *testing.T) {
	rs, err := rules.LoadHealth()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, r := range rs {
		known[r.ID] = true
		if !slices.Contains(health.Areas, r.Area) {
			t.Errorf("%s: unknown area %s", r.ID, r.Area)
		}
		if tx.Steps[r.Area].Name == "" {
			t.Errorf("%s: area %s has no name in ru.yaml", r.ID, r.Area)
		}
		d, ok := tx.HealthFindings[r.ID]
		if !ok || d.Title == "" || d.Explain == "" {
			t.Errorf("%s: no text in ru.yaml", r.ID)
		}
		if r.Fix != "" {
			// A fix must turn into at least one action besides the cache flush.
			res := &health.Result{Steps: []probe.Step{{ID: "health_x", Facts: map[string]string{}, Evidence: map[string]any{}}}}
			p := health.MakePlan(res, []string{r.Fix})
			if r.Fix != "hosts_stale" && r.Fix != "dns_auto" && len(p.Actions) < 2 {
				t.Errorf("%s: fix %s makes no action", r.ID, r.Fix)
			}
		}
	}
	for id := range tx.HealthFindings {
		if !known[id] {
			t.Errorf("ru.yaml has text for %s, which no rule has", id)
		}
	}
}
