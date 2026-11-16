package rules_test

import (
	"regexp"
	"testing"

	"whynet/locales"
	"whynet/rules"
)

func TestAppMatch(t *testing.T) {
	rs, err := rules.LoadApp()
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{"local.net": "ok", "local.dpi_tool": "none", "app.servers": "5", "app.checked": "5"}
	with := func(kv ...string) map[string]string {
		f := map[string]string{}
		for k, v := range base {
			f[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			f[kv[i]] = kv[i+1]
		}
		return f
	}
	blocked := []string{"app.blocked", "true"}
	cases := []struct {
		facts map[string]string
		want  string
	}{
		{base, "app_ok"},
		{with("app.servers", "0", "app.checked", "0"), "app_no_traffic"},
		{with(append(blocked, "app.sni", "true")...), "app_blocked_name"},
		{with(append(blocked, "app.maybe", "true")...), "app_blocked_maybe"},
		{with(append(blocked, "app.unreachable", "true")...), "app_unreachable"},
		// cut by name outranks "no answer" when both are seen
		{with(append(blocked, "app.sni", "true", "app.unreachable", "true")...), "app_blocked_name"},
		{with(append(blocked, "app.sni", "true", "app.blocked_fakeip", "true")...), "app_router_path"},
		{with(append(blocked, "app.sni", "true", "local.dpi_tool", "several")...), "app_dpi_conflict"},
		{with(append(blocked, "app.sni", "true", "local.dpi_tool", "zapret", "local.dpi_game_filter", "all", "local.dpi_ipset_filter", "none", "app.game_ports", "true")...), "app_game_filter_none"},
		{with(append(blocked, "app.sni", "true", "local.dpi_tool", "zapret", "app.dpi_unlisted", "true")...), "app_dpi_not_listed"},
		{with(append(blocked, "app.sni", "true", "local.dpi_tool", "goodbyedpi")...), "app_dpi_fails"},
		{with("app.lossy", "true"), "app_lossy"},
		{with("app.program_trouble", "true"), "app_flaky"},
	}
	for _, c := range cases {
		if got := rules.Match(rs, c.facts); got.ID != c.want {
			t.Errorf("facts %v: got %s, want %s", c.facts, got.ID, c.want)
		}
	}
	// The site rules never fire on an app check's facts.
	site, _, _ := rules.Load()
	if got := rules.Match(site, with(append(blocked, "app.sni", "true")...)); got.ID != "unknown" {
		t.Errorf("site rules matched app facts: %s", got.ID)
	}
}

func TestAppRulesAndTextsAgree(t *testing.T) {
	rs, err := rules.LoadApp()
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
		if _, ok := tx.AppDiagnoses[r.ID]; !ok {
			t.Errorf("app rule %s has no text in ru.yaml", r.ID)
		}
	}
	placeholder := regexp.MustCompile(`\{([a-z_.]+)\}`)
	known := regexp.MustCompile(`^(host|(app|local)\.[a-z_]+)$`)
	for id, d := range tx.AppDiagnoses {
		if !used[id] {
			t.Errorf("app text %s has no rule", id)
		}
		if d.Title == "" || d.Explain == "" || d.Advice == "" {
			t.Errorf("app text %s is incomplete", id)
		}
		for _, m := range placeholder.FindAllStringSubmatch(d.Explain+d.Advice, -1) {
			if !known.MatchString(m[1]) {
				t.Errorf("app text %s: unknown placeholder {%s}", id, m[1])
			}
		}
	}
	if last := rs[len(rs)-1]; last.ID != "unknown" || len(last.When) != 0 {
		t.Errorf("the last app rule must be the unconditional unknown, got %+v", last)
	}
	for _, id := range []string{"record", "names", "check"} {
		if tx.Steps[id].Name == "" || tx.Steps[id].What == "" {
			t.Errorf("step %s has no text", id)
		}
	}
}
