"use strict";
// Browser discovery + launch + DevTools endpoint waiting.
// Robust by design: we pick a free port ourselves, then POLL the DevTools
// HTTP endpoint until it answers (first launch of Edge/Chrome can take a
// while). The browser's own stderr is kept in a log file so failures are
// diagnosable instead of a bare "fetch failed".

const fs = require("node:fs");
const net = require("node:net");
const path = require("node:path");
const { spawn, execFileSync } = require("node:child_process");
const { httpJson } = require("./cdp.cjs");

function windowsCandidates() {
  const roots = [
    process.env.PROGRAMFILES,
    process.env["PROGRAMFILES(X86)"],
    process.env.LOCALAPPDATA,
  ].filter(Boolean);
  const rels = [
    "Google/Chrome/Application/chrome.exe",
    "Microsoft/Edge/Application/msedge.exe",
    "Chromium/Application/chrome.exe",
    "BraveSoftware/Brave-Browser/Application/brave.exe",
  ];
  const out = [];
  for (const root of roots) for (const rel of rels) out.push(path.join(root, ...rel.split("/")));
  return out;
}

function unixCandidates() {
  const names = ["google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"];
  const out = [];
  for (const name of names) {
    try {
      const p = execFileSync("which", [name], { encoding: "utf8" }).trim();
      if (p) out.push(p);
    } catch {
      /* not present */
    }
  }
  return out;
}

function findBrowser(explicit) {
  const candidates = [];
  if (explicit) candidates.push(explicit);
  if (process.env.CAPTURE_BROWSER) candidates.push(process.env.CAPTURE_BROWSER);
  candidates.push(...(process.platform === "win32" ? windowsCandidates() : unixCandidates()));
  for (const c of candidates) {
    if (c && fs.existsSync(c)) return c;
  }
  return null;
}

function getFreePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
  });
}

function logTail(logFile, lines = 8) {
  try {
    if (!logFile) return "";
    const text = fs.readFileSync(logFile, "utf8").trim().split(/\r?\n/);
    const tail = text.slice(-lines).join("\n");
    return tail ? `\n--- browser log (last lines) ---\n${tail}\n---` : "";
  } catch {
    return "";
  }
}

function launchBrowser({ browserPath, profileDir, url, headless, port, logFile }) {
  fs.mkdirSync(profileDir, { recursive: true });
  const args = [
    `--remote-debugging-port=${port}`,
    "--remote-debugging-address=127.0.0.1",
    "--remote-allow-origins=*",
    `--user-data-dir=${profileDir}`,
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-popup-blocking",
  ];
  if (headless) args.push("--headless=new", "--disable-gpu", "--window-size=1280,900");
  args.push(url || "about:blank");
  let stream = null;
  const stdio = ["ignore", "ignore", "ignore"];
  if (logFile) {
    stream = fs.createWriteStream(logFile, { flags: "a" });
    stdio[1] = "pipe";
    stdio[2] = "pipe";
  }
  const child = spawn(browserPath, args, { stdio });
  if (stream) {
    if (child.stdout) child.stdout.pipe(stream);
    if (child.stderr) child.stderr.pipe(stream);
    child.on("exit", () => {
      try {
        stream.end();
      } catch {
        /* ignore */
      }
    });
  }
  return child;
}

// Poll until the DevTools HTTP endpoint answers, the browser dies, or timeout.
// Also tries the port written in DevToolsActivePort in case the browser chose
// a different one than requested.
async function waitForDevToolsEndpoint({ port, profileDir, timeoutMs = 45000, isAlive, logFile }) {
  const started = Date.now();
  let lastError = "";
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  while (Date.now() - started < timeoutMs) {
    if (isAlive && !isAlive()) {
      throw new Error("Browser process exited during startup." + logTail(logFile));
    }
    const ports = [port];
    try {
      const pf = fs.readFileSync(path.join(profileDir, "DevToolsActivePort"), "utf8");
      const p2 = parseInt(pf.split(/\r?\n/)[0], 10);
      if (Number.isInteger(p2) && p2 > 0 && p2 !== port) ports.push(p2);
    } catch {
      /* file not written yet */
    }
    for (const p of ports) {
      try {
        const version = await httpJson(`http://127.0.0.1:${p}/json/version`, 3000);
        if (version && version.webSocketDebuggerUrl) return { port: p, version };
        lastError = "DevTools answered but sent no webSocketDebuggerUrl";
      } catch (e) {
        lastError = e.message || String(e);
      }
    }
    await sleep(500);
  }
  throw new Error(
    `DevTools endpoint did not become ready within ${Math.round(timeoutMs / 1000)}s (last error: ${lastError}).` +
      logTail(logFile) +
      "\nHints: close other windows using the capture profile (capture/profiles), check antivirus/firewall prompts, or set browserPath in capture.config.json to Chrome.",
  );
}

// Kept for compatibility: read the port file only.
function waitForDevToolsPort(profileDir, timeoutMs = 20000) {
  const portFile = path.join(profileDir, "DevToolsActivePort");
  const started = Date.now();
  return new Promise((resolve, reject) => {
    const tick = () => {
      try {
        const content = fs.readFileSync(portFile, "utf8");
        const port = parseInt(content.split(/\r?\n/)[0], 10);
        if (Number.isInteger(port) && port > 0) return resolve(port);
      } catch {
        /* not written yet */
      }
      if (Date.now() - started > timeoutMs) reject(new Error("Browser did not expose a DevTools port in time."));
      else setTimeout(tick, 200);
    };
    tick();
  });
}

module.exports = {
  findBrowser,
  launchBrowser,
  waitForDevToolsPort,
  waitForDevToolsEndpoint,
  getFreePort,
  logTail,
};
