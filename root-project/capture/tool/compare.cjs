"use strict";
// Session compare: diff an OLD capture session against a NEW one.
//
//   node tool/compare.cjs <old-session-dir> <new-session-dir> [--out <dir>]
//
// Writes diff-report.json and DIFF.md (into the new session dir by default).
// This is the future-proofing workflow: when the site changes its API or
// WebSocket protocol, capture again, run compare, and fix only what changed
// instead of starting from zero. Sessions captured before catalogs existed
// still work — missing catalogs are treated as empty and noted.

const fs = require("node:fs");
const path = require("node:path");

function readJson(dir, rel) {
  try {
    return JSON.parse(fs.readFileSync(path.join(dir, rel), "utf8"));
  } catch {
    return null;
  }
}

function readJsonl(dir, rel) {
  try {
    return fs
      .readFileSync(path.join(dir, rel), "utf8")
      .split(/\r?\n/)
      .filter((l) => l.trim())
      .map((l) => {
        try {
          return JSON.parse(l);
        } catch {
          return null;
        }
      })
      .filter(Boolean);
  } catch {
    return [];
  }
}

function labeledEvents(dir) {
  const events = new Set();
  for (const rel of ["messages/labeled-incoming.jsonl", "messages/labeled-outgoing.jsonl"]) {
    for (const row of readJsonl(dir, rel)) {
      if (row && row.candidate && row.candidate.eventName) events.add(row.candidate.eventName);
    }
  }
  return events;
}

function loadSession(dir) {
  const api = readJson(dir, "api-catalog.json");
  const ws = readJson(dir, "websockets/catalog.json");
  return {
    dir,
    manifest: readJson(dir, "manifest.json"),
    apiCatalog: new Map((api || []).map((e) => [e.pattern, e])),
    wsCatalog: new Map((ws || []).map((e) => [e.key, e])),
    hasApiCatalog: api != null,
    hasWsCatalog: ws != null,
    labeled: labeledEvents(dir),
  };
}

function setDiff(a, b) {
  const A = new Set(a || []);
  const B = new Set(b || []);
  return {
    added: [...B].filter((x) => !A.has(x)).sort(),
    removed: [...A].filter((x) => !B.has(x)).sort(),
  };
}

function diffSessions(oldS, newS) {
  const diff = {
    endpoints: { added: [], removed: [], changed: [] },
    websockets: { added: [], removed: [], changed: [] },
    labeledEvents: {
      added: [...newS.labeled].filter((x) => !oldS.labeled.has(x)).sort(),
      removed: [...oldS.labeled].filter((x) => !newS.labeled.has(x)).sort(),
    },
    notes: [],
  };
  if (!oldS.hasApiCatalog) diff.notes.push("Old session has no api-catalog.json (captured before catalogs existed); endpoint diff may be incomplete.");
  if (!newS.hasApiCatalog) diff.notes.push("New session has no api-catalog.json; endpoint diff may be incomplete.");
  if (!oldS.hasWsCatalog) diff.notes.push("Old session has no websockets/catalog.json; websocket diff may be incomplete.");
  if (!newS.hasWsCatalog) diff.notes.push("New session has no websockets/catalog.json; websocket diff may be incomplete.");

  for (const key of newS.apiCatalog.keys()) if (!oldS.apiCatalog.has(key)) diff.endpoints.added.push(key);
  for (const key of oldS.apiCatalog.keys()) if (!newS.apiCatalog.has(key)) diff.endpoints.removed.push(key);
  for (const [key, n] of newS.apiCatalog) {
    const o = oldS.apiCatalog.get(key);
    if (!o) continue;
    const changes = [];
    const st = setDiff(Object.keys(o.statuses || {}), Object.keys(n.statuses || {}));
    if (st.added.length) changes.push(`status codes added: ${st.added.join(", ")}`);
    if (st.removed.length) changes.push(`status codes removed: ${st.removed.join(", ")}`);
    const rq = setDiff(o.requestKeys, n.requestKeys);
    if (rq.added.length) changes.push(`request keys added: ${rq.added.join(", ")}`);
    if (rq.removed.length) changes.push(`request keys removed: ${rq.removed.join(", ")}`);
    const rs = setDiff(o.responseKeys, n.responseKeys);
    if (rs.added.length) changes.push(`response keys added: ${rs.added.join(", ")}`);
    if (rs.removed.length) changes.push(`response keys removed: ${rs.removed.join(", ")}`);
    if (changes.length) diff.endpoints.changed.push({ pattern: key, changes });
  }

  for (const key of newS.wsCatalog.keys()) if (!oldS.wsCatalog.has(key)) diff.websockets.added.push(key);
  for (const key of oldS.wsCatalog.keys()) if (!newS.wsCatalog.has(key)) diff.websockets.removed.push(key);
  for (const [key, n] of newS.wsCatalog) {
    const o = oldS.wsCatalog.get(key);
    if (!o) continue;
    const changes = [];
    const pk = setDiff(o.payloadKeys, n.payloadKeys);
    if (pk.added.length) changes.push(`payload keys added: ${pk.added.join(", ")}`);
    if (pk.removed.length) changes.push(`payload keys removed: ${pk.removed.join(", ")}`);
    if ((o.sent > 0) !== (n.sent > 0) || (o.received > 0) !== (n.received > 0)) {
      changes.push(`direction changed (sent ${o.sent}→${n.sent}, received ${o.received}→${n.received})`);
    }
    if (changes.length) diff.websockets.changed.push({ key, changes });
  }

  diff.endpoints.added.sort();
  diff.endpoints.removed.sort();
  diff.websockets.added.sort();
  diff.websockets.removed.sort();
  const oFp = oldS.manifest && oldS.manifest.schemaFingerprint;
  const nFp = newS.manifest && newS.manifest.schemaFingerprint;
  diff.schemaFingerprint = {
    old: oFp || null,
    new: nFp || null,
    identical: Boolean(oFp && nFp && oFp.combined === nFp.combined),
  };
  diff.summary = {
    endpointsAdded: diff.endpoints.added.length,
    endpointsRemoved: diff.endpoints.removed.length,
    endpointsChanged: diff.endpoints.changed.length,
    wsAdded: diff.websockets.added.length,
    wsRemoved: diff.websockets.removed.length,
    wsChanged: diff.websockets.changed.length,
    labeledAdded: diff.labeledEvents.added.length,
    labeledRemoved: diff.labeledEvents.removed.length,
  };
  return diff;
}

