# Show Mode DOM detection fix — scope and evidence

Date: 2026-10-14. Owner authorized a fix targeted at Browser option 03 only.

## Packaging/layout

The checkout originally contained `root-project-showmode-dom-UNVERIFIED.zip`
only. Source is now extracted as `root-project/` so this fix is editable/reviewable.
The original ZIP remains byte-identical (SHA-256 below); extraction is not a
rewrite of every source file. A new separately named UNVERIFIED source ZIP is
provided for Windows testing. Do not replace an existing data folder.

Original ZIP SHA-256:
`428611a91dc63500fa579acd2291b095ef0bffb30fa497bf24d60e58dbbd2342`

## Production scope (three files)

| File | Change |
|---|---|
| `internal/browser/domchat.go` | Show Mode-only DOM contracts/snapshot/native send verification. Shared Backend and driver startup/login methods untouched. |
| `internal/browser/showmode_dom.js` (new) | Embedded exact-selector parser, grouped authors, observer/watchdog queue, identity, active cycle control/state. |
| `live_chat.go` | Show Mode-only serial runner, IDs/author filtering, generation checks, safe failure/rotation, local username substitution/no-username EVA gate. |

Supporting changes only:

- `live_chat_test.go`, `internal/browser/domchat_test.go`.
- `tests/showmode-dom.test.cjs`, synthetic `tests/fixtures/showmode-chat.html`.
- `package.json`: adds test to `npm test` and `test:showmode`.
- `scripts/verify.cjs`: adds Show Mode DOM test to existing Node gate.
- `docs/SHOW_MODE.md`, this scope report, map/memory and repository-root memory.

## Explicitly untouched

`app.go`, `main.go`, `internal/browser/{rod,chromedp,backend,session,scripts}.go`,
all `internal/bot/**` (including EVA/AI core/assets, worker/API/realtime/registry),
`internal/model/**`, license, proxy, store/uistore, workerplan, runtimeassets,
frontend assets/bindings, legacy desktop entry, installers/bundlers, go.mod,
go.sum, package-lock.json, capture and apitest. No DB migration or CI added.
The existing call after `settings.LiveChat` remains the sole production entry.

## Executed evidence (not live-runtime proof)

- Actual production JS evaluated against both authorized saved DOM snapshots
  010/011 in offline jsdom: all three IDs, grouped author, self/partner, composer
  and active SKIP detected. Private capture content is NOT copied into source.
- Dedicated synthetic DOM suite: 18 passing tests (geometry shim only; real
  production parser/observer). Includes hydration, invisible time, grouped author,
  same text/new ID, rerender, retained rows, remount, same-name rematch, exact
  active labels, busy/disabled controls, unknown authors, overflow and no-network
  source checks. New all-Node suite: 52 tests passing including existing tests.
- Isolated Go harness: exact production `live_chat.go`, `domchat.go`, tests and
  embedded helper compiled against real pinned Rod v0.116.2 sources; model and EVA
  core/assets were copied unchanged. Test-only App/settings/license and Rod-page
  boundaries replaced desktop/Badger/startup dependencies, not the runner logic.
  Nine root runner/responder tests and browser confirmation tests (seven cases)
  passed with `-race`; isolated `go vet` passed; Windows-target test binaries for
  both packages compiled, **not executed**. This is NOT a full desktop build/test.
  All harness/dependency substitutions lived outside the project; real module
  files and shared drivers were not changed to obtain this evidence.
- Full standard gate: run with actual Go 1.27.2 installed in scratch. Module
  download fails with EOF at the inaccessible Go proxy. Dependent Go regression,
  vet, full Windows build/tests and Wails build remain BLOCKED. The current
  `VERIFICATION-REPORT.json` in the new source ZIP is authoritative.
- Windows desktop/site/manual login: **NOT_RUN**. No real account, browser,
  remote license check, chat API or LLM request was run during verification.

## Regression boundary audit

Every one of the original 165 source entries is compared byte-for-byte against
the original ZIP. The only changed original entries are the two production Go
files, their existing live runner test, two test-wiring files and three existing
Show Mode/map/memory docs. No original file is missing. All other original files
are byte-identical, including ordinary Rod/ChromeDP, workers, frontend, module
locks and licensing. New files are listed above; no capture/runtime data shipped.

## Limits

This is source for a Windows retest, not a certified release or a prebuilt EXE.
DOM-only automation necessarily depends on live layout, account identity labels
and the active page. Unknown identity/send uncertainty pauses visibly rather
than guessing. Do not claim exact-once server delivery from DOM confirmation.
The separate capture observer Map/add bug was intentionally not touched.
