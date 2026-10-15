# Project Memory — decisions and current state

Purpose: when this project is shared with another agent/model, this file plus
`AGENTS.md` and `docs/PROJECT_MAP.md` should be enough to understand where
things stand without re-asking the user. The AUTO block below is refreshed by
`scripts/project-map.cjs`; the decision log is manual — **append an entry
after every meaningful change** (newest last). Never rewrite old entries.

<!-- AUTO:BEGIN -->
_Auto status 2026-10-14. Do not edit this block by hand._

- Source fingerprint: `d925500ebaaed804…`
- Browser options: `rod`, `chromedp`, `show_mode`
- Capture tool: v1.2.1
- Last verification: **BLOCKED** (PASS 5, FAIL 0, BLOCKED 8) — BLOCKED is not PASS; see `verification-output/report.json`
<!-- AUTO:END -->

## Decision log

- (template) YYYY-MM-DD — what changed, why, and how it was verified.
- 2026-10-14 — Browser direct-run: numbered Rod/ChromeDP rows each with their
  own Run button; shared Profile/Count (1–5)/Headless; accepted settings are
  remembered. Browser-free verification system added (see docs/VERIFICATION.md).
- 2026-10-14 — Show Mode added as Browser option 03: a visible Rod preset.
  Backend normalizes `show_mode` → `rod` + `headless=false` before saving or
  dispatching, so saved sessions stay compatible. Not an anti-detection mode.
- 2026-10-14 — Standalone manual capture tool added under `capture/`
  (dependency-free Node + the user's Chrome/Edge via CDP). Captures HTTP,
  WebSocket frames, DOM message hints and storage, redacted by default, into
  `capture/capture-sessions/` (excluded from source packages). Purpose: the
  user captures real traffic manually so future WebSocket/API automation can
  be built from evidence instead of guesses.
- 2026-10-14 — Project map/memory system added: this file plus
  docs/PROJECT_MAP.md are auto-refreshed by scripts/project-map.cjs on every
  `npm run verify`. Capture tool gained a WebSocket event catalog, manifest
  schema fingerprints and a session compare tool (`capture/tool/compare.cjs`)
  so a future chitchat.gg API/WebSocket change can be diagnosed by capturing
  again and diffing against a baseline instead of starting from zero.
- 2026-10-14 — Capture tool v1.2.0 after first real Windows run failed with a
  bare "fetch failed" (DevTools endpoint was polled only once). Fixes: free
  port selection + 45s DevTools polling with browser-stderr `browser.log`,
  `tool/doctor.cjs` smoke test (actually launches the browser via CDP),
  one-click `install.ps1` (winget → MSI fallback for Node, winget Chrome
  fallback), live capability checklist + `[live]` status lines in the
  terminal, and a session `SUMMARY.md`. User machine: Node 24 + Microsoft
  Edge confirmed by the old install check.
- 2026-10-14 — First real ChitChat capture analysed end-to-end and distilled
  into `docs/CHITCHAT_PROTOCOL.md` + `docs/chitchat-protocol.json` (baseline
  session 20261014-185017, schema fingerprints recorded there). Proven facts:
  Socket.IO `chatMessage` = incoming; sending = `POST
  /users/me/conversations/:id/messages` multipart `{content, nonce}`;
  skip = `PATCH /match/disconnect` + `POST /match`; session is cookie-based.
  Existing `internal/bot/realtime.go` speaks the same socket protocol, so the
  capture confirms it. Show Mode live-chat design (in-browser, CDP frames +
  page-context fetch, no DOM selectors) is documented in the protocol file
  and awaits user approval. Capture redactor no longer hides `author` fields
  (v1.2.1).
- 2026-10-14 — Show Mode live chat implemented (user-approved, evidence from
  docs/CHITCHAT_PROTOCOL.md): `BrowserSettings.LiveChat` is set only by the
  Show Mode preset; after login save, `app.runLiveChat` keeps the Rod browser
  open and chats in place — incoming from CDP Socket.IO `chatMessage` frames
  (`internal/browser` `ChatMessageSource`, Rod-only), replies from the
  selected mode (EVA engine via exported Start/Reply; sequences walk their
  lines; EVA AI compose not applied live), sent via the page's own
  authenticated fetch (multipart `content`+`nonce`), skip via
  `PATCH /match/disconnect` + `POST /match`. Closing the browser stops it.
  Browser-free fake-backend tests cover greeting/reply/own-ignore/dedupe/
  skip; full Go compile and live Windows run still pending user testing.
- 2026-10-14 — Show Mode live fix after the user's first real test (Folder
  Sequences sent one line, incoming never detected): ROOT CAUSE — the real
  `chatMessage` author is an object `{id, username}` (as the existing
  realtime.go parses), but the new live parser declared it a string, so
  every real frame failed to parse. Parser now accepts object/string/null
  authors (isolated tests pass with the real shape). Also: API
  history-polling fallback for detection, history seeding so old messages
  are never answered, Folder mode picks one sequence per conversation
  (not all files flattened), transient-empty partner-left tolerance, and
  live INCOMING/SEND/SKIP logs at ACTIVITY level so they show on Home.
