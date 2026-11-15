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
- **Window:** variant A of three mockups (chosen by the author 2026-11-15): input bar on top, steps on the left with a one-line note under a failed step, verdict card on the right (tone icon, confidence, title, explanation, "what to do"), then "Save AI report" (preview with a "don't hide" checkbox, native Save dialog), "Copy", "Details" (facts per step). Light/dark follow Windows. The other variants were a path diagram (B) and a big-answer friendly screen (C).
- License MIT. Repository `whynet` on the author's GitHub; publishing only when the author says so.

## Content checks (decided 2026-11-15)

A site can open while its images, video or scripts don't. Two steps cover this, on Windows 10 and 11 alike:

- **Browser:** a hidden Edge (Chrome if Edge is missing; skipped with a note if neither) with a fresh throwaway profile loads the page over the DevTools protocol and records every request: errors per server, requests that never got an answer, speed of big files. It sees what scripts load (video servers), which HTML parsing can't. It uses the system proxy, like the person's browser.
- **Content servers:** servers named in the HTML, known servers of popular sites (`related` in markers.yaml, for content loaded by scripts) and the servers the browser failed on, each checked like the site itself plus one real file, watching for the "freezes after ~16 KB" pattern and for throttling.
- Ads and counters (`ads` in markers.yaml) are reported but never make a site "broken".

## Checks (v1)

1. **Local:** route to the internet; system proxy (registry `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`); `hosts` entry for the domain; clock skew against the `Date` header of DoH responses (alarm above 5 minutes).
2. **DNS:** system resolver vs DoH over IP literals (1.1.1.1, 8.8.8.8, 9.9.9.9, in turn). Differing answers plus no valid certificate for the domain at the system-resolved IP means spoofing. No DoH reachable at all is a fact of its own and lowers confidence.
3. **TCP:** connect to 443 (and 80) on each IP; classify timeout / reset / refused (Windows errno 10060 / 10054 / 10061, `net.Error.Timeout()`).
4. **TLS:** handshake with the real SNI and a control handshake to the same IP with a neutral SNI. Real fails while control succeeds: blocking by name (DPI). Certificate errors are checked against clock skew and the issuer (antivirus interception).
5. **HTTP:** status, redirects, ISP stub pages (`rules/stubs.yaml`), 403/451 and geo-block hints from the site itself, 5xx, Cloudflare challenge.

Each step records its duration; v1 shows timings under Details, stage 2 builds slow-load analysis on them.

## Later stages

1. **v2:** VPS helper ("down for everyone, or only for you?") on a separate VPS, not the author's VPN one. It returns only a verdict (no page content, so it is no open proxy), refuses private and reserved addresses after resolving (SSRF), allows ports 80/443 only, rate-limits per client, caches per domain for a minute. The app works without it; its address is configurable so anyone can run their own. Slow-load breakdown (DNS / connect / TLS / server think time / transfer speed, compared with the helper).
2. **v3:** live mode: ping router, first ISP hop and an internet target at once to tell Wi-Fi from ISP trouble; "check everything" over a list of popular sites.
3. **v4:** per-application connections (Windows connection tables and TCP stats; admin rights for the stats). Full packet capture (Npcap) is out of scope.
4. "Compare without the bypass": with admin rights, stop zapret/GoodbyeDPI for one check and compare, to tell "the bypass breaks this site" from "the bypass doesn't get through". v1 only detects the tools (process, service, WinDivert driver, whether the site is in zapret's lists) and reports them.
5. Throughout: signed remote updates of the rules file, community-contributed diagnoses via pull requests, English UI.

## Left to the builder

Exact fact keys and rule schema details, UI layout and styling, timeouts per step, the list of neutral SNI names and DoH endpoints, the ISP stub patterns (collected from real runs).
