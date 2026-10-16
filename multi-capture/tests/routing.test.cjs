'use strict';
// The heart of the kit: drive the real MultiCaptureSession with synthetic CDP
// events (a fake CDP client, no browser) and prove that every event is filed
// under the domain of the tab it happened in — and never under another site's.
//
// This is the "kon site er capture konta" guarantee, tested end to end through
// the production code path (session.cjs), headless and offline.

const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const {MultiCaptureSession} = require('../tool/session.cjs');
const sites = require('../tool/sites.cjs');

class FakeCDP {
  constructor() {
    this.handlers = [];
    this.sent = [];
    this.dumps = new Map(); // sessionId -> dump object
    this.bodies = new Map(); // requestId -> body text
    this.failEvaluate = new Set();
    this.closed = false;
  }

  onEvent(fn) {
    this.handlers.push(fn);
  }

  send(method, params = {}, sessionId) {
    this.sent.push({method, params, sessionId});
    if (method === 'Runtime.evaluate') {
      if (this.failEvaluate.has(sessionId)) return Promise.reject(new Error('page gone'));
      const expression = String(params.expression || '');
      if (expression.includes('document.hasFocus')) return Promise.resolve({result: {value: false}});
      const value = this.dumps.get(sessionId);
      return Promise.resolve({result: value ? {value} : {}});
    }
    if (method === 'Target.getTargets') return Promise.resolve({targetInfos: []});
    if (method === 'Network.getResponseBody') return Promise.resolve({body: this.bodies.get(params.requestId) || '{}', base64Encoded: false});
    if (method === 'Network.getCookies') return Promise.resolve({cookies: [{name: 'sid', domain: '.site-a.com', path: '/', value: 'TOPSECRET'}]});
    if (method === 'Page.captureScreenshot') return Promise.resolve({data: Buffer.from('png-bytes').toString('base64')});
    return Promise.resolve({});
  }

  close() {
    this.closed = true;
  }

  emit(msg) {
    return Promise.all(this.handlers.map((fn) => fn(msg)));
  }
}

function newHarness() {
  const toolDir = fs.mkdtempSync(path.join(os.tmpdir(), 'multi-capture-run-'));
  const cdp = new FakeCDP();
  const logs = [];
  const session = new MultiCaptureSession({
    config: {rawMode: false, maxBodyBytes: 65536},
    toolDir,
    log: (m) => logs.push(m),
    disableTimers: true,
    cdp,
  });
  return {toolDir, cdp, logs, session};
}

const attach = (h, sessionId, targetId, url) =>
  h.cdp.emit({
    method: 'Target.attachedToTarget',
    params: {sessionId, targetInfo: {targetId, type: 'page', url}},
  });

const navigate = (h, sessionId, url) => h.cdp.emit({method: 'Page.frameNavigated', params: {frame: {url}}, sessionId});

const request = (h, sessionId, requestId, {method = 'POST', url, postData = '', type = 'XHR'} = {}) =>
  h.cdp.emit({
    method: 'Network.requestWillBeSent',
    params: {requestId, request: {method, url, postData, headers: {'content-type': 'application/json'}}, type, wallTime: 1},
    sessionId,
  });

const response = (h, sessionId, requestId, {url = '', status = 200, mimeType = 'application/json'} = {}) =>
  h.cdp.emit({method: 'Network.responseReceived', params: {requestId, response: {url, status, statusText: 'OK', headers: {}, mimeType}}, sessionId});

const finished = (h, sessionId, requestId) => h.cdp.emit({method: 'Network.loadingFinished', params: {requestId}, sessionId});

const wsCreated = (h, sessionId, requestId, url) =>
  h.cdp.emit({method: 'Network.webSocketCreated', params: {requestId, url}, sessionId});

const wsFrame = (h, sessionId, requestId, payloadData, direction = 'received') =>
  h.cdp.emit({
    method: direction === 'sent' ? 'Network.webSocketFrameSent' : 'Network.webSocketFrameReceived',
    params: {requestId, response: {opcode: 1, payloadData}},
    sessionId,
  });

const hook = (h, sessionId, payload) =>
  h.cdp.emit({
    method: 'Runtime.consoleAPICalled',
    params: {type: 'log', args: [{value: '__CAPTURE_HOOK__ ' + JSON.stringify(payload)}]},
    sessionId,
  });

