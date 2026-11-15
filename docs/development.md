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
go run ./cmd/whynet-cli -json youtube.com    # every fact and all raw evidence
go run ./cmd/whynet-cli -save youtube.com    # also writes the AI report .md here
go run ./cmd/whynet-cli -save -no-hide ...   # report without masking
go run ./cmd/whynet-cli -no-browser ...      # skip the hidden Edge/Chrome step (faster)
wails dev                                    # app with live reload
wails build                                  # build/bin/whynet.exe
```

The window: `wails build` regenerates `frontend/wailsjs/` (bindings to `app.go`, committed) and embeds `frontend/dist`. Fonts come from the pinned `@fontsource/golos-text` and `@fontsource/jetbrains-mono` packages (OFL) and ship inside the exe, so the app needs no internet for them. npm 11 warns that esbuild's install script was not approved; the build works without it.

`wails` lives in `%USERPROFILE%\go\bin`; add it to PATH or call it by full path. When piping CLI output in Windows PowerShell 5.1, set `[Console]::OutputEncoding = [Text.Encoding]::UTF8` first, or Russian text arrives garbled (a real console window is fine).

## Conventions

- Code comments, commit messages and docs in English; UI strings in `locales/ru.yaml` only, never inline.
- Probes return facts; decisions go into `rules/diagnoses.yaml`.
- Every new diagnosis gets a rule, a text in `locales/ru.yaml` (`TestRulesAndTextsAgree` enforces both), a row in [diagnoses.md](diagnoses.md), a table case in `rules/rules_test.go` and, where possible, a lab case in `rules/lab_test.go` that produces the facts from a real local server.
- A new fact key goes into the facts table in [architecture.md](architecture.md).
- Work on a branch; `main` gets merged work. Push and releases only when the author asks.
