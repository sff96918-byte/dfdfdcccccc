/* ChitChat Bot // Show Mode - guided flow controller.
 *
 * Blank home -> the step rail slides in from the right -> each step's panel
 * slides in as it becomes the current one -> the console unlocks with every
 * option as soon as the first chat message (bot or user) is observed.
 *
 * Where the steps come from: the Python server decides (see service.flow_state
 * in /api/state.flow). This file only animates, and it never slides while the
 * operator is typing, while the pointer is over the rail/stage, while HOLD is
 * on, or when the operating system asks for reduced motion (WCAG 2.2.2).
 */
"use strict";

const Flow = (() => {
  const STAGES = [
    { key: "message", label: "MESSAGE", panel: "panel-message", hint: "browse to the folder with your .txt chat patterns" },
    { key: "proxy", label: "PROXY", panel: "panel-proxy", hint: "enable a proxy list, or save it empty to go direct" },
    { key: "setting", label: "SETTING", panel: "panel-settings", hint: "who writes first, and the timings" },
    { key: "browser", label: "BROWSER", panel: "panel-browser", hint: "run Show Mode and log in inside the window that opens" },
    { key: "live", label: "LIVE", panel: "panel-chat", hint: "waiting for the first message from the bot or the user" },
  ];
  const TAB_FOR_STAGE = { message: "message", proxy: "proxy", setting: "settings", browser: "browser", live: "chat" };
  const PULSE_MS = 1200; // beat between "step done" and the slide
  const QUIET_MS = 2000; // stay put this long after the operator touched the form
  const RETRY_MS = 900;  // how often a paused slide asks "is it calm now?"
  const $ = (id) => document.getElementById(id);

  const state = {
    stage: null,
    complete: {},
    unlocked: false,
    hold: false,
    side: "right",
    reduced: false,
    demo: false,
    started: false,
  };
  let hooks = { post: null, toast: null };
  let pending = null;
  let timer = null;
  let lastTouch = 0;

  /* ------------------------------------------------------------- rendering */
  function renderSteps() {
    $("rail-steps").innerHTML = STAGES.map((stage) => {
      const done = !!state.complete[stage.key];
      const active = state.stage === stage.key && !state.unlocked;
      const mark = done ? "✓" : active ? "▸" : "·";
      const cls = ["rail-step", done ? "done" : "", active ? "active" : ""].join(" ").trim();
      return `<li class="${cls}" data-stage="${stage.key}"><span class="rail-step-mark">${mark}</span>` +
        `<span class="rail-step-label">${stage.label}</span></li>`;
    }).join("");
    $("rail-steps").dataset.signature = stagesSignature();

    $("flow-dots").innerHTML = STAGES.map((stage) => {
      const done = !!state.complete[stage.key];
      const active = state.stage === stage.key && !state.unlocked;
      return `<span class="flow-dot ${done ? "done" : ""} ${active ? "active" : ""}" role="listitem" ` +
        `title="${stage.label}"></span>`;
    }).join("");
  }

  function stagesSignature() {
    return STAGES.map((stage) => `${stage.key}:${state.complete[stage.key] ? 1 : 0}:${state.stage === stage.key ? 1 : 0}`).join("|");
  }

  function renderPill(snapshot) {
    const pill = $("flow-pill");
    const running = !!(snapshot && snapshot.running);
    const status = String((snapshot && snapshot.status) || "idle").toUpperCase();
    const live = !!state.complete.live;
    pill.className = "pill " + (live ? "running" : running ? (status === "WAITING-LOGIN" ? "waiting" : "running") : "idle");
    pill.innerHTML = `<i></i> ${live ? "LIVE" : running ? status.replace("WAITING-LOGIN", "WAIT LOGIN") : "IDLE"}`;
  }

  function railHint() {
    const stage = STAGES.find((item) => item.key === state.stage);
    const hint = state.unlocked
      ? "Flow finished - the console is open. ⟲ flow starts the guided steps again."
      : stage
        ? `Step: ${stage.label} - ${stage.hint}.`
        : "";
    $("rail-hint").textContent = hint;
  }

  /* --------------------------------------------------------------- staging */
  function stagePanel(key) {
    const stage = STAGES.find((item) => item.key === key);
    return stage ? $(stage.panel) : null;
  }

  function showStage(key, { slide = true } = {}) {
    const panel = stagePanel(key);
    const host = $("flow-stage");
    const grid = document.querySelector("#console .grid");
    if (!panel || !host || !grid) return;
    if (host.firstElementChild === panel && state.stage === key) return;

    // Put the previous panel back where it belongs, then slide the new one in.
    // (.panel is display:none without .active, so the moved panel gets it.)
    Array.from(host.children).forEach((child) => grid.appendChild(child));
    panel.classList.add("active");
    host.appendChild(panel);
    if (slide) {
      host.classList.remove("slide-in");
      void host.offsetWidth; // restart the CSS animation
      host.classList.add("slide-in");
    }
    host.dataset.stage = key;
    state.stage = key;
    renderSteps();
    railHint();
  }

  /* ------------------------------------------------------ auto-advance ---- */
  function reducedMotion() {
    return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  }

  function busy() {
    // Never move the ground under the operator's hands.
    const active = document.activeElement;
    const typing = active && active.closest && active.closest("#flow-stage, #rail") &&
      ["INPUT", "TEXTAREA", "SELECT"].includes(active.tagName);
    if (typing) return true;
    if (state.hold || $("flow-hold").getAttribute("aria-pressed") === "true") return true;
    if (state.reduced) return true;
    if ($("rail").matches(":hover")) return true;
    return Date.now() - lastTouch < QUIET_MS;
  }

  function nextIncomplete(fromKey) {
    const index = STAGES.findIndex((item) => item.key === fromKey);
    for (let step = index + 1; step < STAGES.length; step += 1) {
      if (!state.complete[STAGES[step].key]) return STAGES[step].key;
    }
    return null;
  }

  function queueAdvance(flow) {
    const current = state.stage;
    if (!current || !flow.complete[current]) return;
    const target = nextIncomplete(current);
    if (!target) return;
    if (pending && pending.target === target) return;
    clearTimeout(timer);
    pending = { target };
    const chip = $("rail-steps").querySelector(`li[data-stage="${target}"]`);
    if (chip) chip.classList.add("next-up");
    const finish = () => {
      pending = null;
      if (chip) chip.classList.remove("next-up");
      showStage(target);
      if (hooks.toast) hooks.toast(`${STAGES.find((s) => s.key === target).label.toLowerCase()} step`);
    };
    const attempt = (left) => {
      if (state.hold || state.unlocked) return; // HOLD cancels the pending slide
      if (busy() && left > 0) {
        // The operator is working here: wait for a calm moment instead.
        timer = setTimeout(() => attempt(left - 1), RETRY_MS);
        return;
      }
      finish();
    };
    timer = setTimeout(() => attempt(30), PULSE_MS);
  }

  /* --------------------------------------------------------------- console */
  function unlock({ announce = false } = {}) {
    if (state.unlocked) return;
    state.unlocked = true;
    const host = $("flow-stage");
    const grid = document.querySelector("#console .grid");
    Array.from(host.children).forEach((child) => grid.appendChild(child));
    $("console").hidden = false;
    $("flow").classList.add("unlocked");
    $("rail").classList.add("unlocked");
    $("flow-home").hidden = true;
    $("flow-stage").hidden = true;
    document.body.dataset.flow = "console";
    clearTimeout(timer);
    pending = null;
    renderSteps();
    railHint();
    document.dispatchEvent(new CustomEvent("flow:unlocked"));
    if (announce && hooks.toast) hooks.toast("console unlocked - every option is available now");
  }

  /* ------------------------------------------------------------------- api */
  async function postPrefs(changes) {
    if (!hooks.post) return;
    try {
      await hooks.post("/api/flow/prefs", changes);
    } catch (error) {
      if (hooks.toast) hooks.toast(error.message, true);
    }
  }

  function wire() {
    $("rail-steps").addEventListener("click", (event) => {
      const chip = event.target.closest("li[data-stage]");
      if (!chip) return;
      const key = chip.dataset.stage;
      if (state.unlocked) {
        // After the unlock the dock is just a tab shortcut.
        const tab = document.querySelector(`.tab[data-tab="${TAB_FOR_STAGE[key]}"]`);
        if (tab) tab.click();
        return;
      }
      if (state.complete[key] || key === state.stage) showStage(key);
      else if (hooks.toast) hooks.toast("finish the current step first (or press SKIP)", true);
    });

    $("flow-next").addEventListener("click", () => {
      const target = nextIncomplete(state.stage) || STAGES[STAGES.length - 1].key;
      pending = null;
      clearTimeout(timer);
      showStage(target);
    });

    $("flow-hold").addEventListener("click", () => {
      state.hold = !state.hold;
      $("flow-hold").setAttribute("aria-pressed", state.hold ? "true" : "false");
      $("flow-hold").classList.toggle("on", state.hold);
      $("flow-hold").textContent = state.hold ? "▶ RESUME" : "⏸ HOLD";
      if (hooks.toast) hooks.toast(state.hold ? "auto-slide held" : "auto-slide resumed");
      postPrefs({ hold: state.hold });
    });

    $("flow-flip").addEventListener("click", () => {
      state.side = state.side === "right" ? "left" : "right";
      document.body.dataset.rail = state.side;
      $("flow").dataset.side = state.side;
      postPrefs({ rail_side: state.side });
    });

    $("flow-skip").addEventListener("click", () => {
      unlock({ announce: true });
      if (hooks.post) hooks.post("/api/flow/prefs", { console_unlocked: true }).catch(() => {});
    });

    $("flow-reset").addEventListener("click", async () => {
      if (!hooks.post) return;
      try {
        await hooks.post("/api/flow/reset", {});
        window.location.reload();
      } catch (error) {
        if (hooks.toast) hooks.toast(error.message, true);
      }
    });

    // Any typing/clicking inside the flow keeps the slide parked (WCAG 2.2.2).
    ["keydown", "input", "click", "paste"].forEach((type) =>
      document.addEventListener(type, (event) => {
        const target = event.target;
        if (target && target.closest && target.closest("#flow-stage, #rail")) lastTouch = Date.now();
      }, true),
    );

    document.addEventListener("keydown", (event) => {
      if (state.unlocked) return;
      if (event.target && event.target.closest && event.target.closest("button")) return;
      const active = document.activeElement;
      const typing = active && active.closest && active.closest("#flow-stage") &&
        ["INPUT", "TEXTAREA", "SELECT"].includes(active.tagName);
      if (event.key === "Escape" && !typing) {
        unlock({ announce: true });
        if (hooks.post) hooks.post("/api/flow/prefs", { console_unlocked: true }).catch(() => {});
      }
      if (event.key === "h" && !typing && !event.ctrlKey && !event.metaKey) $("flow-hold").click();
      if (event.key === "n" && !typing && !event.ctrlKey && !event.metaKey) $("flow-next").click();
    });

    window.addEventListener("resize", () => {});
  }

  /* --------------------------------------------------------------- observe */
  function observe(snapshot) {
    if (state.demo) return;
    const flow = (snapshot && snapshot.flow) || null;
    if (!flow) return;
    state.reduced = reducedMotion();
    const wasStage = state.stage;
    if (state.unlocked) {
      // Already open (here or on the server): keep the dock in sync, never
      // move a panel again, and never push the operator back into the wizard.
      state.complete = flow.complete || state.complete;
      state.side = flow.rail_side || state.side;
      document.body.dataset.rail = state.side;
      renderPill(snapshot);
      if ($("rail-steps").dataset.signature !== stagesSignature()) renderSteps();
      return;
    }
    state.complete = flow.complete || {};
    state.hold = !!flow.hold;
    state.side = flow.rail_side || "right";
    $("flow-hold").setAttribute("aria-pressed", state.hold ? "true" : "false");
    $("flow-hold").classList.toggle("on", state.hold);
    $("flow-hold").textContent = state.hold ? "▶ RESUME" : "⏸ HOLD";
    document.body.dataset.rail = state.side;
    $("flow").dataset.side = state.side;
    renderPill(snapshot);

    if (flow.unlocked || flow.complete.live) {
      unlock({ announce: !state.started ? false : flow.complete.live && !state.unlocked });
      return;
    }

    const signature = stagesSignature();
    if (!state.started || wasStage === null) {
      state.started = true;
      showStage(flow.stage, { slide: false });
      requestAnimationFrame(() => $("flow").classList.add("ready"));
    } else if (!$("rail-steps").dataset.signature || $("rail-steps").dataset.signature !== signature) {
      renderSteps();
    }
    if (wasStage !== null && flow.stage !== wasStage && !state.hold && flow.complete[wasStage]) {
      showStage(flow.stage);
    }
    queueAdvance(flow);
    renderSteps();
  }

  /* ------------------------------------------------------------ preview aid */
  function demoMode() {
    state.demo = true;
    document.body.dataset.flow = "demo";
    let index = 0;
    const tickDemo = () => {
      if (index >= STAGES.length) {
        state.complete = Object.fromEntries(STAGES.map((stage) => [stage.key, true]));
        state.complete.live = true;
        renderSteps();
        unlock({ announce: true });
        return;
      }
      const stage = STAGES[index];
      state.complete = Object.fromEntries(
        STAGES.map((item, position) => [item.key, position < index]),
      );
      showStage(stage.key);
      renderPill({ running: index >= 3, status: index === 3 ? "waiting-login" : "idle" });
      index += 1;
      setTimeout(tickDemo, 2600);
    };
    setTimeout(() => { $("flow").classList.add("ready"); tickDemo(); }, 700);
  }

  function init(givenHooks) {
    hooks = givenHooks || {};
    state.reduced = reducedMotion();
    wire();
    renderSteps();
    railHint();
    // The rail appears even if the API is slow; the steps then fill themselves in.
    setTimeout(() => $("flow").classList.add("ready"), 1500);
    if (new URLSearchParams(window.location.search).get("flow") === "demo") demoMode();
    return Flow;
  }

  return { init, observe, unlock, showStage, state };
})();

/* A top-level `const` lives in the global lexical scope - the browser does NOT
 * expose it as window.Flow, so app.js would never see it and the console would
 * stay hidden behind the blank home. Publish it explicitly. */
window.Flow = Flow;
