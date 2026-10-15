# ChitChat Bot — Python Show Mode (Phase 1)

The same bot behaviour as the Go app's **Show Mode**, rewritten in Python:
Playwright drives your installed **Chrome or Edge**, the shared in-page DOM
observer script is reused **byte-for-byte**, and the runner keeps the same
rules and the same log strings as `root-project/live_chat.go`.

Nothing in `root-project/` is imported or changed at runtime; this folder is
independent. `chitchatport/assets/showmode_dom.js` is a verbatim copy for
fidelity (its sha256 is checked against the Go tree). `chitchatport/ui/styles.css`
is the old Go stylesheet, kept only as a reference copy - the dashboard now has
its own **hacker-dark** theme (`ui/theme.css`).

### The dashboard (this is the "other method")

The first attempts drew the UI inside a native WebView window, and on some
Windows setups that window stayed **black**. So the dashboard no longer depends
on a WebView at all:

* `app.py` starts a small **local HTTP server** (`chitchatport/server.py`,
  Python standard library only) bound to `127.0.0.1`,
* it serves a static UI (`chitchatport/ui/`) and a JSON API,
* your **own browser** opens it - Chrome, Edge, Firefox, anything you already use.

No window to go black, no WebView runtime to install, and the same code path
also works inside the optional pywebview window (`run-window.bat`), because that
window simply points at the same URL. Every `/api/*` call needs a one-run token
that is injected into the served page, and static files can only be served from
`ui/`, so nothing else on the machine can drive the bot.

## Install and run

**Windows, one click (recommended):**

```bat
install.bat      :: finds Python, creates .venv, installs the requirements, runs the checks
run.bat          :: starts the dashboard and opens it in your browser  (recommended)
run-window.bat   :: same dashboard in a native window (needs: pip install pywebview)
```

`install.bat` is safe to run again at any time: it reuses an existing `.venv`,
skips the download when the packages are already importable, and falls back to
the system Python when a virtual environment cannot be created. `run.bat`
starts `install.bat` by itself when a dependency is missing.

**Manual (any OS):**

```bat
python -m pip install -r requirements.txt
python app.py
```

No `playwright install` step is needed: the system browser is used
(`channel="chrome"`, falling back to `msedge`). Pick a channel in the Browser
tab if you want to pin one.

`run.bat` prints the dashboard URL and opens your browser at it. Keep its
console window open while the bot runs; `Ctrl+C` there (or the Stop button)
stops the bot. If you ever want a fixed port, use
`python app.py --port 8300`, and `python app.py --diag` prints which
browsers Playwright can start on this machine.

### Dashboard tour

| Part | What it does |
|---|---|
| Header | connection pill, run status + uptime, Stop/Start, 1-5 / r / s shortcuts |
| Left column | **Setting** (first message, timings), **Message** (single/folder, sequence), **Browser** (profile, channel, headless is ignored) |
| Right column | **Chat** (live read-only transcript, one session per partner) and **Logs** (filter by ACTIVITY/INFO/WARN/ERROR, search, follow, copy, clear) |
| Look | matrix-rain canvas background, glass cards with cut corners, level-coloured log lines, toasts. `motion: on/off` freezes the rain on low-end machines |

Keyboard: `1` Setting, `2` Message, `3` Browser, `4` Chat, `5` Logs, `r` refresh,
`s` start/stop.

## What works in Phase 1

| Area | Status |
|---|---|
| Visible browser + manual login, `login_info_*.json` written with the **same schema** as the Go app | done |
| Same DOM observer script (rows, authors, phase, buttons, overflow, probe) | done |
| First message: `bot` / `user` / `random`, read once per conversation | done |
| User-first: waits, answers only the newest partner row, never twice, sequence starts at line 1 | done |
| Single / Folder sequences (one file per conversation, `%username%` / `{snap}`) | done |
| Skip/START rotation, send confirmation by a new own row, no automatic resend | done |
| Browser dashboard (local HTTP + JSON API, token-protected) with hacker-dark theme | done |
| Read-only Chat transcript + Logs tab (`data/logs/port.log`) | done |
| EVAChat Flow responder, AI compose, proxy rotation, license gate, worker bot (match/DM) | **Phase 2** (planned) |

## Verify

```bat
python tools\verify.py
```

It byte-compiles everything, imports the engine without a GUI, runs the unittest
suite (runner, settings, DOM asset, browser helpers, transcript, dashboard server
and UI wiring - 67 tests) and checks that the DOM script still matches the Go
asset. The dashboard tests start the real HTTP server on a free port, so the API
and the token check are verified without opening a browser. `BLOCKED` means "could not check"
(e.g. the Go tree is not next to this folder) and is never reported as PASS.

Run the tests alone with:

```bat
python -m unittest discover -s tests -t .
```

## How to test it like the Go app

1. Start the app, open **Setting**, choose *First message (Show Mode)* and save.
2. Open **Browser** → **Run Show Mode** → a fresh Chrome/Edge window opens
   (separate from the dashboard tab).
3. Log in **inside that window** (the log shows `Waiting for login token…`, then
   `Login saved to …`).
4. Watch the **Chat** tab and the log lines; compare them with the Go app:
   `CHAT STARTED … first=bot|user`, `WAITING FIRST SMS`, `WAITING NEXT SMS`,
   `FIRST SMS ANSWERED`, `INCOMING`, `SEND CONFIRMED`, `SKIP …`.
5. With `user` or `random` chosen, keep the partner silent and confirm nothing
   is sent; then write something and confirm exactly one reply.

## Layout

```
app.py                        entry point: starts the dashboard server, opens the browser
                              (--window = pywebview window, --diag = environment check)
chitchatport/
  browser.py                  Playwright backend (system Chrome/Edge), login capture
  domchat.py                  DOM types + send/login confirmation helpers
  domscript.py                wrappers around the shared DOM script
  dom_asset → assets/showmode_dom.js   (verbatim copy of the Go asset)
  homefeed.py                 read-only transcript feed
  logging_util.py             ACTIVITY/INFO/WARN/ERROR log + port.log
  responders.py               sequence responder (begin() seam kept)
  runner.py                   the liveRunner port (first-message modes, queue, skip)
  server.py                   local HTTP dashboard server (stdlib, token-protected)
  service.py                  settings + run control + login wait + wiring
  settings.py                 JSON settings with the Go defaults/normalization
  ui/                         dashboard: index.html + theme.css (hacker dark) + app.js
                              styles.css is the old Go stylesheet, kept as reference only
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