const readJson = (file) => JSON.parse(fs.readFileSync(file, 'utf8'));
const readJsonl = (file) =>
  fs
    .readFileSync(file, 'utf8')
    .trim()
    .split('\n')
    .filter(Boolean)
    .map((line) => JSON.parse(line));

async function startHarness() {
  const h = newHarness();
  h.session.HOOKS_SOURCE = ''; // hooks are injected by the browser; not needed for routing tests
  await h.session.start();
  h.runDir = h.session.run.dir;
  h.siteDir = (domain) => path.join(h.runDir, 'sites', domain);
  return h;
}

// Site A: opens a socket, receives a partner message, sends one, marks both.
async function captureSiteA(h) {
  await attach(h, 'S1', 'T1', 'https://app.site-a.com/chat');
  await wsCreated(h, 'S1', 'WS1', 'wss://socket.site-a.com/socket.io/?EIO=4');
  await wsFrame(h, 'S1', 'WS1', '42["chatMessage",{"author":{"username":"stranger1"},"content":"hey there"}]');
  await request(h, 'S1', 'R1', {url: 'https://api.site-a.com/users/me/conversations/abcdef123456/messages', postData: '{"content":"hi there","nonce":"n1"}'});
  await response(h, 'S1', 'R1', {url: 'https://api.site-a.com/users/me/conversations/abcdef123456/messages'});
  await finished(h, 'S1', 'R1');
  await hook(h, 'S1', {type: 'marker', label: 'incoming', pageUrl: 'https://app.site-a.com/chat'});
  await hook(h, 'S1', {type: 'marker', label: 'outgoing', pageUrl: 'https://app.site-a.com/chat'});
}

// Site B without any message-like traffic: used to prove a marker on B cannot
// pick up a candidate that belongs to site A.
async function captureSiteBQuiet(h) {
  await attach(h, 'S2', 'T2', 'https://other-site.net/');
  await wsCreated(h, 'S2', 'WS2', 'wss://live.other-site.net/ws');
  await wsFrame(h, 'S2', 'WS2', '{"type":"typing","who":"someone99"}');
}

// Site B: own socket, own message, no markers.
async function captureSiteB(h) {
  await attach(h, 'S2', 'T2', 'https://other-site.net/');
  await wsCreated(h, 'S2', 'WS2', 'wss://live.other-site.net/ws');
  await wsFrame(h, 'S2', 'WS2', '{"type":"incoming","text":"b-message-here","from":"someone99"}');
}

test('two sites in two tabs become two folders, and nothing is mixed', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await captureSiteB(h);

  assert.deepEqual([...h.session.runtimes.keys()].sort(), ['app.site-a.com', 'other-site.net']);
  assert.ok(fs.existsSync(path.join(h.siteDir('app.site-a.com'), 'messages', 'incoming-candidates.jsonl')));
  assert.ok(fs.existsSync(path.join(h.siteDir('other-site.net'), 'messages', 'incoming-candidates.jsonl')));

  const aIncoming = readJsonl(path.join(h.siteDir('app.site-a.com'), 'messages', 'incoming-candidates.jsonl'));
  const bIncoming = readJsonl(path.join(h.siteDir('other-site.net'), 'messages', 'incoming-candidates.jsonl'));
  assert.deepEqual(aIncoming.map((r) => r.messageText), ['hey there']);
  assert.deepEqual(aIncoming.map((r) => r.eventName), ['chatMessage']);
  assert.deepEqual(bIncoming.map((r) => r.messageText), ['b-message-here']);
  assert.ok(!JSON.stringify(aIncoming).includes('b-message-here'));
  assert.ok(!JSON.stringify(bIncoming).includes('hey there'));

  // Per-socket frame files are tab-scoped, so identical CDP request ids in two
  // tabs can never overwrite each other.
  assert.ok(fs.existsSync(path.join(h.siteDir('app.site-a.com'), 'websockets', 'S1_WS1.jsonl')));
  assert.ok(fs.existsSync(path.join(h.siteDir('other-site.net'), 'websockets', 'S2_WS2.jsonl')));
  assert.ok(!fs.existsSync(path.join(h.siteDir('app.site-a.com'), 'websockets', 'S2_WS2.jsonl')));

  const a = h.session.runtimes.get('app.site-a.com').store.counts;
  const b = h.session.runtimes.get('other-site.net').store.counts;
  assert.equal(a.wsFramesReceived, 1);
  assert.equal(b.wsFramesReceived, 1);
  assert.equal(a.httpRequests, 1);
  assert.equal(b.httpRequests, 0, "site B must not inherit site A's HTTP counts");
  assert.equal(a.outgoingCandidates, 1);
  assert.equal(b.outgoingCandidates, 0);
  assert.equal(a.incomingMarkers, 1);
  assert.equal(b.incomingMarkers, 0);
});

