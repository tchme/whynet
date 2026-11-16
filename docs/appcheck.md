# App check (design)

Status: draft 2026-11-16, waiting for the author's approval. Stage 1 (TCP, no admin rights) is designed here; stage 2 (UDP, admin rights) shares the program picker with the stability check's stage 2 ([stability.md](stability.md)) and is not designed here.

## Intent

"Discord (a game, a launcher) doesn't connect, or works only partly." A site check can't answer this: the program talks to its own servers, on its own ports, by names the person never sees. The app check lets the person pick the running program, records where it connects while they repeat what fails, then checks each of those servers the way whynet checks a site's content servers and says which ones are blocked or unreachable, why, and what to do. Everything goes into the AI report.

The author picked this as the next stage (2026-11-16): games and their stability matter to them, and in Russia the programs that break are mostly blocked services (Discord, launchers, game stores). Stage 1 answers "it doesn't connect"; "it lags in the game" needs UDP and is stage 2.

## Goals

- Pick a running program from a list; record its TCP connections for as long as the person needs (until "Готово", 2 min at most); no admin rights.
- Name each server it talked to when possible, and say who owns the network (AS).
- Check each server ourselves: does it answer on that port, does it lose packets, is it cut by name, does zapret cover it.
- Use what the program itself went through as evidence: connections stuck waiting for an answer, connections cut right after they opened.
- One verdict with advice, a servers table in Details, everything in the AI report, the same privacy masking as the site check.
- The console: `whynet-cli -app <program> [-for 30s]`.

## Non-goals

- UDP (game traffic itself, Discord voice): stage 2, admin rights.
- Fixing anything: no changes to zapret lists, the router or Windows; advice only.
- Browsers: picking one says "для браузера — проверка сайта" and offers the site mode.
- Background monitoring: it records only while its screen says so.
- Packet capture or payloads.

## Anti-goals (failure even if it works)

- Blaming a server the program only touched in passing (an ad or analytics server, a CDN probe) for the program not working.
- A confident verdict on a server whose name is unknown: without a name, "cut by name" can't be told apart, so it says "probably" or only "doesn't answer".
- Needing admin rights for the core answer.
- Touching a VPN, a route or any setting; traffic beyond the program's own servers and the lookups whynet already does.

## What was measured (2026-11-16, the author's PC: Windows 11 26H2, fake-ip router)

- The TCP table with owning processes (`Get-NetTCPConnection`, i.e. `GetExtendedTcpTable`) is readable without admin. Opera, Discord, Telegram, Spotify, Yandex Messenger and others showed 3-24 connections each; behind the router most remote addresses are fake-ip (198.18.x.x).
- The Windows DNS cache is **not** readable without admin on this PC (`Get-DnsClientCache` empty, `ipconfig /displaydns`: "requires elevation"). It probably is on Windows 10 (not checked).
- The router gives no PTR for fake-ip addresses; real addresses have PTR rarely and with internal names (`xiva-daria.stable.qloud-b.yandex.net` for Yandex Messenger).
- A TLS handshake **without SNI** returns a certificate that names the service for many servers, through the fake-ip router too: Discord `discord.gg` (443) and `discord.media` (2053, 2096), Spotify `*.spotify.com`, Razer `*.razer.com`. It fails for Cloudflare-fronted services (ChatGPT, Claude: handshake failure) and for non-TLS protocols (Telegram's MTProto on 443, Spotify on 4070).
- Ports are not only 443: Discord 2053/2096, Spotify 4070, Google 5228.

## Approaches

| | Approach | Trade-off |
|---|---|---|
| **A (picked)** | Record the program's connections, then check every server ourselves; the program's own experience (stuck, cut) is evidence too | The same logic as the site check: the program shows what got stuck, our checks say why. More work than B or C. |
| B | Observe only: judge from the TCP table | Fast, but can't tell a cut by name from any other drop and knows no names: weak verdicts |
| C | List the servers with names and owners for the AI, no verdict | A quick draft; breaks "rules decide" |

The author picked A (2026-11-16).

## How it works

### 1. Pick a program

A list of running programs: those with a visible window and those with TCP connections right now, windowed ones first; one row per program (exe name, window title), not per process. All processes of that exe and their child processes count as the program (Discord, launchers and Electron apps run several). System processes (`svchost` and the like) are not listed. A browser in the list leads to the site check instead.

### 2. Record

While recording, the screen shows the servers as they appear. Every 250 ms whynet reads the TCP table (`GetExtendedTcpTable`, `TCP_TABLE_OWNER_PID_ALL`, IPv4 and IPv6) and keeps, per remote address and port: when first and last seen, how many separate connections, the states seen, whether a connection stayed in `SYN_SENT` (sent, no answer) for more than 3 s, how many connections were cut within 2 s of being established, the longest one. Loopback and LAN addresses are dropped; fake-ip addresses are marked. Recording ends with "Готово" (5 s at least) or after 2 min.

Short connections can fall between two reads; that is fine: whynet checks every server it saw at least once itself, and blocked connects wait long in `SYN_SENT`.

### 3. Name and owner

For each server, the first source that gives a name: the DNS cache (`DnsGetCacheDataTable`, then a cache-only query per name; skipped when Windows refuses), the certificate from a handshake without SNI (the first name; a wildcard `*.discord.gg` gives `discord.gg`), the PTR record. Otherwise "name unknown". The owner network (AS and its name) through the lookup whynet already has (`probe.ASN`); for a fake-ip address, through the real address of its name when the name is known (DoH).

### 4. Check each server

Up to 20 servers, troubled ones first (stuck or cut by the program), then by traffic; ads and counters (the ad list) are checked last and never blamed.

- Connect on the server's own port, 5 tries: answer time and lost tries (the content check's `ping`).
- With a name and a TLS answer: a handshake with the name and one with the neutral name, as the site check does (`sni` / `ip` / `closed` / ok).
- zapret: the name against its lists, the address against its ipset (and its IPSet Filter mode), the port against Game Filter ports.
- Behind a fake-ip router: the router answers the connect itself, so the answer time is a handshake (as the content check does).

