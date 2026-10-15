"use strict";
// Capture session: launches a normal visible browser with a DevTools port,
// attaches via CDP, and records network + websocket + DOM + storage evidence
// to disk in real time. Manual login is done by the user in the browser.

const fs = require("node:fs");
const path = require("node:path");
const { CDP } = require("./cdp.cjs");
const { findBrowser, launchBrowser, waitForDevToolsEndpoint, getFreePort } = require("./browser.cjs");
const { SessionStore } = require("./store.cjs");
const red = require("./redactor.cjs");
const { parsePayload, findMessageInfo, extractEventName, classifyFrame } = require("./classifier.cjs");
const { Catalog } = require("./catalog.cjs");
const { computeCapabilities, renderCapabilityLines } = require("./capabilities.cjs");
const { WsCatalog } = require("./wscatalog.cjs");
const crypto = require("node:crypto");
const { buildHar } = require("./har.cjs");

const HOOKS_SOURCE = fs.readFileSync(path.join(__dirname, "hooks.js"), "utf8");
const TOOL_VERSION = "1.2.1";

const DUMP_EXPR = `(() => {
  function dump(s){ var o={}; try{ for(var i=0;i<s.length;i++){ var k=s.key(i); o[k]=s.getItem(k);} }catch(e){} return o; }
  return {
    url: location.href,
    title: document.title || "",
    html: document.documentElement ? document.documentElement.outerHTML : "",
    localStorage: dump(window.localStorage),
    sessionStorage: dump(window.sessionStorage)
  };
})()`;

class CaptureSession {
  constructor(options) {
    this.config = options.config;
    this.captureDir = options.captureDir; // .../capture
    this.log = options.onLog || ((m) => console.log(m));
    this.sessions = new Map(); // sessionId -> {targetId, url}
    this.requests = new Map(); // key -> record
    this.harRecords = [];
    this.sockets = new Map(); // requestId -> socket info
    this.candidates = []; // message candidates from network
    this.domMessages = [];
    this.catalog = new Catalog();
    this.wsCatalog = new WsCatalog();
    this.startedAt = new Date();
    this.stopped = false;
    this._ready = false;
    this._browserDied = false;
    this._announcedCaps = new Set();
    this._done = new Promise((r) => (this._resolveDone = r));
  }

  get done() {
    return this._done;
  }

  redactEnabled() {
    return !this.config.rawMode;
  }

  safeUrl(u) {
    return red.redactUrl(u || "", this.redactEnabled());
  }