- 2026-10-14 — Show Mode rebuilt DOM-only (user explicitly retired the
  WebSocket/API approach: "only browser diye, no websocket api"): the old
  `internal/browser/livechat*.go` transport was deleted; Show Mode live chat
  now reads message rows from the page DOM, types replies into the message
  box, and clicks Skip/START (`internal/browser/domchat.go` +
  `live_chat.go`). Structural heuristics: rows are elements containing a
  `10:24 PM`-style time whose first line is the author; input found by
  "message" placeholder; buttons matched by label. Runner seeds history,
  dedupes by author+text counts, suppresses own echoes, clicks START in the
  lobby, logs a one-line page probe when no chat box is visible. Verified:
  full Windows build (`GOOS=windows go build ./...`) PASSED in a temp copy
  using GitHub-mirror replaces for the blocked vanity module paths (real
  go.mod untouched); Go tests compile and `go vet` is clean; the runner
  state machine was EXECUTED against a fake DOM backend in a throwaway
  Linux harness (START→greeting→reply→echo-suppression→skip→re-match→
  partner-left; it caught and fixed a 6s START-throttle that delayed
  re-matching); jsdom simulation of the DOM heuristics 12/12 PASS; Node
  source tests PASS. The protocol docs remain as evidence only; real DOM
  behaviour on the live site awaits the user's Windows retest. Options
  01/02 untouched.
- 2026-10-14 — HANDOFF / session close (user request): DOM Show Mode
  packaged as `root-project-showmode-dom-UNVERIFIED.zip` (repo root;
  integrity: `node scripts/package-source.cjs --check <zip>`). Full
  `root-project/` source is committed to git from this push on (it was
  previously delivered as zip-only). **Next session start:** read
  AGENTS.md + docs/PROJECT_MAP.md + this file + docs/SHOW_MODE.md.
  **Awaiting:** user's Windows retest of the DOM build. If detection fails,
  the Home log's `Page probe:` line shows what the bot saw — fix the
  heuristics in `internal/browser/domchat.go` from that evidence; do not
  revive the WebSocket/API route for Show Mode.

- 2026-10-14 — Owner authorized targeted Show Mode incoming-message fix. Replaced clock/name guessing with an embedded exact-selector DOM observer, stable row IDs, grouped-author parsing, safe self/partner classification, serial queue and native Rod input/Enter confirmation. Generation checks prevent stale reply/skip; uncertain sends pause without automatic retry. Only domchat.go, new showmode_dom.js and live_chat.go are production changes; local live adapter renders configured handles and applies the existing EVA no-username gate without changing worker/AI core. Tests/wiring/docs updated; all other original files byte-compared against the original ZIP. New Node suite 52 PASS; isolated production-runner/Rod-boundary Go race tests/vet and Windows-target test compilation PASS, not full-app verification. Full standard gate BLOCKED by dependency proxy EOF; Windows live/site NOT_RUN. See SHOW_MODE_FIX.md. Source is now actually extracted under root-project/ in this repository; original ZIP unchanged, new download remains UNVERIFIED.

