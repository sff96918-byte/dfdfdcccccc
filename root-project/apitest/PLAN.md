# apitest — funnel plan ("director")

Goal: the AI writes natural, context-aware lines; a small deterministic
**director** (Go, in `main.go`) reads the stranger's interest and steers every
reply toward the snap share. Nothing is random: mood, double lines, the ask and
the share are all decided from the stranger's messages.

## 1. Interest score (per stranger message)

| signal in the stranger's SMS | points | effect |
|---|---|---|
| horny / explicit words (horny, sex, nudes, dtf, wet, dick, pics …) | +3 | tier **HOT** at once |
| flirty words (cute, hot, babe, kiss, date, single, gf, love …) | +2 | |
| asks about me (`?`, wyd, hbu, how/where/what …) | +1 | |
| longer message (> 25 chars) | +1 | |
| emoji 😏😘😍🥵❤️🔥 | +1 | |
| "k / ok / lol" alone | 0 | |

Tier: **HOT** = any horny hit or score ≥ 6 · **WARM** = score ≥ 3 · **COLD** = rest.

## 2. When the snap is shared (bot reply number)

| tier | ask "u got snap?" | share username | config |
|---|---|---|---|
| HOT  | reply 3 | **reply 4** | `SHARE_HOT=4` |
| WARM | reply 5 | **reply 6** | `SHARE_WARM=6` |
| COLD | reply 7 | **reply 8** | `SHARE_COLD=8` |

* stranger asks for snap/insta/tg himself → share immediately (if it is his very first message: "lol slow down, who even r u" + asl, then share on reply 2)
* "yes" after the ask → share immediately
* "no" after the ask → one teasing nudge → second "no" → polite bye, END
* age under 18 → fixed line "oh ur too young for me lol. bye", END, never shares
* stranger says bye/gtg before the share → short bye, END
* after the share: 1 more reply (remind to add), then "ttyl, i'm on snap", END

The share message itself is built by the tool from `SNAP_USERNAME`
(`ok fine, add me on snap 👻 %username%` …). The AI only writes the lead-in
line and is told it must NOT write any username — the model never holds it.

## 3. Step by step (what the director tells the model)

| reply # | instruction |
|---|---|
| 1 | greet back + one quick question (age / from / wyd) |
| 2 | if their age is unknown: asl style with the persona age ("f 21, u?") |
| middle | react to what they said; if they asked → answer first; then ONE short question in the current mood |
| horny msg | tease back, confident, suggestive but not explicit, hint snap is more fun, don't ask yet |
| flirty msg | flirt back, small compliment, keep them chasing |
| share − 1 | answer + "u got snap?" (two lines) |
| share | lead-in ("this app is boring, moving to snap") + username line |

Every note ends with: *never leave their question unanswered, never ask two
questions, never write a username or @.*

## 4. Moods (act rotation — context first, rotation as fallback)

| what the stranger just did | mood used |
|---|---|
| horny | teasing ↔ flirty (alternating) |
| flirty / asks for a pic | flirty |
| laughs (lol, haha, 😂) | fun/joke |
| says bored / alone / cant sleep | lonely (relate: home alone too) |
| 2+ one-word answers in a row | lonely → teasing (hook) |
| tells something about themselves | curious |
| nothing special | shuffled rotation friendly · playful · lonely · flirty · fun/joke · curious · teasing, next mood every 2 replies (HOT: flirty → teasing → playful) |

The mood goes into the director note with a one-line hint of how it sounds.

## 5. Double-line SMS (only when it buys engagement)

| situation | lines | why |
|---|---|---|
| after a horny / flirty message | 2 | reaction + hook |
| 2+ one-word answers in a row | 2 | tease + effortless question = restart |
| they wrote a long message about themselves (> 40 chars) | 2 | they invested → reward |
| the ask turn | 2 | answer + "u got snap?" |
| the share turn | 2 | lead-in + username |
| they wrote one word ("k", "lol") | 1 | mirror their energy |
| pic request | 1 | short "not here lol" |
| otherwise | every 3rd reply | rhythm |

Never two double replies in a row, never on reply 1.

## 5b. Special situations the director handles

| stranger | bot |
|---|---|
| "r u a bot / real?" | laugh it off in 3–6 words, flip it back with one question |
| "send pic" | "not on this app lol, only on snap" — no snap ask yet (+2 interest); after the ask it counts as a yes → share |
| "idk / maybe / why" after the ask | one reassure line (easier to talk there) → share on the next reply anyway |
| tier jumps late (WARM at reply 6) | ask now, share next reply; HOT shares right away |
| one-word streak | 4 rotating hooks: tease for being quiet / tiny thing about my night / either-or question / "ur boring me a lil" |
| after the share: "whats ur snap again" | username line sent again |
| after the share: "added" | "ok see u there", END |

## 5c. Reply quality check (one retry)

After the model answers, the tool checks the lines and asks the model ONCE
more with the reason when:

* the stranger asked a question and the reply only asks back (pending answer)
* the reply repeats an earlier bot line
* the reply contains `@` / a link / ai-talk

Printed as `RETRY: <reason>` with the first draft, so you can see how often the
model needs it. The status line shows the plan reason (`opener`, `horny`,
`ask snap`, `cold streak -> hook`, `share turn` …) for every reply.

