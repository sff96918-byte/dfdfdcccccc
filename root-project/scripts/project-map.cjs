"use strict";
// Auto-generated project map + memory refresher.
//
// docs/PROJECT_MAP.md    - architecture map any agent can read to understand
//                          the project A-to-Z (auto section + manual notes).
// docs/PROJECT_MEMORY.md - living memory: auto status block + a manual
//                          decision log agents append to after each change.
//
// Only text between <!-- AUTO:BEGIN --> and <!-- AUTO:END --> is regenerated.
// Everything outside the markers is preserved byte-for-byte. Running this
// script twice with unchanged sources produces identical output (except the
// date line). It runs automatically at the start of `npm run verify`, or
// manually via `npm run map:update`.

const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { inventory, fingerprint } = require("./source-files.cjs");

const AUTO_BEGIN = "<!-- AUTO:BEGIN -->";
const AUTO_END = "<!-- AUTO:END -->";

function groupOf(rel) {
  const first = rel.split("/")[0];
  if (first === rel) return "(root files)";
  return first + "/";
}

function collectStats(files) {
  const groups = new Map();
  let bytes = 0;
  for (const [rel, data] of Object.entries(files)) {
    bytes += data.length;
    const g = groupOf(rel);
    const cur = groups.get(g) || { count: 0, bytes: 0 };
    cur.count += 1;
    cur.bytes += data.length;
    groups.set(g, cur);
  }
  return { total: Object.keys(files).length, bytes, groups };
}

function internalPackages(files) {
  const pkgs = new Map();
  for (const rel of Object.keys(files)) {
    const m = rel.match(/^internal\/([^/]+)\//);
    if (m && rel.endsWith(".go")) pkgs.set(m[1], (pkgs.get(m[1]) || 0) + 1);
  }
  return [...pkgs.entries()].sort((a, b) => a[0].localeCompare(b[0]));
}

function browserOptions(rootDir) {
  try {
    const src = fs.readFileSync(path.join(rootDir, "internal/model/ui.go"), "utf8");
    const out = [];
    for (const m of src.matchAll(/Key:\s*"([^"]+)",\s*\n\s*Label:\s*"([^"]+)"/g)) {
      out.push({ key: m[1], label: m[2] });
    }
    return out;
  } catch {
    return [];
  }
}

function captureToolVersion(rootDir) {
  try {
    const src = fs.readFileSync(path.join(rootDir, "capture/tool/capture.cjs"), "utf8");
    const m = src.match(/TOOL_VERSION\s*=\s*"([^"]+)"/);
    return m ? m[1] : null;
  } catch {
    return null;
  }
}

function verificationStatus(rootDir) {
  try {
    const report = JSON.parse(
      fs.readFileSync(path.join(rootDir, "verification-output/report.json"), "utf8"),
    );
    const counts = { PASS: 0, FAIL: 0, BLOCKED: 0, SKIPPED: 0 };
    for (const c of report.checks || []) {
      if (counts[c.status] != null) counts[c.status] += 1;
    }
    return { overall: report.overall || "UNKNOWN", counts, sourceFingerprint: report.sourceFingerprint || "" };
  } catch {
    return null;
  }
}

