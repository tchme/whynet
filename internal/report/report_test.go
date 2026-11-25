package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"whynet/internal/probe"
	"whynet/locales"
	"whynet/rules"
)

func TestMarkdownHidesSiteURLDetails(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	res := &probe.Result{Host: "example.com", Steps: []probe.Step{
		{ID: "http", Status: "warn", Facts: map[string]string{"http.status": "307"}, Evidence: map[string]any{
			"redirects": []map[string]any{
				{"status": 302, "to": "https://redirect-user:redirect-pass@first.example/one?key=first-query#first-fragment"},
				{"status": 307, "to": "http://second.example/two?key=second-query"},
			},
			"final_url": "https://first.example/final?key=final-query#final-fragment",
			"headers":   map[string]string{"Location": "/relative?key=relative-query#relative-fragment"},
			"error":     `Get "https://example.com/error?key=error-query": failed to parse Location header "../%oops?key=invalid-location": parse "../%oops?key=invalid-location": invalid URL escape "%oo"`,
		}},
		{ID: "browser", Status: "fail", Facts: map[string]string{"br.error": "failed on https://example.com/browser?key=browser-query"},
			Evidence: map[string]any{"browser": probe.BrowserResult{Error: "failed on https://example.com/browser?key=browser-query"}}},
		{ID: "res", Status: "warn", Evidence: map[string]any{"hosts": []*probe.HostCheck{{Host: "cdn.example", Result: "ip",
			Detail: `Get "https://cdn.example/asset?key=resource-query": timeout`, Sample: "https://cdn.example/asset"}}}},
		{ID: "own", Status: "warn", Evidence: map[string]any{"record": &probe.OwnRecord{Site: "example.com", PageURL: "https://example.com/page", Files: []*probe.OwnFile{
			{URL: "https://img.example/image.png", Kind: "image", Error: "net::ERR_FAILED https://img.example/image.png?key=har-query"},
		}}}},
	}}
	for _, scheme := range []string{"", "https://"} {
		t.Run("input="+scheme, func(t *testing.T) {
			res.Input = scheme + "input-user:input-password@example.com/path?key=input-query#input-fragment"
			before, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			v := rules.Verdict{ID: "unknown", Confidence: "likely"}
			hidden, shown := Markdown(res, v, tx, true), Markdown(res, v, tx, false)
			for _, private := range []string{"input-user", "input-password", "input-query", "input-fragment", "redirect-user", "redirect-pass",
				"first-query", "first-fragment", "second-query", "final-query", "final-fragment", "relative-query", "relative-fragment",
				"error-query", "invalid-location", "browser-query", "resource-query", "har-query"} {
				if strings.Contains(hidden, private) {
					t.Errorf("masked report leaks %q", private)
				}
				if !strings.Contains(shown, private) {
					t.Errorf("unmasked report lost %q", private)
				}
			}
			for _, want := range []string{scheme + "example.com/path", "https://first.example/one", "http://second.example/two", "https://first.example/final",
				`"Location": "/relative"`, "../%oops", "https://example.com/error", "https://example.com/browser", "https://cdn.example/asset",
				"https://img.example/image.png", "`http.status`: 307", `"status": 302`, `"status": 307`} {
				if !strings.Contains(hidden, want) {
					t.Errorf("masked report lost %q", want)
				}
			}
			_, raw, _ := strings.Cut(hidden, "````json\n")
			raw, _, _ = strings.Cut(raw, "````\n")
			if !json.Valid([]byte(raw)) {
				t.Error("masked report contains invalid JSON")
			}
			after, err := json.Marshal(res)
			if err != nil || string(after) != string(before) {
				t.Error("rendering changed raw site evidence")
			}
		})
	}
	res.Steps[0].Evidence["headers"] = map[string]string{"Location": "//location-user:location-pass@hop.example/end?key=location-query"}
	if hidden := Markdown(res, rules.Verdict{}, tx, true); !strings.Contains(hidden, `"Location": "//hop.example/end"`) || strings.Contains(hidden, "location-pass") {
		t.Error("scheme-relative Location was not masked")
	}
}

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
		{ID: "own", Status: "warn", Facts: map[string]string{"own.blocked": "true"}, Evidence: map[string]any{"record": &probe.OwnRecord{
			Site: "x.com", Requests: 142, ShownMs: 678, Files: []*probe.OwnFile{
				{URL: "https://cmp.x.com/cmp.js", Host: "cmp.x.com", Kind: "script", What: "blocked", Error: "net::ERR_BLOCKED_BY_CLIENT",
					Whynet: &probe.OwnCompare{File: true, OK: true, Ms: 1100}},
				{URL: "https://ad.x.com/a", Host: "ad.x.com", Kind: "data", What: "blocked", Ad: true}}}}},
	}}
	md := Markdown(res, rules.Verdict{ID: "unknown", Confidence: "likely"}, tx, true)
	for _, want := range []string{"net::ERR_CONNECTION_RESET ×1", "| gone.x.com |  | — |", "| img.x.com |  | 7 (потери 3 из 5) |",
		"| https://img.x.com/a.jpg | картинки | 3200 | подключение к серверу | connect 3000 · wait 100 · download 100 |  |",
		"### 3. Запись твоего браузера " + tx.Status["warn"] + "\n", // no "(0 мс)": a record has no time of its own
		"Сайт записи: x.com · запросов: 142 · страница показалась через 678 мс",
		"| https://cmp.x.com/cmp.js | скрипты | blocked | 0 |  | net::ERR_BLOCKED_BY_CLIENT |  | file: ok=true, 1100 ms |",
		"| https://ad.x.com/a | данные для скриптов | blocked | 0 |  |  | да | — |"} {
		if !strings.Contains(md, want) {
			t.Errorf("report lacks %q", want)
		}
	}
	if strings.ContainsAny(md, "Ѓ") || strings.Contains(md, "Г—") || strings.Contains(md, "вЂ") {
		t.Error("report has mojibake")
	}
}

