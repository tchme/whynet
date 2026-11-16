package report

import (
	"strings"
	"testing"
	"time"

	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

func TestMarkdownHidesPrivateValues(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	res := &probe.Result{
		Input: "youtube.com", Host: "youtube.com",
		Started: time.Date(2026, 11, 15, 21, 4, 0, 0, time.UTC),
		Steps: []probe.Step{{ID: "local", Status: "ok", Facts: map[string]string{"local.net": "ok",
			"local.dpi_tool": "none", "local.dpi_installed": "true", "local.windivert": "running", "local.dpi_listed": "true",
			"local.dpi_ipset_filter": "none", "local.dpi_game_filter": "all"},
			Evidence: map[string]any{"public": map[string]string{"ip": "95.24.13.7", "isp": "ROSTELECOM-AS, RU"}, "error": `open C:\Users\ivan\x`}}},
		Mask: map[string]string{"95.24.13.7": "95.24.x.x", "ivan": "[user-name]"},
	}
	v := rules.Verdict{ID: "all_ok", Confidence: "high"}

	hidden := Markdown(res, v, tx, true)
	for _, private := range []string{"95.24.13.7", "ivan"} {
		if strings.Contains(hidden, private) {
			t.Errorf("hidden report leaks %q", private)
		}
	}
	for _, want := range []string{"95.24.x.x", "ROSTELECOM-AS", tx.Diagnoses["all_ok"].Title, tx.Report["ai_prompt"], "````json", "`local.net`: ok", "- Провайдер: ROSTELECOM-AS, RU",
		"- Обход блокировок: запущен: ничего; драйвер WinDivert: работает; сайт в списках запрета: да; адрес в IP-списках: неизвестно",
		"- Настройки Flowseal: IPSet Filter [none], Game Filter [all]"} {
		if !strings.Contains(hidden, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if shown := Markdown(res, v, tx, false); !strings.Contains(shown, "95.24.13.7") {
		t.Error("report with hide=false lost the public IP")
	}
	if got := FileName(res); got != "whynet-youtube.com-2026-11-15-2104.md" {
		t.Errorf("FileName = %s", got)
	}
}

// The tables carry "×" and "—" as they are: a cp1251 round trip once turned them into "Г—" and "вЂ”".
func TestMarkdownSymbols(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	res := &probe.Result{Input: "x.com", Host: "x.com", Steps: []probe.Step{
		{ID: "browser", Status: "warn", Evidence: map[string]any{"browser": probe.BrowserResult{Status: "ok",
			Hosts: []*probe.BrowserHost{{Host: "cdn.x.com", Requests: 1, Failed: 1, Errors: map[string]int{"net::ERR_CONNECTION_RESET": 1}}},
			Slow: []*probe.SlowFile{{URL: "https://img.x.com/a.jpg", Host: "img.x.com", Kind: "image", Ms: 3200, Phase: "connect",
				Phases: map[string]int64{"download": 100, "connect": 3000, "wait": 100}}}}}},
		{ID: "res", Status: "ok", Evidence: map[string]any{"hosts": []*probe.HostCheck{{Host: "gone.x.com", Result: "gone"},
			{Host: "img.x.com", Result: "ok", PingMs: 7, Lost: 3, Tries: 5}}}},
	}}
	md := Markdown(res, rules.Verdict{ID: "unknown", Confidence: "likely"}, tx, true)
	for _, want := range []string{"net::ERR_CONNECTION_RESET ×1", "| gone.x.com |  | — |", "| img.x.com |  | 7 (потери 3 из 5) |",
		"| https://img.x.com/a.jpg | картинки | 3200 | подключение к серверу | connect 3000 · wait 100 · download 100 |  |"} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.ContainsAny(md, "Ѓ") || strings.Contains(md, "Г—") || strings.Contains(md, "вЂ") {
		t.Error("report has mojibake")
	}
}