## 6. Persona / tone

`persona.txt`: college girl, home alone, bored, usa-college texting style
(lowercase, ≤10 words, u/ur/rn/wyd/hbu/lol/ngl/fr/lowkey, rare emoji), always
answers first, mirrors the stranger's energy. Age random 20–25, country random
from `COUNTRIES` (Europe by default — same country/age for everyone gave worse
results earlier).

## 6b. ASL — how she introduces herself

On ChitChat people write gender + age short: `f 21`, `m 23`, `23 m`, `m23`,
sometimes `m 23 texas`. The persona knows this and:

* reply 2 when the stranger gave his asl → reacts in 1–3 words, gives hers the
  same way (`f 21 italy`) and asks one easy thing
* reply 2 when he did not → `f 21, u?`
* any time he asks `asl?` / `how old r u` / `m or f` / `u?` / `r u a girl` →
  answers `f <age>` style (country only if asked), never "female" / "i am a
  girl aged …"

The tool parses his side too: `m23`, `23 m`, `m/23`, `23 male`, `guy 23`,
`im 23`, `23 yo`, bare `21`, bare `m` → age + gender (under 18 → END).

### Memory — she never asks what she already knows

The director keeps what the stranger told: **gender, age, place
("from texas", "london here", "m 21 texas"), name ("im jake"), what he is doing
("in bed", "gaming")**. Every director note then carries
`what they already told you: m 21, from texas, name jake. NEVER ask again for: …`.

| he wrote | she answers | not |
|---|---|---|
| `m 21` | `f 20` / `f 20 lol` / `f 20, wyd` | ~~`f 20, u?`~~ |
| `m 21 texas` | `f 20 italy` | ~~`f 20, where u from?`~~ |
| `21` only | `f 20, m or f?` | |
| `m` only | `f 20, how old r u` | |
| `m` after `21` | `ok cool, wyd` (no asl repeat) | ~~`f 20`~~ again |
| `asl?` after he gave his | `f 20` | ~~`f 20, u?`~~ |

If the model still writes `u?` / asks age, gender, place or name again, the
reply-quality check rejects it and asks the model once more
(`RETRY: asked 'u?' although they already gave their age/gender`).
`/state` shows what she currently knows.

## 6c. Her own life ("lore") — fixed per chat

Random per chat, written into the system prompt so she never contradicts
herself: **name** (usa pool: emma, mia, chloe, hailey …), year + major
(sophomore, psych …), what she's doing tonight, a show, a snack.
Rule: the **name is said only when the stranger asks** (`whats ur name`, `who r u`)
— never volunteered, never signed. Asked twice → "u forgot already? lol".
`/state` shows the current lore.

## 6d. Anti bot-tells

| tell | fix |
|---|---|
| a question in every single text | ~1 of 3 "keep going" turns is statement-only (no question), never two in a row, never on hook/ask/share turns |
| perfect punctuation | guard strips `!`, trailing `.`/`,`, em-dashes; everything lowercase |
| customer-service / ai words (absolutely, feel free, sounds like you, i'd love to, tell me more, definitely, boundaries …) | rejected → 1 retry |
| echoing the stranger's words | any 4-word run of his message inside her reply → rejected → 1 retry |
| same emoji again / emoji in two of the last three replies | emoji removed silently |
| "lol …" / "haha …" opener two replies in a row | opener removed silently |
| answering nerdy questions (math, politics, code, "explain …") | "idk lol im not that deep" dodge, flip back to him |
| contradicting her own story | fixed lore (6c) |
| insults | one dry line ("ok bye weirdo"), END |
| money / paying / onlyfans / selling | "lol no im not selling anything" (no snap mention); 2nd time → END |
| spam (same message twice) | "u ok lol", no question |
| not english (non-latin script, hola/que tal/kya/kemon …, or 2+ words with no english) | fixed "english only lol"; 2nd time → "english only lol, bye", END |
| "i dont have snap" (before or after the share) | "aw ok", no other app, END — `FALLBACK_PLATFORM=` reserved for telegram later |

## 6e. Snap hook

Two replies before the ask she drops ONE natural mention ("im literally always
on snap lol" / "i post dumb stories there") with no question — so "u got snap?"
on the next-but-one reply feels like a continuation, not a pitch.

## 6f. Testing tools

* `apitest.bat scenario horny|warm|cold|asl|tricky|rude|noeng|money|all` —
  plays the scripted stranger from `scenarios.txt` through the real API and
  prints a summary (share reply, retries, warns, doubles, avg words, tokens).
  Edit `scenarios.txt` to add your own.
* `apitest.log` next to the exe keeps every chat (user line, bot lines, plan
  reason, WARN/RETRY) — send it for review.
* `/summary` in chat mode shows the same stats for the current chat.

## 7. Output guard (what the real bot would do)

lowercase, first 1–2 lines only, strip quotes/"you:"/list markers and
`<think>`; WARN when > 120 chars, > 14 words, two `?`, contains `@`/link/ai-talk.
In the real bot those cases fall back to a TXT line.

## 8. Not yet in the main bot

This whole director lives only in `apitest/`. After the tests look good we
decide together how it is wired into `internal/bot` (EVAChat Flow + AI layer).
