'use strict';
// Pure rule tests: no jsdom, no browser. Each test pins one contract rule so a
// later adapter cannot silently weaken the contract.

const {test} = require('node:test');
const assert = require('node:assert/strict');

const {checkStatic, checkInteraction, summarize} = require('../kit/contract.cjs');

function controls(overrides) {
  return Object.assign(
    {
      hasInput: true,
      inputDesc: 'textarea:Send a message',
      buttons: ['SKIP'],
      chatEnded: false,
      partner: 'Partner',
      ownName: 'Me',
      conversation: 'doc:1',
      phase: 'chatting',
      canSend: true,
      observerReady: true,
      overflow: false
    },
    overrides || {}
  );
}

function sample(overrides, messageOverrides) {
  const options = overrides || {};
  const snapshot = {
    controls: controls(options),
    messages: messageOverrides ||
      options.messages || [
        {id: 'a', author: 'Partner', text: 'hey there my friend', order: 1},
        {id: 'b', author: 'Partner', text: 'how are you today', order: 2},
        {id: 'c', author: 'Me', text: 'good thanks', order: 3}
      ],
    revision: 3
  };
  return {snapshot, secondSnapshot: JSON.parse(JSON.stringify(snapshot)), probeText: 'phase=chatting rows=3 input=true action=SKIP'};
}

function status(rows, id) {
  const found = rows.find((r) => r.id.startsWith(id));
  assert.ok(found, 'rule ' + id + ' missing from ' + rows.map((r) => r.id).join(', '));
  return found.status;
}

test('R1 fails when the observer emits something other than the agreed shape', () => {
  const rows = checkStatic({snapshot: {messages: []}, probeText: ''});
  assert.equal(status(rows, 'R1'), 'FAIL');
  assert.equal(rows.length, 1, 'later rules must not run on an unusable snapshot');
});

test('R2 rejects a phase outside lobby/matching/chatting', () => {
  const rows = checkStatic(sample({phase: 'talking'}));
  assert.equal(status(rows, 'R2'), 'FAIL');
});

test('R3/R4/R5 demand conversation, partner and own identity while chatting', () => {
  const rows = checkStatic(sample({conversation: '', partner: '', ownName: ''}));
  assert.equal(status(rows, 'R3'), 'FAIL');
  assert.equal(status(rows, 'R4'), 'FAIL');
  assert.equal(status(rows, 'R5'), 'FAIL');
});

test('R3/R4/R5 do not demand chat identity outside a live chat', () => {
  const rows = checkStatic(sample({phase: 'lobby', partner: '', ownName: '', conversation: '', canSend: false, buttons: []}));
  assert.equal(status(rows, 'R3'), 'PASS');
  assert.equal(status(rows, 'R4'), 'PASS');
  assert.equal(status(rows, 'R5'), 'PASS');
});

test('R6 needs id, text and a positive unique order per row', () => {
  assert.equal(status(checkStatic(sample(null, [{id: 'a', author: 'P', text: '', order: 1}])), 'R6'), 'FAIL');
  assert.equal(status(checkStatic(sample(null, [{id: 'a', author: 'P', text: 'x', order: 0}])), 'R6'), 'FAIL');
  assert.equal(
    status(
      checkStatic(sample(null, [{id: 'a', author: 'P', text: 'x', order: 1}, {id: 'a', author: 'P', text: 'y', order: 2}])),
      'R6'
    ),
    'FAIL'
  );
  assert.equal(
    status(
      checkStatic(sample(null, [{id: 'a', author: 'P', text: 'x', order: 1}, {id: 'b', author: 'P', text: 'y', order: 1}])),
      'R6'
    ),
    'FAIL'
  );
});

