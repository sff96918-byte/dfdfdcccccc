# Multi-site capture kit — ek browser, onek site, alada alada capture

**কী করে (সংক্ষেপে):** একটা browser খোলে, সেটা **বন্ধ না করে** আপনি একের পর এক
site খুলবেন (প্রতি site-এর জন্য একটা tab), login করবেন, normally chat করবেন।
Kit নিজেই **domain দেখে site চিনে নেয়** এবং সেই site-এর সব capture আলাদা folder-এ
লিখে রাখে — কোনটা কোন site-এর, সেটা নিয়ে কোনো ধোঁয়াশা থাকে না।

```
sessions/20261016-1530_multi/
├── RUN-SUMMARY.md          <- kon site er capture konta (ek nazare table)
├── run.json                <- same info, machine-readable index
├── sites/
│   ├── app.chitchat.gg/    <- site 1 er sob kichu ekhane
│   ├── chatib.net/         <- site 2 er sob kichu ekhane
│   └── isexychat.com/      <- site 3 er sob kichu ekhane
└── browser.log
```

প্রতিটি site folder-এর ভিতরে একই রকম structure (এটা root-project/capture —
আপনার আগের একক-সাইট tool — এর হুবহু একই layout, তাই পুরনো analyzer/compare
tool গুলো এখানেও চলে):

```
sites/<domain>/
├── SUMMARY.md              site er capability checklist + numbers + hosts
├── SHARE-README.md         share korar somoy ki pathaben
├── manifest.json           counts, coverage, capabilities, file index
├── messages/
│   ├── sms-summary.json    *** incoming kivabe ashe / outgoing kivabe pathai ***
│   ├── labeled-incoming.jsonl   আপনার Ctrl+Alt+I marker (ground truth)
│   ├── labeled-outgoing.jsonl   আপনার Ctrl+Alt+O marker (ground truth)
│   ├── incoming-candidates.jsonl / outgoing-candidates.jsonl
│   ├── dom-messages.jsonl  page-e dekha message node
│   └── correlation.json    DOM message <-> network event (time + text match)
├── websockets/             index.jsonl, catalog.json, <tab>_<socket>.jsonl
├── http/                   request/response body (redacted)
├── api-catalog.json        endpoint pattern + body key shapes
├── network.har             Chrome DevTools-এ import korar moto
├── dom/*.html              page-er DOM snapshot (adapter banate lagbe)
├── storage/*.json          cookies/localStorage (redacted)
├── screenshots/*.png
└── visits.jsonl            kon somoy ei site tab-e khulechilen / bondho hoyeche
```

---

## চালানোর নিয়ম (Windows)

```bat
install.bat      :: setup check (Node, browser, shared primitives) — age ekbar
run.bat          :: capture shuru: ek browser khulbe
```

তারপর browser-এ:

1. **প্রথম site** খুলুন (Ctrl+T বা address bar) → login করুন (প্রথমবার) → চ্যাট করুন।
2. **নতুন tab** (Ctrl+T) → **দ্বিতীয় site** → login → চ্যাট। আগের tab বন্ধ করার
   দরকার নেই, browser-ও বন্ধ করতে হবে না।
3. একই ভাবে যত site চান।
4. প্রতিটি message আসা/যাওয়ার সময় marker দিন (নিচে দেখুন)।
5. শেষ হলে terminal-এ **Q** (browser বন্ধ হবে) বা browser window বন্ধ করুন।

চালু থাকা অবস্থায় terminal-এ দেখতে পাবেন:

```
[capture] NEW SITE: app.chitchat.gg => sites/app.chitchat.gg/  (ek site = ek folder, kono mix hobe na)
[capture] Site: chatib.net  ->  sites/chatib.net/   (2 site(s) in this run)
[capture] app.chitchat.gg: HOYE GECHE: WebSocket live frames (realtime chat-er vitti) => labh: ...
```

### Terminal keys (capture চলাকালীন)

| Key | কাজ |
|---|---|
| `i` | incoming example save (partner-এর message এইমাত্র এলো) |
| `o` | outgoing example save (আপনি এইমাত্র message পাঠালেন) |
| `s` | page snapshot save |
| `l` | কোন কোন site folder তৈরি হয়েছে, list |
| `t` | per-site table (HTTP/WS/DOM/markers) |
| `q` | capture শেষ (browser বন্ধ হবে) |

Page-এর ভিতরে একই marker: **Ctrl+Alt+I / Ctrl+Alt+O / Ctrl+Alt+S** (যে tab-এ আপ্ন
আছেন, সেটাই count হয় — তাই site মিশে যাওয়ার ভয় নেই)।

### Linux / Mac

```bash
node tool/doctor.cjs          # setup check
node tool/main.cjs            # capture (--url <start url>, --raw, --headless, --profile <dir>)
```

---

## কীভাবে domain আলাদা হয় (attribution rule)

