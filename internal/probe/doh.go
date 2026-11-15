package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Independent resolvers, by IP so the system DNS is not involved. Tried in turn:
// one of them may be blocked.
var dohServers = []string{"https://1.1.1.1/dns-query", "https://8.8.8.8/resolve"}

// direct ignores proxies: whynet checks the path itself.
var direct = &http.Client{Transport: &http.Transport{Proxy: nil}}

func dohLookup(ctx context.Context, host string) (DoHAnswer, error) {
	server, ips, err := dohQuery(ctx, host, "A")
	return DoHAnswer{Server: server, IPs: ips}, err
}

// dohQuery asks the DoH servers in turn and returns the first real answer.
func dohQuery(ctx context.Context, name, qtype string) (server string, data []string, err error) {
	var errs []error
	for _, s := range dohServers {
		data, err := dohOne(ctx, s, name, qtype)
		if err == nil || errors.Is(err, ErrNXDomain) {
			return s, data, err
		}
		errs = append(errs, err)
	}
	return "", nil, errors.Join(errs...)
}

func dohOne(ctx context.Context, server, name, qtype string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", server+"?name="+url.QueryEscape(name)+"&type="+qtype, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := direct.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", server, resp.StatusCode)
	}
	var r struct {
		Status int
		Answer []struct {
			Type int    `json:"type"`
			Data string `json:"data"`
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&r); err != nil {
		return nil, fmt.Errorf("%s: %w", server, err)
	}
	switch r.Status {
	case 0:
	case 3:
		return nil, ErrNXDomain
	default:
		return nil, fmt.Errorf("%s: DNS rcode %d", server, r.Status)
	}
	want := map[string]int{"A": 1, "TXT": 16}[qtype]
	var out []string
	for _, a := range r.Answer {
		if a.Type == want {
			out = append(out, strings.Trim(a.Data, `"`))
		}
	}
	return out, nil
}