- 2026-10-14 — Owner reports the delivered Show Mode v2 works fully in their
  Windows retest (user report, not a sandbox live run). Keep that chat/browser
  implementation untouched. Next authorized scope: Home logs only. All App.log
  levels now append to `logs.txt` beside the active `data/` directory, independent
  of Badger/UI persistence; main routes Go and emitted Wails diagnostics there.
  Home log rows/rendering removed; counters/actions and Browser status events
  preserved. Frontend backend-call failures/uncaught errors use the existing Wails
  logging bridge without serializing request/settings arguments. Text file uses
  UTF-8 JSON lines with UTC time, level, source and untruncated message; appends
  across sessions and survives Clear Logs/Clean. No automatic rotation/truncation.
  Known credential forms are masked; review personal chat data before sharing.
  Disk-full/permission failures are reported to stderr (cannot promise a file
  write when the OS refuses it); Wails' existing framework log-level filtering
  is unchanged. Go logger tests/race/vet and Windows-target test compilation PASS;
  Node suite 57 PASS. Real root App/Badger tests and full desktop verification
  remain dependency-blocked; Windows runtime retest of this logging change is
  NOT_RUN. No chat/worker/driver/license/model/DB schema/binding changes. Added
  runtime logs.txt Git/source-package exclusion and scoped regression coverage.
  No new ZIP or remote push was requested for this step; previous working ZIP
  stays unchanged.

- 2026-10-15 — Owner requested restore of the working project and completion
  of Home file logging after audit found missing frontend assets and uncaptured
  driver stdout. Restored all missing UI assets from the owner-tested v2 ZIP;
  styles, Show Mode/browser/worker/license/proxy/model/store/lockfiles remain
  unchanged. Reapplied only Home log removal and frontend error forwarding.
  Desktop-only stdout/stderr capture now retains printed driver diagnostics,
  drains partial/long lines at normal shutdown and uses original stderr on disk
  errors (no recursion). Go SetCrashOutput appends unhandled goroutine panic/
  fatal runtime stack text directly to logs.txt; ordinary records remain JSON
  lines, so crash files may mix JSON lines and plain text. Known-token redaction
  applies to ordinary records, not raw runtime crash dumps; review before sharing.
  Wails INFO/WARNING/ERROR enabled, raw RPC DEBUG/TRACE deliberately not enabled.
  58 Node tests PASS; 13 actual Go logger race tests and vet PASS; Windows-target
  logger tests compile only. Full standard gate dependency-BLOCKED; root App/
  Badger and native Windows logging test NOT_RUN. See FILE_LOGGING.md. A separate
  UNVERIFIED source archive preserves all frontend files; previous working ZIPs
  kept unchanged. No remote push or main merge implied.

- 2026-10-15 — Owner approved read-only Home live User/Bot transcript plan.
  Implemented separate in-memory bounded feed from actual Show Mode DOM rows,
  ID/generation/run dedup, full-text safe UI render, row ordering reserved before
  hydration, multi-browser/conversation selector, closed-session replay and
  coalesced Wails notifications plus one-second resync. Optional native-send
  snapshot hook retains final actual own row before Skip; no intent/draft is
  displayed. runLiveChat only wraps/starts/stops a passive 250ms observer; all
  reply/queue/rotation decisions and shared drivers/worker/AI/license/model/stores
  remain unchanged. Diagnostic logger/logs.txt remains separate and unchanged;
  raw SMS is not persisted. History: 500 messages/session, 12 sessions, visible
  retention notices; desktop restart clears memory. Node 62 PASS; isolated real
  production transcript/runner/Rod compilation and race tests/vet PASS; actual
  logger race regressions PASS. Full standard gate dependency-BLOCKED and native
  Windows display acceptance NOT_RUN. See HOME_CHAT.md for scope, test boundaries
  and usage. Owner approval preceded all code edits; no remote push implied.

