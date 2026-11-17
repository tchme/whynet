// Package health checks this PC's network setup (docs/health.md): every area records facts and
// raw evidence; rules/health.yaml decides what is wrong; fix.go changes what the person agreed to.
package health

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"whynet/internal/live"
	"whynet/internal/probe"
)

// Areas in the order the screen and the report list them.
var Areas = []string{"health_adapter", "health_address", "health_dns", "health_proxy", "health_hosts",
	"health_tcp", "health_ipv6", "health_mtu", "health_clock", "health_soft"}

// Result is one health check.
type Result struct {
	Started time.Time         `json:"started"`
	Seconds float64           `json:"seconds"`
	Steps   []probe.Step      `json:"steps"`
	Mask    map[string]string `json:"-"`
}

// Facts merges every area's facts.
func (r *Result) Facts() map[string]string {
	f := map[string]string{}
	for _, s := range r.Steps {
		maps.Copy(f, s.Facts)
	}
	return f
}

// Evidence is one area's evidence value, nil when missing.
func (r *Result) Evidence(area, key string) any {
	for _, s := range r.Steps {
		if s.ID == area {
			return s.Evidence[key]
		}
	}
	return nil
}

// Thresholds (docs/health.md, "Calls made by the designer").
const (
	errorWindow  = 8 * time.Second // the adapter's error counters: read at the start and this long after
	errorsGrown  = 10              // new errors in that window that make a finding
	slowLinkMbps = 100             // a wired link at this speed or less
	weakWiFiPct  = 40
	dnsSlow      = 500 * time.Millisecond
	pinCap       = 400 // hosts names tested at most
)

type checker struct {
	ctx      context.Context
	m        probe.Markers
	start    time.Time
	local    func() probe.Local
	adapters []probe.Adapter // up, read once at the start
	adErr    error
	errs0    map[uint64]uint64
}

// Run checks every area at once and calls onStep as each one finishes (from one goroutine at a time).
func Run(ctx context.Context, m probe.Markers, onStep func(probe.Step)) *Result {
	c := &checker{ctx: ctx, m: m, start: time.Now(), errs0: map[uint64]uint64{}}
	localCh := make(chan probe.Local, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				localCh <- probe.Local{Facts: map[string]string{}, Evidence: map[string]any{"panic": fmt.Sprint(p)}, Mask: map[string]string{}}
			}
		}()
		localCh <- probe.LocalInfo(ctx, m)
	}()
	c.local = sync.OnceValue(func() probe.Local { return <-localCh })
	c.adapters, c.adErr = probe.Adapters()
	for _, a := range c.physical() {
		if n, ok := errorCount(a.LUID); ok {
			c.errs0[a.LUID] = n
		}
	}

	areas := map[string]func(*probe.Step){
		"health_adapter": c.adapter, "health_address": c.address, "health_dns": c.dns, "health_proxy": c.proxy,
		"health_hosts": c.hosts, "health_tcp": c.tcp, "health_ipv6": c.ipv6, "health_mtu": c.mtu,
		"health_clock": c.clock, "health_soft": c.soft,
	}
	var mu sync.Mutex
	done := map[string]probe.Step{}
	var wg sync.WaitGroup
	for _, id := range Areas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := probe.Step{ID: id, Status: "ok", Facts: map[string]string{}, Evidence: map[string]any{}}
			t0 := time.Now()
			func() {
				defer func() {
					if p := recover(); p != nil {
						s.Status = "error"
						s.Evidence["panic"] = fmt.Sprint(p)
					}
				}()
				areas[id](&s)
			}()
			s.Ms = time.Since(t0).Milliseconds()
			mu.Lock()
			defer mu.Unlock()
			done[id] = s
			if onStep != nil {
				onStep(s)
			}
		}()
	}
	wg.Wait()
	res := &Result{Started: c.start, Mask: map[string]string{}}
	for _, id := range Areas {
		res.Steps = append(res.Steps, done[id])
	}
	maps.Copy(res.Mask, c.local().Mask)
	res.Seconds = time.Since(c.start).Seconds()
	return res
}