* একটি **tab = একটি site**। যে page-এ আপনি আছেন (`https://app.chitchat.gg/`), তার
  hostname (থেকে `www.` বাদ দিয়ে) হয় folder name → `sites/app.chitchat.gg/`।
* ওই page যেসব **অন্য host**-এ কথা বলে (`api.chitchat.gg`, `wss://...dditsadn.com`,
  CDN) — সেগুলোর সব frame/request **ঐ site-এরই folder-এ** যায়, এবং প্রতিটি
  record-এ তার নিজের host লেখা থাকে। ফল: site folder-এর `manifest.json` →
  `hosts` list-এ দেখা যাবে "site: api.chitchat.gg" vs "third-party: cdn.other.net"।
* **iframe/worker**-এর ভিতরে থাকা chat (যেমন কোনো site-এর ভিতরে ঢোকানো chat
  widget) parent tab-এর site folder-এই থাকে — আলাদা folder বানায় না।
* **একই tab**-এ অন্য site-এ চলে গেলে (navigation) — নতুন site-এর folder খোলে,
  আগের site-এর file-এ নতুন site-এর কিছু লুকায় না।
* **একই site দ্বিতীয়বার** খুললে same folder-এ append হয় (দুইবার আলাদা folder হয় না)।
* যতক্ষণ কোনো site page ধরা না পড়ে (যেমন `about:blank`), কোনো folder তৈরি হয় না;
  ওই event গুলো `run.json` → `unattributed`-এ গণনা হয় — অনুমান করে কোনো site-এ
  ফেলা হয় না।
* Registrable domain (eTLD+1) **ইচ্ছাকৃতভাবে ব্যবহার করা হয়নি**: full public-suffix
  list ছাড়া ওটা ভুল করে `a.co.uk` আর `b.co.uk`-কে এক folder-এ মিশিয়ে দিত।
  তাই folder = *আপনি যে host-এ ছিলেন*, এবং তার সাথে সম্পর্কিত host-গুলো
  একই folder-এর `hosts` list-এ দেখা যাবে।

## SMS / chat-এর "root" অংশ কোন file-এ

প্রতি site-এর জন্য সবচেয়ে দরকারি ফাইল: **`messages/sms-summary.json`**

```json
{
  "site": "app.chitchat.gg",
  "incoming": {
    "markers": 2, "confirmed": 1,
    "confirmed_examples": [{"source": "websocket", "eventName": "chatMessage",
                            "messageText": "...", "match": "high (text match)"}],
    "how_to_detect": "Confirmed by your marker: WS event \"chatMessage\" (high (text match))",
    "observed_only": {"websocket_events": ["chatMessage", "typing"], "http_endpoints": [...]}
  },
  "outgoing": {
    "confirmed": 1,
    "confirmed_examples": [{"source": "http-request", "method": "POST",
                            "endpoint": "api.chitchat.gg/users/me/conversations/:id/messages"}],
    "how_to_send": "Confirmed by your marker: HTTP POST api.chitchat.gg/users/me/conversations/:id/messages (time-window only)"
  }
}
```

* **`confirmed`** মানে আপনি নিজে marker দিয়ে সেটা চিহ্নিত করেছেন (ground truth)।
* **`observed_only`** মানে tool অনুমান করছে — প্রমাণ নয়।
* কোনো marker দেওয়া না হলে সেটা স্পষ্ট করে **`NOT CONFIRMED`** লেখা থাকে —
  মিথ্যে "কাজ করছে" দেখানো হয় না।

**Marker দেওয়ার নিয়ম (গুরুত্বপূর্ণ):** যে message এসেছে বা পাঠিয়েছেন, তার **15
সেকেন্ডের মধ্যে** marker দিন। Marker ওই site-এর শেষ ১৫ সেকেন্ডের event-গুলোর
সাথে মেলানো হয় (`messages/labeled-*.jsonl` + `correlation.json`)। ভুল site-এর
event কখনো মেলানো হয় না (এটা টেস্ট করা)।

## Config

`capture.config.json`:

| Key | মানে |
|---|---|
| `startUrl` | browser খুললে কোন page দিয়ে শুরু (`about:blank` = নিজে খুলুন) |
| `browserPath` | খালি রাখলে নিজে খুঁজে নেবে (Chrome/Edge/Chromium/Brave) |
| `profileDir` | login মনে রাখার profile folder (default `profiles/browser`) |
| `headless` | false রাখুন — নিজে login/চ্যাট করবেন |
| `rawMode` | **সত্যি না হলে false রাখুন**; true = token/cookie/password save হবে, ওই folder কাউকে দেবেন না |
| `maxBodyBytes` | body কত বড় পর্যন্ত save হবে (default 256 KB) |

**আগের capture tool-এর login reuse করতে:** `"profileDir": "../root-project/capture/profiles/browser"`
— একই profile, তাই যেখানে আগে login করেছিলেন সেখানে আবার login লাগবে না।

