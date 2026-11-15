package probe

import (
	"encoding/json"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"testing"
)

func hostOf(r BrowserResult, host string) *BrowserHost {
	for _, h := range r.Hosts {
		if h.Host == host {
			return h
		}
	}
	return nil
}

func TestPageResources(t *testing.T) {
	base, _ := url.Parse("https://example.com/watch/1")
	html := `<html><head>
		<link rel="stylesheet" href="/s.css"><link rel="icon" href="https://img.cdn.net/fav.ico">
		<link rel="preload" as="font" href="https://fonts.cdn.net/a.woff2">
		<meta property="og:image" content="https://img.cdn.net/og.jpg">
		<script src="//js.cdn.net/app.js?v=1"></script></head><body>
		<img src="data:image/png;base64,AAAA"><img srcset="https://img.cdn.net/1.jpg 1x, https://pics.cdn.net/2.jpg 2x">
		<video poster="https://img.cdn.net/p.jpg"><source src="https://video.cdn.net/v.mp4"></video>
		<iframe src="https://player.example.org/embed"></iframe><a href="https://not-a-resource.net/">link</a>
	</body></html>`
	got := map[string][]string{}
	var order []string
	for _, h := range pageResources([]byte(html), base) {
		got[h.Host] = h.Kinds
		order = append(order, h.Host)
	}
	want := map[string][]string{
		"example.com": {"style"}, "img.cdn.net": {"image"}, "fonts.cdn.net": {"font"}, "js.cdn.net": {"script"},
		"pics.cdn.net": {"image"}, "video.cdn.net": {"media"}, "player.example.org": {"frame"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pageResources = %v, want %v", got, want)
	}
	if order[0] != "example.com" || order[1] != "img.cdn.net" {
		t.Errorf("page order lost: %v", order)
	}
	if hosts := relatedHosts("m.youtube.com", map[string][]string{"youtube.com": {"i.ytimg.com"}}); !reflect.DeepEqual(hosts, []string{"i.ytimg.com"}) {
		t.Errorf("relatedHosts for a subdomain = %v", hosts)
	}
	if hosts := relatedHosts("notyoutube.com", map[string][]string{"youtube.com": {"i.ytimg.com"}}); hosts != nil {
		t.Errorf("relatedHosts matched a look-alike: %v", hosts)
	}
}

func TestBrowserAggregation(t *testing.T) {
	a := newAgg()
	ev := func(method string, p map[string]any) {
		b, _ := json.Marshal(p)
		a.event(method, b)
	}
	req := func(id, u string, ts float64) {
		ev("Network.requestWillBeSent", map[string]any{"requestId": id, "timestamp": ts, "type": "Image", "request": map[string]any{"url": u}})
	}
	req("1", "https://a.example/x.png", 1)
	ev("Network.loadingFinished", map[string]any{"requestId": "1", "timestamp": 1.2, "encodedDataLength": 1000})
	req("2", "https://b.example/y.png?token=secret", 1)
	ev("Network.loadingFailed", map[string]any{"requestId": "2", "timestamp": 6, "errorText": "net::ERR_CONNECTION_RESET"})
	req("3", "https://a.example/cancelled", 1)
	ev("Network.loadingFailed", map[string]any{"requestId": "3", "timestamp": 1.1, "errorText": "net::ERR_ABORTED", "canceled": true})
	req("4", "data:image/png;base64,AAAA", 1)
	req("5", "https://v.example/video", 2) // 1 MB in 32 s = 32 KB/s
	ev("Network.requestWillBeSent", map[string]any{"requestId": "9", "timestamp": 2, "type": "Document", "request": map[string]any{"url": "https://d.example/"}})
	ev("Network.loadingFinished", map[string]any{"requestId": "9", "timestamp": 34, "encodedDataLength": 1 << 20}) // a slowly generated page: no speed
	ev("Network.loadingFinished", map[string]any{"requestId": "5", "timestamp": 34, "encodedDataLength": 1 << 20})
	req("6", "https://a.example/stream", 2) // answered, still downloading: fine
	ev("Network.responseReceived", map[string]any{"requestId": "6", "timestamp": 2.1})
	req("7", "https://h.example/hung", 3) // no answer for 31 s: blocked
	ev("Network.requestWillBeSent", map[string]any{"requestId": "8", "timestamp": 3, "type": "XHR", "request": map[string]any{"url": "https://h.example/longpoll"}})

	if n := a.inflight(); n != 3 {
		t.Errorf("inflight = %d, want 3", n)
	}
	r := a.result()
	if r.Requests != 8 || r.Failed != 2 { // the silent long-poll XHR is not a failure
		t.Errorf("requests %d failed %d, want 8 and 2", r.Requests, r.Failed)
	}
	if d := hostOf(r, "d.example"); d == nil || d.KBps != 0 {
		t.Errorf("a generated page must not count as network speed: %+v", d)
	}
	for _, h := range r.Hosts {
		if h.Host == "h.example" && (h.Failed != 1 || chromeErrClass(slices.Collect(maps.Keys(h.Errors))[0]) != "timeout") {
			t.Errorf("hung request not counted as a timeout: %+v", h)
		}
	}
	if b := r.Hosts[0]; b.Host != "b.example" && b.Host != "h.example" {
		t.Errorf("failed hosts must come first: %+v", r.Hosts[0])
	}
	if b := hostOf(r, "b.example"); b == nil || b.Errors["net::ERR_CONNECTION_RESET"] != 1 || b.Sample != "https://b.example/y.png" {
		t.Errorf("failed host first, sample without its query: %+v", b)
	}
	for _, h := range r.Hosts {
		if h.Host == "v.example" && h.KBps != 32 {
			t.Errorf("v.example speed = %d KB/s, want 32", h.KBps)
		}
	}
	for e, want := range map[string]string{"net::ERR_CONNECTION_RESET": "reset", "net::ERR_CONNECTION_TIMED_OUT": "timeout",
		"net::ERR_NAME_NOT_RESOLVED": "dns", "net::ERR_CERT_DATE_INVALID": "cert", "net::ERR_SSL_PROTOCOL_ERROR": "tls", "net::ERR_FAILED": "other"} {
		if got := chromeErrClass(e); got != want {
			t.Errorf("chromeErrClass(%s) = %s, want %s", e, got, want)
		}
	}
}
