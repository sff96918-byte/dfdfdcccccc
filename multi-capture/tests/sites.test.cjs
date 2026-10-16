'use strict';
// Pure helpers: domain attribution, folder keys, the SMS summary and the run
// report. No browser, no CDP.

const {test} = require('node:test');
const assert = require('node:assert/strict');

const sites = require('../tool/sites.cjs');

test('domainOf strips www and only accepts real hosts', () => {
  assert.equal(sites.domainOf('https://www.chitchat.gg/text'), 'chitchat.gg');
  assert.equal(sites.domainOf('https://app.chitchat.gg/'), 'app.chitchat.gg');
  assert.equal(sites.domainOf('http://127.0.0.1:8080/x'), '127.0.0.1');
  assert.equal(sites.domainOf('wss://socket.site-a.com/socket.io/'), 'socket.site-a.com');
  assert.equal(sites.domainOf('about:blank'), '');
  assert.equal(sites.domainOf('chrome://newtab/'), '');
  assert.equal(sites.domainOf(''), '');
  assert.equal(sites.domainOf('not a url'), '');
});

test('siteKey is folder-safe, stable and never empty', () => {
  assert.equal(sites.siteKey('chitchat.gg'), 'chitchat.gg');
  assert.equal(sites.siteKey('WWW.Site-A.com'), 'www.site-a.com');
  assert.equal(sites.siteKey('http://x'), 'http_x');
  assert.equal(sites.siteKey(''), 'unknown');
  assert.equal(sites.siteKey('///'), 'unknown');
  assert.ok(sites.siteKey('a'.repeat(200)).length <= 60);
});

test('hostRole separates the site itself from third parties', () => {
  assert.equal(sites.hostRole('chitchat.gg', 'chitchat.gg'), 'site');
  assert.equal(sites.hostRole('api.chitchat.gg', 'chitchat.gg'), 'site');
  assert.equal(sites.hostRole('ls-entry.dditsadn.com', 'chitchat.gg'), 'third-party');
  assert.equal(sites.hostRole('notchitchat.gg', 'chitchat.gg'), 'third-party');
});

test('endpointPattern collapses ids so repeated calls group together', () => {
  assert.equal(
    sites.endpointPattern('https://api.chitchat.gg/users/me/conversations/1234567890abcdef/messages'),
    'api.chitchat.gg/users/me/conversations/:id/messages'
  );
  assert.equal(sites.endpointPattern('https://api.test/rooms/42/messages'), 'api.test/rooms/:id/messages');
  assert.equal(sites.endpointPattern('nonsense'), 'nonsense');
});

function markerRow(label, candidate, domText) {
  return {
    labeledAt: '2026-10-16T10:00:00.000Z',
    site: 'site-a.com',
    label,
    matched: Boolean(candidate),
    candidate: candidate || null,
    domMessage: domText ? {text: domText} : null,
  };
}

test('smsSummary reports CONFIRMED incoming/outgoing from the operator markers only', () => {
  const candidates = [
    {source: 'websocket', direction: 'incoming-candidate', url: 'wss://socket.site-a.com/', eventName: 'chatMessage', messageText: 'hey there', tsMs: 1},
    {source: 'websocket', direction: 'incoming-candidate', url: 'wss://socket.site-a.com/', eventName: 'chatMessage', messageText: 'second one', tsMs: 2},
    {source: 'http-request', direction: 'outgoing-candidate', url: 'https://api.site-a.com/users/me/conversations/abcdef123456/messages', messageText: 'hi there', tsMs: 3},
    {source: 'websocket', direction: 'incoming-candidate', url: 'wss://socket.site-a.com/', eventName: 'typing', tsMs: 4},
  ];
  const summary = sites.smsSummary({
    site: 'site-a.com',
    domain: 'site-a.com',
    markers: [
      markerRow('incoming', candidates[0], 'hey there'),
      markerRow('outgoing', candidates[2]),
    ],
    candidates,
  });
  assert.equal(summary.incoming.confirmed, 1);
  assert.equal(summary.incoming.confirmed_examples[0].eventName, 'chatMessage');
  assert.equal(summary.incoming.confirmed_examples[0].match, 'high (text match)');
  assert.match(summary.incoming.how_to_detect, /WS event "chatMessage"/);
  assert.equal(summary.outgoing.confirmed, 1);
  assert.equal(summary.outgoing.confirmed_examples[0].eventName, null);
  assert.match(summary.outgoing.how_to_send, /api\.site-a\.com\/users\/me\/conversations\/:id\/messages/);
  assert.deepEqual(summary.incoming.observed_only.websocket_events.sort(), ['chatMessage', 'typing']);
});