test('R7 allows unresolved authors only before the first attributed row', () => {
  const leading = checkStatic(sample(null, [{id: 'a', author: '', text: 'x', order: 1}, {id: 'b', author: 'P', text: 'y', order: 2}]));
  assert.equal(status(leading, 'R7'), 'PASS', 'a leading unknown row is legacy history, not a bug');
  const orphan = checkStatic(sample(null, [{id: 'a', author: 'P', text: 'x', order: 1}, {id: 'b', author: '', text: 'y', order: 2}]));
  assert.equal(status(orphan, 'R7'), 'FAIL', 'an unattributed row after a named row means grouped-author resolution is missing');
});

test('R8 only allows canSend inside a live chat with a usable composer', () => {
  assert.equal(status(checkStatic(sample({phase: 'lobby'})), 'R8'), 'FAIL');
  assert.equal(status(checkStatic(sample({observerReady: false})), 'R8'), 'FAIL');
  assert.equal(status(checkStatic(sample({hasInput: false, canSend: false})), 'R8'), 'SKIP');
  assert.equal(status(checkStatic(sample({canSend: false})), 'R8'), 'PASS');
});

test('R9 rejects blank and duplicated control labels', () => {
  assert.equal(status(checkStatic(sample({buttons: ['SKIP', 'SKIP']})), 'R9'), 'FAIL');
  assert.equal(status(checkStatic(sample({buttons: ['  ']})), 'R9'), 'FAIL');
  assert.equal(status(checkStatic(sample({buttons: ['SKIP\nSTOP']})), 'R9'), 'FAIL');
});

test('R10 fails on a loose click and passes on exact-label discipline', () => {
  const loose = checkInteraction({
    labels: ['SKIP'],
    clicks: [
      {kind: 'exact', label: 'SKIP', result: 'SKIP'},
      {kind: 'wrong', label: 'SKI', result: 'SKIP'}
    ]
  });
  assert.equal(status(loose, 'R10'), 'FAIL');

  const exact = checkInteraction({
    labels: ['NEXT'],
    clicks: [
      {kind: 'exact', label: 'NEXT', result: 'NEXT'},
      {kind: 'wrong', label: 'NEX', result: ''},
      {kind: 'wrong', label: '__NO_SUCH_CONTROL__', result: ''}
    ]
  });
  assert.equal(status(exact, 'R10'), 'PASS');

  assert.equal(status(checkInteraction({labels: [], clicks: []}), 'R10'), 'SKIP');
});

test('R11 fails when the probe leaks page text or says nothing', () => {
  const leak = sample();
  leak.probeText = 'phase=chatting last=hey there my friend';
  assert.equal(status(checkStatic(leak), 'R11'), 'FAIL');
  assert.equal(status(checkStatic({...sample(), probeText: 'chatting rows=3'}), 'R11'), 'FAIL');
});

test('R12 fails when an adapter claims a chat or clicks on a foreign page', () => {
  const claimed = checkInteraction({labels: ['SKIP'], clicks: [{kind: 'exact', label: 'SKIP', result: 'SKIP'}], foreign: {phase: 'chatting', canSend: true, buttons: ['SKIP'], clickResults: ['SKIP']}});
  assert.equal(status(claimed, 'R12'), 'FAIL');

  const silent = checkInteraction({labels: ['SKIP'], clicks: [{kind: 'exact', label: 'SKIP', result: 'SKIP'}], foreign: {phase: 'lobby', canSend: false, buttons: [], clickResults: ['', '']}});
  assert.equal(status(silent, 'R12'), 'PASS');

  assert.equal(status(checkInteraction({labels: [], clicks: []}), 'R12'), 'SKIP');
});

test('R13 fails when snapshot() invents state without a page change', () => {
  const unstable = sample();
  unstable.secondSnapshot.revision = unstable.snapshot.revision + 1;
  assert.equal(status(checkStatic(unstable), 'R13'), 'FAIL');
  assert.equal(status(checkStatic(sample()), 'R13'), 'PASS');
});

test('summarize keeps PASS, FAIL and SKIP apart', () => {
  const rows = [{status: 'PASS'}, {status: 'PASS'}, {status: 'FAIL'}, {status: 'SKIP'}];
  assert.deepEqual(summarize(rows), {pass: 2, fail: 1, skip: 1, total: 4});
});
