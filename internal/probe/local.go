package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// hostsLines returns the hosts-file lines that name host.
func hostsLines(content, host string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		l, _, _ := strings.Cut(line, "#")
		f := strings.Fields(l)
		for _, name := range f[min(1, len(f)):] {
			if strings.EqualFold(name, host) {
				out = append(out, strings.TrimSpace(line))
				break
			}
		}
	}
	return out
}

// parseTrace reads Cloudflare's /cdn-cgi/trace "key=value" lines.
func parseTrace(body string) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	return kv
}

// maskIP keeps the network part of a public IPv4 address: enough to tell the ISP, not the person.
func maskIP(ip string) string {
	if p := strings.Split(ip, "."); len(p) == 4 {
		return p[0] + "." + p[1] + ".x.x"
	}
	return "[hidden-ip]"
}

// publicInfo asks Cloudflare who we look like from outside, then the AS name via Team Cymru over DoH.
// It also returns the server's clock for the skew check.
func publicInfo(ctx context.Context) (info map[string]string, date time.Time, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://1.1.1.1/cdn-cgi/trace", nil)
	resp, err := direct.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	resp.Body.Close()
	date, _ = http.ParseTime(resp.Header.Get("Date"))
	kv := parseTrace(string(body))
	info = map[string]string{"ip": kv["ip"], "country": kv["loc"], "cloudflare_warp": kv["warp"]}
	if ip := net.ParseIP(kv["ip"]).To4(); ip != nil {
		rev := fmt.Sprintf("%d.%d.%d.%d", ip[3], ip[2], ip[1], ip[0])
		if _, txt, err := dohQuery(ctx, rev+".origin.asn.cymru.com", "TXT"); err == nil && len(txt) > 0 {
			// "12389 | 95.24.0.0/14 | RU | ripencc | 2001-06-08"
			asn := strings.TrimSpace(strings.Split(txt[0], "|")[0])
			info["asn"] = "AS" + asn
			if _, txt, err := dohQuery(ctx, "AS"+asn+".asn.cymru.com", "TXT"); err == nil && len(txt) > 0 {
				// "12389 | RU | ripencc | 2001-06-08 | ROSTELECOM-AS, RU"
				f := strings.Split(txt[0], "|")
				info["isp"] = strings.TrimSpace(f[len(f)-1])
			}
		}
	}
	return info, date, nil
}

// serverDate is the clock fallback when the trace endpoint is unreachable.
func serverDate(ctx context.Context, u string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "HEAD", u, nil)
	resp, err := direct.Do(req)
	if err != nil {
		return time.Time{}, err
	}
	resp.Body.Close()
	return http.ParseTime(resp.Header.Get("Date"))
}
