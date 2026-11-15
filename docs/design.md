# whynet design

Status: approved 2026-11-15 for v1 (release v0.1.0). Later stages are a direction, not a commitment.

## Intent

A site doesn't open, and the person has no idea why. whynet answers that question. You type an address, it walks the path from your computer to the site, finds the step where the path breaks, and says in plain words what happened and what to do. The technical facts stay behind a "Details" (Подробнее) button for people who want them.

It must run on the person's own machine: blocking and breakage live in their network (their ISP, their router, their PC), so a website or a server-side bot would test from the wrong place.

The author wants a long-lived open-source project that many people use, built in their own area of interest (networking, VPN, server hardening).

## Goals (v1)

- Check one site and give one verdict: what broke, how sure we are, what to do.
- Cover the ~12 causes behind most real cases (see [diagnoses.md](diagnoses.md)).
- Plain Russian by default, full technical facts on demand, "Copy report" for sending to a friend or an issue.
- One portable `.exe`, no install, no admin rights.

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
- License MIT. Repository `whynet` on the author's GitHub; publishing only when the author says so.

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
4. Throughout: signed remote updates of the rules file, community-contributed diagnoses via pull requests, English UI.

## Left to the builder

Exact fact keys and rule schema details, UI layout and styling, timeouts per step, the list of neutral SNI names and DoH endpoints, the ISP stub patterns (collected from real runs).
