# EVAChat Flow — chat engine mapping (input TXT → rule → output TXT)

This is the authoritative description of what `internal/bot/eva_chat.go`
does. It is a 1:1 port of the reference EVA bot engine (`eva_flow.py`,
`EvaFlowBot`) — same categories, same stage machine, same rules. When you
change the engine, change this file in the same commit.

Runtime folder: `data/eva_chat/` (seeded from `internal/bot/eva_assets/`).

```
data/eva_chat/
├── countries.txt            keyword=country  (user country detection, {country})
├── input/                   USER SMS triggers  (1 per line, '#' = comment)
│   ├── greeting.txt  age_gender.txt  country.txt  how_are_you.txt  your_age.txt
│   ├── english_only.txt  horny.txt  horny_answer.txt  share_snap.txt  snapchat.txt
│   └── middle_chat/  flirty_reply.txt  warm_reply.txt  busy_later.txt
└── output/                  BOT replies  (1 per line, '|' also splits a line)
    ├── greeting.txt  age_gender.txt  age_ask.txt  country.txt  how_are_you.txt
    ├── your_age.txt  english_only.txt  horny.txt  horny_answer.txt
    ├── flirty_questions.txt  ask_snap.txt  retry_nudge.txt  share_snap.txt
    ├── snapchat.txt  closer.txt
    └── middle_chat/  flirty_reply.txt  warm_reply.txt  busy_later.txt
```

Same file name in `input/` and `output/` = same category. Dropping a new
`input/x.txt` + `output/x.txt` pair adds a category with no code change.

## 1. Category table

| Category (file name) | input/ detects (user says) | output/ replies with | Repeat? |
|---|---|---|---|
| `greeting.txt` | hi, hey, hlw, sup … | bot greeting | once |
| `age_gender.txt` | m21, m 21, 20m, f19 (regex `([mf])\s?(\d{1,2})`, 13–99) | "f 20", "20f" … | once |
| `age_ask.txt` | *(bot-initiated)* no age after greeting | "f 20 u? age?" | once |
| `country.txt` | where u from?, from?, (also "im from X" lines) | bot's own country ("uk wbu") | once |
| `flirty_questions.txt` | *(bot-initiated)* funnel driver / no match | flirty question | repeat |
| `how_are_you.txt` | how are u, hru, wyd | "bored no cap u?" | once |
| `your_age.txt` | how old are u, ur age? | "19", "im 20" | once |
| `english_only.txt` | hola, como estas … (STRICT match) | "sorry i only speak english" | once |
| `horny.txt` | horny statements/keywords + leet regex (h0rny, s3x, n0des, d1ck, f*ck, nsfw, 18+, onlyfans …) | "little bit", "maybe 🥵" | repeat |
| `horny_answer.txt` | direct horny QUESTION (horny?, hrny?, are u horny?, or `?` + leet) | "yes", "yep", "so much" (line 1) | repeat |
| `middle_chat/flirty_reply.txt` | ur cute, love u, kiss, single? | "ngl ur adorable" | repeat |
| `middle_chat/warm_reply.txt` | lol, ok, nice, cool, idk | "hehe okay" | once |
| `middle_chat/busy_later.txt` | gtg, bye, brb, sleepy | "np, text me when free" | once (2nd → closer) |
| `ask_snap.txt` | *(bot-initiated)* after horny / flirty_reply; or after 3 neutral MIDDLE replies | "u got snp?" | repeat |
| `retry_nudge.txt` | *(bot-initiated)* "no" after the ask | "come on add me na" | once |
| `share_snap.txt` | yes-words after the ask (yes, yeop, ok, sure, send, add me, @ …) | share line with `%username%` | terminal |
| `snapchat.txt` | user offers own snap (my snap is, add me on snap) | "added u on snap %username%" | terminal |
| `closer.txt` | *(bot-initiated)* END / cap / underage / double refusal | "ttyl, snap me 👻" | repeat |

Scan priority (first match wins): `share_snap, snapchat, horny,
middle_chat/flirty_reply, how_are_you, your_age, english_only, country,
greeting, age_gender, middle_chat/busy_later, middle_chat/warm_reply`, then
any custom category sorted by name. `horny_answer` is never in the scan
(dedicated logic). Output files named `info.txt` / `README.txt` are not pools.

