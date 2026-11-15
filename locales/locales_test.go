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
	got := tx.Note(map[string]string{"tls.status": "reset", "tls.control": "ok"})
	if want := "Соединение сбросили сразу после имени сайта. С нейтральным именем тот же сервер ответил."; got != want {
		t.Errorf("Note = %q, want %q", got, want)
	}
	if got := tx.Note(map[string]string{"http.class": "5xx", "http.status": "502"}); got != "Сервер ответил ошибкой 502." {
		t.Errorf("Note fills placeholders: got %q", got)
	}
	if got := tx.Note(map[string]string{"tls.status": "ok"}); got != "" {
		t.Errorf("Note for a healthy step = %q, want empty", got)
	}
	for _, n := range tx.Notes {
		if k, v, ok := strings.Cut(n.Fact, "="); !ok || k == "" || v == "" || n.Text == "" {
			t.Errorf("malformed note %+v", n)
		}
	}
}
