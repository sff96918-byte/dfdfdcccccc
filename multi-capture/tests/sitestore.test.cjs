'use strict';
// Folder layout: one folder per domain, identical in shape to the single-site
// capture tool, and no file is ever shared between two sites.

const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const {RunStore, SiteStore, freshCounts, freshCoverage} = require('../tool/sitestore.cjs');

function tmpDir() {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'multi-capture-store-'));
}

test('a run creates one folder per domain under sessions/<run>/sites/', () => {
  const base = tmpDir();
  const run = new RunStore(base);
  const a = run.site('chitchat.gg');
  const b = run.site('www.other-site.net');
  assert.equal(a.dir, path.join(run.dir, 'sites', 'chitchat.gg'));
  assert.equal(b.dir, path.join(run.dir, 'sites', 'www.other-site.net'));
  assert.equal(a.relativeDir, path.posix.join('sites', 'chitchat.gg'));
  assert.ok(fs.existsSync(path.join(a.dir, 'messages')));
  assert.ok(fs.existsSync(path.join(a.dir, 'websockets')));
  assert.ok(fs.existsSync(path.join(a.dir, 'http')));
  assert.ok(fs.existsSync(path.join(a.dir, 'dom')));
  assert.ok(fs.existsSync(path.join(a.dir, 'storage')));
  assert.ok(fs.existsSync(path.join(a.dir, 'screenshots')));
});

test('jsonl appends stay inside the site that wrote them', () => {
  const base = tmpDir();
  const run = new RunStore(base);
  const a = run.site('site-a.com');
  const b = run.site('site-b.net');
  a.appendJsonl('messages/incoming-candidates.jsonl', {site: 'site-a.com', messageText: 'a1'});
  a.appendJsonl('messages/incoming-candidates.jsonl', {site: 'site-a.com', messageText: 'a2'});
  b.appendJsonl('messages/incoming-candidates.jsonl', {site: 'site-b.net', messageText: 'b1'});
  const lines = (dir) =>
    fs
      .readFileSync(path.join(dir, 'messages', 'incoming-candidates.jsonl'), 'utf8')
      .trim()
      .split('\n')
      .map((l) => JSON.parse(l).messageText);
  assert.deepEqual(lines(a.dir), ['a1', 'a2']);
  assert.deepEqual(lines(b.dir), ['b1']);
});

test('counts, coverage, hosts and visits are per site', () => {
  const base = tmpDir();
  const run = new RunStore(base);
  const a = run.site('site-a.com');
  const b = run.site('site-b.net');
  a.counts.httpRequests += 3;
  b.counts.httpRequests += 1;
  a.coverage.markers = true;
  a.noteHost('api.site-a.com', 'site-a.com');
  a.noteHost('cdn.other.net', 'site-a.com');
  a.visits += 1;
  assert.equal(a.counts.httpRequests, 3);
  assert.equal(b.counts.httpRequests, 1);
  assert.equal(b.coverage.markers, false);
  assert.deepEqual([...a.hosts].sort(), ['api.site-a.com', 'cdn.other.net']);
  assert.equal(a.visits, 1);
  assert.equal(b.visits, 0);
  assert.deepEqual(Object.keys(freshCounts()).sort(), Object.keys(a.counts).sort());
  assert.deepEqual(Object.keys(freshCoverage()).sort(), Object.keys(a.coverage).sort());
});

test('a site store never escapes the run folder', () => {
  const base = tmpDir();
  const run = new RunStore(base);
  const evil = run.site('../../etc');
  assert.ok(evil.dir.startsWith(path.join(run.dir, 'sites')));
  assert.equal(evil.key.includes('/'), false);
});

test('the same domain always maps to the same folder (second visit appends)', () => {
  const base = tmpDir();
  const run = new RunStore(base);
  const first = run.site('chitchat.gg');
  first.appendJsonl('visits.jsonl', {event: 'visit_started'});
  const again = run.site('chitchat.gg');
  again.appendJsonl('visits.jsonl', {event: 'visit_started'});
  assert.equal(first.dir, again.dir);
  const rows = fs
    .readFileSync(path.join(again.dir, 'visits.jsonl'), 'utf8')
    .trim()
    .split('\n')
    .map((l) => JSON.parse(l).event);
  assert.deepEqual(rows, ['visit_started', 'visit_started']);
});

test('site manifests are separate files', () => {
  const base = tmpDir();
  const run = new RunStore(base);
  const a = run.site('site-a.com');
  const b = run.site('site-b.net');
  a.writeManifest({site: 'site-a.com'});
  b.writeManifest({site: 'site-b.net'});
  assert.equal(JSON.parse(fs.readFileSync(path.join(a.dir, 'manifest.json'), 'utf8')).site, 'site-a.com');
  assert.equal(JSON.parse(fs.readFileSync(path.join(b.dir, 'manifest.json'), 'utf8')).site, 'site-b.net');
  assert.equal(SiteStore.name, 'SiteStore');
});
