# Multi-site ("bulk site") bot — plan + adapter starter kit

**সংক্ষেপে:** হ্যাঁ, amra ekadhik site-e (ba "bulk" site-e) auto chat bot banate
pari — chitchat.gg যে পদ্ধতিতে বানানো, ঠিক সেই পদ্ধতিতেই। কারণ bot-এর ৮০% কোড
আগেই সাইট-নিরপেক্ষ (browser চালানো, reply engine, dashboard, proxy, logging);
প্রতি সাইটে শুধু **একটা ছোট observer script** লিখতে হয় যেটা ওই সাইটের page থেকে
"কে কী লিখেছে / কোথায় লিখতে হবে / কোন বাটন চাপতে হবে" পড়ে একই JSON দেয়।

এই folder-টা সেই কাজের starter kit: আসল সাইটে হাত দেওয়ার **আগেই** অফলাইনে
(কোনো browser, login বা network ছাড়াই) নতুন সাইটের adapter ঠিক আছে কিনা
পরীক্ষা করা যায়।

```bash
cd bulk-sites
npm install                 # jsdom only (dev)
node kit/check.cjs --all    # contract checker: PASS/FAIL/SKIP per rule
npm test                    # 18 tests over the rules + two real page fixtures
```

আজ এই folder-এ যা যাচাই হয়েছে (2026-10-16): `npm test` **18/18 PASS**,
`check --all` **PASS 26, FAIL 0, SKIP 0**, আর ইচ্ছাকৃতভাবে ভাঙা একটা adapter
**7 rules FAIL** খায় (নিচে দেখুন)। কোনো browser, login বা site touch করা হয়নি —
এই কারণে এটা "live verified" নয়, এটা offline verified.

---

## 1. The answer

The ChitChat bot (both `root-project/` Go and `python-port/` Python) is already
split into a site-neutral core and a small site-specific edge:

| Part | Site-specific? | Where it lives today | Reused for a new site |
|---|---|---|---|
| Browser launch, profile, login capture, proxy | no | `python-port/chitchatport/browser.py`, `proxyrelay.py` | as-is |
| DOM contract (JSON shape of controls/messages) | no | `python-port/chitchatport/domchat.py`, `root-project/internal/browser/domchat.go` | as-is |
| Runner (greeting, echo suppression, dedupe, skip, re-match, delay) | no | `python-port/chitchatport/runner.py` (or `live_chat.go`) | as-is |
| Reply engine (Single / Folder / EVAChat Flow, age gate) | no | `python-port/chitchatport/responders.py`, `root-project/internal/bot/**` | as-is |
| Dashboard, transcript, logs, proxy UI | no | `python-port/chitchatport/ui/**`, `server.py`, `service.py` | as-is |
| **In-page observer** (selectors: rows, author, composer, skip/next) | **yes** | `root-project/internal/browser/showmode_dom.js` | **write one per site** |
| **Login flow / which URL / which control labels** | **yes** | site profile (URL + asset + labels) | **one small entry per site** |

So a new site is **not** a new bot. It is: one observer script + one site profile
+ a login profile, then the same runner drives it.

## 2. Three tiers (pick from evidence, not from hope)

| Tier | How the bot talks | When to use | Effort per site |
|---|---|---|---|
| 1. DOM observer (Show Mode style) | reads/types in the page like a user, in a visible browser | any site you can log into manually; the only option for sites with anti-bot/iframe/WebRTC chat | ~0.5–2 days incl. capture |
| 2. Protocol adapter | your own HTTP/WebSocket client, session cookie from a saved login (the `joingy` way: `/start`, `/event`, `/boop`) | when a capture shows a simple REST/WS API and no Cloudflare challenge | ~2–5 days |
| 3. Hybrid | DOM for login/anti-bot, protocol for messages | large sites that need a real browser to get a session, then a clean API | 3–6 days |

**Decision rule:** run the existing capture tool on the site first. If the chat
travels over an authenticated REST/WS endpoint that a plain client can speak →
tier 2 (cheap at runtime, many sessions, no browser). If the traffic is opaque
(encrypted WS frames, WebRTC, Cloudflare, iframes) → tier 1.

## 3. Onboarding a site (the 10 steps)

1. **Capture once, manually.** `root-project/capture/run.bat --url https://the-newsite.com`
   → log in yourself, have one normal chat, press `Ctrl+Alt+I` when a message
   arrives, `Ctrl+Alt+O` right after you send one, `Ctrl+Alt+S` for a page
   snapshot. Everything lands in `capture/capture-sessions/<time>_<site>/`.
2. **Take a chatting page out of the capture** and save it as
   `bulk-sites/fixtures/<site>-chat.html`. Delete real usernames/messages/ids —
   the repo rule is synthetic or redacted fixtures only.
3. **Copy the adapter:** `sites/example-room/observer.js` →
   `sites/<site>/observer.js`. Change only the marked selector block
   (message list, author cell, composer, next/skip control, own-name control,
   phase rule). The emitted JSON keys must not change.
4. **Register it** in `sites.json` (url, observer path, fixture path, foreign page).
5. **Check it offline:** `node kit/check.cjs --site <site>`. Fix every FAIL row.
   Passing rows you cannot check are reported as SKIP, never as PASS.
