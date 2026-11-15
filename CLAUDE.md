# whynet

Windows desktop app (Go + Wails v2) that tells a non-technical person why a site doesn't open: it walks local -> DNS -> TCP -> TLS -> HTTP, finds the broken step and explains it in plain Russian, with technical facts under "Подробнее". Open source, MIT.

## Commands

- `go test ./...` — tests (run before every commit)
- `go run ./cmd/whynet-cli check <site>` — console run
- `wails dev` / `wails build` — app / exe

## Key rules

- Probes gather facts, rules decide (`rules/diagnoses.yaml`); never put a diagnosis in probe code.
- All user-facing text in `locales/ru.yaml`.
- Uncertain evidence gives an honest "probably", never a confident guess.
- v1 plan (Russian, the author's working document): `C:\Users\Dimasta\.claude\plans\concurrent-whistling-heron.md`.

## Docs

- [docs/design.md](docs/design.md) — intent, goals, anti-goals, approaches, later stages
- [docs/architecture.md](docs/architecture.md) — code map and data flow
- [docs/diagnoses.md](docs/diagnoses.md) — every diagnosis: detection and how to reproduce
- [docs/development.md](docs/development.md) — setup, commands, conventions
- [docs/bugs.md](docs/bugs.md) — verified pitfalls and fixed bugs
