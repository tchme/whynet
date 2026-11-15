# Architecture

Planned layout for v1; update this file as the code lands.

```
whynet/
  main.go, app.go          Wails entry point; methods the UI calls, events for live step progress
  cmd/whynet-cli/          console front end: run checks, print facts (JSON) and the verdict
  internal/netx/           "network" interface (dial, resolve, DoH); swapped for a fake in tests
  internal/probe/          checks: local, dns, tcp, tls, http; each adds facts and a duration
  internal/diagnose/       rules engine: facts -> diagnosis (first matching rule wins)
  internal/report/         plain-text report for "Copy report"
  rules/diagnoses.yaml     ordered rules, embedded with go:embed
  rules/stubs.yaml         ISP stub page markers
  locales/ru.yaml          every user-facing string (UI and diagnoses)
  frontend/                HTML/CSS/JS (Wails vanilla template)
```

## Data flow

```
address -> probe.Run(ctx, netx) -> Facts (flat map + per-step timings)
        -> diagnose.Match(rules, Facts) -> Verdict{id, confidence, facts used}
        -> UI / CLI render texts from locales/ru.yaml by id
```

- Probes only gather facts; they never decide. All judgement lives in the rules file, so a new blocking pattern is usually a rules change, not a code change.
- The UI and the CLI are thin front ends over the same core.
- "Details" renders the facts grouped by step, so it needs no per-diagnosis code.
