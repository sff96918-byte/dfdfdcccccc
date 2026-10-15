# Show Mode (Browser row 03) — DOM observer edition

Updated 2026-10-14. Rod/ChromeDP rows 01/02, login capture, Start Bot workers,
license, proxy, DB schema and AI engine are unchanged. See `SHOW_MODE_FIX.md`
for the exact scope and verification limitations.

## Use

1. Extract the **new** UNVERIFIED source ZIP to a new folder (do not overwrite
   existing runtime data), run the usual Windows build with `install.bat build`.
2. Set Message mode and save. For the simplest test choose Single Sequence with
   three lines: `hello`, `how are you`, `nice to meet you`. If your templates
   contain `%username%` / `{snap}`, configure Message → Usernames too.
3. Browser → **03 Show Mode → Run**, Count 1. Login/CAPTCHA are manual.
4. After the existing login save, this browser stays open. Chat automation uses
   the page DOM and native browser input/Enter only. The website naturally uses
   its own networking; the bot does not create chat API/WebSocket transport.
5. Close this browser window to stop. Home Stop Bot is the separate worker
   control, not a promised Show Mode cancellation button. Do not type into the
   same composer while automation is running; existing drafts are not overwritten.

## First message (who writes first)

Setting → **First message (Show Mode)** decides how a *new* Show Mode chat
starts. The value is read once per conversation, so a saved change applies to the
next chat without closing the browser. It does **not** change the Start Bot
worker: the match/DM flows keep their own first-message rules.

| Option | Behaviour |
|---|---|
| `Bot writes first` (default) | Original behaviour: the bot opens with the selected mode's own first message (EVAChat `greeting.txt`, or the first line of the chosen sequence) and then chats normally. |
| `User writes first` | The bot sends no opener. When the partner writes, it replies once through the same engines — EVAChat uses its own user-writes-first rules (`m21` → `age_gender.txt`, `from?` → `country.txt`, otherwise `greeting.txt`); a sequence starts from its **first** line, so no line is lost. |
| `Random` | 50/50 per new chat: some chats get the bot opener, others wait for the partner. |

Safety rules for all three options: the first snapshot of a conversation is
history. In `User writes first` / `Random` only the **newest** partner message is
answered, and only while no own row follows it — so a reload, a repeated
hydration or an already answered message can never cause a second reply. Unknown
authors, composer readiness, unconfirmed-send pausing, Skip/START and the Home
transcript behave exactly as before. If nothing is configured or the setting
cannot be read, the original bot-writes-first behaviour applies.

The choice is visible in the log. Saving the Setting logs the normalized value
(`General settings saved. Poll=… Action=… First=bot|user|random`) and every chat
decides once: `CHAT STARTED … first=bot|user`. In `User writes first` the runner
adds exactly one line per state change so `logs.txt` alone explains what
happened:

| Log line | Meaning |
|---|---|
| `WAITING FIRST SMS \| user writes first; no opener sent` | The partner has not written at all; nothing was sent. |
| `WAITING NEXT SMS \| user wrote first earlier; only a new partner message is answered` | The partner already wrote and the newest message is already answered; older history is never re-answered. |
| `FIRST SMS ANSWERED \| user wrote first; reply confirmed on screen` | Logged once per chat, only when the reply to the first partner message was confirmed by a new own row. |

## Detection and identity

The authorized October 14 capture contains semantic attributes; the old
clock/first-text-line heuristic missed grouped messages and invisible timestamps.
The production helper is embedded from `internal/browser/showmode_dom.js`.

- Exact viewport/history/message selectors: `[data-message-viewport="true"]`,
  `ol[aria-label="Message history"]`, `li[data-message-id]`.
- Text is read from row-local `p[data-from]`, including multiline text. `data-from`
  equals the message ID in this capture, **not the sender ID**.
- Author comes from the row heading/profile control; consecutive heading-less
  rows inherit the preceding known author in that rendered conversation. A
  leading group with no evidence stays UNKNOWN, not guessed from message text.
- Partner comes from the conversation header/match-intro profile control. Own
  name comes from the account control's `Open profile for` / avatar label.
- A document-root MutationObserver catches child nodes, text hydration and cycle
  state changes; each Go poll also rescans as a watchdog. Events are retained by
  stable message ID, including rows leaving the DOM between polls. No 40-row cap.
- Initial attachment seeds existing rows as history. Later partner rows enter a
  serial Go queue; own rows are ignored by identity, not identical-text guesses.
  IDs already handled are not replayed on edits/rerenders.
- Read-only context-destroyed evaluations retry briefly on the same Rod page after
  reload; a new document gets a new conversation identity. No browser is relaunched.

## Send and lifecycle safeguards

- Only `[data-chat-composer="true"] textarea[aria-label="Send a message"]` is
  used, never the hidden sizing textarea or a random/search input.
- Rod `Element.Input` + `Element.Type(input.Enter)` use native browser input.
  No chat fetch, synthetic keyboard dispatch or form submission workaround.
- Confirmation requires a **new ID + own author + exact text + same generation**;
  an old identical row or partner's identical text cannot falsely confirm a send.
- Unconfirmed/failed send pauses that conversation, logs an ACTIVITY diagnostic,
  and never auto-resends or silently skips. Inspect the page, then manually start
  a new conversation to resume; this avoids duplicate delivery under uncertainty.
- Cycle controls use exact active `aria-label`, disabled and busy states. Hidden
  START/STOP/SKIP/CONFIRM text inside the same button is ignored. Confirmation
  clicks are handled serially; returning from a click is not proof of a new chat.
- New document/match/partner generation discards stale queues/responders. A
  same-name rematch can be recognized from a replaced intro with disjoint rows.
  Rechecks before replies and each extra line prevent stale sends/skip actions.
- A bounded 2,000-row queue fails closed with a visible diagnostic rather than
  silently dropping unread messages. Unknown/missing/conflicting identities also
  fail closed. Duplicate display names cannot safely be resolved by text alone.

Replies use the existing Single/Folder/EVA engines; Folder picks one sequence per
conversation. Only the Show Mode adapter gained local username substitution and
EVA `ReplyWithOptions` no-username gating. EVA age/refusal rules remain in the
unchanged core engine. Worker counters/persistence and AI compose are not added
here. No AI-lab integration or alternate transport is part of this fix.

## Diagnostics and manual acceptance

Home shows ACTIVITY events including `DOM READY`, `CHAT STARTED`, `INCOMING`,
`SEND CONFIRMED`, `AUTHOR UNKNOWN`, `SEND UNCONFIRMED`, `SKIP` and `PAGE PROBE`.
New diagnostics contain counts/state, not message bodies, tokens or credentials.
A probe reports viewport/composer/action/unknown-author/observer status.

On Windows verify: opener → partner reply → bot reply → two consecutive partner
messages → identical text repeated → own echo ignored → Skip/confirm → new
partner/new opener → manual partner-left → reload → browser close. Separately
check ordinary Rod/ChromeDP Run and Start Bot remain unchanged. A greeting alone
is NOT acceptance. If layout changes, collect only sanitized affected DOM and
state diagnostics; do not upload profiles, cookies or raw capture sessions.

Tests: `npm run test:showmode`, `npm test`; Windows `go test .`,
`go test ./internal/browser`, and the normal `verify.bat` gate. All use synthetic
fixtures/fake backends by default, never a real login. Real Windows/site behavior
has **NOT_RUN** status in this delivery; full project verification is **BLOCKED**.