func TestAppMarkdown(t *testing.T) {
	tx, err := locales.Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	res := &probe.AppResult{
		Result: probe.Result{Input: "Discord.exe", Host: "Discord", Started: time.Date(2026, 11, 16, 23, 5, 0, 0, time.UTC),
			Steps: []probe.Step{
				{ID: "local", Status: "ok", Facts: map[string]string{"local.net": "ok", "local.dpi_tool": "none"},
					Evidence: map[string]any{"public": map[string]string{"ip": "95.24.13.7", "isp": "CORBINA-AS", "asn": "AS8402", "country": "RU"}}},
				{ID: "record", Status: "warn", Facts: map[string]string{"app.servers": "2", "app.cut": "1"}},
				{ID: "check", Status: "fail", Facts: map[string]string{"app.blocked": "true", "app.sni": "true", "app.blocked_hosts": "discord.gg"}},
			},
			Mask: map[string]string{"95.24.13.7": "95.24.x.x"}},
		Exe: "Discord.exe", Seconds: 23.6,
		Servers: []*probe.AppServer{
			{IP: "198.18.0.95", Port: 443, FakeIP: true, RealIP: "162.159.135.234", Conns: 4, Cut: 4, Name: "discord.gg", NameFrom: "cert",
				ASN: "AS13335", Owner: "Cloudflare", Checked: true, Result: "sni", Detail: "reset: connection reset", PingMs: 31, PingTLS: true},
			{IP: "149.154.167.51", Port: 443, Conns: 2, Open: 2, Checked: true, Result: "open", PingMs: 40, Lost: 3, Tries: 5, DPIName: "false", DPIIP: "true"},
		},
	}
	v := rules.Verdict{ID: "app_blocked_name", Confidence: "high"}
	md := AppMarkdown(res, v, tx, true)
	for _, want := range []string{tx.App["ai_prompt"], "- Программа: Discord (`Discord.exe`)", "записано 24 с",
		tx.AppDiagnoses["app_blocked_name"].Title, "— discord.gg — отвечают", "95.24.x.x",
		"| discord.gg | сертификат сервера | 198.18.0.95:443; через прокси роутера (fake-ip), настоящий адрес 162.159.135.234 | AS13335 Cloudflare | соединений 4: оборвано сразу 4",
		"| — | имя неизвестно | 149.154.167.51:443 |", "потери 3 из 5", "имя в списках: false; адрес в ipset: true; порт в Game Filter: нет",
		"### 2. Запись соединений программы", "````json"} {
		if !strings.Contains(md, want) {
			t.Errorf("app report lacks %q", want)
		}
	}
	for _, bad := range []string{"95.24.13.7", "Г—", "вЂ”", "{app."} {
		if strings.Contains(md, bad) {
			t.Errorf("app report contains %q", bad)
		}
	}
	if got := AppFileName(res); got != "whynet-app-Discord-2026-11-16-2305.md" {
		t.Errorf("AppFileName = %s", got)
	}
}
