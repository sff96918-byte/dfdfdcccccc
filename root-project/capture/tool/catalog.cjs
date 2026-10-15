"use strict";
// Endpoint catalog: normalizes URLs into patterns so later analysis can see
// "POST /rooms/:id/messages -> 200 {id, text, created_at}" at a glance.

function normalizePath(pathname) {
  return String(pathname || "/")
    .split("/")
    .map((seg) => {
      if (!seg) return seg;
      if (/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(seg)) return ":uuid";
      if (/^\d+$/.test(seg)) return ":id";
      if (/^[0-9a-f]{16,}$/i.test(seg)) return ":hex";
      if (/^[A-Za-z0-9_-]{20,}$/.test(seg) && /\d/.test(seg)) return ":id";
      return seg;
    })
    .join("/");
}

function keyShape(value, prefix = "", out = new Set(), depth = 0) {
  if (!value || typeof value !== "object" || depth > 3) return out;
  for (const [k, v] of Object.entries(value)) {
    const path = prefix ? `${prefix}.${k}` : k;
    out.add(path);
    if (v && typeof v === "object") keyShape(v, path, out, depth + 1);
  }
  return out;
}

class Catalog {
  constructor() {
    this.entries = new Map();
  }

  add({ method, url, status, requestBody, responseBody, ts }) {
    let parsed;
    try {
      parsed = new URL(url);
    } catch {
      return;
    }
    const pattern = `${method.toUpperCase()} ${parsed.origin}${normalizePath(parsed.pathname)}`;
    let e = this.entries.get(pattern);
    if (!e) {
      e = {
        pattern,
        method: method.toUpperCase(),
        origin: parsed.origin,
        count: 0,
        statuses: {},
        requestKeys: new Set(),
        responseKeys: new Set(),
        firstSeen: ts,
        lastSeen: ts,
      };
      this.entries.set(pattern, e);
    }
    e.count += 1;
    e.lastSeen = ts;
    if (status != null) e.statuses[status] = (e.statuses[status] || 0) + 1;
    if (requestBody && typeof requestBody === "object") {
      for (const k of keyShape(requestBody)) e.requestKeys.add(k);
    }
    if (responseBody && typeof responseBody === "object") {
      for (const k of keyShape(responseBody)) e.responseKeys.add(k);
    }
  }

  toJSON() {
    return [...this.entries.values()]
      .sort((a, b) => b.count - a.count)
      .map((e) => ({
        pattern: e.pattern,
        method: e.method,
        origin: e.origin,
        count: e.count,
        statuses: e.statuses,
        requestKeys: [...e.requestKeys].sort(),
        responseKeys: [...e.responseKeys].sort(),
        firstSeen: e.firstSeen,
        lastSeen: e.lastSeen,
      }));
  }
}

module.exports = { normalizePath, keyShape, Catalog };
