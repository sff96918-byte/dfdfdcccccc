# ChitChat Bot — Python Show Mode (Phase 1)

The same bot behaviour as the Go app's **Show Mode**, rewritten in Python:
Playwright drives your installed **Chrome or Edge**, the shared in-page DOM
observer script is reused **byte-for-byte**, and the runner keeps the same
rules and the same log strings as `root-project/live_chat.go`.

Nothing in `root-project/` is imported or changed at runtime; this folder is
independent. Two files are verbatim copies for fidelity:
`chitchatport/assets/showmode_dom.js` and `chitchatport/ui/styles.css`.

## Install and run

```bat
cd python-port
python -m pip install -r requirements.txt
python app.py
```

No `playwright install` step is needed: the system browser is used
(`channel="chrome"`, falling back to `msedge`). Pick a channel in the Browser
tab if you want to pin one.

## What works in Phase 1

| Area | Status |
|---|---|
| Visible browser + manual login, `login_info_*.json` written with the **same schema** as the Go app | done |
| Same DOM observer script (rows, authors, phase, buttons, overflow, probe) | done |
| First message: `bot` / `user` / `random`, read once per conversation | done |
| User-first: waits, answers only the newest partner row, never twice, sequence starts at line 1 | done |
| Single / Folder sequences (one file per conversation, `%username%` / `{snap}`) | done |
| Skip/START rotation, send confirmation by a new own row, no automatic resend | done |
| Read-only Chat transcript + Logs tab (`data/logs/port.log`) | done |
| EVAChat Flow responder, AI compose, proxy rotation, license gate, worker bot (match/DM) | **Phase 2** (planned) |

## Verify

```bat
python tools\verify.py
```

It byte-compiles everything, imports the engine without a GUI, runs the unittest
suite (runner, settings, DOM asset, browser helpers, transcript) and checks that
the DOM script still matches the Go asset. `BLOCKED` means "could not check"
(e.g. the Go tree is not next to this folder) and is never reported as PASS.

Run the tests alone with:

```bat
python -m unittest discover -s tests -t .
```

## How to test it like the Go app

1. Start the app, open **Setting**, choose *First message (Show Mode)* and save.
2. Open **Browser** → **Run Show Mode** → a fresh Chrome/Edge window opens.
3. Log in **inside that window** (the log shows `Waiting for login token…`, then
   `Login saved to …`).
4. Watch the **Chat** tab and the log lines; compare them with the Go app:
   `CHAT STARTED … first=bot|user`, `WAITING FIRST SMS`, `WAITING NEXT SMS`,
   `FIRST SMS ANSWERED`, `INCOMING`, `SEND CONFIRMED`, `SKIP …`.
5. With `user` or `random` chosen, keep the partner silent and confirm nothing
   is sent; then write something and confirm exactly one reply.

## Layout

```
app.py                        pywebview window + JS API
chitchatport/
  browser.py                  Playwright backend (system Chrome/Edge), login capture
  domchat.py                  DOM types + send/login confirmation helpers
  domscript.py                wrappers around the shared DOM script
  dom_asset → assets/showmode_dom.js   (verbatim copy of the Go asset)
  homefeed.py                 read-only transcript feed
  logging_util.py             ACTIVITY/INFO/WARN/ERROR log + port.log
  responders.py               sequence responder (begin() seam kept)
  runner.py                   the liveRunner port (first-message modes, queue, skip)
  service.py                  settings + run control + login wait + wiring
  settings.py                 JSON settings with the Go defaults/normalization
  ui/                         dashboard (styles.css copied from the Go dist)
tests/                        unittest suite (no extra dependencies)
tools/verify.py               local verification gate
```

## Differences worth knowing

* Storage is JSON under `data/state/` instead of Badger; writes are atomic.
* The transcript feed ports the core rules of `home_chat.go` (sessions, roles,
  dedup, bounds); a few display-only edge cases are simplified and documented in
  the module docstring.
* EVAChat Flow is intentionally **not** ported yet: choosing it pauses with
  `REPLY ENGINE FAILED | EVAChat Flow is not ported yet…` instead of guessing.
* `backend` in the saved login JSON is `"playwright"` (the Go app writes
  `"rod"`/`"chromedp"`); everything else in the file matches.
