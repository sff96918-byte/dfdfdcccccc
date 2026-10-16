'use strict';
// Shared primitives from the existing, user-tested capture tool
// (root-project/capture/tool). Nothing is duplicated or forked: the CDP client,
// browser launcher, redactor, classifier, catalogs, HAR builder and capability
// checklist are the exact same files the single-site capture tool uses, so a
// session recorded by this tool is byte-compatible with its analysers
// (including tool/compare.cjs).
//
// This tool lives outside root-project/ and never writes inside it.

const fs = require('node:fs');
const path = require('node:path');

const CAPTURE_TOOL_DIR = path.resolve(__dirname, '..', '..', 'root-project', 'capture', 'tool');
const cache = new Map();

function loadPrimitive(file) {
  if (cache.has(file)) return cache.get(file);
  const full = path.join(CAPTURE_TOOL_DIR, file);
  if (!fs.existsSync(full)) {
    throw new Error(
      [
        `Missing ${full}.`,
        'This tool reuses root-project/capture/tool/* on purpose (same CDP client, redactor and',
        'catalogs as the single-site capture tool). Keep the repository layout intact, or run the',
        'standalone capture tool inside root-project/capture/ if root-project is not present.',
      ].join(' ')
    );
  }
  const mod = require(full);
  cache.set(file, mod);
  return mod;
}

module.exports = {
  CAPTURE_TOOL_DIR,
  hooksSource: () => fs.readFileSync(path.join(CAPTURE_TOOL_DIR, 'hooks.js'), 'utf8'),
  cdp: () => loadPrimitive('cdp.cjs'),
  browser: () => loadPrimitive('browser.cjs'),
  redactor: () => loadPrimitive('redactor.cjs'),
  classifier: () => loadPrimitive('classifier.cjs'),
  catalog: () => loadPrimitive('catalog.cjs'),
  wscatalog: () => loadPrimitive('wscatalog.cjs'),
  capabilities: () => loadPrimitive('capabilities.cjs'),
  har: () => loadPrimitive('har.cjs'),
  store: () => loadPrimitive('store.cjs'),
};