test('a marker on one site never matches another site\'s candidate', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await captureSiteBQuiet(h);
  // Site A has a fresh incoming candidate. Now mark an incoming message while
  // looking at site B, where nothing message-like was received: it must stay
  // unmatched instead of stealing site A's evidence.
  await hook(h, 'S2', {type: 'marker', label: 'incoming', pageUrl: 'https://other-site.net/'});

  const bLabeled = readJsonl(path.join(h.siteDir('other-site.net'), 'messages', 'labeled-incoming.jsonl'));
  assert.equal(bLabeled.length, 1);
  assert.equal(bLabeled[0].matched, false);
  assert.equal(bLabeled[0].candidate, null);
  assert.equal(bLabeled[0].site, 'other-site.net');

  const aLabeled = readJsonl(path.join(h.siteDir('app.site-a.com'), 'messages', 'labeled-incoming.jsonl'));
  assert.equal(aLabeled.length, 1);
  assert.equal(aLabeled[0].matched, true);
  assert.equal(aLabeled[0].candidate.messageText, 'hey there');
  assert.equal(h.session.runtimes.get('app.site-a.com').store.counts.incomingMarkers, 1);
  assert.equal(h.session.runtimes.get('other-site.net').store.counts.incomingMarkers, 1);
});

test('same tab navigating to another site opens that site\'s folder for later events', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await navigate(h, 'S1', 'https://third-site.org/inbox');
  await wsCreated(h, 'S1', 'WS9', 'wss://third-site.org/ws');
  await wsFrame(h, 'S1', 'WS9', '{"text":"third-site-message"}');

  assert.ok(h.session.runtimes.has('third-site.org'));
  const third = readJsonl(path.join(h.siteDir('third-site.org'), 'messages', 'incoming-candidates.jsonl'));
  assert.deepEqual(third.map((r) => r.messageText), ['third-site-message']);
  const a = readJsonl(path.join(h.siteDir('app.site-a.com'), 'messages', 'incoming-candidates.jsonl'));
  assert.ok(!JSON.stringify(a).includes('third-site-message'), "the earlier site keeps only its own evidence");

  const aVisits = readJsonl(path.join(h.siteDir('app.site-a.com'), 'visits.jsonl')).map((r) => r.event);
  const thirdVisits = readJsonl(path.join(h.siteDir('third-site.org'), 'visits.jsonl')).map((r) => r.event);
  assert.deepEqual(aVisits, ['visit_started']);
  assert.deepEqual(thirdVisits, ['visit_started']);
});

test('third-party hosts are recorded against the site that used them', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await captureSiteB(h);
  await request(h, 'S1', 'R7', {method: 'GET', url: 'https://cdn.other.net/pixel.js', type: 'Script'});
  await response(h, 'S1', 'R7', {url: 'https://cdn.other.net/pixel.js', mimeType: 'application/javascript'});
  await finished(h, 'S1', 'R7');

  const aHosts = [...h.session.runtimes.get('app.site-a.com').store.hosts].sort();
  const bHosts = [...h.session.runtimes.get('other-site.net').store.hosts].sort();
  assert.deepEqual(aHosts, ['api.site-a.com', 'app.site-a.com', 'cdn.other.net', 'socket.site-a.com']);
  assert.deepEqual(bHosts, ['live.other-site.net', 'other-site.net']);

  // Role labels in the report: the site itself (and its siblings) vs third party.
  assert.equal(sites.hostRole('api.site-a.com', 'app.site-a.com'), 'site');
  assert.equal(sites.hostRole('cdn.other.net', 'app.site-a.com'), 'third-party');
});