## 2. Matching layers (per input file)

1. exact — raw lowercase or punctuation-stripped text equals a trigger
2. word-boundary substring — trigger (≥3 chars) appears as whole words
3. (2.5) regex layer — `horny.txt` only: leet/obfuscated patterns
4. slang-normalised exact/substring — u→you, ur→your, r→are, im→i am,
   wat/wut→what, wats→what is, whr→where, wbu/wby→what about you,
   hbu→how about you, sc→snapchat (skipped for triggers that would collapse
   to one token, e.g. "ur @")
5. token subset — all trigger tokens (≥3 chars, at least one ≥4) present in
   any order

`english_only.txt` is STRICT: layers 1–2 only.
No-file fallbacks: gender/age regex, `countries.txt` (longest keyword first,
built-in fallback map if the file is missing).

## 3. Stage machine

`NEW → WAIT_AGE_GENDER → WAIT_COUNTRY → MIDDLE → END`

Bot-writes-first (DM/match modes): `Start()` sends `greeting.txt` and goes
to `WAIT_AGE_GENDER`. User-writes-first: stage `NEW` below.

ALWAYS, before any stage, in this order for every user message
(multi-line message → only the LAST line is used):

1. harvest gender/age (first time only) and country
2. age < 18 → `blocked = true` (never shares, positive → `closer.txt`)
3. stage END → `closer.txt`
4. share gating (`shouldShare`):
   * refusal regex (dont have snap / no snap / not on sc …) → never share
   * user asks for the handle (regex snap|sc|snapchat|snp|ghost|insta|
     instagram|telegram|tg|discord|dc|@) or hits `input/snapchat.txt` →
     share NOW
   * bare yes-words / `input/share_snap.txt` / "@" → share ONLY if
     `asked_snap` is set
   * share pool = `snapchat.txt` if that category matched, else
     `share_snap.txt` → stage END. No usernames configured → `closer.txt`.
5. horny QUESTION (`input/horny_answer.txt` or `?` + leet) → 2 lines:
   `horny_answer.txt` then `horny.txt` (sent as a follow-up message),
   `ask_next = true`, counts 2 replies, stage → MIDDLE
6. `ask_next` set → `ask_snap.txt` regardless of what the user wrote
   (`asked_snap = true`)
7. after the ask (last pool ask_snap/retry_nudge) a negative (no/nope/nah/
   naw/not really/never/later/nvm) → `retry_nudge.txt` once; second
   negative → `closer.txt` → END

Then the stage:

| Stage | user message | reply pool | next stage |
|---|---|---|---|
| NEW | age/gender | `age_gender.txt` | WAIT_COUNTRY |
| NEW | country question | `country.txt` | WAIT_COUNTRY |
| NEW | anything else | `greeting.txt` | WAIT_AGE_GENDER |
| WAIT_AGE_GENDER | age/gender | `age_gender.txt` | WAIT_COUNTRY |
| WAIT_AGE_GENDER | country question | `country.txt` | WAIT_COUNTRY |
| WAIT_AGE_GENDER | other category | that pool (miss +1; 2 misses → also `country.txt`/`flirty_questions.txt`) | → WAIT_COUNTRY on 2 misses |
| WAIT_AGE_GENDER | no match | `age_ask.txt` (miss +1); 2 misses or age_ask spent → `country.txt`/`flirty_questions.txt` | → WAIT_COUNTRY |
| WAIT_COUNTRY | country statement (no `?`) | `flirty_questions.txt` | MIDDLE |
| WAIT_COUNTRY | country question | `country.txt` (once) | stays |
| WAIT_COUNTRY | late age/gender | `age_gender.txt` (once) | stays |
| WAIT_COUNTRY | other category | that pool; 2 misses → `flirty_questions.txt` | → MIDDLE on 2 misses |
| WAIT_COUNTRY | no match | `flirty_questions.txt` | MIDDLE |
| MIDDLE | reply count ≥ 8 | `closer.txt` | END |
| MIDDLE | busy words again (busy_later spent) | `closer.txt` | END |
| MIDDLE | category match (available pools only) | that pool | stays |
| MIDDLE | no match, not asked yet, ≥3 MIDDLE replies | `ask_snap.txt` (`asked_snap`) | stays |
| MIDDLE | no match | `flirty_questions.txt` | stays |

