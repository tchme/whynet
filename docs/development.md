# Development

## Setup (Windows)

```powershell
winget install --id GoLang.Go -e
winget install --id OpenJS.NodeJS.LTS -e
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails doctor          # must report no missing dependencies
```

WebView2 ships with Windows 11.

## Commands

```powershell
go test ./...                                # unit tests and the failure lab (~5 s)
go vet ./...
go run ./cmd/whynet-cli youtube.com          # progress + verdict
go run ./cmd/whynet-cli -json youtube.com    # raw facts and evidence, with no report masking
go run ./cmd/whynet-cli -save youtube.com    # also writes the AI report .md here
go run ./cmd/whynet-cli -save -no-hide ...   # report without masking
go run ./cmd/whynet-cli -no-browser ...      # skip the hidden Edge/Chrome step (faster)
go run ./cmd/whynet-cli -live -for 1m [server]  # stability check: ping the chain for 1 minute (Ctrl+C ends early)
go run ./cmd/whynet-cli -app                    # app check: list running programs
go run ./cmd/whynet-cli -app -for 20s discord   # record Discord's TCP connections 20 s, check its servers
go run ./cmd/whynet-cli -health                 # Windows network health: every finding (checks only)
wails dev                                    # app with live reload
wails build                                  # build/bin/whynet.exe
```

The window: `wails build` regenerates `frontend/wailsjs/` (bindings to `app.go`, committed) and embeds `frontend/dist`. Fonts come from the pinned `@fontsource/golos-text` and `@fontsource/jetbrains-mono` packages (OFL) and ship inside the exe, so the app needs no internet for them. npm 11 warns that esbuild's install script was not approved; the build works without it.

Markdown reports are masked by default. `-no-hide` controls the Markdown written by `-save`. CLI `-json` always prints the collected result, including the original site input and HTTP redirect URLs, even when `-save` also writes a masked Markdown report.

## Releasing

1. Bump the version in `wails.json` (`info.productVersion`) and `internal/report/report.go` (`Version`).
2. Run Go tests and vet, check the frontend, then commit and build `whynet.exe` from that revision in a clean directory. Build `cmd/whynet-cli` for smoke tests. Keep the build SHA-256 and the results for the exact Windows version, network and VPN profiles in a separate record. Recheck applicable scenarios if the executable's bytes change.
3. Tag `vX.Y.Z`, push and publish on the owner's request. Attach the tested executable under the exact name **`whynet.exe`**. Confirm that its published SHA-256 matches the tested file. The in-app updater requires a valid GitHub digest. Release notes and the README use Russian first and English below the divider.

To test an upgrade from 0.7.0, build `v0.7.0` from a scratch copy and point its updater at a local test release with `-ldflags "-X whynet/internal/update.LatestURL=http://127.0.0.1:8765/latest"`. Serve a `latest` JSON (`tag_name`, `assets[{name, size, digest, browser_download_url}]`) and the exact candidate executable with `python -m http.server 8765`. Run the old executable from a temporary folder and press "Да, обновить". Use `go test ./internal/update` for invalid digests, launch failure and interrupted replacement. The old executable is kept as `whynet.exe.old` after a successful replacement.

Ad list: `go run ./tools/adlist` downloads the current StevenBlack/hosts list and rewrites `rules/ads.txt.gz`; update the date in `rules/ads-LICENSE.txt` and commit both.

`wails` lives in `%USERPROFILE%\go\bin`; add it to PATH or call it by full path. When piping CLI output in Windows PowerShell 5.1, set `[Console]::OutputEncoding = [Text.Encoding]::UTF8` first, or Russian text arrives garbled (a real console window is fine).

## Conventions

- Code comments, commit messages and docs in English; UI strings in `locales/ru.yaml` only, never inline.
- Probes return facts; decisions go into `rules/diagnoses.yaml`.
- Every new diagnosis gets a rule, a text in `locales/ru.yaml` (`TestRulesAndTextsAgree` enforces both), a row in [diagnoses.md](diagnoses.md), a table case in `rules/rules_test.go` and, where possible, a lab case in `rules/lab_test.go` that produces the facts from a real local server.
- A new fact key goes into the facts table in [architecture.md](architecture.md).
- Work on a branch; `main` gets merged work. Push and releases only when the author asks.

Driving the built window from a script: find buttons by their accessible names through UI Automation (`System.Windows.Automation`: Invoke, SelectionItem for the mode tabs, ExpandCollapse for the Details bar) instead of mouse clicks; screenshots with `PrintWindow(hwnd, hdc, 2)` (WebView2 needs PW_RENDERFULLCONTENT; screen copies catch whatever window is on top). Mouse coordinates broke across two monitors with different scaling.