test('a chat inside an iframe stays in the parent tab\'s site folder', async () => {
  const h = await startHarness();
  await attach(h, 'S1', 'T1', 'https://portal.example.com/chat');
  // The site embeds its chat client in an OOPIF on another host.
  await h.cdp.emit({
    method: 'Target.attachedToTarget',
    params: {sessionId: 'F1', targetInfo: {targetId: 'T1F', type: 'iframe', url: 'https://chat-widget.other-cdn.net/frame'}},
    sessionId: 'S1',
  });
  await wsCreated(h, 'F1', 'WSF', 'wss://chat-widget.other-cdn.net/socket.io/');
  await wsFrame(h, 'F1', 'WSF', '42["chatMessage",{"author":{"username":"iframe-person"},"content":"iframe message"}]');
  await hook(h, 'F1', {type: 'marker', label: 'incoming', pageUrl: 'https://chat-widget.other-cdn.net/frame'});

  assert.deepEqual([...h.session.runtimes.keys()], ['portal.example.com'], 'the iframe host must not become its own site');
  const dir = h.siteDir('portal.example.com');
  const incoming = readJsonl(path.join(dir, 'messages', 'incoming-candidates.jsonl'));
  assert.deepEqual(incoming.map((r) => r.messageText), ['iframe message']);
  const labeled = readJsonl(path.join(dir, 'messages', 'labeled-incoming.jsonl'));
  assert.equal(labeled[0].matched, true, 'the iframe message is the marker evidence for the parent site');
  const hosts = [...h.session.runtimes.get('portal.example.com').store.hosts].sort();
  assert.deepEqual(hosts, ['chat-widget.other-cdn.net', 'portal.example.com']);
});

test('a blank or unknown page is counted as unattributed, never guessed into a site', async () => {
  const h = await startHarness();
  await attach(h, 'S5', 'T5', 'about:blank');
  await wsCreated(h, 'S5', 'WS5', 'wss://mystery.test/ws');
  await wsFrame(h, 'S5', 'WS5', '{"text":"who am i talking to"}');
  assert.equal(h.session.runtimes.size, 0, 'no site folder may be invented');
  assert.ok(h.session.unattributed > 0);
  assert.equal(h.session.siteTable().length, 0);
});

test('tab close is recorded on the right site', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await captureSiteB(h);
  await h.cdp.emit({method: 'Target.targetDestroyed', params: {targetId: 'T2'}});
  const events = readJsonl(path.join(h.siteDir('other-site.net'), 'visits.jsonl')).map((r) => r.event);
  assert.deepEqual(events.sort(), ['tab_closed', 'visit_started']);
  assert.ok(!fs.existsSync(path.join(h.siteDir('app.site-a.com'), 'visits.jsonl')) === false);
  const aEvents = readJsonl(path.join(h.siteDir('app.site-a.com'), 'visits.jsonl')).map((r) => r.event);
  assert.deepEqual(aEvents, ['visit_started'], 'closing tab B must not touch site A');
  assert.equal(h.session.sessions.has('S2'), false);
  assert.equal(h.session.sessions.has('S1'), true);
});

