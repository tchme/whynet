# Windows network health (design)

Status: approved 2026-11-17 with the mockup `docs/mockups/health.html` ("макет ок"); the author agreed to the heavy reset as stage 2 and the name "Сеть Windows". Roadmap item 2 in [design.md](design.md). Stage 1 BUILT 2026-11-17 (`internal/health`, `rules/health.yaml`, `app_health.go`, `internal/report/health.go`, `whynet-cli -health`); plan `~/.claude/plans/whynet-health.md`. See "Built: stage 1" at the end for what differs from the design and what was verified.

## Intent

"Something on this PC doesn't work and I don't know what" — the moment the author used to answer by typing `ipconfig /flushdns`, `netsh winsock reset` and the like one after another, blind. The site check looks at one site; this mode looks at Windows itself: the adapter, its address and route, DNS, proxy, hosts, the TCP stack, the clock. One button checks everything, the screen lists what is wrong, and one more button fixes what was found, with the old values saved and one-click undo. Then the person goes back to what failed and tries again.

The author picked approach A on 2026-11-17: the same "one button" feeling as the forum combo, but only fixes for problems the check actually found, everything undoable, and the heavy forum resets kept behind a second step with an honest list of what they would break on this PC.

## Goals

- One check of the PC's network setup, ~15 s, no admin rights.
- A list of findings, each one: what is wrong, how sure we are, what it breaks, what to do.
- "Починить всё найденное" applies the fixes for the findings on screen plus a safe cache cleanup; one UAC prompt at most for the whole batch, through the existing helper.
- Every change saves the old value first; "Вернуть как было" restores the last batch.
- After the fixes the check runs again and shows what changed; buttons lead to the site check and the app check to try the failing thing again.
- The AI report for the PC's network setup, with the same masking as the other reports.
- The console: `whynet-cli -health [-json] [-save]` (read-only).

## Non-goals

- Optimising a healthy PC ("ускоритель интернета"): no tweak is offered without a finding behind it.
- Changing DNS servers to public ones, editing routes, touching VPN adapters.
- Background monitoring or scheduled fixes.
- Fixing the router or the ISP: those get advice only.

## Anti-goals (failure even if it works)

