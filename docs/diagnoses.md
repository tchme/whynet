# Diagnoses catalog

Every diagnosis whynet can give: how it is detected and how it is tested. Rules live in `rules/diagnoses.yaml` (order matters: first match wins), texts in `locales/ru.yaml`, fact keys in [architecture.md](architecture.md). "Lab" = `rules/lab_test.go` (fake network with real local servers), "table" = `rules/rules_test.go` (facts straight into rules).

| id | Meaning (plain) | Detected by | Tested |
|---|---|---|---|
| `no_internet` | No internet at all | `local.net=none`; or system DNS failing and no DoH reachable (likely) | table |
| `hosts_entry` | The site is rewritten in the `hosts` file | `local.hosts_entry=true` | table; unit test of the parser |
| `no_such_site` | Site doesn't exist / typo | NXDOMAIN from system DNS and DoH (likely, if DoH is unreachable) | lab, table; live: `doesnotexist-whynet-test.com` |
| `clock_wrong` | Computer clock is wrong | certificate error plus clock skew > 5 min | table |
| `dns_spoofed` | ISP substitutes the site's address | system and DoH differ, real address fails, DoH address works (`tls.alt=ok`) | lab |
| `dns_blocked` | The site is hidden in DNS (likely) | system DNS says NXDOMAIN, DoH knows it and the DoH address works | table; live: rutracker.org through the author's router DNS (2026-11-16) |
| `isp_stub` | ISP stub page | page or final URL matches `markers.yaml` stubs | lab (stub over HTTP after a TLS cut); live: rutracker.org on Beeline, `307 → http://blackhole.beeline.ru/` "Ресурс заблокирован" (shows up on some runs only, others time out) |
| `sni_blocked_router` | Behind a fake-ip router: the site's name is cut, the path itself works (likely) | `dns.match=fakeip`, TLS cut, control SNI ok | table; live: rutracker.org excluded from Podkop, Beeline DPI |
| `proxy_path_broken` | Breaks on the way through a VPN/proxy (usually on the router) | `dns.match=fakeip` and TLS not ok | lab |
| `dpi_conflict` | Two WinDivert bypass tools fight (likely) | `local.dpi_tool=several` and TLS not ok, or cut after the handshake (as `cut_midway`) | table |
| `dpi_not_listed` | zapret runs, but the site is not in its lists (likely) | zapret running, `local.dpi_listed=false`, `tcp.dpi_ipset` not true, TLS cut or cut after the handshake | table; list and address matching unit tests |
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
| `proxy_in_way` | Works directly, but a system proxy is on (likely) | `local.proxy=on`, site ok | table |
| `site_blocks_you` | Even a plain browser window got the site's block page (likely: VPN/proxy) | `br.blocked_page` | table |
| `bot_check` | The site turns away automated checks; a normal browser will likely open it | `br.antibot`, or TLS ok and `http.challenge` | table; live: ozon.ru (turns away any browser with a debugging port) |
| `browser_ok` | The person's plain window loaded the page | `br.visible` and `br.status=ok` | table |
| `page_slow` | The page shows very late or never (likely) | `br.slow_load` (> 8 s) or `br.loaded=false` | table |
| `all_ok` | Everything works; the problem is in the browser | TLS ok and 2xx/3xx | lab, table; live: youtube.com, github.com |
| `unknown` | whynet can't tell; save the AI report | no other rule matched (always last); also the browser's navigation failed (`br.status=nav_failed`) for another reason while whynet's own request passed | table |

`all_ok` matters as much as the failures: it sends the person to the browser (cache, extensions, its own proxy or DNS-over-HTTPS setting).

Still to verify on real networks (plan step 6): `sni_blocked`, `isp_stub` and `dns_spoofed` against real ISP blocking, `geo_blocked`, `https_intercepted`, `clock_wrong`, `proxy_in_way`, and the DPI-bypass failure diagnoses (`dpi_*`) on a site zapret actually fails on.

Content checks, verified live 2026-11-15 (Edge 157): youtube.com — 164 browser requests to 9 servers incl. real `rr*---sn-*.googlevideo.com` video servers, 6 content servers checked, `all_ok` in ~9 s; a watch page measured the video server at ~1600 KB/s; habr.com — ~300 requests, `all_ok` in ~17 s on three runs in a row, Google ad servers cut by name and listed as ads.

First run on someone else's network, 2026-11-16: Windows 10 Home 22H2, ISP Flex Ltd. (AS21453), no VPN, Flowseal zapret; ya.ru, rutracker.org, linkedin.com, instagram.com, discord.com and a youtube.com video, each with zapret running and stopped (reports kept by the author, not in the repo). The browser step works on Windows 10.
- zapret stopped: rutracker.org (TLS reset), discord.com and youtube.com (TLS timeout) `sni_blocked`, control SNI ok; linkedin.com handshake ok, request reset (`unknown` then, now `cut_midway`); instagram.com opens directly (the ISP's DNS gives a working Meta address; the DoH address times out), `all_ok`; ya.ru `all_ok`.
- discord.com without zapret: the TLS probe timed out, but the HTTP step's later connection (http:// → 301 → https://) got the page: this DPI does not cut every connection.
- zapret running: discord.com, instagram.com, ya.ru `all_ok`; rutracker.org handshake ok, the second request reset (`unknown` then; "not in the lists" by name, though Flowseal's `ipset-all.txt` holds Cloudflare's 172.67.128.0/17, now `tcp.dpi_ipset`); linkedin.com: whynet's request got a reCAPTCHA page, the browser's navigation was reset, yet `all_ok` (now `dpi_not_listed`, or `dpi_strategy_fails` when Flowseal's ipset is in "any" mode); youtube.com: 6 failed requests to a googlevideo node neither DNS knows gave `content_flaky` "though the servers passed their own check" (now `all_ok` via `res.browser_dead`).

Verified live 2026-11-15 with Flowseal 1.9.9d on the Desktop:
- zapret stopped, driver still loaded: `dpi_tool=none`, `windivert=running`, folder found from the driver path, discord.com listed.
- `geo_blocked`: an artifact frame host on `claudeusercontent.com`, reached directly (not through the router proxy), answered HTTP 451 from Cloudflare's Moscow node; the browser showed `ERR_BLOCKED_BY_RESPONSE`, whynet gave `geo_blocked` (high).
- zapret running (`general.bat` as admin): `dpi_tool=zapret`; a normal-rights whynet reads the elevated winws.exe's path and full command line (5170 chars, the strategy); discord.com and youtube.com `listed=true`, yandex.ru `excluded`; all three `all_ok`.