function section(title, items, render) {
  if (!items.length) return [];
  return [`### ${title} (${items.length})`, "", ...items.map(render), ""];
}

function renderMarkdown(diff, oldDir, newDir) {
  const L = [];
  L.push("# Capture diff — what changed on the site");
  L.push("");
  L.push(`- Old (baseline): \`${oldDir}\``);
  L.push(`- New: \`${newDir}\``);
  L.push(`- Schema fingerprint identical: **${diff.schemaFingerprint.identical ? "YES — protocol shape unchanged" : "NO — inspect the sections below"}**`);
  L.push("");
  if (diff.notes.length) {
    L.push("## Notes");
    L.push("");
    for (const n of diff.notes) L.push(`- ${n}`);
    L.push("");
  }
  L.push("## Summary");
  L.push("");
  L.push(`- API endpoints: +${diff.summary.endpointsAdded} / -${diff.summary.endpointsRemoved} / changed ${diff.summary.endpointsChanged}`);
  L.push(`- WebSocket events: +${diff.summary.wsAdded} / -${diff.summary.wsRemoved} / changed ${diff.summary.wsChanged}`);
  L.push(`- User-labeled message events: +${diff.summary.labeledAdded} / -${diff.summary.labeledRemoved}`);
  L.push("");
  L.push("## API endpoints");
  L.push("");
  L.push(...section("Added", diff.endpoints.added, (x) => `- \`${x}\``));
  L.push(...section("Removed", diff.endpoints.removed, (x) => `- \`${x}\``));
  L.push(...section("Changed", diff.endpoints.changed, (x) => `- \`${x.pattern}\`\n${x.changes.map((c) => `  - ${c}`).join("\n")}`));
  if (!diff.endpoints.added.length && !diff.endpoints.removed.length && !diff.endpoints.changed.length) L.push("No endpoint changes.\n");
  L.push("## WebSocket events");
  L.push("");
  L.push(...section("Added", diff.websockets.added, (x) => `- \`${x}\``));
  L.push(...section("Removed", diff.websockets.removed, (x) => `- \`${x}\``));
  L.push(...section("Changed", diff.websockets.changed, (x) => `- \`${x.key}\`\n${x.changes.map((c) => `  - ${c}`).join("\n")}`));
  if (!diff.websockets.added.length && !diff.websockets.removed.length && !diff.websockets.changed.length) L.push("No websocket event changes.\n");
  L.push("## What to do next");
  L.push("");
  L.push("1. For each added/renamed event or endpoint, open the NEW session's `messages/labeled-*.jsonl` and `messages/correlation.json` to see a confirmed example.");
  L.push("2. Update only the affected mapping/logic (event names, endpoints, payload keys listed above). Unchanged sections need no work.");
  L.push("3. Keep this DIFF.md with the new session as the new baseline record.");
  L.push("");
  return L.join("\n");
}

function main(argv) {
  const [oldDir, newDir] = argv.filter((a) => !a.startsWith("--"));
  let outDir = newDir;
  const oi = argv.indexOf("--out");
  if (oi !== -1 && argv[oi + 1]) outDir = argv[oi + 1];
  if (!oldDir || !newDir) {
    console.error("Usage: node tool/compare.cjs <old-session-dir> <new-session-dir> [--out <dir>]");
    process.exit(2);
  }
  const diff = diffSessions(loadSession(oldDir), loadSession(newDir));
  fs.mkdirSync(outDir, { recursive: true });
  fs.writeFileSync(path.join(outDir, "diff-report.json"), JSON.stringify(diff, null, 2) + "\n");
  fs.writeFileSync(path.join(outDir, "DIFF.md"), renderMarkdown(diff, oldDir, newDir));
  console.log(`Endpoints: +${diff.summary.endpointsAdded} -${diff.summary.endpointsRemoved} changed ${diff.summary.endpointsChanged}`);
  console.log(`WebSocket events: +${diff.summary.wsAdded} -${diff.summary.wsRemoved} changed ${diff.summary.wsChanged}`);
  console.log(`Schema identical: ${diff.schemaFingerprint.identical}`);
  console.log(`Wrote ${path.join(outDir, "DIFF.md")} and diff-report.json`);
}

if (require.main === module) main(process.argv.slice(2));

module.exports = { loadSession, diffSessions, renderMarkdown };
