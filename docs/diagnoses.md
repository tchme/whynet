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
| `isp_stub` | ISP stub page | page or final URL matches `markers.yaml` stubs; or, on the plain-HTTP fallback, any ISP's page citing the blocking law plus "restricted/blocked" (`stub_law` + `stub_action`, searched in the whole page: ER-Telecom's text starts after ~70 KB of inline styles) | lab (stub over HTTP after a TLS cut; an unlisted stub known by the law's text); the real Beeline and ER-Telecom pages with their hosts removed from the list both found by "о защите информации" + "ограничен" (2026-11-16); live: rutracker.org on Beeline, `307 → http://blackhole.beeline.ru/` "Ресурс заблокирован" (shows up on some runs only, others time out); ER-Telecom (Dom.ru), linkedin.com and instagram.com `302 → http://lawfilter.ertelecom.ru` "Доступ закрыт" (a friend's report replayed, 2026-11-16) |
| `isp_stub_maybe` | Probably an ISP's stub nobody listed: the real name was cut, plain HTTP led to another site's page (likely) | `http.other_site` (final URL plain HTTP on another registered domain) | lab (redirect to another host; a redirect to the site's own www page is not one) |
| `sni_blocked_router` | Behind a fake-ip router: the site's name is cut, the path itself works (likely) | `dns.match=fakeip`, TLS cut, control SNI ok | table; live: rutracker.org excluded from Podkop, Beeline DPI |
| `proxy_path_broken` | Breaks on the way through a VPN/proxy (usually on the router) | `dns.match=fakeip` and TLS not ok | lab |
| `dpi_conflict` | Two WinDivert bypass tools fight (likely) | `local.dpi_tool=several` and TLS not ok, or cut after the handshake (as `cut_midway`) | table |
| `dpi_not_listed` | zapret runs, but the site is not in its lists (likely) | zapret running, `local.dpi_listed=false`, `tcp.dpi_ipset` not true, TLS cut or cut after the handshake | table; list and address matching unit tests; live, advice included: linkedin.com and rutracker.org on Flex Ltd., `all_ok` once added to `list-general-user.txt` (2026-11-16) |
| `dpi_strategy_fails` | The bypass runs but doesn't get through, or breaks the site (likely) | zapret/GoodbyeDPI running, TLS cut or cut after the handshake | table |
| `dpi_off` | A bypass is installed but not running (likely) | none running, `local.dpi_installed=true`, cut by name | table |
| `sni_blocked` | Blocked by site name (DPI) | TCP ok; real SNI reset/timeout/EOF; control SNI ok | lab (server that resets on one SNI) |
| `sni_suspected` | Probably blocked by name: the real name is cut, the server itself closes a neutral one, so no comparison (likely) | TCP ok; real SNI reset/timeout; `tls.control=closed` | lab (server that resets one name and closes the rest); friends' linkedin.com reports replayed (Beeline timeout, ER-Telecom reset) |
| `cut_midway` | The handshake passes, then the connection is reset on the request (likely: DPI) | TLS ok and `http.status=reset`, or the browser's navigation failed with `ERR_CONNECTION_RESET` | table; live: linkedin.com and rutracker.org on Flex Ltd. (AS21453), 2026-11-16 |
| `isp_dns_down` | ISP's DNS doesn't answer | system DNS timeout/error/empty, DoH ok, site works | lab |
| `ip_unreachable` | Server doesn't answer: blocked by address or down (likely) | TCP timeout/reset/unreachable; or TLS cut with any SNI | lab (black hole) |
| `server_broken` | The site's server is broken | TCP refused, or HTTP 5xx | lab (scripted refused, real 502) |
| `bad_certificate` | Certificate can't be trusted | certificate error, clock fine | lab (wrong name) , table |
| `content_stall` | The page opens, its content freezes after the first KB (likely) | `res.stall` | lab (server that sends 16 KB and goes silent) |
| `content_blocked` | The page opens, some content servers fail (likely) | `res.worst` set | lab (server reset on its name); live: an ad server on habr.com before ads were excluded |
| `content_slow` | Content is throttled (likely) | `res.slow` | not yet |
| `content_late_loss` | Some files come late: packets get lost on the way to their server (likely) | `br.late` and the worst file's time went to connect/TLS, or one of the late servers loses ping tries (`res.late_lossy`) | table |
| `content_late_server` | Some files come late: their server takes long to answer (likely) | `br.late`, phase `wait` | table |
| `content_late` | Some files come late or never (likely) | `br.late`, any other phase (queue, DNS, download, no answer) | table; lab (one stuck image of a feed, a late counter ignored); real Edge against a local server delaying one image 3.5 s (phase `wait`) and never answering another (`none`, 8 s+) |
| `own_blocked` | The person's own browser stopped requests to the page's own servers (likely: an ad blocker or another extension) | `own.blocked_site` (the "own" step from a HAR record) | table; `TestOwnStep`; live: dzen.ru in the author's Opera, its ad blocker stopped dzen's own app bundles (static.dzeninfra.ru): the grey blocks (2026-11-16) |
| `own_failed` | Requests break in the person's browser while whynet's check finds the network fine (likely: the browser's VPN/proxy/secure DNS) | `own.failed` | table |
| `own_late` | Files come late in the person's browser only (likely: the browser's VPN, proxy or "turbo" mode) | `own.late` | table |
| `own_blocked_ads` | The person's browser stops ads and counters only (some sites wait for them) | `own.blocked` | table |
| `own_clean` | Nothing went wrong in the record: it was made while the page worked | `own.clean` | table |
| `content_lossy` | Everything loaded, but a content server loses connects (likely) | `res.lossy` (2+ of 5 tries resent) | table; live: dzen.ru, privacy-cs.mail.ru 4 of 5 connects at ~1000 ms, the rest 7 ms (2026-11-16) |
| `content_flaky` | The browser failed on servers that pass their own check (likely) | `br.worst` set, no `res` failure, not all of them dead links (`res.browser_dead` gives `all_ok`) | lab (dead link case) |
| `geo_blocked` | The site itself refuses your country | `http.geo` (451, or a geo text on a 4xx) | table |
| `rate_limited` | The site answers "too many requests" to this address, often a shared VPN address (likely) | `http.status=429` | lab; a friend's report replayed: instagram.com through his router VPN, his own browser showed HTTP 429 too |
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
| `live_fake_answers` | A VPN client on this PC answers the internet's pings itself (TUN): nothing past the router is measured (likely) | both controls' median under 1 ms (`live.fake_answers`) | table, analysis; a friend's Throne recording (77.88.8.8 and 1.1.1.1 at 0.0 ms, router 0.5 ms; it said `live_stable`, high) |
| `live_vpn` | The path through a VPN on this PC (likely) | `live.path_vpn` and the node beyond or the internet unstable | table, session (VPN adapter on the default route) |
| `live_isp` | The ISP's access network (likely) | the first node beyond the router unstable by itself, confirmed by the internet links | table, analysis (also: a node that only ignores pings to itself is not blamed) |
| `live_isp_network` | Deeper in the ISP's network (likely) | both controls unstable by themselves | table, analysis |
| `live_route` | The route to the server or the server (likely) | the server (or its stand-in) unstable by itself | table, analysis |
| `live_stable` | The network held | nothing else matched (always last) | table, analysis; live: the author's PC on cable, 40 s (2026-11-16) |

## App check verdicts

Rules in `rules/app.yaml` (first match wins), texts under `app_diagnoses` in `locales/ru.yaml`, facts in [architecture.md](architecture.md). "Lab" = `rules/app_lab_test.go` (recorded servers against local servers), "table" = `rules/app_test.go`.

| id | Meaning (plain) | Detected by | Tested |
|---|---|---|---|
| `app_no_traffic_any` | Neither TCP nor UDP went out while recording (UDP recorded) | `app.servers=0`, `app.udp=recorded`, `app.udp_servers=0` | table |
| `app_no_traffic` | The program opened no TCP connection while recording (offline, waiting, or UDP only); UDP not recorded | `app.servers=0`, `app.udp!=recorded` | table, lab |
| `app_dpi_conflict` | Two bypass tools fight | `local.dpi_tool=several`, `app.blocked` | table |
| `app_game_filter_none` | zapret's Game Filter on with IPSet Filter `none`: its game rules reach no address | zapret, Game Filter all/tcp, IPSet Filter none, a blocked server on a Game Filter port | table |
| `app_dpi_not_listed` | zapret runs, a blocked server is in neither of its lists | zapret, `app.dpi_unlisted`, `app.blocked` | table |
| `app_dpi_fails` | A bypass runs, the servers are still cut | zapret/GoodbyeDPI, `app.blocked` | table |
| `app_router_path` | Every blocked server sits behind a fake-ip router: the router's path | `app.blocked_fakeip` | table, lab |
| `app_blocked_name` | Cut by name (DPI) | `app.sni` | table, lab (name from the certificate) |
| `app_blocked_maybe` | Cut, the name can't be tested (unknown name, not TLS, or the server closes other names) | `app.maybe` | table, lab |
| `app_unreachable` | A server doesn't answer on its port | `app.unreachable` | table, lab; live: a test program dialling 192.0.2.1:443 and :2053 (2026-11-16) |
| `app_lossy` | Servers answer but lose connects | `app.lossy` | table |
| `app_flaky` | The program had failed connects, whynet's own check passed | `app.program_trouble` | table, lab |
| `app_udp_no_reply` | The busiest UDP server never answered (blocked UDP: Discord voice in Russia) | `app.udp_main_noreply` | table; unit test on a synthetic stream. Not seen live |
| `app_udp_freezes` | A steady UDP stream froze 2+ times while the program kept sending: lag | `app.udp_freezes` | table; synthetic stream; live, simulated (2026-11-17): Discord voice with the voice server's address routed to a dead gateway on the author's PC (outbound lost, the person rejoined the channel): the server fell back to about one packet a second, 5 freezes up to 1001 ms |
| `app_udp_stopped` | A UDP server answered, then went quiet for good | `app.udp=recorded`, `app.udp_stopped!=0` | table; synthetic stream |
| `app_udp_ok` | TCP fine and the UDP stream steady both ways | `app.udp=recorded`, UDP servers, nothing above | table; live: Discord voice (104.29.136.195:19300, Cloudflare) on the author's PC, CLI and window (2026-11-17) |
| `app_ok` | Every server answers | servers checked, nothing above | table, lab; live: Discord (through the router), Telegram, Spotify, Yandex Messenger on the author's PC (2026-11-16) |
| `unknown` | Nothing matched | always last | table |

`all_ok` matters as much as the failures: it sends the person to the browser (cache, extensions, its own proxy or DNS-over-HTTPS setting).

Still to verify on real networks (plan step 6): `isp_stub` and `dns_spoofed` against real ISP blocking, `https_intercepted`, `dpi_strategy_fails` and `dpi_conflict`. Done: `sni_blocked`, `cut_midway`, `dpi_not_listed` (a friend's network), `geo_blocked`, `hosts_entry`, `clock_wrong`, `proxy_in_way` (the author's PC). The "router VPN off" runs of the plan are dropped: the author's VPN must never be interrupted, and the friend's network has no VPN.

Content checks, verified live 2026-11-15 (Edge 157): youtube.com — 164 browser requests to 9 servers incl. real `rr*---sn-*.googlevideo.com` video servers, 6 content servers checked, `all_ok` in ~9 s; a watch page measured the video server at ~1600 KB/s; habr.com — ~300 requests, `all_ok` in ~17 s on three runs in a row, Google ad servers cut by name and listed as ads.

First run on someone else's network, 2026-11-16: Windows 10 Home 22H2, ISP Flex Ltd. (AS21453), no VPN, Flowseal zapret; ya.ru, rutracker.org, linkedin.com, instagram.com, discord.com and a youtube.com video, each with zapret running and stopped (reports kept by the author, not in the repo). The browser step works on Windows 10.
- zapret stopped: rutracker.org (TLS reset), discord.com and youtube.com (TLS timeout) `sni_blocked`, control SNI ok; linkedin.com handshake ok, request reset (`unknown` then, now `cut_midway`); instagram.com opens directly (the ISP's DNS gives a working Meta address; the DoH address times out), `all_ok`; ya.ru `all_ok`.
- discord.com without zapret: the TLS probe timed out, but the HTTP step's later connection (http:// → 301 → https://) got the page: this DPI does not cut every connection.
- zapret running: discord.com, instagram.com, ya.ru `all_ok`; rutracker.org handshake ok, the second request reset (`unknown` then; "not in the lists" by name, though Flowseal's `ipset-all.txt` holds Cloudflare's 172.67.128.0/17, now `tcp.dpi_ipset`); linkedin.com: whynet's request got a reCAPTCHA page, the browser's navigation was reset, yet `all_ok` (now `dpi_not_listed`, or `dpi_strategy_fails` when Flowseal's ipset is in "any" mode); youtube.com: 6 failed requests to a googlevideo node neither DNS knows gave `content_flaky` "though the servers passed their own check" (now `all_ok` via `res.browser_dead`).
- Re-run with 0.1.2 (zapret running, Flowseal 1.10.4, its menu showed IPSet Filter `[none]`, Game Filter `[disabled]`): linkedin.com `dpi_not_listed` (handshake ok, request reset), rutracker.org `dpi_not_listed` (this time the TLS was reset: the earlier passing handshake was the DPI's own inconsistency, not zapret), youtube.com `all_ok` (no failed requests this time, so `res.browser_dead` was not seen live). `tcp.dpi_ipset=false` for Cloudflare and Google addresses matches the `none` mode. After the friend added both sites to `list-general-user.txt` and restarted zapret: both `all_ok`.

Two more friends, v0.3.0, 2026-11-16 (reports kept by the author): Beeline (AS8402), Windows 10, no Edge or Chrome on the PC, a router VPN with fake-ip; ER-Telecom/Dom.ru (AS56330, Kurgan), Windows 11, a router VPN. Each ran the same sites with the VPN on and off and said what opened in their own browser.
- Matched: ya.ru `all_ok` everywhere; rutracker.org, discord.com, youtube.com `sni_blocked` without the VPN on both ISPs; with the VPN discord.com and rutracker.org `all_ok`; instagram.com through the Beeline router VPN `content_blocked` on `scontent.cdninstagram.com` (the router's list has instagram.com only, most likely). Both stability runs `live_stable` on cable.
- Fixed (see bugs.md): ER-Telecom's stub not known, linkedin.com's `ip_unreachable` (`sni_suspected` now), HTTP 429 `unknown` (`rate_limited` now), the browser step skipped without a reason.
- Open: on Beeline without a VPN the person says instagram.com opens and youtube.com shows its main page with previews (no video), while whynet says `sni_blocked` for both: TLS over TCP times out with the real name on both DNS answers. Guess, not checked: the browser reaches them over QUIC (HTTP/3, UDP), which this DPI doesn't cut; to check: the same browser with QUIC off (`chrome://flags`, Experimental QUIC protocol). Also open: linkedin.com through the ER-Telecom router VPN gives `sni_blocked` with "VPN will help" while the VPN is on (that router doesn't route the site; nothing on the PC shows the router VPN for a site it doesn't route); the stability "beyond the router" lane on ER-Telecom pinged 172.21.191.252, which answers the trace but no direct ping, so the lane was silent (100 % lost) the whole run.

A third friend, 2026-11-17 (v0.3.0): MTS (AS43148, Kurgan), Windows 11 26H2, Flowseal zapret 1.10.3 (IPSet Filter none, Game Filter disabled; Windows denied reading winws.exe's path, the folder was found anyway), the Throne VPN client (sing-box TUN adapter `throne-tun`, "sing-tun Tunnel"; not recognised then, now in `vpn_adapters`). Three sets: nothing, zapret, VPN and zapret; no word on what opened in his browser.
- Nothing on: discord.com, instagram.com, rutracker.org, youtube.com `sni_blocked`; linkedin.com `cut_midway`; ozon.ru and nexusmods.com `bot_check`; twitch.tv, ya.ru `all_ok`.
- zapret: rutracker.org, linkedin.com `dpi_not_listed`; instagram.com `ip_unreachable` (the system DNS gave 31.13.72.174, which MTS drops at TCP; the DoH address answers TCP and is cut by name).
- VPN and zapret: instagram.com HTTP 429 (`unknown` then, `rate_limited` now); linkedin.com `content_blocked` (platform.linkedin.com, li.protechts.net cut by name: the VPN client routes some names only); youtube.com `content_late` on Google's noisy files (see bugs.md, now `all_ok`).

Verified live 2026-11-15 with Flowseal 1.9.9d on the Desktop:
- zapret stopped, driver still loaded: `dpi_tool=none`, `windivert=running`, folder found from the driver path, discord.com listed.
- `geo_blocked`: an artifact frame host on `claudeusercontent.com`, reached directly (not through the router proxy), answered HTTP 451 from Cloudflare's Moscow node; the browser showed `ERR_BLOCKED_BY_RESPONSE`, whynet gave `geo_blocked` (high).
- zapret running (`general.bat` as admin): `dpi_tool=zapret`; a normal-rights whynet reads the elevated winws.exe's path and full command line (5170 chars, the strategy); discord.com and youtube.com `listed=true`, yandex.ru `excluded`; all three `all_ok`.
