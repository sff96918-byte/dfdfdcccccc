"use strict";
// Doctor: proves on THIS machine that run.bat will work — checks Node, finds
// the browser, then actually launches it headless, connects via CDP, and
// closes it. Run by install.bat; can also be run manually any time:
//   node tool/doctor.cjs

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { CDP } = require("./cdp.cjs");
const { findBrowser, launchBrowser, waitForDevToolsEndpoint, getFreePort } = require("./browser.cjs");
const { loadConfig } = require("./capture.cjs");

async function main() {
  let failures = 0;
  const ok = (msg) => console.log(`[OK] ${msg}`);
  const bad = (msg) => {
    failures += 1;
    console.log(`[FAIL] ${msg}`);
  };

  const major = parseInt(process.versions.node.split(".")[0], 10);
  if (major >= 20) ok(`Node.js ${process.versions.node}`);
  else bad(`Node.js ${process.versions.node} is too old; install Node 22 LTS.`);

  const captureDir = path.resolve(__dirname, "..");
  const config = loadConfig(captureDir, {});
  const browser = findBrowser(config.browserPath);
  if (!browser) {
    bad("No Chrome/Edge/Chromium found. Install Google Chrome, or set browserPath in capture.config.json.");
    process.exit(1);
  }
  ok(`Browser: ${browser}`);

  const tmpProfile = fs.mkdtempSync(path.join(os.tmpdir(), "capture-doctor-"));
  const logFile = path.join(tmpProfile, "browser.log");
  const port = await getFreePort();
  const child = launchBrowser({
    browserPath: browser,
    profileDir: path.join(tmpProfile, "profile"),
    url: "about:blank",
    headless: true,
    port,
    logFile,
  });
  let died = false;
  child.on("exit", () => (died = true));
  try {
    const { version } = await waitForDevToolsEndpoint({
      port,
      profileDir: path.join(tmpProfile, "profile"),
      timeoutMs: 45000,
      isAlive: () => !died && child.exitCode === null,
      logFile,
    });
    ok(`DevTools endpoint ready (${version.Browser || "Chromium"})`);
    const cdp = await new CDP(version.webSocketDebuggerUrl).connect();
    const { targetInfos } = await cdp.send("Target.getTargets");
    ok(`CDP connected; ${Array.isArray(targetInfos) ? targetInfos.length : 0} target(s) visible`);
    await cdp.send("Browser.close").catch(() => {});
    cdp.close();
  } catch (e) {
    bad(`Browser/CDP smoke test failed: ${e.message}`);
  } finally {
    try {
      if (!child.killed) child.kill();
    } catch {
      /* ignore */
    }
    try {
      fs.rmSync(tmpProfile, { recursive: true, force: true });
    } catch {
      /* ignore */
    }
  }

  if (failures) {
    console.log("DOCTOR: FAIL — fix the items above, then run install.bat again.");
    process.exit(1);
  }
  console.log("DOCTOR: PASS — run.bat diye capture suru kora jabe.");
}

main().catch((e) => {
  console.error("DOCTOR ERROR:", e.message || e);
  process.exit(1);
});