- **Breaking a VPN or a bypass.** VPN adapters (TUN/TAP/WireGuard/sing-tun/OpenVPN DCO, the markers whynet already has) and their routes are never touched; DNS is never switched away from what the router hands out (a fake-ip router like the author's would cut the PC off its VPN).
- **"Fixing" a deliberate setting.** A lowered MTU, a hosts list from a zapret pack, telemetry blocks in hosts, a stopped-but-manual service: shown, never reverted unless the check proves it breaks something.
- **A change without undo,** except caches (nothing to restore) and the heavy reset (stage 2, restores what it can and says what it can't).
- **Running anything the person didn't see first:** the batch is listed before the button is pressed, the UAC prompt names whynet, the result of each step is shown.
- **Saying "fixed" when the re-check still fails.**

## What was measured (2026-11-17, the author's PC: Windows 11 26H2, cable, fake-ip router, all without admin)

- Readable: adapters with link speed (`Get-NetAdapter`: Realtek 2.5GbE at 1 Gbps), error and discard counters (1953 discards of 85 M packets: normal, so discards alone are no finding), `netsh int tcp show global` (autotuning `normal`, RSS on), WinHTTP proxy (direct), service states, the Winsock catalog (Microsoft entries only), Windows' own connectivity verdict per profile (`IPv4Connectivity Internet`, `IPv6Connectivity NoTraffic`), default routes and metrics, DNS servers per adapter, MTU per interface, the hosts file.
- Not readable: adapter power management (`Get-NetAdapterPowerManagement` fails on this NIC); the DNS cache (`ipconfig /displaydns` needs elevation, see appcheck.md).
- **hosts holds 289 active lines**: 200 names pinned to one Cloudflare address (104.25.158.178), 36 to a Telegram address (149.154.167.220), Steam/Akamai pins: a zapret-pack style hosts list. Pins go stale when the service moves, and the site then breaks silently. This is the strongest real-world case found.
- **Ethernet MTU 1400** (default 1500), set by someone on purpose: an example of a deliberate setting to show, not fix.
- `W32Time` is Stopped with start type Manual and `NlaSvc` is Stopped/Manual: normal on Windows 11 (trigger-start), so a stopped service alone is no finding; a wrong clock is.
- IPv6: only ULA (`fd39::/16`) and link-local addresses, no `::/0` route: not broken, just no IPv6.
- OpenVPN TAP and DCO adapters installed, disconnected.

## Approaches

| | Approach | Trade-off |
|---|---|---|
| **A (picked)** | "Doctor": check, list findings, fix only what was found plus a safe cleanup, undo everything; heavy reset behind a second step | The forum combo's convenience without its damage; more work: a fix and an undo per finding |
| B | One "reset everything" button running the forum combo (flushdns, release/renew, winsock reset, int ip reset, reboot) | Familiar and simple, but blind: wipes manual IP/DNS/MTU, can drop VPN clients, no undo; against "never interrupt a VPN" |
| C | Check and advise, no fixes (copyable commands) | Safest, little use to a person who wants "press, fixed" |

## How it works

### 1. Checks

All run in parallel with their own timeouts and panic recovery; ~15 s in total (the error counters are read at the start and the end). Each check records raw facts (`health.*`) and evidence for the report even when nothing is wrong.

| Area | What is read | Finding when | Fix (tier) |
|---|---|---|---|
| Address | each physical adapter that is up: address, gateway, DHCP | 169.254.x.x (no answer from DHCP) or no gateway | new address from the router for that adapter only (admin) |
| Route | default routes, metrics, adapter kinds | the default route goes through a VPN adapter whose program is not running (a leftover after a crash) | advice: start or reconnect the VPN client, or restart the PC; never touched |
| Cable / Wi-Fi | link speed, error counters (start vs end), Wi-Fi signal and band (`internal/live` already reads WLAN) | wired at 100 Mbps or less ("probably the cable or the port"); errors growing during the check; weak Wi-Fi | advice |
| DNS servers | each adapter's servers, manual or from DHCP; 3 queries each with a timeout | a server doesn't answer or is slow | manual server dead and the DHCP one works: back to automatic for that adapter (admin, undo); otherwise advice (router) |
| DNS service | `Dnscache` state | not running | start it (admin) |
| hosts | active lines: pins (name → real address) and blocks (0.0.0.0/127.0.0.1); pins grouped by address | a pinned address doesn't answer on 443, or answers with a certificate not covering the name (stale pin); a block of Windows' connectivity check names | stale pins: comment them out with a `# whynet <date>` mark, backup copy first (admin, undo); blocks: advice only |
| Proxy | WinINET (HKCU: on, server, PAC), WinHTTP | the proxy is on and nothing answers on its address (a VPN/proxy client closed and left it on), or the PAC script can't be fetched | turn it off (WinINET: no admin; WinHTTP: admin; undo) |
| TCP stack | `netsh int tcp show global`, the Winsock catalog | autotuning not `normal`, RSS off (game "boosters" do this); non-Microsoft Winsock entries (named by their DLL) | autotuning / RSS back (admin, undo); Winsock: advice, heavy reset in stage 2 |
| IPv6 | global addresses, `::/0` route, Windows' IPv6 connectivity | a global IPv6 address with no working route: programs may try IPv6 first and wait | turn IPv6 off on that physical adapter (admin, undo) |
| MTU | MTU per adapter; a don't-fragment ping to the internet at that size | path smaller than the adapter's MTU (some sites hang) | advice (the MTU may be deliberate, see "measured") |
| Clock | skew against the internet (the site check's measure), `W32Time` | skew over 2 min | start time sync and resync now (admin); undo returns the service's previous state |
| Windows' "no internet" | profile connectivity vs whynet's own reachability | Windows says "no internet" while the internet works: some programs (Store, Office, games' launchers) then refuse to go online | advice naming the likely cause found above (hosts block, proxy, DNS) |
| Interfering programs | processes/services from a list in `rules/markers.yaml`: Killer, SmartByte, cFosSpeed, NetLimiter; non-Microsoft adapter bindings; WinDivert (the DPI facts) | a known throttler runs | advice; WinDivert and bindings are info for the report |

### 2. Findings

Facts go through `rules/health.yaml`. Unlike the other modes, **every** matching rule fires: the screen lists all findings, not one verdict. Each rule has an id, its facts, a kind (`problem` — something is broken; `advice` — likely a problem we can't fix from here; `info` — worth knowing, shown folded) and an optional fix id. The texts live in `locales/ru.yaml`. The top line sums up: "Нашёл 2 проблемы и 1 совет" or "С сетью Windows всё в порядке".

### 3. Fixing

- The button says what it will do: "Починить всё найденное (2)"; with no fixable findings it is "Почистить кэш" (the safe cleanup alone), so the person always has their one button.
- **Safe cleanup**, always in the batch: flush the DNS cache (`DnsFlushResolverCache`) and the neighbour (ARP) cache (`FlushIpNetTable2`, admin). No undo: there is nothing to restore; the button's hint says so.
- Fixes run in a fixed order (proxy, hosts, DNS, stack, address, clock: least disruptive first); a failed one doesn't stop the rest; each shows ✓ or ✗ with the error.
- **Admin**: if any fix in the batch needs it, one UAC prompt starts the existing helper with a new job, `whynet.exe -helper <addr> <key> fix 60`. After the key, the window sends the batch as one JSON line; the helper runs only actions from a fixed list with validated parameters (adapter by its LUID and never a VPN adapter; enum values; hosts lines matched by their exact text), never a shell string, reports each result, and exits. UAC declined: the no-admin fixes still run, the rest stay listed with "нужны права администратора".
- **Undo**: before the batch, the old values go to `%LOCALAPPDATA%\whynet\health\<time>.json` (plus the hosts backup next to it). "Вернуть как было" restores the last batch through the same path. It stays offered while that file exists.
- **Re-check**: after the batch the checks run again; the screen shows what went away, what stayed, and buttons "Проверить сайт" and "Проверить программу".

### 4. Stage 2: heavy reset

Behind "Не помогло?" under the findings: `netsh winsock reset` and `netsh int ip reset` (+ IPv6), which need a reboot and wipe manual settings. Before it runs, whynet lists what **this PC** would lose, from a snapshot it takes first: manual IP addresses and DNS servers per adapter, MTU, interface metrics, the TCP globals, non-Microsoft Winsock entries (and which program installed them). The person confirms; whynet runs it through the helper and offers to reboot now or later. On the next start the mode offers "Вернуть ручные настройки" from the snapshot. Windows' "Network reset" (`netcfg -d`) is never offered: it removes VPN adapters.

### 5. Report and window

- AI report: the PC (OS, adapters with kinds, masked as elsewhere), every check with its raw values (ok ones too), the findings, the batch that ran and each result, the re-check, raw JSON. hosts: counts and the stale pins, not the whole file.
- The window: a fourth mode "Сеть Windows" next to "Сайт", "Стабильность" and "Программа"; screens as in `docs/mockups/health.html`: start, checking (the areas tick off live), findings, fixing, result after the re-check, undo.
- Later, not in this design: the site check's verdicts that point at the PC (`proxy_in_way`, `clock_wrong`, `hosts_entry`, `isp_dns_down`) get a "Проверить сеть Windows" button.

## Constraints

- Windows 10 and 11; the check itself without admin; no new dependencies (iphlpapi, dnsapi, registry, `netsh` started without a shell, as the probes already do).
- Every check has its own timeout and panic recovery; one failing check never hides the others.
- Facts in probes, decisions in rules, texts in `locales/ru.yaml`.
- Nothing touches a VPN adapter, a route or the DNS a router hands out.

## Calls made by the designer (the author may change them)

- All matching findings are shown, not the first match (a PC can have three independent problems).
- Stage 1 = checks, the safe cleanup, targeted fixes, undo, the report, the CLI; the heavy reset is stage 2, so stage 1 ships without the riskiest part.
- The CLI only checks; fixing is in the window, where the batch is seen before it runs.
- hosts: only stale pins are fixed, by commenting out with a backup; a zapret pack may rewrite hosts on its next update — then the pins come back and the advice says to update the pack's hosts list instead.
- A stopped service is a finding only when its job is visibly broken (a wrong clock, no DNS service), never by itself.
- Thresholds: clock 2 min, wired link 100 Mbps, DNS 3 queries.
- The mode's name "Сеть Windows".

## Left to the builder

Whether `DnsFlushResolverCache` and DHCP renew need admin on Windows 10 / 11 (measure; the tier follows), reading adapter power saving without admin (drop the check if it can't be read), the exact check list order on screen, hosts pin sampling (how many names per pinned address), fact names, finding texts, timeouts, the helper's `fix` job format.

## Built: stage 1 (2026-11-17)

Measured before building (the author's PC, no admin): `DnsFlushResolverCache` works without admin; `FlushIpNetTable2` is refused without it (so the ARP cache is flushed only when the batch already runs elevated: no UAC prompt for it alone); `GetNetworkConnectivityHint` and `WinHttpGetDefaultProxyConfiguration` work without admin. `netsh int tcp show global` prints labels in the Windows language (Russian on the author's PC) but English values in a fixed line order, so it is read by order and value; the Winsock catalog is read from the registry (`Protocol_Catalog9\Catalog_Entries*\PackedCatalogItem`, the provider path first) instead of netsh. The adapter's own MTU (`IP_ADAPTER_ADDRESSES.Mtu`) stays 1500 when a person lowers the IPv4 MTU: the check reads the IPv4 interface's `NlMtu` (`GetIpInterfaceEntry`).

Differences from the design (the builder's calls):

- IPv6 "address but no route": advice only (turning IPv6 off on one adapter has no clean API without PowerShell).
- "Default route through a VPN adapter whose program is not running": not detected (no reliable sign that the VPN program is gone); VPN adapters are listed in the report.
- MTU: a fact and an info finding; no don't-fragment path test.
- Starting the DNS Client service has no undo (stopping it again helps nobody); a new DHCP address, time resync and the caches have nothing to undo either.
- hosts pins: one TCP connect per pinned address, then a TLS handshake per name (up to 400 names, 16 at a time, 4 s each). Stale = the address doesn't answer (dead), answers with a certificate for another name (other_cert), or answers TLS for no name (no_tls). Cut only with the real name = DPI, not stale; an untrusted certificate (interceptor, clock) is not stale either. A line is stale only when every name on it is.
- Findings show all matching rules (`rules.MatchAll`); a rule has `kind`, `area`, `fix`. A `"!x"` condition passes when the fact is missing, so such rules are guarded by a fact the area always sets (a test caught `health_throttler` firing with no facts; it now uses `health.throttler_found`).
- Fix batch: actions run in a fixed order; the person's own proxy setting and the DNS flush run in the window's process, the rest through the elevated helper's `fix` job (`whynet.exe -helper <addr> <key> fix 120`): one JSON line of actions in, one outcome per line out; the helper validates every action against a fixed list (`health.Validate`: adapter by IPv4 index or name and never a VPN adapter, enum values, IP addresses, hosts lines matched exactly) and runs Windows tools without a shell. hosts lines are commented as `# [whynet <date>] <line>`, a copy goes next to the file (`hosts.whynet-<date>`), undo removes exactly that prefix. Undo actions are saved in `%LOCALAPPDATA%\whynet\health\undo.json`, newest batch first; "Вернуть как было" runs them and keeps what failed.

Verified on the author's PC (2026-11-17): `whynet-cli -health` and the built window (UI Automation + PrintWindow): areas tick off live, the findings screen as in the mockup. Found live: 31 stale hosts lines of 289 (17 addresses): checked by hand with curl/schannel — `kws1.web.telegram.org` at 149.154.167.220 gets `SEC_E_WRONG_PRINCIPAL` (DNS now gives 149.154.174.100) while `web.telegram.org` there works and is kept; Akamai/anticheatexpert addresses time out, 47.246.2.226 refuses, gryphline answers no TLS. MTU 1400 found after switching to `NlMtu`. Fix and undo in the window, the same day with the author's go (they clicked "Да" in UAC twice): "Починить всё найденное (1)" commented exactly the 31 lines (314 lines before and after), `hosts.whynet-2026-11-17` byte-identical to the original, DNS and ARP caches flushed, the re-check said "Починил 1 из 1" (pins 289 -> 258); "Вернуть как было" gave hosts back byte for byte (same SHA-256) and removed undo.json. Also live the same day: a real leftover proxy (`http://127.0.0.1:12334` with nothing listening, switched on for the test) found as `health_proxy_dead`, fixed without a UAC prompt ("Починил 1 из 1", ProxyEnable back to 0); then "Вернуть как было" with the author clicking "Нет" in UAC: the proxy part went through, the hosts part came back failed with "Права администратора не дали…", hosts stayed as the author had left it and undo.json kept only the hosts entry (the proxy was then switched back off by hand). Fixed after that: done lines in the past tense and with the values of the check the batch was planned on, the raw "declined" error hidden, "Вернул N из M" for a half-done undo (`TestHealthViewAfterBatches`). Not seen live yet: APIPA, a dead manual DNS, autotuning off, a throttler, Wi-Fi, Windows 10.
