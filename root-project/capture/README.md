# Capture Tool — browser network / WebSocket / DOM capture

আপনি নিজে browser-এ manually browse/login/chat করবেন, tool পেছনে সবকিছু
real-time save করবে — যাতে পরে সেই capture দেখে WebSocket/API automation
ঠিকভাবে বানানো যায়। কোনো site automation এই tool করে না; browser-টা
সম্পূর্ণ আপনার হাতে।

## প্রথমবার (শুধু একবার)

1. `install.bat` double-click করুন — Node.js আর Chrome/Edge আছে কিনা check করবে।
   কোনো npm package লাগে না (tool শুধু Node built-in + আপনার browser ব্যবহার করে)।
2. Node না থাকলে https://nodejs.org থেকে Node 22 LTS install করে আবার `install.bat` চালান।

## প্রতিবার capture করতে

1. `run.bat` double-click করুন → একটা normal browser খুলবে (ChitChat-এ)।
   অন্য site ধরতে চাইলে: command line-এ `run.bat --url https://example.com`
2. নিজে login করুন, normally chat/browse করুন।
3. **সবচেয়ে দরকারি অংশ — marker দিন:**
   - অন্যের message আসা মাত্র **Ctrl+Alt+I** চাপুন (= incoming-এর আসল উদাহরণ)
   - নিজে message পাঠিয়েই **Ctrl+Alt+O** চাপুন (= outgoing-এর আসল উদাহরণ)
   - যেকোনো সময় page-এর অবস্থা ধরে রাখতে **Ctrl+Alt+S** (snapshot)
   - Terminal-এও একই কাজ: `I` / `O` / `S` key, থামাতে `Q`
4. শেষ হলে terminal-এ `Q` চাপুন অথবা browser বন্ধ করে দিন। সব data
   `capture-sessions\<সময়>_<site>\` folder-এ save হয়ে যাবে।

### Live logs — capture চলাকালীন যা দেখবেন

- প্রতিটা সাফল্য ধাপে: `HOYE GECHE: <কী পাওয়া গেল> => labh: <এটা দিয়ে bot-এ কী করা যাবে>`
- প্রতি 20 সেকেন্ডে এক লাইনে অবস্থা: কত HTTP, কয়টা WebSocket frame (পাঠানো/আসা),
  কয়টা DOM message, কয়টা marker — মানে কতদূর capture হয়েছে ফলো রাখতে পারবেন
- থামালে শেষে পুরো **capability checklist** দেখাবে, আর session folder-এ
  `SUMMARY.md`-তে একই report save থাকবে — কোনটা পাওয়া গেছে, কোনটা বাকি
  (যেমন marker দেওয়া হয়নি), সব লেখা থাকবে

## কোন file-এ কী থাকে (share করার সময় এটাই পাঠাবেন)

| File | কী আছে |
|---|---|
| `manifest.json` | Index: কখন, কোন site, কতগুলো request/frame, coverage |
| `messages/labeled-incoming.jsonl` | আপনার confirm করা incoming উদাহরণ (সবচেয়ে মূল্যবান) |
| `messages/labeled-outgoing.jsonl` | আপনার confirm করা outgoing উদাহরণ |
| `messages/correlation.json` | কোন network event থেকে কোন visible message এলো |
| `messages/*-candidates.jsonl` | Tool-এর নিজে detect করা সম্ভাব্য message |
| `websockets/*.jsonl` | প্রতি socket-এর সব frame (sent/received, event name, payload) |
| `api-catalog.json` | সব API endpoint-এর pattern, method, status, body-র key গুলো |
| `network.har` | DevTools-এ import করা যায় এমন HTTP archive |
| `http/*.json` | প্রতি request-এর headers + body আলাদা করে |
| `dom/*.html`, `screenshots/*.png` | Page-এর HTML ও ছবি snapshot |
| `storage/*.json` | Cookies + local/session storage snapshot |

Session folder-এর ভিতরে `SHARE-README.md`-ও তৈরি হয়ে যায়।

## ভবিষ্যতে site বদলে গেলে (আবার শূন্য থেকে শুরু নয়)

ChitChat যদি API/WebSocket বদলে ফেলে, এই workflow-তেই fix হবে:

1. **এখনই একটা baseline capture রাখুন** — ভালোভাবে চলা অবস্থায় একবার
   `run.bat` দিয়ে capture করে marker (Ctrl+Alt+I/O) সহ session-টা রেখে দিন।
2. Site বদলে গেছে মনে হলে **আবার একইভাবে নতুন capture** নিন।
3. দুটো session compare করুন:
   `compare.bat capture-sessions\<old-folder> capture-sessions\<new-folder>`
4. নতুন session-এ তৈরি `DIFF.md` দেখুন — কোন endpoint/event যোগ-বিয়োগ
   হয়েছে, কোন payload key বদলেছে, কোন event-এর নাম পাল্টেছে — সব তালিকা আকারে থাকবে।
5. ওই `DIFF.md` + নতুন session folder আমাকে share করুন — শুধু বদলে যাওয়া
   অংশটাই fix করতে হবে, বাকি সব আগের মতোই থাকবে।

প্রতি session-এর `manifest.json`-এ একটা schema fingerprint থাকে — দুটো
capture-এর fingerprint মিলে গেলে protocol-এর গঠন বদলায়নি বলে দ্রুত নিশ্চিত
হওয়া যায়। পুরনো (catalog-আসার আগের) session-এর সাথে compare করলেও tool
চলবে, শুধু diff অসম্পূর্ণ হতে পারে বলে note দিয়ে দেবে।

## Safety (গুরুত্বপূর্ণ)

- Default-এ token, cookie, password, Authorization **REDACTED** থাকে —
  মানে আসল secret save হয় না, শুধু গঠনটা থাকে। এই অবস্থায় folder share করা safe।
- `capture.config.json`-এ `rawMode: true` করলে আসল secret save হবে।
  **Raw session কখনো কারো সাথে share করবেন না।**
- Browser profile `profiles\` folder-এ থাকে (login মনে রাখে)। এটা কখনো share করবেন না,
  আর এটা source ZIP-এও যায় না।
- Tool শুধু আপনার নিজের PC-তেই data রাখে; কোথাও upload করে না।
- শুধুমাত্র নিজের account আর যেসব site-এ আপনার অধিকার আছে সেগুলোতেই ব্যবহার করুন।

## নিজে test করতে চাইলে (real site ছাড়া)

```
node selftest\run-selftest.cjs
node --test tests\capture.test.cjs
```

Self-test একটা local fake chat page চালিয়ে পুরো pipeline verify করে —
Chrome/Edge না থাকলে SKIP লিখবে, তখন Windows-এ চালালেই হবে।

## সমস্যা হলে

- "No Chrome/Edge found" → Chrome install করুন, অথবা `capture.config.json`-এ
  `"browserPath": "C:\\path\\to\\chrome.exe"` দিন।
- Browser আগেই খোলা profile নিয়ে ঝামেলা করলে `profiles\` folder-এর browser
  instance আগে বন্ধ করুন।
- কোনো site-এ WebSocket payload binary/encoded হলে সেটা raw আকারে save হয়;
  DOM correlation দিয়ে তখনও বোঝা যায় কোন frame কোন message — decode পরের ধাপের কাজ।
