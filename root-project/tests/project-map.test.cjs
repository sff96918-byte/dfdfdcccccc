"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const { updateProjectDocs, AUTO_BEGIN, AUTO_END } = require("../scripts/project-map.cjs");

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "projmap-"));
  fs.mkdirSync(path.join(root, "internal/model"), { recursive: true });
  fs.mkdirSync(path.join(root, "internal/browser"), { recursive: true });
  fs.mkdirSync(path.join(root, "verification-output"), { recursive: true });
  fs.writeFileSync(
    path.join(root, "internal/model/ui.go"),
    'package model\n\nfunc DefaultBrowserOptions() []BrowserOption {\n\treturn []BrowserOption{\n\t\t{\n\t\t\tKey:         "rod",\n\t\t\tLabel:       "Rod",\n\t\t},\n\t\t{\n\t\t\tKey:         "show_mode",\n\t\t\tLabel:       "Show Mode",\n\t\t},\n\t}\n}\n',
  );
  fs.writeFileSync(path.join(root, "internal/browser/backend.go"), "package browser\n");
  fs.writeFileSync(path.join(root, "app.go"), "package main\n");
  fs.writeFileSync(
    path.join(root, "verification-output/report.json"),
    JSON.stringify({ schema: 1, overall: "BLOCKED", sourceFingerprint: "abc", checks: [{ status: "PASS" }, { status: "BLOCKED" }] }),
  );
  return root;
}

test("project map generates AUTO block with inventory and browser options", () => {
  const root = fixture();
  updateProjectDocs(root, new Date("2026-10-14T00:00:00Z"));
  const map = fs.readFileSync(path.join(root, "docs/PROJECT_MAP.md"), "utf8");
  assert.ok(map.includes(AUTO_BEGIN) && map.includes(AUTO_END));
  assert.ok(map.includes("`rod` — Rod"));
  assert.ok(map.includes("`show_mode` — Show Mode"));
  assert.ok(map.includes("`internal/browser/`"));
  const mem = fs.readFileSync(path.join(root, "docs/PROJECT_MEMORY.md"), "utf8");
  assert.ok(mem.includes("Last verification: **BLOCKED**"));
  assert.ok(mem.includes("Decision log"));
});

test("manual text outside AUTO markers survives regeneration; output is idempotent", () => {
  const root = fixture();
  const date = new Date("2026-10-14T00:00:00Z");
  updateProjectDocs(root, date);
  const mapPath = path.join(root, "docs/PROJECT_MAP.md");
  const first = fs.readFileSync(mapPath, "utf8");
  fs.writeFileSync(mapPath, first + "\n## My manual note\nkeep me\n");
  updateProjectDocs(root, date);
  const second = fs.readFileSync(mapPath, "utf8");
  assert.ok(second.includes("keep me"));
  updateProjectDocs(root, date);
  assert.equal(fs.readFileSync(mapPath, "utf8"), second);
});
