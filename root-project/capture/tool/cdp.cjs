"use strict";
// Minimal Chrome DevTools Protocol client over Node's built-in WebSocket.
// No npm dependencies. Supports flat sessions (sessionId on each message).

class CDP {
  constructor(webSocketUrl) {
    this.url = webSocketUrl;
    this.nextId = 0;
    this.pending = new Map();
    this.eventHandlers = [];
    this.ws = null;
  }

  async connect(timeoutMs = 15000) {
    this.ws = new WebSocket(this.url);
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("CDP connect timeout")), timeoutMs);
      this.ws.addEventListener("open", () => {
        clearTimeout(timer);
        resolve();
      });
      this.ws.addEventListener("error", (e) => {
        clearTimeout(timer);
        reject(new Error("CDP websocket error: " + (e.message || "unknown")));
      });
    });
    this.ws.addEventListener("message", (ev) => {
      let msg;
      try {
        msg = JSON.parse(String(ev.data));
      } catch {
        return;
      }
      if (msg.id != null && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(`${msg.error.message || "CDP error"} (${msg.error.code || ""})`));
        else resolve(msg.result || {});
        return;
      }
      if (msg.method) {
        for (const fn of this.eventHandlers) {
          try {
            fn(msg);
          } catch {
            /* a handler must never kill the connection */
          }
        }
      }
    });
    this.ws.addEventListener("close", () => {
      for (const { reject } of this.pending.values()) reject(new Error("CDP connection closed"));
      this.pending.clear();
    });
    return this;
  }

  onEvent(fn) {
    this.eventHandlers.push(fn);
  }

  send(method, params = {}, sessionId) {
    const id = ++this.nextId;
    const msg = { id, method, params };
    if (sessionId) msg.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      try {
        this.ws.send(JSON.stringify(msg));
      } catch (e) {
        this.pending.delete(id);
        reject(e);
      }
    });
  }

  close() {
    try {
      if (this.ws) this.ws.close();
    } catch {
      /* ignore */
    }
  }
}

async function httpJson(url, timeoutMs = 5000) {
  const ctrl = new AbortController();
  const t = setTimeout(() => ctrl.abort(), timeoutMs);
  try {
    const res = await fetch(url, { signal: ctrl.signal });
    if (!res.ok) throw new Error(`HTTP ${res.status} for ${url}`);
    return await res.json();
  } finally {
    clearTimeout(t);
  }
}

module.exports = { CDP, httpJson };
