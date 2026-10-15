"use strict";
// WebSocket event catalog: aggregates every frame by socket URL + event name
// so a future session can be diffed against a baseline. If chitchat renames
// an event or changes its payload keys, the diff shows exactly that.

const { keyShape } = require("./catalog.cjs");

class WsCatalog {
  constructor() {
    this.entries = new Map();
  }

  add({ url, eventName, direction, payload, ts }) {
    const name = eventName || "(no-event)";
    const key = `${url || ""} :: ${name}`;
    let e = this.entries.get(key);
    if (!e) {
      e = {
        key,
        url: url || "",
        eventName: name,
        sent: 0,
        received: 0,
        payloadKeys: new Set(),
        firstSeen: ts || null,
        lastSeen: ts || null,
      };
      this.entries.set(key, e);
    }
    if (direction === "sent") e.sent += 1;
    else if (direction === "received") e.received += 1;
    if (payload && typeof payload === "object" && !Array.isArray(payload)) {
      for (const k of keyShape(payload)) e.payloadKeys.add(k);
    }
    if (ts) e.lastSeen = ts;
  }

  toJSON() {
    return [...this.entries.values()]
      .sort((a, b) => b.sent + b.received - (a.sent + a.received))
      .map((e) => ({
        key: e.key,
        url: e.url,
        eventName: e.eventName,
        sent: e.sent,
        received: e.received,
        payloadKeys: [...e.payloadKeys].sort(),
        firstSeen: e.firstSeen,
        lastSeen: e.lastSeen,
      }));
  }
}

module.exports = { WsCatalog };