After any reply whose pool is `horny.txt` or `middle_chat/flirty_reply.txt`
→ `ask_next = true` (the re-ask loop: horny/flirty → ask → yes → share).

## 4. Pick rules (`pick`)

* NO-REPEAT (file): a non-repeatable pool fires once per user; a spent pool
  is redirected to `flirty_questions.txt`. Repeatable:
  `flirty_questions, closer, horny, middle_chat/flirty_reply, ask_snap,
  horny_answer`.
* NO-REPEAT (line): unused lines first; never the same line back-to-back;
  pool exhausted → start over.
* Missing pool → one of `hii / hey / lol / hmm / oh nice` so the bot never
  goes silent.
* Placeholders: `{country}` → user's country or `usa`; `%username%` and
  `{snap}` → a dashboard **Usernames** entry (per-user assignment via
  `getOrAssignUsername`, increments the snap/tg counter, flags the user).

## 5. Per-user memory (`model.EVAChatSession`, persisted in bot state)

`stage, sequence_step, user_gender, user_age, user_country, blocked,
bot_reply_count, middle_replies, ask_next, asked_snap, snap_nudges,
horny_detected, snap_pivoted, misses, pools_used, used_replies (per pool),
last_pool, transcript (last 60 user/bot lines)`.

## 6. Differences from the reference (deliberate, small)

| Reference (`eva_flow.py`) | This port | Why |
|---|---|---|
| snap id from `data/snap_ids.txt` round-robin | dashboard **Usernames** list, same mechanism as the other two modes | one place to manage handles + existing counter/flagging |
| no proactive ask | after 3 neutral MIDDLE replies the bot sends `ask_snap.txt` once | otherwise a user who never flirts never reaches the ask→yes→share chain |
| country answer = country detected AND no `country.txt` hit | also counts as answer when the message has no `?` | `input/country.txt` contains "im from X" statements, which would otherwise be treated as a question |
| 2nd horny line fetched via `get_pending_replies` | returned as `EVAReplyResult.Extra` and sent as a follow-up message | Go worker wiring |
| `EVA_MAX_REPLIES` env | constant `evaMaxReplies = 8` | no env plumbing in the desktop app |

## 7. Tests

`internal/bot/eva_chat_test.go` mirrors the reference `test_flow.py` checks:
pack integrity, comment triggers, basic funnel, first-SMS variants, opener,
horny 2-line → ask → share, leet, middle categories, direct ask shares,
yes-words without ask never share, refusal → nudge → closer, repeat loop with
different lines, underage block, multi-line, reply cap, age-ask progression,
no-repeat pools, countries + placeholders, transcript memory, proactive ask,
no-usernames closer, adapter username rendering, custom category auto-join.

## 8. AI compose layer (hybrid) — `internal/bot/eva_ai.go`

Optional. Dashboard → Message → EVAChat Flow → **AI reply text (hybrid)**:
API base URL (any OpenAI-compatible `/chat/completions`), model, key
(stored only in local `data/` settings, never in git). **Test AI** button
sends one small request.

Division of labour:

| Decided by the TXT engine (unchanged) | Written by the AI |
|---|---|
| stage, category, pool, misses | the reply TEXT for every composable pool |
| ask_snap → yes → share chain, retry_nudge, closer | — (share_snap/snapchat lines are NEVER rewritten; they carry `%username%`) |
| age gate, reply cap, no-repeat pools | — |
| 2nd line of the horny answer (`horny.txt`) | 1st line (`horny_answer.txt`) |

Per call the model receives: system prompt = `data/eva_chat/persona.txt`
(with `{age}` / `{country}` from the per-user persona: random age 20–25 and a
random line of `persona_countries.txt`, fixed for that user) + hard rules; user
prompt = last 24 transcript lines, known facts (stranger age/gender/country),
the stranger's last message, the **intent** of the chosen pool (table in
`evaAIIntents`) and up to 4 style examples from that pool.

Output guard (`evaAISanitize`): first line only, quotes/prefixes stripped,
lowercased, ≤160 chars, rejected if it contains `@`, links, telegram/whatsapp,
"as an ai"-style text, any configured username, or repeats an earlier bot
line. Any rejection or API/network error → the TXT line is sent instead and
`[evachat-ai] fallback ...` is logged. The transcript always stores the text
that was actually sent.
