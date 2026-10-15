// Browser-free verification. Never launches the app, Chromium or a real login.
const fs = require("node:fs");
const path = require("node:path");
const { spawnSync } = require("node:child_process");
const {
  root,
  inventory,
  fingerprint,
  workflowFingerprint,
} = require("./source-files.cjs");
const output = path.join(root, "verification-output");
const {
  overall,
  assertCompleteChecks,
  classifyResult,
} = require("./verification-policy.cjs");
function main() {
  // Refresh the auto project map/memory from the current source first, so the
  // fingerprint below (and any later packaging) already includes them. This
  // never fails verification by itself.
  try {
    require("./project-map.cjs").updateProjectDocs(root);
    console.log("[info] project map/memory refreshed (docs/PROJECT_MAP.md, docs/PROJECT_MEMORY.md)");
  } catch (e) {
    console.log(`[info] project map refresh skipped: ${e.message}`);
  }
  fs.mkdirSync(output, { recursive: true });
  // Invalidate a previous report before any new check; a crash cannot leave a stale PASS.
  fs.rmSync(path.join(output, "report.json"), { force: true });
  const before = fingerprint(inventory());
  const workflowBefore = workflowFingerprint();
  const checks = [];
  // Remove externally supplied build tags: browser_live must never be enabled here.
  const env = { ...process.env, GOFLAGS: "", GOWORK: "off" };
  function record(name, status, detail = "", metadata = {}) {
    checks.push({ name, status, detail, ...metadata });
    console.log(`[${status}] ${name}${detail ? " — " + detail : ""}`);
  }
  function run(
    name,
    command,
    args,
    { extraEnv = {}, emptyOutput = false, dependency = false } = {},
  ) {
    const started = Date.now();
    const r = spawnSync(command, args, {
      cwd: root,
      env: { ...env, ...extraEnv },
      encoding: "utf8",
      timeout: 600000,
      maxBuffer: 16 * 1024 * 1024,
      windowsHide: true,
    });
    const text = (r.stdout || "") + (r.stderr || "");
    const log = name.replace(/[^a-z0-9-]/gi, "-") + ".txt";
    fs.writeFileSync(
      path.join(output, log),
      text + (r.error ? "\n" + r.error.message : ""),
    );
    const { status, reason } = classifyResult(r, { dependency, emptyOutput });
    const logSHA256 = require("node:crypto")
      .createHash("sha256")
      .update(fs.readFileSync(path.join(output, log)))
      .digest("hex");
    record(name, status, `${reason}; log=${log}`, {
      command,
      args,
      exitCode: r.status,
      signal: r.signal || null,
      durationMs: Date.now() - started,
      reason,
      log,
      logSHA256,
    });
    return status === "PASS";
  }
  run("frontend-syntax", process.execPath, [
    "--check",
    "cmd/chitchatui/frontend/dist/app.js",
  ]);
  run("ui-contract-package-tests", process.execPath, [
    "--test",
    "tests/browser-ui.test.cjs",
    "tests/contracts.test.cjs",
    "tests/package.test.cjs",
    "tests/verification.test.cjs",
    "tests/showmode-dom.test.cjs",
  ]);
  const go = process.env.GO_BINARY || "go";
  const goAvailable = run("go-toolchain", go, ["version"]);
  if (goAvailable) {
    const fmt =
      process.env.GOFMT_BINARY ||
      (path.isAbsolute(go)
        ? path.join(
            path.dirname(go),
            process.platform === "win32" ? "gofmt.exe" : "gofmt",
          )
        : "gofmt");
    run(
      "changed-go-format",
      fmt,
      [
        "-l", "app.go", "main.go", "app_logging_test.go",
        "home_chat.go", "home_chat_test.go", "live_chat.go", "internal/browser/domchat.go",
        "internal/applog/applog.go", "internal/applog/applog_test.go",
        "internal/applog/capture.go", "internal/applog/capture_test.go",
        "browser_run_test.go", "browser_lifecycle_test.go",
        "internal/browser/rod.go", "internal/browser/chromedp.go",
        "internal/proxy/proxy.go", "internal/proxy/connect.go",
        "internal/proxy/relay.go", "internal/proxy/testproxy.go",
        "internal/proxy/proxy_test.go", "internal/proxy/relay_test.go",
        "internal/proxy/diagnostics_test.go",
      ],
      { emptyOutput: true },
    );
    const dependencies = run("go-dependencies", go, ["mod", "download"], {
      dependency: true,
    });
    if (dependencies) {
      run("module-checksums", go, ["mod", "verify"]);
      const regression = [
        "./internal/applog",
        "./internal/bot",
        "./internal/browser",
        "./internal/model",
        "./internal/uistore",
        "./internal/workerplan",
        "./internal/runtimeassets",
        "./internal/proxy",
        "./internal/store",
        "./internal/api",
      ];
      run("go-regression-tests", go, [
        "test",
        "-count=1",
        "-timeout=180s",
        ...regression,
      ]);
      run("go-static-analysis", go, ["vet", ...regression]);
      run("windows-cross-build", go, ["build", "./..."], {
        extraEnv: { GOOS: "windows", GOARCH: "amd64", CGO_ENABLED: "0" },
      });
      if (process.platform === "win32") {
        run("windows-app-tests", go, [
          "test",
          "-count=1",
          "-timeout=180s",
          ".",
        ]);
        run("windows-app-vet", go, ["vet", "."]);
        run("wails-production-build", "wails", [
          "build",
          "-clean",
          "-skipbindings",
          "-m",
          "-s",
          "-webview2",
          "error",
          "-o",
          "chitchatui.exe",
        ]);
      } else {
        record(
          "windows-app-tests",
          "BLOCKED",
          "Windows runner required; fake browser only",
        );
        record("windows-app-vet", "BLOCKED", "Windows runner required");
        record("wails-production-build", "BLOCKED", "Windows runner required");
      }
    } else {
      for (const name of [
        "module-checksums",
        "go-regression-tests",
        "go-static-analysis",
        "windows-cross-build",
        "windows-app-tests",
        "windows-app-vet",
        "wails-production-build",
      ])
        record(name, "BLOCKED", "Dependency download failed; not executed");
    }
  } else {
    for (const name of [
      "changed-go-format",
      "go-dependencies",
      "module-checksums",
      "go-regression-tests",
      "go-static-analysis",
      "windows-cross-build",
      "windows-app-tests",
      "windows-app-vet",
      "wails-production-build",
    ])
      record(name, "BLOCKED", "Go toolchain unavailable; not executed");
  }
  const after = fingerprint(inventory());
  record(
    "source-unchanged-during-verification",
    before === after && workflowBefore === workflowFingerprint()
      ? "PASS"
      : "FAIL",
    "Includes source, tests, dependencies lock and verification scripts",
  );
  assertCompleteChecks(checks);
  const report = {
    schema: 1,
    createdAt: new Date().toISOString(),
    platform: process.platform,
    node: process.version,
    overall: overall(checks),
    sourceFingerprint: after,
    workflowFingerprint: workflowFingerprint(),
    checks,
    liveBrowser: "NOT_RUN",
    liveLogin: "NOT_RUN",
    note: "PASS means browser-free checks passed, not live Windows/login verification.",
  };
  fs.writeFileSync(
    path.join(output, "report.tmp.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  fs.renameSync(
    path.join(output, "report.tmp.json"),
    path.join(output, "report.json"),
  );
  console.log(
    `\nOverall: ${report.overall}; report: ${path.join(output, "report.json")}`,
  );
  process.exitCode = report.overall === "PASS" ? 0 : 1;
}
if (require.main === module) main();
module.exports = { overall };
