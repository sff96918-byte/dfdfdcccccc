'use strict';
// Multi-site capture session: ONE visible browser, MANY sites, every event
// filed under the domain of the tab it happened in.
//
// Attribution rule (the thing this tool is for):
//   - a CDP page target (tab) has a top-level URL; its domain decides the
//     site folder for ALL events of that tab,
//   - requests that the page makes to other hosts (api.*, CDN, socket hosts)
//     still land in that same site folder, and each record keeps its own host,
//     so nothing is lost and nothing is mixed into another site,
//   - tabs are tracked separately: two sites in two tabs never share files,
//     and two tabs of the SAME site append to the same site folder.
//
// The capture engine itself (CDP client, hooks, redactor, classifier, catalogs,
// HAR, capability checklist) is the existing single-site tool's code, reused
// through deps.cjs — only routing and the per-domain layout are new.

const fs = require('node:fs');
const path = require('node:path');

const deps = require('./deps.cjs');
const { RunStore } = require('./sitestore.cjs');
const {
  domainOf,
  hostOf,
  siteKey,
  smsSummary,
  renderSiteSummary,
  renderRunSummary,
} = require('./sites.cjs');

const TOOL_VERSION = '0.1.0';

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

const FOCUS_EXPR = '(() => { try { return !!document.hasFocus(); } catch (e) { return false; } })()';

class MultiCaptureSession {
  constructor(options) {
    this.config = options.config || {};
    this.toolDir = options.toolDir || path.resolve(__dirname, '..');
    this.log = options.log || ((m) => console.log(m));
    this.disableTimers = !!options.disableTimers;
    this.cdpOverride = options.cdp || null; // tests inject a fake CDP client

    this.runtimes = new Map(); // site key -> SiteRuntime
    this.sessions = new Map(); // page sessionId -> {targetId, url, domain, tab, visits, lastActivity}
    this.frames = new Map(); // child sessionId (iframe/worker) -> {parentSessionId, url, type}
    this.targets = new Map(); // targetId -> {url, type}
    this.requests = new Map(); // `${sessionId}:${requestId}` -> record (record.site = key)
    this.sockets = new Map(); // `${sessionId}:${requestId}` -> socket info
    this.unattributed = 0;
    this.activeTab = null; // sessionId of the focused tab (best effort)
    this.startedAt = new Date();
    this.stopped = false;
    this._ready = false;
    this._browserDied = false;
    this._announced = new Set();
    this._done = new Promise((resolve) => (this._resolveDone = resolve));
  }

  get done() {
    return this._done;
  }

  redactEnabled() {
    return !this.config.rawMode;
  }

  safeUrl(url) {
    return deps.redactor().redactUrl(url || '', this.redactEnabled());
  }

  firstSessionId() {
    for (const id of this.sessions.keys()) return id;
    return null;
  }

  activeSessionId() {
    if (this.activeTab && this.sessions.has(this.activeTab)) return this.activeTab;
    return this.firstSessionId();
  }

  // ------------------------------------------------------------------ start
  async start() {
    const cfg = this.config;
    const browserMod = deps.browser();
    const browserPath = this.cdpOverride ? 'fake-cdp' : browserMod.findBrowser(cfg.browserPath || '');
    if (!browserPath) {
      throw new Error(
        'No Chrome/Edge/Chromium found. Install Google Chrome, or set browserPath in capture.config.json (or CAPTURE_BROWSER env var).'
      );
    }
    this.browserPath = browserPath;
    this.run = new RunStore(path.join(this.toolDir, 'sessions'));
    this.log(`[capture] Run folder: ${this.run.dir}`);
    this.log(`[capture] Per-site folders will appear as sites/<domain>/ — ek folder = ek site.`);
    if (this.redactEnabled()) this.log('[capture] Redaction ON (tokens/cookies/passwords hidden).');
    else this.log('[capture] WARNING: rawMode ON — secrets will be saved. DO NOT share these folders.');

    if (this.cdpOverride) {
      await this.attach(this.cdpOverride);
      return this;
    }

    const profileDir = this.resolveProfileDir();
    this.profileDir = profileDir;
    const port = await browserMod.getFreePort();
    this.browserLog = this.run.browserLogPath();
    this.child = browserMod.launchBrowser({
      browserPath,
      profileDir,
      url: cfg.startUrl || 'about:blank',
      headless: !!cfg.headless,
      port,
      logFile: this.browserLog,
    });
    this.child.on('exit', () => {
      this._browserDied = true;
      if (this._ready && !this.stopped) {
        this.log('[capture] Browser closed by user; finishing the run.');
        this.stop('browser-closed').catch(() => {});
      }
    });
    let version;
    try {
      const ready = await browserMod.waitForDevToolsEndpoint({
        port,
        profileDir,
        isAlive: () => !this._browserDied && this.child.exitCode === null,
        logFile: this.browserLog,
      });
      version = ready.version;
      const cdp = await new (deps.cdp().CDP)(version.webSocketDebuggerUrl).connect();
      await this.attach(cdp);
    } catch (error) {
      try {
        if (this.child && !this.child.killed) this.child.kill();
      } catch {
        /* ignore */
      }
      throw new Error(`Browser connect failed: ${error.message} (browser log: ${this.browserLog})`);
    }
    return this;
  }

  resolveProfileDir() {
    const configured = String(this.config.profileDir || '').trim();
    if (configured) return path.isAbsolute(configured) ? configured : path.resolve(this.toolDir, configured);
    return path.join(this.toolDir, 'profiles', 'browser');
  }

