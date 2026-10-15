const state = {
  snapshot: null,
  currentTab: "home",
  browserFormLoaded: false,
  browserRunPending: false,
  browserActivityVersion: 0,
  chat: { version: -1, selected: "", manual: false, pending: false, dirty: false, token: 0, rendered: "" },
};

function backend() {
  return window.go && window.go.main && window.go.main.App;
}

async function invoke(method, ...args) {
  try {
    const api = backend();
    if (!api || typeof api[method] !== "function") {
      throw new Error(`Wails binding not ready: ${method}`);
    }
    return await api[method](...args);
  } catch (error) {
    reportFrontendError(`Backend ${method} failed`, error);
    throw error;
  }
}

function runtimeEvents() {
  return window.runtime || {};
}

// Never serialize backend arguments: settings can contain credentials.
function reportFrontendError(context, error) {
  try {
    const runtime = runtimeEvents();
    if (typeof runtime.LogError === "function") {
      const detail = error && error.stack ? error.stack : String(error);
      runtime.LogError(`Frontend | ${context} | ${detail}`);
    }
  } catch (_) { /* Do not recurse when the logging bridge is unavailable. */ }
}

function setRunning(running) {
  const pill = document.getElementById("running-pill");
  pill.textContent = running ? "Running" : "Stopped";
  pill.className = `pill ${running ? "running" : "stopped"}`;
  const cleanButton = document.getElementById("clean-profiles");
  if (cleanButton) {
    cleanButton.disabled = !!running;
    cleanButton.title = running ? "Stop the bot before cleaning profile data." : "";
  }
}

// The transcript has its own event/API; it never parses technical log text.
function renderChatTranscript(snapshot) {
  const box = document.getElementById("chat-messages");
  const select = document.getElementById("chat-session");
  const follow = box.scrollHeight - box.scrollTop - box.clientHeight < 64;
  const oldTop = box.scrollTop;
  const changedConversation = state.chat.rendered !== snapshot.selected;
  select.replaceChildren();
  const automatic = document.createElement("option");
  automatic.value = ""; automatic.textContent = "Latest active chat"; select.append(automatic);
  const sessions = snapshot.sessions || [];
  for (const session of sessions) {
    const option = document.createElement("option");
    option.value = session.id;
    option.textContent = session.label + (session.active ? "" : " (ended)");
    select.append(option);
  }
  if (state.chat.manual && !sessions.some(s => s.id === state.chat.selected)) {
    state.chat.manual = false; state.chat.selected = "";
  }
  select.value = state.chat.manual ? state.chat.selected : "";
  select.disabled = sessions.length === 0;
  const current = sessions.find(s => s.id === snapshot.selected);
  const note = document.getElementById("chat-history-note");
  const notices = [];
  if (current && current.dropped) notices.push(`${current.dropped} older messages outside the retained display window.`);
  if (snapshot.evicted) notices.push(`${snapshot.evicted} older conversations no longer retained.`);
  note.textContent = notices.join(" "); note.hidden = notices.length === 0;
  const fragment = document.createDocumentFragment();
  const seen = new Set();
  for (const message of [...(snapshot.messages || [])].sort((a,b) => a.order - b.order)) {
    if (!message.id || seen.has(message.id) || !["user", "bot"].includes(message.role)) continue;
    seen.add(message.id);
    const row = document.createElement("div");
    row.className = `home-chat-row home-chat-${message.role}`;
    row.dataset.messageId = message.id;
    const label = document.createElement("strong");
    label.textContent = message.role === "user" ? "User:" : "Bot:";
    const text = document.createElement("span");
    text.textContent = message.text; // Never innerHTML; messages may contain markup.
    row.append(label, text); fragment.append(row);
  }
  if (!fragment.childNodes.length) {
    const empty = document.createElement("p"); empty.className = "home-chat-empty";
    empty.textContent = "Waiting for identified browser messages."; fragment.append(empty);
  }
  box.replaceChildren(fragment);
  state.chat.rendered = snapshot.selected || "";
  box.scrollTop = changedConversation || follow ? box.scrollHeight : oldTop;
}

