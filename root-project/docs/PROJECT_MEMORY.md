# Project Memory — decisions and current state

Purpose: when this project is shared with another agent/model, this file plus
`AGENTS.md` and `docs/PROJECT_MAP.md` should be enough to understand where
things stand without re-asking the user. The AUTO block below is refreshed by
`scripts/project-map.cjs`; the decision log is manual — **append an entry
after every meaningful change** (newest last). Never rewrite old entries.

<!-- AUTO:BEGIN -->
_Auto status 2026-10-15. Do not edit this block by hand._

- Source fingerprint: `ea3f38eed1775bf1…`
- Browser options: `rod`, `chromedp`, `show_mode`
- Capture tool: v1.2.1
- Last verification: **BLOCKED** (PASS 3, FAIL 0, BLOCKED 10) — BLOCKED is not PASS; see `verification-output/report.json`
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

## 2026-10-15 — Show Mode first-message setting (bot / user / random)

Owner asked for a Setting option deciding who writes first inside the browser:
the partner first and then the bot replies, the bot first, or random. Decision
(owner-approved options): the option is **Show Mode only** (Start-Bot match/DM
flows keep their own first-message rules), the value is **read once per new
conversation** so a saved change applies to the next chat without closing the
browser, and in `User writes first` a sequence starts from its **first** line so
no line is lost.

Changes (all in the approved file list; `internal/bot/**`, drivers, proxy,
applog, Home transcript, installers and dependency locks untouched):
`internal/model/ui.go` — `GeneralSettings.FirstMessageMode` with
`FirstMessageBot`/`FirstMessageUser`/`FirstMessageRandom`, default `bot`, and
`EffectiveFirstMessageMode()` normalizing empty/unknown values back to `bot`;
`internal/uistore/store.go` — that normalization on load and save;
`live_chat.go` — `liveRunner.firstMessageMode`/`rng`/`botFirst`, one decision per
conversation in `reset()`, `step()` sends the opener only in bot-first mode
(otherwise the newest partner row stays unread and the existing queue answers it
exactly once), `CHAT STARTED … first=bot|user` plus `WAITING FIRST SMS`
diagnostics, and `sequenceLiveResponder.begin()` so the first `Reply()` — not
only `Start()` — picks the sequence and uses its first line (before this fix a
user-first sequence would have ended immediately and skipped the partner);
`cmd/chitchatui/frontend/dist/{index.html,app.js}` — the Setting select and its
save/fill wiring; `models.ts`, `scripts/verify.cjs` (gofmt list), `live_chat_test.go`
and `tests/browser-ui.test.cjs` — bindings, gate list and tests.

Evidence: Node suite **66 PASS / 0 FAIL** (65 before plus the new Setting test);
`node --check` on the changed frontend files; Python brace/quote balance and
gofmt-column checks on the changed Go files (no Go toolchain exists in this
sandbox — see the sandbox BLOCKED report). Eight new Go tests cover
default/normalization, saved-setting read-back, user-first waiting and answering
once, newest-row-only answering, no repeat for an already answered row, the
default bot opener, random per-conversation decisions with safe fallbacks, and
the sequence first-line rule. Real Windows/site acceptance and the Go
compile/vet gate are **NOT_RUN** here: run `go test . -run TestShowMode -v`,
`go test . ./internal/model ./internal/uistore`, `go vet` and `verify.bat` on
Windows. Behaviour docs: docs/SHOW_MODE.md → "First message (who writes first)".

## 2026-10-15 — Show Mode first-message debug logging (owner request)