  async attach(cdp) {
    this.cdp = cdp;
    try {
      this.HOOKS_SOURCE = deps.hooksSource();
    } catch (error) {
      this.log('[capture] WARNING: in-page hooks unavailable: ' + error.message);
      this.HOOKS_SOURCE = '';
    }
    this.cdp.onEvent((msg) => this.onCdpEvent(msg).catch(() => {}));
    await this.cdp.send('Target.setDiscoverTargets', {discover: true}).catch(() => {});
    await this.cdp.send('Target.setAutoAttach', {autoAttach: true, waitForDebuggerOnStart: false, flatten: true}).catch(() => {});
    const { targetInfos } = (await this.cdp.send('Target.getTargets').catch(() => ({}))) || {};
    for (const info of targetInfos || []) {
      if (info.type === 'page') {
        this.targets.set(info.targetId, {url: info.url, type: info.type});
        try {
          await this.cdp.send('Target.attachToTarget', {targetId: info.targetId, flatten: true});
        } catch {
          /* already attached via auto-attach */
        }
      }
    }
    if (!this.disableTimers) this.startTimers();
    this._ready = true;
    this.log('[capture] Browser open. Ekhon ek ek kore site kholun: notun tab (Ctrl+T) -> site URL -> login -> chat.');
    this.log('[capture] Site switch korle folder nijei alada hobe: sessions/<run>/sites/<domain>/');
    this.log('[capture] Markers: Ctrl+Alt+I = incoming example, Ctrl+Alt+O = outgoing example, Ctrl+Alt+S = snapshot.');
    this.log('[capture] Terminal keys: i/o/s marker, l = site list, t = site table, q = stop (browser close).');
  }

  startTimers() {
    this._heartbeat = setInterval(() => {
      for (const runtime of this.runtimes.values()) this.writeSiteCatalogs(runtime);
      this.writeLiveManifest();
    }, 10000);
    this._focusTimer = setInterval(() => this.pollFocus(), 2500);
    this._statusTimer = setInterval(() => this.logStatusLine(), 20000);
  }

  logStatusLine() {
    const parts = [...this.runtimes.values()].map((runtime) => {
      const c = runtime.store.counts;
      return `${runtime.store.key}: HTTP ${c.httpRequests} WS ${c.wsFramesSent}/${c.wsFramesReceived} DOM ${c.domMessages} I:${c.incomingMarkers} O:${c.outgoingMarkers}`;
    });
    const mins = Math.max(0, Math.round((Date.now() - this.startedAt.getTime()) / 1000));
    this.log(
      `[live ${mins}s] sites ${this.runtimes.size}${parts.length ? ' | ' + parts.join(' | ') : ' — kono site ekhono dhora poreni'}`
    );
  }

  async pollFocus() {
    if (!this.cdp) return;
    for (const sessionId of [...this.sessions.keys()]) {
      try {
        const res = await this.cdp.send('Runtime.evaluate', {expression: FOCUS_EXPR, returnByValue: true}, sessionId);
        const focused = res && res.result ? res.result.value : false;
        if (focused) {
          if (this.activeTab !== sessionId) {
            this.activeTab = sessionId;
            const info = this.sessions.get(sessionId);
            if (info && info.domain) this.log(`[capture] Active tab now: ${info.domain} (${info.url ? this.safeUrl(info.url) : ''})`);
          }
          break;
        }
      } catch {
        /* tab may be gone or navigating */
      }
    }
  }

  // --------------------------------------------------------------- routing
  async onCdpEvent(msg) {
    const { method, params, sessionId } = msg || {};
    if (method === 'Target.attachedToTarget') {
      const info = params.targetInfo || {};
      const childSessionId = params.sessionId;
      if (info.type === 'page') await this.setupPage(childSessionId, info);
      else if (['iframe', 'worker', 'shared_worker', 'service_worker'].includes(info.type)) {
        // A chat embedded in an iframe (or its worker) keeps the parent tab's
        // site folder: the files stay together with the page the operator is on.
        await this.setupChildTarget(childSessionId, info, sessionId);
      }
      return;
    }
    if (method === 'Target.targetInfoChanged') {
      const info = params.targetInfo || {};
      if (info.type === 'page') return this.onTargetInfo(info);
      return;
    }
    if (method === 'Target.targetDestroyed') return this.onTargetDestroyed(params.targetId);
    // Child sessions (iframe/worker) carry network + hook events too; they are
    // routed to the tab they belong to instead of being dropped.
    if (!sessionId || (!this.sessions.has(sessionId) && !this.frames.has(sessionId))) return;
    switch (method) {
      case 'Network.requestWillBeSent':
        return this.onRequest(sessionId, params);
      case 'Network.responseReceived':
        return this.onResponse(sessionId, params);
      case 'Network.loadingFinished':
        return this.onLoadingFinished(sessionId, params);
      case 'Network.loadingFailed':
        return this.onLoadingFailed(sessionId, params);
      case 'Network.webSocketCreated':
        return this.onWsCreated(sessionId, params);
      case 'Network.webSocketHandshakeResponseReceived':
        return this.onWsHandshake(sessionId, params);
      case 'Network.webSocketFrameSent':
        return this.onWsFrame(sessionId, params, 'sent');
      case 'Network.webSocketFrameReceived':
        return this.onWsFrame(sessionId, params, 'received');
      case 'Network.webSocketClosed':
        return this.onWsClosed(sessionId, params);
      case 'Network.eventSourceMessageReceived': {
        const runtime = this.runtimeOf(sessionId);
        if (!runtime) return;
        runtime.store.coverage.websockets = true;
        return this.recordCandidate(runtime, {
          source: 'sse',
          direction: 'incoming-candidate',
          url: '',
          parsed: deps.classifier().parsePayload(params.data),
          rawText: params.data,
        });
      }
      case 'Runtime.consoleAPICalled':
        return this.onConsole(sessionId, params);
      case 'Runtime.exceptionThrown': {
        const runtime = this.runtimeOf(sessionId);
        if (!runtime) return;
        runtime.store.counts.pageErrors += 1;
        runtime.store.appendJsonl('errors.jsonl', {
          ts: new Date().toISOString(),
          text: String(params.exceptionDetails?.text || 'exception'),
          url: this.safeUrl(params.exceptionDetails?.url || ''),
        });
        return;
      }
      case 'Page.frameNavigated':
        return this.onFrameNavigated(sessionId, params);
      case 'Page.frameDetached':
        return;
      default:
        return;
    }
  }

