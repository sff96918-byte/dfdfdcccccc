'use strict';
// Entry point for the multi-site capture kit.
//
//   node tool/main.cjs [--url https://first-site.com] [--headless] [--raw]
//                      [--browser <path>] [--profile <dir>]
//
// ONE browser window opens and stays open. Open site after site in new tabs
// (Ctrl+T), log in normally, chat normally. Every site gets its own capture
// folder automatically (sessions/<run>/sites/<domain>/), so nothing is mixed.
//
// Stop with Q in this terminal, Ctrl+C, or by closing the browser window.

const path = require('node:path');
const readline = require('node:readline');
const fs = require('node:fs');

const { MultiCaptureSession } = require('./session.cjs');

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i += 1) {
    const token = argv[i];
    const next = () => argv[++i];
    if (token === '--url') out.startUrl = next();
    else if (token === '--headless') out.headless = true;
    else if (token === '--visible') out.headless = false;
    else if (token === '--raw') out.rawMode = true;
    else if (token === '--browser') out.browserPath = next();
    else if (token === '--profile') out.profileDir = next();
    else if (token === '--config') out.configPath = next();
    else if (token === '--help' || token === '-h') out.help = true;
    else throw new Error('unknown argument: ' + token);
  }
  return out;
}

function loadConfig(toolDir, overrides) {
  const defaults = {
    startUrl: 'about:blank',
    browserPath: '',
    profileDir: '',
    headless: false,
    rawMode: false,
    maxBodyBytes: 262144,
  };
  let fileConfig = {};
  const configPath = overrides.configPath
    ? path.resolve(overrides.configPath)
    : path.join(toolDir, 'capture.config.json');
  if (fs.existsSync(configPath)) {
    try {
      fileConfig = JSON.parse(fs.readFileSync(configPath, 'utf8'));
    } catch (error) {
      throw new Error(`${configPath} is not valid JSON: ${error.message}`);
    }
  }
  const merged = {...defaults, ...fileConfig, ...overrides};
  delete merged.configPath;
  return merged;
}

function usage() {
  process.stdout.write(
    [
      'Multi-site capture kit',
      '',
      '  node tool/main.cjs [--url <start url>] [--headless] [--raw]',
      '                      [--browser <chrome path>] [--profile <dir>] [--config <json>]',
      '',
      'One browser opens; open each site in its own tab. Capture folders are per',
      'domain: sessions/<run>/sites/<domain>/',
      '',
      'Terminal keys while capturing:',
      '  i  save an INCOMING example (partner message just arrived)',
      '  o  save an OUTGOING example (you just sent a message)',
      '  s  save a page snapshot',
      '  l  list site folders + counts',
      '  t  per-site table',
      '  q  stop the run (closes the browser window)',
      '',
    ].join('\n')
  );
}

function tableLines(rows) {
  const head = ['SITE', 'FOLDER', 'VISITS', 'HTTP', 'WS S/R', 'DOM', 'SNAP', 'MARKERS I/O', 'INCOMING', 'OUTGOING'];
  const body = rows.map((r) => [r.site, r.dir, String(r.visits), r.http, r.wsFrames, String(r.dom), String(r.snapshots), r.markers, r.incoming, r.outgoing]);
  const widths = head.map((h, i) => Math.max(h.length, ...body.map((b) => String(b[i]).length)));
  const line = (cells) => cells.map((c, i) => String(c).padEnd(widths[i])).join('  ');
  return [line(head), widths.map((w) => '-'.repeat(w)).join('  '), ...body.map(line)];
}

async function main() {
  const toolDir = path.resolve(__dirname, '..');
  let args;
  try {
    args = parseArgs(process.argv.slice(2));
  } catch (error) {
    process.stderr.write(error.message + '\n');
    process.exitCode = 2;
    return;
  }
  if (args.help) {
    usage();
    return;
  }
  const config = loadConfig(toolDir, args);
  const session = new MultiCaptureSession({config, toolDir});
  await session.start();

  const shutdown = (reason) => {
    session.stop(reason).catch(() => {});
  };
  process.on('SIGINT', () => shutdown('ctrl-c'));
  process.on('SIGTERM', () => shutdown('sigterm'));

  const help = () => {
    session.log('[capture] keys: i=incoming marker, o=outgoing marker, s=snapshot, l=site list, t=site table, q=stop');
  };

  if (process.stdin.isTTY) {
    readline.emitKeypressEvents(process.stdin);
    process.stdin.setRawMode(true);
    process.stdin.resume();
    process.stdin.on('keypress', (str, key) => {
      if (!key) return;
      if (key.ctrl && key.name === 'c') return shutdown('ctrl-c');
      const sessionId = session.activeSessionId();
      if (key.name === 'q') shutdown('terminal-q');
      else if (key.name === 'i') session.handleMarker('incoming', {sessionId});
      else if (key.name === 'o') session.handleMarker('outgoing', {sessionId});
      else if (key.name === 's') session.handleMarker('snapshot', {sessionId});
      else if (key.name === 'l') {
        const rows = session.siteTable();
        if (!rows.length) session.log('[capture] ekhono kono site dhora poreni.');
        for (const row of rows) session.log(`[capture] ${row.site} -> ${row.dir} (visits ${row.visits}, HTTP ${row.http}, WS ${row.wsFrames}, markers ${row.markers})`);
      } else if (key.name === 't') {
        const rows = session.siteTable();
        if (!rows.length) session.log('[capture] ekhono kono site dhora poreni.');
        else for (const line of tableLines(rows)) session.log('[capture] ' + line);
      } else if (key.name === 'h' || key.name === '?') help();
    });
  }

  await session.done;
  process.exit(0);
}

main().catch((error) => {
  console.error('[capture] Failed:', error.message || error);
  process.exit(1);
});
