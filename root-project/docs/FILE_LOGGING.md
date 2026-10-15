# Home file diagnostics — targeted change

Owner request: save dashboard activity/error/other logs in project `logs.txt`,
remove Home's log stream, and preserve working browser/chat features.
Completed source update: 2026-10-15 (owner timezone).

## Location and use

Launch the newly built app using the normal `run-dashboard.bat` / installer.
The file is `<project>/logs.txt`, next to the existing `<project>/data/` directory.
If the EXE is launched with a different working directory, the file follows that
runtime data directory; no data-folder relocation is introduced here.

All App.log levels, dashboard-worker callbacks, Show Mode activity/error events,
Go standard logger output, desktop Go stdout/stderr (including driver fmt.Print
messages), and emitted Wails INFO/WARNING/ERROR logs reach the same file.
Frontend backend-call failures and uncaught errors use the existing Wails logging
bridge, without serializing invocation arguments. Framework raw RPC DEBUG/TRACE
logging is deliberately not enabled: it can dump sensitive settings. App logs
have no level filter. This is desktop logging, not an alteration of standalone
CLI, capture tool, or AI-lab logging.

Ordinary records are UTF-8 JSON lines with UTC `time`, `level`, `source`, and
`message`. Multiline errors are escaped inside the message, never truncated by
the new sink. App/browser/profile labels already present in the messages are
preserved. Information already omitted/truncated by an unchanged upstream log
producer cannot be reconstructed by this sink.

An unhandled Go panic/fatal runtime crash appends a **plain-text runtime dump** to
this same text file via Go's SetCrashOutput, including the stack/source lines.
After a crash the file can therefore contain JSON lines followed by plain text;
do not assume the whole file is a JSON document. The direct crash output survives
fatal process exit without relying on a goroutine to flush an asynchronous pipe.
Normal shutdown drains both stdout/stderr streams, including partial final lines.

The file appends across restarts. Clear Logs still clears only legacy UI database
history; it does NOT erase logs.txt. Clean does not erase logs.txt either. No
automatic truncation/rotation was introduced: back up and manually remove this
file with the app stopped if it becomes large. Old DB-only history is not
backfilled into the file.

Known credential forms are masked for ordinary records (token/password/API key,
Authorization/Cookie and URL userinfo). Raw runtime crash text is not redacted by
the Go runtime; arbitrary private chat text/unlabeled secrets are not guaranteed
to be detected. Review the file before sharing it. Never commit/runtime-package
logs.txt: .gitignore and source inventory exclude it.

OS kill/power loss, a crash before capture installation, external browser-process
output not emitted into the desktop process, and disk-full/permissions failures
cannot be promised as saved logs. Disk write errors use the original stderr
handle, not the captured pipe, so they cannot recurse into the failing sink.

## Production file scope

- `app.go`: new import and existing log() function only. Writes file before UI DB.
- `main.go`: logging setup/capture and Wails logger configuration only.
- `internal/applog/applog.go`: append-only structured logger and known-secret masking.
- `internal/applog/capture.go`: startup-only process streams/crash hook.
- `cmd/chitchatui/frontend/dist/app.js`: remove Home render/append functions and
  calls; keep Browser status events; report frontend errors via Wails.
- `cmd/chitchatui/frontend/dist/index.html`: remove log container, update Home
  explanatory text and Clear Logs tooltip; keep counters and action buttons.

Missing frontend files were restored first from the owner's working v2 archive.
`styles.css` stays byte-identical. Show Mode/live_chat, shared browser drivers,
workers/AI, license, proxy, model, stores, bindings and dependency locks stay
unchanged. Support edits are scoped tests, runtime exclusion, verification wiring
and project memory/map only. Original two working archives remain unchanged.

## Evidence and pending acceptance

- Node suite: 58 PASS, 0 FAIL, including actual-frontend Home removal, controls,
  browser status and error-forwarding regressions; existing Show Mode tests pass.
- Actual `internal/applog` package: 13 Go tests PASS with race detection; vet PASS.
  Tests cover concurrency, all levels, long/Unicode/multiline records, restart
  append, captured stdout/stderr and partial lines, disk-error nonrecursion, and
  a real synthetic goroutine panic in a disposable subprocess.
- Windows-target logger test binary compiles; not executed on Windows.
- Full normal verification remains BLOCKED by dependency-proxy EOF. Root App/
  Badger integration tests and full Wails build remain unverified in this sandbox.
  Native Windows/site test of this logging change is NOT_RUN.

Retest on Windows: launch -> Home has no log rows -> run the existing working
Show Mode -> inspect logs.txt for DOM READY/INCOMING/SEND CONFIRMED -> close the
browser and see stop/page diagnostics -> restart app and confirm old history
remains. Clear Logs should not delete that history. On any error send a reviewed
copy of logs.txt, not profiles, cookies, storage-state or the data directory.

## Subsequent approved Home display

Home now has a separate actual-message User/Bot transcript (HOME_CHAT.md). The
technical log stream stays removed; file diagnostics behaviour is unchanged.