- 2026-10-15 — Owner requested fresh all-update recheck and full source ZIP.
  No production code or dependencies modified. Node 62 PASS; Show Mode subset
  separately 19 PASS; capture mock tests 12 PASS; actual logger's 13 race tests,
  vet and Windows-target compilation PASS. Actual standard-library package
  regressions/vet (workerplan/runtimeassets etc.) and standalone apitest unit
  tests/vet PASS; no real API/browser run. Isolated exact transcript/runner/real
  pinned Rod race tests/vet/Windows-target compilation repeated PASS; these are
  NOT full App/Badger/Wails proof. Full standard gate remains dependency-BLOCKED;
  native Windows/live display acceptance NOT_RUN. See RECHECK_2026-10-15.md. Do
  not call all checks green or rename the source archive VERIFIED. Previous
  working ZIPs unchanged; rechecked test-only download remains UNVERIFIED.

## Known limitations (keep honest)

- Full Go/Windows verification depends on module downloads and a Windows
  environment; sandbox runs may legitimately end BLOCKED. BLOCKED is not PASS.
- Live browser/login behaviour is only proven by the user's manual Windows
  test unless explicitly stated otherwise.

## How to add memory

1. Run your change and its tests.
2. Run `npm run map:update` (or `npm run verify`, which does it first).
3. Append one dated bullet to the decision log above: change + reason +
   verification evidence. Keep secrets, tokens and account data out.

## 2026-10-15 — HTTP/SOCKS5 and dashboard Test Proxy

Owner requested authenticated HTTP/SOCKS5 format support and in-dashboard tests.
Added safe parser formats/IPv6/aliases, plain-line default protocol selector,
unsaved 20-line/four-worker HTTPS checks with sanitized per-line results, and
one loopback relay per Rod/Show Mode/ChromeDP proxied browser. Both test and
browser use the same upstream CONNECT/SOCKS5 auth implementation; no direct
fallback. Cleanup, TLS, failure classification and literal UI rendering tested.
65 Node tests PASS; actual proxy race/vet PASS; actual browser/proxy sources
compile/race/vet and Windows-target test compilation in pinned-dependency isolated
module PASS. Not a native Wails/Windows/owner-proxy verification. Full gate BLOCKED.
See PROXY_TEST.md. Latest full source: root-project-proxy-dashboard-UNVERIFIED.zip.
No dependency lock, chat/AI/Home/worker/license changes. No new remote push.

## 2026-10-15 (later) — provider rejection diagnostics for Test Proxy

Owner reported a real Webshare line failing the dashboard proxy test with
`HTTP proxy rejected CONNECT (402)` and asked to stop looping and settle it.
Investigation result: the CONNECT request the app sends was verified
byte-for-byte (`CONNECT host:port HTTP/1.1`, `Host`, `Proxy-Authorization:
Basic …`) and matches curl's shape, so the 402 is the provider's own answer and
the app was previously throwing that answer away. Fix: `TunnelError` now carries
the proxy's status, `Proxy-Authenticate` and a bounded (512 B / 1.2 s) body
excerpt, sanitized to one credential-free line (tags stripped, control chars
removed, username/password and Basic token redacted, 200-char cap); the test
result gained `hint`, `provider_message`, `proxy_authenticate` and
`authenticated`; the dashboard renders "Provider said"/"Auth challenge"/"Next
step" and, for a line without credentials, says so explicitly. No new network
request was added and no exit IP is collected. `internal/proxy/diagnostics_test.go`
covers capture, redaction, sanitizing, truncation, 402/407/no-credential hints
and the network-failure hint. Evidence: `go test -race ./internal/proxy`,
`go vet ./internal/proxy`, `go build ./internal/browser`, 65 Node tests PASS.
The full standard gate stays dependency-BLOCKED (badger's
`golang.org/x/net`, `google.golang.org/protobuf`, `go.opentelemetry.io/otel`
vanity paths are unreachable from this sandbox), and the owner's real
proxy/Windows acceptance is still NOT_RUN. Behaviour docs: PROXY_TEST.md.
