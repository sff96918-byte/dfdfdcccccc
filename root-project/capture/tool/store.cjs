"use strict";
// Session store: creates the per-session folder tree and appends JSONL in
// real time (appendFileSync per event, so a crash loses nothing committed).

const fs = require("node:fs");
const path = require("node:path");

function stamp(date = new Date()) {
  const p = (n) => String(n).padStart(2, "0");
  return `${date.getFullYear()}${p(date.getMonth() + 1)}${p(date.getDate())}-${p(date.getHours())}${p(date.getMinutes())}${p(date.getSeconds())}`;
}

function safeName(text, fallback = "session") {
  const s = String(text || "")
    .replace(/^https?:\/\//i, "")
    .replace(/[^A-Za-z0-9._-]+/g, "_")
    .replace(/^[._-]+|[._-]+$/g, "")
    .slice(0, 60);
  return s || fallback;
}

class SessionStore {
  constructor(baseDir, startUrl) {
    let host = "blank";
    try {
      host = new URL(startUrl).host || host;
    } catch {
      /* keep default */
    }
    this.dir = path.join(baseDir, `${stamp()}_${safeName(host)}`);
    const sub = [
      "",
      "websockets",
      "http",
      "messages",
      "dom",
      "screenshots",
      "storage",
    ];
    for (const s of sub) fs.mkdirSync(path.join(this.dir, s), { recursive: true });
    this.counts = {
      httpRequests: 0,
      httpResponses: 0,
      wsConnections: 0,
      wsFramesSent: 0,
      wsFramesReceived: 0,
      hookEvents: 0,
      domMessages: 0,
      markers: 0,
      incomingCandidates: 0,
      outgoingCandidates: 0,
      incomingMarkers: 0,
      outgoingMarkers: 0,
      snapshots: 0,
      consoleMessages: 0,
      pageErrors: 0,
    };
    this.coverage = {
      http: false,
      websockets: false,
      domSnapshots: false,
      domMessages: false,
      storage: false,
      console: false,
      screenshots: false,
      markers: false,
    };
  }

  appendJsonl(relPath, obj) {
    fs.appendFileSync(path.join(this.dir, relPath), JSON.stringify(obj) + "\n");
  }

  event(type, data) {
    this.appendJsonl("events.jsonl", { ts: new Date().toISOString(), type, ...(data || {}) });
  }

  write(relPath, content) {
    fs.writeFileSync(path.join(this.dir, relPath), content);
  }

  writeJson(relPath, obj) {
    this.write(relPath, JSON.stringify(obj, null, 2) + "\n");
  }

  writeManifest(manifest) {
    this.writeJson("manifest.json", manifest);
  }

  fileExists(relPath) {
    return fs.existsSync(path.join(this.dir, relPath));
  }
}

module.exports = { SessionStore, safeName, stamp };
