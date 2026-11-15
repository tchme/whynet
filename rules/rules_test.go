package rules_test

import (
	"regexp"
	"testing"

	"whynet/locales"
	"whynet/rules"
)

// Cases the lab can't produce cheaply: facts straight into the rules.
func TestMatch(t *testing.T) {
	rs, _, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	ok := map[string]string{"local.net": "ok", "dns.system": "ok", "dns.doh": "ok", "dns.match": "same", "tcp.status": "ok", "tls.status": "ok", "http.class": "2xx", "http.status": "200"}
	with := func(kv ...string) map[string]string {
		f := map[string]string{}
		for k, v := range ok {
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
		{ok, "all_ok"},
		{with("local.net", "none"), "no_internet"},
		{with("local.hosts_entry", "true"), "hosts_entry"},
		{with("local.clock", "wrong", "tls.status", "cert_expired"), "clock_wrong"},
		{with("tls.status", "cert_expired"), "bad_certificate"},
		{with("http.class", "4xx", "http.status", "451", "http.geo", "true"), "geo_blocked"},
		{with("tls.intercepted", "true"), "https_intercepted"},
		{with("local.proxy", "on"), "proxy_in_way"},
		{with("http.class", "4xx", "http.status", "403", "http.challenge", "true"), "all_ok"},
		{with("dns.system", "nxdomain", "dns.doh", "unavailable", "tcp.status", "skip", "tls.status", "skip"), "no_such_site"},
		{with("dns.system", "timeout", "dns.doh", "unavailable", "tcp.status", "skip", "tls.status", "skip"), "no_internet"},
		{with("dns.system", "nxdomain"), "dns_spoofed"},
		{with("tls.status", "alert"), "unknown"},
	}
	for _, c := range cases {
		if got := rules.Match(rs, c.facts); got.ID != c.want {
			t.Errorf("%v: got %s, want %s", c.facts, got.ID, c.want)
		}
	}
}

// Every rule has texts, every text has a rule, every placeholder is a fact or "host".
func TestRulesAndTextsAgree(t *testing.T) {
	rs, _, err := rules.Load()
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
		if _, ok := tx.Diagnoses[r.ID]; !ok {
			t.Errorf("rule %s has no text in ru.yaml", r.ID)
		}
	}
	placeholder := regexp.MustCompile(`\{([a-z_.]+)\}`)
	known := regexp.MustCompile(`^(host|local\.[a-z_]+|dns\.[a-z_]+|tcp\.[a-z_]+|tls\.[a-z_]+|http\.[a-z_]+)$`)
	for id, d := range tx.Diagnoses {
		if !used[id] {
			t.Errorf("text %s has no rule", id)
		}
		if d.Title == "" || d.Explain == "" || d.Advice == "" {
			t.Errorf("text %s is incomplete", id)
		}
		for _, m := range placeholder.FindAllStringSubmatch(d.Explain+d.Advice, -1) {
			if !known.MatchString(m[1]) {
				t.Errorf("text %s: unknown placeholder {%s}", id, m[1])
			}
		}
	}
	if last := rs[len(rs)-1]; last.ID != "unknown" || len(last.When) != 0 {
		t.Errorf("the last rule must be the unconditional unknown, got %+v", last)
	}
}