  async start() {
    const cfg = this.config;
    const browserPath = findBrowser(cfg.browserPath);
    if (!browserPath) {
      throw new Error(
        "No Chrome/Edge/Chromium found. Install Google Chrome, or set browserPath in capture.config.json (or CAPTURE_BROWSER env var).",
      );
    }
    this.browserPath = browserPath;
    this.store = new SessionStore(path.join(this.captureDir, "capture-sessions"), cfg.startUrl);
    this.store.event("session_started", {
      tool: "root-project capture",
      version: TOOL_VERSION,
      startUrl: this.safeUrl(cfg.startUrl),
      browser: browserPath,
      redactEnabled: this.redactEnabled(),
      headless: !!cfg.headless,
    });
    this.log(`[capture] Session folder: ${this.store.dir}`);
    if (this.redactEnabled()) this.log("[capture] Redaction ON (tokens/cookies/passwords hidden). Set rawMode=true in capture.config.json only if you accept the risk and never share raw output.");
    else this.log("[capture] WARNING: rawMode ON — secrets will be saved. DO NOT share this session folder.");

    const profileDir = path.join(this.captureDir, "profiles", "browser");
    const browserLog = path.join(this.store.dir, "browser.log");
    const port = await getFreePort();
    this.child = launchBrowser({ browserPath, profileDir, url: cfg.startUrl, headless: !!cfg.headless, port, logFile: browserLog });
    this.child.on("exit", () => {
      this._browserDied = true;
      if (this._ready && !this.stopped) {
        this.log("[capture] Browser closed by user; finishing session.");
        this.stop("browser-closed").catch(() => {});
      }
    });
    let version;
    try {
      const ready = await waitForDevToolsEndpoint({
        port,
        profileDir,
        isAlive: () => !this._browserDied && this.child.exitCode === null,
        logFile: browserLog,
      });
      version = ready.version;
      this.cdp = await new CDP(version.webSocketDebuggerUrl).connect();
    } catch (e) {
      try {
        if (this.child && !this.child.killed) this.child.kill();
      } catch {
        /* ignore */
      }
      throw new Error(`Browser connect failed: ${e.message} (browser log: ${browserLog})`);
    }
    this.cdp.onEvent((msg) => this.onCdpEvent(msg).catch(() => {}));
    await this.cdp.send("Target.setDiscoverTargets", { discover: true });
    await this.cdp.send("Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: false, flatten: true });
    const { targetInfos } = await this.cdp.send("Target.getTargets");
    for (const t of targetInfos || []) {
      if (t.type === "page") {
        try {
          await this.cdp.send("Target.attachToTarget", { targetId: t.targetId, flatten: true });
        } catch {
          /* already attached via auto-attach */
        }
      }
    }
    this._heartbeat = setInterval(() => { this.writeManifest(false); this.writeCatalogs(); }, 10000);
    this._ready = true;
    this.log("[capture] Browser open hoye geche. Ekhane NOTUN kore login korun (eta capture-er alada profile), tarpor normally chat korun.");
    this.log("[capture] Markers: Ctrl+Alt+I = incoming example, Ctrl+Alt+O = outgoing example, Ctrl+Alt+S = snapshot. Stop: browser bondho korun othoba terminal-e Q chapen.");
    this.log("[capture] --- Capture hole ja ja paoya jabe (live checklist) ---");
    for (const line of renderCapabilityLines(this.capabilities())) this.log("[capture] " + line);
    this.log("[capture] ------------------------------------------------------");
    this._statusTimer = setInterval(() => {
      const c = this.store.counts;
      const mins = Math.max(0, Math.round((Date.now() - this.startedAt.getTime()) / 1000));
      this.log(`[live ${mins}s] HTTP ${c.httpRequests}/${c.httpResponses} | WS socket ${c.wsConnections}, frame pathano ${c.wsFramesSent}, esheche ${c.wsFramesReceived} | DOM msg ${c.domMessages} | snapshot ${c.snapshots} | marker I:${c.incomingMarkers} O:${c.outgoingMarkers} — sob auto save hocche`);
    }, 20000);
    return this;
  }

  async onCdpEvent(msg) {
    const { method, params, sessionId } = msg;
    if (method === "Target.attachedToTarget") {
      const info = params.targetInfo || {};
      if (info.type === "page") await this.setupPage(params.sessionId, info);
      return;
    }
    if (!sessionId || !this.sessions.has(sessionId)) return;
    switch (method) {
      case "Network.requestWillBeSent":
        return this.onRequest(sessionId, params);
      case "Network.responseReceived":
        return this.onResponse(sessionId, params);
      case "Network.loadingFinished":
        return this.onLoadingFinished(sessionId, params);
      case "Network.loadingFailed":
        return this.onLoadingFailed(sessionId, params);
      case "Network.webSocketCreated":
        return this.onWsCreated(sessionId, params);
      case "Network.webSocketHandshakeResponseReceived":
        return this.onWsHandshake(sessionId, params);
      case "Network.webSocketFrameSent":
        return this.onWsFrame(sessionId, params, "sent");
      case "Network.webSocketFrameReceived":
        return this.onWsFrame(sessionId, params, "received");
      case "Network.webSocketClosed":
        return this.onWsClosed(sessionId, params);
      case "Network.eventSourceMessageReceived":
        this.store.coverage.websockets = true;
        return this.recordCandidate({
          source: "sse",
          direction: "incoming-candidate",
          url: "",
          parsed: parsePayload(params.data),
          rawText: params.data,
        });
      case "Runtime.consoleAPICalled":
        return this.onConsole(sessionId, params);
      case "Runtime.exceptionThrown":
        this.store.counts.pageErrors += 1;
        this.store.appendJsonl("errors.jsonl", {
          ts: new Date().toISOString(),
          text: String(params.exceptionDetails?.text || "exception"),
          url: params.exceptionDetails?.url || "",
        });
        return;
      case "Page.frameNavigated":
        if (!params.frame?.parentId) {
          const s = this.sessions.get(sessionId);
          if (s) s.url = params.frame.url;
          this.store.event("navigation", { url: this.safeUrl(params.frame?.url) });
          this.scheduleSnapshot(sessionId, "navigation");
        }
        return;
    }
  }

  async setupPage(sessionId, targetInfo) {
    if (this.sessions.has(sessionId)) return;
    this.sessions.set(sessionId, { targetId: targetInfo.targetId, url: targetInfo.url });
    const send = (m, p) => this.cdp.send(m, p, sessionId).catch(() => {});
    await send("Page.enable");
    await send("Runtime.enable");
    await send("Network.enable", {
      maxTotalBufferSize: 100 * 1024 * 1024,
      maxResourceBufferSize: 50 * 1024 * 1024,
      maxPostDataSize: 1024 * 1024,
    });
    await send("Page.addScriptToEvaluateOnNewDocument", { source: HOOKS_SOURCE });
    // For an already-loaded document, install hooks immediately too.
    await send("Runtime.evaluate", { expression: HOOKS_SOURCE });
    this.scheduleSnapshot(sessionId, "attach");
  }

  key(sessionId, requestId) {
    return `${sessionId}:${requestId}`;
  }

  onRequest(sessionId, p) {
    const req = p.request || {};
    const body = req.postData ? red.processBody(req.postData, this.redactEnabled(), this.config.maxBodyBytes) : null;
    const rec = {
      id: p.requestId,
      sessionId,
      startedAt: new Date().toISOString(),
      wallTime: p.wallTime,
      method: req.method,
      url: this.safeUrl(req.url),
      rawUrl: req.url,
      requestHeaders: red.redactHeaders(req.headers, this.redactEnabled()),
      requestBodyText: body ? body.text : "",
      requestBodyParsed: body ? body.parsed : null,
      resourceType: p.type || "",
      initiator: p.initiator ? { type: p.initiator.type } : null,
    };
    this.requests.set(this.key(sessionId, p.requestId), rec);
    this.store.counts.httpRequests += 1;
    this.store.coverage.http = true;
    this.store.event("http_request", { id: p.requestId, method: req.method, url: rec.url, type: p.type || "" });
    // Outgoing message candidate from request body.
    if (body && body.parsed) {
      const info = findMessageInfo(body.parsed);
      if (info && info.hasMessage && /POST|PUT|PATCH/i.test(req.method || "")) {
        this.recordCandidate({
          source: "http-request",
          direction: "outgoing-candidate",
          url: rec.url,
          eventName: info.eventName,
          messageText: info.text,
          author: info.author,
          parsed: body.parsed,
        });
      }
    }
  }

  onResponse(sessionId, p) {
    const rec = this.requests.get(this.key(sessionId, p.requestId));
    const resp = p.response || {};
    if (rec) {
      rec.status = resp.status;
      rec.statusText = resp.statusText || "";
      rec.responseHeaders = red.redactHeaders(resp.headers, this.redactEnabled());
      rec.mimeType = resp.mimeType || "";
      rec.protocol = resp.protocol || "";
      rec.remoteIPAddress = resp.remoteIPAddress || "";
      rec.remotePort = resp.remotePort || null;
    }
    this.store.counts.httpResponses += 1;
    this.store.event("http_response", {
      id: p.requestId,
      status: resp.status,
      url: this.safeUrl(resp.url),
      remoteIPAddress: resp.remoteIPAddress || "",
      protocol: resp.protocol || "",
    });
  }

  async onLoadingFinished(sessionId, p) {
    const rec = this.requests.get(this.key(sessionId, p.requestId));
    if (!rec) return;
    rec.durationMs = undefined;
    let bodyText = "";
    let bodyParsed = null;
    try {
      const body = await this.cdp.send("Network.getResponseBody", { requestId: p.requestId }, sessionId);
      if (body && typeof body.body === "string") {
        if (body.base64Encoded && !/json|text|xml|javascript/i.test(rec.mimeType || "")) {
          const binName = `http/${p.requestId.replace(/[^A-Za-z0-9._-]/g, "_")}.bin`;
          this.store.write(binName, Buffer.from(body.body, "base64"));
          bodyText = `[binary body saved to ${binName}]`;
        } else {
          const raw = body.base64Encoded ? Buffer.from(body.body, "base64").toString("utf8") : body.body;
          const processed = red.processBody(raw, this.redactEnabled(), this.config.maxBodyBytes);
          bodyText = processed.text;
          bodyParsed = processed.parsed;
        }
      }
    } catch {
      /* body may already be evicted; headers/status are still recorded */
    }
    rec.responseBodyText = bodyText;
    rec.responseBodyParsed = bodyParsed;
    this.harRecords.push(rec);
    this.store.writeJson(`http/${p.requestId.replace(/[^A-Za-z0-9._-]/g, "_")}.json`, {
      id: rec.id,
      startedAt: rec.startedAt,
      method: rec.method,
      url: rec.url,
      resourceType: rec.resourceType,
      requestHeaders: rec.requestHeaders,
      requestBody: rec.requestBodyParsed != null ? rec.requestBodyParsed : rec.requestBodyText || undefined,
      status: rec.status,
      statusText: rec.statusText,
      responseHeaders: rec.responseHeaders,
      mimeType: rec.mimeType,
      protocol: rec.protocol,
      remoteIPAddress: rec.remoteIPAddress,
      remotePort: rec.remotePort,
      responseBody: bodyParsed != null ? bodyParsed : bodyText || undefined,
    });
    this.catalog.add({
      method: rec.method,
      url: rec.rawUrl,
      status: rec.status,
      requestBody: rec.requestBodyParsed,
      responseBody: bodyParsed,
      ts: rec.startedAt,
    });
    // Incoming-data candidate from API responses carrying message text.
    if (bodyParsed) {
      const info = findMessageInfo(bodyParsed);
      if (info && info.hasMessage && /XHR|Fetch/i.test(rec.resourceType || "")) {
        this.recordCandidate({
          source: "http-response",
          direction: "incoming-candidate",
          url: rec.url,
          eventName: info.eventName,
          messageText: info.text,
          author: info.author,
          parsed: bodyParsed,
        });
      }
    }
    this.requests.delete(this.key(sessionId, p.requestId));
  }

  onLoadingFailed(sessionId, p) {
    const rec = this.requests.get(this.key(sessionId, p.requestId));
    this.store.event("http_failed", {
      id: p.requestId,
      url: rec ? rec.url : "",
      error: p.errorText || "",
      blockedReason: p.blockedReason || "",
    });
    if (rec) {
      this.harRecords.push({ ...rec, status: 0, statusText: p.errorText || "failed" });
      this.requests.delete(this.key(sessionId, p.requestId));
    }
  }

  onWsCreated(sessionId, p) {
    const info = { id: p.requestId, url: this.safeUrl(p.url), sessionId, createdAt: new Date().toISOString(), framesSent: 0, framesReceived: 0 };
    this.sockets.set(p.requestId, info);
    this.store.counts.wsConnections += 1;
    this.store.coverage.websockets = true;
    this.store.appendJsonl("websockets/index.jsonl", { ts: info.createdAt, event: "created", id: p.requestId, url: info.url });
    this.store.event("ws_created", { id: p.requestId, url: info.url });
    this.log(`[capture] WebSocket opened: ${info.url}`);
  }

  onWsHandshake(sessionId, p) {
    const resp = p.response || {};
    this.store.appendJsonl("websockets/index.jsonl", {
      ts: new Date().toISOString(),
      event: "handshake",
      id: p.requestId,
      status: resp.status,
      headers: red.redactHeaders(resp.headers, this.redactEnabled()),
    });
  }

  onWsClosed(sessionId, p) {
    const s = this.sockets.get(p.requestId);
    this.store.appendJsonl("websockets/index.jsonl", {
      ts: new Date().toISOString(),
      event: "closed",
      id: p.requestId,
      url: s ? s.url : "",
      framesSent: s ? s.framesSent : 0,
      framesReceived: s ? s.framesReceived : 0,
    });
    this.store.event("ws_closed", { id: p.requestId });
  }

  onWsFrame(sessionId, p, direction) {
    const frame = p.response || {};
    const s = this.sockets.get(p.requestId);
    if (s) {
      if (direction === "sent") s.framesSent += 1;
      else s.framesReceived += 1;
    }
    if (direction === "sent") this.store.counts.wsFramesSent += 1;
    else this.store.counts.wsFramesReceived += 1;

    let payloadText = frame.payloadData || "";
    let binary = false;
    if (frame.opcode === 2) {
      binary = true;
      try {
        payloadText = Buffer.from(payloadText, "base64").toString("utf8");
      } catch {
        payloadText = "";
      }
    }
    const parsed = binary && !payloadText ? { kind: "binary" } : parsePayload(payloadText);
    const processed =
      parsed.kind === "json" || parsed.kind === "socketio"
        ? { parsed: red.redactValue(parsed.value, this.redactEnabled()), text: red.truncate(JSON.stringify(red.redactValue(parsed.value, this.redactEnabled())), this.config.maxBodyBytes) }
        : { parsed: null, text: red.truncate(payloadText, this.config.maxBodyBytes) };
    const entry = {
      ts: new Date().toISOString(),
      direction,
      opcode: frame.opcode,
      url: s ? s.url : "",
      eventName: extractEventName(parsed),
      payload: processed.parsed != null ? processed.parsed : processed.text,
    };
    const file = `websockets/${String(p.requestId).replace(/[^A-Za-z0-9._-]/g, "_")}.jsonl`;
    this.store.appendJsonl(file, entry);
    this.store.event(direction === "sent" ? "ws_frame_sent" : "ws_frame_received", {
      id: p.requestId,
      url: entry.url,
      eventName: entry.eventName,
      preview: red.truncate(processed.text, 160),
    });
    this.wsCatalog.add({
      url: entry.url,
      eventName: entry.eventName,
      direction,
      payload: processed.parsed,
      ts: entry.ts,
    });
    this.checkCapabilities();
    const kind = classifyFrame(direction, parsed);
    if (kind) {
      const info = findMessageInfo(parsed.kind === "socketio" ? parsed.value : parsed.value);
      this.recordCandidate({
        source: "websocket",
        direction: kind,
        url: entry.url,
        eventName: entry.eventName,
        messageText: info ? info.text : null,
        author: info ? info.author : null,
        parsed: processed.parsed,
        rawText: processed.text,
        socketId: p.requestId,
      });
    }
  }

  recordCandidate(c) {
    const entry = {
      ts: new Date().toISOString(),
      tsMs: Date.now(),
      source: c.source,
      direction: c.direction,
      url: c.url ? this.safeUrl(c.url) : "",
      eventName: c.eventName || null,
      messageText: c.messageText || null,
      author: c.author || null,
      payload:
        c.parsed != null
          ? red.redactValue(c.parsed, this.redactEnabled())
          : c.rawText
            ? red.truncate(c.rawText, 2000)
            : undefined,
    };
    if (entry.direction && entry.direction.startsWith("incoming")) this.store.counts.incomingCandidates += 1;
    if (entry.direction && entry.direction.startsWith("outgoing")) this.store.counts.outgoingCandidates += 1;
    this.candidates.push(entry);
    if (this.candidates.length > 500) this.candidates.shift();
    if (entry.source !== "sse") {
      this.store.appendJsonl(
        entry.direction && entry.direction.startsWith("outgoing") ? "messages/outgoing-candidates.jsonl" : "messages/incoming-candidates.jsonl",
        entry,
      );
    }
    return entry;
  }

  onConsole(sessionId, p) {
    const args = p.args || [];
    const text = args.map((a) => (a.value != null ? String(a.value) : a.description || "")).join(" ");
    if (text.startsWith("__CAPTURE_HOOK__ ")) {
      let hook;
      try {
        hook = JSON.parse(text.slice("__CAPTURE_HOOK__ ".length));
      } catch {
        return;
      }
      this.store.counts.hookEvents += 1;
      this.store.appendJsonl("hooks.jsonl", hook);
      if (hook.type === "dom_message") {
        const dom = {
          ts: new Date().toISOString(),
          tsMs: Date.now(),
          directionHint: hook.directionHint,
          text: hook.text,
          className: hook.className,
          selector: hook.selector,
          html: hook.html,
          pageUrl: this.safeUrl(hook.pageUrl),
        };
        this.domMessages.push(dom);
        if (this.domMessages.length > 500) this.domMessages.shift();
        this.store.counts.domMessages += 1;
        this.store.coverage.domMessages = true;
        this.store.appendJsonl("messages/dom-messages.jsonl", dom);
        this.checkCapabilities();
      } else if (hook.type === "marker") {
        this.handleMarker(hook.label);
      }
      return;
    }
    this.store.counts.consoleMessages += 1;
    this.store.coverage.console = true;
    this.store.appendJsonl("console.jsonl", {
      ts: new Date().toISOString(),
      level: p.type || "log",
      text: red.truncate(text, 2000),
    });
  }

  handleMarker(label) {
    if (!label || label === "snapshot") {
      const sid = this.firstSessionId();
      if (sid) this.scheduleSnapshot(sid, "marker");
      return;
    }
    const now = Date.now();
    this.store.counts.markers += 1;
    if (label === "incoming") this.store.counts.incomingMarkers += 1;
    if (label === "outgoing") this.store.counts.outgoingMarkers += 1;
    this.store.coverage.markers = true;
    const candidate = [...this.candidates].reverse().find((c) => c.direction && c.direction.startsWith(label) && now - c.tsMs <= 15000);
    const dom = [...this.domMessages].reverse().find((d) => d.directionHint === label && now - d.tsMs <= 15000);
    const sample = {
      labeledAt: new Date().toISOString(),
      label,
      matched: Boolean(candidate || dom),
      candidate: candidate || null,
      domMessage: dom || null,
      note: candidate || dom ? "Nearest evidence within 15s before the marker." : "No matching evidence within 15s; widen the window when analysing correlation.json.",
    };
    this.store.appendJsonl(`messages/labeled-${label}.jsonl`, sample);
    this.store.event("marker", { label, matched: sample.matched });
    this.log(`[capture] Marker saved: ${label} (matched=${sample.matched}).`);
    this.checkCapabilities();
  }

  capabilities() {
    if (!this.store) return [];
    return computeCapabilities({ counts: this.store.counts, coverage: this.store.coverage });
  }

  checkCapabilities() {
    for (const cap of this.capabilities()) {
      if (cap.achieved && !this._announcedCaps.has(cap.key)) {
        this._announcedCaps.add(cap.key);
        this.log(`[capture] HOYE GECHE: ${cap.label} => labh: ${cap.enables}`);
      }
    }
  }

  firstSessionId() {
    for (const id of this.sessions.keys()) return id;
    return null;
  }

  scheduleSnapshot(sessionId, reason) {
    setTimeout(() => {
      this.takeSnapshot(sessionId, reason).catch(() => {});
    }, 700);
  }

  async evaluate(sessionId, expression) {
    const res = await this.cdp.send("Runtime.evaluate", { expression, returnByValue: true }, sessionId);
    if (res.exceptionDetails) throw new Error(res.exceptionDetails.text || "evaluate failed");
    return res.result ? res.result.value : undefined;
  }

  async takeSnapshot(sessionId, reason) {
    const dump = await this.evaluate(sessionId, DUMP_EXPR);
    if (!dump) return;
    const n = ++this.store.counts.snapshots;
    const base = `${String(n).padStart(3, "0")}-${reason}`;
    this.store.write(`dom/${base}.html`, String(dump.html || ""));
    const storage = {
      ts: new Date().toISOString(),
      reason,
      url: this.safeUrl(dump.url),
      title: dump.title,
      localStorage: red.redactValue(dump.localStorage || {}, this.redactEnabled()),
      sessionStorage: red.redactValue(dump.sessionStorage || {}, this.redactEnabled()),
    };
    try {
      const cookies = await this.cdp.send("Network.getCookies", { urls: [dump.url] }, sessionId);
      storage.cookies = (cookies.cookies || []).map((c) => ({
        name: c.name,
        domain: c.domain,
        path: c.path,
        value: this.redactEnabled() ? "REDACTED" : c.value,
        expires: c.expires,
        httpOnly: c.httpOnly,
        secure: c.secure,
      }));
    } catch {
      storage.cookies = [];
    }
    this.store.writeJson(`storage/${base}.json`, storage);
    this.store.coverage.domSnapshots = true;
    this.store.coverage.storage = true;
    try {
      const shot = await this.cdp.send("Page.captureScreenshot", { format: "png" }, sessionId);
      if (shot && shot.data) {
        this.store.write(`screenshots/${base}.png`, Buffer.from(shot.data, "base64"));
        this.store.coverage.screenshots = true;
      }
    } catch {
      /* screenshots are best-effort */
    }
    this.store.event("snapshot", { reason, url: storage.url, file: base });
    this.checkCapabilities();
  }

  buildCorrelation() {
    const out = [];
    for (const dom of this.domMessages) {
      const near = this.candidates
        .filter((c) => Math.abs(c.tsMs - dom.tsMs) <= 2500)
        .map((c) => {
          const textMatch =
            c.messageText &&
            dom.text &&
            (dom.text.includes(c.messageText) || c.messageText.includes(dom.text));
          return {
            source: c.source,
            direction: c.direction,
            eventName: c.eventName,
            deltaMs: c.tsMs - dom.tsMs,
            messageText: c.messageText,
            confidence: textMatch ? "high (text match)" : "time-window only",
          };
        })
        .sort((a, b) => Math.abs(a.deltaMs) - Math.abs(b.deltaMs))
        .slice(0, 5);
      out.push({ domMessage: dom, network: near });
    }
    return out;
  }

  writeCatalogs() {
    if (!this.store) return;
    try {
      this.store.writeJson("api-catalog.json", this.catalog.toJSON());
      this.store.writeJson("websockets/catalog.json", this.wsCatalog.toJSON());
    } catch {
      /* best effort during shutdown */
    }
  }

  schemaFingerprint() {
    const h = (arr) => crypto.createHash("sha256").update([...arr].sort().join("\n")).digest("hex");
    const api = this.catalog.toJSON().map((e) => e.pattern);
    const ws = this.wsCatalog.toJSON().map((e) => e.key);
    return { apiEndpoints: h(api), websocketEvents: h(ws), combined: h([...api, ...ws]) };
  }

  writeManifest(final) {
    if (!this.store) return;
    const manifest = {
      schema: 1,
      tool: "root-project capture",
      version: TOOL_VERSION,
      startedAt: this.startedAt.toISOString(),
      endedAt: final ? new Date().toISOString() : null,
      startUrl: this.safeUrl(this.config.startUrl),
      browser: this.browserPath || "",
      redactEnabled: this.redactEnabled(),
      liveBrowserNote: "User browsed manually in a visible browser; no site automation was performed by the tool.",
      counts: this.store.counts,
      capabilities: this.capabilities(),
      schemaFingerprint: this.schemaFingerprint(),
      coverage: this.store.coverage,
      files: {
        "events.jsonl": "Every capture event, one JSON per line, in order.",
        "network.har": "HTTP archive (redacted). Open in Chrome DevTools > Network > import.",
        "api-catalog.json": "Endpoint patterns with methods, statuses and body key shapes.",
        "http/*.json": "One file per HTTP request with headers and bodies (redacted).",
        "websockets/index.jsonl": "Socket lifecycle (created/handshake/closed).",
        "websockets/catalog.json": "Per-event WebSocket aggregate: directions, counts, payload key shapes. Used by compare.",
        "websockets/*.jsonl": "Frames per socket: direction, opcode, eventName, payload.",
        "messages/incoming-candidates.jsonl": "Received frames/responses that look like chat messages.",
        "messages/outgoing-candidates.jsonl": "Sent frames/requests that look like chat messages.",
        "messages/dom-messages.jsonl": "Message-like DOM nodes added while browsing.",
        "messages/labeled-incoming.jsonl": "User-confirmed examples (Ctrl+Alt+I). Ground truth.",
        "messages/labeled-outgoing.jsonl": "User-confirmed examples (Ctrl+Alt+O). Ground truth.",
        "messages/correlation.json": "DOM messages matched to nearby network evidence.",
        "dom/*.html": "Full page HTML snapshots.",
        "storage/*.json": "Cookies + local/session storage snapshots (redacted).",
        "screenshots/*.png": "Page screenshots at snapshot points.",
        "console.jsonl / errors.jsonl / hooks.jsonl": "Page console, JS errors, in-page hook evidence.",
      },
    };
    this.store.writeManifest(manifest);
    return manifest;
  }

  async stop(reason = "stop") {
    if (this.stopped) return;
    this.stopped = true;
    if (this._heartbeat) clearInterval(this._heartbeat);
    if (this._statusTimer) clearInterval(this._statusTimer);
    try {
      const sid = this.firstSessionId();
      if (sid && this.cdp) {
        await Promise.race([
          this.takeSnapshot(sid, "final"),
          new Promise((r) => setTimeout(r, 5000)),
        ]);
      }
    } catch {
      /* best effort */
    }
    try {
      this.writeCatalogs();
      this.store.writeJson("network.har", buildHar(this.harRecords, "root-project capture"));
      this.store.writeJson("messages/correlation.json", this.buildCorrelation());
      const caps = this.capabilities();
      const c = this.store.counts;
      this.store.write(
        "SUMMARY.md",
        [
          "# Capture summary — ki ki dhora poreche, ki kora jabe",
          "",
          `Session: ${this.store.dir}`,
          `Site: ${this.safeUrl(this.config.startUrl)}`,
          "",
          "## Capability checklist",
          "",
          ...renderCapabilityLines(caps),
          "",
          "## Numbers",
          "",
          `- HTTP request/response: ${c.httpRequests}/${c.httpResponses}`,
          `- WebSocket: ${c.wsConnections} socket, frame pathano ${c.wsFramesSent}, esheche ${c.wsFramesReceived}`,
          `- DOM messages: ${c.domMessages}, snapshots: ${c.snapshots}`,
          `- Markers: incoming ${c.incomingMarkers}, outgoing ${c.outgoingMarkers}`,
          "",
          "## Bot banate ja lagbe",
          "",
          caps.find((x) => x.key === "websocket" && x.achieved)
            ? "- WebSocket frames ache: `websockets/catalog.json` + `messages/correlation.json` dekhe realtime chat-er method ber kora jabe."
            : "- WebSocket frame paoya jayni: chat page-e giye kichukkhon chat kore abar capture korun.",
          caps.find((x) => x.key === "incoming" && x.achieved)
            ? "- Incoming confirmed: `messages/labeled-incoming.jsonl` holo ground truth."
            : "- Incoming confirm hoyni: porer bar onner message ele Ctrl+Alt+I chapen.",
          caps.find((x) => x.key === "outgoing" && x.achieved)
            ? "- Outgoing confirmed: `messages/labeled-outgoing.jsonl` holo ground truth."
            : "- Outgoing confirm hoyni: porer bar nije message pathiye Ctrl+Alt+O chapen.",
          "",
        ].join("\n"),
      );
      this.log("[capture] --- Final summary ---");
      for (const line of renderCapabilityLines(caps)) this.log("[capture] " + line);
      this.store.write(
        "SHARE-README.md",
        [
          "# Capture session — what to share",
          "",
          "Share this whole session folder (it is redacted by default). Start with:",
          "",
          "1. `manifest.json` — index, counts and coverage.",
          "2. `messages/labeled-*.jsonl` — examples you confirmed with Ctrl+Alt+I / Ctrl+Alt+O.",
          "3. `messages/correlation.json` — which network event produced which visible message.",
          "4. `api-catalog.json` + `websockets/*.jsonl` — endpoint and socket schemas.",
          "",
          "Do NOT share a session captured with rawMode=true: it can contain live tokens/cookies.",
          `Stop reason: ${reason}`,
          "",
        ].join("\n"),
      );
      this.writeManifest(true);
      this.store.event("session_stopped", { reason });
      this.log(`[capture] Saved: ${this.store.dir}`);
    } finally {
      try {
        if (this.cdp) await this.cdp.send("Browser.close").catch(() => {});
      } catch {
        /* ignore */
      }
      try {
        if (this.cdp) this.cdp.close();
      } catch {
        /* ignore */
      }
      try {
        if (this.child && !this.child.killed) this.child.kill();
      } catch {
        /* ignore */
      }
      this._resolveDone();
    }
  }
}

function loadConfig(captureDir, overrides = {}) {
  const defaults = {
    startUrl: "https://chitchat.gg/",
    browserPath: "",
    headless: false,
    rawMode: false,
    maxBodyBytes: 262144,
  };
  let fileCfg = {};
  const cfgPath = path.join(captureDir, "capture.config.json");
  if (fs.existsSync(cfgPath)) {
    try {
      fileCfg = JSON.parse(fs.readFileSync(cfgPath, "utf8"));
    } catch (e) {
      throw new Error(`capture.config.json is not valid JSON: ${e.message}`);
    }
  }
  return { ...defaults, ...fileCfg, ...overrides };
}

module.exports = { CaptureSession, loadConfig, TOOL_VERSION };
