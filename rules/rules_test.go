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
		{with("http.class", "4xx", "http.status", "403", "http.challenge", "true"), "bot_check"},
		{with("dns.system", "nxdomain", "dns.doh", "unavailable", "tcp.status", "skip", "tls.status", "skip"), "no_such_site"},
		{with("dns.system", "timeout", "dns.doh", "unavailable", "tcp.status", "skip", "tls.status", "skip"), "no_internet"},
		{with("dns.system", "nxdomain"), "dns_blocked"},
		{with("tls.status", "alert"), "unknown"},
		// behind a fake-ip router: the name is cut, the path works; a stub page still says more
		{with("dns.match", "fakeip", "tls.status", "timeout", "tls.control", "ok", "http.status", "timeout", "http.class", "fail"), "sni_blocked_router"},
		{with("dns.match", "fakeip", "tls.status", "timeout", "tls.control", "ok", "http.stub", "true"), "isp_stub"},
		{with("dns.match", "fakeip", "tls.status", "timeout", "tls.control", "failed"), "proxy_path_broken"},
		// DPI-bypass tools: a cut with a tool running is about the tool first
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "several"), "dpi_conflict"},
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "zapret", "local.dpi_listed", "false"), "dpi_not_listed"},
		{with("tls.status", "timeout", "tls.control", "failed", "local.dpi_tool", "zapret", "local.dpi_listed", "true"), "dpi_strategy_fails"},
		{with("tls.status", "eof", "local.dpi_tool", "goodbyedpi"), "dpi_strategy_fails"},
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "none", "local.dpi_installed", "true"), "dpi_off"},
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "none", "local.dpi_installed", "false"), "sni_blocked"},
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "zapret", "local.dpi_listed", "excluded"), "dpi_strategy_fails"},
		{with("local.dpi_tool", "zapret", "local.dpi_listed", "true"), "all_ok"},
		// not listed by name, but by address (a Cloudflare range in ipset-all.txt): the strategy is at fault
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "zapret", "local.dpi_listed", "false", "tcp.dpi_ipset", "true"), "dpi_strategy_fails"},
		{with("tls.status", "reset", "tls.control", "ok", "local.dpi_tool", "zapret", "local.dpi_listed", "false", "tcp.dpi_ipset", "false"), "dpi_not_listed"},
		// the handshake passes, the request is reset (live, Flex Ltd. 2026-11-16)
		{with("http.status", "reset", "http.class", "fail", "local.dpi_tool", "none"), "cut_midway"},
		{with("http.status", "reset", "http.class", "fail", "local.dpi_tool", "zapret", "local.dpi_listed", "false", "tcp.dpi_ipset", "true"), "dpi_strategy_fails"},
		{with("http.status", "reset", "http.class", "fail", "local.dpi_tool", "zapret", "local.dpi_listed", "false", "tcp.dpi_ipset", "false"), "dpi_not_listed"},
		// whynet's request passed (an anti-bot page), the browser's was reset: never "all fine"
		{with("http.challenge", "true", "br.status", "nav_failed", "br.error", "net::ERR_CONNECTION_RESET"), "cut_midway"},
		{with("http.challenge", "true", "br.status", "nav_failed", "br.error", "net::ERR_CONNECTION_RESET", "local.dpi_tool", "zapret", "local.dpi_listed", "false"), "dpi_not_listed"},
		{with("br.status", "nav_failed", "br.error", "net::ERR_NAME_NOT_RESOLVED"), "unknown"},
		// a dead system proxy: the direct way works, the browser can't get out (was "unknown")
		{with("local.proxy", "on", "br.status", "nav_failed", "br.error", "net::ERR_PROXY_CONNECTION_FAILED"), "proxy_in_way"},
		{with("local.proxy", "on", "tls.intercepted", "true"), "https_intercepted"},
		// the page shows late or never; a blocked content server explains more and wins
		{with("br.status", "ok", "br.slow_load", "true", "br.dom_s", "12.4"), "page_slow"},
		{with("br.status", "ok", "br.loaded", "false", "br.dom_s", "20+"), "page_slow"},
		{with("br.loaded", "false", "res.worst", "sni"), "content_blocked"},
		// the site's own block page outranks timings: it already says why
		{with("http.challenge", "true", "br.antibot", "true", "br.slow_load", "true"), "bot_check"},
		{with("br.blocked_page", "true", "br.page_title", "Похоже, нет соединения", "br.visible", "true"), "site_blocks_you"},
		// a plain window loaded the page: whynet's own request meeting an anti-bot check doesn't matter
		{with("http.class", "4xx", "http.status", "403", "http.challenge", "true", "br.visible", "true", "br.status", "ok", "br.requests", "903", "br.failed", "3"), "browser_ok"},
	}
	for _, c := range cases {
		if got := rules.Match(rs, c.facts); got.ID != c.want {
			t.Errorf("%v: got %s, want %s", c.facts, got.ID, c.want)
		}
	}
}

// The built-in ad list catches ads and spares content servers seen in real checks.
func TestAdList(t *testing.T) {
	_, m, err := rules.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.AdSet) < 10000 {
		t.Fatalf("ad list has %d domains", len(m.AdSet))
	}
	for _, ad := range []string{"securepubads.g.doubleclick.net", "mc.yandex.ru", "www.googletagmanager.com", "x.top-fwz1.mail.ru"} {
		if !m.IsAd(ad) {
			t.Errorf("%s should be an ad server", ad)
		}
	}
	for _, content := range []string{"i.ytimg.com", "fonts.gstatic.com", "yastatic.net", "habrastorage.org", "cdn2.ozone.ru", "redirector.googlevideo.com", "yandex.ru"} {
		if m.IsAd(content) {
			t.Errorf("%s is content, not ads", content)
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
	known := regexp.MustCompile(`^(host|(local|dns|tcp|tls|http|br|res)\.[a-z_]+)$`)
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
