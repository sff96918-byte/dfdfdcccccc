'use strict';
// Folder layout for one browser run with many sites.
//
//   sessions/<run-stamp>/
//     run.json                 run index (which site is in which folder)
//     RUN-SUMMARY.md           human answer to "kon site er capture konta"
//     browser.log              the browser's own stdout/stderr for this run
//     sites/<domain>/          one folder per site — layout identical to the
//                              single-site capture tool, so its analysers and
//                              tool/compare.cjs work on it unchanged
//
// A site folder is created the first time that domain is really seen in a tab;
// an unknown/blank page never creates one.

const fs = require('node:fs');
const path = require('node:path');

const { siteKey } = require('./sites.cjs');
const deps = require('./deps.cjs');

const SUB_DIRS = ['', 'websockets', 'http', 'messages', 'dom', 'screenshots', 'storage'];

function freshCounts() {
  return {
    httpRequests: 0,
    httpResponses: 0,
    wsConnections: 0,
    wsFramesSent: 0,
    wsFramesReceived: 0,
    hookEvents: 0,
    domMessages: 0,
    markers: 0,
    incomingCandidates: 0,
    outgoingCandidates: 0,
    incomingMarkers: 0,
    outgoingMarkers: 0,
    snapshots: 0,
    consoleMessages: 0,
    pageErrors: 0,
  };
}

function freshCoverage() {
  return {
    http: false,
    websockets: false,
    domSnapshots: false,
    domMessages: false,
    storage: false,
    console: false,
    screenshots: false,
    markers: false,
  };
}

class SiteStore {
  constructor(runDir, domain) {
    this.domain = domain;
    this.key = siteKey(domain);
    this.dir = path.join(runDir, 'sites', this.key);
    for (const sub of SUB_DIRS) fs.mkdirSync(path.join(this.dir, sub), {recursive: true});
    this.counts = freshCounts();
    this.coverage = freshCoverage();
    this.hosts = new Set();
    this.visits = 0;
    this.firstSeen = new Date().toISOString();
    this.lastSeen = this.firstSeen;
    this.relativeDir = path.posix.join('sites', this.key);
  }

  touch() {
    this.lastSeen = new Date().toISOString();
  }

  noteHost(host, domain) {
    if (!host) return;
    this.hosts.add(String(host).toLowerCase());
  }

  appendJsonl(relPath, obj) {
    fs.appendFileSync(path.join(this.dir, relPath), JSON.stringify(obj) + '\n');
  }

  event(type, data) {
    this.touch();
    this.appendJsonl('events.jsonl', {ts: new Date().toISOString(), type, ...(data || {})});
  }

  write(relPath, content) {
    fs.writeFileSync(path.join(this.dir, relPath), content);
  }

  writeJson(relPath, obj) {
    this.write(relPath, JSON.stringify(obj, null, 2) + '\n');
  }

  writeManifest(manifest) {
    this.writeJson('manifest.json', manifest);
  }

  fileExists(relPath) {
    return fs.existsSync(path.join(this.dir, relPath));
  }
}

class RunStore {
  constructor(baseDir) {
    const stamp = deps.store().stamp();
    this.dir = path.join(baseDir, `${stamp}_multi`);
    fs.mkdirSync(path.join(this.dir, 'sites'), {recursive: true});
    this.sitesDir = path.join(this.dir, 'sites');
    this.createdAt = new Date().toISOString();
  }

  site(domain) {
    // SiteStore appends the "sites/" level itself, so hand it the run root.
    return new SiteStore(this.dir, domain);
  }

  writeJson(relPath, obj) {
    fs.writeFileSync(path.join(this.dir, relPath), JSON.stringify(obj, null, 2) + '\n');
  }

  write(relPath, content) {
    fs.writeFileSync(path.join(this.dir, relPath), content);
  }

  browserLogPath() {
    return path.join(this.dir, 'browser.log');
  }
}

module.exports = {SiteStore, RunStore, freshCounts, freshCoverage, SUB_DIRS};