// physical are the adapters that are up and are neither a VPN nor a virtual machine's switch.
func (c *checker) physical() []probe.Adapter {
	var out []probe.Adapter
	for _, a := range c.adapters {
		if !c.m.IsVPN(a) && !c.virtual(a) {
			out = append(out, a)
		}
	}
	return out
}

func (c *checker) virtual(a probe.Adapter) bool {
	s := strings.ToLower(a.Name + " " + a.Description)
	return slices.ContainsFunc(c.m.VirtualAdapters, func(v string) bool { return strings.Contains(s, v) })
}

// main is the physical adapter the internet goes through: the first with a gateway.
func (c *checker) main() *probe.Adapter {
	for _, a := range c.physical() {
		if len(a.Gateways) > 0 {
			return &a
		}
	}
	return nil
}

// view is an adapter as the report lists it: no addresses, they are private.
func (c *checker) view(a probe.Adapter) map[string]any {
	return map[string]any{"name": a.Name, "description": a.Description, "kind": a.Kind, "link_mbps": a.Mbps,
		"mtu": a.MTU, "dhcp": a.DHCP, "vpn": c.m.IsVPN(a), "virtual": c.virtual(a), "gateway": len(a.Gateways) > 0}
}

func (c *checker) adapter(s *probe.Step) {
	if c.adErr != nil {
		s.Evidence["error"] = c.adErr.Error()
	}
	var all []map[string]any
	for _, a := range c.adapters {
		all = append(all, c.view(a))
	}
	s.Evidence["adapters"] = all
	m := c.main()
	if m == nil {
		s.Facts["health.adapter"] = "none"
		return
	}
	s.Facts["health.adapter"] = m.Kind
	s.Facts["health.link_mbps"] = strconv.FormatUint(m.Mbps, 10)
	s.Facts["health.link_slow"] = strconv.FormatBool(m.Kind == "cable" && m.Mbps > 0 && m.Mbps <= slowLinkMbps)
	if m.Kind == "wifi" {
		if w, ok, err := live.ReadWiFi(m.GUID); err != nil {
			s.Evidence["wifi_error"] = err.Error()
		} else if ok {
			s.Facts["health.wifi_signal"] = strconv.Itoa(w.Signal)
			s.Facts["health.wifi_band"] = w.Band
			s.Facts["health.wifi_weak"] = strconv.FormatBool(w.Signal < weakWiFiPct)
			w.SSID = "" // can name a person; not needed here
			s.Evidence["wifi"] = w
		}
	}
	// New errors while the check runs: old ones may be from a cable plugged in weeks ago.
	select {
	case <-time.After(time.Until(c.start.Add(errorWindow))):
	case <-c.ctx.Done():
		return
	}
	if n0, ok := c.errs0[m.LUID]; ok {
		if n1, ok := errorCount(m.LUID); ok {
			s.Facts["health.errors_new"] = strconv.FormatUint(n1-n0, 10)
			s.Facts["health.errors_growing"] = strconv.FormatBool(n1-n0 >= errorsGrown)
			s.Evidence["errors_total"] = n1
		}
	}
}

