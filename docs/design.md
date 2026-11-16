# whynet design

Status: approved 2026-11-15 for v1 (release v0.1.0). Later stages are a direction, not a commitment.

## Intent

A site doesn't open, and the person has no idea why. whynet answers that question. You type an address, it walks the path from your computer to the site, finds the step where the path breaks, and says in plain words what happened and what to do. The technical facts stay behind a "Details" (Подробнее) button for people who want them.

It must run on the person's own machine: blocking and breakage live in their network (their ISP, their router, their PC), so a website or a server-side bot would test from the wrong place.

The author wants a long-lived open-source project that many people use, built in their own area of interest (networking, VPN, server hardening).

## Goals (v1)

- Check one site and give one verdict: what broke, how sure we are, what to do.
- Cover the ~12 causes behind most real cases (see [diagnoses.md](diagnoses.md)).
- Plain Russian by default, full technical facts on demand.
- **The AI report is the core feature** (the author's top priority, 2026-11-15): one button saves everything whynet learned into one file that a person drops into ChatGPT/Claude and gets an analysis. Even if whynet never grows big, it must be a stable app that does this well. See "AI report" below.
- **Stability:** no check may crash the app or hang it. Every step has its own timeout and panic recovery; one failed step never stops the others; a report is always saved, partial if need be, with what could not be checked.
- One portable `.exe`, no install, no admin rights.

## AI report

The built-in rules cover common cases; the report covers the rest, so probes record **raw evidence** even when no rule needs it: exact error texts, DNS answers, certificate chain, redirect chain, response headers, every step's timing.

- **Format:** one Markdown file (`whynet-<site>-<date>.md`), the format language models read best. Top: an instruction to the AI, so the file can be dropped into a chat with no prompt. Then a summary with whynet's own verdict, the computer and network (OS, adapters, VPN found or not, proxy, DNS servers, ISP and country, clock), each step in plain words (what was checked, what came back), and last the raw data as JSON.
- **Language:** the UI language (Russian in v1).
- **Privacy:** by default the report hides the public IP (only ISP, AS number and country stay), computer name, Windows user name and MAC addresses. Before saving, the app shows what goes into the file; a "don't hide" checkbox keeps everything for advanced users. Masking runs over the whole finished text, so it also catches these values inside raw evidence.
- The CLI saves the same file (`--save`), so the report can be built and tested before the UI exists.

## Non-goals (v1)

- A helper server, slow-load analysis, live monitoring, per-app traffic (later stages below).
- Remote rule updates, auto-update, English UI, macOS/Linux builds, a code-signed exe.

## Anti-goals

These would make whynet a failure even if it worked:

- **Confident wrong answers.** When the evidence is ambiguous, the verdict says "probably" and why. A timeout looks the same whether the server is down or the address is blocked; v1 says so instead of guessing.
- **Jargon in the main verdict.** "DNS", "TLS", "SNI" belong under Details.
- **Sending the user's data anywhere** beyond what a check needs (DoH lookups of the checked domain). No telemetry. *(Author-of-the-design call.)*
- **Becoming a bypass tool.** whynet diagnoses and advises; it does not tunnel traffic. *(Author-of-the-design call; keeps the project simple and its GitHub repo low-risk.)*

## Approaches considered

| Approach | Verdict |
|---|---|
| Batch file (`nslookup` + `curl`) | Fine for a one-evening spike; Windows-only text console, hacky checks (no control SNI), not something to be proud of. |
| Website on a VPS | Rejected: checks would run from the VPS, which isn't blocked. Browser JS can't do raw DNS or TLS checks. |
| **Go + Wails desktop app** | **Chosen.** Go's net/tls stack allows exact checks (custom SNI, per-IP dials, error classes); Wails gives an HTML/CSS UI in a single small exe; the same core also builds a CLI and, later, other OSes. |
| Desktop app + VPS helper | Chosen as stage 2 on top of the above. |

## Decided

- Go + Wails v2, Windows first. UI in Russian; every user-facing string in `locales/ru.yaml` so English is a new file, not a rewrite.
- **Facts vs rules.** Probes produce a flat map of facts (`tls.status=reset`, `dns.match=differ`, …). A rules file (`rules/diagnoses.yaml`, embedded with `go:embed`) maps facts to a diagnosis: ordered rules, a list of conditions each, first match wins. No expression language.
- A CLI (`whynet-cli`) shares the core; it is the main tool for development and testing.
- Tests include a "lab": a fake network for timeouts, DNS spoofing and NXDOMAIN, plus real local servers that reset TCP, cut TLS after ClientHello, or serve an ISP-style stub page.
- **Window size (2026-11-16, the author's request):** it opens as just the address bar and one line of help, then grows to fit each result and "Details" (up to 90% of the screen, moving up when needed). "Details" starts with a table of the servers the page loads from: what each serves, ping (TCP connect time: ICMP is often filtered and the TCP delay is what a browser feels), speed, the browser's requests and failures, the result. Steps 6 and 7 are named for what they mean to a person: "Загрузка картинок, видео и скриптов", "Проверка серверов содержимого".
- **Window:** variant A of three mockups (chosen by the author 2026-11-15): input bar on top, steps on the left with a one-line note under a failed step, verdict card on the right (tone icon, confidence, title, explanation, "what to do"), then "Save AI report" (preview with a "don't hide" checkbox, native Save dialog), "Copy", "Details" (facts per step). Always dark, whatever the Windows theme (the author's choice, 2026-11-16: one look, no variants). The other variants were a path diagram (B) and a big-answer friendly screen (C).
- License MIT. Repository `whynet` on the author's GitHub; publishing only when the author says so.

## Content checks (decided 2026-11-15)

A site can open while its images, video or scripts don't. Two steps cover this, on Windows 10 and 11 alike:

- **Browser:** a hidden Edge (Chrome if Edge is missing; skipped with a note if neither) with a fresh throwaway profile loads the page over the DevTools protocol and records every request: errors per server, requests that never got an answer, speed of big files. It sees what scripts load (video servers), which HTML parsing can't. It uses the system proxy, like the person's browser.
- **Content servers:** servers named in the HTML, known servers of popular sites (`related` in markers.yaml, for content loaded by scripts) and the servers the browser failed on, each checked like the site itself plus one real file, watching for the "freezes after ~16 KB" pattern and for throttling.
- Ads and counters are reported but never make a site "broken": a built-in list of 76k ad and tracker domains from StevenBlack/hosts (MIT, `rules/ads.txt.gz`, rebuilt with `tools/adlist`), plus a short hand list in markers.yaml.
- **Visible window (decided 2026-11-16):** "Проверить в окне браузера" opens a plain Edge window with a throwaway profile; the person passes an anti-bot check, logs in or plays a video, then presses "Готово" or closes the window, and whynet checks everything the page loaded. Live counters show while it is open; it gives up after 10 minutes. It is not automated in any way the site can see: no debugging port (sites like ozon.ru turn away a browser with one), its own User-Agent; whynet reads Chromium's network log instead. No page timings or page text there, only the window title.
- **Slow pages:** the time until the page is parsed and shown (DOMContentLoaded) is measured; over 8 s, or never within the 20 s budget, gives `page_slow` — unless the site's own block page (anti-bot, "turn off VPN") already explains it.
- **Slow files (2026-11-16, the author's request: "which block, from which address, its ping, how to fix"):** the hidden browser keeps DevTools' timing of every request, split into phases (waiting in the browser, DNS, connect, TLS, the server's answer, download). Files the person sees missing (images, scripts, styles, fonts) that took 3 s or more, never answered or failed are listed by address (no query: signed links carry tokens) with the phase that took the time; HTTP/2 files queued behind a slow connect are blamed on that connect. Their servers are checked first in the content step, whose ping is now 5 tries: the median, and how many tries lost a packet (a try 800 ms slower than the best one: Windows resends a lost connect after 1 s). Behind a fake-ip router the router answers a connect itself in 0 ms, so the ping there is a TLS handshake. Verdicts by phase: lost packets (connect/TLS, or the server loses ping tries), a slow server (`wait`), or plain "late"; a lossy server alone gives `content_lossy` even when nothing came late this time. The visible window has no DevTools timings: only "until the answer" and the download.
- Browser runs never sign in or sync (Edge does both on its own otherwise, see bugs.md).

## Updates (2026-11-16, the author's request)

On start whynet asks GitHub Releases for the latest version. When it is newer, a banner offers "Да, обновить / Нет". "Yes" downloads `whynet.exe` from the release, checks its size and SHA-256 against GitHub's digest, renames the running exe aside (Windows allows renaming a running exe, not overwriting it), puts the new one in its place, starts it and quits; the next start removes the old file. On any failure the old exe stays and the release page opens for a manual download (e.g. an exe in Program Files can't be replaced without admin rights). The exe isn't code-signed, so the trust is the GitHub account and HTTPS.

## Which way to the site (2026-11-16)

For a site behind Cloudflare, whynet asks the site's own `/cdn-cgi/trace` where it sees the person and compares that country with the one an independent server sees. "The site sees you from Germany (Frankfurt node): you reach it through a VPN or proxy" — or "directly". Made for people with a VPN on the router, where it's otherwise unclear which sites go where. The address the site sees is masked in the report (it may be the person's own VPS).

## Checks (v1)

1. **Local:** route to the internet; system proxy (registry `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`); `hosts` entry for the domain; clock skew against the `Date` header of DoH responses (alarm above 5 minutes).
2. **DNS:** system resolver vs DoH over IP literals (1.1.1.1, 8.8.8.8, 9.9.9.9, in turn). Differing answers plus no valid certificate for the domain at the system-resolved IP means spoofing. No DoH reachable at all is a fact of its own and lowers confidence.
3. **TCP:** connect to 443 (and 80) on each IP; classify timeout / reset / refused (Windows errno 10060 / 10054 / 10061, `net.Error.Timeout()`).
4. **TLS:** handshake with the real SNI and a control handshake to the same IP with a neutral SNI. Real fails while control succeeds: blocking by name (DPI). Certificate errors are checked against clock skew and the issuer (antivirus interception).
5. **HTTP:** status, redirects, ISP stub pages (`rules/stubs.yaml`), 403/451 and geo-block hints from the site itself, 5xx, Cloudflare challenge.

Each step records its duration; v1 shows timings under Details, stage 2 builds slow-load analysis on them.

## Later stages

Order of priority (set 2026-11-16, the author's ideas first). The same rules hold for every stage: probes gather facts, rules decide, everything lands in the AI report, nothing may interrupt a VPN.

1. **App check** (the author's idea; was v4). The person picks a running program (a game, Discord, a launcher); whynet records where it connects for a while and checks each address: connect time, the name it belongs to (DNS cache, reverse DNS), its network owner (AS), signs of blocking (no answer, cut by name), whether zapret covers it (name and address lists, Game Filter ports and IPSet Filter), then advice and an AI report for that program.
   - Stage 1, no admin rights: TCP connections with their owning process (`GetExtendedTcpTable`), checked like the content servers of a site.
   - Stage 2: UDP, where most games live. Windows keeps no remote address for UDP sockets, so this needs a kernel network trace (ETW, admin rights, asked for explicitly). Stability over minutes: loss, jitter, reconnects. Full packet capture (Npcap) stays out of scope.
   - First rule, possible on facts whynet already has: Game Filter on with IPSet Filter `none` reaches no address (Flowseal's game sections use `--ipset=ipset-all.txt`).
   - The computer's own load next to the network in the stability check: CPU per second (no admin), later frame times (admin), so "the network was calm, the PC was not" is measured. Replaces judging lag by the person's "лагнуло" marks, which come too late to be evidence (see stability.md).
2. **Windows network health** (the author's idea), a separate button, not part of every check. Read-only first: adapters (errors, link speed, Wi-Fi signal, power saving), DNS servers (answer, speed), Winsock catalog (third-party layers), system and WinHTTP proxy (a stale one pointing nowhere), `hosts`, TCP global settings (autotuning, ECN, RSS), MTU per interface and the real path MTU, broken IPv6 (an address but no route), interface order (a VPN adapter taking the default route), the time service (stopped on the author's PC).
   Fixes come second, in tiers, each shown before it runs, with the old value saved and one-click undo: safe ones without admin (flush the DNS cache, turn off a dead system proxy); admin ones (autotuning back to normal, start time sync); risky ones that need a reboot or can break VPN clients (`netsh winsock reset`, `netsh int ip reset`, which also drops static addresses) only as advice with an explicit warning. VPN adapters and routes are never touched.
3. **"Compare without the bypass":** with admin rights, stop zapret/GoodbyeDPI for one check and compare, to tell "the bypass breaks this site" from "the bypass doesn't get through".
4. **VPS helper** ("down for everyone, or only for you?") on a separate VPS, not the author's VPN one. It returns only a verdict (no page content, so it is no open proxy), refuses private and reserved addresses after resolving (SSRF), allows ports 80/443 only, rate-limits per client, caches per domain for a minute. The app works without it; its address is configurable so anyone can run their own. Slow-load breakdown compared with the helper (the per-file breakdown on this PC is done, see "Slow files" above).
5. **Live mode:** ping router, first ISP hop and an internet target at once to tell Wi-Fi from ISP trouble; "check everything" over a list of popular sites.
6. Throughout: real-world runs on more ISPs (each one so far found wrong verdicts), signed remote updates of the rules file, community-contributed diagnoses via pull requests, English UI.

## Left to the builder

Exact fact keys and rule schema details, UI layout and styling, timeouts per step, the list of neutral SNI names and DoH endpoints, the ISP stub patterns (collected from real runs).
