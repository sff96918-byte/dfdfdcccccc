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

#### The guided flow (blank home → rail → first message)

The home page opens **blank** - only a faint `waiting for setup …` line and five
step dots. A **step rail slides in from the right** and walks you through the
setup one step at a time:

```
MESSAGE -> PROXY -> SETTING -> BROWSER -> LIVE
```

Each step shows only its own form (the tab bar and the rest of the console stay
hidden), and the next step slides in **after the previous one is really
finished** - a loaded patterns folder for `MESSAGE`, a proxy list or an explicit
*disabled* save for `PROXY`, a settings save for `SETTING`, a running Show Mode
with a saved login for `BROWSER`. Then, as soon as the **first chat message**
arrives (from the bot *or* the partner), step 5 turns green and the **whole
console unlocks**: all six tabs, the log panel and the chat transcript, exactly
as described below. The rail then shrinks to a thin dock on the edge.

* `HOLD` (or `h`) stops the automatic slide; a green tick pulses ~1.2 s before
  each slide; `NEXT ▸` (or `n`) moves on by hand: `⇄` moves the rail to the left
  side and the choice is remembered; `SKIP → console` and `Esc` unlock
  everything immediately; `⟲ flow` starts the guided steps again.
* The slide **never** runs while you are typing in a step, for two seconds after
  you last touched it, while the pointer rests on the rail, or when Windows asks
  for reduced motion (`prefers-reduced-motion`). This is the WCAG 2.2.2 rule for
  auto-advancing content, and it is tested.
* The steps are decided **in Python** (`/api/state` → `flow`, see
  `service.flow_state`); the browser only animates. `run.bat` needs no extra
  flag - `?flow=demo` on the URL is a scripted preview for screenshots.

#### Message sources (folder or API - nothing to type)

The MESSAGE tab has exactly two ways in:

* **BROWSE FOLDER…** opens your browser's own folder picker (the same idea as
  the Go app's *Select Folder with Chat Patterns*) and loads every `.txt` file
  in the folder you chose - top level only, empty files skipped, exactly the Go
  rule. Your browser reads the files, so no path is ever typed and the dashboard
  also works when it is opened from another machine.
* **API** - the same two loaders as JSON, with the `X-API-Token` header (the
  `copy` button in the panel puts the run token on the clipboard):

  ```json
  POST /api/folder        {"path": "C:\\patterns"}
  POST /api/folder/files  {"files": [{"name": "a.txt", "text": "hi there"}]}
  ```

  Both answer `{"ok": true, "count": N}` or `{"ok": false, "error": "no valid
  .txt files found in the selected folder"}`. A request body over 1 MB is
  refused, so a very large folder should go through the path route. `chat_mode`,
  `sequence` and `use_loaded_sequences` stay scriptable through
  `POST /api/settings/message`; the tab itself only edits usernames.

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
| Guided flow | blank home, the step rail (MESSAGE → PROXY → SETTING → BROWSER → LIVE) and the console unlock at the first chat message |
| Header | connection pill, run status + uptime, Stop/Start, 1-6 / r / s shortcuts |
| Tabs | **Proxy**, **Setting**, **Message**, **Browser**, **Chat**, **Logs** |
| Left column | **Proxy** (enable, list, default protocol, Test), **Setting** (first message, timings), **Message** (browse a patterns folder or load it over the API, usernames), **Browser** (profile, channel, headless is ignored) |
| Right column | **Chat** (live read-only transcript, one session per partner) and **Logs** (filter by ACTIVITY/INFO/WARN/ERROR, search, follow, copy, clear) |
| Look | matrix-rain canvas background, glass cards with cut corners, level-coloured log lines, toasts. `motion: on/off` freezes the rain on low-end machines |

Keyboard: `1` Browser, `2` Proxy, `3` Setting, `4` Message, `5` Chat, `6` Logs,
`r` refresh, `s` start/stop - and while the guided flow is running: `h` hold the
automatic slide, `n` next step, `Esc` skip to the console.

## What works in Phase 1

