package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
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
	if asn, name := ASN(ctx, kv["ip"]); asn != "" {
		info["asn"], info["isp"] = asn, name
	}
	return info, date, nil
}

// ASN names the network an IPv4 address belongs to ("AS12389", "ROSTELECOM-AS, RU") through
// Team Cymru's DNS records, asked over DoH; "" for private addresses or when unknown.
func ASN(ctx context.Context, ip string) (asn, name string) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || cgnat.Contains(a) {
		return "", ""
	}
	b := a.As4()
	rev := fmt.Sprintf("%d.%d.%d.%d", b[3], b[2], b[1], b[0])
	_, txt, err := dohQuery(ctx, rev+".origin.asn.cymru.com", "TXT")
	if err != nil || len(txt) == 0 {
		return "", ""
	}
	num := originASN(txt[0])
	if num == "" {
		return "", ""
	}
	asn = "AS" + num
	if _, txt, err := dohQuery(ctx, asn+".asn.cymru.com", "TXT"); err == nil && len(txt) > 0 {
		// "12389 | RU | ripencc | 2001-06-08 | ROSTELECOM-AS, RU"
		f := strings.Split(txt[0], "|")
		name = strings.TrimSpace(f[len(f)-1])
	}
	return asn, name
}

// originASN reads the AS number from a Team Cymru origin record: "12389 | 95.24.0.0/14 | RU | ...".
// A prefix announced by several networks lists them all ("13238 208398 | 77.88.8.0/24 | ..."):
// the first one names it.
func originASN(txt string) string {
	if nums := strings.Fields(strings.Split(txt, "|")[0]); len(nums) > 0 {
		return nums[0]
	}
	return ""
}

// cgnat is the carrier-grade NAT range: an ISP's own inside addresses.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// serverDate is the clock fallback when the trace endpoint is unreachable. A wrong clock makes
// every certificate look expired or not yet valid, which is exactly when the trace fails; so the
// certificate is still checked (chain and name), but at a moment inside its own validity, not now.
func serverDate(ctx context.Context, u string) (time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pu, err := url.Parse(u)
	if err != nil {
		return time.Time{}, err
	}
	tr := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // the check is VerifyConnection below
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyAnyTime(cs.PeerCertificates, pu.Hostname(), nil)
		},
	}}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "HEAD", u, nil)
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		return time.Time{}, err
	}
	resp.Body.Close()
	return http.ParseTime(resp.Header.Get("Date"))
}

// verifyAnyTime checks a server's certificate chain and name as of the middle of the leaf's
// validity, ignoring this computer's clock. roots nil: the system store.
func verifyAnyTime(certs []*x509.Certificate, host string, roots *x509.CertPool) error {
	if len(certs) == 0 {
		return errors.New("no certificate")
	}
	leaf, inter := certs[0], x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter, Roots: roots,
		CurrentTime: leaf.NotBefore.Add(leaf.NotAfter.Sub(leaf.NotBefore) / 2)})
	return err
}
