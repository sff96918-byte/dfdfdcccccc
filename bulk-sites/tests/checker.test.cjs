'use strict';
// End-to-end checks: a real jsdom page + a real observer script, through the
// same code path the CLI uses. This is what proves the kit is site-agnostic
// (two completely different pages both satisfy one contract) and that it
// catches a wrong adapter.

const {test} = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const {check, siteTargets, ROOT} = require('../kit/run.cjs');

const CHITCHAT_PAGE = path.join(ROOT, '..', 'root-project', 'tests', 'fixtures', 'showmode-chat.html');
const CHITCHAT_OBSERVER = path.join(ROOT, '..', 'root-project', 'internal', 'browser', 'showmode_dom.js');
const ROOM_PAGE = path.join(ROOT, 'fixtures', 'example-room.html');
const ROOM_OBSERVER = path.join(ROOT, 'sites', 'example-room', 'observer.js');

const fails = (result) => result.rows.filter((r) => r.status === 'FAIL').map((r) => r.id.split(' ')[0]);
const statusOf = (result, id) => (result.rows.find((r) => r.id.startsWith(id)) || {}).status;

test('the ChitChat Show Mode helper satisfies the contract on its own page', () => {
  const result = check(siteTargets('chitchat'));
  assert.deepEqual(fails(result), [], 'failing rules: ' + fails(result).join(', '));
  assert.equal(statusOf(result, 'R4'), 'PASS');
  assert.equal(statusOf(result, 'R5'), 'PASS');
  assert.equal(statusOf(result, 'R10'), 'PASS');
  assert.equal(statusOf(result, 'R12'), 'PASS', 'it must stay silent on the other site page');
  assert.equal(result.snapshot.messages.length, 3);
});

test('a second site with unrelated markup satisfies the same contract', () => {
  const result = check(siteTargets('example-room'));
  assert.deepEqual(fails(result), [], 'failing rules: ' + fails(result).join(', '));
  assert.equal(result.snapshot.controls.phase, 'chatting');
  assert.equal(result.snapshot.controls.partner, 'Strange42', 'partner comes from the newest non-own row');
  assert.equal(result.snapshot.controls.ownName, 'Guest Me');
  assert.equal(result.snapshot.messages.find((m) => m.id === 'm2').author, 'Strange42', 'grouped row inherits its author');
  assert.deepEqual(result.snapshot.controls.buttons, ['NEXT']);
  assert.equal(statusOf(result, 'R12'), 'PASS');
});

test('the checker catches the classic adapter mistakes instead of passing them', () => {
  const result = check({
    name: 'broken',
    url: 'https://app.chitchat.gg/',
    observerPath: path.join(ROOT, 'fixtures', 'broken-observer.js'),
    pagePath: CHITCHAT_PAGE,
    foreignPagePath: ROOM_PAGE,
    kind: 'dom',
    status: 'negative fixture'
  });
  const caught = fails(result);
  for (const id of ['R4', 'R5', 'R8', 'R10', 'R11', 'R12', 'R13']) {
    assert.ok(caught.includes(id), id + ' should fail for the broken adapter, got: ' + (caught.join(', ') || 'none'));
  }
  assert.equal(result.summary.pass < result.summary.total, true);
});

test('a foreign page cannot make either adapter claim a live chat', () => {
  // Each observer is pointed at the OTHER site's page. Neither may report a
  // chat, a partner or a clickable control there: that is what stops a wrong
  // adapter from typing into an unrelated page.
  const roomObserverOnChatPage = check({
    name: 'room-on-chitchat',
    url: 'https://example.invalid/room',
    observerPath: ROOM_OBSERVER,
    pagePath: CHITCHAT_PAGE,
    foreignPagePath: CHITCHAT_PAGE,
    kind: 'dom',
    status: 'ad-hoc'
  });
  assert.equal(roomObserverOnChatPage.snapshot.controls.canSend, false);
  assert.equal(roomObserverOnChatPage.snapshot.controls.phase, 'lobby');
  assert.equal(statusOf(roomObserverOnChatPage, 'R12'), 'PASS');

  const chitchatObserverOnRoomPage = check({
    name: 'chitchat-on-room',
    url: 'https://app.chitchat.gg/',
    observerPath: CHITCHAT_OBSERVER,
    pagePath: ROOM_PAGE,
    foreignPagePath: ROOM_PAGE,
    kind: 'dom',
    status: 'ad-hoc'
  });
  assert.equal(chitchatObserverOnRoomPage.snapshot.controls.canSend, false);
  assert.equal(statusOf(chitchatObserverOnRoomPage, 'R12'), 'PASS');
});

test('an unknown site name is refused with the list of known sites', () => {
  assert.throws(() => siteTargets('nope'), /unknown site "nope"/);
});
