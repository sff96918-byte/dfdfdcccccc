# ChitChat Bot — EVAChat Flow source

Clean source-only project for the Windows Wails desktop app and CLI worker.

## Included

- Go module source (`app.go`, `main.go`, `cmd/`, `internal/`)
- Wails dashboard source/assets
- EVAChat Flow engine and embedded editable starter TXT pack
- Build script, test report, database/editing guide, and security `.gitignore`

## Not included

No runtime account data is included: no `data/` folder, cookies, browser profiles,
login JSON, session storage, tokens, Badger DB, logs, cache, binaries, Chromium,
or WebView2 runtime.

## Build on Windows (one click)

Double-click **`install.bat`** in the project root. It installs what is missing
(Go 1.25+, Wails CLI v2.12.0, WebView2 runtime), builds the dashboard the same
way it was originally built (`wails build -clean -webview2 error`), builds the
CLI worker, and offers to start `build\bin\chitchatui.exe`. Afterwards use
**`run-dashboard.bat`**. Log: `build\install.log`.

```bat
install.bat          :: install + build + run
install.bat build    :: only rebuild
install.bat run      :: only start the dashboard
install.bat bundle   :: portable bundle with WebView2 + Chromium (scripts\bundle-windows.ps1)
```

Manual equivalent:

```powershell
wails build -clean -webview2 error
# or build the complete bundle
.\scripts\bundle-windows.ps1 -Clean
```

See [`EVACHAT_FLOW_GUIDE.md`](EVACHAT_FLOW_GUIDE.md) and
[`TEST_REPORT.md`](TEST_REPORT.md) before building or publishing a release.