async function refreshChatTranscript() {
  // Fake/older backends need not expose the new optional display API.
  if (!backend() || typeof backend().GetLiveChatSnapshot !== "function") return;
  if (state.chat.pending) { state.chat.dirty = true; return; }
  state.chat.pending = true; state.chat.dirty = false;
  const token = ++state.chat.token;
  try {
    const snapshot = await invoke("GetLiveChatSnapshot", state.chat.manual ? state.chat.selected : "");
    if (token === state.chat.token && snapshot.version >= state.chat.version) {
      state.chat.version = snapshot.version;
      renderChatTranscript(snapshot);
    }
  } catch (_) { /* invoke already forwards diagnostics to logs.txt. */ }
  finally {
    state.chat.pending = false;
    if (state.chat.dirty) { state.chat.dirty = false; queueMicrotask(refreshChatTranscript); }
  }
}

function browserStatus(message, isError = false) {
  const status = document.getElementById("browser-status");
  status.textContent = `Status: ${message}`;
  status.classList.toggle("browser-error", isError);
}

function setBrowserRunPending(pending) {
  state.browserRunPending = pending;
  document.querySelectorAll(".browser-run").forEach((button) => {
    button.disabled = pending;
  });
  document.getElementById("browser-options").setAttribute("aria-busy", String(pending));
}

async function runBrowserOption(option) {
  if (state.browserRunPending) return;
  const count = Number(document.getElementById("fresh-browser-count").value);
  if (!Number.isInteger(count) || count < 1 || count > 5) {
    browserStatus("Browser count must be between 1 and 5.", true);
    return;
  }
  const settings = {
    backend: option.key,
    profile: document.getElementById("browser-profile").value.trim() || "default",
    headless: option.key === "show_mode" ? false : document.getElementById("browser-headless").checked,
  };
  setBrowserRunPending(true);
  const activityVersion = state.browserActivityVersion;
  browserStatus(`Requesting ${count} fresh ${option.label} browser(s)...`);
  try {
    await invoke("RunBrowser", settings, count);
    // A fast worker may already have emitted a failure or login status.
    if (state.browserActivityVersion === activityVersion) {
      browserStatus(`${option.label}: launch request accepted (${count}). Waiting for browser activity...`);
    }
  } catch (error) {
    browserStatus(error.message || String(error), true);
  } finally {
    setBrowserRunPending(false);
  }
}

function renderBrowserOptions(options) {
  const root = document.getElementById("browser-options");
  root.innerHTML = "";
  document.getElementById("browser-available").textContent = `Available: ${options.length}`;
  options.forEach((option, index) => {
    const row = document.createElement("div");
    row.className = "browser-choice browser-run-row";
    row.innerHTML = `
      <span class="browser-number">${String(index + 1).padStart(2, "0")}</span>
      <div class="browser-details">
        <div class="browser-choice-title">${escapeHtml(option.label)}</div>
        <div class="browser-choice-description">${escapeHtml(option.description)}</div>
      </div>
      <button type="button" class="primary-button browser-run" aria-label="Run ${escapeHtml(option.label)}">Run</button>
    `;
    row.querySelector("button").addEventListener("click", () => runBrowserOption(option));
    root.appendChild(row);
  });
  setBrowserRunPending(state.browserRunPending);
}

function renderHomeStats(stats) {
  const home = stats || {};
  document.getElementById("stat-bot-accounts").textContent = String(home.bot_accounts || 0);
  document.getElementById("stat-running-accounts").textContent = String(home.running_accounts || 0);
  document.getElementById("stat-user-send").textContent = String(home.user_send_count || 0);
  document.getElementById("stat-traffic-users").textContent = String(home.traffic_users || 0);
  document.getElementById("stat-snap-tg-user").textContent = String(home.snap_tg_sent_count || 0);
  document.getElementById("stat-messages-sent").textContent = String(home.messages_sent || 0);
  document.getElementById("stat-dead-accounts").textContent = String(home.dead_accounts || 0);
  document.getElementById("stat-skipped-accounts").textContent = String(home.skipped_accounts || 0);
}