  async setupPage(sessionId, targetInfo) {
    if (this.sessions.has(sessionId)) return;
    const url = targetInfo.url || '';
    const siteDomain = domainOf(url);
    const info = {
      targetId: targetInfo.targetId,
      url,
      domain: siteDomain ? siteKey(siteDomain) : '', // site key ('' until a real site page is known)
      tab: String(sessionId).slice(0, 8),
      openedAt: new Date().toISOString(),
      lastActivity: Date.now(),
    };
    this.sessions.set(sessionId, info);
    this.targets.set(targetInfo.targetId, {url, type: 'page'});
    const send = (m, p) => this.cdp.send(m, p, sessionId).catch(() => {});
    await send('Page.enable');
    await send('Runtime.enable');
    await send('Network.enable', {
      maxTotalBufferSize: 100 * 1024 * 1024,
      maxResourceBufferSize: 50 * 1024 * 1024,
      maxPostDataSize: 1024 * 1024,
    });
    if (this.HOOKS_SOURCE) {
      await send('Page.addScriptToEvaluateOnNewDocument', {source: this.HOOKS_SOURCE});
      await send('Runtime.evaluate', {expression: this.HOOKS_SOURCE});
    }
    if (siteDomain && info.domain) this.noteSite(info, url, 'tab-attached');
    this.scheduleSnapshot(sessionId, 'attach');
  }

  // Child target (iframe/worker): same Network/Runtime/hook wiring as a page,
  // but no site of its own — it inherits the tab it belongs to.
  async setupChildTarget(sessionId, info, parentSessionId) {
    if (this.frames.has(sessionId) || this.sessions.has(sessionId)) return;
    this.frames.set(sessionId, {parentSessionId: parentSessionId || null, url: info.url || '', type: info.type, targetId: info.targetId});
    const send = (m, p) => this.cdp.send(m, p, sessionId).catch(() => {});
    await send('Page.enable');
    await send('Runtime.enable');
    await send('Network.enable', {maxTotalBufferSize: 50 * 1024 * 1024, maxResourceBufferSize: 25 * 1024 * 1024});
    if (this.HOOKS_SOURCE) {
      await send('Page.addScriptToEvaluateOnNewDocument', {source: this.HOOKS_SOURCE});
      await send('Runtime.evaluate', {expression: this.HOOKS_SOURCE});
    }
  }

  // Walk from a child session up to the page (tab) it belongs to.
  resolvePageSession(sessionId) {
    let id = sessionId;
    for (let guard = 0; id && guard < 20; guard += 1) {
      if (this.sessions.has(id)) return id;
      const link = this.frames.get(id);
      if (!link || !link.parentSessionId) return null;
      id = link.parentSessionId;
    }
    return null;
  }

  pageSessionInfo(sessionId) {
    const id = this.resolvePageSession(sessionId);
    return id ? {id, info: this.sessions.get(id)} : null;
  }

  onTargetInfo(info) {
    this.targets.set(info.targetId, {url: info.url, type: info.type});
    for (const [sessionId, session] of this.sessions) {
      if (session.targetId !== info.targetId) continue;
      if (info.url && info.url !== session.url) session.url = info.url;
      const nextDomain = domainOf(info.url);
      // A tab that was blank when it was attached gets its site folder here.
      if (nextDomain && nextDomain !== session.domain) this.noteSite(session, info.url, 'tab-updated');
    }
  }

  onTargetDestroyed(targetId) {
    for (const [sessionId, link] of [...this.frames]) {
      if (link.targetId === targetId) this.frames.delete(sessionId);
    }
    this.targets.delete(targetId);
    for (const [sessionId, session] of this.sessions) {
      if (session.targetId !== targetId) continue;
      const runtime = session.domain ? this.runtimes.get(session.domain) : null;
      if (runtime) {
        runtime.store.appendJsonl('visits.jsonl', {
          ts: new Date().toISOString(),
          event: 'tab_closed',
          tab: session.tab,
          url: this.safeUrl(session.url),
        });
        runtime.store.event('tab_closed', {tab: session.tab, url: this.safeUrl(session.url)});
      }
      this.sessions.delete(sessionId);
      if (this.activeTab === sessionId) this.activeTab = null;
      this.log(`[capture] Tab closed (${session.domain || 'unknown site'}).`);
    }
  }

  // Give a tab its site folder (and count a visit when it re-enters later).
  noteSite(session, url, reason) {
    const domain = domainOf(url);
    if (!domain) return null;
    const key = siteKey(domain);
    const changed = session.domain !== key;
    session.domain = key;
    if (url) session.url = url;
    const runtime = this.runtimeFor(domain);
    session.lastActivity = Date.now(); // a visited tab gets its final snapshot too
    const host = hostOf(url);
    runtime.store.noteHost(host, domain);
    const now = Date.now();
    if (changed || !runtime.lastVisitAt || now - runtime.lastVisitAt > 60000) {
      runtime.store.visits += 1;
      runtime.store.appendJsonl('visits.jsonl', {
        ts: new Date().toISOString(),
        event: 'visit_started',
        reason,
        tab: session.tab,
        url: this.safeUrl(url),
      });
      runtime.store.event('visit_started', {tab: session.tab, url: this.safeUrl(url), reason});
      runtime.lastVisitAt = now;
    }
    if (changed) {
      this.log(`[capture] Site: ${runtime.domain}  ->  ${runtime.store.relativeDir}/   (${this.runtimes.size} site(s) in this run)`);
    }
    return runtime;
  }

  runtimeFor(domain) {
    const key = siteKey(domain);
    let runtime = this.runtimes.get(key);
    if (!runtime) {
      const store = this.run.site(domain);
      const catalogMod = deps.catalog();
      const wsMod = deps.wscatalog();
      runtime = {
        key,
        domain,
        store,
        catalog: new catalogMod.Catalog(),
        wsCatalog: new wsMod.WsCatalog(),
        harRecords: [],
        markers: [],
        candidates: [],
        domMessages: [],
        announced: new Set(),
        lastVisitAt: 0,
      };
      this.runtimes.set(key, runtime);
      this.log(`[capture] NEW SITE: ${domain} => ${store.relativeDir}/  (ek site = ek folder, kono mix hobe na)`);
      store.event('site_folder_created', {domain, tool: 'multi-capture', version: TOOL_VERSION});
    }
    return runtime;
  }

  runtimeOf(sessionId) {
    const resolved = this.pageSessionInfo(sessionId);
    const session = resolved && resolved.info;
    if (!session || !session.domain) {
      this.unattributed += 1;
      return null;
    }
    session.lastActivity = Date.now();
    return this.runtimes.get(session.domain) || null;
  }

