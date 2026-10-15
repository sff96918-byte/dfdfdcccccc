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
  tab: "overview",
  // true while the saved sequence uses %username% / {snap} with no usernames:
  // the topbar alert shows it and RUN refuses to start a doomed run.
  needsUsernames: false,
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
  // Overview / operations
  diag: null,
  workers: 0,
  proxyRows: [],
  proxyValidateTimer: null,
  chatSearch: "",
  chatFollow: true,
  theme: "theme",           // "theme" (matrix glow) or "plain" (flat)
  savedProxyValid: false,
  helpShown: false,
};

/* ------------------------------------------------------------------ helpers */
const $ = (id) => document.getElementById(id);
const esc = (value) =>
  String(value == null ? "" : value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
// One rendered message can hold several lines: the dashboard counts lines, not
// bubbles, so a 3-line partner message never looks like a single "message".
const msgLines = (text) =>
  String(text == null ? "" : text)
    .replace(/\r/g, "")
    .split("\n")
    .filter((line) => line.trim()).length || 1;

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
  // START gate (Message -> Proxy -> Setting -> START -> Browser/RUN).
  const flow = snapshot.flow || {};
  const started = !!flow.started;
  $("top-start").hidden = started;
  $("top-start").disabled = !flow.ready_to_start;
  $("top-start").title = flow.ready_to_start ? "open the Browser step (RUN)" : "finish " + (flow.missing || []).join(", ").toUpperCase() + " first";
  $("top-stop").hidden = !started;
  // The Browser tab (RUN) exists only after START.
  document.body.dataset.started = started ? "yes" : "no";
  const browserTab = document.querySelector('.tab[data-tab="browser"]');
  if (browserTab) browserTab.hidden = !started;
  if (!started && state.tab === "browser") showTab("message");
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
  const counts = logLevelCounts(snapshot);
  document.querySelectorAll("#log-levels .chip").forEach((chip) => {
    const level = chip.dataset.level;
    const count = counts[level] || 0;
    chip.textContent = `${level} ${count}`;
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
  const all = chat.messages || [];
  const messages = chatRows();
  const box = $("chat-messages");
  const near = box.scrollHeight - box.scrollTop - box.clientHeight < 80;
  const messagesSignature = `${chat.selected}|${all.length}|${state.chatSearch}|${all.length ? all[all.length - 1].id : ""}`;
  if (box.dataset.signature !== messagesSignature) {
    box.dataset.signature = messagesSignature;
    box.innerHTML = messages.length
      ? messages
          .map(
            (row) => {
              const lines = msgLines(row.text);
              return (
                `<div class="msg ${row.role === "bot" ? "bot" : "user"}">` +
                `<span class="who">${row.role === "bot" ? "BOT ▸" : "USER ◂"}` +
                `<span class="dim"> ${lines} ln</span></span>` +
                `<span class="txt">${esc(row.text)}</span></div>`
              );
            },
          )
          .join("")
      : all.length
        ? '<div class="empty">no message matches the filter</div>'
        : '<div class="empty">nothing observed yet — run Show Mode and log in inside the browser window</div>';
  }
  if (near && state.chatFollow) box.scrollTop = box.scrollHeight;
  const stats = chat.stats || {};
  $("chat-meta").textContent = all.length
    ? `USER ${stats.user_lines || 0} lines / ${stats.user_bubbles || 0} bubbles · ` +
      `BOT ${stats.bot_lines || 0} lines / ${stats.bot_bubbles || 0} bubbles · ` +
      `${messages.length}/${all.length} shown · evicted ${chat.evicted || 0}`
    : "";
  if (select.value) state.chatSelected = select.value;
}

function render(snapshot) {
  state.snapshot = snapshot;
  if (window.Flow) Flow.observe(snapshot);
  renderRunState(snapshot);
  renderOverview(snapshot);
  renderStatusStrip(snapshot);
  if (!state.savedBrowser) fillBrowser(snapshot);
  fillProxy(snapshot);
  if (!state.savedGeneral) fillGeneral(snapshot);
  if (!state.savedMessage) fillMessage(snapshot);
  renderLogs(snapshot);
  renderChat(snapshot);
}

/* ------------------------------------------------------- confirm / help UI */
let dialogResolve = null;

function closeDialog(value) {
  $("dialog").hidden = true;
  const resolve = dialogResolve;
  dialogResolve = null;
  if (resolve) resolve(!!value);
}

/* One in-app dialog for confirmations and help. Destructive buttons go through
 * this instead of window.confirm so the wording can explain the consequence. */
function askConfirm({ title, body, ok = "OK", cancel = "CANCEL", danger = false }) {
  $("dialog-title").textContent = title;
  $("dialog-body").innerHTML = body;
  $("dialog-ok").textContent = ok;
  $("dialog-ok").className = "btn " + (danger ? "danger" : "primary");
  $("dialog-cancel").textContent = cancel;
  $("dialog-cancel").hidden = cancel === "";
  $("dialog").hidden = false;
  $("dialog-ok").focus();
  return new Promise((resolve) => {
    dialogResolve = resolve;
  });
}

const SHORTCUTS = [
  ["1 … 7", "Overview, Browser, Proxy, Settings, Message, Chat, Logs"],
  ["r", "RUN (only when the Browser step is open)"],
  ["s", "STOP every browser"],
  ["x", "start / stop the guided setup rail"],
  ["t", "switch the theme (glow / plain)"],
  ["h / n / b / Esc", "guided flow: hold, next, back, skip to the console"],
  ["?", "this help"],
];

function showHelp() {
  const rows = SHORTCUTS.map(([key, what]) => `<tr><td><code>${esc(key)}</code></td><td>${esc(what)}</td></tr>`).join("");
  askConfirm({
    title: "How to drive this dashboard",
    body:
      `<p class="hint">The normal order: <b>MESSAGE</b> → <b>PROXY</b> → <b>SETTING</b> → <b>START</b> → ` +
      `<b>RUN</b> → log in inside the browser window. The Overview tab then shows every browser, ` +
      `the counters and the one next action.</p><table class="table help-table">${rows}</table>`,
    ok: "GOT IT",
    cancel: "",
  });
}

/* ------------------------------------------------------------------ theme */
function applyTheme() {
  document.body.dataset.theme = state.theme;
  if (state.theme === "plain") {
    $("rain").style.display = "none";
    state.rain.on = false;
    if (state.rain.timer) clearInterval(state.rain.timer);
    state.rain.timer = null;
  }
  $("theme-toggle").textContent = state.theme === "plain" ? "◑ PLAIN" : "◐ THEME";
  try {
    localStorage.setItem("chitchat-theme", state.theme);
  } catch (_) {
    /* private mode: the theme just is not remembered */
  }
}

function toggleTheme() {
  state.theme = state.theme === "plain" ? "theme" : "plain";
  applyTheme();
  toast(state.theme === "plain" ? "plain theme" : "glow theme");
}

/* -------------------------------------------------------------- overview */
function workerStateChip(worker) {
  const stateName = String(worker.state || "starting").toLowerCase();
  const chip = `<span class="worker-state ${stateName.replace("waiting-login", "waiting")}">${esc(stateName.toUpperCase().replace("WAITING-LOGIN", "WAIT LOGIN"))}</span>`;
  // A runner that stopped safely (unconfirmed send, missing identity, engine
  // failure) is NOT busy: say so instead of showing RUNNING forever.
  if (worker.paused) {
    const reason = worker.activity_note || worker.last_activity || "";
    return `<span class="worker-state error" title="${esc(reason)}">PAUSED</span>`;
  }
  const activity = String(worker.activity || "").toLowerCase();
  if (stateName !== "running" || !activity) return chip;
  return (
    chip +
    ` <span class="worker-state activity ${esc(activity)}" title="${esc(worker.activity_note || "")}">${esc(
      activity === "waiting" ? "DOM WAIT" : activity.toUpperCase(),
    )}</span>`
  );
}

function renderWorkers(snapshot) {
  const workers = snapshot.workers || [];
  const rows = $("worker-rows");
  $("worker-empty").hidden = workers.length > 0;
  const signature = workers
    .map((w) =>
      [
        w.index, w.state, w.profile, w.channel, w.proxy, w.chats, w.sent, w.incoming,
        w.user_lines, w.bot_lines, w.activity, w.activity_note, w.paused,
        w.partner, w.phase, w.note,
      ].join("|"),
    )
    .join(";");
  if (rows.dataset.signature === signature) return;
  rows.dataset.signature = signature;
  rows.innerHTML = workers
    .map((worker) => {
      const closable = !["done", "error"].includes(String(worker.state || ""));
      const chat = worker.partner
        ? `${esc(worker.partner)}${worker.phase && worker.phase !== "chatting" ? ` <span class="dim">(${esc(worker.phase)})</span>` : ""}`
        : '<span class="dim">—</span>';
      const activity = worker.last_activity || worker.note || "";
      return (
        `<tr>` +
        `<td>${esc(worker.index)}/${esc(worker.total || 1)}</td>` +
        `<td>${esc(worker.profile || "default")}</td>` +
        `<td>${esc(worker.channel || "auto")}</td>` +
        `<td>${esc(worker.proxy || "direct")}</td>` +
        `<td>${workerStateChip(worker)}</td>` +
        `<td>${chat}</td>` +
        `<td>✉ ${esc(worker.bot_lines || worker.sent || 0)} <span class="dim">/ ◂ ${esc(
          worker.user_lines || 0,
        )} · ${esc(worker.incoming || 0)} in</span></td>` +
        `<td class="cell-activity" title="${esc(activity)}">${esc(activity)}</td>` +
        `<td>${closable ? `<button type="button" class="btn tiny danger" data-close="${esc(worker.index)}">CLOSE</button>` : ""}</td>` +
        `</tr>`
      );
    })
    .join("");
  rows.querySelectorAll("button[data-close]").forEach((button) =>
    button.addEventListener("click", () => closeWorker(Number(button.dataset.close))),
  );
}

function renderOverview(snapshot) {
  const overview = snapshot.overview || {};
  const kpis = [
    ["STATE", String(overview.status || "idle").toUpperCase()],
    ["BROWSERS", `${overview.workers_active || 0} active / ${overview.workers_total || 0}`],
    ["WAITING LOGIN", String(overview.workers_waiting_login || 0)],
    ["MESSAGES SENT", String(overview.messages_sent || 0)],
    ["INCOMING", String(overview.messages_incoming || 0)],
    // Lines, not bubbles: a 3-line partner message is one bubble and 3 lines.
    [
      "LINES (BOT / USER)",
      `${(overview.lines || {}).bot || 0} / ${(overview.lines || {}).user || 0}`,
    ],
    ["CHATS", String(overview.chats || 0)],
    ["MESSAGE MODE", String((overview.message || {}).mode || "single").replace("eva_chat_flow", "evachat")],
    ["FIRST MESSAGE", String((overview.general || {}).first_message_mode || "bot")],
  ];
  $("overview-kpis").innerHTML = kpis
    .map(([key, value]) => `<div class="kpi"><b>${esc(key)}</b><span>${esc(value)}</span></div>`)
    .join("");

  const next = overview.next_action || {};
  const nextNode = $("overview-next");
  nextNode.textContent = next.text || "—";
  nextNode.dataset.tab = next.tab || "";

  const preview = (overview.proxy || {}).preview || [];
  $("overview-proxy").innerHTML = (overview.proxy || {}).enabled
    ? preview.length
      ? preview.map((row) => `<li>browser ${esc(row.index)} → <code>${esc(row.proxy)}</code></li>`).join("")
      : '<li class="warn">proxy is enabled but the list has no valid line</li>'
    : '<li class="dim">proxy is off — browsers go direct</li>';

  renderWorkers(snapshot);
}

function renderStatusStrip(snapshot) {
  const overview = snapshot.overview || {};
  $("ss-state").textContent = String(overview.status || "idle").toUpperCase();
  $("ss-workers").textContent = `${overview.workers_active || 0} active / ${overview.workers_total || 0}`;
  $("ss-sent").textContent = String(overview.messages_sent || 0);
  $("ss-incoming").textContent = String(overview.messages_incoming || 0);
  const lines = overview.lines || {};
  $("ss-lines").textContent = `✉ ${lines.bot || 0} · ◂ ${lines.user || 0}`;
  const proxy = overview.proxy || {};
  $("ss-proxy").textContent = proxy.enabled
    ? proxy.error
      ? "invalid list"
      : `${proxy.lines || 0} line(s)`
    : "off";
  $("ss-delay").textContent = (overview.general || {}).reply_delay || "—";
  const next = overview.next_action || {};
  $("ss-next").textContent = next.text ? "NEXT ▸ " + next.text : "NEXT ▸ —";
  $("ss-next").dataset.tab = next.tab || "";
  $("stat-workers").textContent = `◱ ${overview.workers_active || 0}/${overview.workers_total || 0}`;
  $("stat-sent").textContent = `✉ ${overview.messages_sent || 0}`;
}

async function closeWorker(index) {
  const worker = (state.snapshot && state.snapshot.workers || []).find((item) => item.index === index);
  const ok = await askConfirm({
    title: `Close browser ${index}?`,
    body: `<p>Only browser <b>${esc(index)}</b> (${esc((worker || {}).profile || "default")}) stops after its current step. ` +
      `The other browsers keep running.</p>`,
    ok: "CLOSE BROWSER",
    danger: true,
  });
  if (!ok) return;
  try {
    const answer = await post("/api/workers/close", { index });
    if (answer && answer.ok === false) throw new Error(answer.error || "could not close the browser");
    toast(`closing browser ${index}`);
    await tick();
  } catch (error) {
    toast(error.message, true);
  }
}

async function runDiagnostics() {
  const button = $("ov-diag");
  button.disabled = true;
  button.textContent = "CHECKING…";
  $("ov-diag-summary").textContent = "checking python, playwright, browsers, folders, logs…";
  try {
    const report = await api("/api/diagnostics");
    state.diag = report;
    const rows = (report.checks || [])
      .map(
        (check) =>
          `<div class="diag-row ${String(check.status).toLowerCase()}">` +
          `<span class="diag-status">${esc(check.status)}</span>` +
          `<span class="diag-label">${esc(check.label)}</span>` +
          `<span class="diag-detail">${esc(check.detail)}</span></div>`,
      )
      .join("");
    $("ov-diag-results").innerHTML = rows;
    $("ov-diag-results").hidden = false;
    $("ov-diag-copy").hidden = false;
    const summary = report.summary || {};
    $("ov-diag-summary").textContent =
      `${summary.PASS || 0} pass · ${summary.FAIL || 0} fail · ${summary.WARN || 0} warn · ${summary.BLOCKED || 0} blocked`;
    toast(report.ok ? "system check passed" : "system check found problems", !report.ok);
  } catch (error) {
    $("ov-diag-summary").textContent = "check failed: " + error.message;
    toast(error.message, true);
  } finally {
    button.disabled = false;
    button.textContent = "CHECK SYSTEM";
  }
}

function copyDiagnostics() {
  if (!state.diag) return;
  const lines = (state.diag.checks || []).map((check) => `[${check.status}] ${check.label}: ${check.detail}`);
  const summary = state.diag.summary || {};
  lines.push("");
  lines.push(`app ${(state.diag.system || {}).app} · python ${(state.diag.system || {}).python}`);
  lines.push(`PASS ${summary.PASS || 0} · FAIL ${summary.FAIL || 0} · WARN ${summary.WARN || 0} · BLOCKED ${summary.BLOCKED || 0}`);
  copyText(lines.join("\n"), "diagnostics copied");
}

/* ------------------------------------------------------------------- chat */
function chatRows() {
  const chat = (state.snapshot && state.snapshot.chat) || {};
  const filter = state.chatSearch.trim().toLowerCase();
  return (chat.messages || []).filter((row) => !filter || String(row.text || "").toLowerCase().includes(filter));
}

function chatAsText() {
  const chat = (state.snapshot && state.snapshot.chat) || {};
  const session = (chat.sessions || []).find((item) => item.id === chat.selected) || {};
  const lines = [`# ${session.label || "(no chat)"}`, `# ${new Date().toISOString()}`];
  chatRows().forEach((row) => {
    lines.push(`[${row.role === "bot" ? "BOT" : "USER"}] ${row.text}`);
  });
  return lines.join("\n");
}

function copyChatSession() {
  copyText(chatAsText(), "chat copied");
}

function exportChatSession() {
  const chat = (state.snapshot && state.snapshot.chat) || {};
  const name = "chitchat-" + String(chat.selected || "chat").replace(/[^\w.-]+/g, "_") + ".txt";
  downloadText(name, chatAsText(), "text/plain");
}

/* ------------------------------------------------------------------- logs */
function logLevelCounts(snapshot) {
  const counts = { ACTIVITY: 0, INFO: 0, WARN: 0, ERROR: 0 };
  (snapshot.logs || []).forEach((entry) => {
    const level = String(entry.level || "").toUpperCase();
    if (level in counts) counts[level] += 1;
  });
  return counts;
}

async function downloadLogsFile() {
  try {
    const response = await fetch("/api/logs/file", { headers: { "X-API-Token": API_TOKEN } });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "port.log";
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
    toast("port.log downloaded");
  } catch (error) {
    toast("download failed: " + error.message, true);
  }
}

/* -------------------------------------------------------------- utilities */
function copyText(text, message) {
  const done = () => toast(message || "copied");
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done, () => fallbackCopy(text, done));
    return;
  }
  fallbackCopy(text, done);
}

