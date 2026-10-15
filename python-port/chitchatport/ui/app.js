/* ChitChat Bot // Show Mode - dashboard client.
 * Talks to the local Python server over fetch(); works identically inside a
 * pywebview window and in a normal browser tab. The per-run API token comes
 * from the <meta name="api-token"> tag the server injects into index.html. */
"use strict";

const TOKEN_TAG = document.querySelector('meta[name="api-token"]');
const API_TOKEN = (TOKEN_TAG && TOKEN_TAG.content) || "";
const TOKEN_READY = API_TOKEN && API_TOKEN !== "__API_TOKEN__";

const state = {
  snapshot: null,
  tab: "browser",
  chatSelected: "",
  logLevels: new Set(["ACTIVITY", "INFO", "WARN", "ERROR"]),
  logSearch: "",
  follow: true,
  failures: 0,
  savedGeneral: false,
  savedMessage: false,
  savedBrowser: false,
  savedProxy: false,
  proxyTesting: false,
  rain: { on: true, columns: 0, drops: [], timer: null },
};

/* ------------------------------------------------------------------ helpers */
const $ = (id) => document.getElementById(id);
const esc = (value) =>
  String(value == null ? "" : value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

function toast(message, isError = false) {
  const box = document.createElement("div");
  box.className = "toast" + (isError ? " error" : "");
  box.textContent = message;
  $("toasts").appendChild(box);
  setTimeout(() => box.remove(), isError ? 9000 : 4200);
}

// A fallback notice (guided flow failed, missing token) must survive the 1 s
// status polls that clear the banner again.
let stickyBanner = "";

function banner(message, ok = false) {
  const node = $("banner");
  if (!message && stickyBanner) message = stickyBanner;
  if (!message) {
    node.hidden = true;
    return;
  }
  node.hidden = false;
  node.classList.toggle("ok", !!ok);
  node.textContent = message;
}

function footer(message) {
  $("footer-status").textContent = message;
}

function clock(seconds) {
  const total = Math.max(0, Math.floor(seconds));
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(Math.floor(total / 3600))}:${pad(Math.floor((total % 3600) / 60))}:${pad(total % 60)}`;
}

/* --------------------------------------------------------------------- api */
async function api(path, { method = "GET", body = null, token = true } = {}) {
  const headers = {};
  if (token && TOKEN_READY) headers["X-API-Token"] = API_TOKEN;
  if (body !== null) headers["Content-Type"] = "application/json";
  const response = await fetch(path, {
    method,
    headers,
    body: body === null ? undefined : JSON.stringify(body),
  });
  let payload = null;
  try {
    payload = await response.json();
  } catch (_) {
    payload = null;
  }
  if (!response.ok) {
    const detail = payload && payload.error ? payload.error : `HTTP ${response.status}`;
    throw new Error(detail);
  }
  return payload;
}

const post = (path, body) => api(path, { method: "POST", body: body || {} });

/* ------------------------------------------------------------------ matrix */
function rainSizing() {
  const canvas = $("rain");
  const ratio = Math.min(window.devicePixelRatio || 1, 1.5);
  canvas.width = Math.floor(window.innerWidth * ratio);
  canvas.height = Math.floor(window.innerHeight * ratio);
  const context = canvas.getContext("2d");
  context.setTransform(ratio, 0, 0, ratio, 0, 0);
  const columns = Math.max(8, Math.floor(window.innerWidth / 16));
  state.rain.columns = columns;
  state.rain.drops = new Array(columns).fill(0).map(() => Math.random() * -50);
}

function rainTick() {
  if (!state.rain.on) return;
  const canvas = $("rain");
  const context = canvas.getContext("2d");
  const width = window.innerWidth;
  const height = window.innerHeight;
  context.fillStyle = "rgba(3, 6, 10, 0.12)";
  context.fillRect(0, 0, width, height);
  context.font = "14px " + getComputedStyle(document.body).fontFamily;
  for (let i = 0; i < state.rain.columns; i += 1) {
    const glyph = String.fromCharCode(0x30a0 + Math.floor(Math.random() * 60));
    const x = i * 16;
    const y = state.rain.drops[i] * 16;
    context.fillStyle = Math.random() > 0.985 ? "#eafff5" : "rgba(0, 255, 156, 0.85)";
    context.fillText(glyph, x, y);
    if (y > height && Math.random() > 0.975) state.rain.drops[i] = 0;
    else state.rain.drops[i] += 1;
  }
}

function rainStart() {
  if (state.rain.timer) return;
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    state.rain.on = false;
    $("motion-toggle").classList.remove("on");
  }
  rainSizing();
  state.rain.timer = setInterval(rainTick, 75);
}

function rainToggle() {
  state.rain.on = !state.rain.on;
  $("motion-toggle").classList.toggle("on", state.rain.on);
  if (state.rain.on) {
    $("rain").style.display = "";
    rainSizing();
  } else {
    const context = $("rain").getContext("2d");
    context.clearRect(0, 0, window.innerWidth, window.innerHeight);
  }
}

/* ------------------------------------------------------------------ render */
function renderRunState(snapshot) {
  const pill = $("run-pill");
  const running = !!snapshot.running;
  const status = String(snapshot.status || "idle").toUpperCase();
  pill.className = "pill " + (running ? (status === "WAITING-LOGIN" ? "waiting" : "running") : "idle");
  pill.innerHTML = `<i></i> ${running ? status.replace("WAITING-LOGIN", "WAIT LOGIN") : "IDLE"}`;
  $("run-btn").disabled = running;
  $("stop-btn").disabled = !running;
  $("stat-uptime").textContent = clock(((snapshot.system || {}).uptime_seconds) || 0);
  $("stat-port").textContent = ":" + (location.port || "—");
  $("login-saved").textContent = snapshot.login_saved ? "login → " + snapshot.login_saved : "";

  const system = snapshot.system || {};
  $("system-list").innerHTML = [
    ["app", system.app || "?"],
    ["python", system.python || "?"],
    ["platform", system.platform || "?"],
    ["data dir", snapshot.data_dir || "?"],
    ["log file", system.log_file || "?"],
    ["dashboard", location.origin],
  ]
    .map(([key, value]) => `<dt>${esc(key)}</dt><dd>${esc(value)}</dd>`)
    .join("");
}

function renderLogs(snapshot) {
  const entries = (snapshot.logs || []).filter((entry) => {
    if (!state.logLevels.has(String(entry.level || "").toUpperCase())) return false;
    if (!state.logSearch) return true;
    return String(entry.message || "").toLowerCase().includes(state.logSearch);
  });
  const list = $("log-list");
  const near = list.scrollHeight - list.scrollTop - list.clientHeight < 80;
  const signature = `${entries.length}|${entries.length ? entries[entries.length - 1].timestamp : ""}` +
    `|${[...state.logLevels].sort().join(",")}|${state.logSearch}`;
  if (list.dataset.signature === signature) {
    if (state.follow && near) list.scrollTop = list.scrollHeight;
    return;
  }
  list.dataset.signature = signature;
  list.innerHTML = entries
    .map((entry) => {
      const level = String(entry.level || "").toLowerCase();
      const time = String(entry.timestamp || "").slice(11, 19) || "--:--:--";
      return `<div class="log-line"><span class="log-time">${esc(time)}</span><span class="log-level ${esc(level)}">${esc(entry.level || "")}</span><span class="log-msg">${esc(entry.message || "")}</span></div>`;
    })
    .join("") || '<div class="empty">no log lines match the filter</div>';
  if (state.follow && near) list.scrollTop = list.scrollHeight;
}

function renderChat(snapshot) {
  const chat = snapshot.chat || {};
  const sessions = chat.sessions || [];
  const wanted = state.chatSelected || chat.selected || "";
  const signature = sessions.map((session) => `${session.id}|${session.active}|${session.dropped}`).join(";");
  const select = $("chat-sessions");
  if (select.dataset.signature !== signature) {
    select.dataset.signature = signature;
    select.innerHTML = sessions.length
      ? sessions
          .map((session) => {
            const selected = session.id === wanted ? " selected" : "";
            const mark = session.active ? " ●" : "";
            return `<option value="${esc(session.id)}"${selected}>${esc(session.label + mark)}</option>`;
          })
          .join("")
      : '<option value="">— no chat observed yet —</option>';
  }
  const messages = chat.messages || [];
  const box = $("chat-messages");
  const near = box.scrollHeight - box.scrollTop - box.clientHeight < 80;
  box.innerHTML = messages.length
    ? messages
        .map(
          (row) =>
            `<div class="msg ${row.role === "bot" ? "bot" : "user"}">` +
            `<span class="who">${row.role === "bot" ? "BOT ▸" : "USER ◂"}</span>` +
            `<span class="txt">${esc(row.text)}</span></div>`,
        )
        .join("")
    : '<div class="empty">nothing observed yet — run Show Mode and log in inside the browser window</div>';
  if (near) box.scrollTop = box.scrollHeight;
  $("chat-meta").textContent = messages.length
    ? `${messages.length} message(s) · evicted ${chat.evicted || 0}`
    : "";
  if (select.value) state.chatSelected = select.value;
}

function render(snapshot) {
  state.snapshot = snapshot;
  if (window.Flow) Flow.observe(snapshot);
  renderRunState(snapshot);
  if (!state.savedBrowser) fillBrowser(snapshot);
  fillProxy(snapshot);
  if (!state.savedGeneral) fillGeneral(snapshot);
  if (!state.savedMessage) fillMessage(snapshot);
  renderLogs(snapshot);
  renderChat(snapshot);
}

/* --------------------------------------------------------- form read/write */
function fillBrowser(snapshot) {
  const browser = snapshot.browser || {};
  $("browser-profile").value = browser.profile || "default";
  $("browser-channel").value = browser.channel || "auto";
  $("browser-headless").checked = !!browser.headless;
}

function fillGeneral(snapshot) {
  const general = snapshot.general || {};
  const mode = general.first_message_mode || "bot";
  document
    .querySelectorAll('input[name="first-message-mode"]')
    .forEach((input) => (input.checked = input.value === mode));
  $("reply-delay-ms").value = general.reply_delay_ms ?? 900;
  $("poll-interval-ms").value = general.poll_interval_ms ?? 350;
  $("poll-min").value = general.poll_min_seconds ?? 20;
  $("poll-max").value = general.poll_max_seconds ?? 40;
  $("action-min").value = general.action_min_seconds ?? 5;
  $("action-max").value = general.action_max_seconds ?? 10;
}

function fillMessage(snapshot) {
  const message = snapshot.message || {};
  $("message-usernames").value = message.usernames || "";
  const loaded = (message.loaded_sequences || []).length;
  $("folder-summary").textContent = loaded
    ? `${loaded} sequence file(s) loaded${message.chat_mode === "folder" ? " · folder mode" : ""}`
    : "no folder loaded";
}

const num = (id) => {
  const value = Number($(id).value);
  return Number.isFinite(value) ? Math.trunc(value) : 0;
};

/* -------------------------------------------------------------------- poll */
async function tick() {
  try {
    const snapshot = await api(`/api/state?selected=${encodeURIComponent(state.chatSelected || "")}`);
    if (state.snapshot && (snapshot.logs || []).length < (state.snapshot.logs || []).length) {
      $("log-list").dataset.signature = ""; // logs were cleared server-side
    }
    state.failures = 0;
    banner("");
    $("stat-link").textContent = "API ●";
    render(snapshot);
    footer(`connected · ${location.origin} · polling`);
  } catch (error) {
    state.failures += 1;
    $("stat-link").textContent = "API ◌";
    footer("connection problem: " + error.message);
    if (state.failures >= 3) {
      banner(
        "Lost the connection to the Python server (" +
          error.message +
          "). If the console window was closed, start it again with run.bat and reload this page.",
      );
    }
  }
}

/* ------------------------------------------------------------------ actions */
/* ------------------------------------------------------------------- proxy */
// Same rule as the Go dashboard: a plain line inherits the selected protocol.
function proxyInputLines() {
  const raw = $("proxy-entries").value;
  if ($("proxy-default-scheme").value !== "socks5") return raw;
  return raw
    .split(/\r?\n/)
    .map((line) => {
      const text = line.trim();
      return text && !/^[a-z][a-z0-9+.-]*:\/\//i.test(text) ? "socks5://" + text : line;
    })
    .join("\n");
}

function proxyLineCount() {
  return proxyInputLines()
    .split(/\r?\n/)
    .filter((line) => line.trim()).length;
}

function updateProxyCount() {
  const lines = proxyLineCount();
  $("proxy-count").textContent = lines ? `(${lines} line${lines === 1 ? "" : "s"})` : "";
  const counter = $("proxy-counter");
  const enabled = $("proxy-enabled").checked;
  counter.className = "pill " + (enabled && lines ? "running" : enabled ? "error" : "idle");
  counter.innerHTML = `<i></i> ${lines} LINE${lines === 1 ? "" : "S"}`;
  $("proxy-rotation").textContent = enabled
    ? lines
      ? `${lines} line(s) rotate across fresh browsers.`
      : "enabled but empty: saving will be refused"
    : "proxy use is disabled";
  return { lines, enabled };
}

function fillProxy(snapshot) {
  const proxy = snapshot.proxy || {};
  if (!state.savedProxy) {
    $("proxy-enabled").checked = !!proxy.enabled;
    $("proxy-entries").value = proxy.entries || "";
    updateProxyCount();
  }
  $("proxy-status").textContent = proxy.error
    ? "Saved list has a problem: " + proxy.error
    : proxy.enabled
      ? `${proxy.lines || 0} line(s) saved and enabled.`
      : "Not tested. Tests use an HTTPS request to app.chitchat.gg through each proxy.";
}

async function saveProxy() {
  const saved = await post("/api/settings/proxy", {
    enabled: $("proxy-enabled").checked,
    entries: proxyInputLines(),
  });
  state.savedProxy = false;
  updateProxyCount();
  toast(saved.enabled ? `proxy saved · ${saved.entries.split(/\r?\n/).filter((l) => l.trim()).length} line(s)` : "proxy disabled");
  await tick();
}

function proxyResultCard(row) {
  const kind = row.ok ? "pass" : row.tunnel_ok ? "warn" : "fail";
  const title = row.ok ? "PASS" : row.tunnel_ok ? "REACHABLE / SITE RESTRICTED" : "FAIL";
  const head = `Line ${row.line}: ${title} — ${row.label || "Invalid entry"} — ${row.latency_ms} ms` +
    (row.status ? ` — HTTP ${row.status}` : "") + (row.authenticated ? " — auth" : "");
  const details = [`<div class="proxy-result-head">${esc(head)}</div>`,
                   `<div class="proxy-result-detail">${esc(row.detail || "")}</div>`];
  if (row.provider_message) details.push(`<div class="proxy-result-note">Provider said: “${esc(row.provider_message)}”</div>`);
  if (row.proxy_authenticate) details.push(`<div class="proxy-result-note">Auth challenge: ${esc(row.proxy_authenticate)}</div>`);
  if (row.hint) details.push(`<div class="proxy-result-hint">Next step: ${esc(row.hint)}</div>`);
  return `<div class="proxy-result ${kind}">${details.join("")}</div>`;
}

async function testProxy() {
  if (state.proxyTesting) return;
  const raw = proxyInputLines();
  const box = $("proxy-results");
  box.innerHTML = "";
  if (!raw.trim()) {
    $("proxy-status").textContent = "Enter at least one proxy to test.";
    return;
  }
  state.proxyTesting = true;
  const button = $("proxy-test");
  button.disabled = true;
  button.textContent = "> TESTING…";
  $("proxy-status").textContent = "Testing proxy authentication and HTTPS access… (up to 12 seconds per proxy; four at a time)";
  try {
    const answer = await post("/api/proxy/test", { entries: raw });
    const rows = answer.results || [];
    let passed = 0;
    let reachable = 0;
    rows.forEach((row) => {
      if (row.ok) passed += 1;
      if (row.tunnel_ok) reachable += 1;
    });
    box.innerHTML = rows.map(proxyResultCard).join("") || '<div class="empty">no lines to test</div>';
    $("proxy-status").textContent = `${rows.length} line(s) tested · ${passed} passed · ${reachable} reached the destination.` +
      " Results are for the values in the box right now; nothing was saved.";
    toast(`${passed}/${rows.length} proxy line(s) passed`);
  } catch (error) {
    $("proxy-status").textContent = "Test failed: " + error.message;
    toast(error.message, true);
  } finally {
    state.proxyTesting = false;
    button.disabled = false;
    button.textContent = "> TEST PROXY";
  }
}

async function saveBrowser() {
  const saved = await post("/api/settings/browser", {
    profile: $("browser-profile").value.trim() || "default",
    channel: $("browser-channel").value,
    headless: $("browser-headless").checked,
  });
  state.savedBrowser = false;
  toast(`browser settings saved · ${saved.channel} / ${saved.profile}`);
  await tick();
}

async function saveGeneral() {
  const mode = (document.querySelector('input[name="first-message-mode"]:checked') || {}).value || "bot";
  const saved = await post("/api/settings/general", {
    first_message_mode: mode,
    reply_delay_ms: num("reply-delay-ms"),
    poll_interval_ms: num("poll-interval-ms"),
    poll_min_seconds: num("poll-min"),
    poll_max_seconds: num("poll-max"),
    action_min_seconds: num("action-min"),
    action_max_seconds: num("action-max"),
  });
  toast(`settings saved · first message = ${saved.first_message_mode}`);
  $("general-saved").textContent = "saved " + new Date().toLocaleTimeString();
  state.savedGeneral = false;
  await tick();
}

async function saveMessage() {
  // Only the usernames are editable here; chat_mode / sequence stay for the API.
  await post("/api/settings/message", { usernames: $("message-usernames").value });
  toast("usernames saved");
  state.savedMessage = false;
  await tick();
}

async function startShowMode() {
  const count = Number($("browser-count").value) || 1;
  const result = await post("/api/start", { count });
  if (result && result.ok === false) throw new Error(result.error || "could not start");
  toast("Show Mode starting: log in inside the new browser window");
  await tick();
}

async function stopShowMode() {
  await post("/api/stop", {});
  toast("stopping after the current step");
  await tick();
}

// The server refuses request bodies above 1 MB, so a huge folder is sent by
// path over the API instead of by contents from the browser.
const MAX_UPLOAD_BYTES = 1000000;

function pickedSequenceFiles(fileList) {
  return Array.from(fileList || []).filter((file) => {
    const relative = file.webkitRelativePath || file.name;
    return relative.split(/[\\/]/).length <= 2 && file.name.toLowerCase().endsWith(".txt");
  });
}

async function loadFolderFromPicker(fileList) {
  const picked = Array.from(fileList || []);
  if (!picked.length) return;
  const wanted = pickedSequenceFiles(picked);
  if (!wanted.length) throw new Error("no valid .txt files found in the selected folder");
  const total = wanted.reduce((sum, file) => sum + file.size, 0);
  if (total > MAX_UPLOAD_BYTES) {
    throw new Error(
      `folder too large for the browser upload (${Math.round(total / 1024)} KB) - use the API with a path`,
    );
  }
  const files = [];
  for (const file of wanted) files.push({ name: file.name, text: await file.text() });
  const result = await post("/api/folder/files", { files });
  if (result && result.ok === false) throw new Error(result.error || "could not load the folder");
  const skipped = (picked.length - wanted.length) + (result.skipped || 0);
  toast(`loaded ${result.count} sequence file(s)` + (skipped ? ` · ${skipped} skipped` : ""));
  state.savedMessage = false;
  await tick();
}

function tokenText() {
  return TOKEN_READY ? API_TOKEN : "(this page has no API token - open the dashboard from run.bat)";
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(tokenText());
    toast("API token copied to the clipboard");
  } catch (_) {
    toast("clipboard blocked by the browser; use the show button", true);
  }
}

function revealToken() {
  const node = $("token-value");
  const hidden = node.dataset.shown !== "1";
  node.dataset.shown = hidden ? "1" : "0";
  node.textContent = hidden ? API_TOKEN || "(no token)" : "•".repeat(8);
  $("token-reveal").textContent = hidden ? "hide" : "show";
}

async function copyLogs() {
  const text = (state.snapshot && state.snapshot.logs ? state.snapshot.logs : [])
    .map((entry) => `${entry.timestamp} ${entry.level} ${entry.message}`)
    .join("\n");
  try {
    await navigator.clipboard.writeText(text);
    toast("logs copied to the clipboard");
  } catch (_) {
    toast("clipboard blocked by the browser; select the text manually", true);
  }
}

/* -------------------------------------------------------------------- tabs */
function showTab(name) {
  state.tab = name;
  document.querySelectorAll(".tab").forEach((tab) => tab.classList.toggle("active", tab.dataset.tab === name));
  document.querySelectorAll(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === "panel-" + name));
}

/* ------------------------------------------------------------------- wiring */
function wire() {
  document.querySelectorAll(".tab").forEach((tab) =>
    tab.addEventListener("click", () => showTab(tab.dataset.tab)),
  );
  $("motion-toggle").addEventListener("click", rainToggle);
  $("run-btn").addEventListener("click", () =>
    startShowMode().catch((error) => toast(error.message, true)),
  );
  $("stop-btn").addEventListener("click", () =>
    stopShowMode().catch((error) => toast(error.message, true)),
  );
  $("proxy-save").addEventListener("click", (event) => {
    event.preventDefault();
    saveProxy().catch((error) => toast(error.message, true));
  });
  $("proxy-test").addEventListener("click", () => testProxy());
  $("proxy-enabled").addEventListener("change", () => {
    state.savedProxy = true;
    updateProxyCount();
  });
  $("proxy-entries").addEventListener("input", () => {
    state.savedProxy = true;
    updateProxyCount();
  });
  $("proxy-default-scheme").addEventListener("change", updateProxyCount);
  $("browser-save").addEventListener("click", (event) => {
    event.preventDefault();
    saveBrowser().catch((error) => toast(error.message, true));
  });
  $("general-save").addEventListener("click", (event) => {
    event.preventDefault();
    saveGeneral().catch((error) => toast(error.message, true));
  });
  $("message-save").addEventListener("click", (event) => {
    event.preventDefault();
    saveMessage().catch((error) => toast(error.message, true));
  });
  $("folder-browse").addEventListener("click", () => $("folder-input").click());
  $("folder-input").addEventListener("change", (event) => {
    const input = event.target;
    loadFolderFromPicker(input.files)
      .catch((error) => toast(error.message, true))
      .finally(() => {
        input.value = ""; // pick the same folder twice and it still fires
      });
  });
  $("token-copy").addEventListener("click", () => copyToken());
  $("token-reveal").addEventListener("click", () => revealToken());
  $("chat-sessions").addEventListener("change", (event) => {
    state.chatSelected = event.target.value;
    tick();
  });
  $("log-search").addEventListener("input", (event) => {
    state.logSearch = event.target.value.trim().toLowerCase();
    if (state.snapshot) renderLogs(state.snapshot);
  });
  document.querySelectorAll("#log-levels .chip").forEach((chip) =>
    chip.addEventListener("click", () => {
      const level = chip.dataset.level;
      if (state.logLevels.has(level)) state.logLevels.delete(level);
      else state.logLevels.add(level);
      chip.classList.toggle("on", state.logLevels.has(level));
      if (state.snapshot) renderLogs(state.snapshot);
    }),
  );
  $("log-follow").addEventListener("change", (event) => {
    state.follow = event.target.checked;
  });
  $("log-copy").addEventListener("click", () => copyLogs());
  $("log-refresh").addEventListener("click", () => tick());
  $("log-clear").addEventListener("click", async () => {
    try {
      await post("/api/logs/clear", {});
      toast("logs cleared");
      await tick();
    } catch (error) {
      toast(error.message, true);
    }
  });
  // Local edit guards: never overwrite what the operator is typing.
  ["browser-profile", "browser-channel", "browser-headless"].forEach((id) =>
    $(id).addEventListener("input", () => {
      state.savedBrowser = true;
    }),
  );
  [
    "reply-delay-ms",
    "poll-interval-ms",
    "poll-min",
    "poll-max",
    "action-min",
    "action-max",
    "first-message-mode",
  ].forEach((id) => {
    document.querySelectorAll(`#${id}, input[name="first-message-mode"]`).forEach((node) =>
      node.addEventListener("input", () => {
        state.savedGeneral = true;
      }),
    );
  });
  ["message-usernames"].forEach((id) =>
    $(id).addEventListener("input", () => {
      state.savedMessage = true;
    }),
  );
  document.addEventListener("keydown", (event) => {
    if (event.target instanceof Element && event.target.matches("input, textarea, select")) return;
    const tabs = ["browser", "proxy", "settings", "message", "chat", "logs"];
    if (event.key >= "1" && event.key <= "6") showTab(tabs[Number(event.key) - 1]);
    if (event.key === "r") $("run-btn").disabled || startShowMode().catch(() => {});
    if (event.key === "s") stopShowMode().catch(() => {});
  });
  window.addEventListener("resize", rainSizing);
}

/* The guided flow is optional: if it cannot start, the console must still be
 * reachable. A blank page is never an acceptable failure mode. */
function startFlow() {
  try {
    if (!window.Flow) return false;
    Flow.init({ post, toast });
    return true;
  } catch (error) {
    return false;
  }
}

function showConsole(reason) {
  $("console").hidden = false;
  $("flow").hidden = true;
  document.body.dataset.flow = "fallback";
  if (reason) {
    stickyBanner = reason;
    banner(reason);
  }
}

document.addEventListener("flow:unlocked", () => {
  // The guided flow is over: normalise the tabs (one active panel) and show
  // the console as it was, so every option behaves exactly like before.
  showTab(state.tab || "browser");
});

document.addEventListener("DOMContentLoaded", async () => {
  wire();
  if (!startFlow()) {
    showConsole(
      "The guided flow could not start (ui/flow.js is missing or failed) - " +
        "showing the full dashboard instead.",
    );
  }
  rainStart();
  if (!TOKEN_READY) {
    showConsole(
      "This page was opened without the server token (file:// or a stale tab). " +
        "Start the dashboard with run.bat or run-window.bat and use the URL printed there.",
    );
    return;
  }
  await tick(true);
  setInterval(() => tick(), 1000);
});
