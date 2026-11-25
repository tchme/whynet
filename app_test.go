package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"whynet/internal/health"
	"whynet/internal/live"
	"whynet/internal/probe"
)

// Servers the content step didn't check say why: the site itself, or the check limit.
func TestHostViewsExplainUnchecked(t *testing.T) {
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	var checks []*probe.HostCheck
	for i := range probe.MaxHosts {
		checks = append(checks, &probe.HostCheck{Host: fmt.Sprintf("s%d.example.com", i), Result: "ok"})
	}
	res := &probe.Result{Host: "example.com", Steps: []probe.Step{
		{ID: "tcp", Evidence: map[string]any{"tries": []map[string]any{{"result": "timeout", "ms": int64(6000)}, {"result": "ok", "ms": int64(9)}}}},
		{ID: "browser", Evidence: map[string]any{"browser": probe.BrowserResult{Hosts: []*probe.BrowserHost{
			{Host: "example.com", Requests: 8, Types: []string{"document", "fetch"}},
			{Host: "1.cu.example.com", Requests: 1, Types: []string{"ping"}},
			{Host: "mc.yandex.ru", Requests: 3, Types: []string{"script"}},
		}}}},
		{ID: "res", Evidence: map[string]any{"hosts": checks}},
	}}
	views := map[string]HostView{}
	for _, v := range a.hostViews(res) {
		views[v.Host] = v
	}
	if v := views["example.com"]; v.Result != a.t.UI["site_itself"] || v.PingMs != 9 || v.Kinds != "встроенные окна, данные для скриптов" {
		t.Errorf("the site itself: %+v", v)
	}
	if v := views["1.cu.example.com"]; !strings.Contains(v.Result, "20") || v.Kinds != "данные для скриптов" || v.Requests != 1 {
		t.Errorf("over the limit: %+v", v)
	}
	if v := views["mc.yandex.ru"]; !strings.HasSuffix(v.Kinds, a.t.Kinds["ads"]) {
		t.Errorf("an ad server beyond the limit: %+v", v)
	}
}

// The monitor left of the main one has negative coordinates (the author's: work area x -1920..0).
func TestKeepInside(t *testing.T) {
	for _, c := range []struct{ pos, size, lo, hi, want int32 }{
		{-1000, 1335, -1920, 0, -1335}, // grown past the right edge: moves left
		{-3427, 1335, -1920, 0, -1920}, // already off to the left: back in
		{100, 500, 0, 2560, 100},       // fits: stays
		{100, 3000, 0, 2560, 0},        // wider than the monitor: starts at its edge
	} {
		if got := keepInside(c.pos, c.size, c.lo, c.hi); got != c.want {
			t.Errorf("keepInside(%d, %d, %d, %d) = %d, want %d", c.pos, c.size, c.lo, c.hi, got, c.want)
		}
	}
}

func TestReportsFollowRequestedMode(t *testing.T) {
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	a.last = &probe.Result{Input: "https://site.example/?token=preview-secret", Host: "site.example"}
	a.lastLive = &live.Result{Input: "game.example"}
	a.lastApp = &probe.AppResult{Result: probe.Result{Host: "fixture.exe"}}
	a.health = &healthState{res: &health.Result{}}
	for _, tc := range []struct{ mode, prefix string }{
		{"health", "whynet-windows-"}, {"site", "whynet-site.example-"},
		{"app", "whynet-app-fixture.exe-"}, {"live", "whynet-stability-"}, {"site", "whynet-site.example-"},
	} {
		md, name := a.report(tc.mode, true)
		preview, err := a.Report(tc.mode, true)
		if err != nil || md == "" || md != preview || !strings.HasPrefix(name, tc.prefix) {
			t.Errorf("mode %s: filename %q, preview matches %v, error %v", tc.mode, name, preview == md, err)
		}
		if strings.Contains(md, "preview-secret") {
			t.Errorf("mode %s: masked report contains a private query", tc.mode)
		}
	}
	if md, err := a.Report("site", false); err != nil || !strings.Contains(md, "preview-secret") {
		t.Fatalf("explicit unmasked site preview did not include the original query, error %v", err)
	}
	a.lastApp = nil
	if _, err := a.Report("app", true); err == nil {
		t.Fatal("an empty tab fell back to another result")
	}
	if err := a.CopyReport("app", true); err == nil {
		t.Fatal("an empty tab tried to copy a report")
	}
}

func TestHARComparisonUsesSamePage(t *testing.T) {
	a, err := NewApp()
	if err != nil {
		t.Fatal(err)
	}
	first := &probe.Result{Input: "https://site.example/first", Host: "site.example"}
	a.last = first
	record := func(page string) []byte {
		entries := make([]map[string]any, 5)
		for i := range entries {
			entries[i] = map[string]any{"request": map[string]string{"url": page}, "response": map[string]int{"status": 200}}
		}
		out, err := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	page := "https://Site.Example:443/second?private=har-secret#fragment"
	if got, err := a.readHAR(record(page)); err != nil || !got.Recheck {
		t.Fatalf("a different page must be rechecked: %+v %v", got, err)
	}
	failure := errors.New("fixture failed")
	if _, err := a.compareHAR(func(string) (*probe.Result, error) { return nil, failure }); !errors.Is(err, failure) || a.last != first {
		t.Fatalf("failed comparison replaced the previous result: %v", err)
	}
	if _, err := a.compareHAR(func(site string) (*probe.Result, error) {
		if site != "https://site.example/second" {
			t.Fatalf("fresh check got %q", site)
		}
		return &probe.Result{Input: site, Host: "site.example"}, nil
	}); err != nil || a.last.Input != "https://site.example/second" || len(first.Steps) != 0 {
		t.Fatalf("wrong page compared: %+v, error %v", a.last, err)
	}
	if got, err := a.readHAR(record("https://site.example/second?changed=1")); err != nil || got.Recheck {
		t.Fatalf("the same sanitized page should reuse the check: %+v %v", got, err)
	}
	if _, err := a.readHAR(record("https://site.example:8443/second")); err == nil {
		t.Fatal("an unsupported HTTPS port was allowed in the comparison")
	}
}
