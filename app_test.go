package main

import (
	"fmt"
	"strings"
	"testing"

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
