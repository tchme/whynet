# Diagnoses catalog

Every diagnosis whynet can give: how it is detected and how it is tested. Rules live in `rules/diagnoses.yaml` (order matters: first match wins), texts in `locales/ru.yaml`, fact keys in [architecture.md](architecture.md). "Lab" = `rules/lab_test.go` (fake network with real local servers), "table" = `rules/rules_test.go` (facts straight into rules).

| id | Meaning (plain) | Detected by | Tested |
|---|---|---|---|
| `no_internet` | No internet at all | `local.net=none`; or system DNS failing and no DoH reachable (likely) | table |
| `hosts_entry` | The site is rewritten in the `hosts` file | `local.hosts_entry=true` | table; unit test of the parser |
| `no_such_site` | Site doesn't exist / typo | NXDOMAIN from system DNS and DoH (likely, if DoH is unreachable) | lab, table; live: `doesnotexist-whynet-test.com` |
| `clock_wrong` | Computer clock is wrong | certificate error plus clock skew > 5 min | table |
| `dns_spoofed` | ISP substitutes the site's address | system and DoH differ, real address fails, DoH address works (`tls.alt=ok`); or ISP NXDOMAIN while DoH works (likely) | lab, table |
| `isp_stub` | ISP stub page | page or final URL matches `markers.yaml` stubs | lab (stub over HTTP after a TLS cut) |
| `proxy_path_broken` | Breaks on the way through a VPN/proxy (usually on the router) | `dns.match=fakeip` and TLS not ok | lab |
| `dpi_conflict` | Two WinDivert bypass tools fight (likely) | `local.dpi_tool=several` and TLS not ok | table |
| `dpi_not_listed` | zapret runs, but the site is not in its lists (likely) | zapret running, `local.dpi_listed=false`, TLS cut | table; list matching unit test |
| `dpi_strategy_fails` | The bypass runs but doesn't get through, or breaks the site (likely) | zapret/GoodbyeDPI running, TLS cut | table |
| `dpi_off` | A bypass is installed but not running (likely) | none running, `local.dpi_installed=true`, cut by name | table |
| `sni_blocked` | Blocked by site name (DPI) | TCP ok; real SNI reset/timeout/EOF; control SNI ok | lab (server that resets on one SNI) |
| `isp_dns_down` | ISP's DNS doesn't answer | system DNS timeout/error/empty, DoH ok, site works | lab |
| `ip_unreachable` | Server doesn't answer: blocked by address or down (likely) | TCP timeout/reset/unreachable; or TLS cut with any SNI | lab (black hole) |
| `server_broken` | The site's server is broken | TCP refused, or HTTP 5xx | lab (scripted refused, real 502) |
| `bad_certificate` | Certificate can't be trusted | certificate error, clock fine | lab (wrong name) , table |
| `content_stall` | The page opens, its content freezes after the first KB (likely) | `res.stall` | lab (server that sends 16 KB and goes silent) |
| `content_blocked` | The page opens, some content servers fail (likely) | `res.worst` set | lab (server reset on its name); live: an ad server on habr.com before ads were excluded |
| `content_slow` | Content is throttled (likely) | `res.slow` | not yet |
| `content_flaky` | The browser failed on servers that pass their own check (likely) | `br.worst` set, no `res` failure | not yet |
| `geo_blocked` | The site itself refuses your country | `http.geo` (451, or a geo text on a 4xx) | table |
| `https_intercepted` | HTTPS intercepted by antivirus/other software (likely) | interceptor marker in the chain | table |
| `proxy_in_way` | Works directly, but a system proxy is on (likely) | `local.proxy=on`, site ok | table |
| `all_ok` | Everything works; the problem is in the browser | TLS ok and 2xx/3xx, or a Cloudflare challenge | lab, table; live: youtube.com, github.com |
| `unknown` | whynet can't tell; save the AI report | no other rule matched (always last) | table |

`all_ok` matters as much as the failures: it sends the person to the browser (cache, extensions, its own proxy or DNS-over-HTTPS setting).

Still to verify on real networks (plan step 6): `sni_blocked`, `isp_stub` and `dns_spoofed` against real ISP blocking, `geo_blocked`, `https_intercepted`, `clock_wrong`, `proxy_in_way`, and the DPI-bypass failure diagnoses (`dpi_*`) on a site zapret actually fails on.

Content checks, verified live 2026-11-15 (Edge 157): youtube.com — 164 browser requests to 9 servers incl. real `rr*---sn-*.googlevideo.com` video servers, 6 content servers checked, `all_ok` in ~9 s; a watch page measured the video server at ~1600 KB/s; habr.com — ~300 requests, `all_ok` in ~17 s on three runs in a row, Google ad servers cut by name and listed as ads.

Verified live 2026-11-15 with Flowseal 1.9.9d on the Desktop:
- zapret stopped, driver still loaded: `dpi_tool=none`, `windivert=running`, folder found from the driver path, discord.com listed.
- `geo_blocked`: an artifact frame host on `claudeusercontent.com`, reached directly (not through the router proxy), answered HTTP 451 from Cloudflare's Moscow node; the browser showed `ERR_BLOCKED_BY_RESPONSE`, whynet gave `geo_blocked` (high).
- zapret running (`general.bat` as admin): `dpi_tool=zapret`; a normal-rights whynet reads the elevated winws.exe's path and full command line (5170 chars, the strategy); discord.com and youtube.com `listed=true`, yandex.ru `excluded`; all three `all_ok`.