func (c *checker) address(s *probe.Step) {
	m := c.main()
	s.Facts["health.gateway"] = "none"
	if m != nil {
		s.Facts["health.gateway"] = "ok"
	}
	// 169.254.x.x and nothing else: the adapter asked the router for an address and got no answer.
	for _, a := range c.physical() {
		apipa := len(a.IPv4) > 0
		for _, ip := range a.IPv4 {
			apipa = apipa && strings.HasPrefix(ip, "169.254.")
		}
		if apipa && a.DHCP {
			s.Facts["health.apipa"] = "true"
			s.Facts["health.apipa_adapter"] = a.Name
			s.Evidence["apipa"] = map[string]any{"name": a.Name, "luid": a.LUID}
			break
		}
	}
	if s.Facts["health.apipa"] == "" {
		s.Facts["health.apipa"] = "false"
	}
	s.Facts["health.connectivity"] = connectivity()
	var vpn []string
	for _, a := range c.adapters {
		if c.m.IsVPN(a) {
			vpn = append(vpn, a.Name)
		}
	}
	s.Facts["health.vpn_adapters"] = strconv.Itoa(len(vpn))
	if len(vpn) > 0 {
		s.Evidence["vpn_adapters"] = vpn
	}
	l := c.local()
	for _, k := range []string{"local.net", "local.vpn", "local.country"} {
		if v, ok := l.Facts[k]; ok {
			s.Facts[k] = v
		}
	}
	for _, k := range []string{"os", "public"} {
		if v, ok := l.Evidence[k]; ok {
			s.Evidence[k] = v
		}
	}
	// Windows says "no internet" while whynet reached Cloudflare: something local fools its check.
	_, reached := l.Evidence["public"]
	s.Facts["health.ncsi_wrong"] = strconv.FormatBool(reached && s.Facts["health.connectivity"] != "internet" && s.Facts["health.connectivity"] != "unknown")
}

