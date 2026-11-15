package locales

import (
	"strings"
	"testing"
)

func TestNote(t *testing.T) {
	tx, err := Load("ru")
	if err != nil {
		t.Fatal(err)
	}
	got := tx.Note(map[string]string{"tls.status": "reset", "tls.control": "ok"}, true)
	if want := "Соединение сбросили сразу после имени сайта. С нейтральным именем тот же сервер ответил."; got != want {
		t.Errorf("Note = %q, want %q", got, want)
	}
	if got := tx.Note(map[string]string{"http.class": "5xx", "http.status": "502"}, true); got != "Сервер ответил ошибкой 502." {
		t.Errorf("Note fills placeholders: got %q", got)
	}
	if got := tx.Note(map[string]string{"tls.status": "ok"}, true); got != "" {
		t.Errorf("Note for a healthy step = %q, want empty", got)
	}
	// info notes show on a healthy step, problem notes don't; codes become names
	via := map[string]string{"http.via_vpn": "true", "http.seen_country": "DE", "http.seen_colo": "FRA", "http.class": "5xx", "http.status": "502"}
	if got, want := tx.Note(via, false), "Сайт видит тебя из Германии (узел Cloudflare — Франкфурт (FRA)): к нему ты идёшь через VPN или прокси."; got != want {
		t.Errorf("info note = %q, want %q", got, want)
	}
	if tx.Countries["NO"] != "Норвегии" || tx.Named(map[string]string{"x_country": "ZZ"})["x_country"] != "ZZ" {
		t.Errorf("country names: NO=%q, unknown codes must stay", tx.Countries["NO"])
	}
	for _, n := range tx.Notes {
		if k, v, ok := strings.Cut(n.Fact, "="); !ok || k == "" || v == "" || n.Text == "" {
			t.Errorf("malformed note %+v", n)
		}
	}
}
