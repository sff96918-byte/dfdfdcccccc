# EVAChat Flow — Test Report

## Passed

### Go unit tests

```text
go test ./internal/bot ./internal/model ./internal/uistore

ok   chitchatbot/internal/bot
?    chitchatbot/internal/model [no test files]
ok   chitchatbot/internal/uistore
```

Re-verified 2026-10-13 with Go 1.27.2 (Linux sandbox) after porting the EVA
reference chat engine (`eva_flow.py`) 1:1: 42 bot tests (23 EVAChat) + 2
uistore tests pass; `GOOS=windows GOARCH=amd64 go build ./...` and `go vet`
pass.

The EVAChat tests mirror the reference `test_flow.py` behaviour spec:

- TXT pack integrity (every input has an output pool, scan priority, README not a pool)
- `#` comment triggers ignored
- basic funnel: hi → greeting, m21 → age_gender, from? → country, usa → flirty_questions
- first-SMS variants (m21 / where u from / horny?)
- bot-writes-first opener then the same funnel
- horny QUESTION → 2 lines (horny_answer + horny) → ask_snap → yes → share_snap → END
- leet detection (h0rny?, s3x) and the strict ask chain
- MIDDLE categories: how_are_you, your_age, warm_reply, busy_later (2nd → closer), flirty_reply → ask → share, english_only
- direct snap/insta/ghost request → share immediately; user offering own snap → terminal share
- yes-words WITHOUT the bot's ask never share
- refusal → retry_nudge once → second refusal → closer; firm refusal never shares
- repeat loop: flirty_reply fires again with a different line, ask re-fires
- underage (m15) → blocked, never shares, closer
- multi-line message → last line drives the engine
- reply cap → closer → END
- 2 age-ask misses → progresses to country
- no-repeat pools (greeting, age_gender, how_are_you once; flirty_questions repeats, never same line)
- countries.txt detection and `{country}` / `{snap}` / `%username%` placeholders
- transcript memory, pools_used, last_pool
- proactive ask_snap in neutral middle chat
- no usernames configured → closer instead of an empty share line
- runtime adapter renders `%username%` from the dashboard Usernames list and persists the session
- custom input+output pair auto-joins the scan (and `|` splits output lines)

### Windows compile validation

```text
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```

Completed successfully. This verifies the changed Go source compiles for the project's Windows target.

### Frontend checks

```text
node --check cmd/chitchatui/frontend/dist/app.js
```

Completed successfully. Additional structural checks verified the EVAChat mode radio button, EVAChat panel, data-folder action, and absence of old keyword-panel DOM references.

## Not run in this Linux workspace

A native Windows Wails desktop bundle cannot be produced here. Run the documented Windows command before release:

```powershell
wails build -clean -webview2 error
# or
.\scripts\bundle-windows.ps1 -Clean
```

The unmodified repository-wide Linux browser test also requires a runnable Chromium desktop environment, and the root Windows-license package uses the Windows-only WMI dependency. Those environment limitations are unrelated to the EVAChat changes.

## Sensitive-data verification

The delivered source tree has no `data/` runtime directory, profile sessions, login JSON, Badger database, cookies, or account-specific logs. `.gitignore` excludes `data/` to prevent these from being committed in a future release.
