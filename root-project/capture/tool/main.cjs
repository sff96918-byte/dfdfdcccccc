"use strict";
// Entry point. Usage:
//   node tool/main.cjs [--url https://example.com] [--headless]
// Stop with Q in this terminal, Ctrl+C, or by closing the browser window.

const path = require("node:path");
const readline = require("node:readline");
const { CaptureSession, loadConfig } = require("./capture.cjs");

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--url") out.startUrl = argv[++i];
    else if (a === "--headless") out.headless = true;
    else if (a === "--visible") out.headless = false;
    else if (a === "--raw") out.rawMode = true;
    else if (a === "--browser") out.browserPath = argv[++i];
  }
  return out;
}

async function main() {
  const captureDir = path.resolve(__dirname, "..");
  const config = loadConfig(captureDir, parseArgs(process.argv.slice(2)));
  const session = new CaptureSession({ config, captureDir });
  await session.start();

  const shutdown = (reason) => {
    session.stop(reason).catch(() => {});
  };
  process.on("SIGINT", () => shutdown("ctrl-c"));
  process.on("SIGTERM", () => shutdown("sigterm"));

  if (process.stdin.isTTY) {
    readline.emitKeypressEvents(process.stdin);
    process.stdin.setRawMode(true);
    process.stdin.resume();
    process.stdin.on("keypress", (str, key) => {
      if (!key) return;
      if (key.ctrl && key.name === "c") return shutdown("ctrl-c");
      const sid = session.firstSessionId();
      if (key.name === "q") shutdown("terminal-q");
      else if (key.name === "i") session.handleMarker("incoming");
      else if (key.name === "o") session.handleMarker("outgoing");
      else if (key.name === "s" && sid) session.scheduleSnapshot(sid, "terminal");
    });
  }

  await session.done;
  process.exit(0);
}

main().catch((err) => {
  console.error("[capture] Failed:", err.message || err);
  process.exit(1);
});
