"use strict";
// End-to-end self-test WITHOUT any real website or account:
//   node selftest/run-selftest.cjs
// Starts the local test chat server, launches a headless browser through the
// capture engine, sends one message, waits for the fake incoming reply, then
// verifies the saved session. If no Chrome/Edge/Chromium exists on this
// machine it prints SKIP (run it on Windows after install.bat).

const fs = require("node:fs");
const path = require("node:path");
const { startSelfTestServer } = require("./server.cjs");
const { CaptureSession, loadConfig } = require("../tool/capture.cjs");
const { findBrowser } = require("../tool/browser.cjs");

function readAll(dir, rel) {
  const p = path.join(dir, rel);
  return fs.existsSync(p) ? fs.readFileSync(p, "utf8") : "";
}

async function main() {
  const captureDir = path.resolve(__dirname, "..");
  const browser = findBrowser("");
  if (!browser) {
    console.log("SKIP: no Chrome/Edge/Chromium found on this machine. Unit tests still validate the logic; run this self-test on Windows after install.bat.");
    process.exit(0);
  }
  const { server, port } = await startSelfTestServer(0);
  const url = `http://127.0.0.1:${port}/`;
  const config = loadConfig(captureDir, { startUrl: url, headless: true });
  const session = new CaptureSession({ config, captureDir, onLog: (m) => console.log(m) });
  try {
    await session.start();
    // Wait for the page session, then type + click Send inside the real page.
    let sid = null;
    for (let i = 0; i < 50 && !sid; i++) {
      sid = session.firstSessionId();
      if (!sid) await new Promise((r) => setTimeout(r, 200));
    }
    if (!sid) throw new Error("page session never attached");
    await new Promise((r) => setTimeout(r, 1000));
    await session.evaluate(
      sid,
      `(() => { document.getElementById('text').value='hello outgoing (selftest)'; document.getElementById('send').click(); return true; })()`,
    );
    await new Promise((r) => setTimeout(r, 2000));
    await session.stop("selftest");
  } finally {
    server.close();
  }

  const dir = session.store.dir;
  const checks = {
    sessionFolder: fs.existsSync(dir),
    eventsLogged: readAll(dir, "events.jsonl").includes("ws_created"),
    wsFramesSaved: fs.readdirSync(path.join(dir, "websockets")).some((f) => f.endsWith(".jsonl") && f !== "index.jsonl"),
    outgoingCandidate: readAll(dir, "messages/outgoing-candidates.jsonl").includes("hello outgoing"),
    incomingCandidate: readAll(dir, "messages/incoming-candidates.jsonl").includes("hello incoming"),
    domMessages: readAll(dir, "messages/dom-messages.jsonl").length > 0,
    catalogWritten: fs.existsSync(path.join(dir, "api-catalog.json")),
    harWritten: fs.existsSync(path.join(dir, "network.har")),
    snapshotHtml: fs.readdirSync(path.join(dir, "dom")).some((f) => f.endsWith(".html")),
  };
  let failed = 0;
  for (const [name, ok] of Object.entries(checks)) {
    console.log(`${ok ? "PASS" : "FAIL"} ${name}`);
    if (!ok) failed += 1;
  }
  console.log(`Self-test session: ${dir}`);
  if (failed) {
    console.log(`SELFTEST FAIL (${failed} checks)`);
    process.exit(1);
  }
  console.log("SELFTEST PASS");
}

main().catch((e) => {
  console.error("SELFTEST ERROR:", e.message || e);
  process.exit(1);
});
