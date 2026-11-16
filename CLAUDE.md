# whynet

Windows 10/11 desktop app (Go + Wails v2) that tells a non-technical person why a site doesn't open or loads broken: it walks local -> DNS -> TCP -> TLS -> HTTP, then loads the page in a hidden Edge and checks every server its content comes from; finds the broken step and explains it in plain Russian, with technical facts under "Подробнее". A second mode, the stability check, pings the router, the ISP, the internet and a game server at once while the person plays and says which link makes the ping jump. A third, the app check, records which servers a running program (Discord, a launcher) connects to over TCP and checks each of them. Open source, MIT.

## Commands

- `go test ./...` — tests (run before every commit)
- `go run ./cmd/whynet-cli [-json] [-save] [-no-hide] <site>` — console run; `-save` writes the AI report
- `go run ./cmd/whynet-cli -live [-for 2m] [server]` — the stability check from the console
- `go run ./cmd/whynet-cli -app [-for 30s] [program or PID]` — the app check from the console (no name: lists programs)
- `wails dev` / `wails build` — app / exe (`wails` is in `%USERPROFILE%\go\bin`)

## Key rules

- Probes gather facts, rules decide (`rules/diagnoses.yaml`); never put a diagnosis in probe code.
- All user-facing text in `locales/ru.yaml`.
- Uncertain evidence gives an honest "probably", never a confident guess.
- The AI report (`internal/report`) is the core feature: probes record raw evidence even when no rule uses it; private values stay masked by default.
- No step may crash or hang the app: own timeout, panic recovery, later steps still run.

## Docs

- [docs/design.md](docs/design.md) — intent, goals, anti-goals, approaches, later stages
- [docs/architecture.md](docs/architecture.md) — code map and data flow
- [docs/diagnoses.md](docs/diagnoses.md) — every diagnosis: detection and how to reproduce
- [docs/stability.md](docs/stability.md) — the stability check for games (design): ping chain, trace, Wi-Fi, picking a program
- [docs/appcheck.md](docs/appcheck.md) — the app check (design): pick a program, record its TCP connections, check each server
- [docs/development.md](docs/development.md) — setup, commands, conventions
- [docs/bugs.md](docs/bugs.md) — verified pitfalls and fixed bugs
