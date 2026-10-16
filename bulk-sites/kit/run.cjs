'use strict';
// Site resolution + one full contract run. Used by the CLI and by the tests.

const fs = require('node:fs');
const path = require('node:path');

const {checkStatic, checkInteraction, summarize} = require('./contract.cjs');
const {evaluatePage} = require('./harness.cjs');

const ROOT = path.resolve(__dirname, '..');

function readJson(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function siteRegistry() {
  return readJson(path.join(ROOT, 'sites.json'));
}

function resolveFromRoot(value) {
  return path.isAbsolute(value) ? value : path.resolve(ROOT, value);
}

// A site entry looks like:
//   "mysite": {"kind":"dom", "url":"https://...", "observer":"sites/mysite/observer.js",
//              "page":"fixtures/mysite-chat.html", "status":"..."}
function siteTargets(target) {
  const registry = siteRegistry();
  const list = Object.keys(registry);
  const name = target || list[0];
  const site = registry[name];
  if (!site) throw new Error('unknown site "' + name + '" (known: ' + list.join(', ') + ')');
  const targets = {
    name,
    url: site.url,
    observerPath: resolveFromRoot(site.observer),
    pagePath: resolveFromRoot(site.page),
    foreignPagePath: site.foreign_page ? resolveFromRoot(site.foreign_page) : path.join(ROOT, 'fixtures', 'foreign-room.html'),
    status: site.status || '',
    kind: site.kind || 'dom'
  };
  for (const key of ['observerPath', 'pagePath']) {
    if (!fs.existsSync(targets[key])) throw new Error('missing file for ' + key + ': ' + targets[key]);
  }
  if (targets.kind !== 'dom') {
    throw new Error(
      '"' + name + '" is kind=' + targets.kind + ': this kit only checks DOM observers. An API/WebSocket site needs a transport adapter instead (see README, tier 2).'
    );
  }
  return targets;
}

function check(target) {
  const observerSource = fs.readFileSync(target.observerPath, 'utf8');
  const html = fs.readFileSync(target.pagePath, 'utf8');
  const foreignHtml = target.foreignPagePath && fs.existsSync(target.foreignPagePath) ? fs.readFileSync(target.foreignPagePath, 'utf8') : null;

  const observed = evaluatePage({observerSource, html, url: target.url, foreignHtml});
  const rows = [
    ...checkStatic({snapshot: observed.first, secondSnapshot: observed.second, probeText: observed.probeText}),
    ...checkInteraction({labels: observed.first.controls.buttons, clicks: observed.clicks, foreign: observed.foreign})
  ];
  return {name: target.name, rows, summary: summarize(rows), snapshot: observed.first, probe: observed.probeText};
}

module.exports = {ROOT, readJson, siteRegistry, siteTargets, check};