function evaAISettings() {
  return {
    ai_enabled: document.getElementById("evachat-ai-enabled").checked,
    ai_base_url: document.getElementById("evachat-ai-base-url").value.trim(),
    ai_model: document.getElementById("evachat-ai-model").value.trim(),
    ai_api_key: document.getElementById("evachat-ai-api-key").value.trim(),
  };
}

function setChatMode(mode) {
  const validMode = (mode === "folder" || mode === "eva_chat_flow") ? mode : "single";
  ["single", "folder", "evachat"].forEach((m) => {
    const radio = document.getElementById(`chat-mode-${m}`);
    const label = document.getElementById(`mode-option-${m}`);
    const section = document.getElementById(`section-${m}-mode`);
    if (radio) radio.checked = ((m === "evachat" ? "eva_chat_flow" : m) === validMode);
    if (label) label.classList.toggle("active", (m === "evachat" ? "eva_chat_flow" : m) === validMode);
    if (section) section.style.display = ((m === "evachat" ? "eva_chat_flow" : m) === validMode) ? "block" : "none";
  });
  const useLoaded = document.getElementById("use-loaded-sequences");
  if (useLoaded) useLoaded.checked = (validMode === "folder");
}

function fillForms(snapshot) {
  // Subsequent state refreshes must not discard current, unsaved form values.
  if (!state.browserFormLoaded) {
    document.getElementById("browser-profile").value = snapshot.browser.profile || "default";
    document.getElementById("browser-headless").checked = !!snapshot.browser.headless;
    state.browserFormLoaded = true;
  }
  
  const currentMode = snapshot.message.chat_mode || ((snapshot.message.use_loaded_sequences && snapshot.message.loaded_sequences && snapshot.message.loaded_sequences.length > 0) ? "folder" : "single");
  setChatMode(currentMode);

  document.getElementById("loaded-sequences-status").textContent = snapshot.message.loaded_sequences ? `${snapshot.message.loaded_sequences.length} sequences loaded` : "0 sequences loaded";
  document.getElementById("message-sequence").value = snapshot.message.sequence || "";
  const evaStatus = document.getElementById("evachat-status");
  if (evaStatus) evaStatus.textContent = currentMode === "eva_chat_flow" ? "EVAChat Flow will load the editable data/eva_chat TXT pack when the bot starts." : "TXT data is initialized when EVAChat Flow starts.";
  document.getElementById("message-usernames").value = snapshot.message.usernames || "";
  document.getElementById("evachat-ai-enabled").checked = !!snapshot.message.ai_enabled;
  document.getElementById("evachat-ai-base-url").value = snapshot.message.ai_base_url || "";
  document.getElementById("evachat-ai-model").value = snapshot.message.ai_model || "";
  document.getElementById("evachat-ai-api-key").value = snapshot.message.ai_api_key || "";
  document.getElementById("proxy-enabled").checked = !!snapshot.proxy.enabled;
  document.getElementById("proxy-entries").value = snapshot.proxy.entries || "";
  document.getElementById("poll-min").value = snapshot.general.poll_min_seconds;
  document.getElementById("poll-max").value = snapshot.general.poll_max_seconds;
  document.getElementById("action-min").value = snapshot.general.action_min_seconds;
  document.getElementById("action-max").value = snapshot.general.action_max_seconds;
  document.getElementById("match-reply-timeout-min").value = snapshot.general.match_reply_timeout_min_seconds || 60;
  document.getElementById("match-reply-timeout-max").value = snapshot.general.match_reply_timeout_max_seconds || 60;
  document.getElementById("match-close-wait-min").value = snapshot.general.match_close_wait_min_seconds || 0;
  document.getElementById("match-close-wait-max").value = snapshot.general.match_close_wait_max_seconds || 0;
  document.getElementById("reply-delay-min").value = snapshot.general.reply_delay_min_seconds || 0;
  document.getElementById("reply-delay-max").value = snapshot.general.reply_delay_max_seconds || 0;
  renderBrowserOptions(snapshot.browser_options || []);
  renderHomeStats(snapshot.home);
  setRunning(!!snapshot.running);
  if (snapshot.license_invalid) {
    const overlay = document.getElementById('license-overlay');
    overlay.style.display = 'flex';
    document.getElementById('license-message').textContent = snapshot.license_reason || "Your license is invalid or expired.";
    document.getElementById('license-key-input').value = snapshot.license_key || "UNKNOWN";
    return;
  }

  document.querySelector('.shell').classList.add('visible');

  const messageLineCount = String(snapshot.message.sequence || "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean).length;
  document.getElementById("home-summary").textContent =
    `Backend: ${snapshot.browser.backend} | Profiles auto-start | Message steps: ${messageLineCount}`;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

async function refreshState() {
  const snapshot = await invoke("GetInitialState");
  state.snapshot = snapshot;
  fillForms(snapshot);
  await refreshChatTranscript();
}

async function autoRefreshStats() {
  try {
    const snapshot = await invoke("GetInitialState");
    renderHomeStats(snapshot.home);
  } catch (err) {
    // ignore
  }
}

function activateTab(tabName) {
  state.currentTab = tabName;

  document.querySelectorAll(".nav-button").forEach((button) => {
    button.classList.toggle("active", button.dataset.tab === tabName);
  });
  document.querySelectorAll(".tab-panel").forEach((panel) => {
    panel.classList.toggle("active", panel.dataset.panel === tabName);
  });
}

function bindTabs() {
  document.querySelectorAll(".nav-button").forEach((button) => {
    button.addEventListener("click", () => activateTab(button.dataset.tab));
  });
}

function proxyInputLines() {
  const raw = document.getElementById("proxy-entries").value;
  if (document.getElementById("proxy-default-scheme").value !== "socks5") return raw;
  return raw.split(/\r?\n/).map(line => {
    const text = line.trim();
    return text && !/^[a-z][a-z0-9+.-]*:\/\//i.test(text) ? "socks5://" + text : line;
  }).join("\n");
}

async function testProxyEntries() {
  const button = document.getElementById("test-proxy");
  if (button.disabled) return;
  const raw = proxyInputLines();
  const status = document.getElementById("proxy-test-status");
  const results = document.getElementById("proxy-test-results");
  results.replaceChildren();
  if (!raw.trim()) { status.textContent = "Enter at least one proxy to test."; return; }
  button.disabled = true;
  button.textContent = "Testing…";
  status.textContent = "Testing proxy authentication and HTTPS access… (up to 12 seconds per proxy; four at a time)";
  try {
    const rows = await invoke("TestProxyEntries", raw);
    let passed = 0, reachable = 0;
    for (const row of rows || []) {
      if (row.ok) passed++;
      if (row.tunnel_ok) reachable++;
      const item = document.createElement("p");
      item.className = row.ok ? "proxy-test-pass" : row.tunnel_ok ? "proxy-test-warn" : "proxy-test-fail";
      // textContent only: proxy/provider text is never treated as markup.
      item.textContent = `Line ${row.line}: ${row.ok ? "PASS" : row.tunnel_ok ? "REACHABLE / SITE RESTRICTED" : "FAIL"} — ${row.label || "Invalid entry"} — ${row.latency_ms} ms${row.status ? ` — HTTP ${row.status}` : ""} — ${row.detail}`;
      if (row.provider_message) item.textContent += `\nProvider said: “${row.provider_message}”`;
      if (row.proxy_authenticate) item.textContent += `\nAuth challenge: ${row.proxy_authenticate}`;
      if (row.hint) item.textContent += `\nNext step: ${row.hint}`;
      results.appendChild(item);
    }
    status.textContent = `${passed}/${(rows || []).length} passed; ${reachable} HTTPS tunnels verified. A successful test is not a browser-login guarantee.`;
    if (proxyInputLines() !== raw) status.textContent += " List edited: these results are for the previous tested values; test again.";
  } catch (error) {
    status.textContent = error.message || String(error);
  } finally {
    button.disabled = false;
    button.textContent = "Test Proxy";
  }
}

function bindForms() {
  document.getElementById("test-proxy").addEventListener("click", testProxyEntries);
  // Enter must not submit/reload the page or implicitly choose a backend.
  document.getElementById("browser-form").addEventListener("submit", (event) => {
    event.preventDefault();
  });

  document.querySelectorAll('input[name="chat-mode"]').forEach((radio) => {
    radio.addEventListener("change", (e) => {
      setChatMode(e.target.value);
    });
  });

  ["single", "folder", "evachat"].forEach((mode) => {
    const label = document.getElementById(`mode-option-${mode}`);
    if (label) {
      label.addEventListener("click", () => {
        setChatMode(mode === "evachat" ? "eva_chat_flow" : mode);
      });
    }
  });

  document.getElementById("load-folder-btn").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      const sequences = await invoke("LoadMessageFolder");
      if (sequences && sequences.length > 0) {
        summary.textContent = `Loaded ${sequences.length} message sequences from folder.`;
        setChatMode("folder");
        await refreshState();
        setChatMode("folder");
      }
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("clean-loaded-btn").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      await invoke("ClearLoadedMessages");
      summary.textContent = "Loaded sequences cleared from DB.";
      setChatMode("single");
      await refreshState();
      setChatMode("single");
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("evachat-ai-test-btn").addEventListener("click", async () => {
    const status = document.getElementById("evachat-ai-status");
    status.textContent = "Testing AI API...";
    try {
      const result = await invoke("TestEVAChatAI", {
        chat_mode: "eva_chat_flow",
        sequence: document.getElementById("message-sequence").value,
        usernames: document.getElementById("message-usernames").value,
        ...evaAISettings(),
      });
      status.textContent = result || "AI OK";
    } catch (error) {
      status.textContent = "AI test failed: " + (error.message || String(error));
    }
  });

  document.getElementById("open-evachat-folder-btn").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      const result = await invoke("OpenEVAChatDataFolder");
      if (result) summary.textContent = result;
    } catch (error) {
      if (summary) summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("message-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const selectedMode = document.querySelector('input[name="chat-mode"]:checked')?.value || "single";
    await invoke("SaveMessageSettings", {
      chat_mode: selectedMode,
      use_loaded_sequences: selectedMode === "folder",
      sequence: document.getElementById("message-sequence").value,
      usernames: document.getElementById("message-usernames").value,
      ...evaAISettings(),
    });
    await refreshState();
    setChatMode(selectedMode);
  });

  document.getElementById("proxy-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    const summary = document.getElementById("home-summary");
    try {
      await invoke("SaveProxySettings", {
        enabled: document.getElementById("proxy-enabled").checked,
        entries: proxyInputLines(),
      });
      await refreshState();
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("general-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    await invoke("SaveGeneralSettings", {
      poll_min_seconds: Number(document.getElementById("poll-min").value),
      poll_max_seconds: Number(document.getElementById("poll-max").value),
      action_min_seconds: Number(document.getElementById("action-min").value),
      action_max_seconds: Number(document.getElementById("action-max").value),
      match_reply_timeout_min_seconds: Number(document.getElementById("match-reply-timeout-min").value),
      match_reply_timeout_max_seconds: Number(document.getElementById("match-reply-timeout-max").value),
      match_close_wait_min_seconds: Number(document.getElementById("match-close-wait-min").value),
      match_close_wait_max_seconds: Number(document.getElementById("match-close-wait-max").value),
      reply_delay_min_seconds: Number(document.getElementById("reply-delay-min").value),
      reply_delay_max_seconds: Number(document.getElementById("reply-delay-max").value),
    });
    await refreshState();
  });
}

