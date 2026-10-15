# ChitChat Bot — Python Show Mode (Phase 1)

The same bot behaviour as the Go app's **Show Mode**, rewritten in Python:
Playwright drives your installed **Chrome or Edge**, the shared in-page DOM
observer script is reused **byte-for-byte**, and the runner keeps the same
rules and the same log strings as `root-project/live_chat.go`.

Nothing in `root-project/` is imported or changed at runtime; this folder is
independent. `chitchatport/assets/showmode_dom.js` is a verbatim copy for
fidelity (its sha256 is checked against the Go tree). `chitchatport/ui/styles.css`
is the old Go stylesheet, kept only as a reference copy - the dashboard now has
its own **hacker-dark** theme (`ui/theme.css`). The Go frontend files
(`index.html`, `app.js`, `styles.css`) are also kept verbatim in
`root-project/reference-ui/`, so the identity of `ui/styles.css` can always be
checked even when the Go build output (`cmd/chitchatui/frontend/dist`) is not
part of the folder.

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
* If `ui/flow.js` is ever missing or fails to start, the dashboard **falls back
  to the full console** with a banner saying so - a blank page is never the
  failure mode (this is checked by a test, and by a headless page run).
* The steps are decided **in Python** (`/api/state` → `flow`, see
  `service.flow_state`); the browser only animates. `run.bat` needs no extra
  flag - `?flow=demo` on the URL is a scripted preview for screenshots.

#### Message sources (folder, path or API - nothing to type)

The MESSAGE tab edits the whole message setup and has three ways in:

* **BROWSE FOLDER…** opens your browser's own folder picker (the same idea as
  the Go app's *Select Folder with Chat Patterns*) and loads every `.txt` file
  in the folder you chose - top level only, empty files skipped, exactly the Go
  rule. Your browser reads the files, so no path is ever typed and the dashboard
  also works when it is opened from another machine.
* **Folder path on this machine** - one text box plus **LOAD**, for when the
  dashboard runs on the same PC as the patterns; it reads the folder on the
  server side. Every loaded `.txt` shows up as a chip with its message count
  (`#1 · 2 line(s) · hi there`), and **CLEAR** empties the folder again so the
  single sequence is used.
* **API** - the same loaders as JSON, with the `X-API-Token` header (the
  `copy` button in the panel puts the run token on the clipboard):

  ```json
  POST /api/folder        {"path": "C:\\patterns"}
  POST /api/folder/files  {"files": [{"name": "a.txt", "text": "hi there"}]}
  POST /api/folder/clear  {}
  ```

  They answer `{"ok": true, "count": N}` or `{"ok": false, "error": "no valid
  .txt files found in the selected folder"}`. A request body over 1 MB is
  refused, so a very large folder should go through the path route.

**Chat mode** picks what a chat sends: *Single sequence* uses the multi-line box
on the tab (`sequence`), *Folder* uses one loaded `.txt` per chat - the dropdown,
the sequence box and the usernames box all go through `POST /api/settings/message`
and the answer is the new `message_state` (line counts, loaded files). Switching
to Folder without a loaded folder is refused with a toast instead of starting a
run that could not send anything.

A sequence line that uses `%username%` or `{snap}` needs the **Usernames** box
(one per line). This is the Go rule, not a new one - without a username both apps
stop with `REPLY ENGINE FAILED | Show Mode sequence needs a Message → Usernames
entry`. The MESSAGE tab says so the moment the folder is loaded, and the guided
flow will not move past the MESSAGE step until at least one username is saved.

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

### Dashboard tour (Console 2.0)

Seven tabs, one job each. Everything an operator needs to *decide* something -
which browser is where, which proxy line is broken, what the next step is - is on
the first tab or in the status strip under the tabs; the other tabs are the
tools.

