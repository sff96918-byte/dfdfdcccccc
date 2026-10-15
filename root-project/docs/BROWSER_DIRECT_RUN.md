> Update: advanced verification is now included. The implementation history
> below describes the first direct-run patch; use `VERIFICATION.md` and the
> delivered `VERIFICATION-REPORT.json` for current verification status.

# Browser direct-run change

## Scope and layout

This user-authorized Browser-only change supersedes the older EVA-only scope
for these files. No chat, AI, license, proxy-driver or browser-driver logic changed.
The source was extracted from `root project.zip` into `root-project/`; the
original archive and reference archive remain unchanged.

Changed existing files:
- `app.go`
- `cmd/chitchatui/frontend/dist/index.html`
- `cmd/chitchatui/frontend/dist/app.js`
- `cmd/chitchatui/frontend/dist/styles.css`
- `cmd/chitchatui/frontend/wailsjs/go/main/App.js`
- `cmd/chitchatui/frontend/wailsjs/go/main/App.d.ts`

Added tests: `browser_run_test.go`, `tests/browser-ui.test.cjs`.
Generated wrapper signatures were synchronized for `RunBrowser`; a normal
Wails build can regenerate them. No model/binding type changes are needed.

## Behavior

Browser shows the available backend count, shared Profile/Count/Headless
controls, numbered Rod/ChromeDP rows with individual Run buttons, and status.
No Save Browser, backend radio, or separate Run Fresh Browsers button remains.

Each Run sends that row's backend and the current form values in ONE request.
The server validates backend and count, normalizes profile using existing
safeProfileName, validates saved proxy settings, creates the destination, and
persists accepted settings before dispatching browsers. Dispatch uses the
request's value copy, never a second read of the shared defaults.

Count remains 1–5 PER REQUEST, not a global cap. Buttons are disabled only while
the launch request is pending; subsequent intentional requests are allowed.
Initial saved Profile/Headless values load once; later snapshots do not erase
edits. Run does not trigger an unrelated full-form refresh.

The launch method is asynchronous. "Request accepted" is not proof that a
browser opened or login succeeded. Browser status displays the latest existing
fresh-browser worker log across requests. Worker events arriving before the
request returns are not overwritten by the acknowledgement. This is NOT a
per-browser job list. Existing Home logging and bot controls are preserved.

Settings remember the latest accepted request (including for later bot defaults),
even if that browser later fails to start. Preflight errors do not save settings.
Run starts fresh login capture, NOT the chat bot. Existing login JSON format,
proxy rotation, temporary profiles, close-after-save, and cleanup remain intact.
Headless is preserved but should be OFF for interactive manual login.

The legacy SaveBrowserSettings and RunFreshBrowsers APIs remain for compatibility;
only the Browser UI's old controls were removed.

## Verification performed in the Linux sandbox

- `node --check cmd/chitchatui/frontend/dist/app.js`: PASS.
- Actual frontend script + HTML tested with jsdom 26: **9/9 PASS**.
- `gofmt` for changed Go files: PASS.
- `go test ./internal/model`: PASS (package has no test files).
- Exact production validateBrowserRun + safeProfileName functions, copied into
  a temporary isolated package with the real model source, passed the validation
  test. This checks only those helpers, NOT the full app/dispatch path.
- Byte comparison against original archive: only the six listed existing files
  differ. Legacy app, browser drivers, license, chat/AI and dependencies unchanged.
- Full Go tests / Windows cross-build: **BLOCKED**, not passed. The environment
  cannot fetch modules from proxy.golang.org (EOF). No dependency versions or
  module files were changed to work around this restriction.
- Windows desktop launch/login, real proxy and Wails runtime: **NOT RUN**.
- Integration tests in browser_run_test.go are supplied but not executed here.

## Reproduce frontend checks

Use a temporary dependency directory so production dependencies are untouched:

```powershell
npm install --prefix "$env:TEMP/browser-ui-tests" --no-audit --no-fund jsdom@26
$env:NODE_PATH = "$env:TEMP/browser-ui-tests/node_modules"
node --check cmd/chitchatui/frontend/dist/app.js
node --test tests/browser-ui.test.cjs
```

## Required Windows verification before release

From the extracted project directory, with Go 1.25+ and Wails dependencies:

```powershell
go test . -run 'Test(ValidateBrowserRun|DirectRun)' -v
go test ./internal/bot ./internal/uistore
go vet ./internal/bot ./internal/model ./internal/uistore
go build ./...
wails build -clean -webview2 error
```

Manual checklist:
1. Browser shows Available: 2, 01 Rod / Run, 02 ChromeDP / Run; no Save button.
2. Previously saved Rod -> click ChromeDP Run: actual ChromeDP capture starts.
3. Reverse the backends and repeat; use Headless OFF for manual login.
4. Set a new Profile and Count 1, then 5. Verify destination and browser count.
5. Finish login: correct backend/profile in saved login JSON, browser closes.
6. Close before login; test invalid proxy/browser startup error. Status reports
   the outcome and Run remains usable.
7. Edit profile/headless; click Refresh or save another tab. Browser edits remain.
8. Restart app: accepted Profile/Headless defaults return.
9. Verify existing bot Start/Stop and saved-session discovery still work.

Do not treat this change as fully runtime-verified until these checks pass.
