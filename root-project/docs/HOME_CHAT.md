# Home live browser transcript — 2026-10-15

Owner approved the file-scoped plan: display only full actual User/Bot messages
in Home's former technical-log area. This is a read-only Show Mode display, not
another transport, reply engine, worker integration or a resend mechanism.

## Operation

Run Browser -> 03 Show Mode and login manually as before. Home shows actual
partner rows as `User:` and own-account rows as `Bot:` (including manually sent
own-account rows). Drafts/proposed reply text do not count. Existing visible
history is displayed but remains seeded/ignored for automated replies exactly
as before. Consecutive user rows are not forcibly alternated with bot rows.

The existing DOM observer provides ID, author, full text and additive row-order
metadata. Positions are reserved before delayed text hydration. A passive 250ms
reader runs while replies are delayed/paused; it never types, sends or clicks.
An optional native-send observation hook retains actual snapshots, including a
confirmed final reply before immediate Skip. Boolean send semantics and native
input/Enter checks are unchanged. Unknown/conflicting identities are not guessed.

A separate local Wails `ui:chat_changed` notification triggers a coalesced
`GetLiveChatSnapshot(sessionID)` request. This is local desktop/UI IPC, NOT the
website chat API/WebSocket. One-second UI snapshot replay repairs missed events
and updates closed-session status. Delay depends on browser/CPU/layout readiness;
this is not a zero-latency/exactly-once server-delivery claim.

`Latest active chat` follows the most recently created active conversation. The
selector lets the owner inspect another browser or retained earlier conversation.
Different run/generation keys never mix same IDs/names. A late final receipt
updates its previous conversation without reactivating it. Lobby/close preserves
the last transcript; normal new-match generation produces a distinct session.

Display history retains up to 500 messages per conversation and 12 conversations
(inactive oldest evicted first; if all are active, oldest display history is
evicted without stopping its browser). Notices report discarded older history. Retained
individual messages are not truncated; multiline/Unicode/long text uses safe
textContent + pre-wrap, never innerHTML. Auto-scroll follows the bottom but does
not force a reader down when inspecting older messages. Transcript data is only
in memory: page refresh/tab switching can replay it; desktop restart clears it.
Clear Logs retains its previous technical-DB-only meaning; it does not clear this
transcript or logs.txt. No raw SMS persistence was added to logs.txt or the DB.

The original DOM reader still fails closed at its existing 2,000-row capacity;
missing authors/layout changes/site removal before any scan cannot be promised
as captured messages. These limits are not hidden by this display feature.

## Production scope

- New `home_chat.go`: mutex-protected bounded transcript, session separation,
  passive observer, additive send wrapper, notification and snapshot handling.
- `app.go`: one feed field and one public read-only getter only.
- `live_chat.go`: start/stop/wrap the observer in runLiveChat only; responder,
  queue, timing, send decisions and Skip/START rules remain unchanged.
- `internal/browser/domchat.go`: optional SendDOMMessageObserved; old method
  delegates with nil callback. Existing native send body gains snapshot taps only.
- `internal/browser/showmode_dom.js`: display-order metadata only; selectors,
  author classification, text extraction and conversation logic unchanged.
- `cmd/chitchatui/frontend/dist/{index.html,app.js,styles.css}`: Home panel,
  dedicated events/getter, session choice, safe render/resync/autoscroll, scoped CSS.

Supporting changes: home_chat_test.go; existing browser-UI/DOM tests; gofmt gate
list; project map/memory and this guide. No changes to shared Rod/ChromeDP driver
startup/login/proxy, worker/AI/license/model/store, diagnostics logger, bindings,
module locks, installers, other message modes or existing counters/actions.

## Evidence

- All Node tests: 62 PASS, 0 FAIL; actual production frontend/embedded DOM helper.
- Isolated harness: exact production home_chat/runner/DOM-send sources, unchanged
  model/EVA core, real pinned Rod v0.116.2; only Wails/App/Badger/license/Rod-page
  startup boundaries replaced outside the project. Ten new transcript tests,
  nine existing runner/responder tests and browser confirmation cases pass with
  race detection; isolated vet and Windows-target test compilation pass. This
  is NOT a full Wails/native browser test. Real module files are unchanged.
- Actual diagnostics logger package regression: 13 tests with race detection PASS.
- Full standard gate remains dependency-proxy BLOCKED. Real root App/Badger/Wails
  Windows integration and native live-site acceptance for this display: NOT_RUN.

Manual Windows acceptance: opener -> incoming -> reply; consecutive/repeated
messages; long/multiline/emoji; unsent draft not shown; unconfirmed send only shown
if an actual own row exists; final reply/Skip and partner change; multiple browser
selection; refresh/tab return; automation paused; browser close; markup displayed
literally; technical errors stay in logs.txt rather than transcript rows. Verify
ordinary Rod/ChromeDP, counters, buttons and existing Show Mode replies unchanged.
