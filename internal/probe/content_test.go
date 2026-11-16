package probe

import (
	"encoding/json"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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

// A tiny NetLog: the browser's own request is dropped, the page's requests are counted.
const testNetlog = `{"constants":{"logEventTypes":{"REQUEST_ALIVE":2,"URL_REQUEST_START_JOB":136,"HTTP_TRANSACTION_READ_RESPONSE_HEADERS":245,"URL_REQUEST_JOB_FILTERED_BYTES_READ":150},"logSourceType":{"URL_REQUEST":1},"netError":{"ERR_CONNECTION_RESET":-101,"ERR_ABORTED":-3}},
"events": [
{"params":{"url":"https://edge.microsoft.com/x","network_isolation_key":"null null"},"phase":1,"source":{"id":1,"type":1},"time":"1000","type":136},
{"params":{"url":"https://ozon.ru/","network_isolation_key":"https://ozon.ru https://ozon.ru","request_type":"main frame"},"phase":1,"source":{"id":2,"type":1},"time":"1000","type":136},
{"params":{},"phase":0,"source":{"id":2,"type":1},"time":"1100","type":245},
{"params":{"byte_count":5000},"phase":0,"source":{"id":2,"type":1},"time":"1200","type":150},
{"params":{},"phase":2,"source":{"id":2,"type":1},"time":"1300","type":2},
{"params":{"url":"https://cdn.ozone.ru/a.js","network_isolation_key":"https://ozon.ru https://ozon.ru"},"phase":1,"source":{"id":3,"type":1},"time":"1400","type":136},
{"params":{"net_error":-101},"phase":2,"source":{"id":3,"type":1},"time":"1500","type":2},
{"params":{"url":"https://img.ozone.ru/b.png","network_isolation_key":"https://ozon.ru https://ozon.ru"},"phase":1,"source":{"id":4,"type":1},"time":"1600","type":136},
{"params":{"net_error":-3},"phase":2,"source":{"id":4,"type":1},"time":"1700","type":2},
{"params":{"url":"https://hung.ozone.ru/c.js","network_isolation_key":"https://ozon.ru https://ozon.ru"},"phase":1,"source":{"id":5,"type":1},"time":"2000","type":136},
{"params":{},"phase":1,"source":{"id":9,"type":1},"time":"9000","type":2},
`

func TestNetlogParser(t *testing.T) {
	n := newNetlogParser("www.ozon.ru")
	for _, l := range strings.Split(testNetlog, "\n") {
		n.line([]byte(l))
	}
	r := n.agg.result()
	if r.Requests != 4 || r.Failed != 2 {
		t.Fatalf("requests %d failed %d, want 4 and 2 (edge.microsoft.com dropped, the aborted one not a failure)", r.Requests, r.Failed)
	}
	if h := hostOf(r, "cdn.ozone.ru"); h == nil || h.Errors["net::ERR_CONNECTION_RESET"] != 1 {
		t.Errorf("reset not recorded: %+v", h)
	}
	if h := hostOf(r, "hung.ozone.ru"); h == nil || h.Errors[hungError] != 1 {
		t.Errorf("a script silent for 7 s must count as timed out: %+v", h)
	}
	if h := hostOf(r, "ozon.ru"); h == nil || h.Bytes != 5000 || h.Failed != 0 {
		t.Errorf("main page: %+v", h)
	}
	if hostOf(r, "edge.microsoft.com") != nil {
		t.Error("the browser's own traffic leaked into the page's servers")
	}
}

// The browser appends to its log while it runs; lines cut mid-way must wait for their end.
func TestNetlogTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "net.json")
	cut := strings.Index(testNetlog, `cdn.ozone.ru`) // mid-line: the third request is cut in half
	os.WriteFile(path, []byte(testNetlog[:cut]), 0o644)
	tail := &netlogTail{path: path, p: newNetlogParser("ozon.ru")}
	tail.read()
	if n := len(tail.p.agg.order); n != 1 {
		t.Fatalf("after the first half: %d requests, want 1", n)
	}
	os.WriteFile(path, []byte(testNetlog), 0o644)
	tail.read()
	if n := len(tail.p.agg.order); n != 4 {
		t.Errorf("after the rest: %d requests, want 4", n)
	}
	if got := pageTitle("Ozon маркетплейс — Профиль 1: Microsoft Edge"); got != "Ozon маркетплейс" {
		t.Errorf("pageTitle = %q", got)
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

// Which files came late, and where their time went (DevTools timings, ms after requestTime).
func TestSlowFiles(t *testing.T) {
	a := newAgg()
	ev := func(method string, p map[string]any) {
		b, _ := json.Marshal(p)
		a.event(method, b)
	}
	req := func(id, typ, u string, ts float64) {
		ev("Network.requestWillBeSent", map[string]any{"requestId": id, "timestamp": ts, "type": typ, "request": map[string]any{"url": u}})
	}
	answer := func(id string, ts float64, timing map[string]float64) {
		tm := map[string]float64{"proxyStart": -1, "dnsStart": -1, "dnsEnd": -1, "connectStart": -1, "connectEnd": -1, "sslStart": -1, "sslEnd": -1}
		maps.Copy(tm, timing)
		ev("Network.responseReceived", map[string]any{"requestId": id, "timestamp": ts, "response": map[string]any{"remoteIPAddress": "10.0.0.7", "timing": tm}})
	}
	done := func(id string, ts float64) {
		ev("Network.loadingFinished", map[string]any{"requestId": id, "timestamp": ts, "encodedDataLength": 1000})
	}
	// A: the connect took 1.5 s (a resent packet), 3.0 s in all.
	req("A", "Image", "https://cdn.example/a.jpg?sig=secret", 1)
	answer("A", 2.7, map[string]float64{"requestTime": 1, "connectStart": 0, "sslStart": 1500, "sslEnd": 1550, "connectEnd": 1550, "sendStart": 1551, "sendEnd": 1552, "receiveHeadersEnd": 1700})
	done("A", 4)
	// B: same server, waited for A's connection (HTTP/2): its own timing shows only the wait.
	req("B", "Image", "https://cdn.example/b.jpg", 1)
	answer("B", 2.7, map[string]float64{"requestTime": 1, "sendStart": 1555, "sendEnd": 1556, "receiveHeadersEnd": 1700})
	done("B", 4.2)
	// C: the server thought for 3 s.
	req("C", "Script", "https://api.example/app.js", 2)
	answer("C", 5, map[string]float64{"requestTime": 2, "dnsStart": 0, "dnsEnd": 5, "connectStart": 5, "sslStart": 10, "sslEnd": 20, "connectEnd": 20, "sendStart": 20, "sendEnd": 21, "receiveHeadersEnd": 3021})
	done("C", 5.1)
	req("D", "Image", "https://h.example/stuck.jpg", 2) // never answered
	req("E", "Image", "https://cdn.example/fast.jpg", 1)
	done("E", 1.2)
	req("F", "Media", "https://v.example/video", 1) // a stream: long by design
	answer("F", 1.1, map[string]float64{"requestTime": 1, "sendStart": 0, "sendEnd": 1, "receiveHeadersEnd": 50})
	done("F", 11)
	req("G", "XHR", "https://api.example/poll", 1) // a slow API call is not a missing block
	done("G", 12)
	req("H", "Image", "https://r.example/x.png", 1)
	ev("Network.loadingFailed", map[string]any{"requestId": "H", "timestamp": 1.5, "errorText": "net::ERR_CONNECTION_RESET"})
	req("I", "Image", "https://cdn.example/carousel.jpg", 1) // the page swapped it out after 5 s
	ev("Network.loadingFailed", map[string]any{"requestId": "I", "timestamp": 6, "errorText": "net::ERR_ABORTED", "canceled": true})

	got := map[string]*SlowFile{}
	var order []string
	for _, f := range a.result().Slow {
		got[f.URL] = f
		order = append(order, f.URL)
	}
	want := []string{"https://h.example/stuck.jpg", "https://r.example/x.png", // failed first
		"https://cdn.example/b.jpg", "https://api.example/app.js", "https://cdn.example/a.jpg"}
	if !slices.Equal(order, want) {
		t.Fatalf("slow files %v, want %v", order, want)
	}
	for u, w := range map[string]string{"https://cdn.example/a.jpg": "connect", "https://cdn.example/b.jpg": "connect",
		"https://api.example/app.js": "wait", "https://h.example/stuck.jpg": "none", "https://r.example/x.png": ""} {
		if got[u].Phase != w {
			t.Errorf("%s: phase %q, want %q (phases %v)", u, got[u].Phase, w, got[u].Phases)
		}
	}
	if f := got["https://cdn.example/a.jpg"]; f.Ms != 3000 || f.IP != "10.0.0.7" || f.Kind != "image" ||
		!maps.Equal(f.Phases, map[string]int64{"connect": 1500, "tls": 50, "wait": 148, "download": 1300}) {
		t.Errorf("a.jpg: %+v", f)
	}
	if f := got["https://h.example/stuck.jpg"]; !f.Unfinished || f.Error != hungError || f.Ms != 10000 {
		t.Errorf("stuck.jpg: %+v", f)
	}
	if got := shortURL("https://i.example.com/236x/0a/1b/2c/0a1b2c3d4e5f60718293a4b5c6d7e8f9-very-long-name.jpg"); got != "i.example.com/236x/0a/1b/2c/0a1b2c3d4e5f60…d7e8f9-very-long-name.jpg" {
		t.Errorf("shortURL = %s", got)
	}
}

// A try a second slower than the best one had a packet resent.
func TestPingStats(t *testing.T) {
	for _, c := range []struct {
		times []int64
		ping  int64
		lost  int
	}{
		{[]int64{7, 1017, 1009, 6, 1006}, 7, 3}, // privacy-cs.mail.ru, live
		{[]int64{7, 7, 6, 7, 6}, 7, 0},
		{[]int64{90, 130, 950, 85, 100}, 100, 1}, // TLS through a fake-ip router
		{[]int64{3000, 3010, 2990}, 3000, 0},     // far, but steady
	} {
		if ping, lost := pingStats(c.times); ping != c.ping || lost != c.lost {
			t.Errorf("pingStats(%v) = %d, %d; want %d, %d", c.times, ping, lost, c.ping, c.lost)
		}
	}
}
