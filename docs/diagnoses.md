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
| `geo_blocked` | The site itself refuses your country | `http.geo` (451, or a geo text on a 4xx) | table |
| `https_intercepted` | HTTPS intercepted by antivirus/other software (likely) | interceptor marker in the chain | table |
| `proxy_in_way` | Works directly, but a system proxy is on (likely) | `local.proxy=on`, site ok | table |
| `all_ok` | Everything works; the problem is in the browser | TLS ok and 2xx/3xx, or a Cloudflare challenge | lab, table; live: youtube.com, github.com |
| `unknown` | whynet can't tell; save the AI report | no other rule matched (always last) | table |

`all_ok` matters as much as the failures: it sends the person to the browser (cache, extensions, its own proxy or DNS-over-HTTPS setting).

Still to verify on real networks (plan step 6): `sni_blocked`, `isp_stub` and `dns_spoofed` against real ISP blocking, `geo_blocked`, `https_intercepted`, `clock_wrong`, `proxy_in_way`, and the DPI-bypass failure diagnoses (`dpi_*`) on a site zapret actually fails on.

Verified live 2026-11-15 with Flowseal 1.9.9d on the Desktop:
- zapret stopped, driver still loaded: `dpi_tool=none`, `windivert=running`, folder found from the driver path, discord.com listed.
- zapret running (`general.bat` as admin): `dpi_tool=zapret`; a normal-rights whynet reads the elevated winws.exe's path and full command line (5170 chars, the strategy); discord.com and youtube.com `listed=true`, yandex.ru `excluded`; all three `all_ok`.
