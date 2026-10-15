# Clean root project — start here

This folder is the complete editable application source, not a prebuilt EXE.
The current browser UI has three numbered rows: Rod, ChromeDP and Show Mode,
each with its own Run button. Show Mode always uses a visible Rod browser.

## Windows setup and manual test

1. Extract the root-project download into a **new folder**. This root-only
   package does not include the repository-level GitHub workflow.
2. Open `root-project/` and run `install.bat` for the existing Windows build/setup.
3. Open Dashboard → Browser. Leave Headless off for interactive login.
4. Test the Rod and ChromeDP Run buttons with Count 1 first.

No account/session data is included. Do not overwrite an existing data folder
or delete older working copies just to test this source.

## Manual traffic capture (optional)

`capture/` contains a standalone recorder: browse any site manually in a
visible browser while network, WebSocket, DOM and storage evidence is saved
(redacted by default) into `capture/capture-sessions/`. Start with
`capture/install.bat` once, then `capture/run.bat`. See `capture/README.md`.
Capture sessions and the capture browser profile are excluded from source
packages and must not be shared when raw mode was used.

## Verification / debugging (no browser launched)

With Node 22, Go 1.25+ and Wails 2.12.0 installed, run `verify.bat`.
See `docs/VERIFICATION.md` for exact prerequisites and commands.

- `verification-output/report.json`: each check's status, reason, command,
  exit code, duration and log filename/hash.
- `verification-output/*.txt`: detailed check output.
- `npm test`: UI, contract, packaging and verification-tool tests only.
- `npm run package:source`: package only after complete verification passes.

The download contains `VERIFICATION-REPORT.json`, `SOURCE-MANIFEST.json` and
`verification-evidence/` beside the source folder. BLOCKED is not PASS. An
UNVERIFIED source download is for manual testing, not a certified release.

## What “clean” means

The source package excludes node_modules, caches, private sessions, runtime
binaries, build output and older ZIPs. Tests, lockfile and necessary source assets
(including frontend/dist) stay included. Original backups and the reference
project are not deleted. Installer, chat/AI, license and browser drivers remain
outside this cleanup's scope.

See `docs/DEBUG_CLEANUP.md` for the specific fixes in this delivery.