test('stop() writes a complete, self-consistent report per site and for the run', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await captureSiteB(h);
  await request(h, 'S1', 'R8', {url: 'https://api.site-a.com/users/me/conversations/abcdef123456/messages', postData: '{"content":"second out","nonce":"n2"}'});
  await response(h, 'S1', 'R8', {url: 'https://api.site-a.com/users/me/conversations/abcdef123456/messages'});
  h.cdp.bodies.set('R8', JSON.stringify({ok: true}));
  await finished(h, 'S1', 'R8');

  h.cdp.dumps.set('S1', {url: 'https://app.site-a.com/chat', title: 'A chat', html: '<html>site-a-final</html>', localStorage: {token: 'SECRET-A'}, sessionStorage: {}});
  h.cdp.dumps.set('S2', {url: 'https://other-site.net/', title: 'B', html: '<html>site-b-final</html>', localStorage: {}, sessionStorage: {}});
  await h.session.stop('test-stop');

  for (const domain of ['app.site-a.com', 'other-site.net']) {
    const dir = h.siteDir(domain);
    for (const rel of ['SUMMARY.md', 'SHARE-README.md', 'manifest.json', 'network.har', 'api-catalog.json', 'messages/correlation.json', 'messages/sms-summary.json', 'websockets/catalog.json']) {
      assert.ok(fs.existsSync(path.join(dir, rel)), `${domain} is missing ${rel}`);
    }
    const dom = fs.readdirSync(path.join(dir, 'dom'));
    assert.ok(dom.some((name) => name.endsWith('-final.html')), `${domain} has no final DOM snapshot`);
    assert.ok(fs.readdirSync(path.join(dir, 'screenshots')).length >= 1);
    const storage = readJson(path.join(dir, 'storage', dom.filter((n) => n.endsWith('-final.html'))[0].replace('.html', '.json')));
    assert.equal(storage.cookies[0].value, 'REDACTED', 'cookies stay redacted by default');
    assert.equal(storage.site, domain);
  }

  // localStorage must be redacted per site (name "token" is a known secret).
  const aStorageDir = path.join(h.siteDir('app.site-a.com'), 'storage');
  const aStorage = readJson(path.join(aStorageDir, fs.readdirSync(aStorageDir)[0]));
  assert.equal(aStorage.localStorage.token, 'REDACTED');

  const aSms = readJson(path.join(h.siteDir('app.site-a.com'), 'messages', 'sms-summary.json'));
  assert.equal(aSms.site, 'app.site-a.com');
  assert.equal(aSms.incoming.confirmed, 1);
  assert.match(aSms.incoming.how_to_detect, /chatMessage/);
  assert.equal(aSms.outgoing.confirmed, 1);
  assert.match(aSms.outgoing.how_to_send, /api\.site-a\.com\/users\/me\/conversations\/:id\/messages/);

  const bSms = readJson(path.join(h.siteDir('other-site.net'), 'messages', 'sms-summary.json'));
  assert.equal(bSms.incoming.confirmed, 0);
  assert.match(bSms.incoming.how_to_detect, /NOT CONFIRMED/);
  assert.ok(!JSON.stringify(bSms).includes('hey there'), 'site B report must not contain site A evidence');

  const runIndex = readJson(path.join(h.runDir, 'run.json'));
  assert.equal(runIndex.sites.length, 2);
  assert.equal(runIndex.reason, 'test-stop');
  assert.deepEqual(runIndex.sites.map((s) => s.dir).sort(), ['sites/app.site-a.com', 'sites/other-site.net']);
  const runSummary = fs.readFileSync(path.join(h.runDir, 'RUN-SUMMARY.md'), 'utf8');
  assert.match(runSummary, /app\.site-a\.com/);
  assert.match(runSummary, /other-site\.net/);
  assert.match(runSummary, /sites\/app\.site-a\.com\//);

  // The per-site manifest is per site, and the shared tool's compare/analysers
  // read exactly these keys.
  const aManifest = readJson(path.join(h.siteDir('app.site-a.com'), 'manifest.json'));
  assert.equal(aManifest.domain, 'app.site-a.com');
  assert.equal(aManifest.site, 'app.site-a.com');
  assert.equal(aManifest.counts.httpRequests, 2);
  const bManifest = readJson(path.join(h.siteDir('other-site.net'), 'manifest.json'));
  assert.equal(bManifest.counts.httpRequests, 0);
});

test('stop() still produces reports when a page died before the final snapshot', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  h.cdp.failEvaluate.add('S1');
  await h.session.stop('page-gone');
  assert.ok(fs.existsSync(path.join(h.siteDir('app.site-a.com'), 'SUMMARY.md')));
  const sms = readJson(path.join(h.siteDir('app.site-a.com'), 'messages', 'sms-summary.json'));
  assert.equal(sms.incoming.confirmed, 1);
  assert.equal(fs.readdirSync(path.join(h.siteDir('app.site-a.com'), 'dom')).length, 0, 'no snapshot file is faked when the page is gone');
});

test('site folders are announced to the operator with their domain', async () => {
  const h = await startHarness();
  await captureSiteA(h);
  await captureSiteB(h);
  const text = h.logs.join('\n');
  assert.match(text, /NEW SITE: app\.site-a\.com => sites\/app\.site-a\.com\//);
  assert.match(text, /NEW SITE: other-site\.net => sites\/other-site\.net\//);
});