// dnsServer is one DNS server an adapter uses, and how it answered.
type dnsServer struct {
	Adapter string `json:"adapter"`
	Server  string `json:"server"`
	Manual  bool   `json:"manual"`
	Ms      int64  `json:"ms,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (c *checker) dns(s *probe.Step) {
	s.Facts["health.dnscache"] = probe.ServiceState("Dnscache")
	var servers []dnsServer
	var fixes []map[string]any
	for _, a := range c.physical() {
		manual, dhcp := dnsConfig(a.GUID)
		for _, ip := range a.DNS {
			if strings.HasPrefix(ip, "fec0:") {
				continue // the old site-local placeholder Windows lists when there is no IPv6 DNS
			}
			servers = append(servers, dnsServer{Adapter: a.Name, Server: ip, Manual: slices.Contains(manual, ip)})
		}
		// Servers set by hand that don't answer, while the router's (from DHCP) would: back to automatic.
		if len(manual) > 0 && len(dhcp) > 0 && a.DHCP {
			fixes = append(fixes, map[string]any{"adapter": a.Name, "index": a.Index, "luid": a.LUID, "manual": manual, "dhcp": dhcp})
		}
	}
	parallel(8, len(servers), func(i int) {
		d, err := queryDNS(c.ctx, servers[i].Server)
		if err != nil {
			servers[i].Error = err.Error()
		} else {
			servers[i].Ms = max(d.Milliseconds(), 1)
		}
	})
	s.Evidence["servers"] = servers
	s.Facts["health.dns"] = dnsVerdict(servers)
	// The fix is offered only when every manual server of that adapter is dead and a DHCP one answers.
	s.Facts["health.dns_manual_dead"] = "false"
	for _, f := range fixes {
		dead := true
		for _, sv := range servers {
			if sv.Adapter == f["adapter"] && sv.Manual && sv.Error == "" {
				dead = false
			}
		}
		if !dead {
			continue
		}
		alive := false
		for _, ip := range f["dhcp"].([]string) {
			if _, err := queryDNS(c.ctx, ip); err == nil {
				alive = true
				break
			}
		}
		if alive {
			s.Facts["health.dns_manual_dead"] = "true"
			s.Facts["health.dns_manual_adapter"] = f["adapter"].(string)
			s.Facts["health.dns_manual_servers"] = strings.Join(f["manual"].([]string), ", ")
			s.Evidence["dns_fix"] = f
			break
		}
	}
}

// dnsVerdict: none (no servers), dead_all, dead_some, slow (all answer, the best one slowly), ok.
func dnsVerdict(servers []dnsServer) string {
	if len(servers) == 0 {
		return "none"
	}
	dead, best := 0, int64(0)
	for _, s := range servers {
		if s.Error != "" {
			dead++
		} else if best == 0 || s.Ms < best {
			best = s.Ms
		}
	}
	switch {
	case dead == len(servers):
		return "dead_all"
	case dead > 0:
		return "dead_some"
	case best > dnsSlow.Milliseconds():
		return "slow"
	}
	return "ok"
}

// queryDNS asks one server for a name that can't be in the hosts file; "not found" is an answer too.
func queryDNS(ctx context.Context, server string) (time.Duration, error) {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp", net.JoinHostPort(server, "53"))
	}}
	var err error
	for try := range 3 {
		qctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		t := time.Now()
		_, err = r.LookupNetIP(qctx, "ip4", fmt.Sprintf("whynet-%d-%d.example.com", t.UnixNano()%1e6, try))
		cancel()
		var de *net.DNSError
		if err == nil || errors.As(err, &de) && de.IsNotFound {
			return time.Since(t), nil
		}
	}
	return 0, err
}

func (c *checker) proxy(s *probe.Step) {
	on, server, pac, err := inetProxy()
	if err != nil {
		s.Evidence["error"] = err.Error()
	}
	s.Facts["health.proxy"], s.Facts["health.proxy_pac"] = "off", "none"
	if on && server != "" {
		s.Facts["health.proxy"] = proxyState(c.ctx, proxyAddrs(server))
		s.Facts["health.proxy_server"] = server
	}
	if pac != "" {
		s.Facts["health.proxy_pac"] = pacState(c.ctx, pac)
		s.Facts["health.proxy_pac_url"] = pac
	}
	s.Evidence["inet"] = map[string]any{"enabled": on, "server": server, "pac": pac}
	ws, bypass, err := winHTTPProxy()
	switch {
	case err != nil:
		s.Facts["health.winhttp"] = "unknown"
		s.Evidence["winhttp_error"] = err.Error()
	case ws == "":
		s.Facts["health.winhttp"] = "direct"
	default:
		s.Facts["health.winhttp"] = proxyState(c.ctx, proxyAddrs(ws))
		s.Facts["health.winhttp_server"] = ws
		s.Evidence["winhttp"] = map[string]any{"server": ws, "bypass": bypass}
	}
}

// proxyAddrs reads a proxy setting ("host:port", "http=h:p;https=h:p", "socks=h:p", with or
// without a scheme) into the addresses to try; port 80 when none is given.
func proxyAddrs(setting string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(setting, func(r rune) bool { return r == ';' || r == ' ' }) {
		if _, v, ok := strings.Cut(part, "="); ok {
			part = v
		}
		if _, v, ok := strings.Cut(part, "://"); ok {
			part = v
		}
		part = strings.TrimRight(part, "/")
		if part == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(part); err != nil {
			part = net.JoinHostPort(strings.Trim(part, "[]"), "80")
		}
		if !slices.Contains(out, part) {
			out = append(out, part)
		}
	}
	return out
}

// proxyState: ok when any of the addresses accepts a connection, dead when none does.
func proxyState(ctx context.Context, addrs []string) string {
	if len(addrs) == 0 {
		return "unknown"
	}
	for _, a := range addrs {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, err := (&net.Dialer{}).DialContext(cctx, "tcp", a)
		cancel()
		if err == nil {
			conn.Close()
			return "ok"
		}
	}
	return "dead"
}

// pacState fetches the proxy script directly: ok, dead. A file:// script is only checked to exist.
func pacState(ctx context.Context, u string) string {
	if strings.HasPrefix(strings.ToLower(u), "file:") {
		pu, err := url.Parse(u)
		if err != nil {
			return "dead"
		}
		if _, err := os.Stat(filepath.FromSlash(strings.TrimPrefix(pu.Path, "/"))); err != nil {
			return "dead"
		}
		return "ok"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "dead"
	}
	resp, err := (&http.Client{Transport: &http.Transport{Proxy: nil}}).Do(req)
	if err != nil {
		return "dead"
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "dead"
	}
	return "ok"
}

func (c *checker) hosts(s *probe.Step) {
	b, err := os.ReadFile(hostsPath())
	if err != nil {
		s.Facts["health.hosts"] = "unreadable"
		s.Evidence["error"] = err.Error()
		return
	}
	lines := parseHosts(string(b))
	counts := map[string]int{}
	for _, l := range lines {
		counts[l.Kind]++
	}
	s.Facts["health.hosts"] = "ok"
	s.Facts["health.hosts_lines"] = strconv.Itoa(len(lines))
	s.Facts["health.hosts_pins"] = strconv.Itoa(counts["pin"])
	s.Facts["health.hosts_blocks"] = strconv.Itoa(counts["block"])
	s.Facts["health.hosts_ncsi"] = strconv.FormatBool(blocksNCSI(lines))
	pc := pinCheck{Dial: (&net.Dialer{}).DialContext, Wait: 4 * time.Second, Cap: pinCap, Par: 16}
	res, unchecked := pc.run(c.ctx, lines)
	s.Facts["health.hosts_unchecked"] = strconv.Itoa(unchecked)
	byResult := map[string]int{}
	for _, r := range res {
		byResult[r.Result]++
	}
	s.Evidence["pin_results"] = byResult
	s.Facts["health.hosts_cut"] = strconv.Itoa(byResult["cut"])
	stale := staleLines(lines, res)
	s.Facts["health.hosts_stale"] = strconv.Itoa(len(stale))
	if len(stale) == 0 {
		return
	}
	var names, addrs []string
	var texts []string
	why := map[string]string{}
	for _, r := range res {
		why[r.Addr+" "+r.Name] = r.Result
	}
	var detail []map[string]string
	for _, l := range stale {
		texts = append(texts, l.Text)
		names = append(names, l.Names...)
		if !slices.Contains(addrs, l.Addr) {
			addrs = append(addrs, l.Addr)
		}
		detail = append(detail, map[string]string{"line": l.Text, "why": why[l.Addr+" "+l.Names[0]]})
	}
	s.Facts["health.hosts_stale_names"], s.Facts["health.hosts_stale_more"] = firstOf(names, 2)
	s.Facts["health.hosts_stale_addrs"], s.Facts["health.hosts_stale_addrs_more"] = firstOf(addrs, 1)
	s.Evidence["stale_lines"] = texts // the fix comments out exactly these
	s.Evidence["stale"] = detail
}

// firstOf: the first n items joined, and " (+k)" for the rest ("" when none are left).
func firstOf(items []string, n int) (first, more string) {
	if len(items) <= n {
		return strings.Join(items, ", "), ""
	}
	return strings.Join(items[:n], ", "), fmt.Sprintf(" (+%d)", len(items)-n)
}

func (c *checker) tcp(s *probe.Step) {
	out, err := netsh(c.ctx, "int", "tcp", "show", "global")
	if err != nil {
		s.Evidence["netsh_error"] = err.Error()
	}
	s.Facts["health.rss"], s.Facts["health.autotuning"] = parseTCPGlobal(out)
	paths, err := winsockPaths()
	if err != nil {
		s.Evidence["winsock_error"] = err.Error()
		s.Facts["health.winsock"] = "unknown"
		return
	}
	foreign := foreignWinsock(paths)
	s.Facts["health.winsock"] = "clean"
	if len(foreign) > 0 {
		s.Facts["health.winsock"] = "foreign"
		s.Facts["health.winsock_names"], _ = firstOf(foreign, 3)
		s.Evidence["winsock_foreign"] = foreign
	}
}

// parseTCPGlobal reads "netsh int tcp show global": its labels follow the Windows language but
// the first two "label : value" lines are always receive-side scaling, then receive window
// auto-tuning, and the values are not translated.
// shortcut: relies on that line order (Windows 10 22H2, 11 26H2); "unknown" when the values don't fit.
func parseTCPGlobal(out string) (rss, autotuning string) {
	rss, autotuning = "unknown", "unknown"
	var vals []string
	for _, line := range strings.Split(out, "\n") {
		if _, v, ok := strings.Cut(line, ":"); ok {
			vals = append(vals, strings.ToLower(strings.TrimSpace(v)))
		}
	}
	if len(vals) >= 2 {
		if slices.Contains([]string{"enabled", "disabled"}, vals[0]) {
			rss = vals[0]
		}
		if slices.Contains([]string{"disabled", "highlyrestricted", "restricted", "normal", "experimental"}, vals[1]) {
			autotuning = vals[1]
		}
	}
	return rss, autotuning
}

// msWinsock are the providers Windows itself installs (file names, lowercase).
var msWinsock = []string{"mswsock.dll", "rsvpsp.dll", "hvsocket.dll", "afunix.dll", "wshbth.dll", "wshqos.dll", "napinsp.dll", "pnrpnsp.dll", "nlansp_c.dll", "winrnr.dll", "wshtcpip.dll"}

// foreignWinsock returns the provider DLLs that are not Windows' own (file names, once each).
func foreignWinsock(paths []string) []string {
	var out []string
	for _, p := range paths {
		name := strings.ToLower(filepath.Base(strings.ReplaceAll(p, `\`, "/")))
		if !slices.Contains(msWinsock, name) && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func (c *checker) ipv6(s *probe.Step) {
	m := c.main()
	if m == nil || !m.IPv6Global {
		s.Facts["health.ipv6"] = "none"
		return
	}
	// A global address but no way out: programs that try IPv6 first wait before falling back.
	ctx, cancel := context.WithTimeout(c.ctx, 4*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp6", "[2606:4700:4700::1111]:443")
	if err != nil {
		s.Facts["health.ipv6"] = "broken"
		s.Evidence["error"] = err.Error()
		return
	}
	conn.Close()
	s.Facts["health.ipv6"] = "ok"
}

func (c *checker) mtu(s *probe.Step) {
	m := c.main()
	if m == nil {
		return
	}
	mtu := m.MTU
	if n, ok := ipv4MTU(m.LUID); ok {
		mtu = n
	}
	s.Facts["health.mtu"] = strconv.FormatUint(uint64(mtu), 10)
	s.Facts["health.mtu_low"] = strconv.FormatBool(mtu > 0 && mtu < 1500)
	s.Facts["health.mtu_adapter"] = m.Name
}

func (c *checker) clock(s *probe.Step) {
	s.Facts["health.w32time"] = probe.ServiceState("W32Time")
	l := c.local()
	for _, k := range []string{"local.clock", "local.clock_skew_sec"} {
		if v, ok := l.Facts[k]; ok {
			s.Facts[k] = v
		}
	}
	// The site check calls 5 min "wrong" (certificates); here 2 min is already worth a sync.
	if n, err := strconv.Atoi(l.Facts["local.clock_skew_sec"]); err == nil {
		s.Facts["health.clock_off"] = strconv.FormatBool(n > 120 || n < -120)
		s.Facts["health.clock_skew_min"] = strconv.Itoa((max(n, -n) + 30) / 60)
	}
}

func (c *checker) soft(s *probe.Step) {
	tools, err := probe.RunningTools(c.m.Throttlers)
	if err != nil {
		s.Evidence["error"] = err.Error()
	}
	s.Facts["health.throttler_found"] = strconv.FormatBool(len(tools) > 0)
	if len(tools) > 0 {
		s.Facts["health.throttler"] = strings.Join(tools, ", ")
	}
	l := c.local()
	for k, v := range l.Facts {
		if strings.HasPrefix(k, "local.dpi_") || k == "local.windivert" {
			s.Facts[k] = v
		}
	}
	if v, ok := l.Evidence["dpi_bypass"]; ok {
		s.Evidence["dpi_bypass"] = v
	}
}