test('smsSummary never claims confirmation without a marker', () => {
  const candidates = [
    {source: 'websocket', direction: 'incoming-candidate', url: 'wss://x/', eventName: 'chatMessage', messageText: 'hello there', tsMs: 1},
  ];
  const summary = sites.smsSummary({site: 'x.com', domain: 'x.com', markers: [], candidates});
  assert.equal(summary.incoming.confirmed, 0);
  assert.match(summary.incoming.how_to_detect, /NOT CONFIRMED/);
  assert.match(summary.outgoing.how_to_send, /NOT CONFIRMED/);

  const empty = sites.smsSummary({site: 'x.com', domain: 'x.com', markers: [], candidates: []});
  assert.match(empty.incoming.how_to_detect, /nothing message-like was received/);
});

test('markerConfidence is honest about a time-window-only match', () => {
  assert.equal(sites.markerConfidence({candidate: {messageText: 'abc'}, domMessage: {text: 'xyz' + 'yz'}}), 'time-window only');
  assert.equal(sites.markerConfidence({candidate: {messageText: 'abc'}, domMessage: {text: 'x abc y'}}), 'high (text match)');
  assert.equal(sites.markerConfidence({candidate: null}), 'time-window only');
});

test('renderRunSummary shows one row per site with its folder and confirmation state', () => {
  const index = {
    tool: 'multi-capture',
    version: '0.1.0',
    startedAt: '2026-10-16T10:00:00.000Z',
    endedAt: '2026-10-16T11:00:00.000Z',
    reason: 'terminal-q',
    browser: '/usr/bin/chrome',
    profileDir: '/tmp/profile',
    redactEnabled: true,
    unattributed: 3,
    sites: [
      {
        key: 'chitchat.gg',
        domain: 'chitchat.gg',
        visits: 2,
        hosts: ['app.chitchat.gg', 'api.chitchat.gg'],
        counts: {httpRequests: 10, httpResponses: 9, wsFramesSent: 4, wsFramesReceived: 7, domMessages: 3, incomingMarkers: 1, outgoingMarkers: 1},
        capabilities: [{key: 'incoming', achieved: true}, {key: 'outgoing', achieved: false}],
      },
    ],
  };
  const text = sites.renderRunSummary(index);
  assert.match(text, /kon site er capture konta/);
  assert.match(text, /\| chitchat\.gg \| `sites\/chitchat\.gg\/` \| 2 \| 10\/9 \| 4\/7 \| 3 \| 1\/1 \| YES \| no \|/);
  assert.match(text, /Unattributed events \(before any site was known\): 3/);
  assert.match(text, /hosts: app\.chitchat\.gg, api\.chitchat\.gg/);
});

test('renderSiteSummary lists only this site and points at the sms summary', () => {
  const text = sites.renderSiteSummary({
    site: 'site-a.com',
    domain: 'site-a.com',
    storeDir: '/tmp/run/sites/site-a.com',
    counts: {httpRequests: 2, httpResponses: 2, wsConnections: 1, wsFramesSent: 1, wsFramesReceived: 1, domMessages: 2, snapshots: 1, incomingMarkers: 1, outgoingMarkers: 0, incomingCandidates: 1, outgoingCandidates: 1},
    capLines: ['- [YES] Incoming message method CONFIRMED'],
    hosts: ['app.site-a.com', 'cdn.other.net'],
    visits: 2,
    reason: 'terminal-q',
    startedAt: 'a',
    endedAt: 'b',
    redactEnabled: true,
  });
  assert.match(text, /# Capture summary — site-a\.com/);
  assert.match(text, /site: app\.site-a\.com/);
  assert.match(text, /third-party: cdn\.other\.net/);
  assert.match(text, /messages\/sms-summary\.json/);
});