6. **Wire it in** as a site profile instead of editing the runner: URL + observer
   asset + control labels + chat-mode defaults. In `python-port` the observer is
   loaded through `domscript.py`, so a second site means another asset + profile,
   not another copy of the runner.
7. **Login:** one visible browser, manual login, saved profile
   (`login_info_*.json`, same schema both apps already use).
8. **Live smoke test:** 1 browser, Count 1, watch for the INCOMING/SEND/SKIP
   lines. Do not test a new adapter on an account that matters.
9. **Only then scale to "bulk":** N profiles → N browsers, one proxy per profile,
   staggered starts, per-session delays and caps.
10. **Re-run the check after every site redesign.** A changed DOM is the #1 way
    a working bot turns into a silent one.

## 4. What "bulk" adds on top

* **Parallel sessions:** one separate browser profile per session (never one
  profile shared by two windows), one proxy per profile, staggered start times.
  Existing pieces: `python-port/chitchatport/proxy.py` + `proxyrelay.py`,
  `multiplechat`'s `proxy_manager.py` and per-site `max_sessions`.
* **Different sites at once:** one profile set per site, one adapter per site,
  one dashboard row per running browser — the contract checker keeps the
  adapters honest.
* **Honest limits:** parallel sessions per site are capped by the site itself
  (anti-bot, rate limits, IP reputation). Headless usually loses the arms race;
  a visible browser per chat is the reliable default. Bulk across *several*
  sites is safer than bulk hammering *one*.

## 5. Risks and hard rules

* **ToS/account bans:** automating a commercial dating/chat site can get accounts
  banned. Keep first tests on throwaway accounts and respect the site's rules.
* **Cloudflare / bot protection:** the `multiplechat` captures already show
  DirtyRoulette/Flingster/Shagle blocked without residential proxies. Tier 1 on a
  protected site typically needs a real browser + residential IP, and may simply
  not be viable.
* **Never leak credentials or private chats:** probe output must stay
  metadata-only (rule R11), captures must be redacted before sharing
  (`capture/` redactor), and no keys/cookies/tokens in the repo.
* **Keep the age gate:** the EVAChat Flow rule (any revealed age < 18 → no snap
  share, polite end) must stay intact on every site.
* **Fail closed:** an adapter that cannot read the page must report "no chat,
  nothing to send" (rule R12) instead of guessing.

## 6. This folder's contents

```
bulk-sites/
├── kit/contract.cjs     the 13 rules (pure functions, unit-tested)
├── kit/harness.cjs      jsdom: load a page + an observer, collect observations
├── kit/run.cjs          one full run for a site (static + interaction rules)
├── kit/check.cjs        CLI: --site / --all / --observer --page [--foreign]
├── sites/example-room/observer.js   worked example: a second, unrelated site
├── sites.json           site registry (chitchat + example-room)
├── fixtures/            synthetic pages (example-room, broken-observer)
└── tests/               18 node:test cases
```

### The 13 contract rules

| Rule | Meaning |
|---|---|
| R1 snapshot-shape | `{controls:{}, messages:[], revision:int}` |
| R2 phase-vocabulary | `lobby` / `matching` / `chatting` only |
| R3 conversation-key | non-empty while chatting; a new chat gets a new key |
| R4 partner-identity | who we are talking to is named, never guessed |
| R5 own-identity | our own display name is reported (echo/confirmed-send) |
| R6 row-identity | every row: non-empty id + text, unique positive order |
| R7 row-authors | grouped rows inherit the previous author; no orphan after that |
| R8 send-gate | `canSend` only inside a live chat with a usable composer |
| R9 control-labels | clean, unique labels usable as exact click targets |
| R10 click-discipline | exact label clicks, near-miss labels are refused |
| R11 probe-privacy | the diagnostic probe never carries message text |
| R12 fail-closed-foreign | on an unknown page: no chat, no clicks |
| R13 snapshot-idempotent | repeated snapshots invent nothing |

### Evidence from this run (2026-10-16)

```
node kit/check.cjs --all
  CHITCHAT      PASS 13, FAIL 0, SKIP 0     (real adapter, synthetic page fixture)
  EXAMPLE-ROOM  PASS 13, FAIL 0, SKIP 0     (synthetic second site)
node kit/check.cjs --observer fixtures/broken-observer.js --page ../root-project/tests/fixtures/showmode-chat.html
  PASS 6, FAIL 7  ->  exit code 1
  FAIL R4 R5 R8 R10 R11 R12 R13             (id-as-author, no identities,
                                             canSend always true, substring click,
                                             probe leak, phantom revision)
npm test   ->  18/18 PASS
```

**NOT RUN (and not claimed):** any real site, any login, Playwright, proxy,
Windows, or multi-session run. This kit is offline-only by design.

## 7. What I need to continue

1. The site URL (and whether it needs a login/registration).
2. One capture session from that site (step 1 above) — or a saved page HTML of a
   chat, redacted.
3. Which tier we are aiming for (DOM first is the safe default; if the capture
   shows a clean API, tier 2 is cheaper at runtime).

Then the work is: `sites/<site>/observer.js` + `sites.json` entry → `check` PASS
→ site profile in the runner → live smoke test with one browser.
