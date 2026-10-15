const { test } = require("node:test");
const assert = require("node:assert/strict");
const { zipSync } = require("fflate");
const {
  excluded,
  hashes,
  fingerprint,
} = require("../scripts/source-files.cjs");
const { validateArchive } = require("../scripts/package-source.cjs");
test("source inventory excludes runtime/private/build data but keeps embedded UI", () => {
  for (const p of [
    "data/profiles/a.json",
    "logs.txt",
    "nested/LOGS.TXT",
    "nested/data/a.json",
    "node_modules/a.js",
    "build/a.exe",
    "dist/file",
    "runtime/chrome.exe",
    ".env",
    "x/login_info_1.json",
    "x/storage_state.json",
    "verification-output/report.json",
  ])
    assert.ok(excluded(p), p);
  assert.equal(excluded("cmd/chitchatui/frontend/dist/app.js"), false);
});
test("fingerprint is order independent and detects any content change", () => {
  const a = { a: Buffer.from("1"), b: Buffer.from("2") };
  assert.equal(fingerprint(a), fingerprint({ b: a.b, a: a.a }));
  assert.notEqual(fingerprint(a), fingerprint({ ...a, a: Buffer.from("3") }));
});
test("package integrity rejects tampering, missing and unexpected files", () => {
  const payload = { "root-project/a.txt": Buffer.from("original") };
  const manifest = Buffer.from(JSON.stringify({ sha256: hashes(payload) }));
  const archive = { ...payload, "SOURCE-MANIFEST.json": manifest };
  assert.doesNotThrow(() => validateArchive(zipSync(archive)));
  assert.throws(() =>
    validateArchive(
      zipSync({ ...archive, "root-project/a.txt": Buffer.from("changed") }),
    ),
  );
  assert.throws(() =>
    validateArchive(zipSync({ "SOURCE-MANIFEST.json": manifest })),
  );
  assert.throws(() =>
    validateArchive(zipSync({ ...archive, "extra.txt": Buffer.from("extra") })),
  );
});

const { validateReport } = require("../scripts/package-source.cjs");
const { REQUIRED_CHECKS } = require("../scripts/verification-policy.cjs");
const { workflowFingerprint } = require("../scripts/source-files.cjs");
test("release gate rejects blocked, stale and inconsistent reports", () => {
  const report = {
    schema: 1,
    sourceFingerprint: "current",
    workflowFingerprint: workflowFingerprint(),
    overall: "PASS",
    checks: REQUIRED_CHECKS.map((name) => ({ name, status: "PASS" })),
  };
  assert.doesNotThrow(() => validateReport(report, "current"));
  assert.throws(() => validateReport(report, "edited", true));
  assert.throws(() =>
    validateReport({ ...report, checks: [] }, "current", true),
  );
  assert.throws(() =>
    validateReport(
      { ...report, checks: [{ status: "FAIL" }] },
      "current",
      true,
    ),
  );
  const blocked = {
    ...report,
    overall: "BLOCKED",
    checks: REQUIRED_CHECKS.map((name) => ({ name, status: "BLOCKED" })),
  };
  assert.throws(() => validateReport(blocked, "current"));
  assert.doesNotThrow(() => validateReport(blocked, "current", true));
});

test("release gate rejects a report that omits required checks", () => {
  const partial = {
    schema: 1,
    sourceFingerprint: "current",
    overall: "PASS",
    checks: [{ name: "frontend-syntax", status: "PASS" }],
  };
  assert.throws(
    () => validateReport(partial, "current"),
    /required|Incomplete/i,
  );
});
test("source inventory excludes Windows case variants of private/runtime paths", () => {
  for (const p of [
    "DATA/account.json",
    "Profiles/account.json",
    "Runtime/chrome.bin",
    ".ENV",
    "x/Headless_Sessions/session.json",
  ])
    assert.ok(excluded(p), p);
});
test("archive check rejects Windows drive and alternate-stream paths", () => {
  for (const name of ["C:/outside.txt", "root-project/file.txt:stream"]) {
    const payload = { [name]: Buffer.from("data") };
    const manifest = Buffer.from(JSON.stringify({ sha256: hashes(payload) }));
    assert.throws(
      () =>
        validateArchive(
          zipSync({ ...payload, "SOURCE-MANIFEST.json": manifest }),
        ),
      /Unsafe/,
    );
  }
});
test("workflow edits invalidate an otherwise unchanged source report", () => {
  const checks = REQUIRED_CHECKS.map((name) => ({ name, status: "PASS" }));
  const report = {
    schema: 1,
    sourceFingerprint: "same",
    workflowFingerprint: "old",
    overall: "PASS",
    checks,
  };
  assert.throws(
    () => validateReport(report, "same", true, "new"),
    /Workflow changed/,
  );
  assert.doesNotThrow(() => validateReport(report, "same", false, "old"));
});
test("archive rejects case collisions, reserved device names and dot normalization", () => {
  for (const payload of [
    {
      "root-project/FOO": Buffer.from("a"),
      "root-project/foo": Buffer.from("b"),
    },
    { "root-project/CON.txt": Buffer.from("a") },
    { "root-project/file.": Buffer.from("a") },
    { "root-project/./file": Buffer.from("a") },
  ]) {
    const manifest = Buffer.from(JSON.stringify({ sha256: hashes(payload) }));
    assert.throws(
      () =>
        validateArchive(
          zipSync({ ...payload, "SOURCE-MANIFEST.json": manifest }),
        ),
      /Unsafe/,
    );
  }
});
test("archive diagnostics handle missing manifests and ignore harmless JSON key order", () => {
  assert.throws(
    () => validateArchive(zipSync({ file: Buffer.from("a") })),
    /Missing SOURCE-MANIFEST/,
  );
  const files = { "a.txt": Buffer.from("a"), "b.txt": Buffer.from("b") };
  const original = hashes(files);
  const reversed = Object.fromEntries(Object.entries(original).reverse());
  assert.doesNotThrow(() =>
    validateArchive(
      zipSync({
        ...files,
        "SOURCE-MANIFEST.json": Buffer.from(
          JSON.stringify({ sha256: reversed }),
        ),
      }),
    ),
  );
});
