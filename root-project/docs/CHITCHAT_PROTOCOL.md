# ChitChat protocol — distilled from real capture (2026-10-14)

> **Status note (2026-10-14, later same day):** this document stays as
> protocol evidence (the Start-Bot worker speaks this protocol), but
> **Show Mode live chat no longer uses it** — the user explicitly chose a
> DOM-only implementation (read rows, type replies, click buttons; see
> `docs/SHOW_MODE.md`). Do not "fix" Show Mode by returning to the
> WebSocket/API route.

This file is the permanent in-project knowledge base for how chitchat.gg
actually talks to its servers. It was distilled from a manual browser capture
(capture tool session `20261014-185017_chitchat.gg`, tool v1.2.0, Chrome,
redaction ON, ~7 minutes: login → match → chat). Raw capture lives outside
this repo (user-shared `wbsccapture` repository); **this document + the JSON
summary are the source of truth so the raw capture never has to be re-read.**
Schema fingerprints of that baseline session:

- apiEndpoints: `161fdb4db69053f9a72375eb8be92a00e564071fabfd2f82044673b38d366ed0`
- websocketEvents: `c0b5d54b716aee4e853694b60aefada4df74ac43268b70fcb440624dd7b9baad`

When the site changes: re-capture with `capture/run.bat`, run
`capture/compare.bat <baseline> <new>` and update only the changed rows here.

## Surfaces

| Surface | Base |
|---|---|
| Web app (SPA) | `https://app.chitchat.gg` (entry via `https://www.chitchat.gg/`) |
| REST API | `https://api.chitchat.gg` |
| Realtime | `wss://api.chitchat.gg/socket.io/?EIO=4&transport=websocket` (Socket.IO, EIO=4) |

Page flow observed: `www.chitchat.gg/` → `app.chitchat.gg/start/new` →
`app.chitchat.gg/text` (the chat page). The app is a client-rendered SPA;
server HTML snapshots are just the shell, so DOM selectors are the *least*
reliable automation surface (the UI uses Tailwind utility classes, no
semantic message classes — the capture DOM observer found 0 message nodes).

## Auth / session

- Login: `POST /auth/login` JSON `{email, password, token}` where `token` is
  the captcha token → `201 {restored: bool}`. Captcha is why login stays
  manual in Show Mode.
- Session is **cookie-based**: cookies `token` and `__Secure-text-session`
  on `.chitchat.gg`. API calls ride on those cookies (page `fetch` uses the
  browser's own credentials).
- localStorage keys seen: `cachedUserId`, `loginDate`, `settings-storage`,
  `dark-mode` (+ analytics). Own user id: `GET /users/me` → `{id (24 hex),
  username, avatar, badges, createdAt, preferences, interests, email, gender,
  verified, hasPassword, ageVerified, matchPreferences}`.
- Session checks the existing app already uses align: `GET /users/me`,
  `GET /users/me/relationships`, `GET /auth/session-status`.

## Chat lifecycle (observed sequence)

1. Open `/text`, Socket.IO connects (client sends connect packet
   `40{"release":"<build-hash>"}` — the release hash changes per deploy;
   `internal/bot/realtime.go` already extracts it from site JS).
2. `POST /match` `{withInterests: false}` → `201 {matched: true}`
   (a `403 {error:"Forbidden", message:"Flagged"}` was also observed once).
3. `GET /match/active` → `{match: {conversation: {id (24 hex), participants:
   [{profile: {id, username, ...}}, ...]}}}` — this gives the current
   **conversationId** and the partner profile.
4. Incoming messages arrive as WebSocket `chatMessage` events (below).
   History/polling: `GET /users/me/conversations/:id/messages?limit&offset`.
5. **Send**: `POST /users/me/conversations/:id/messages` as
   `multipart/form-data` with fields `content` (text) and `nonce` (UUID) →
   `201` with the stored message object. (The browser never sends chat text
   over the socket; sent socket frames were only presence/pings.)
6. Typing out: `POST /users/me/conversations/:id/typing`. Read receipts:
   `PATCH /users/me/conversations/:id/read`.
7. **Skip / next partner**: `PATCH /match/disconnect` then `POST /match`
   again, then `GET /match/active` for the new conversation.

## WebSocket events (Socket.IO)

Client → server observed: connect packet `40{...}`, engine ping/pong
(`2`/`3`), `presenceSync` (periodic, no payload). Server → client observed:

| Event | Payload | Meaning |
|---|---|---|
| `chatMessage` | `{message: Message}` | **Incoming chat message** (also the reliable incoming signal) |
| `typing` | `{userId, conversationId}` | Partner is typing |
| `messageEdited` | `{message: Message}` | A message was edited |
| `onlineFriends` | (list) | Friend presence |
| `presenceSync` | — | Presence sync |

`matchUpdate` is handled by existing `internal/bot/realtime.go` (code-known,
not seen in this capture window). Message object schema:

```json
{
  "id": "24hex", "conversationId": "24hex", "author": "<sender user id>",
  "content": "text", "type": "TEXT", "attachments": [],
  "createdAt": "ISO-8601", "status": "SENT", "flags": 0, "reactions": []
}
```

Own vs partner: compare `message.author` with the own id from `GET
/users/me`. (The baseline capture cannot prove values: the capture redactor
hid `author` because its name matches /auth/. That redactor quirk is fixed in
capture tool v1.2.1; structure is unaffected.)

## Evidence map (which capture file proved what)

- Send = HTTP multipart POST: `http/` record for
  `POST /users/me/conversations/:id/messages` (201) + `api-catalog.json`.
- Incoming = WS `chatMessage`: `websockets/*.jsonl`, `websockets/catalog.json`
  (22 received in ~7 min).
- Skip = `PATCH /match/disconnect` + `POST /match`: `hooks.jsonl` fetch log.
- Session/cookies/storage keys: `storage/*.json` snapshots.
- **Noise warning:** `messages/incoming-candidates.jsonl` also contains
  non-chat API texts (moderation strings etc.). Only WS `chatMessage` is a
  trustworthy incoming signal. No user markers were pressed in this session
  and no outgoing *socket* message exists (sending is HTTP), so
  `outgoingCandidates = 0` is expected, not a failure.

## Show Mode live-chat design (evidence-based, pending approval)

Browser stays open after manual login/captcha; automation runs *inside that
browser*, so no DOM selectors are needed:

- **Incoming:** Rod/CDP `Network.webSocketFrameReceived` → parse Socket.IO
  `42["chatMessage", {message}]`; ignore `author == ownId`.
- **Reply text:** existing chat engine (EVAChat/sequences), unchanged.
- **Send:** page-context `fetch` POST multipart `{content, nonce}` to
  `/users/me/conversations/:id/messages` with browser credentials → expect 201.
- **Skip:** page `fetch` `PATCH /match/disconnect` → `POST /match` →
  `GET /match/active` for the new conversationId. Optional: `PATCH .../read`,
  `POST .../typing` for natural behaviour.
- **Stop:** user closes the browser window (or dashboard stop); loop ends.
- File scope: `internal/browser/rod.go` (+ small live-chat helper),
  `app.go` Show-Mode branch only, `internal/model/ui.go` text,
  dashboard status text, new browser-free tests with a fake backend.
  ChromeDP, EVA engine core, license, proxy, DB: untouched.
