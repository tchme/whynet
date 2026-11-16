# Stability check (design)

Status: approved by the author 2026-11-16. Stage 1 built 2026-11-16 (package `internal/live`, `app_live.go`, the window's "Стабильность" mode, `whynet-cli -live`); stage 2 is designed here and not built yet.

## Intent

"My ping in the game jumps, for days now." The author solved a friend's case by hand: while the friend played, two consoles pinged the router and the game server at once; the router went from 1–3 ms to 250–500 ms, so the problem was the Wi-Fi link, and a cable fixed it. The stability check does that for anyone: while the person plays (or uses any program), whynet watches every link of the chain at once and says **where** the instability lives: this device and its Wi-Fi, the ISP, the route to the server, or the server. A trace to the server shows where along the route delay and loss begin. People who already know a server's address can give it directly, as an IP or a name, the way they type a site today.

## Goals

- The friend's case gets the right verdict ("the Wi-Fi link to the router is unstable, use a cable") after a few minutes of play, with the numbers behind it.
- Tell apart: local link (Wi-Fi or cable, device), first link beyond the router (usually the ISP's access network), the ISP's network further on, the route or the server itself.
- A trace that doesn't invent problems: a hop that ignores pings is not a lossy hop (see Rules of reading).
- Everything in the AI report, like the site check.
- Stage 2: pick a running program instead of typing an address; its servers are found by themselves, UDP included.

## Non-goals

- Fixing anything. This check only reads; fixes belong to the planned "Windows network health" feature.
- Packet capture or payloads: addresses, ports, sizes and timings only.
- Background monitoring: it runs only while its screen says it is recording.
- Measuring the game's own frame rate or CPU load (named in advice as a possible cause, not measured).

## Anti-goals (failure even if it works)

- The check itself causes lag: total probe traffic stays tiny (see Measurement).
- It interrupts or reroutes a VPN, or changes any setting.
- It blames the ISP for what is the Wi-Fi, or the route for ICMP rate limiting.
- It needs admin rights for the core answer (stage 1 needs none).

## Constraints

- Windows 10 and 11; Go + Wails as today; no new dependencies: ICMP (`iphlpapi`: `IcmpCreateFile`, `IcmpSendEcho2`), Wi-Fi (`wlanapi`: `WlanOpenHandle`, `WlanQueryInterface`), TCP table (`GetExtendedTcpTable`) and ETW (`advapi32`) through `windows.NewLazySystemDLL`, as `dpi_windows.go` already does.
- The house rules: probes gather facts, `rules/diagnoses.yaml` decides, texts in `locales/ru.yaml`, every step with its own timeout and panic recovery, private values masked in the report by default.

## Approaches considered

How stage 2 gets the rights to see UDP (Windows keeps no remote address for UDP sockets; a kernel network trace, ETW, has it and needs admin):

| | How | Trade-off |
|---|---|---|
| A | Restart all of whynet as admin, resume the same check | Simple messaging; but the whole app, the hidden Edge included, then runs elevated, and the state must survive the restart |
| **B (picked)** | Ask "Нужны права администратора — да / нет"; on yes, start a second copy of whynet as a small elevated helper that only records the chosen program's UDP traffic; the window stays as it is | Least privilege, nothing to resume (the window never left); needs a pipe between the two copies |
| C | No admin: TCP connections only, plus typing the address | Always the fallback for "no" |

Order (picked): stage 1 first, the chain without picking a program, because the main value (where it jumps) doesn't need the server: the friend's case was decided by the router line alone.

## Stage 1: the chain

**Input.** Optional: a server as an IP or a name (a name is resolved like a site; an IP is used as is). Empty: the chain without a server.

**Links,** nearest first:

1. **Router:** the default gateway of the adapter that carries the default route (`adapters()` already reads gateways). Its adapter's type tells Wi-Fi (`IF_TYPE_IEEE80211`) from cable.
2. **Beyond the router:** the first hop after the gateway on a trace to a control address; named "the first node beyond your router, usually the ISP". With a second router at home (double NAT) that hop is the second router; the report shows the hop's address and owner so it can be read either way.
3. **Internet controls:** two steady anycast addresses, one in Russia (77.88.8.8, Yandex DNS) and one global (1.1.1.1), so a jumpy server can be compared against a calm internet.
4. **Server** (if given). Many game servers ignore pings: then the last hop of its trace that answers stands in for it, and the verdict says so.

**Measurement.**

- Every link pinged every 500 ms, all in the same tick, ICMP echo with a 32-byte payload, 1 s timeout (no answer within 1 s counts as lost). About 10 small packets a second in all: nothing a game would feel.
- Trace rounds to the server (or to the control, without one): every 5 s, TTL 1–30 probed in parallel, 1 s per hop, stop at the destination. Per hop: address, owner network (AS, through the Team Cymru DoH lookup whynet already uses), loss, median and worst delay over the session.
- Wi-Fi, if the link is wireless, read every 5 s: signal quality (%), RSSI (dBm), band and channel, PHY type, link rate. The network name (SSID) is masked in the report by default (it can name a person); the access point's MAC is not collected.
- Duration: runs until the person presses "Готово"; a verdict needs at least 30 s (60 samples a link), the screen suggests 2–3 minutes in the game, longer for rare spikes (a whole match); it stops by itself after 1 hour (the author's limit, to keep the report small).

**Reading the numbers** (probe-side classification, as `res.slow` is today):

- Per link: median, 95th percentile, worst, jitter (mean change between consecutive answers), loss %, spikes (an answer over max(3 × median, median + 50 ms)).
- A link is **unstable** when loss ≥ 2 %, or spikes ≥ 2 % of samples, or p95 − median ≥ 50 ms (router: ≥ 20 ms, since a healthy local link answers in 1–3 ms). **Silent** when it never answers (some routers ignore pings): its place in the chain is then unknown, never "fine".
- **Blame goes to the nearest link that adds it:** a spike or loss at link N in the same tick as one at link N−1 is inherited, not N's own. A link is unstable "by itself" only on what is left after that.
- **Periodic spikes:** on a wireless router link, spikes that repeat every ~60 s (±5 s, at least 3 times) are the Windows background Wi-Fi scan; a separate fact, so advice can name it.

**Rules of reading a trace:** loss at a hop counts only if every later hop loses at least as much (routers rate-limit answers to pings meant for them, while still forwarding traffic); delay that jumps at a hop and stays up on every later hop is where the route gets long (another country, a detour); delay at one hop only is that router answering slowly, not a problem.

**Facts** (prefix `live.`; full table goes to `architecture.md` when built): per link `live.<link>` = stable, unstable, silent, plus `_median_ms`, `_p95_ms`, `_loss_pct`, `_spike_pct`; `live.router_kind` = wifi, cable; `live.router_periodic_s`; `live.wifi_signal`, `live.wifi_rssi`, `live.wifi_band`; `live.server_stand_in` (the hop used when the server ignores pings); `live.route_jump_hop` (where delay goes up and stays); `live.seconds`; `live.lag_marks`, `live.lag_marks_calm` (marks with a calm network).

**Verdicts** (first match wins, as today; texts later in `locales/ru.yaml`):

| id | When | Says |
|---|---|---|
| `live_too_short` | under 30 s | measure longer |
| `live_wifi` | router unstable by itself, wireless | the Wi-Fi link between this computer and the router; cable, closer, 5 GHz; the background scan when periodic |
| `live_lan` | router unstable by itself, cable | the cable, the port, the network card or the router itself; can't tell which from here |
| `live_isp` | router stable, the first node beyond it unstable by itself | the ISP's access network: call them with the report; reboot the router / ONT first |
| `live_isp_network` | router and the first node stable, the controls unstable | further inside the ISP or its uplink |
| `live_route` | everything stable up to the controls, the server (or its stand-in) unstable | the route to this server or the server; the trace hop where it starts; a VPN may route around it (or make it worse) |
| `live_router_silent` | the router never answers | can't judge the local link; the rest as far as it goes |
| `live_not_network` | nothing unstable, and lag marks fell on a calm network | the lag the person felt was not the network: frame rate, a busy PC, the game's own server load |
| `live_stable` | nothing unstable | the network held during the check; run it while the jumps happen; frame drops and a busy PC also feel like lag |

**"Лагнуло" marks.** While recording, a global hotkey (Ctrl+Alt+L, `RegisterHotKey`, works while a game has the focus; if another program holds it, the screen says so) marks the moment the person felt lag. Each mark is read against the samples around it (±2 s): the network jumped (and which link was to blame), or the network was calm, which says the lag was not the network (frame drops, a busy PC). Nothing else can tell those apart, so the verdict card names it.

**Compare.** "Проверить ещё раз и сравнить" after a verdict starts the same check again; the result shows before / after (usual and worst ping to the server, spikes, loss) with a verdict of its own ("стало стабильно" or what is still wrong). Kept in memory for this session only; both runs go into the AI report.

**Screen** (mockup approved by the author, 2026-11-16: [mockups/stability.html](mockups/stability.html)): a switch "Сайт / Стабильность · для игр" left of the address field; the server field (optional). During the run: the chain on the left (now, and "по его вине" — spikes a link caused itself, never the word "own spikes"), on the right the time, the count of lag marks, a progress bar of the recording (a tick at 0:30 "вердикт", full at 3:00 "достаточно", after which the bar stays full with a tick and the timer keeps going, so 3:00 never reads as the end; recording goes on until "Готово" or 1 hour), a live line chart, "Готово" (disabled for the first 30 s, saying how many seconds are left). The verdict appears at once on "Готово": everything is gathered while recording, so there is no waiting after it. Afterwards: the chain with each link's result, a verdict card (lag marks explained, the advice, "Сохранить отчёт для ИИ", "Проверить ещё раз и сравнить", "Скопировать", details); details: the whole-session chart with two views, "Полосы" (default: one lane per link, red where the link caused a spike, pale red where it only repeated a nearer link's, lost packets and lag marks shown) and "Линии" (ms per link), both with a hover showing the time to the second, every link's ping and who was to blame; then the links table, the trace table, Wi-Fi and lag marks.

**AI report:** the same file shape as a site check (instruction for the AI, summary, steps, raw JSON): links table, trace table, Wi-Fi, and the session in buckets (per link: min, median, worst, lost) plus the time of every spike, so an AI can see when things happened; not every raw sample. Buckets grow with the length so a 1-hour run stays small: 5 s up to 5 minutes, then sized to keep about 60 rows (1 minute for an hour); spikes listed one by one up to 200, then counted per bucket. A 1-hour report stays around 100 KB.

**CLI:** `whynet-cli -live [-for 2m] [server]`.

## Stage 2: pick a program

- A list of running programs that have a window (name, icon), the game among them.
- TCP connections of that program from `GetExtendedTcpTable` (owner PID), no admin.
- UDP: the question "Нужны права администратора, чтобы увидеть UDP — да / нет". On yes: the main window creates a named pipe readable by the current user only and starts `whynet.exe --udp-helper` elevated (`ShellExecute` with `runas`), passing the program's PID and the pipe's name. The helper runs an ETW real-time session on the Microsoft-Windows-Kernel-Network provider, keeps only that PID's UDP send/receive events, and writes per-second totals per remote address (address, port, packets, bytes, never payloads) to the pipe. It stops its trace session and exits when the pipe closes, when recording ends, or after 1 hour. On no: TCP only, plus the address field.
- The program's main server is the remote UDP address with the most packets (DNS, multicast and LAN addresses excluded); it becomes the chain's server link and the trace target. Every other address it used is listed with the per-address checks a site's content servers get today.
- First rule on facts whynet already records: Game Filter on with IPSet Filter `none` reaches no address (Flowseal's game sections use `--ipset=ipset-all.txt`).

## Calls made in writing this (the author can overturn any)

- **A VPN on this PC** (WARP, AmneziaWG): the router link is always the gateway of the physical adapter (VPN adapters skipped by the existing `vpn_adapters` markers), which stays reachable on the LAN; when the default route goes through a VPN adapter, link 2 is named "the VPN server" instead of the ISP, and the verdicts say the path goes through the VPN. Nothing is switched off to measure.
- **A fake-ip router** (the author's): a server given by name that resolves to 198.18.0.0/15 is resolved again through DoH to its real address, and the report says the program's own traffic may go through the router's VPN while whynet's pings may not.
- **Two controls:** they count as unstable only when both are; one jumpy control alone is that control's own path.
- **IPv4 only** in stage 1 (as the site check's DNS today); a server given as IPv6 is refused with a plain message. Shortcut, to lift when a real game server needs it.
- **One check at a time:** the site check and the stability check don't run together.

## Decided while building stage 1

- **Trouble is confirmed by the links behind.** A spike or loss on a link counts only when some farther link shows trouble at the same tick; otherwise it is the node answering pings to itself slowly (routers deprioritise that) while forwarding traffic fine. Live on the author's line the ISP's edge (100.64.49.23) answered its own pings at 2 ms usually and up to 22 ms, with nothing behind it moving: "stable", as it should be. The wandering-p95 test is not applied to that intermediate node at all.
- **The node beyond the router** is the first trace hop in TTL 1-3 that is neither the router nor the target (on the author's line hop 2 never answers; hop 3 is the ISP's CGNAT edge).
- **Controls by name:** "Интернет · Россия" (77.88.8.8) and "Интернет · мир" (1.1.1.1), two separate links on the screen and in the report.
- **The live view's "по его вине"** is a running estimate (a two-minute median per link, no confirmation); the verdict's numbers come from the full analysis at "Готово".
- **"Лагнуло" button** appears in the window only when Ctrl+Alt+L is held by another program.
- **The window's Wi-Fi card** never shows the network name; the report keeps it masked unless "don't hide" is ticked (names under 3 characters are left alone, so masking cannot eat unrelated text).
- **Not verified live yet:** Wi-Fi reading (the author's PC is on cable), the hotkey pressed in a game, a real Wi-Fi or ISP problem; the window was checked by screenshots of the built frontend with the Go side stubbed, not in the running app.

## Left to the builder

Threshold tuning after real runs (the numbers above are a start, recorded as facts so they can be re-read), the chart drawn on a plain canvas (no chart library), the exact ETW event IDs and field layout, the pipe protocol, how the second mode sits in the window (after the mockup).