| Area | Status |
|---|---|
| Visible browser + manual login, `login_info_*.json` written with the **same schema** as the Go app | done |
| Same DOM observer script (rows, authors, phase, buttons, overflow, probe) | done |
| First message: `bot` / `user` / `random`, read once per conversation | done |
| User-first: waits, answers only the newest partner row, never twice, sequence starts at line 1 | done |
| Single / Folder sequences (one file per conversation, `%username%` / `{snap}`) | done |
| Proxy list (HTTP / SOCKS5, `host:port`, `host:port:user:pass`, `http://`, `socks5://`) with per-browser rotation | done |
| Proxy **Test** button: per-line PASS / REACHABLE / FAIL, latency, provider message, next-step hint | done |
| Local relay so credentials never reach the browser flags; the relay never dials directly | done |
| Skip/START rotation, send confirmation by a new own row, no automatic resend | done |
| Browser dashboard (local HTTP + JSON API, token-protected) with hacker-dark theme | done |
| Read-only Chat transcript + Logs tab (`data/logs/port.log`) | done |
| EVAChat Flow responder, AI compose, license gate, worker bot (match/DM) | **Phase 2** (planned) |

## Proxy

The Proxy tab works exactly like the Go dashboard's:

* tick **Enable proxy**, paste one proxy per line, pick the protocol for plain
  lines (HTTP or SOCKS5), then **Save Proxy**;
* **Test Proxy** checks up to 20 lines, four at a time, without saving anything
  and without logging in - each line gets PASS, REACHABLE / SITE RESTRICTED or
  FAIL plus the provider's own words and one next step.

Accepted lines (same grammar as the Go app):

```
1.2.3.4:8080                      host:port            -> HTTP
1.2.3.5:8080:user:pass            host:port:user:pass  -> HTTP
http://user:pass@1.2.3.4:8080     URL form
socks5://user:pass@1.2.3.4:1080   SOCKS5 with auth
socks5://1.2.3.4:1080:user:pass   SOCKS5, provider style
[2001:db8::1]:8080                IPv6 must use brackets
```

Plain lines take the protocol from the dropdown; an explicit `http://` or
`socks5://` prefix always wins. URL credentials must be percent-encoded
(`u%40name` for `u@name`).

**How a browser uses a proxy.** When Show Mode starts with proxies enabled, each
fresh browser gets the next line in rotation. Chrome/Edge is pointed at a small
**local relay** (`127.0.0.1`, one per browser, alive only while that browser is);
the relay adds your username/password on the upstream connection. That is why
credentials never appear in a browser command line or in a process list, and why
a failing proxy can never silently fall back to a direct connection: the relay
only ever dials the configured proxy. The log shows the same lines as the Go app:

```
Fresh browsers will use 3 proxy line(s) in rotation.
[default fresh 1/1] Using proxy http://1.2.3.4:8080
```

`data/state/proxy.json` stores the list (`enabled`, `entries`). A v1 settings
file with a single `address` + `username` + `password` is migrated into the list
on load.

Not ported yet: the worker-bot proxy assignment (`Start Bot` flows, Phase 2).

## Verify

```bat
python tools\verify.py
```

It byte-compiles everything, imports the engine without a GUI, runs the unittest
suite (runner, settings, DOM asset, browser helpers, transcript, dashboard server
and UI wiring, the guided-flow stages and routes, plus the proxy parser, relay
and diagnostics), serves the dashboard on a throwaway port and walks
the flow (blank home → rail → first chat row → unlock), and checks that the DOM
script still matches the Go asset. The dashboard tests start
the real HTTP server on a free port, and the proxy tests run real HTTP and SOCKS5
fake proxies inside the test process, so the API, the token check, credential
injection, remote DNS and the 407/network hints are all verified here without a
browser and without the internet. The proxy test file carries a throwaway
self-signed certificate for `127.0.0.1` (so the TLS path can be tested offline) -
it is not a secret and is not used anywhere outside the tests. `BLOCKED` means "could not check"
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
  browser.py                  Playwright backend (system Chrome/Edge), login capture, proxy relay
  domchat.py                  DOM types + send/login confirmation helpers
  domscript.py                wrappers around the shared DOM script
  dom_asset → assets/showmode_dom.js   (verbatim copy of the Go asset)
  homefeed.py                 read-only transcript feed
  logging_util.py             ACTIVITY/INFO/WARN/ERROR log + port.log
  responders.py               sequence responder (begin() seam kept)
  runner.py                   the liveRunner port (first-message modes, queue, skip)
  proxy.py                    proxy list parsing, rotation, tunnelling, diagnostics
  proxyrelay.py               loopback relay that adds proxy credentials upstream
  server.py                   local HTTP dashboard server (stdlib, token-protected)
  service.py                  settings + run control + login wait + wiring
  settings.py                 JSON settings with the Go defaults/normalization
  ui/                         dashboard: index.html + theme.css (hacker dark) + app.js
                              + flow.js (guided flow: rail, stages, unlock)
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