  onFrameNavigated(sessionId, params) {
    const frame = params.frame || {};
    if (!this.sessions.has(sessionId)) {
      // Navigation inside a child target (iframe/worker): only remember the host.
      const resolved = this.pageSessionInfo(sessionId);
      const host = hostOf(frame.url);
      if (resolved && resolved.info.domain && host) {
        const runtime = this.runtimes.get(resolved.info.domain);
        if (runtime) runtime.store.noteHost(host, runtime.domain);
      }
      return;
    }
    if (frame.parentId) {
      // Sub-frame (iframe/video widget): remember its host on the same site so
      // the operator can see which third parties a site pulls in.
      const session = this.sessions.get(sessionId);
      const host = hostOf(frame.url);
      if (session && session.domain && host) {
        const runtime = this.runtimes.get(session.domain);
        if (runtime) runtime.store.noteHost(host, session.domain);
      }
      return;
    }
    const session = this.sessions.get(sessionId);
    if (!session) return;
    const runtime = this.noteSite(session, frame.url, 'navigation');
    if (!runtime) return;
    runtime.store.event('navigation', {tab: session.tab, url: this.safeUrl(frame.url)});
    this.scheduleSnapshot(sessionId, 'navigation');
  }

  // ------------------------------------------------------------------ http
  onRequest(sessionId, params) {
    const runtime = this.runtimeOf(sessionId);
    const req = params.request || {};
    const body = req.postData ? deps.redactor().processBody(req.postData, this.redactEnabled(), this.config.maxBodyBytes) : null;
    const rec = {
      id: params.requestId,
      sessionId,
      site: runtime ? runtime.key : '',
      startedAt: new Date().toISOString(),
      wallTime: params.wallTime,
      method: req.method,
      url: this.safeUrl(req.url),
      rawUrl: req.url,
      requestHeaders: deps.redactor().redactHeaders(req.headers, this.redactEnabled()),
      requestBodyText: body ? body.text : '',
      requestBodyParsed: body ? body.parsed : null,
      resourceType: params.type || '',
      initiator: params.initiator ? {type: params.initiator.type} : null,
    };
    this.requests.set(`${sessionId}:${params.requestId}`, rec);
    if (!runtime) return;
    const host = hostOf(req.url);
    runtime.store.noteHost(host, runtime.domain);
    runtime.store.counts.httpRequests += 1;
    runtime.store.coverage.http = true;
    runtime.store.event('http_request', {id: params.requestId, method: req.method, url: rec.url, host, type: params.type || ''});
    if (body && body.parsed) {
      const info = deps.classifier().findMessageInfo(body.parsed);
      if (info && info.hasMessage && /POST|PUT|PATCH/i.test(req.method || '')) {
        this.recordCandidate(runtime, {
          source: 'http-request',
          direction: 'outgoing-candidate',
          url: rec.url,
          method: req.method,
          eventName: info.eventName,
          messageText: info.text,
          author: info.author,
          parsed: body.parsed,
        });
      }
    }
  }

  onResponse(sessionId, params) {
    const rec = this.requests.get(`${sessionId}:${params.requestId}`);
    const resp = params.response || {};
    if (rec) {
      rec.status = resp.status;
      rec.statusText = resp.statusText || '';
      rec.responseHeaders = deps.redactor().redactHeaders(resp.headers, this.redactEnabled());
      rec.mimeType = resp.mimeType || '';
      rec.protocol = resp.protocol || '';
      rec.remoteIPAddress = resp.remoteIPAddress || '';
      rec.remotePort = resp.remotePort || null;
    }
    const runtime = this.runtimeOf(sessionId);
    if (!runtime) return;
    runtime.store.counts.httpResponses += 1;
    runtime.store.event('http_response', {
      id: params.requestId,
      status: resp.status,
      url: this.safeUrl(resp.url),
      remoteIPAddress: resp.remoteIPAddress || '',
      protocol: resp.protocol || '',
    });
  }

