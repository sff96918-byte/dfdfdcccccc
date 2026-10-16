'use strict';
// Setup check for the multi-site capture kit: Node version, the shared
// primitives from root-project/capture/tool, the folder layout, and (with
// --launch) an actual headless browser launch through the same code path.
//
//   node tool/doctor.cjs           fast checks, no browser
//   node tool/doctor.cjs --launch  also prove the browser + CDP wiring works

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const deps = require('./deps.cjs');

const results = [];
function record(status, name, detail) {
  results.push(status);
  console.log(`[${status}] ${name}${detail ? ' — ' + detail : ''}`);
}

async function main() {
  const toolDir = path.resolve(__dirname, '..');
  const launch = process.argv.includes('--launch');

  const major = parseInt(process.versions.node.split('.')[0], 10);
  record(major >= 20 ? 'PASS' : 'FAIL', 'node', `v${process.versions.node} (need >= 20)`);

  try {
    for (const name of ['cdp', 'browser', 'redactor', 'classifier', 'catalog', 'wscatalog', 'capabilities', 'har', 'store']) deps[name]();
    record('PASS', 'shared-primitives', deps.CAPTURE_TOOL_DIR);
  } catch (error) {
    record('FAIL', 'shared-primitives', error.message);
    process.exitCode = 1;
    return;
  }

  if (fs.existsSync(path.join(toolDir, 'capture.config.json'))) record('PASS', 'capture.config.json', 'present');
  else record('WARN', 'capture.config.json', 'missing — defaults will be used');

  if (!fs.existsSync(path.join(toolDir, 'sessions'))) fs.mkdirSync(path.join(toolDir, 'sessions'), {recursive: true});
  record('PASS', 'sessions folder', path.join(toolDir, 'sessions'));
  if (!fs.existsSync(path.join(toolDir, 'profiles'))) fs.mkdirSync(path.join(toolDir, 'profiles'), {recursive: true});
  record('PASS', 'profiles folder (login memory)', path.join(toolDir, 'profiles', 'browser'));

  const browserPath = deps.browser().findBrowser('');
  if (!browserPath) {
    record('FAIL', 'browser', 'No Chrome/Edge/Chromium found. Install Google Chrome, or set browserPath in capture.config.json.');
    process.exitCode = 1;
    return;
  }
  record('PASS', 'browser', browserPath);

  if (!launch) {
    record('SKIP', 'browser-launch', 'run with --launch to prove the browser + CDP wiring on this machine');
    return;
  }

  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'multi-capture-doctor-'));
  const port = await deps.browser().getFreePort();
  const logFile = path.join(tmp, 'browser.log');
  const child = deps.browser().launchBrowser({
    browserPath,
    profileDir: path.join(tmp, 'profile'),
    url: 'about:blank',
    headless: true,
    port,
    logFile,
  });
  let died = false;
  child.on('exit', () => (died = true));
  try {
    const ready = await deps.browser().waitForDevToolsEndpoint({
      port,
      profileDir: path.join(tmp, 'profile'),
      isAlive: () => !died && child.exitCode === null,
      logFile,
    });
    const cdp = await new (deps.cdp().CDP)(ready.version.webSocketDebuggerUrl).connect();
    const targets = await cdp.send('Target.getTargets');
    const pages = (targets.targetInfos || []).filter((t) => t.type === 'page').length;
    cdp.close();
    record('PASS', 'browser-launch', `CDP connected, ${pages} page target(s), ${ready.version.Browser || 'ok'}`);
  } catch (error) {
    record('FAIL', 'browser-launch', error.message);
    process.exitCode = 1;
  } finally {
    try {
      if (!child.killed) child.kill();
    } catch {
      /* ignore */
    }
  }
}

main()
  .then(() => {
    const failed = results.filter((s) => s === 'FAIL').length;
    const blocked = results.filter((s) => s === 'SKIP').length;
    console.log('');
    console.log(`RESULT: ${failed ? 'FAIL' : blocked ? 'PASS with SKIP' : 'PASS'} — FAIL ${failed}, SKIP ${blocked}. SKIP is not a pass.`);
  })
  .catch((error) => {
    console.error('[doctor] Failed:', error.message || error);
    process.exit(1);
  });
