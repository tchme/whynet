# Stability check (design)

Status: approved by the author 2026-11-16. Stage 1 built 2026-11-16 (package `internal/live`, `app_live.go`, the window's "Стабильность" mode, `whynet-cli -live`); stage 2's program pick became the "Программа" mode (docs/appcheck.md), its CPU lane is built here. Stage 3 (frame times) designed and built 2026-11-17: CLI, report and window (mockup `docs/mockups/stability-frames.html`, approved).

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
- Measuring the game's own frame rate or CPU load in stage 1 (CPU came in stage 2, frame times are stage 3 below).

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

**Facts** (prefix `live.`; full table goes to `architecture.md` when built): per link `live.<link>` = stable, unstable, silent, plus `_median_ms`, `_p95_ms`, `_loss_pct`, `_spike_pct`; `live.router_kind` = wifi, cable; `live.router_periodic_s`; `live.wifi_signal`, `live.wifi_rssi`, `live.wifi_band`; `live.server_stand_in` (the hop used when the server ignores pings); `live.route_jump_hop` (where delay goes up and stays); `live.seconds`; `live.lag_marks` (count only: marks never decide a verdict).

**Verdicts** (first match wins, as today; texts later in `locales/ru.yaml`):

| id | When | Says |
|---|---|---|
| `live_too_short` | under 30 s | measure longer |
| `live_wifi` | router unstable by itself, wireless | the Wi-Fi link between this computer and the router; cable, closer, 5 GHz; the background scan when periodic |
| `live_lan` | router unstable by itself, cable | the cable, the port, the network card or the router itself; can't tell which from here |
| `live_fake_answers` | both controls answer in under 1 ms (faster than any real path) | a VPN client on this PC (its TUN adapter) answers the pings itself: turn the VPN or its TUN mode off for the recording; the router link is still measured (added 2026-11-17 after a friend's Throne recording said "stable") |
| `live_isp` | router stable, the first node beyond it unstable by itself | the ISP's access network: call them with the report; reboot the router / ONT first |
| `live_isp_network` | router and the first node stable, the controls unstable | further inside the ISP or its uplink |
| `live_route` | everything stable up to the controls, the server (or its stand-in) unstable | the route to this server or the server; the trace hop where it starts; a VPN may route around it (or make it worse) |
| `live_router_silent` | the router never answers | can't judge the local link; the rest as far as it goes |
| `live_stable` | nothing unstable | the network held during the check; run it while the jumps happen; frame drops and a busy PC also feel like lag |

**"Лагнуло" marks are bookmarks.** While recording, a global hotkey (Ctrl+Alt+L, read by polling the key state every 30 ms, so it works while a game has the focus; first built on `RegisterHotKey`, which a game reading raw input with `RIDEV_NOHOTKEYS` switches off, see bugs.md; the "Лагнуло" button for a taken combination stays, now unreachable) puts a mark on the chart. People press it a second or three after the lag (the author's point, 2026-11-16: in the moment nobody hits a key combination in time), so a mark looks back 10 s (and 1 s ahead) for the nearest moment the network jumped and names it ("за 2,5 с до отметки скакала сеть (Роутер · Wi-Fi)"), or says there was no jump in those 10 s. The 10 s before each mark are shaded on the chart, and the hover over them shows the mark's line. **No verdict is drawn from marks:** the first version's `live_not_network` ("the lag was not the network") was removed before anyone met it, because a late press on a session with one or two spikes (below the "unstable" bar) would have blamed the PC for a network spike. Telling "the PC lags" apart is left to measuring the PC (stage 2).

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
- **The computer as a lane** (the author's choice, 2026-11-16, instead of judging by "лагнуло" marks; built the same day): the whole computer's CPU load is read every tick (0.5 s) without admin (`GetSystemTimes`, busy share since the previous tick). It is drawn as one more lane "Компьютер · процессор" in the lanes view (bar height = load, orange from 90 %), shown in the hover and a Details card, and recorded in the AI report: a summary section (average, max, share of readings at 90 % or more), a "CPU max" column in the time table, and the CPU max of the 10 s each "лагнуло" mark looks back over. The verdict gets one extra line when the CPU hit 90 %. So "the network was calm while the CPU sat at 100 %" is measured, not guessed. No rule uses it yet: total CPU misses one maxed-out core of a game's main thread. Frame times (PresentMon-style ETW, admin rights) are the next step, with the same helper as UDP. Only then can a verdict like "the lag is the computer" come back.

## Stage 3: frame times (2026-11-17)

**Intent.** The stability check says where the *network* jumps. A game also "lags" when its own frames stall (a hitch, a freeze for 100-500 ms) while the network is calm, and people call both "лагнуло". Since the marks were demoted to bookmarks, whynet can't tell the two apart. Measuring the game's frames gives back the verdict "the network held, the game's frames stalled: this is the computer, not the internet", with numbers, the way the friend's Wi-Fi case got its numbers.

**Goals.**

- While recording, whynet sees every frame the game sends to the screen, without the person naming the game (the author's choice, variant A).
- A verdict for "network calm, frames stalled", and an extra line when both happened.
- A "лагнуло" mark names a frame stall in its 10 s look-back, as it names network jumps today.
- The frames lane, Details card and AI report section, like the CPU lane.
- Works on Windows 10 and 11, with any graphics API (DirectX 9-12, Vulkan, OpenGL).

**Non-goals.** Frames on the screen as displayed (tearing, VRR, "MsBetweenDisplayChange": needs PresentMon's whole state machine); GPU load; per-core CPU; input lag; an FPS overlay; frames in the "Программа" mode; choosing the game by hand.

**Anti-goals (failure even if it works).**

- Touching the game: no injection, no overlay, no hooks, nothing an anti-cheat would see. ETW only reads what Windows already emits system-wide.
- Costing the game frames: only the present events are enabled, never GPU scheduling or context switches.
- Blaming the computer for the network or the other way round: when the network is unstable its verdict stays first.
- Needing admin for the core answer: without consent the check runs as today (network + CPU).

**Approaches considered** (the author picked A on 2026-11-17):

| | How | Trade-off |
|---|---|---|
| **A (picked)** | Present events from ETW in the existing elevated helper, own syscalls, as for UDP | No new binary or dependency; the event layouts are ours to verify live |
| B | Ship Intel's PresentMon.exe and parse its CSV | Proven numbers; but a third-party exe in the app (size, antivirus flags), breaks the no-new-dependency rule, still needs admin |
| C | No admin: GPU load counters | Not frame times; can't see a stall |

**Source.** A real-time ETW session in the helper with the present providers only:

- `Microsoft-Windows-DXGI` (DirectX 10-12) and `Microsoft-Windows-D3D9`: `Present_Start` per call, with the process id in the event header.
- `Microsoft-Windows-DxgKrnl`, present keyword only: catches what the runtimes miss (Vulkan, OpenGL), the way PresentMon fills its "Other" runtime.
- Per process, a frame is one present call; a process with runtime events uses those only, so a DXGI frame is not counted twice through DxgKrnl. Frame time = the interval between consecutive presents of the same process (PresentMon's `MsBetweenPresents`): what a frame-time graph in CapFrameX or Afterburner shows.

**Found live (2026-11-17, a 30 s spike on the author's PC, Win11, elevated):**

- GUIDs (from `logman query providers`): DXGI `{CA11C036-0102-4A2D-A6AD-F03CFED5D3C9}`, D3D9 `{783ACA0A-790E-4D7F-8451-AA850511C6B9}`, DxgKrnl `{802EC45A-1E99-4B83-9920-87C98277BA9D}`; DxgKrnl's Present keyword is `0x8000000`.
- **DXGI event 42** (task 9, opcode 1: `Present_Start`, keyword `0x8000000000000002`) comes once per frame with the app's own PID in the header; 43 is its stop. **DxgKrnl event 184** (`Present_Info`) comes 1:1 with it from the same PID (Opera 5098 vs 5096, Edge 4678 vs 4678, VS Code 2253 vs 2252), so the dedup rule above holds: DXGI if the process has it, else DxgKrnl 184. Only the header (PID, time) is read; no payload.
- The stall page (a 200 ms busy wait every 5 s) gave exactly five gaps of 201-202 ms at 5 s steps, median 5.88 ms (170 Hz monitor). The frames came from Edge's GPU process while the foreground window belonged to its main process (different PIDs, same `msedge.exe`): grouping by exe name is needed.
- Background programs present at full rate (the author's Opera GX at ~170 fps on the other monitor, VS Code at 75 fps), so "most frames overall" would pick the wrong program; the foreground share decides.
- The Present keyword brings ~10 000 DxgKrnl events a second in all (DWM and the kernel included); the spike's own CPU time was 31 ms over 30 s. The helper sends only events 42 and 184 (~400 a second here).
- The D3D9 provider sent nothing (no D3D9 program was running); D3D9 programs present through the kernel, so DxgKrnl 184 should catch them: the D3D9 provider is left out until a D3D9 game shows otherwise. Not seen yet: a D3D12 or Vulkan game (flip-model presents may skip 184; then they come with DXGI 42 for D3D12, and Vulkan needs a live check).

**The helper.** One helper for both jobs: `whynet.exe -helper <addr> <key> <udp|frames|udp,frames> <max>`, one ETW session with the providers asked for. The JSON-lines stream carries two kinds of line (a datagram, a present: pid, time). Its own time limit comes from the window (3 min for the app check, 61 min for the stability check), capped at 61 min whatever it is told. The UAC prompt, the key, exit on close stay as they are. The pairing of the old flag `-udp-helper` is dropped (both sides are the same exe).

**Which process is the game** (variant A). The main window reads the foreground window's process every tick (`GetForegroundWindow` + `GetWindowThreadProcessId`, no admin). Processes are grouped by exe name, not PID: a browser's frames come from its GPU process while its window belongs to the main process. The game is the exe with the most frames presented while it was in the foreground; `dwm.exe` and whynet's own WebView2 never count. If no exe presented frames in the foreground for at least 30 s in all, there is no game: `live.frames = no_game`, no frame verdict, a line says so. The report names the exe and the share of the recording it was in front.

**Reading the frames** (start values, recorded as facts so they can be re-read after real runs):

- Per tick (0.5 s): frames, worst frame time.
- Over the session: median fps, average fps, 1 % low (fps from the 99th percentile frame time), worst frame, the time the game was in the background (not counted: a minimised game presents slowly or not at all).
- **A stall** is a frame time over max(3 × the median of the previous 2 s, 100 ms), counted only while the game was in front (first written as "median + 30 ms"; Frostpunk's 36-65 ms hitches at 169 fps then gave a false `live_pc_frames`, see below). Stalls closer than 1 s together are one stall (a hitch often spans a few slow frames).
- **A pause** is a gap over 2 s (a loading screen, a menu, alt-tab): listed with its time, never counted as a stall, so a level load doesn't read as "the computer lags".
- **Stalls during a network spike:** a stall in the same tick as confirmed network trouble is counted separately (`live.frames_stalls_net`): some games freeze on network waits.
- CPU at each stall comes from the CPU lane (busy ≥ 90 % in that tick or the one before).

**Facts** (prefix `live.`): `frames` = recorded, off, declined, failed, no_game; `frames_exe`, `frames_front_pct`, `frames_fps_median`, `frames_fps_low1`, `frames_worst_ms`, `frames_stalls`, `frames_stalls_per_min`, `frames_stalls_net`, `frames_stalls_cpu` (stalls at CPU ≥ 90 %).

**Verdicts** (`rules/live.yaml`, after `live_route`, before `live_stable`; texts in `locales/ru.yaml`):

| id | When | Says |
|---|---|---|
| `live_pc_cpu` | network stable; stalls ≥ 3; more than half of them at CPU ≥ 90 % | the game's frames stalled N times (up to X ms) while the network held; the processor was full at those moments: close background programs, check what loads it (antivirus scans, updates, the browser) |
| `live_pc_frames` | network stable; stalls ≥ 3 | the game's frames stalled N times while the network held: this is the computer (the video card, its driver, the disk, the game itself), not the internet; whynet can't tell which part |

"Network stable" is the rule order itself: these two sit after every network verdict, so they fire only when none did. When a network verdict wins and the game also stalled, an extra line under it gives the stall count. Below the bar (1-2 stalls), the numbers are in Details and the report, no verdict. `live_stable` adds "кадры игры тоже были ровными" when frames were recorded.

**Compare.** When both runs recorded frames, the before / after table gets a row "Замирания кадров".

**Calls made here (the author can overturn any):** pauses over 2 s are not stalls; the bar is 3 stalls whatever the length; "most at CPU ≥ 90 %" means more than half; the checkbox starts off and is remembered, like "И UDP"; the compare row.

**"Лагнуло" marks.** The 10 s look-back names the nearer of a network jump and a frame stall: "за 1,5 с до отметки игра замерла на 180 мс".

**Screen** (the mockup `docs/mockups/stability.html` gets these first, for the author's approval):

- Before recording: a checkbox "И кадры игры (нужны права администратора)" by the server field, remembered as "И UDP" is in the "Программа" mode.
- While recording: one more row "Игра · кадры" under the chain: the game's exe once found, fps now, stalls so far.
- Afterwards: a lane "Игра · кадры" in "Полосы" (fps as bar height, red at a stall, grey while the game was in the background); the hover shows fps and the worst frame of that second; a Details card (game, fps, 1 % low, worst frame, stalls, how many at CPU ≥ 90 %).
- Declined or failed: a line under the verdict, as `udp_note_*` does.

**AI report.** A "Кадры игры" section: exe, time in front, fps median / average / 1 % low, worst frame, stalls (time, length, CPU then, network trouble then) up to 200; per bucket in the time table: min fps, stalls. A 1-hour report grows by a few KB. The game's exe name is not masked (not personal); window titles are never read.

**CLI.** `whynet-cli -live -frames [-for 2m] [server]`.

**Constraints.** Windows 10 and 11; no new dependencies (`advapi32` ETW as in `internal/etw`, `user32` for the foreground window); every part under the house rules (facts → rules → locale, timeouts, panic recovery).

**Left to the builder.** The exact provider GUIDs, event ids and keywords (from the providers' manifests, confirmed live), the DxgKrnl dedup rule details, keeping frame intervals only for exes that were ever in the foreground, how the frames row sits in the live view.

**Verified live (2026-11-17, the author's PC, `whynet-cli -live -frames`):**

- The stall page in Edge: `msedge.exe` picked as the game by itself, 16 stalls of 201-202 ms in 80 s (one every 5 s, as built), `live_pc_frames`; the report's table has every stall with the CPU and the network at that moment.
- UAC declined (the author clicked "Нет"): `live.frames = declined`, the line under the verdict, the network check complete. First live run of a declined path in whynet.
- The app check's UDP through the reworked helper: Discord voice, 104.29.136.195:19300, `app_udp_ok`, the helper exited after the recording.
- A real game, Frostpunk (DirectX 11): `frostpunk.exe` picked by itself, ~169 fps; the first 25 s at ~17 fps were a loading screen (the recording started during it, the author says), the 65 ms hitch most likely its end; then 36 and 40 ms hitches in play. A loading screen at 17 fps cannot be told from a game running at 17 fps, so it stays in the numbers (it pulled the 1 % low to 16.9 fps); no verdict reads the 1 % low. With the first threshold (median + 30 ms) that made 3 stalls and a false `live_pc_frames`; the threshold is now 100 ms (a freeze the eye sees). The running estimate in the live row also kept showing 169 fps through the 17 fps stretch, because slow frames were left out of its average; it now averages every frame below a pause.
- Frostpunk again with the 100 ms threshold, recorded in play: `live_stable` and "кадры шли ровно", 170 fps, worst frame 32 ms, 0 stalls; the 50 s the author spent in another window were left out (in front 61.8 %).
- **Valorant with Riot Vanguard** (kernel anti-cheat, `vgk` running), the Range, 200 s: the game ran on, nothing complained; `valorant-win64-shipping.exe` picked by itself (in front 99.8 %), ~1030 fps, 198 440 frames streamed through the helper without trouble, worst frame 67 ms, 0 stalls, `live_stable`. The live row had counted 1 stall from the gap across a moment another window was in front; the running estimate now restarts when the window in front changes.
- Steam's own window (`steamwebhelper.exe`) presents at 170 fps; when it is in front the most, it is "the game". Correct by the rule; the report names it.
- Pitfall for testers: the UAC prompt covers the chat and gets "Да" by reflex; announce it and wait 10 s before triggering it.

**Not verified live:** a D3D12 or Vulkan game (presents may skip DxgKrnl 184; DXGI 42 should still come for D3D12), Windows 10. The window was checked in the running exe with Frostpunk (checkbox, UAC, the live row, the result row, the lane, the hover, the Details card); then with stalls made on cue (a throwaway tool suspended frostpunk.exe for 300 ms every 15 s): 12 freezes, 10 stalls counted and 2 left out while the author had another window in front on purpose (grey on the lane), `live_pc_frames`, the hover "Игра: замер кадр — 319 мс. Сеть в этот момент спокойна.", and after the hotkey fix 6 "лагнуло" marks pressed in the game, each naming its stall. The compare row was not seen live.

**Verification plan.** Live on the author's PC: a browser page whose animation busy-waits 200 ms every few seconds (stalls on cue, the browser's GPU process named through its exe); then a real game if the author has one at hand; the declined UAC path; Windows 10 on a friend's PC when one is available. Not reproducible here: an anti-cheat game (expected fine: nothing touches the game).

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
