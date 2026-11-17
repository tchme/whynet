package report

import (
	"strings"
	"testing"
	"time"

	"whynet/internal/health"
	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

func TestHealthMarkdown(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rules.LoadHealth()
	if err != nil {
		t.Fatal(err)
	}
	res := &health.Result{Started: time.Date(2026, 11, 17, 22, 0, 0, 0, time.UTC), Mask: map[string]string{"95.24.1.2": "95.24.x.x"},
		Steps: []probe.Step{
			{ID: "health_address", Facts: map[string]string{"local.net": "ok"}, Evidence: map[string]any{
				"os": "Windows 11 Pro", "public": map[string]string{"ip": "95.24.1.2", "isp": "Beeline", "asn": "AS8402", "country": "RU"}}},
			{ID: "health_proxy", Facts: map[string]string{"health.proxy": "dead", "health.proxy_server": "127.0.0.1:10809"}},
		}}
	finds := rules.MatchAll(rs, res.Facts())
	p := health.MakePlan(res, []string{"proxy_off"})
	md := HealthMarkdown(res, finds, []HealthBatch{{Outcomes: []health.Outcome{{Action: p.Actions[0], OK: true}}}}, nil, nil, tx, true)
	for _, want := range []string{"127.0.0.1:10809", "health_proxy_dead", "Beeline", "95.24.x.x", "Выключу прокси 127.0.0.1:10809 — ✓"} {
		if !strings.Contains(md, want) {
			t.Errorf("no %q in the report", want)
		}
	}
	if strings.Contains(md, "95.24.1.2") || strings.Contains(md, "{health.") {
		t.Error("a private value or an unfilled placeholder in the report")
	}
	if AreaStatus(finds)["health_proxy"] != "fail" {
		t.Error("a problem makes its area fail")
	}
}

// Every action has words in ru.yaml, never the raw id.
func TestHealthActionTexts(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []health.Action{
		{ID: "proxy_enable", Value: "0"}, {ID: "proxy_enable", Value: "1"}, {ID: "pac_set"}, {ID: "pac_set", Value: "http://x/p.pac"},
		{ID: "winhttp_set"}, {ID: "winhttp_set", Value: "10.0.0.1:3128"}, {ID: "hosts_comment", Lines: []string{"a"}},
		{ID: "hosts_uncomment", Lines: []string{"a"}}, {ID: "dns_source", Value: "dhcp", Adapter: "Ethernet"},
		{ID: "dns_source", Value: "static", Values: []string{"1.1.1.1"}, Adapter: "Ethernet"}, {ID: "service_start", Value: "Dnscache"},
		{ID: "service_stop", Value: "W32Time"}, {ID: "tcp_global", Value: "autotuninglevel=normal"}, {ID: "tcp_global", Value: "rss=enabled"},
		{ID: "tcp_global", Value: "rss=disabled"}, {ID: "dhcp_renew", Adapter: "Ethernet"}, {ID: "time_resync"}, {ID: "flush_dns"}, {ID: "flush_arp"},
	} {
		got := HealthAction(tx, a, map[string]string{})
		if strings.HasPrefix(got, a.ID) || strings.Contains(got, "{") {
			t.Errorf("%+v: %q", a, got)
		}
	}
}