function renderMapAuto({ files, rootDir, dateStr }) {
  const stats = collectStats(files);
  const lines = [];
  lines.push(AUTO_BEGIN);
  lines.push(`_Auto-generated ${dateStr} by \`scripts/project-map.cjs\`. Do not edit this block by hand._`);
  lines.push("");
  lines.push("## Source inventory");
  lines.push("");
  lines.push(`- Total source files: **${stats.total}** (${(stats.bytes / 1024).toFixed(1)} KiB, excluding runtime/private data)`);
  lines.push("");
  lines.push("| Area | Files | Size |");
  lines.push("|---|---:|---:|");
  for (const [g, s] of [...stats.groups.entries()].sort((a, b) => b[1].count - a[1].count)) {
    lines.push(`| \`${g}\` | ${s.count} | ${(s.bytes / 1024).toFixed(1)} KiB |`);
  }
  lines.push("");
  lines.push("## Internal Go packages");
  lines.push("");
  const pkgs = internalPackages(files);
  if (pkgs.length) {
    for (const [name, count] of pkgs) lines.push(`- \`internal/${name}/\` — ${count} Go file(s)`);
  } else lines.push("- (none found)");
  lines.push("");
  lines.push("## Browser options (Dashboard → Browser)");
  lines.push("");
  const opts = browserOptions(rootDir);
  if (opts.length) {
    opts.forEach((o, i) => lines.push(`${i + 1}. \`${o.key}\` — ${o.label}`));
  } else lines.push("- (could not parse internal/model/ui.go)");
  lines.push("");
  lines.push("## Key entry points");
  lines.push("");
  lines.push("- `main.go` / `app.go` — Wails application and bound methods");
  lines.push("- `cmd/chitchatui/` — dashboard frontend (embedded `frontend/dist/`)");
  lines.push("- `capture/` — standalone manual traffic capture tool (see `capture/README.md`)");
  lines.push("- `scripts/verify.cjs` — browser-free verification gate (`verify.bat`)");
  lines.push("- `scripts/package-source.cjs` — source ZIP packaging (refuses stale reports)");
  lines.push("- `install.bat` / `run-dashboard.bat` — Windows setup and launch");
  const capVer = captureToolVersion(rootDir);
  if (capVer) lines.push(`- Capture tool version: **${capVer}**`);
  lines.push("");
  lines.push("## Documentation");
  lines.push("");
  const docNames = new Set(
    Object.keys(files).filter((f) => f.startsWith("docs/") && f.endsWith(".md")),
  );
  docNames.add("docs/PROJECT_MAP.md");
  docNames.add("docs/PROJECT_MEMORY.md");
  for (const rel of [...docNames].sort()) {
    lines.push(`- \`${rel}\``);
  }
  lines.push("- `AGENTS.md` — locked scope and hard rules (read first)");
  lines.push("- `START-HERE.md` — packaging/testing entry guide");
  lines.push(AUTO_END);
  return lines.join("\n");
}

function renderMemoryAuto({ files, rootDir, dateStr }) {
  const ver = verificationStatus(rootDir);
  const opts = browserOptions(rootDir);
  const capVer = captureToolVersion(rootDir);
  const fp = fingerprint(files);
  const lines = [];
  lines.push(AUTO_BEGIN);
  lines.push(`_Auto status ${dateStr}. Do not edit this block by hand._`);
  lines.push("");
  lines.push(`- Source fingerprint: \`${fp.slice(0, 16)}…\``);
  lines.push(`- Browser options: ${opts.length ? opts.map((o) => `\`${o.key}\``).join(", ") : "(unknown)"}`);
  lines.push(`- Capture tool: ${capVer ? `v${capVer}` : "(absent)"}`);
  if (ver) {
    lines.push(
      `- Last verification: **${ver.overall}** (PASS ${ver.counts.PASS}, FAIL ${ver.counts.FAIL}, BLOCKED ${ver.counts.BLOCKED}) — BLOCKED is not PASS; see \`verification-output/report.json\``,
    );
  } else {
    lines.push("- Last verification: no report found yet (`npm run verify`)");
  }
  lines.push(AUTO_END);
  return lines.join("\n");
}

function replaceAutoBlock(existing, autoBlock) {
  const begin = existing.indexOf(AUTO_BEGIN);
  const end = existing.indexOf(AUTO_END);
  if (begin === -1 || end === -1 || end < begin) return null;
  return existing.slice(0, begin) + autoBlock + existing.slice(end + AUTO_END.length);
}

const MAP_TEMPLATE = `# Project Map — ChitChat Bot (root project)

This file is the architecture map for any human or AI agent joining the
project. The block between the AUTO markers is regenerated from the actual
source tree by \`scripts/project-map.cjs\` (automatically on every
\`npm run verify\`, or manually with \`npm run map:update\`). Read it together
with \`AGENTS.md\` (locked scope/rules) and \`docs/PROJECT_MEMORY.md\`
(decisions and current state).

${AUTO_BEGIN}
(auto section not generated yet)
${AUTO_END}

## Manual notes (agents: keep this section current)

- Product: Windows desktop app (Go + Wails) + CLI worker for chitchat.gg.
- Dashboard Browser page has numbered per-row Run buttons; option behaviour is
  documented in \`docs/BROWSER_DIRECT_RUN.md\` and \`docs/SHOW_MODE.md\`.
- \`capture/\` is a standalone manual capture tool. It is NOT part of the
  desktop app runtime and must stay dependency-free (Node built-ins only).
- Verification never launches a browser or a real login by default.
`;

