package probe

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// harText builds a HAR record: a Chromium one unless firefox; entries are url, type, status, _error, time, timings.
func harText(t *testing.T, firefox bool, entries ...map[string]any) []byte {
	t.Helper()
	page := map[string]any{"id": "page_1", "title": "https://dzen.ru/a/xyz?utm=secret", "pageTimings": map[string]any{"onContentLoad": 678.3}}
	creator := "WebInspector"
	if firefox {
		page["title"], creator = "MrBeast | Дзен", "Firefox"
	}
	var es []map[string]any
	for _, e := range entries {
		resp := map[string]any{"status": e["status"], "content": map[string]any{"mimeType": e["mime"]},
			"cookies": []any{map[string]any{"name": "Session_id", "value": "secret-cookie"}}}
		if e["error"] != nil {
			resp["_error"] = e["error"]
		}
		tm := map[string]any{"blocked": -1, "dns": -1, "connect": -1, "ssl": -1, "send": 0, "wait": 0, "receive": 0}
		if e["timings"] != nil {
			for k, v := range e["timings"].(map[string]any) {
				tm[k] = v
			}
		}
		x := map[string]any{"startedDateTime": "2026-11-16T13:46:43.742Z", "time": e["time"],
			"request":  map[string]any{"method": "GET", "url": e["url"], "headers": []any{map[string]any{"name": "Cookie", "value": "secret-cookie"}}},
			"response": resp, "serverIPAddress": "[2a02:6b8::1]", "timings": tm}
		if !firefox {
			x["_resourceType"] = e["type"]
		}
		es = append(es, x)
	}
	b, err := json.Marshal(map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": creator},
		"pages": []any{page}, "entries": es}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseHAR(t *testing.T) {
	m := Markers{Ads: []string{"ad.mail.ru"}}
	b := harText(t, false,
		map[string]any{"url": "https://dzen.ru/a/xyz?utm=secret", "type": "document", "status": 200, "time": 300},
		map[string]any{"url": "https://privacy-cs.mail.ru/static/cmp.js?v=1", "type": "script", "status": 0, "error": "net::ERR_BLOCKED_BY_CLIENT", "time": 1},
		map[string]any{"url": "https://ad.mail.ru/adq/", "type": "fetch", "status": 0, "error": "net::ERR_BLOCKED_BY_CLIENT", "time": 1},
		map[string]any{"url": "https://ad.mail.ru/slow.js", "type": "script", "status": 200, "time": 9000},
		map[string]any{"url": "https://avatars.dzeninfra.ru/a.jpg", "type": "image", "status": 200, "time": 4100,
			"timings": map[string]any{"blocked": 2, "connect": 3050, "ssl": 40, "send": 0, "wait": 100, "receive": 900}},
		map[string]any{"url": "https://avatars.dzeninfra.ru/b.jpg", "type": "image", "status": 200, "time": 3500, // waited for a.jpg's connection
			"timings": map[string]any{"blocked": 3100, "send": 0, "wait": 100, "receive": 300}},
		map[string]any{"url": "https://static.dzeninfra.ru/x.css", "type": "stylesheet", "status": 0, "error": "net::ERR_CONNECTION_RESET", "time": 20},
		map[string]any{"url": "https://dzen.ru/cancelled.jpg", "type": "image", "status": 0, "error": "net::ERR_ABORTED", "time": 5},
		map[string]any{"url": "https://stats.dzen.ru/beacon", "type": "ping", "status": 0, "time": 0}, // a beacon left open
		map[string]any{"url": "https://dzen.ru/fast.jpg", "type": "image", "status": 200, "time": 80},
		map[string]any{"url": "https://dzen.ru/api", "type": "xhr", "status": 502, "time": 50},
	)
	rec, err := ParseHAR(b, m)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Site != "dzen.ru" || rec.PageURL != "https://dzen.ru/a/xyz" || rec.Requests != 11 || rec.ShownMs != 678 {
		t.Errorf("record: %+v", rec)
	}
	var got []string
	for _, f := range rec.Files {
		got = append(got, f.What+" "+f.URL+" "+f.Phase)
	}
	want := []string{
		"blocked https://privacy-cs.mail.ru/static/cmp.js ", // the page's own server stopped: first
		"failed https://dzen.ru/api ",                       // the longer failure first
		"failed https://static.dzeninfra.ru/x.css ",
		"late https://avatars.dzeninfra.ru/a.jpg connect",
		"late https://avatars.dzeninfra.ru/b.jpg connect", // its own timing says "queue"
		"late https://ad.mail.ru/slow.js ",                // ads last
		"blocked https://ad.mail.ru/adq/ ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if f := rec.Files[0]; f.IP != "2a02:6b8::1" || f.Kind != "script" || f.Ad {
		t.Errorf("blocked file: %+v", f)
	}
	if f := rec.Files[3]; f.Phases["connect"] != 3010 || f.Phases["tls"] != 40 || f.Phases["download"] != 900 {
		t.Errorf("late phases: %v", f.Phases)
	}
	if out, _ := json.Marshal(rec); strings.Contains(string(out), "secret") {
		t.Errorf("the record kept a cookie or a query: %s", out)
	}

	// Firefox: the page title is not its address, no resource types: the first request and the MIME type tell.
	ff, err := ParseHAR(harText(t, true,
		map[string]any{"url": "https://dzen.ru/a/xyz", "status": 200, "time": 300, "mime": "text/html"},
		map[string]any{"url": "https://avatars.dzeninfra.ru/c", "status": 0, "time": 0, "mime": "image/webp"},
	), m)
	if err != nil || ff.Site != "dzen.ru" || len(ff.Files) != 1 || ff.Files[0].Kind != "image" || ff.Files[0].What != "none" {
		t.Errorf("firefox record: %+v %v", ff, err)
	}

	for _, c := range []struct {
		text string
		want error
	}{
		{"просто текст", ErrNotHAR},
		{`{"foo": 1}`, ErrNotHAR},
		{string(harText(t, false)), ErrEmptyHAR},
		{string(harText(t, false, map[string]any{"url": "https://static.dzeninfra.ru/x.js", "type": "xhr", "status": 200, "time": 246})), ErrOneRequest}, // the author's first try
	} {
		if _, err := ParseHAR([]byte(c.text), m); !errors.Is(err, c.want) {
			t.Errorf("%.30s: %v, want %v", c.text, err, c.want)
		}
	}
}

// The record against whynet's own browser: the same file, else the same server.
func TestOwnStep(t *testing.T) {
	m := Markers{Ads: []string{"ad.mail.ru"}}
	rec, err := ParseHAR(harText(t, false,
		map[string]any{"url": "https://dzen.ru/a/xyz", "type": "document", "status": 200, "time": 300},
		map[string]any{"url": "https://privacy-cs.mail.ru/static/cmp.js", "type": "script", "status": 0, "error": "net::ERR_BLOCKED_BY_CLIENT", "time": 1},
		map[string]any{"url": "https://ad.mail.ru/adq/", "type": "fetch", "status": 0, "error": "net::ERR_BLOCKED_BY_CLIENT", "time": 1},
		map[string]any{"url": "https://avatars.dzeninfra.ru/a.jpg", "type": "image", "status": 200, "time": 4100,
			"timings": map[string]any{"connect": 3050, "ssl": 40, "wait": 100, "receive": 900}},
	), m)
	if err != nil {
		t.Fatal(err)
	}
	br := BrowserResult{Status: "ok", Hosts: []*BrowserHost{{Host: "avatars.dzeninfra.ru", Requests: 5, Failed: 1}},
		files: map[string]fileSeen{"https://privacy-cs.mail.ru/static/cmp.js": {ok: true, ms: 1100}}}
	res := &Result{Host: "dzen.ru", Steps: []Step{{ID: "browser", Evidence: map[string]any{"browser": br}}}}
	s := OwnStep(rec, res)
	for k, v := range map[string]string{"own.site": "dzen.ru", "own.requests": "4", "own.blocked": "true", "own.blocked_n": "2",
		"own.blocked_hosts": "privacy-cs.mail.ru, ad.mail.ru", "own.blocked_site": "true", "own.blocked_site_hosts": "privacy-cs.mail.ru",
		"own.late": "true", "own.late_hosts": "avatars.dzeninfra.ru", "own.late_file": "avatars.dzeninfra.ru/a.jpg", "own.late_s": "4.1",
		"own.late_phase": "connect", "own.failed": "", "own.clean": ""} {
		if s.Facts[k] != v {
			t.Errorf("%s = %q, want %q", k, s.Facts[k], v)
		}
	}
	byURL := map[string]*OwnFile{}
	for _, f := range rec.Files {
		byURL[f.URL] = f
	}
	if w := byURL["https://privacy-cs.mail.ru/static/cmp.js"].Whynet; w == nil || !w.File || !w.OK || w.Ms != 1100 {
		t.Errorf("same file: %+v", w)
	}
	if w := byURL["https://avatars.dzeninfra.ru/a.jpg"].Whynet; w == nil || w.File || !w.OK || w.Loaded != 4 || w.Requests != 5 {
		t.Errorf("same server: %+v", w)
	}
	if w := byURL["https://ad.mail.ru/adq/"].Whynet; w != nil {
		t.Errorf("a server whynet never used: %+v", w)
	}
	if s.Status != "warn" {
		t.Errorf("status %s", s.Status)
	}

	clean, _ := ParseHAR(harText(t, false,
		map[string]any{"url": "https://dzen.ru/a/xyz", "type": "document", "status": 200, "time": 300},
		map[string]any{"url": "https://dzen.ru/b.jpg", "type": "image", "status": 200, "time": 30},
	), m)
	if s := OwnStep(clean, res); s.Facts["own.clean"] != "true" || s.Status != "ok" {
		t.Errorf("clean record: %v %s", s.Facts, s.Status)
	}
	if !SameSite("m.dzen.ru", "dzen.ru") || SameSite("dzen.ru", "pinterest.com") {
		t.Error("SameSite")
	}
}