Owner asked to improve debug output for the new first-message setting ("step by
step, carefully"). Three small, behaviour-neutral additions; the chat logic,
timing, queues and first-message rules are unchanged:

1. `app.go` — the saved-settings log now names the effective mode:
   `General settings saved. Poll=… Action=… First=bot|user|random`. The wording
   moved from "Timing settings saved." because the line is no longer only about
   timing; nothing in tests or docs depended on the old string (the legacy
   mirror `cmd/chitchatui/app.go` keeps the old line and is not built).
2. `live_chat.go` — the waiting diagnostic is no longer misleading: `WAITING
   FIRST SMS | user writes first; no opener sent` is emitted only when the
   partner has not written at all, and the already-answered case logs `WAITING
   NEXT SMS | user wrote first earlier; only a new partner message is answered`
   (new `hasPartnerRow` helper; queueing untouched).
3. `live_chat.go` — `FIRST SMS ANSWERED | user wrote first; reply confirmed on
   screen` is logged once per conversation, "confirmed" meaning `sendLines` now
   reports the number of rows actually confirmed (`(bool, int)`), so an
   exhausted sequence or a failed/unconfirmed send can never produce a false
   confirmation line. It fires for the message that arrived before the observer
   started as well as for one that arrives later, and never in bot-writes-first
   mode.

Known pitfall caught while writing the tests (recorded so it is not repeated):
the first design keyed the confirmation on the opening row ID, which would have
stayed silent for the most common path (partner writes after the observer
started) and could have claimed a confirmed reply when nothing was sent.

Tests: new `TestShowModeUserFirstLogsFirstSMSAnsweredOnce` (exactly one
confirmation line, later messages are normal replies), the existing answered-row
test now asserts `WAITING NEXT SMS` and that `WAITING FIRST SMS` is absent, and
new `TestSaveGeneralSettingsLogsEffectiveFirstMessageMode` (unknown value logs
the normalized `First=bot`, user mode logs `First=user`, timing values stay).
Verification: Node suite 66 PASS / 0 FAIL, `node --check` clean, `scripts/verify.cjs`
on the changed Go files. Go tests/vet are **NOT_RUN** in this sandbox (no Go
toolchain) — run `go test . -run "TestShowMode|TestFirstMessage|TestSequence" -v`,
`go test . ./internal/model ./internal/uistore` and `go vet ./...` on Windows.
Behaviour docs: docs/SHOW_MODE.md → "First message (who writes first)".

## 2026-10-15 — Robustness fixes F1–F4 (owner-approved after the audit)

Owner asked to complete the audit findings. Four targeted fixes, each with a
test; nothing else was touched (`internal/bot`, drivers, proxy, applog, capture,
apitest, installers and the Home transcript behaviour stay unchanged):

1. **F1 symptom (`app.go`)** — `countSentMessages` no longer fails the whole
   dashboard because of one unreadable `profiles/*/bot_state.json`: it logs
   `Skipped unreadable bot state <path>: <err>` once per path (new
   `warnedStates` map on `App`, guarded by the existing `a.mu`; the dashboard
   refreshes every 3s, so logging per refresh would flood logs.txt) and keeps
   counting the readable profiles. Test:
   `TestCountSentMessagesSkipsCorruptStateOnce`.
2. **F1 root cause (`internal/store/store.go`)** — `SaveJSON` writes to a
   sibling temp file (`<name>.tmp-*`), fsyncs, closes and renames it over the
   target, retrying the rename up to 3 times because Windows can refuse it while
   a reader or indexer holds the file open. Readers can no longer observe a
   truncated file, the state every consumer (bot state, login info, DM lists,
   registry) treats as fatal. Temp names never match the exact globs
   (`profiles/*/bot_state.json`, `login_info*.json`) and are removed on failure.
   Test: `TestSaveJSONIsAtomicAndLeavesNoTempFiles` (concurrent reader + 40
   200 KB writes: no partial JSON, no leftover files).
3. **F2 (`home_chat.go`)** — `homeObservedDOM.SendDOMMessage` now holds `readMu`
   like the read path, so the 250 ms watchdog read can no longer race the DOM
   reads a send performs internally (such a race could surface as a spurious
   `SEND UNCONFIRMED` pause). Cost is a delayed display read during a send, not
   a delayed send. Test: `TestHomeObservedDOMNeverOverlapsSendWithReads`
   (4 readers + 1 send, peak concurrent DOM operations must stay 1).
4. **F3 (`live_chat.go`)** — `newestPartnerRowID` prefers the row order the page
   reports (`Order`, assigned in DOM order by `showmode_dom.js`) instead of
   trusting slice/hydration order, and keeps the old slice-order logic as the
   fallback for backends and fakes without an order. Test:
   `TestNewestPartnerRowIDPrefersReportedOrder` (ordered, own-row-after, older
   own row, orderless cases).
5. **F4 (`cmd/chitchatui/frontend/dist/styles.css`)** — `select` joined the base
   field rules (`font: inherit` plus width/padding/border/dark background and a
   pointer cursor), so the new Setting dropdown matches the dark form instead of
   the browser default; the Home chat toolbar select shares the same styling.
   Cosmetic only.

`scripts/verify.cjs` also gained `internal/store/store.go` in the gofmt list.
Verification: Node **66 PASS / 0 FAIL**, `node --check` clean, brace/quote,
unused-import, internal-selector and gofmt-column checks clean, `scripts/verify.cjs`
3 PASS / 10 BLOCKED / 0 FAIL. Go tests/vet remain **NOT_RUN** here (no Go
toolchain) — run `go test ./... -v`, `go vet ./...` and `verify.bat` on Windows.

- 2026-10-16 — New sibling folder `bulk-sites/` (owner question: can the bot run
  on more/"bulk" sites the way it runs on ChitChat?). **No root-project or
  python-port file was modified** — it is an offline starter kit for per-site DOM
  adapters plus a 13-rule contract checker: `kit/contract.cjs` (rules R1-R13),
  `kit/harness.cjs` (jsdom page + observer -> observations), `kit/run.cjs`,
  `kit/check.cjs` CLI (`--site`/`--all`/`--observer --page [--foreign]`),
  `sites.json` registry, `sites/example-room/observer.js` (worked second-site
  example, synthetic, copy-me template) and `fixtures/*` (synthetic pages plus a
  deliberately broken adapter). The `chitchat` registry entry points at the real
  `root-project/internal/browser/showmode_dom.js` and the existing synthetic
  `tests/fixtures/showmode-chat.html`, so the reference adapter is checked, not
  copied. Verified offline in this repo 2026-10-16: `npm test` 18/18 PASS;
  `node kit/check.cjs --all` PASS 26 / FAIL 0 / SKIP 0; the broken adapter FAILs
  exactly R4,R5,R8,R10,R11,R12,R13 with exit code 1 (proves the checker catches
  id-as-author, missing identities, always-true canSend, substring clicks, probe
  leaks and phantom revisions). **NOT_RUN:** any real site, login, Playwright,
  proxy, Windows or multi-session run. README documents the three onboarding
  tiers (DOM / protocol / hybrid), the capture-first workflow, risks and the
  pending inputs (site URL + one capture session) before an adapter can be
  written for a real target.