const MEMORY_TEMPLATE = `# Project Memory — decisions and current state

Purpose: when this project is shared with another agent/model, this file plus
\`AGENTS.md\` and \`docs/PROJECT_MAP.md\` should be enough to understand where
things stand without re-asking the user. The AUTO block below is refreshed by
\`scripts/project-map.cjs\`; the decision log is manual — **append an entry
after every meaningful change** (newest last). Never rewrite old entries.

${AUTO_BEGIN}
(auto status not generated yet)
${AUTO_END}

## Decision log

- (template) YYYY-MM-DD — what changed, why, and how it was verified.

## Known limitations (keep honest)

- Full Go/Windows verification depends on module downloads and a Windows
  environment; sandbox runs may legitimately end BLOCKED. BLOCKED is not PASS.
- Live browser/login behaviour is only proven by the user's manual Windows
  test unless explicitly stated otherwise.

## How to add memory

1. Run your change and its tests.
2. Run \`npm run map:update\` (or \`npm run verify\`, which does it first).
3. Append one dated bullet to the decision log above: change + reason +
   verification evidence. Keep secrets, tokens and account data out.
`;

function updateProjectDocs(rootDir, now = new Date()) {
  const dateStr = now.toISOString().slice(0, 10);
  const files = inventory(rootDir);
  // The two generated files must not describe themselves: excluding them keeps
  // repeated runs byte-identical (idempotent) with unchanged sources.
  delete files["docs/PROJECT_MAP.md"];
  delete files["docs/PROJECT_MEMORY.md"];
  const docsDir = path.join(rootDir, "docs");
  fs.mkdirSync(docsDir, { recursive: true });

  const mapPath = path.join(docsDir, "PROJECT_MAP.md");
  const mapAuto = renderMapAuto({ files, rootDir, dateStr });
  let mapContent = fs.existsSync(mapPath) ? fs.readFileSync(mapPath, "utf8") : MAP_TEMPLATE;
  mapContent = replaceAutoBlock(mapContent, mapAuto) ?? MAP_TEMPLATE.replace(/<!-- AUTO:BEGIN -->[\s\S]*?<!-- AUTO:END -->/, mapAuto);
  fs.writeFileSync(mapPath, mapContent);

  const memPath = path.join(docsDir, "PROJECT_MEMORY.md");
  const memAuto = renderMemoryAuto({ files, rootDir, dateStr });
  let memContent = fs.existsSync(memPath) ? fs.readFileSync(memPath, "utf8") : MEMORY_TEMPLATE;
  memContent = replaceAutoBlock(memContent, memAuto) ?? MEMORY_TEMPLATE.replace(/<!-- AUTO:BEGIN -->[\s\S]*?<!-- AUTO:END -->/, memAuto);
  fs.writeFileSync(memPath, memContent);

  return { mapPath, memoryPath: memPath };
}

function digestOfFiles(rootDir) {
  const files = inventory(rootDir);
  return crypto.createHash("sha256").update(JSON.stringify(Object.keys(files).sort())).digest("hex");
}

if (require.main === module) {
  const rootDir = path.resolve(__dirname, "..");
  const { mapPath, memoryPath } = updateProjectDocs(rootDir);
  console.log(`Project map updated: ${path.relative(rootDir, mapPath)}`);
  console.log(`Project memory updated: ${path.relative(rootDir, memoryPath)}`);
}

module.exports = {
  updateProjectDocs,
  replaceAutoBlock,
  renderMapAuto,
  renderMemoryAuto,
  browserOptions,
  digestOfFiles,
  AUTO_BEGIN,
  AUTO_END,
};
