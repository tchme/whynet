# Architecture

```
whynet/
  main.go, app.go          Wails entry point (UI not built yet: step 5)
  cmd/whynet-cli/          console front end: progress, verdict, -json, -save (AI report)
  internal/probe/          the checks; Env holds everything external, tests swap it
    probe.go               Run, Step/Result, Env, Target (input -> ASCII host + path), local/dns/tcp steps
    tls.go, http.go        tls step (real SNI, control SNI, alt address), http step
    classify.go            error -> fact words (Windows WSA codes), fake-ip range
    doh.go                 DNS-over-HTTPS JSON client (1.1.1.1, then 8.8.8.8)
    local.go               hosts parsing, Cloudflare trace + Team Cymru AS lookup, IP masking
    local_windows.go       route, OS, adapters (GetAdaptersAddresses), proxy (registry), hosts, clock
    dpi.go                 bundle folder from a path, zapret hostlist matching (plain = with subdomains, ^ = exact)
    dpi_windows.go         DPI-bypass tools: processes (+exe, command line), services, WinDivert driver; no admin needed
  internal/report/         the AI report (Markdown) and its file name; Version
  rules/                   rules engine + data, embedded: diagnoses.yaml, markers.yaml
  locales/                 every user-facing string: ru.yaml
  frontend/                HTML/CSS/JS (Wails vanilla template)
```

## Data flow

```
input -> probe.Target -> probe.Run(ctx, Env, onStep) -> Result{Steps[{facts, evidence}], Mask}
      -> rules.Match(rules, Result.Facts()) -> Verdict{id, confidence}
      -> CLI / UI text from locales by id ({fact} placeholders filled)
      -> report.Markdown(Result, Verdict, texts, hide) -> .md file
```

- Probes only gather facts and evidence; all judgement is in `rules/diagnoses.yaml`. Markers (`markers.yaml`) are lowercase substrings probes use to label what they saw (stub pages, geo-block texts, HTTPS interceptors, VPN adapters).
- Every step runs under panic recovery and its own timeouts; a failed step is recorded and the next steps still run (skipping what they cannot do).
- DPI-bypass tools (zapret/Flowseal, GoodbyeDPI) rewrite packets through WinDivert for every process, whynet included, so the checks see what the browser sees. The bundle folder is found from the process exe, the service command line or the driver path, in that order; its lists tell whether the site is covered. Tool names, services and drivers are data in `markers.yaml`.
- The report masks private values (public IP to its first two octets, computer and user names as a safety net) by plain replacement over the finished text, raw JSON included.

## Facts

| Key | Values |
|---|---|
| `local.net` | ok, none (no route out) |
| `local.proxy` | on, off |
| `local.hosts_entry` | true, false |
| `local.clock` / `local.clock_skew_sec` | ok, wrong (> 5 min), unknown / seconds, local minus server |
| `local.vpn` | true, false (a VPN adapter on this PC; a VPN on the router shows as `dns.match=fakeip`) |
| `local.dpi_tool` | none, zapret, goodbyedpi, several: a WinDivert-based bypass running (process or service) |
| `local.dpi_installed` | true when a bypass service or the WinDivert driver is registered, running or not |
| `local.windivert` | running, stopped, absent, unknown: the driver stays loaded after zapret exits, until reboot |
| `local.dpi_listed` | true, false, excluded, unknown: is the site in the bundle's `lists\*.txt` (only when a bypass is found) |
| `dns.system` | ok, nxdomain, timeout, error, empty, literal (input was an IP) |
| `dns.doh` | ok, nxdomain, unavailable, empty |
| `dns.match` | same, differ, fakeip (system answers in 198.18.0.0/15), unknown |
| `dns.used` | system, doh (whose addresses the next steps use) |
| `tcp.status` / `tcp.ip` | ok, timeout, reset, refused, unreachable, eof, error, skip / address used |
| `tls.status` | ok, reset, timeout, eof, alert, cert_expired, cert_name, cert_untrusted, cert_invalid, error, skip |
| `tls.control` | ok, failed: handshake to the same address with a neutral SNI (`example.org`), only after a cut |
| `tls.alt` | like `tls.status`: real SNI to the DoH address, only when `dns.match=differ` |
| `tls.intercepted` / `tls.interceptor` | true / the marker found in the chain |
| `http.scheme` | https (TLS worked), http (fallback, where ISP stubs show up) |
| `http.status` / `http.class` | code or error class / 2xx..5xx, fail |
| `http.stub`, `http.geo`, `http.challenge` | true when seen |
