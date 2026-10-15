# Project Map — ChitChat Bot (root project)

This file is the architecture map for any human or AI agent joining the
project. The block between the AUTO markers is regenerated from the actual
source tree by `scripts/project-map.cjs` (automatically on every
`npm run verify`, or manually with `npm run map:update`). Read it together
with `AGENTS.md` (locked scope/rules) and `docs/PROJECT_MEMORY.md`
(decisions and current state).

<!-- AUTO:BEGIN -->
_Auto-generated 2026-10-15 by `scripts/project-map.cjs`. Do not edit this block by hand._

## Source inventory

- Total source files: **185** (1087.3 KiB, excluding runtime/private data)

| Area | Files | Size |
|---|---:|---:|
| `internal/` | 88 | 445.6 KiB |
| `capture/` | 25 | 114.3 KiB |
| `(root files)` | 24 | 185.8 KiB |
| `apitest/` | 12 | 99.3 KiB |
| `docs/` | 12 | 73.8 KiB |
| `cmd/` | 9 | 83.5 KiB |
| `scripts/` | 8 | 39.7 KiB |
| `tests/` | 7 | 45.3 KiB |

## Internal Go packages

- `internal/api/` — 1 Go file(s)
- `internal/applog/` — 4 Go file(s)
- `internal/bot/` — 21 Go file(s)
- `internal/browser/` — 9 Go file(s)
- `internal/license/` — 1 Go file(s)
- `internal/model/` — 2 Go file(s)
- `internal/proxy/` — 7 Go file(s)
- `internal/runtimeassets/` — 2 Go file(s)
- `internal/store/` — 1 Go file(s)
- `internal/uistore/` — 2 Go file(s)
- `internal/workerplan/` — 2 Go file(s)

## Browser options (Dashboard → Browser)

1. `rod` — Rod
2. `chromedp` — ChromeDP
3. `show_mode` — Show Mode

## Key entry points

- `main.go` / `app.go` — Wails application and bound methods
- `cmd/chitchatui/` — dashboard frontend (embedded `frontend/dist/`)
- `capture/` — standalone manual traffic capture tool (see `capture/README.md`)
- `scripts/verify.cjs` — browser-free verification gate (`verify.bat`)
- `scripts/package-source.cjs` — source ZIP packaging (refuses stale reports)
- `install.bat` / `run-dashboard.bat` — Windows setup and launch
- Capture tool version: **1.2.1**

## Documentation

- `docs/BROWSER_DIRECT_RUN.md`
- `docs/CHAT_ENGINE_MAPPING.md`
- `docs/CHITCHAT_PROTOCOL.md`
- `docs/DEBUG_CLEANUP.md`
- `docs/FILE_LOGGING.md`
- `docs/HOME_CHAT.md`
- `docs/PROJECT_MAP.md`
- `docs/PROJECT_MEMORY.md`
- `docs/PROXY_TEST.md`
- `docs/RECHECK_2026-10-15.md`
- `docs/SHOW_MODE.md`
- `docs/SHOW_MODE_FIX.md`
- `docs/VERIFICATION.md`
- `AGENTS.md` — locked scope and hard rules (read first)
- `START-HERE.md` — packaging/testing entry guide
<!-- AUTO:END -->

## Manual notes (agents: keep this section current)

- Product: Windows desktop app (Go + Wails) + CLI worker for chitchat.gg.
- Dashboard Browser page has numbered per-row Run buttons; option behaviour is
  documented in `docs/BROWSER_DIRECT_RUN.md` and `docs/SHOW_MODE.md`.
- `capture/` is a standalone manual capture tool. It is NOT part of the
  desktop app runtime and must stay dependency-free (Node built-ins only).
- Verification never launches a browser or a real login by default.