### 5. Verdict

Facts `app.*` from the steps above go through a separate rules file (`rules/app.yaml`, like `live.yaml`), first match wins. A first set, to settle against real runs:

| id | When | Says |
|---|---|---|
| `app_no_traffic` | no TCP connections during the recording | the program made no TCP connections: it may be offline, waiting, or using UDP only (stage 2); repeat the action while recording |
| `app_dpi_conflict`, `app_dpi_not_listed`, `app_dpi_fails` | a bypass runs and a server is cut | as the site verdicts, per server |
| `app_game_filter_none` | Game Filter on, IPSet Filter `none`, the program's ports in the Game Filter range | Flowseal's game sections reach no address in this mode |
| `app_blocked_name` | a non-ad server cut by name | names the server(s); VPN or bypass |
| `app_blocked_maybe` | a server stuck or cut, name unknown, the path to the address open | probably blocked; VPN or bypass |
| `app_unreachable` | a server doesn't answer on its port at all | blocked by address or down |
| `app_router_path` | everything goes through the fake-ip router and breaks there | the router's VPN path; add the names to its list |
| `app_lossy` | servers answer but lose packets | unstable path; the stability check |
| `app_ok` | all servers answer, nothing stuck or cut | the network to its servers is fine; if it lags in the game, that is UDP: the stability check |
| `unknown` | none of the above | save the AI report |

### 6. Report and window

- AI report: the program (exe name; its path masked like other personal paths), the recording's length, a servers table (name and how it was found, address, port, owner, what the program saw, what whynet's check found, zapret coverage), the verdict, raw JSON.
- The window: a third mode "Программа" next to "Сайт" and "Стабильность": the picker, the recording screen with the servers appearing live, the result (verdict card, servers table in Details, the AI report buttons). An HTML mockup comes before any UI code, as for the other two modes.

## Constraints

- Windows 10 and 11, no admin rights, no new dependencies: `GetExtendedTcpTable` (`iphlpapi`), `DnsGetCacheDataTable` (`dnsapi`), process names through the snapshot `dpi_windows.go` already uses.
- Every step has its own timeout and panic recovery; a refused DNS cache or a failed name lookup never stops the check.
- Never interrupts a VPN, never changes a setting.
- Facts in probes, decisions in rules, texts in `locales/ru.yaml`.

## Calls made by the designer (the author may change them)

- Recording until "Готово", 2 min at most, 5 s at least.
- Up to 20 servers checked, troubled ones first.
- A separate rules file `rules/app.yaml` instead of more rows in `diagnoses.yaml`: the facts are per program, not per site.
- Browsers are pointed to the site check rather than checked as programs.
- The window title is shown only in the picker and never goes into the report (a Discord channel or a document name can be personal); the report names the exe.

## Left to the builder

Exact thresholds (stuck after 3 s, cut within 2 s) after real runs, the poll interval if 250 ms costs too much CPU, fact names, verdict texts, the window layout after the mockup, how child processes are found.
