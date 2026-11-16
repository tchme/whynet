# Diagnoses catalog

Every diagnosis whynet can give: how it is detected and how it is tested. Rules live in `rules/diagnoses.yaml` (order matters: first match wins), texts in `locales/ru.yaml`, fact keys in [architecture.md](architecture.md). "Lab" = `rules/lab_test.go` (fake network with real local servers), "table" = `rules/rules_test.go` (facts straight into rules).

| id | Meaning (plain) | Detected by | Tested |
|---|---|---|---|
| `no_internet` | No internet at all | `local.net=none`; or system DNS failing and no DoH reachable (likely) | table |
| `hosts_entry` | The site is rewritten in the `hosts` file | `local.hosts_entry=true` | table; unit test of the parser; live: `10.255.255.1 example.com` added for one run (2026-11-16) |
| `no_such_site` | Site doesn't exist / typo | NXDOMAIN from system DNS and DoH (likely, if DoH is unreachable) | lab, table; live: `doesnotexist-whynet-test.com` |
| `clock_wrong` | Computer clock is wrong | certificate error plus clock skew > 5 min (the fallback clock source checks its certificate at the certificate's own time, so a wrong clock can't hide itself) | table; `TestVerifyAnyTime`; live: clock set 400 days back for one run, skew -34559810 s (2026-11-16) |
| `dns_spoofed` | ISP substitutes the site's address | system and DoH differ, real address fails, DoH address works (`tls.alt=ok`) | lab |
| `dns_blocked` | The site is hidden in DNS (likely) | system DNS says NXDOMAIN, DoH knows it and the DoH address works | table; live: rutracker.org through the author's router DNS (2026-11-16) |
| `isp_stub` | ISP stub page | page or final URL matches `markers.yaml` stubs | lab (stub over HTTP after a TLS cut); live: rutracker.org on Beeline, `307 → http://blackhole.beeline.ru/` "Ресурс заблокирован" (shows up on some runs only, others time out) |
| `sni_blocked_router` | Behind a fake-ip router: the site's name is cut, the path itself works (likely) | `dns.match=fakeip`, TLS cut, control SNI ok | table; live: rutracker.org excluded from Podkop, Beeline DPI |
| `proxy_path_broken` | Breaks on the way through a VPN/proxy (usually on the router) | `dns.match=fakeip` and TLS not ok | lab |
| `dpi_conflict` | Two WinDivert bypass tools fight (likely) | `local.dpi_tool=several` and TLS not ok, or cut after the handshake (as `cut_midway`) | table |
| `dpi_not_listed` | zapret runs, but the site is not in its lists (likely) | zapret running, `local.dpi_listed=false`, `tcp.dpi_ipset` not true, TLS cut or cut after the handshake | table; list and address matching unit tests; live, advice included: linkedin.com and rutracker.org on Flex Ltd., `all_ok` once added to `list-general-user.txt` (2026-11-16) |
| `dpi_strategy_fails` | The bypass runs but doesn't get through, or breaks the site (likely) | zapret/GoodbyeDPI running, TLS cut or cut after the handshake | table |
| `dpi_off` | A bypass is installed but not running (likely) | none running, `local.dpi_installed=true`, cut by name | table |
| `sni_blocked` | Blocked by site name (DPI) | TCP ok; real SNI reset/timeout/EOF; control SNI ok | lab (server that resets on one SNI) |
| `cut_midway` | The handshake passes, then the connection is reset on the request (likely: DPI) | TLS ok and `http.status=reset`, or the browser's navigation failed with `ERR_CONNECTION_RESET` | table; live: linkedin.com and rutracker.org on Flex Ltd. (AS21453), 2026-11-16 |
| `isp_dns_down` | ISP's DNS doesn't answer | system DNS timeout/error/empty, DoH ok, site works | lab |
| `ip_unreachable` | Server doesn't answer: blocked by address or down (likely) | TCP timeout/reset/unreachable; or TLS cut with any SNI | lab (black hole) |
| `server_broken` | The site's server is broken | TCP refused, or HTTP 5xx | lab (scripted refused, real 502) |
| `bad_certificate` | Certificate can't be trusted | certificate error, clock fine | lab (wrong name) , table |
| `content_stall` | The page opens, its content freezes after the first KB (likely) | `res.stall` | lab (server that sends 16 KB and goes silent) |
| `content_blocked` | The page opens, some content servers fail (likely) | `res.worst` set | lab (server reset on its name); live: an ad server on habr.com before ads were excluded |
| `content_slow` | Content is throttled (likely) | `res.slow` | not yet |
| `content_flaky` | The browser failed on servers that pass their own check (likely) | `br.worst` set, no `res` failure, not all of them dead links (`res.browser_dead` gives `all_ok`) | lab (dead link case) |
| `geo_blocked` | The site itself refuses your country | `http.geo` (451, or a geo text on a 4xx) | table |
| `https_intercepted` | HTTPS intercepted by antivirus/other software (likely) | interceptor marker in the chain | table |
| `proxy_in_way` | Works directly, but a system proxy is on (likely) | `local.proxy=on`, site ok; placed before the browser rules, since the browser goes through the proxy | table; live: proxy switched on to a dead address, browser `ERR_PROXY_CONNECTION_FAILED` (2026-11-16) |
| `site_blocks_you` | Even a plain browser window got the site's block page (likely: VPN/proxy) | `br.blocked_page` | table |
| `bot_check` | The site turns away automated checks; a normal browser will likely open it | `br.antibot`, or TLS ok and `http.challenge` | table; live: ozon.ru (turns away any browser with a debugging port) |
| `browser_ok` | The person's plain window loaded the page | `br.visible` and `br.status=ok` | table |
| `page_slow` | The page shows very late or never (likely) | `br.slow_load` (> 8 s) or `br.loaded=false` | table |
| `all_ok` | Everything works; the problem is in the browser | TLS ok and 2xx/3xx | lab, table; live: youtube.com, github.com |
| `unknown` | whynet can't tell; save the AI report | no other rule matched (always last); also the browser's navigation failed (`br.status=nav_failed`) for another reason while whynet's own request passed | table |

