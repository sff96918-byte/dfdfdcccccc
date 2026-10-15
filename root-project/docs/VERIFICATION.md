# Browser-free advanced verification

## What this adds

This is a reusable project workflow, not a promise of real-browser success.
No default check opens Chromium, Wails UI, or a real login. Synthetic sessions,
temporary directories and local HTTP test servers are used instead of accounts.

1. **UI tests:** direct-run behavior, pending-click protection, errors, defaults,
   refresh protection, escaping, asynchronous status.
2. **Contract checks:** literal UI calls resolve to public Go methods; direct-run
   signature agrees with wrappers; DOM IDs exist and are unique; advertised
   backends agree with the factory. These are lightweight source checks, not
   Wails WebView end-to-end tests.
3. **Production lifecycle tests:** the real runFreshBrowser capture/save/cleanup
   flow is invoked with a fake Backend. Tests cover success, launch failure,
   capture failure/closure, no token, unauthorized/failed authentication, missing
   user ID, save failure, factory failure, close-before-delete and temp cleanup.
   Concurrent direct requests are checked for settings isolation.
4. **Regression/static/build checks:** existing browser-free internal-package
   tests, go vet, Windows cross-build, Windows-only app tests/vet, production Wails
   build. Go module checksums are verified. Wails builds with skipbindings / skip
   mod-tidy / skip frontend flags to avoid rewriting verified source; binding
   contracts are checked separately. Builds do not launch the built executable.
5. **Evidence:** every executed check records PASS/FAIL/BLOCKED, exit code and
   a local log. Source SHA-256 fingerprint detects edits after verification.
6. **Release gate:** normal source packaging refuses blocked/failed/stale reports.
   ZIP entries are hashed and the resulting ZIP is reopened and checked.
7. **Optional bundle preflight:** validates executable headers/architecture,
   runtime presence, a locally generated checksum baseline, Windows x64 and
   folder writability. It never runs those executables.

The one previously browser-launching test is now behind `//go:build browser_live`.
The verification runner clears inherited GOFLAGS so it cannot accidentally opt
into that tag. Do not add browser-launching tests to the default test suite.

## Windows: one-command verification

Prerequisites: Node 22, Go 1.25+, Wails CLI 2.12.0 and normal Wails Windows build
prerequisites. No license/account needed for tests. Test App instances bypass
NewApp hardware/license calls; production licensing is unchanged.

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
.\verify.bat
```

`verify.bat` installs exact Node dependencies with `npm ci --ignore-scripts` and
runs the gate. A nonzero exit code means NOT verified. Read:

- `verification-output/report.json`
- `verification-output/*.txt` per-check logs

For portable commands:

```text
npm ci --ignore-scripts --no-audit --no-fund
npm test
npm run verify
npm run package:source
```

Normal packaging requires ALL required checks to PASS and source unchanged since
verification. Node/Go test tools are development-only, not added to the shipped
application. `verification-output/`, node_modules and runtime data are excluded
from source inventory.

## Sandbox / incomplete environment

Linux can run UI/contract tests, package tests, and available Go checks. Windows
app tests/Wails build remain BLOCKED there, NOT PASS. Dependency download failure
also blocks dependent tests/builds; the runner does not count unexecuted checks.

An explicitly labelled source handoff for manual testing can still be generated:

```text
node scripts/package-source.cjs --allow-unverified
```

It is named `root-project-UNVERIFIED-source.zip`, includes the actual report and
manifest, and is NOT a verified release. It refuses stale source even with this
flag. Archive hashes detect accidental changes, not malicious replacement of
both archive and manifest; they are not digital signatures.

## Windows CI

Repository file: `.github/workflows/browser-free-verification.yml` (beside
root-project, not inside it). Runs on push/PR changes or manual dispatch once the
workflow is on GitHub and Actions is enabled. This change does not push/trigger
it automatically. The root-project download does NOT include the workflow;
it contains only the root-project source plus its report/manifest/evidence.

Windows runner installs pinned Go/Node/Wails versions, runs the gate, then
packages ONLY on success. Failure logs are uploaded too. The executable artifact
is a desktop build, not a fully portable Chromium/WebView2 bundle and not
live-tested. The source ZIP records `liveBrowser: NOT_RUN` even after a PASS.

## Optional portable-bundle checks

The existing bundle installer is unchanged and still downloads its runtime
versions dynamically. No untested Chromium/WebView2 version is labelled as a
known-good pinned runtime. Runtime pinning requires selecting and validating
those actual runtime versions first; this remains a release-maintenance task.

After creating a portable bundle with the existing bundle script:

```text
node scripts/preflight.cjs dist/windows-x64 --write-manifest
node scripts/preflight.cjs dist/windows-x64
```

First command records actual binary/runtime checksums; second verifies them on
Windows x64 and probes folder writability with a disposable directory. It does
not read or alter data/. Node is required for this development preflight tool;
normal application use does not require Node. Preflight is optional and separate
from install.bat so existing setup behavior is not silently changed.

This checks consistency only. It cannot guarantee publisher authenticity,
antivirus approval, credentials, CAPTCHA, proxy connectivity or remote API
compatibility. Do not create a new checksum baseline just to hide unexpected
binary changes.

## Scope and actual status of this delivery

Production lifecycle change is limited to app.go: a local backend-factory injection seam,
and earlier cleanup registration so factory/initialization errors do not leak
temporary browser directories. A small frontend correction keeps routine cleanup
logs from hiding the preceding login success or launch failure status. Rod/ChromeDP drivers, chat/AI, license, installer,
proxy implementation and dependency versions are unchanged.

New tools: scripts/verify.cjs, verify.ps1, source-files.cjs, package-source.cjs,
preflight.cjs; verify.bat; package.json/package-lock.json; tests; CI workflow.
Documentation and ignore rules are updated to preserve this procedure.

At implementation time in the sandbox, the Node suite passed. Full Go lifecycle
and Windows checks could not be executed due to unavailable Go module downloads
and the Linux environment. See the delivered report for exact current results.
Windows CI is configured but has NOT been run/pushed from this session.

## Debug/cleanup update

See `DEBUG_CLEANUP.md` and `../START-HERE.md`. The report now includes command,
arguments, exit code, signal, duration, reason and a checksum for each command
log. The release policy requires the complete named checklist; missing checks
cannot become PASS. Workflow changes and changed/missing evidence require a new
verification run. Unknown module failures are FAIL, not automatically BLOCKED.
Known dependency-network failures remain BLOCKED. The packaging checksum is
still consistency evidence, not a signed attestation or live-runtime guarantee.