| Part | What it does |
|---|---|
| Guided flow | blank home, the step rail (MESSAGE → PROXY → SETTING → BROWSER → LIVE) and the console unlock at the first chat message |
| **Overview** | the landing tab: KPI cards (state, browsers, waiting login, sent, incoming, chats, message mode, first message), a **live row per browser** (profile, browser, proxy, state, chat with partner + phase, sent/incoming, last activity) with a per-row **CLOSE**, the proxy rotation each fresh browser will get, the single **next action**, and **▶ RUN / ■ STOP ALL / CHECK SYSTEM** |
| Status strip | under the tabs, always visible: STATE, WORKERS active/total, SENT, INCOMING, **LINES** (lines sent / lines the partner wrote - a 3-line message is 3 lines, not 1), PROXY, REPLY DELAY and a **NEXT ▸** button that jumps straight to the tab that needs you |
| Header | theme (◐), help (?), matrix rain (◉), guided setup again (⟲), worker counter, sent counter, uptime, port, START/STOP, the run pill |
| **Browser** | profile, channel, how many browsers, **RUN / STOP / SAVE**, and the System block (python, platform, data dir, log file, dashboard URL). Show Mode always opens a **visible** window: the Go-only `headless` flag is neither shown nor changed |
| **Proxy** | enable, one line per proxy, the default protocol for plain lines, **live validation while you type** ("2 valid, 1 with a problem: line 3 (proxy port must be a number 1-65535)"), **TEST** (up to 20 lines, 4 at a time) with per-line PASS / REACHABLE / FAIL, **COPY RESULTS** and **REMOVE FAILED** (asks first) |
| **Settings** | only options this port really reads: who writes first (BOT / USER / RANDOM), **Reply delay**, **Skip if no reply** (min-max ranges in seconds, the Go `reply_delay_min_seconds`/`reply_delay_max_seconds` field names and unit, `0` = no wait / off), the **page scan interval** (ms) and the optional **opening-burst** switch. A skip limit below the reply delay is flagged in the panel and as a WARN in the log. The Go worker-engine timings stay in `data/state/general.json` untouched but are not shown, because Show Mode never reads them |
| **Message** | chat mode dropdown, the single sequence box (one line per message), the folder picker, the folder-path box, the loaded-file chips with **CLEAR**, usernames, SAVE |
| **Chat** | live read-only transcript, one session per partner, with search, auto-scroll, **COPY** and **EXPORT .txt** |
| **Logs** | level counters (click ACTIVITY/INFO/WARN/ERROR to filter), search, follow, **COPY**, **DOWNLOAD .log** (the raw `data/logs/port.log`), **DEBUG** bundle, **REFRESH**, **CLEAR** (asks first) |
| Look | matrix-rain glow theme plus a **PLAIN** toggle - remembered per browser, and a system that asks for reduced motion starts plain. Every destructive button asks in an in-app dialog that explains the consequence (never a native popup), focus rings are visible, and errors always show up as a toast instead of failing silently |

Keyboard: `1`…`7` = Overview, Browser, Proxy, Settings, Message, Chat, Logs;
`r` RUN (says "press START first" when the Browser step is closed), `s` STOP
(asks first), `t` switch the theme, `x` run the guided setup again, `?` this
help; while the guided flow is on screen: `h` hold the automatic slide, `n` next
step, `b` back, `Esc` skip to the console.

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
| **Reply delay min-max** and **Skip if no reply min-max** (seconds; a fresh random value inside the range per reply / per conversation, like Go's `sleepRandom`; skip `0` = off): a silent partner is left behind and the next match is started | done |
| START back-off (each START that lands no chat doubles the wait up to 60 s, run-wide) + `DOM WAIT` diagnostics (phase / can-send / label / composer / own / partner / rows) + `PAUSED` + line counters | done |
| User-writes-first option: answer the partner's opening burst (newest up to 3 unread rows), off by default | done |
| Browser dashboard (local HTTP + JSON API, token-protected) with hacker-dark theme | done |
| Console 2.0: Overview tab with a live row per browser (state, chat, counts, last activity) + per-browser CLOSE, status strip with the one next action, in-app confirm dialogs, PLAIN theme | done |
| Preflight **CHECK SYSTEM** (same checks as `python app.py --diag`, PASS/FAIL/WARN/BLOCKED) from the Overview tab, plus the `timings` / `page-scan` / `message-mode` warnings that name a stuck-run misconfiguration | done |
| Silent-bot detector: the saved-settings combinations that make a run send nothing (empty sequence, a chat mode Phase 1 cannot run, `%username%` without usernames), each with the reason, what it does to a run and a one-click FIX that saves through the normal routes and is logged | done |
| Debug bundle that is rich and safe at once: `effective` runner settings, run state (bot/user lines, paused workers, chats, next action), whitelisted worker rows, chat stats/sessions, the CHECK SYSTEM list and a `privacy` note; page **route** (path only, never query or fragment) in every stuck-state log line | done |
| Proxy tab: live per-line validation, COPY RESULTS, REMOVE FAILED | done |
| Message tab: mode dropdown, single sequence box, folder path, loaded-file list + CLEAR | done |
| Read-only Chat transcript (search, copy, export) + Logs tab (`data/logs/port.log` viewer with level counters, copy, raw download, clear) | done |
| EVAChat Flow responder, AI compose, license gate, worker bot (match/DM) | **Phase 2** (planned) |

## Proxy

The Proxy tab works exactly like the Go dashboard's:

* tick **Enable proxy**, paste one proxy per line, pick the protocol for plain
  lines (HTTP or SOCKS5), then **Save Proxy**;
* the box validates as you type (nothing is saved or logged): each bad line is
  named with its number and the reason, so a typo does not need a round trip to
  the provider;
* **Test Proxy** checks up to 20 lines, four at a time, without saving anything
  and without logging in - each line gets PASS, REACHABLE / SITE RESTRICTED or
  FAIL plus the provider's own words and one next step;
* **COPY RESULTS** puts the whole result table on the clipboard (for a report)
  and **REMOVE FAILED** deletes every FAIL line from the box after a confirm,
  so the list can be cleaned before it is saved.

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
and UI wiring, the guided-flow stages and routes, the dashboard API of every
Console 2.0 tab, plus the proxy parser, relay and diagnostics), serves the
dashboard on a throwaway port and walks
the flow (blank home → rail → first chat row → unlock), checks that the DOM
script still matches the Go asset and that `chitchatport/ui/styles.css` is still
byte-identical to the Go frontend stylesheet (from the Go `dist/` folder when it
is there, otherwise from `root-project/reference-ui/`). The dashboard tests start
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