  async onLoadingFinished(sessionId, params) {
    const rec = this.requests.get(`${sessionId}:${params.requestId}`);
    if (!rec) return;
    const runtime = this.runtimes.get(rec.site) || this.runtimeOf(sessionId);
    let bodyText = '';
    let bodyParsed = null;
    try {
      const body = await this.cdp.send('Network.getResponseBody', {requestId: params.requestId}, sessionId);
      if (body && typeof body.body === 'string') {
        if (body.base64Encoded && !/json|text|xml|javascript/i.test(rec.mimeType || '')) {
          const name = `http/${String(params.requestId).replace(/[^A-Za-z0-9._-]/g, '_')}.bin`;
          if (runtime) runtime.store.write(name, Buffer.from(body.body, 'base64'));
          bodyText = `[binary body saved to ${name}]`;
        } else {
          const raw = body.base64Encoded ? Buffer.from(body.body, 'base64').toString('utf8') : body.body;
          const processed = deps.redactor().processBody(raw, this.redactEnabled(), this.config.maxBodyBytes);
          bodyText = processed.text;
          bodyParsed = processed.parsed;
        }
      }
    } catch {
      /* body may already be evicted; headers/status are still recorded */
    }
    rec.responseBodyText = bodyText;
    rec.responseBodyParsed = bodyParsed;
    if (runtime) {
      runtime.harRecords.push(rec);
      runtime.store.writeJson(`http/${String(params.requestId).replace(/[^A-Za-z0-9._-]/g, '_')}.json`, {
        id: rec.id,
        site: rec.site,
        startedAt: rec.startedAt,
        method: rec.method,
        url: rec.url,
        host: hostOf(rec.rawUrl),
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
      runtime.catalog.add({
        method: rec.method,
        url: rec.rawUrl,
        status: rec.status,
        requestBody: rec.requestBodyParsed,
        responseBody: bodyParsed,
        ts: rec.startedAt,
      });
      if (bodyParsed) {
        const info = deps.classifier().findMessageInfo(bodyParsed);
        if (info && info.hasMessage && /XHR|Fetch/i.test(rec.resourceType || '')) {
          this.recordCandidate(runtime, {
            source: 'http-response',
            direction: 'incoming-candidate',
            url: rec.url,
            method: rec.method,
            eventName: info.eventName,
            messageText: info.text,
            author: info.author,
            parsed: bodyParsed,
          });
        }
      }
    }
    this.requests.delete(`${sessionId}:${params.requestId}`);
  }

  onLoadingFailed(sessionId, params) {
    const rec = this.requests.get(`${sessionId}:${params.requestId}`);
    const runtime = (rec && this.runtimes.get(rec.site)) || this.runtimeOf(sessionId);
    if (runtime) {
      runtime.store.event('http_failed', {
        id: params.requestId,
        url: rec ? rec.url : '',
        error: params.errorText || '',
        blockedReason: params.blockedReason || '',
      });
      if (rec) runtime.harRecords.push({...rec, status: 0, statusText: params.errorText || 'failed'});
    }
    if (rec) this.requests.delete(`${sessionId}:${params.requestId}`);
  }

  // ------------------------------------------------------------- websocket
  socketKey(sessionId, requestId) {
    return `${sessionId}:${requestId}`;
  }

  onWsCreated(sessionId, params) {
    const runtime = this.runtimeOf(sessionId);
    const key = this.socketKey(sessionId, params.requestId);
    const info = {
      id: params.requestId,
      key,
      site: runtime ? runtime.key : '',
      url: this.safeUrl(params.url),
      createdAt: new Date().toISOString(),
      framesSent: 0,
      framesReceived: 0,
    };
    this.sockets.set(key, info);
    if (!runtime) return;
    runtime.store.noteHost(hostOf(params.url), runtime.domain);
    runtime.store.counts.wsConnections += 1;
    runtime.store.coverage.websockets = true;
    runtime.store.appendJsonl('websockets/index.jsonl', {
      ts: info.createdAt,
      event: 'created',
      id: key,
      url: info.url,
      host: hostOf(params.url),
    });
    runtime.store.event('ws_created', {id: key, url: info.url});
    this.log(`[capture] ${runtime.domain}: WebSocket opened: ${info.url}`);
  }

  onWsHandshake(sessionId, params) {
    const runtime = this.runtimeOf(sessionId);
    if (!runtime) return;
    const resp = params.response || {};
    runtime.store.appendJsonl('websockets/index.jsonl', {
      ts: new Date().toISOString(),
      event: 'handshake',
      id: this.socketKey(sessionId, params.requestId),
      status: resp.status,
      headers: deps.redactor().redactHeaders(resp.headers, this.redactEnabled()),
    });
  }

  onWsClosed(sessionId, params) {
    const key = this.socketKey(sessionId, params.requestId);
    const socket = this.sockets.get(key);
    const runtime = socket && socket.site ? this.runtimes.get(socket.site) : this.runtimeOf(sessionId);
    if (!runtime) return;
    runtime.store.appendJsonl('websockets/index.jsonl', {
      ts: new Date().toISOString(),
      event: 'closed',
      id: key,
      url: socket ? socket.url : '',
      framesSent: socket ? socket.framesSent : 0,
      framesReceived: socket ? socket.framesReceived : 0,
    });
    runtime.store.event('ws_closed', {id: key});
  }

  onWsFrame(sessionId, params, direction) {
    const runtime = this.runtimeOf(sessionId);
    const key = this.socketKey(sessionId, params.requestId);
    const socket = this.sockets.get(key);
    if (socket) {
      if (direction === 'sent') socket.framesSent += 1;
      else socket.framesReceived += 1;
    }
    const frame = params.response || {};
    if (!runtime) {
      if (frame.payloadData) this.unattributed += 1;
      return;
    }
    if (direction === 'sent') runtime.store.counts.wsFramesSent += 1;
    else runtime.store.counts.wsFramesReceived += 1;

    let payloadText = frame.payloadData || '';
    let binary = false;
    if (frame.opcode === 2) {
      binary = true;
      try {
        payloadText = Buffer.from(payloadText, 'base64').toString('utf8');
      } catch {
        payloadText = '';
      }
    }
    const parsed = binary && !payloadText ? {kind: 'binary'} : deps.classifier().parsePayload(payloadText);
    const processed =
      parsed.kind === 'json' || parsed.kind === 'socketio'
        ? {
            parsed: deps.redactor().redactValue(parsed.value, this.redactEnabled()),
            text: deps.redactor().truncate(
              JSON.stringify(deps.redactor().redactValue(parsed.value, this.redactEnabled())),
              this.config.maxBodyBytes
            ),
          }
        : {parsed: null, text: deps.redactor().truncate(payloadText, this.config.maxBodyBytes)};
    const entry = {
      ts: new Date().toISOString(),
      direction,
      opcode: frame.opcode,
      url: socket ? socket.url : '',
      eventName: deps.classifier().extractEventName(parsed),
      payload: processed.parsed != null ? processed.parsed : processed.text,
    };
    const file = `websockets/${key.replace(/[^A-Za-z0-9._-]/g, '_')}.jsonl`;
    runtime.store.appendJsonl(file, entry);
    runtime.store.event(direction === 'sent' ? 'ws_frame_sent' : 'ws_frame_received', {
      id: key,
      url: entry.url,
      eventName: entry.eventName,
      preview: deps.redactor().truncate(processed.text, 160),
    });
    runtime.wsCatalog.add({
      url: entry.url,
      eventName: entry.eventName,
      direction,
      payload: processed.parsed,
      ts: entry.ts,
    });
    this.checkCapabilities(runtime);
    const kind = deps.classifier().classifyFrame(direction, parsed);
    if (kind) {
      const info = deps.classifier().findMessageInfo(parsed.value);
      this.recordCandidate(runtime, {
        source: 'websocket',
        direction: kind,
        url: entry.url,
        eventName: entry.eventName,
        messageText: info ? info.text : null,
        author: info ? info.author : null,
        parsed: processed.parsed,
        rawText: processed.text,
        socketId: key,
      });
    }
  }

  // -------------------------------------------------------------- messages
  recordCandidate(runtime, candidate) {
    const entry = {
      ts: new Date().toISOString(),
      tsMs: Date.now(),
      site: runtime.key,
      source: candidate.source,
      direction: candidate.direction,
      url: candidate.url ? this.safeUrl(candidate.url) : '',
      method: candidate.method || null,
      eventName: candidate.eventName || null,
      messageText: candidate.messageText || null,
      author: candidate.author || null,
      payload:
        candidate.parsed != null
          ? deps.redactor().redactValue(candidate.parsed, this.redactEnabled())
          : candidate.rawText
            ? deps.redactor().truncate(candidate.rawText, 2000)
            : undefined,
    };
    if (entry.direction.startsWith('incoming')) runtime.store.counts.incomingCandidates += 1;
    if (entry.direction.startsWith('outgoing')) runtime.store.counts.outgoingCandidates += 1;
    runtime.candidates.push(entry);
    if (runtime.candidates.length > 500) runtime.candidates.shift();
    if (entry.source !== 'sse') {
      runtime.store.appendJsonl(
        entry.direction.startsWith('outgoing') ? 'messages/outgoing-candidates.jsonl' : 'messages/incoming-candidates.jsonl',
        entry
      );
    }
    return entry;
  }

  onConsole(sessionId, params) {
    const args = params.args || [];
    const text = args.map((a) => (a.value != null ? String(a.value) : a.description || '')).join(' ');
    if (text.startsWith('__CAPTURE_HOOK__ ')) {
      const runtime = this.runtimeOf(sessionId);
      let hook;
      try {
        hook = JSON.parse(text.slice('__CAPTURE_HOOK__ '.length));
      } catch {
        return;
      }
      if (!runtime) return;
      const pageInfo = this.pageSessionInfo(sessionId);
      runtime.store.counts.hookEvents += 1;
      runtime.store.appendJsonl('hooks.jsonl', {...hook, site: runtime.key, tab: pageInfo ? pageInfo.info.tab : ''});
      if (hook.type === 'dom_message') {
        const dom = {
          ts: new Date().toISOString(),
          tsMs: Date.now(),
          site: runtime.key,
          directionHint: hook.directionHint,
          text: hook.text,
          className: hook.className,
          selector: hook.selector,
          html: hook.html,
          pageUrl: this.safeUrl(hook.pageUrl),
        };
        runtime.domMessages.push(dom);
        if (runtime.domMessages.length > 500) runtime.domMessages.shift();
        runtime.store.counts.domMessages += 1;
        runtime.store.coverage.domMessages = true;
        runtime.store.appendJsonl('messages/dom-messages.jsonl', dom);
        this.checkCapabilities(runtime);
      } else if (hook.type === 'marker') {
        this.handleMarker(hook.label, {sessionId, pageUrl: hook.pageUrl});
      } else if (hook.type === 'hooks_installed' || hook.type === 'observer_started') {
        if (hook.pageUrl) {
          const host = hostOf(hook.pageUrl);
          if (host) runtime.store.noteHost(host, runtime.domain);
        }
      }
      return;
    }
    const runtime = this.runtimeOf(sessionId);
    if (!runtime) return;
    runtime.store.counts.consoleMessages += 1;
    runtime.store.coverage.console = true;
    runtime.store.appendJsonl('console.jsonl', {
      ts: new Date().toISOString(),
      level: params.type || 'log',
      text: deps.redactor().truncate(text, 2000),
    });
  }

  handleMarker(label, context = {}) {
    if (!label || label === 'snapshot') {
      const sessionId = context.sessionId || this.activeSessionId();
      if (sessionId) this.scheduleSnapshot(sessionId, 'marker');
      return;
    }
    let sessionId = context.sessionId || this.activeSessionId();
    const resolved = sessionId ? this.pageSessionInfo(sessionId) : null;
    if (resolved) sessionId = resolved.id;
    const session = resolved ? resolved.info : null;
    if (!session || !session.domain) {
      this.unattributed += 1;
      this.log('[capture] Marker ignored: kono site page ekhono dhora poreni (browser-e site kholun, tarpor Ctrl+Alt+I/O).');
      return;
    }
    const runtime = this.runtimes.get(session.domain);
    if (!runtime) return;
    if (context.pageUrl) {
      const host = hostOf(context.pageUrl);
      if (host) runtime.store.noteHost(host, runtime.domain);
    }
    // Set the focused tab when the marker came from a page — that is the site
    // the operator is actually looking at right now.
    this.activeTab = sessionId;
    const now = Date.now();
    runtime.store.counts.markers += 1;
    if (label === 'incoming') runtime.store.counts.incomingMarkers += 1;
    if (label === 'outgoing') runtime.store.counts.outgoingMarkers += 1;
    runtime.store.coverage.markers = true;
    const candidate = [...runtime.candidates].reverse().find((c) => c.direction.startsWith(label) && now - c.tsMs <= 15000);
    const dom = [...runtime.domMessages].reverse().find((d) => d.directionHint === label && now - d.tsMs <= 15000);
    const sample = {
      labeledAt: new Date().toISOString(),
      site: runtime.key,
      tab: session.tab,
      label,
      matched: Boolean(candidate || dom),
      candidate: candidate || null,
      domMessage: dom || null,
      note:
        candidate || dom
          ? `Nearest ${label} evidence within 15s before the marker (this site only).`
          : 'No matching evidence within 15s; the site may send this differently — capture again with the chat open.',
    };
    runtime.store.appendJsonl(`messages/labeled-${label}.jsonl`, sample);
    runtime.markers.push(sample);
    runtime.store.event('marker', {label, matched: sample.matched, tab: session.tab});
    this.log(`[capture] ${runtime.domain}: marker saved: ${label} (matched=${sample.matched}).`);
    this.checkCapabilities(runtime);
  }

  checkCapabilities(runtime) {
    for (const cap of this.capabilities(runtime)) {
      if (cap.achieved && !runtime.announced.has(cap.key)) {
        runtime.announced.add(cap.key);
        this.log(`[capture] ${runtime.domain}: HOYE GECHE: ${cap.label} => labh: ${cap.enables}`);
      }
    }
  }

  capabilities(runtime) {
    return deps.capabilities().computeCapabilities({counts: runtime.store.counts, coverage: runtime.store.coverage});
  }

  // -------------------------------------------------------------- snapshots
  scheduleSnapshot(sessionId, reason) {
    if (this.disableTimers) return;
    setTimeout(() => {
      this.takeSnapshot(sessionId, reason).catch(() => {});
    }, 700);
  }

  async evaluate(sessionId, expression) {
    const res = await this.cdp.send('Runtime.evaluate', {expression, returnByValue: true}, sessionId);
    if (res.exceptionDetails) throw new Error(res.exceptionDetails.text || 'evaluate failed');
    return res.result ? res.result.value : undefined;
  }

  async takeSnapshot(sessionId, reason) {
    const session = this.sessions.get(sessionId);
    if (!session) return;
    const runtime = session.domain ? this.runtimes.get(session.domain) : null;
    const dump = await this.evaluate(sessionId, DUMP_EXPR);
    if (!dump) return;
    const target = runtime || this.runtimeFor(domainOf(dump.url) || 'unknown');
    const n = ++target.store.counts.snapshots;
    const base = `${String(n).padStart(3, '0')}-${String(reason).replace(/[^A-Za-z0-9._-]/g, '_')}`;
    target.store.write(`dom/${base}.html`, String(dump.html || ''));
    const storage = {
      ts: new Date().toISOString(),
      site: target.key,
      tab: session.tab,
      reason,
      url: this.safeUrl(dump.url),
      title: dump.title,
      localStorage: deps.redactor().redactValue(dump.localStorage || {}, this.redactEnabled()),
      sessionStorage: deps.redactor().redactValue(dump.sessionStorage || {}, this.redactEnabled()),
    };
    try {
      const cookies = await this.cdp.send('Network.getCookies', {urls: [dump.url]}, sessionId);
      storage.cookies = (cookies.cookies || []).map((c) => ({
        name: c.name,
        domain: c.domain,
        path: c.path,
        value: this.redactEnabled() ? 'REDACTED' : c.value,
        expires: c.expires,
        httpOnly: c.httpOnly,
        secure: c.secure,
      }));
    } catch {
      storage.cookies = [];
    }
    target.store.writeJson(`storage/${base}.json`, storage);
    target.store.coverage.domSnapshots = true;
    target.store.coverage.storage = true;
    try {
      const shot = await this.cdp.send('Page.captureScreenshot', {format: 'png'}, sessionId);
      if (shot && shot.data) {
        target.store.write(`screenshots/${base}.png`, Buffer.from(shot.data, 'base64'));
        target.store.coverage.screenshots = true;
      }
    } catch {
      /* screenshots are best-effort */
    }
    target.store.event('snapshot', {reason, tab: session.tab, url: storage.url, file: base});
    this.checkCapabilities(target);
  }

  // --------------------------------------------------------------- reporting
  buildCorrelation(runtime) {
    const out = [];
    for (const dom of runtime.domMessages) {
      const near = runtime.candidates
        .filter((c) => Math.abs(c.tsMs - dom.tsMs) <= 2500)
        .map((c) => {
          const textMatch = c.messageText && dom.text && (dom.text.includes(c.messageText) || c.messageText.includes(dom.text));
          return {
            source: c.source,
            direction: c.direction,
            eventName: c.eventName,
            deltaMs: c.tsMs - dom.tsMs,
            messageText: c.messageText,
            confidence: textMatch ? 'high (text match)' : 'time-window only',
          };
        })
        .sort((a, b) => Math.abs(a.deltaMs) - Math.abs(b.deltaMs))
        .slice(0, 5);
      out.push({domMessage: dom, network: near});
    }
    return out;
  }

  writeSiteCatalogs(runtime) {
    try {
      runtime.store.writeJson('api-catalog.json', runtime.catalog.toJSON());
      runtime.store.writeJson('websockets/catalog.json', runtime.wsCatalog.toJSON());
    } catch {
      /* best effort during shutdown */
    }
  }

  siteIndexEntry(runtime) {
    const caps = this.capabilities(runtime);
    return {
      key: runtime.key,
      domain: runtime.domain,
      dir: runtime.store.relativeDir,
      firstSeen: runtime.store.firstSeen,
      lastSeen: runtime.store.lastSeen,
      visits: runtime.store.visits,
      hosts: [...runtime.store.hosts].sort(),
      counts: runtime.store.counts,
      coverage: runtime.store.coverage,
      capabilities: caps.map((c) => ({key: c.key, label: c.label, achieved: c.achieved})),
    };
  }

  writeLiveManifest() {
    if (!this.run) return;
    try {
      this.run.writeJson('run.json', this.runIndex(false));
    } catch {
      /* best effort */
    }
  }

  runIndex(final, reason) {
    return {
      schema: 1,
      tool: 'multi-capture',
      version: TOOL_VERSION,
      startedAt: this.startedAt.toISOString(),
      endedAt: final ? new Date().toISOString() : null,
      reason: reason || '',
      browser: this.browserPath || '',
      profileDir: this.profileDir || '(injected cdp)',
      redactEnabled: this.redactEnabled(),
      unattributed: this.unattributed,
      liveBrowserNote:
        'The operator browsed and logged in manually in a visible browser; this tool never automates a site. It only records what the browser did, filed per site domain.',
      sites: [...this.runtimes.values()].map((runtime) => this.siteIndexEntry(runtime)),
    };
  }

  writeSiteOutputs(runtime, reason) {
    const caps = this.capabilities(runtime);
    const capLines = deps.capabilities().renderCapabilityLines(caps);
    this.writeSiteCatalogs(runtime);
    runtime.store.writeJson('network.har', deps.har().buildHar(runtime.harRecords, `multi-capture ${runtime.domain}`));
    runtime.store.writeJson('messages/correlation.json', this.buildCorrelation(runtime));
    runtime.store.writeJson(
      'messages/sms-summary.json',
      smsSummary({
        site: runtime.key,
        domain: runtime.domain,
        markers: runtime.markers,
        candidates: runtime.candidates,
        generatedAt: new Date().toISOString(),
      })
    );
    runtime.store.write(
      'SUMMARY.md',
      renderSiteSummary({
        site: runtime.key,
        domain: runtime.domain,
        storeDir: runtime.store.dir,
        counts: runtime.store.counts,
        capLines,
        hosts: [...runtime.store.hosts],
        visits: runtime.store.visits,
        reason,
        startedAt: runtime.store.firstSeen,
        endedAt: new Date().toISOString(),
        redactEnabled: this.redactEnabled(),
      })
    );
    runtime.store.write(
      'SHARE-README.md',
      [
        `# ${runtime.domain} — what to share`,
        '',
        'Share this whole site folder (it is redacted by default). Start with:',
        '',
        '1. `messages/sms-summary.json` — how incoming messages arrive and how to send (from your markers).',
        '2. `messages/labeled-*.jsonl` — the examples you confirmed with Ctrl+Alt+I / Ctrl+Alt+O.',
        '3. `messages/correlation.json` — which network event produced which visible message.',
        '4. `api-catalog.json` + `websockets/catalog.json` + `websockets/*.jsonl` — endpoint and socket schemas.',
        '5. `dom/*.html` — the page as the browser saw it (feed a future site observer adapter).',
        '',
        'Do NOT share a folder captured with rawMode=true: it can contain live tokens/cookies.',
        '',
      ].join('\n')
    );
    runtime.store.writeManifest({
      schema: 1,
      tool: 'multi-capture',
      version: TOOL_VERSION,
      site: runtime.key,
      domain: runtime.domain,
      startedAt: runtime.store.firstSeen,
      endedAt: new Date().toISOString(),
      stopReason: reason,
      startUrl: this.safeUrl(runtime.domain),
      browser: this.browserPath || '',
      redactEnabled: this.redactEnabled(),
      visits: runtime.store.visits,
      hosts: [...runtime.store.hosts].sort(),
      counts: runtime.store.counts,
      capabilities: caps,
      coverage: runtime.store.coverage,
      files: {
        'visits.jsonl': 'When this site was opened in a tab during the run.',
        'events.jsonl': 'Every capture event for this site, one JSON per line, in order.',
        'network.har': 'HTTP archive (redacted) for this site only.',
        'api-catalog.json': 'Endpoint patterns with methods, statuses and body key shapes.',
        'http/*.json': 'One file per HTTP request with headers and bodies (redacted).',
        'websockets/index.jsonl': 'Socket lifecycle (created/handshake/closed).',
        'websockets/catalog.json': 'Per-event WebSocket aggregate. Used by compare.',
        'websockets/*.jsonl': 'Frames per socket: direction, opcode, eventName, payload.',
        'messages/incoming-candidates.jsonl': 'Received frames/responses that look like chat messages.',
        'messages/outgoing-candidates.jsonl': 'Sent frames/requests that look like chat messages.',
        'messages/dom-messages.jsonl': 'Message-like DOM nodes added while browsing.',
        'messages/labeled-incoming.jsonl': 'User-confirmed incoming examples (Ctrl+Alt+I). Ground truth.',
        'messages/labeled-outgoing.jsonl': 'User-confirmed outgoing examples (Ctrl+Alt+O). Ground truth.',
        'messages/correlation.json': 'DOM messages matched to nearby network evidence.',
        'messages/sms-summary.json': 'Per-site answer: how to detect incoming and how to send.',
        'dom/*.html': 'Full page HTML snapshots.',
        'storage/*.json': 'Cookies + local/session storage snapshots (redacted).',
        'screenshots/*.png': 'Page screenshots at snapshot points.',
        'console.jsonl / errors.jsonl / hooks.jsonl': 'Page console, JS errors, in-page hook evidence.',
      },
    });
  }

  siteTable(domain) {
    const rows = [];
    for (const runtime of this.runtimes.values()) {
      if (domain && runtime.key !== siteKey(domain)) continue;
      const c = runtime.store.counts;
      const caps = this.capabilities(runtime);
      const has = (key) => (caps.find((x) => x.key === key && x.achieved) ? 'YES' : 'no');
      rows.push({
        site: runtime.domain,
        dir: runtime.store.relativeDir,
        visits: runtime.store.visits,
        http: `${c.httpRequests}/${c.httpResponses}`,
        wsFrames: `${c.wsFramesSent}/${c.wsFramesReceived}`,
        dom: c.domMessages,
        snapshots: c.snapshots,
        markers: `${c.incomingMarkers}/${c.outgoingMarkers}`,
        incoming: has('incoming'),
        outgoing: has('outgoing'),
      });
    }
    return rows;
  }

  async stop(reason = 'stop') {
    if (this.stopped) return;
    this.stopped = true;
    for (const timer of ['_heartbeat', '_statusTimer', '_focusTimer']) {
      if (this[timer]) clearInterval(this[timer]);
    }
    try {
      // A final snapshot of every tab that was used recently: the end state of
      // each site (composer, message list, lobby) is the most useful DOM dump.
      const now = Date.now();
      const recent = [...this.sessions.entries()].filter(([, s]) => s.domain && now - (s.lastActivity || 0) < 10 * 60 * 1000).slice(0, 6);
      await Promise.race([
        Promise.all(recent.map(([sessionId]) => this.takeSnapshot(sessionId, 'final').catch(() => {}))),
        new Promise((r) => setTimeout(r, 8000)),
      ]);
    } catch {
      /* best effort */
    }
    try {
      for (const runtime of this.runtimes.values()) {
        const fromFiles = this.collectMarkers(runtime);
        runtime.markers = fromFiles.length ? fromFiles : runtime.markers;
        this.writeSiteOutputs(runtime, reason);
      }
      if (this.run) {
        const index = this.runIndex(true, reason);
        this.run.writeJson('run.json', index);
        this.run.write('RUN-SUMMARY.md', renderRunSummary(index));
      }
      this.log('[capture] --- Per-site result ---');
      for (const row of this.siteTable()) {
        this.log(
          `[capture] ${row.site} -> ${row.dir} | visits ${row.visits} | HTTP ${row.http} | WS ${row.wsFrames} | DOM ${row.dom} | markers ${row.markers} | incoming ${row.incoming} | outgoing ${row.outgoing}`
        );
      }
      if (!this.runtimes.size) this.log('[capture] Kono site capture hoyni — browser-e ekta site kholun, tarpor abar chalun.');
      else if (this.run) this.log(`[capture] Run summary: ${path.join(this.run.dir, 'RUN-SUMMARY.md')}`);
    } finally {
      try {
        if (this.cdp) await this.cdp.send('Browser.close').catch(() => {});
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

  // The labeled-*.jsonl entries as in-memory rows (for sms-summary.json).
  collectMarkers(runtime) {
    const rows = [];
    for (const label of ['incoming', 'outgoing']) {
      const file = path.join(runtime.store.dir, 'messages', `labeled-${label}.jsonl`);
      if (!fs.existsSync(file)) continue;
      for (const line of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
        if (!line.trim()) continue;
        try {
          const parsed = JSON.parse(line);
          rows.push({...parsed, label});
        } catch {
          /* skip a torn line rather than failing the report */
        }
      }
    }
    return rows;
  }
}

module.exports = {MultiCaptureSession, TOOL_VERSION, DUMP_EXPR, FOCUS_EXPR};
