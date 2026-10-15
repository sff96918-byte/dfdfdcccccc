/* Python Show Mode dashboard.
 * Uses the same styles.css class names as the Go dashboard and talks to Python
 * through window.pywebview.api (see app.py).
 */
const state = {
  snapshot: null,
  chatSelected: "",
  polling: false,
  logsOnly: false,
};

function api() {
  return window.pywebview && window.pywebview.api ? window.pywebview.api : null;
}

function escapeHtml(value) {
  return String(value == null ? "" : value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function setStatus(message, isError = false) {
  const node = document.getElementById("browser-status");
  node.textContent = message;
  node.classList.toggle("browser-error", !!isError);
}

function renderRunState(snapshot) {
  const pill = document.getElementById("run-pill");
  const running = !!(snapshot && snapshot.running);
  pill.textContent = running ? String(snapshot.status || "running") : "Idle";
  pill.classList.toggle("stopped", !running);
  document.getElementById("run-btn").disabled = running;
  document.getElementById("stop-btn").disabled = !running;
  document.getElementById("sidebar-note").textContent = snapshot
    ? `data: ${snapshot.data_dir}`
    : "System Chrome/Edge · DOM-only chat";
  const saved = snapshot && snapshot.login_saved ? snapshot.login_saved : "";
  document.getElementById("login-saved").textContent = saved ? `Login saved: ${saved}` : "";
}

function fillForms(snapshot) {
  if (!snapshot) return;
  const browser = snapshot.browser || {};
  document.getElementById("browser-profile").value = browser.profile || "default";
  document.getElementById("browser-channel").value = browser.channel || "auto";
  document.getElementById("browser-headless").checked = !!browser.headless;

  const general = snapshot.general || {};
  document.getElementById("first-message-mode").value = general.first_message_mode || "bot";
  document.getElementById("reply-delay-ms").value = general.reply_delay_ms || 0;
  document.getElementById("poll-interval-ms").value = general.poll_interval_ms || 350;
  document.getElementById("poll-min").value = general.poll_min_seconds || 0;
  document.getElementById("poll-max").value = general.poll_max_seconds || 0;
  document.getElementById("action-min").value = general.action_min_seconds || 0;
  document.getElementById("action-max").value = general.action_max_seconds || 0;

  const message = snapshot.message || {};
  document.getElementById("chat-mode").value = message.chat_mode || "single";
  document.getElementById("message-sequence").value = message.sequence || "";
  document.getElementById("message-usernames").value = message.usernames || "";
  const loaded = (message.loaded_sequences || []).length;
  document.getElementById("folder-summary").textContent = loaded
    ? `${loaded} sequence file(s) loaded.`
    : "No folder loaded.";
}

function renderChat(snapshot) {
  const chat = (snapshot && snapshot.chat) || {};
  const select = document.getElementById("chat-sessions");
  const sessions = chat.sessions || [];
  const wanted = state.chatSelected || chat.selected || "";
  const options = sessions
    .map((session) => {
      const selected = session.id === (chat.selected || wanted) ? " selected" : "";
      const mark = session.active ? " (active)" : "";
      return `<option value="${escapeHtml(session.id)}"${selected}>${escapeHtml(session.label + mark)}</option>`;
    })
    .join("");
  if (select.dataset.signature !== options) {
    select.dataset.signature = options;
    select.innerHTML = options || '<option value="">No chat yet</option>';
  }

  const messages = chat.messages || [];
  const list = document.getElementById("chat-messages");
  list.innerHTML = messages.length
    ? messages
        .map((row) => {
          const role = row.role === "bot" ? "home-chat-bot" : "home-chat-user";
          const who = row.role === "bot" ? "Bot" : "User";
          return `<div class="home-chat-row ${role}"><strong>${who}</strong><span>${escapeHtml(row.text)}</span></div>`;
        })
        .join("")
    : '<p class="home-chat-empty">Nothing observed yet.</p>';
  list.scrollTop = list.scrollHeight;
  document.getElementById("chat-meta").textContent = messages.length
    ? `${messages.length} message(s)`
    : "";
  if (select.value) state.chatSelected = select.value;
}

function renderLogs(snapshot) {
  const entries = (snapshot && snapshot.logs) || [];
  const list = document.getElementById("log-list");
  list.innerHTML = entries
    .slice()
    .reverse()
    .map((entry) => {
      const level = String(entry.level || "").toLowerCase();
      return (
        '<li class="log-entry">' +
        `<span class="log-time">${escapeHtml(entry.timestamp || "")}</span>` +
        `<span class="log-level ${escapeHtml(level)}">${escapeHtml(entry.level || "")}</span>` +
        `<span class="log-message">${escapeHtml(entry.message || "")}</span>` +
        "</li>"
      );
    })
    .join("");
}

async function refresh(fill = true) {
  const backend = api();
  if (!backend) return;
  try {
    const snapshot = await backend.get_state(state.chatSelected || "");
    state.snapshot = snapshot;
    if (fill) fillForms(snapshot);
    renderRunState(snapshot);
    renderChat(snapshot);
    renderLogs(snapshot);
  } catch (error) {
    setStatus(error && error.message ? error.message : String(error), true);
  }
}

function startPolling() {
  if (state.polling) return;
  state.polling = true;
  setInterval(() => refresh(false), 700);
}

function currentFormValue(id) {
  const node = document.getElementById(id);
  return node ? node.value : "";
}

function currentNumber(id) {
  const value = Number(currentFormValue(id));
  return Number.isFinite(value) ? value : 0;
}

function bindForms() {
  document.querySelectorAll(".nav-button").forEach((button) => {
    button.addEventListener("click", () => {
      document.querySelectorAll(".nav-button").forEach((item) => item.classList.remove("active"));
      document.querySelectorAll(".tab-panel").forEach((panel) => panel.classList.remove("active"));
      button.classList.add("active");
      const panel = document.getElementById(`tab-${button.dataset.tab}`);
      if (panel) panel.classList.add("active");
    });
  });

  document.getElementById("browser-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const saved = await api().save_browser_settings({
      profile: currentFormValue("browser-profile").trim() || "default",
      channel: currentFormValue("browser-channel") || "auto",
      headless: document.getElementById("browser-headless").checked,
    });
    fillForms(Object.assign({}, state.snapshot, { browser: saved }));
    setStatus(`Browser settings saved (${saved.channel} / ${saved.profile}).`);
  });

  document.getElementById("general-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const saved = await api().save_general_settings({
      first_message_mode: currentFormValue("first-message-mode"),
      reply_delay_ms: currentNumber("reply-delay-ms"),
      poll_interval_ms: currentNumber("poll-interval-ms"),
      poll_min_seconds: currentNumber("poll-min"),
      poll_max_seconds: currentNumber("poll-max"),
      action_min_seconds: currentNumber("action-min"),
      action_max_seconds: currentNumber("action-max"),
    });
    fillForms(Object.assign({}, state.snapshot, { general: saved }));
    await refresh(false);
  });

  document.getElementById("message-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const saved = await api().save_message_settings({
      chat_mode: currentFormValue("chat-mode"),
      sequence: currentFormValue("message-sequence"),
      usernames: currentFormValue("message-usernames"),
    });
    fillForms(Object.assign({}, state.snapshot, { message: saved }));
    await refresh(false);
  });

  document.getElementById("run-btn").addEventListener("click", async () => {
    setStatus("Starting Show Mode…");
    const result = await api().start_show_mode(1);
    if (result && result.ok === false) {
      setStatus(result.error || "Could not start Show Mode.", true);
    } else {
      setStatus("Show Mode started: log in inside the new browser window.");
    }
    await refresh(false);
  });

  document.getElementById("stop-btn").addEventListener("click", async () => {
    await api().stop_show_mode();
    setStatus("Stopping…");
    await refresh(false);
  });

  document.getElementById("load-folder-btn").addEventListener("click", async () => {
    const result = await api().load_message_folder();
    if (result && result.ok === false) {
      document.getElementById("folder-summary").textContent = result.error || "Could not load folder.";
      return;
    }
    await refresh(true);
  });

  document.getElementById("clear-logs-btn").addEventListener("click", async () => {
    await api().clear_logs();
    await refresh(false);
  });

  document.getElementById("refresh-logs-btn").addEventListener("click", () => refresh(false));

  document.getElementById("chat-sessions").addEventListener("change", (event) => {
    state.chatSelected = event.target.value;
    refresh(false);
  });
}

window.addEventListener("DOMContentLoaded", async () => {
  bindForms();
  if (!api()) {
    setStatus("Wails/pywebview bindings not ready yet; retrying…");
    setTimeout(() => refresh(true).then(startPolling), 400);
    return;
  }
  await refresh(true);
  startPolling();
});