## Start / Stop order

`MESSAGE -> PROXY -> SETTING -> ▶ START -> BROWSER (RUN)`. The Browser step
(and its RUN button) does not exist until START is pressed, and START stays
disabled until the three setup steps are done; the server refuses RUN the
same way (`Finish these first: ...`), so an old tab or an API call cannot
bypass it. **■ STOP** stops the bot and closes the Browser step again.
Breaking a setup step later (e.g. a `%username%` folder with no usernames)
closes the gate automatically.

## Moving between steps

Every step on the rail is clickable at any time, in both directions, and
**◂ BACK / NEXT ▸** walk them in order (keys `b` / `n`). Going to a step by
hand parks the automatic slide, so the wizard never pulls you away from the
option you opened; it resumes when you finish that step or press NEXT.
**SKIP ALL** (or `Esc`) opens the full console at once.

## When something goes wrong (debug)

0. **The browser closed by itself?** Read the log line
   `Browser closed - reason: ...` - every close now says why. A login redirect
   or a new sign-in tab no longer counts as a crash: the port waits through
   navigations ("Browser is busy ... checking again") and follows the open tab.

1. **CHECK SYSTEM** on the Overview tab runs the same preflight as
   `python app.py --diag` (Python, Playwright, Chrome/Edge, data folder, log
   file, crash reports, DOM asset). Every line is PASS / FAIL / WARN /
   BLOCKED - BLOCKED means "could not check", never a green light - and
   **COPY REPORT** puts the whole list on the clipboard. The settings that make
   a run look stuck are checked *before* you press RUN, and the same list is in
   the **Settings check** panel right below the button, each row with its own
   one-click **FIX** (and **FIX ALL** when several can be repaired at once):
   `risk-empty-sequence` (nothing to send: the bot opens no chat and answers
   nothing - a Folder whose files are blank counts as empty), `risk-eva-mode`
   (a chat mode Phase 1 cannot run: the runner pauses with `REPLY ENGINE
   FAILED`), `risk-usernames-missing` (`%username%` / `{snap}` with an empty
   Usernames list - the only risk without an automatic fix, because only you
   know the handle), `risk-message-mode` (Folder mode with no loaded file),
   `risk-timings` (skip-if-no-reply shorter than the reply delay leaves every
   chat before its own reply is due), `risk-page-scan` (a page scan slower than
   2 s), `risk-skip-off` and `risk-one-line-sequence` (notes, not mistakes).
   A **BLOCK** row refuses RUN - on the server (`start_show_mode`) and in the
   button itself - because that run would send nothing. Each FIX saves through
   the normal settings routes (nothing else is touched, the change is logged as
   `SETTINGS FIXED | <code>: <what changed>` in seconds), and every PASS prints
   the values it judged instead of a bare "ok".
2. **Logs tab -> DOWNLOAD .log** gives you the raw file
   `data/logs/port.log`, not just the lines the tab is showing (useful when the
   buffer has rotated), and the level chips show how many lines of each level
   the current run produced.
3. **Logs tab -> DEBUG** downloads `chitchat-debug-<time>.json`: version, OS,
   settings (proxy passwords masked, no API token, no usernames), the same
   CHECK SYSTEM list, **what the runner was actually told to do**
   (`effective`: chat mode, sequence count, usernames saved/needed, first
   message mode + burst, reply delay, skip-if-no-reply, page scan interval, all
   in seconds), the live run state (bot/user line counts, paused workers, chats,
   the single next action), **the settings risks that would silence a run**, one
   whitelisted row per worker (state, activity + note, paused, counts, chat) and
   the chat stats + sessions, crash file names and the last 300 log lines. Send that file when asking for help - it is safe
   to share, and a `privacy` field says exactly what is and is not inside.
   While a run is stuck, the `DOM WAIT` / `SEND PAUSED` / `START WAIT` log lines
   now end with `probe[phase=... can_send=... route=/...]`, so one line shows
   both the reason and what the page looked like when it happened.
