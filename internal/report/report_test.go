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
			"local.dpi_tool": "none", "local.dpi_installed": "true", "local.windivert": "running", "local.dpi_listed": "true"},
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
		"- Обход блокировок: запущен: ничего; драйвер WinDivert: работает; сайт в списках запрета: да"} {
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
