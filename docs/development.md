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
go test ./...                         # unit tests and the failure lab
go vet ./...
go run ./cmd/whynet-cli check <site>  # facts + verdict in the console
wails dev                             # app with live reload
wails build                           # build/bin/whynet.exe
```

## Conventions

- Code comments, commit messages and docs in English; UI strings in `locales/ru.yaml` only, never inline.
- Probes return facts; decisions go into `rules/diagnoses.yaml`.
- Every new diagnosis gets a row in [diagnoses.md](diagnoses.md), a table test (facts -> diagnosis) and, where possible, a lab test that produces those facts from a real local server.
- Work on a branch; `main` gets merged work. Push and releases only when the author asks.