4. Any unexpected error writes the full traceback to `data/logs/crash-<time>.txt`
   and the red log line names the file.
5. `run.bat --debug` echoes every log line into the console window as well
   (useful when the dashboard tab is closed). `run.bat --diag` prints the
   environment check (Python, Playwright, Chrome/Edge, data folder, log file,
   crash files, DOM asset) and exits.

## How to test it like the Go app

1. Start the app, open **Setting**, choose *First message (Show Mode)* and save.
2. Open **Browser** → **Run Show Mode** → a fresh Chrome/Edge window opens
   (separate from the dashboard tab).
3. Log in **inside that window** (the log shows `Waiting for login token…`, then
   `Login saved to …`).
4. Watch the **Chat** tab and the log lines; compare them with the Go app:
   `CHAT STARTED … first=bot|user`, `WAITING FIRST SMS`, `WAITING NEXT SMS`,
   `FIRST SMS ANSWERED`, `INCOMING`, `REPLY DELAY | selected=… setting=…-…s`
   (the same line Go writes for every accepted row), `SEND CONFIRMED`, `SKIP …`.
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
  test_dashboard_api.py       the Console 2.0 routes: overview/workers, CHECK SYSTEM,
                              proxy validation, folder path/clear, log download, CLOSE
tools/verify.py               local verification gate
```

## Differences worth knowing

* Storage is JSON under `data/state/` instead of Badger; writes are atomic.
* `data/state/general.json` keeps the **Go field names and units** for every
  timing the Go dashboard shows (seconds), including
  `reply_delay_min_seconds`/`reply_delay_max_seconds`, with Go's normalization
  (`uistore.LoadGeneralSettings`): a non-positive poll/action/skip-timeout
  minimum falls back to the Go default and every maximum is lifted to its
  minimum. A file written by an earlier build of this port
  (`reply_delay_ms`/`reply_delay_max_ms`) is migrated once and the old keys are
  dropped. Only the reply delay's **default** differs: this port starts at
  5-10 s instead of Go's `0`-`0`, so a fresh install does not answer instantly.
* The Overview tab is the landing tab: it answers "is it running, where is every
  browser, what next" in one screen. The Go dashboard has no such screen - the
  numbers come from the same run state the log lines are written from, and the
  next action is derived in Python (`service.overview().next_action`), never
  guessed in JavaScript.
* The four Go worker-engine timing pairs (`poll_*`, `action_*`,
  `match_reply_timeout_*`, `match_close_wait_*`, seconds) are **not on the
  dashboard**: Show Mode never reads them, so offering them would only look like
  behaviour. They stay in `data/state/general.json` under their exact Go names
  (untouched by a save from the dashboard, which merges only the fields it owns),
  so the file stays readable by the Go desktop app and the Phase 2 worker.
  `poll_interval_ms` - the one backend timing the runner really uses - is shown
  on the Settings page as *page scan interval*.
* Lines, not bubbles: `SENT` / `LINES ✉` count the messages the bot sent and the
  partner lines the runner read (a 3-line message is 3 lines), while `INCOMING`
  keeps counting bubbles. The Chat tab shows the same numbers per session
  (`USER 9 lines / 3 bubbles · BOT 5 lines / 4 bubbles`) and marks each
  multi-line bubble with its line count, so "the bot only answered once" can be
  told apart from "the partner wrote five lines in one bubble".
* A runner that stops safely (unconfirmed send, missing composer/identity, engine
  failure) publishes `PAUSED` plus its reason on the Overview row, and the log
  line carries the reason (including the page facts at a `DOM WAIT` and why a
  send was refused) instead of a bare `RUNNING` with frozen counters.
* **STOP** follows the real code path: each browser finishes its current step
  (never mid-send), the window is closed and the port logs that it is closing,
  instead of the old "browser left open" wording. **CLOSE** on a single Overview
  row does the same for that one browser only.
* Go's `model.DefaultMessageSettings()` sequence (`hello how are you…`) is **not**
  injected into a fresh data folder: the guided MESSAGE step is finished by a
  loaded patterns folder or a saved sequence, and a pre-filled default would
  fake that. A v1 file that stored `first_message`/`reply_message` is still
  migrated into `sequence`, like `uistore.LoadMessageSettings`.
* The transcript feed ports the core rules of `home_chat.go` (sessions, roles,
  dedup, bounds); a few display-only edge cases are simplified and documented in
  the module docstring.
* EVAChat Flow is intentionally **not** ported yet: choosing it pauses with
  `REPLY ENGINE FAILED | EVAChat Flow is not ported yet…` instead of guessing.
* `backend` in the saved login JSON is `"playwright"` (the Go app writes
  `"rod"`/`"chromedp"`); everything else in the file matches.
