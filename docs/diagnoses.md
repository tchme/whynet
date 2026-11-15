# Diagnoses catalog

Every diagnosis whynet can give: how it is detected (facts) and how to reproduce it for testing. Rules live in `rules/diagnoses.yaml`, texts in `locales/ru.yaml`. Status: planned for v1, none implemented yet.

| id | Meaning (plain) | Detected by | Reproduce |
|---|---|---|---|
| `no_internet` | No internet at all | no route; every probe of known hosts fails | disable the network adapter |
| `no_such_site` | Site doesn't exist / typo | NXDOMAIN from system DNS and from DoH | check `doesnotexist-whynet.com` |
| `isp_dns_down` | ISP's DNS doesn't answer | system DNS timeout/error, DoH ok | point the adapter at a dead DNS server |
| `dns_spoofed` | ISP substitutes the site's address | system and DoH answers differ; system IP has no valid cert for the domain | lab fake network (a `hosts` entry is caught earlier as `local_override`) |
| `sni_blocked` | Blocked by site name (DPI) | TCP ok; TLS with real SNI reset/timeout; control SNI ok | a site blocked by the author's ISP; lab TLS server that cuts on a given SNI |
| `ip_unreachable` | Address unreachable (probably blocked, or the server is down) | TCP timeout on every IP; other sites fine | firewall rule blocking the IP; lab fake network |
| `isp_stub` | ISP stub page | HTTP answer matches `rules/stubs.yaml` | a blocked site over plain HTTP; lab stub server |
| `geo_blocked` | The site itself refuses your country | 403/451 or known geo-block markers from the origin, TLS ok | a site known to block the author's country |
| `server_broken` | The site's server is broken | 5xx, or TCP refused | lab server returning 502; closed port |
| `clock_wrong` | Computer clock is wrong | cert "not yet valid / expired" plus clock skew > 5 min | set the clock a year ahead |
| `https_intercepted` | HTTPS intercepted by antivirus/other software | cert chain ends in a non-public, locally trusted root | an antivirus with HTTPS scanning on |
| `local_override` | System proxy or `hosts` entry in the way | `local.proxy=on` with a failing proxy, or `local.hosts_entry=true` | dead proxy in Windows settings; line in `hosts` |
| `all_ok` | Everything works; the problem is in the browser | every step ok | any working site |

`all_ok` matters as much as the failures: it sends the person to the browser (cache, extensions, its own proxy or DNS-over-HTTPS setting).