## Stability check verdicts

Rules in `rules/live.yaml` (first match wins, nearest link first), texts under `live_diagnoses` in `locales/ru.yaml`, facts in [architecture.md](architecture.md). "Analysis" = `internal/live/analyze_test.go` (synthetic recordings through the analysis and the rules), "session" = `session_test.go` (a fake network).

| id | Meaning (plain) | Detected by | Tested |
|---|---|---|---|
| `live_too_short` | Recorded under 30 s | `live.too_short` | table, analysis |
| `live_router_silent` | The router never answers pings | `live.router=silent` or no gateway | table, analysis |
| `live_wifi` | Ping jumps because of the Wi-Fi link (high) | router unstable by itself, wireless | table, analysis (the friend's case: spikes every 60 s, everything behind repeats them) |
| `live_lan` | The cable link, the network card or the router (likely) | router unstable by itself, cable | table, analysis |
| `live_vpn` | The path through a VPN on this PC (likely) | `live.path_vpn` and the node beyond or the internet unstable | table, session (VPN adapter on the default route) |
| `live_isp` | The ISP's access network (likely) | the first node beyond the router unstable by itself, confirmed by the internet links | table, analysis (also: a node that only ignores pings to itself is not blamed) |
| `live_isp_network` | Deeper in the ISP's network (likely) | both controls unstable by themselves | table, analysis |
| `live_route` | The route to the server or the server (likely) | the server (or its stand-in) unstable by itself | table, analysis |
| `live_stable` | The network held | nothing else matched (always last) | table, analysis; live: the author's PC on cable, 40 s (2026-11-16) |

`all_ok` matters as much as the failures: it sends the person to the browser (cache, extensions, its own proxy or DNS-over-HTTPS setting).

Still to verify on real networks (plan step 6): `isp_stub` and `dns_spoofed` against real ISP blocking, `https_intercepted`, `dpi_strategy_fails` and `dpi_conflict`. Done: `sni_blocked`, `cut_midway`, `dpi_not_listed` (a friend's network), `geo_blocked`, `hosts_entry`, `clock_wrong`, `proxy_in_way` (the author's PC). The "router VPN off" runs of the plan are dropped: the author's VPN must never be interrupted, and the friend's network has no VPN.

Content checks, verified live 2026-11-15 (Edge 157): youtube.com — 164 browser requests to 9 servers incl. real `rr*---sn-*.googlevideo.com` video servers, 6 content servers checked, `all_ok` in ~9 s; a watch page measured the video server at ~1600 KB/s; habr.com — ~300 requests, `all_ok` in ~17 s on three runs in a row, Google ad servers cut by name and listed as ads.

First run on someone else's network, 2026-11-16: Windows 10 Home 22H2, ISP Flex Ltd. (AS21453), no VPN, Flowseal zapret; ya.ru, rutracker.org, linkedin.com, instagram.com, discord.com and a youtube.com video, each with zapret running and stopped (reports kept by the author, not in the repo). The browser step works on Windows 10.
- zapret stopped: rutracker.org (TLS reset), discord.com and youtube.com (TLS timeout) `sni_blocked`, control SNI ok; linkedin.com handshake ok, request reset (`unknown` then, now `cut_midway`); instagram.com opens directly (the ISP's DNS gives a working Meta address; the DoH address times out), `all_ok`; ya.ru `all_ok`.
- discord.com without zapret: the TLS probe timed out, but the HTTP step's later connection (http:// → 301 → https://) got the page: this DPI does not cut every connection.
- zapret running: discord.com, instagram.com, ya.ru `all_ok`; rutracker.org handshake ok, the second request reset (`unknown` then; "not in the lists" by name, though Flowseal's `ipset-all.txt` holds Cloudflare's 172.67.128.0/17, now `tcp.dpi_ipset`); linkedin.com: whynet's request got a reCAPTCHA page, the browser's navigation was reset, yet `all_ok` (now `dpi_not_listed`, or `dpi_strategy_fails` when Flowseal's ipset is in "any" mode); youtube.com: 6 failed requests to a googlevideo node neither DNS knows gave `content_flaky` "though the servers passed their own check" (now `all_ok` via `res.browser_dead`).
- Re-run with 0.1.2 (zapret running, Flowseal 1.10.4, its menu showed IPSet Filter `[none]`, Game Filter `[disabled]`): linkedin.com `dpi_not_listed` (handshake ok, request reset), rutracker.org `dpi_not_listed` (this time the TLS was reset: the earlier passing handshake was the DPI's own inconsistency, not zapret), youtube.com `all_ok` (no failed requests this time, so `res.browser_dead` was not seen live). `tcp.dpi_ipset=false` for Cloudflare and Google addresses matches the `none` mode. After the friend added both sites to `list-general-user.txt` and restarted zapret: both `all_ok`.

Verified live 2026-11-15 with Flowseal 1.9.9d on the Desktop:
- zapret stopped, driver still loaded: `dpi_tool=none`, `windivert=running`, folder found from the driver path, discord.com listed.
- `geo_blocked`: an artifact frame host on `claudeusercontent.com`, reached directly (not through the router proxy), answered HTTP 451 from Cloudflare's Moscow node; the browser showed `ERR_BLOCKED_BY_RESPONSE`, whynet gave `geo_blocked` (high).
- zapret running (`general.bat` as admin): `dpi_tool=zapret`; a normal-rights whynet reads the elevated winws.exe's path and full command line (5170 chars, the strategy); discord.com and youtube.com `listed=true`, yandex.ru `excluded`; all three `all_ok`.