function fallbackCopy(text, done) {
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  try {
    document.execCommand("copy");
    done();
  } catch (_) {
    toast("clipboard not available", true);
  }
  area.remove();
}

function downloadText(name, text, type) {
  const blob = new Blob([text], { type: (type || "text/plain") + ";charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}

/* --------------------------------------------------------- form read/write */
function fillBrowser(snapshot) {
  const browser = snapshot.browser || {};
  $("browser-profile").value = browser.profile || "default";
  $("browser-channel").value = browser.channel || "auto";
  // NOTE: the stored "headless" flag is a Go-worker-engine field. Show Mode
  // always opens a visible window, so the dashboard neither shows nor edits it;
  // it stays in data/state/browser.json untouched.
}

function fillGeneral(snapshot) {
  const general = snapshot.general || {};
  const mode = general.first_message_mode || "bot";
  document
    .querySelectorAll('input[name="first-message-mode"]')
    .forEach((input) => (input.checked = input.value === mode));
  // Poll interval is backend-only (data/state/general.json); the dashboard
  // shows the two waits the operator actually tunes, each as a min-max range.
  // Reply delay is the Go field pair (seconds), so a data folder written here
  // and one written by the Go app mean the same thing.
  $("reply-delay-min-s").value = general.reply_delay_min_seconds ?? 5;
  $("reply-delay-max-s").value = general.reply_delay_max_seconds ?? 10;
  $("skip-no-reply-min-s").value = msToSeconds(general.skip_if_no_reply_ms ?? 60000);
  $("skip-no-reply-max-s").value = msToSeconds(general.skip_if_no_reply_max_ms ?? 90000);
  // Page scan interval is the one backend timing the runner really uses (Go's
  // pollInterval); the Go worker-engine timings are not shown, because this port
  // never reads them.
  $("poll-interval-ms").value = general.poll_interval_ms ?? 350;
  $("answer-all-burst").checked = Boolean(general.first_message_answer_all);
  // Only the user-writes-first modes can answer an opening burst.
  $("answer-all-field").hidden = mode === "bot";
  // A silence limit below the reply delay abandons chats before the bot answers:
  // flag it where the operator typed it, exactly like the save log does.
  const skipMax = msToSeconds(general.skip_if_no_reply_max_ms ?? 90000);
  const delayMax = Number(general.reply_delay_max_seconds ?? 10) || 0;
  $("general-hint").classList.toggle(
    "warn",
    Boolean(general.skip_if_no_reply_ms) && skipMax < delayMax,
  );
  $("general-hint").textContent =
    Boolean(general.skip_if_no_reply_ms) && skipMax < delayMax
      ? `Skip if no reply (max ${skipMax}s) is shorter than Reply delay max (${delayMax}s): the bot ` +
        "leaves chats before its own reply is due. Use 0 to never leave a quiet partner."
      : "Skip if no reply is the only wait that ends a chat: keep it above Reply delay max " +
        "(Go default 60-90s) or use 0 to never leave a quiet partner.";
}

function fillMessage(snapshot) {
  const message = snapshot.message || {};
  $("message-usernames").value = message.usernames || "";
  const loaded = message.loaded_count || 0;
  $("folder-summary").textContent = loaded
    ? `${loaded} file(s) loaded`
    : "no folder loaded";

  // mode + single sequence are editable now; the values come from the server.
  $("chat-mode").value = message.chat_mode === "folder" ? "folder" : "single";
  $("message-sequence").value = message.sequence || "";
  const sequenceLines = message.sequence_lines || 0;
  $("sequence-hint").textContent = sequenceLines
    ? `${sequenceLines} line(s) — the bot sends them in order, one per message`
    : "empty: a Single-mode chat would have nothing to send";
  const files = message.loaded_files || [];
  $("folder-list").innerHTML = files.length
    ? files
        .map(
          (file) =>
            `<span class="file-chip" title="${esc(file.preview)}">#${esc(file.index)} · ${esc(file.lines)} line(s)` +
            `${file.preview ? " · " + esc(file.preview.slice(0, 24)) : ""}</span>`,
        )
        .join(" ")
    : '<span class="dim">no folder loaded — the single sequence is used</span>';
  $("folder-clear").hidden = files.length === 0;
  $("mode-hint").textContent =
    message.chat_mode === "folder"
      ? loaded
        ? "each chat picks one loaded file"
        : "no file loaded yet — load a folder first"
      : "every chat uses the single sequence";
  // The folder block lives in the mode row, so it says out loud what it is for
  // right now: highlighted while Folder mode has nothing loaded.
  const folderMode = message.chat_mode === "folder";
  $("folder-field").classList.toggle("needs", folderMode && loaded === 0);
  const folderNote = $("folder-note");
  folderNote.hidden = !(folderMode && loaded === 0);
  if (!folderNote.hidden) {
    folderNote.textContent =
      "Folder mode is selected but no .txt is loaded: press BROWSE FOLDER, type the folder path, or switch back to Single.";
  }
  const note = $("message-note");
  note.hidden = !message.needs_usernames;
  if (message.needs_usernames) {
    note.textContent =
      "This sequence uses %username% / {snap} but no username is saved: Show Mode will refuse to reply and RUN stays blocked.";
  }
  // Go and this port both refuse to reply while a %username% line has no
  // username, so say it here instead of letting Show Mode fail at reply time.
  const warning = $("username-warning");
  $("usernames-field").classList.toggle("needs", !!message.needs_usernames);
  // Keep the missing-username state on the app, not only on the MESSAGE panel:
  // the topbar alert and the RUN guard below both read it.
  state.needsUsernames = !!message.needs_usernames;
  $("username-alert").hidden = !state.needsUsernames;
  warning.hidden = !message.needs_usernames;
  if (message.needs_usernames) {
    warning.textContent =
      "your sequence uses %username% / {snap} but no usernames are set - " +
      "add one per line above and SAVE, otherwise Show Mode fails with " +
      "\"REPLY ENGINE FAILED | Show Mode sequence needs a Message \u2192 Usernames entry\"";
  }
}

// The dashboard works in seconds because that is what an operator can judge;
// the settings file keeps Go's milliseconds, so only the display converts.
const msToSeconds = (value) => Math.round(Number(value) || 0) / 1000;

const secondsToMs = (id, fallback) => {
  const value = Number($(id).value);
  return Number.isFinite(value) ? Math.max(0, Math.round(value * 1000)) : fallback;
};

// Go stores whole seconds, so the dashboard sends whole seconds too.
const wholeSeconds = (id, fallback) => {
  const value = Number($(id).value);
  return Number.isFinite(value) ? Math.max(0, Math.round(value)) : fallback;
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
    footer(`connected · ${location.host}`);
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
function proxyDefaultScheme() {
  return $("proxy-default-scheme").value === "socks5" ? "socks5" : "http";
}

/* The server applies the default protocol to plain lines (like Go), so both
 * TEST and SAVE send the raw text plus the chosen protocol. */
function proxyInputLines() {
  return $("proxy-entries").value;
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
    default_scheme: proxyDefaultScheme(),
    enabled: $("proxy-enabled").checked,
    entries: proxyInputLines(),
  });
  state.savedProxy = false;
  updateProxyCount();
  toast(saved.enabled ? `proxy saved · ${saved.entries.split(/\r?\n/).filter((l) => l.trim()).length} line(s)` : "proxy disabled");
  await tick();
}

function proxyResultRow(row) {
  const kind = row.ok ? "pass" : row.tunnel_ok ? "warn" : "fail";
  const title = row.ok ? "PASS" : row.tunnel_ok ? "REACHABLE" : "FAIL";
  const notes = [];
  if (row.detail) notes.push(esc(row.detail));
  if (row.provider_message) notes.push(`Provider said: “${esc(row.provider_message)}”`);
  if (row.proxy_authenticate) notes.push(`Auth challenge: ${esc(row.proxy_authenticate)}`);
  if (row.hint) notes.push(`Next step: ${esc(row.hint)}`);
  return (
    `<tr class="proxy-row ${kind}">` +
    `<td>${esc(row.line)}</td>` +
    `<td><span class="diag-status ${kind}">${esc(title)}</span></td>` +
    `<td><code>${esc(row.label || "invalid entry")}</code></td>` +
    `<td>${row.latency_ms == null ? "—" : esc(row.latency_ms) + " ms"}</td>` +
    `<td>${row.status ? "HTTP " + esc(row.status) : "—"}</td>` +
    `<td>${row.authenticated ? "auth" : "no auth"}</td>` +
    `<td class="cell-activity">${notes.join("<br>")}</td>` +
    `</tr>`
  );
}

function renderProxyResults(rows, { tested = true } = {}) {
  state.proxyRows = rows || [];
  const box = $("proxy-results");
  if (!state.proxyRows.length) {
    box.innerHTML = tested ? '<div class="empty">no lines to test</div>' : "";
    $("proxy-summary").hidden = true;
    $("proxy-copy").hidden = true;
    $("proxy-prune").hidden = true;
    return;
  }
  const pass = state.proxyRows.filter((row) => row.ok).length;
  const reach = state.proxyRows.filter((row) => !row.ok && row.tunnel_ok).length;
  const fail = state.proxyRows.length - pass - reach;
  box.innerHTML =
    '<table class="table proxy-table"><thead><tr>' +
    "<th>LINE</th><th>RESULT</th><th>PROXY</th><th>LATENCY</th><th>HTTP</th><th>AUTH</th><th>DETAILS</th>" +
    "</tr></thead><tbody>" +
    state.proxyRows.map(proxyResultRow).join("") +
    "</tbody></table>";
  $("proxy-summary").innerHTML =
    `<span class="chip on">${pass} PASS</span>` +
    `<span class="chip ${reach ? "on warn" : ""}">${reach} REACHABLE</span>` +
    `<span class="chip ${fail ? "on error" : ""}">${fail} FAIL</span>`;
  $("proxy-summary").hidden = false;
  $("proxy-copy").hidden = false;
  $("proxy-prune").hidden = fail === 0;
}

function copyProxyResults() {
  const lines = state.proxyRows.map((row) => {
    const title = row.ok ? "PASS" : row.tunnel_ok ? "REACHABLE" : "FAIL";
    return [
      `line ${row.line}`,
      title,
      row.label || "invalid entry",
      row.latency_ms == null ? "" : `${row.latency_ms} ms`,
      row.status ? `HTTP ${row.status}` : "",
      row.detail || "",
      row.provider_message ? `provider: ${row.provider_message}` : "",
      row.hint ? `next: ${row.hint}` : "",
    ]
      .filter(Boolean)
      .join(" | ");
  });
  copyText(lines.join("\n"), "proxy results copied");
}

async function pruneFailedProxies() {
  const failed = new Set(state.proxyRows.filter((row) => !row.ok && !row.tunnel_ok).map((row) => row.line));
  if (!failed.size) return;
  const ok = await askConfirm({
    title: `Remove ${failed.size} failed line(s)?`,
    body: "<p>The box keeps every line that passed or reached the destination; " +
      "the FAIL lines are deleted. Nothing is saved until you press SAVE.</p>",
    ok: "REMOVE",
    danger: true,
  });
  if (!ok) return;
  $("proxy-entries").value = $("proxy-entries")
    .value.split(/\r?\n/)
    .filter((line, index) => !(line.trim() && failed.has(index + 1)))
    .join("\n");
  state.savedProxy = true;
  updateProxyCount();
  renderProxyResults(state.proxyRows.filter((row) => !failed.has(row.line)));
}

/* Live validation while typing: the server parses the same text SAVE will use. */
function scheduleProxyValidation() {
  if (state.proxyValidateTimer) clearTimeout(state.proxyValidateTimer);
  state.proxyValidateTimer = setTimeout(validateProxyLive, 350);
}

async function validateProxyLive() {
  const raw = proxyInputLines();
  const note = $("proxy-validate");
  if (!raw.trim()) {
    note.textContent = "no proxy lines yet";
    note.className = "hint";
    return;
  }
  try {
    const answer = await post("/api/proxy/validate", { entries: raw, default_scheme: proxyDefaultScheme() });
    if (answer.ok === false && answer.error) {
      note.textContent = answer.error;
      note.className = "hint warn";
      return;
    }
    const bad = (answer.lines || []).filter((row) => !row.ok);
    note.className = bad.length ? "hint warn" : "hint";
    note.textContent = bad.length
      ? `${answer.valid} valid, ${bad.length} with a problem: ` +
        bad.map((row) => `line ${row.line} (${row.error})`).join("; ")
      : `${answer.valid} valid line(s), nothing saved yet`;
  } catch (error) {
    note.textContent = "validation failed: " + error.message;
    note.className = "hint warn";
  }
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
    const answer = await post("/api/proxy/test", { entries: raw, default_scheme: proxyDefaultScheme() });
    const rows = answer.results || [];
    let passed = 0;
    let reachable = 0;
    rows.forEach((row) => {
      if (row.ok) passed += 1;
      if (row.tunnel_ok) reachable += 1;
    });
    renderProxyResults(rows);
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
  // The payload carries only what this tab owns; the service merges it into the
  // stored settings, so the Go-only "headless" field survives unchanged.
  const saved = await post("/api/settings/browser", {
    profile: $("browser-profile").value.trim() || "default",
    channel: $("browser-channel").value,
  });
  state.savedBrowser = false;
  toast(`browser settings saved · ${saved.channel} / ${saved.profile}`);
  await tick();
}

async function saveGeneral() {
  const mode = (document.querySelector('input[name="first-message-mode"]:checked') || {}).value || "bot";
  // Only the fields this tab owns: the service merges the payload into the
  // stored settings, so the hidden Go-parity poll/action values survive.
  // Only the fields this page owns and the runner reads: the service merges the
  // payload, so the Go worker-engine timings stay in the file untouched.
  const saved = await post("/api/settings/general", {
    first_message_mode: mode,
    first_message_answer_all: $("answer-all-burst").checked,
    reply_delay_min_seconds: wholeSeconds("reply-delay-min-s", 5),
    reply_delay_max_seconds: wholeSeconds("reply-delay-max-s", 10),
    skip_if_no_reply_ms: secondsToMs("skip-no-reply-min-s", 60000),
    skip_if_no_reply_max_ms: secondsToMs("skip-no-reply-max-s", 90000),
    poll_interval_ms: wholeSeconds("poll-interval-ms", 350),
  });
  toast(`settings saved · first message = ${saved.first_message_mode}`);
  $("general-saved").textContent = "saved " + new Date().toLocaleTimeString();
  state.savedGeneral = false;
  await tick();
}

async function saveMessage() {
  const mode = $("chat-mode").value === "folder" ? "folder" : "single";
  if (mode === "folder" && !(state.snapshot && (state.snapshot.message || {}).loaded_count)) {
    toast("load a pattern folder before switching to Folder mode", true);
    return;
  }
  const saved = await post("/api/settings/message", {
    chat_mode: mode,
    sequence: $("message-sequence").value,
    usernames: $("message-usernames").value,
  });
  toast(`message settings saved · ${saved.chat_mode} · ${saved.sequence_lines} single line(s)`);
  $("message-saved").textContent = "saved " + new Date().toLocaleTimeString();
  state.savedMessage = false;
  await tick();
}

async function loadFolderPath() {
  const path = $("folder-path").value.trim();
  if (!path) {
    toast("type the folder path first", true);
    return;
  }
  try {
    const answer = await post("/api/folder", { path });
    if (answer.ok === false) throw new Error(answer.error || "could not load the folder");
    toast(`${answer.count || 0} file(s) loaded from the path`);
    state.savedMessage = false;
    await tick();
  } catch (error) {
    toast(error.message, true);
  }
}

async function clearFolder() {
  const ok = await askConfirm({
    title: "Clear the loaded folder?",
    body: "<p>The loaded pattern files are dropped and the single sequence is used again. " +
      "Your usernames and settings are kept.</p>",
    ok: "CLEAR FOLDER",
    danger: true,
  });
  if (!ok) return;
  try {
    await post("/api/folder/clear", {});
    toast("loaded folder cleared");
    state.savedMessage = false;
    await tick();
  } catch (error) {
    toast(error.message, true);
  }
}

async function startShowMode() {
  // Never start a run that is guaranteed to fail at reply time: Go and this
  // port both refuse a %username% line with no usernames. Warn, show the field
  // and stop - nothing is sent to the server.
  if (state.needsUsernames) {
    toast("usernames missing - your sequence uses %username%: add one in MESSAGE", true);
    revealUsernames();
    return;
  }
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

async function restartFlow() {
  // Back to the blank home with the step rail: the guided setup starts again.
  try {
    await post("/api/flow/reset", {});
    window.location.reload();
  } catch (error) {
    toast(error.message, true);
  }
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

// One file to attach to a bug report: settings (proxy passwords masked, no
// token, no usernames), flow state, crash file names and the last 300 logs.
async function downloadDebugBundle() {
  const bundle = await api("/api/debug");
  const stamp = (bundle.generated || new Date().toISOString()).replace(/[:T]/g, "-").slice(0, 19);
  const blob = new Blob([JSON.stringify(bundle, null, 1)], { type: "application/json" });
  const link = document.createElement("a");
  link.href = URL.createObjectURL(blob);
  link.download = `chitchat-debug-${stamp}.json`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(link.href), 2000);
  const crashes = (bundle.crash_files || []).length;
  toast(crashes ? `debug bundle saved - ${crashes} crash file(s) in data/logs too` : "debug bundle saved");
}

/* -------------------------------------------------------------------- tabs */
function showTab(name) {
  state.tab = name;
  document.querySelectorAll(".tab").forEach((tab) => tab.classList.toggle("active", tab.dataset.tab === name));
  document.querySelectorAll(".panel").forEach((panel) => panel.classList.toggle("active", panel.id === "panel-" + name));
}

// Take the operator to the missing username: the MESSAGE tab with the field
// focused and selected, so one paste and SAVE fixes it.
function revealUsernames() {
  showTab("message");
  const field = $("message-usernames");
  if (!field) return;
  if (field.scrollIntoView) field.scrollIntoView({ block: "center" });
  field.focus();
  if (field.select) field.select();
}

/* ------------------------------------------------------------------- wiring */
function wire() {
  document.querySelectorAll(".tab").forEach((tab) =>
    tab.addEventListener("click", () => showTab(tab.dataset.tab)),
  );
  $("motion-toggle").addEventListener("click", rainToggle);
  $("theme-toggle").addEventListener("click", toggleTheme);
  $("help-open").addEventListener("click", showHelp);
  $("username-alert").addEventListener("click", revealUsernames);

  // -- dialog -------------------------------------------------------------
  $("dialog-ok").addEventListener("click", () => closeDialog(true));
  $("dialog-cancel").addEventListener("click", () => closeDialog(false));
  $("dialog").addEventListener("click", (event) => {
    if (event.target === $("dialog")) closeDialog(false);
  });

  // -- overview -----------------------------------------------------------
  // RUN/STOP on the Overview tab must explain a refusal, never fail silently:
  // the server's gate error ("Finish these first: ...") goes to a toast.
  $("ov-run").addEventListener("click", () => startShowMode().catch((error) => toast(error.message, true)));
  $("ov-stop").addEventListener("click", () => stopShowMode().catch((error) => toast(error.message, true)));
  $("ov-diag").addEventListener("click", () => runDiagnostics());
  $("ov-diag-copy").addEventListener("click", copyDiagnostics);
  $("ss-next").addEventListener("click", () => {
    const tab = $("ss-next").dataset.tab;
    if (tab) showTab(tab);
  });
  $("overview-next").addEventListener("click", () => {
    const tab = $("overview-next").dataset.tab;
    if (tab) showTab(tab);
  });
  $("top-start").addEventListener("click", async () => {
    try {
      const result = await post("/api/flow/start", {});
      if (result && result.ok === false) throw new Error(result.error || "cannot start yet");
      toast("started - choose the browser count and press RUN");
      await tick();
      showTab("browser");
    } catch (error) {
      toast(error.message, true);
    }
  });
  $("top-stop").addEventListener("click", () =>
    stopShowMode().catch((error) => toast(error.message, true)),
  );
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
  $("proxy-copy").addEventListener("click", copyProxyResults);
  $("proxy-prune").addEventListener("click", () => pruneFailedProxies().catch((error) => toast(error.message, true)));
  $("proxy-enabled").addEventListener("change", () => {
    state.savedProxy = true;
    updateProxyCount();
  });
  $("proxy-entries").addEventListener("input", () => {
    state.savedProxy = true;
    updateProxyCount();
    scheduleProxyValidation();
  });
  $("proxy-default-scheme").addEventListener("change", () => {
    updateProxyCount();
    scheduleProxyValidation();
  });
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
  $("folder-path-load").addEventListener("click", () => loadFolderPath().catch((error) => toast(error.message, true)));
  $("folder-clear").addEventListener("click", () => clearFolder().catch((error) => toast(error.message, true)));
  $("chat-mode").addEventListener("change", () => {
    state.savedMessage = true;
    const folderMode = $("chat-mode").value === "folder";
    const loaded = (state.snapshot && (state.snapshot.message || {}).loaded_count) || 0;
    $("mode-hint").textContent = folderMode
      ? loaded
        ? "press SAVE MESSAGE SETTINGS to apply"
        : "pick a folder on the right, then SAVE"
      : "press SAVE MESSAGE SETTINGS to apply";
    $("folder-field").classList.toggle("needs", folderMode && loaded === 0);
    const note = $("folder-note");
    note.hidden = !(folderMode && loaded === 0);
    if (!note.hidden) {
      note.textContent =
        "Folder mode needs at least one .txt: press BROWSE FOLDER, type the folder path, or stay on Single.";
    }
  });
  $("message-sequence").addEventListener("input", () => {
    state.savedMessage = true;
    const lines = $("message-sequence").value.split(/\r?\n/).filter((line) => line.trim()).length;
    $("sequence-hint").textContent = lines
      ? `${lines} line(s) — the bot sends them in order, one per message`
      : "empty: a Single-mode chat would have nothing to send";
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
  $("flow-restart").addEventListener("click", () => restartFlow());
  $("token-copy").addEventListener("click", () => copyToken());
  $("token-reveal").addEventListener("click", () => revealToken());
  $("chat-sessions").addEventListener("change", (event) => {
    state.chatSelected = event.target.value;
    tick();
  });
  $("chat-search").addEventListener("input", (event) => {
    state.chatSearch = String(event.target.value || "").toLowerCase();
    renderChat(state.snapshot || {});
  });
  $("chat-follow").addEventListener("change", (event) => {
    state.chatFollow = !!event.target.checked;
  });
  $("chat-copy").addEventListener("click", copyChatSession);
  $("chat-export").addEventListener("click", exportChatSession);
  $("log-download").addEventListener("click", () => downloadLogsFile());
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
  $("log-debug").addEventListener("click", () =>
    downloadDebugBundle().catch((error) => toast(error.message, true)),
  );
  $("log-refresh").addEventListener("click", () => tick());
  $("log-clear").addEventListener("click", async () => {
    const ok = await askConfirm({
      title: "Clear the dashboard log?",
      body: "<p>This empties the Logs tab and its in-memory history. The file " +
        "<code>data/logs/port.log</code> keeps its lines (use DOWNLOAD .log first if you need them).</p>",
      ok: "CLEAR LOGS",
      danger: true,
    });
    if (!ok) return;
    try {
      await post("/api/logs/clear", {});
      toast("logs cleared");
      await tick();
    } catch (error) {
      toast(error.message, true);
    }
  });
  // Local edit guards: never overwrite what the operator is typing.
  ["browser-profile", "browser-channel"].forEach((id) =>
    $(id).addEventListener("input", () => {
      state.savedBrowser = true;
    }),
  );
  [
    "reply-delay-min-s",
    "reply-delay-max-s",
    "skip-no-reply-min-s",
    "skip-no-reply-max-s",
    "poll-interval-ms",
    "answer-all-burst",
    "first-message-mode",
  ].forEach((id) => {
    document.querySelectorAll(`#${id}, input[name="first-message-mode"]`).forEach((node) =>
      node.addEventListener("input", () => {
        state.savedGeneral = true;
        // The opening-burst switch only exists for USER/RANDOM mode.
        if (node.name === "first-message-mode" || id === "first-message-mode") {
          $("answer-all-field").hidden = (node.value || "") === "bot";
        }
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
    const tabs = ["overview", "browser", "proxy", "settings", "message", "chat", "logs"];
    if (event.key >= "1" && event.key <= "7") showTab(tabs[Number(event.key) - 1]);
    if (event.key === "r") {
      // RUN only exists after START; say so instead of failing silently.
      if ($("run-btn").disabled || !document.body.dataset.started || document.body.dataset.started === "no") {
        toast("RUN is available after START - press START first", true);
        return;
      }
      startShowMode().catch((error) => toast(error.message, true));
    }
    if (event.key === "s") {
      askConfirm({
        title: "Stop every browser?",
        body: "<p>Each worker finishes its current step, closes its browser and stops.</p>",
        ok: "STOP ALL",
        danger: true,
      }).then((ok) => {
        if (ok) stopShowMode().catch((error) => toast(error.message, true));
      });
    }
    if (event.key === "t") toggleTheme();
    if (event.key === "x") $("flow-restart").click();
    if (event.key === "?") showHelp();
  });
  document.addEventListener("keydown", (event) => {
    // Esc closes the dialog first, so the flow's Esc cannot fight it.
    if (event.key === "Escape" && !$("dialog").hidden) {
      event.stopPropagation();
      closeDialog(false);
    }
  }, true);
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
  // the console as it was, so every option behaves exactly like before. The
  // rail ends on the RUN step, so a live run lands on Browser; a run that was
  // never started lands on the Overview dashboard (the first tab).
  const flow = (state.snapshot || {}).flow || {};
  showTab(flow.started ? "browser" : "overview");
});

document.addEventListener("DOMContentLoaded", async () => {
  // Theme: remembered choice wins; a machine that asks for reduced motion
  // starts plain instead of animating the matrix rain.
  let storedTheme = null;
  try {
    storedTheme = localStorage.getItem("chitchat-theme");
  } catch (_) {
    storedTheme = null;
  }
  const wantsCalm =
    window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  state.theme = storedTheme || (wantsCalm ? "plain" : "theme");
  applyTheme();
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