## Windows-এ bulk (onek site eksathe)

* এক run-এ যত ইচ্ছে site/tab — প্রতিটা আলাদা folder।
* বেশি চাপ দিতে চাইলে: **এক profile = এক browser window** (একই profile দুটো
  window-এ একসাথে খুলবেন না — Chrome সংঘর্ষ করে), প্রতি profile-এ আলাদা proxy।
  `root-project/capture/tool/browser.cjs`-এর `launchBrowser()` একই কাজ করতে পারে;
  multi-capture-এর `tool/session.cjs`-এ `profileDir` config দিয়ে প্রতি run-এ
  আলাদা profile বসানো যায়।
* Site যখন থামিয়ে দেয়/Cloudflare দেয়: সেখানে একটাই রাস্তা — real browser +
  residential IP; আরো বেশি session একই IP থেকে গেলে নিজেই ধরা পড়বে।
* ভুলে যাবেন না: সাইটের ToS/ব্যান ঝুঁকি আপনার অ্যাকাউন্টে পড়ে; প্রথমে
  throwaway account-এ test করুন।

## এই kit-এ কী verified (2026-10-16, এই repo-তে চালানো)

```
node --test tests/*.test.cjs     ->  25/25 PASS
```

তার মধ্যে (browser ছাড়াই, কিন্তু production code path দিয়েই — ভুয়া CDP
client দিয়ে আসল `session.cjs` চালিয়ে):

1. দুই site দুই tab → দুই folder; কোনো file/data মিশে যায় না (per-site counts, per-tab socket file names)।
2. একটা site-এর marker অন্য site-এর candidate-কে **কখনো** ধরে না (matched=false)।
3. একই tab-এ নতুন site-এ গেলে পরে যাওয়া event নতুন folder-এ যায়, আগের site-এর file অপরিবর্তিত।
4. Third-party host (CDN) ওই site-এর `hosts`-এ জমা হয়, অন্য site-এ নয়;
   role label (`site` vs `third-party`) সঠিক।
5. iframe/worker-এর chat parent tab-এর site folder-এ থাকে।
6. `about:blank`/অজানা page → কোনো folder নেই, শুধু `unattributed` count।
7. Tab বন্ধ → শুধু ওই site-এর `visits.jsonl`-এ `tab_closed`।
8. `stop()` → প্রতি site: SUMMARY/SHARE/manifest/HAR/catalogs/`sms-summary.json`/DOM snapshot/storage(redacted);
   run-level `run.json` + `RUN-SUMMARY.md`; per-site manifests-এ counts আলাদা।
9. কোনো page মরে গেলেও report লেখা শেষ হয় (crash করে না)।
10. Cookies/localStorage redacted (default), `token` জাতীয় key `REDACTED`।

**NOT RUN (এখানে browser নেই, দাবি করছি না):** আসল Chrome/Edge-এ launch, সত্যিকারের
site-এ login/chat, proxy, Windows-এ `run.bat`, একসাথে অনেক window।
Windows-এ `install.bat` (doctor) আর তারপর `run.bat` — সেটাই আসল প্রমাণ।

## Limitations (সৎ তালিকা)

* Chat যদি `<canvas>`/WebRTC video overlay-এর ভিতরে হয় (DOM-এ message node নেই),
  তাহলে শুধু network capture পাবেন — DOM selector পাবেন না।
* Closed shadow DOM / cross-origin OOPIF-এর *নিজের* network event কিছু ক্ষেত্রে
  parent session-এ আসে না (iframe-এর URL/host অবশ্য `hosts`-এ থাকে)।
  আগের একক-সাইট tool-এরও একই সীমা।
* Service worker-এর ভিতর দিয়ে যা যায়, সেটা browser-এর নিজের কোনো কোনো ক্ষেত্রে
  কম দেখা যায় (worker target attach করা হয়, তবু সব নয়)।
* `rawMode` ছাড়া কিছু token/cookie value দেখতে পাবেন না (ইচ্ছাকৃত)।
* এই tool কোনো site-এ নিজে কিছু করে না — শুধু देखে/লিখে রাখে। Site switch করা,
  login করা, chat করা — সব আপনার হাতে।

## পরের ধাপ (bot বানানোর জন্য)

1. যত site ঘুরবেন, প্রতিটা `sites/<domain>/messages/sms-summary.json` দেখে
   নিন `incoming.how_to_detect` / `outgoing.how_to_send` CONFIRMED কি না।
2. `sites/<domain>/dom/*.html` ফাইলটা নিন → `bulk-sites/kit/check.cjs` দিয়ে সেই
   site-এর observer adapter লিখে যাচাই করুন (আগের ধাপের kit)।
3. দুটো run-এর পার্থক্য বের করতে চাইলে: `node ../root-project/capture/tool/compare.cjs <old-session> <new-session>`
   — file format একই, তাই সরাসরি চলে।