function bindActions() {
  document.getElementById("start-bot").addEventListener("click", async () => {
    await invoke("StartBot");
    await refreshState();
  });

  document.getElementById("stop-bot").addEventListener("click", async () => {
    await invoke("StopBot");
    await refreshState();
  });

  document.getElementById("clean-profiles").addEventListener("click", async () => {
    const summary = document.getElementById("home-summary");
    try {
      await invoke("CleanProfiles");
      await refreshState();
      summary.textContent = "Profile data cleaned. Login profiles and worker ownership data were removed.";
    } catch (error) {
      summary.textContent = error.message || String(error);
    }
  });

  document.getElementById("refresh-state").addEventListener("click", async () => {
    await refreshState();
  });

  document.getElementById("clear-logs").addEventListener("click", async () => {
    await invoke("ClearLogs");
    await refreshState();
  });


}

function bindEvents() {
  const runtime = runtimeEvents();
  document.getElementById("chat-session").addEventListener("change", (event) => {
    state.chat.manual = !!event.target.value;
    state.chat.selected = event.target.value;
    state.chat.token++; // Ignore an older in-flight response for another selection.
    refreshChatTranscript();
  });

  if (typeof runtime.EventsOn === "function") {
    runtime.EventsOn("ui:chat_changed", (event) => {
      if (event && event.version > state.chat.version) refreshChatTranscript();
    });
    runtime.EventsOn("ui:log", (entry) => {
      if (typeof entry.message === "string" && /\[[^\]]+ fresh \d+\/\d+\]/.test(entry.message)
          && !entry.message.includes("] Removed temporary browser data from ")) {
        state.browserActivityVersion += 1;
        browserStatus(entry.message, entry.level === "error");
      }
    });
    runtime.EventsOn("ui:running", (running) => {
      setRunning(!!running);
    });
    runtime.EventsOn("ui:license_invalid", (data) => {
      document.querySelector('.shell').classList.remove('visible');
      const overlay = document.getElementById('license-overlay');
      overlay.style.display = 'flex';
      document.getElementById('license-message').textContent = data.reason || "Your license became invalid or expired.";
      document.getElementById('license-key-input').value = data.key || "UNKNOWN";
    });
  }

  const copyBtn = document.getElementById('copy-license-btn');
  if (copyBtn) {
    copyBtn.addEventListener('click', () => {
      const input = document.getElementById('license-key-input');
      input.select();
      input.setSelectionRange(0, 99999);
      navigator.clipboard.writeText(input.value).then(() => {
        const originalText = copyBtn.textContent;
        copyBtn.textContent = 'Copied!';
        setTimeout(() => copyBtn.textContent = originalText, 2000);
      });
    });
  }
}

async function bootstrap() {
  bindTabs();
  bindForms();
  bindActions();
  bindEvents();
  activateTab("home");

  let attempts = 0;
  while (attempts < 50) {
    attempts += 1;
    try {
      await refreshState();
      setInterval(autoRefreshStats, 3000);
      setInterval(refreshChatTranscript, 1000); // replay after missed events / closure
      return;
    } catch (error) {
      await new Promise((resolve) => setTimeout(resolve, 200));
    }
  }

  document.getElementById("home-summary").textContent = "Failed to connect to Wails bindings.";
}

window.addEventListener("error", (event) => {
  reportFrontendError("Uncaught error", event.error || event.message);
});
window.addEventListener("unhandledrejection", (event) => {
  reportFrontendError("Unhandled promise rejection", event.reason);
});

window.addEventListener("DOMContentLoaded", () => {
  bootstrap().catch((error) => {
    reportFrontendError("Bootstrap failed", error);
    document.getElementById("home-summary").textContent = error.message || String(error);
  });
});
