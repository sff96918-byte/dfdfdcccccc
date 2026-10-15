const { test } = require("node:test");
const assert = require("node:assert/strict");
const { overall } = require("../scripts/verify.cjs");
test("release status never treats blocked or skipped checks as PASS", () => {
  assert.equal(overall([]), "BLOCKED");
  assert.equal(overall([{ status: "PASS" }]), "PASS");
  assert.equal(overall([{ status: "PASS" }, { status: "BLOCKED" }]), "BLOCKED");
  assert.equal(overall([{ status: "SKIPPED" }]), "BLOCKED");
  assert.equal(overall([{ status: "BLOCKED" }, { status: "FAIL" }]), "FAIL");
});
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { executableX64 } = require("../scripts/preflight.cjs");
test("preflight rejects non-EXE/wrong architecture without executing a file", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "preflight-test-"));
  try {
    const file = path.join(dir, "synthetic.exe");
    const data = Buffer.alloc(128);
    data.write("MZ");
    data.writeUInt32LE(64, 60);
    data.writeUInt32LE(0x4550, 64);
    data.writeUInt16LE(0x8664, 68);
    fs.writeFileSync(file, data);
    assert.equal(executableX64(file), true);
    data.writeUInt16LE(0x14c, 68);
    fs.writeFileSync(file, data);
    assert.equal(executableX64(file), false);
    fs.writeFileSync(file, "not executable");
    assert.equal(executableX64(file), false);
  } finally {
    fs.rmSync(dir, { recursive: true });
  }
});

const {
  classifyResult,
  assertCompleteChecks,
  REQUIRED_CHECKS,
} = require("../scripts/verification-policy.cjs");
test("debug classification separates missing tools/network from actual dependency failures", () => {
  assert.equal(classifyResult({ error: { code: "ENOENT" } }).status, "BLOCKED");
  assert.equal(
    classifyResult(
      { status: 1, stderr: 'Get "https://proxy.golang.org/module.zip": EOF' },
      { dependency: true },
    ).status,
    "BLOCKED",
  );
  assert.equal(
    classifyResult(
      { status: 1, stderr: "checksum mismatch" },
      { dependency: true },
    ).status,
    "FAIL",
  );
  assert.equal(
    classifyResult(
      { status: 1, stderr: "unknown revision v99.0.0" },
      { dependency: true },
    ).status,
    "FAIL",
  );
  assert.equal(
    classifyResult({ status: 0, stdout: "app.go\n" }, { emptyOutput: true })
      .status,
    "FAIL",
  );
  assert.equal(classifyResult({ status: 0 }).status, "PASS");
  assert.equal(
    classifyResult({ status: null, signal: "SIGTERM" }).status,
    "FAIL",
  );
});
test("required checks reject omissions, duplicates and invalid statuses", () => {
  const checks = REQUIRED_CHECKS.map((name) => ({ name, status: "PASS" }));
  assert.doesNotThrow(() => assertCompleteChecks(checks));
  assert.throws(() => assertCompleteChecks(checks.slice(1)));
  assert.throws(() => assertCompleteChecks([...checks.slice(1), checks[1]]));
  assert.throws(() =>
    assertCompleteChecks(checks.map((c) => ({ ...c, status: "SKIPPED" }))),
  );
});
const { inspect, hashFile } = require("../scripts/preflight.cjs");
const crypto = require("node:crypto");
function syntheticBundle(dir) {
  const data = Buffer.alloc(128);
  data.write("MZ");
  data.writeUInt32LE(64, 60);
  data.writeUInt32LE(0x4550, 64);
  data.writeUInt16LE(0x8664, 68);
  for (const rel of [
    "chitchatui.exe",
    "chitchatbot.exe",
    "runtime/chromium/chrome.exe",
    "runtime/webview2/msedgewebview2.exe",
  ]) {
    const file = path.join(dir, rel);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, data);
  }
}
test("preflight validates runtime layout and streams reproducible file hashes", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "bundle-test-"));
  try {
    syntheticBundle(dir);
    const result = inspect(dir);
    assert.equal(Object.keys(result).length, 4);
    const file = path.join(dir, "chitchatui.exe");
    assert.equal(
      hashFile(file),
      crypto.createHash("sha256").update(fs.readFileSync(file)).digest("hex"),
    );
    fs.renameSync(
      path.join(dir, "runtime/chromium/chrome.exe"),
      path.join(dir, "runtime/webview2/chrome.exe"),
    );
    assert.throws(() => inspect(dir), /Missing required/);
  } finally {
    fs.rmSync(dir, { recursive: true });
  }
});
test("preflight rejects symlinked runtime directories without following them", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "bundle-link-test-"));
  try {
    syntheticBundle(dir);
    fs.renameSync(path.join(dir, "runtime"), path.join(dir, "other-runtime"));
    fs.symlinkSync(
      path.join(dir, "other-runtime"),
      path.join(dir, "runtime"),
      process.platform === "win32" ? "junction" : "dir",
    );
    assert.throws(() => inspect(dir), /no symlinks/);
  } finally {
    fs.rmSync(dir, { recursive: true });
  }
});
