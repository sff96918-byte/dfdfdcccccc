'use strict';
// The site-adapter contract, encoded as pure rules.
//
// A "site adapter" is one small in-page observer script per site. It reads the
// page's own DOM and reports the SAME neutral JSON shape the ChitChat Show Mode
// helper already emits, so the runner, the reply engine and the dashboard never
// need to know which site they are on.
//
// Every rule here is a property the runner genuinely depends on. A rule that
// cannot be checked is not invented: it is listed as SKIP with the reason.
// SKIP is not PASS.
//
// Statuses: PASS, FAIL, SKIP. No "probably".

const PHASES = ['lobby', 'matching', 'chatting'];

function row(id, status, detail) {
  return {id, status, detail: detail === undefined ? '' : String(detail)};
}

function isObject(value) {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function short(value) {
  const text = typeof value === 'string' ? value : JSON.stringify(value);
  if (text === undefined) return 'undefined';
  return text.length > 60 ? text.slice(0, 57) + '...' : text;
}

function isInt(value) {
  return typeof value === 'number' && Number.isInteger(value);
}

// ---------------------------------------------------------------- static rules
// sample = {snapshot, secondSnapshot, probeText}
function checkStatic(sample) {
  const rows = [];
  const snapshot = sample.snapshot;

  const shapeOk =
    isObject(snapshot) && isObject(snapshot.controls) && Array.isArray(snapshot.messages) && isInt(snapshot.revision);
  rows.push(
    row(
      'R1 snapshot-shape',
      shapeOk ? 'PASS' : 'FAIL',
      shapeOk
        ? 'controls object + messages array + integer revision'
        : 'expected {controls:{}, messages:[], revision:int}, got ' + short(snapshot)
    )
  );
  if (!shapeOk) return rows;

  const controls = snapshot.controls;
  const messages = snapshot.messages;

  const phaseOk = PHASES.indexOf(controls.phase) !== -1;
  rows.push(
    row(
      'R2 phase-vocabulary',
      phaseOk ? 'PASS' : 'FAIL',
      'phase=' + short(controls.phase) + ' (allowed: ' + PHASES.join(', ') + ')'
    )
  );

  const chatting = controls.phase === 'chatting';

  const conversationOk = typeof controls.conversation === 'string' && (!chatting || controls.conversation.length > 0);
  rows.push(
    row(
      'R3 conversation-key',
      conversationOk ? 'PASS' : 'FAIL',
      conversationOk
        ? 'conversation=' + short(controls.conversation)
        : 'chatting must carry a non-empty conversation key (a new chat must produce a different one)'
    )
  );

  const partnerOk = !chatting || String(controls.partner || '').length > 0;
  rows.push(
    row(
      'R4 partner-identity',
      partnerOk ? 'PASS' : 'FAIL',
      partnerOk ? 'partner=' + short(controls.partner) : 'chatting without a partner name: greetings/skips cannot be attributed'
    )
  );

  const ownOk = !chatting || String(controls.ownName || '').length > 0;
  rows.push(
    row(
      'R5 own-identity',
      ownOk ? 'PASS' : 'FAIL',
      ownOk
        ? 'ownName=' + short(controls.ownName)
        : 'chatting without our own display name: echo suppression and confirmed-send are impossible'
    )
  );

  // R6: rows must be addressable and readable.
  const badRow = messages.find(
    (m) => !isObject(m) || !String(m.id || '').length || !isInt(m.order) || m.order <= 0 || !String(m.text || '').length
  );
  const ids = messages.map((m) => String(m.id || ''));
  const orders = messages.map((m) => m && m.order);
  const duplicate = ids.find((id, index) => ids.indexOf(id) !== index);
  const duplicateOrder = orders.find((order, index) => orders.indexOf(order) !== index);
  let rowDetail = messages.length + ' row(s)';
  let rowStatus = 'PASS';
  if (badRow) {
    rowStatus = 'FAIL';
    rowDetail = 'every row needs id, text and a positive integer order; offender: ' + short(badRow);
  } else if (duplicate !== undefined) {
    rowStatus = 'FAIL';
    rowDetail = 'duplicate message id: ' + short(duplicate);
  } else if (duplicateOrder !== undefined) {
    rowStatus = 'FAIL';
    rowDetail = 'duplicate display order: ' + short(duplicateOrder);
  }
  rows.push(row('R6 row-identity', rowStatus, rowDetail));

  // R7: an unattributed row is only tolerable before the first attributed row
  // (a grouped/continuation row must inherit its author, never be guessed later).
  let seenAuthor = false;
  let orphan = '';
  for (const message of messages) {
    const author = String((message && message.author) || '');
    if (author.length) seenAuthor = true;
    else if (seenAuthor) {
      orphan = String((message && message.id) || '?');
      break;
    }
  }
  rows.push(
    row(
      'R7 row-authors',
      orphan ? 'FAIL' : 'PASS',
      orphan
        ? 'row ' + orphan + ' has no author after an attributed row (grouped-author resolution missing)'
        : 'no unresolved author after the first attributed row'
    )
  );

  // R8: canSend is the only gate the runner trusts.
  let sendStatus = 'PASS';
  let sendDetail = 'canSend=' + short(controls.canSend);
  if (controls.canSend && !(chatting && controls.hasInput && controls.observerReady)) {
    sendStatus = 'FAIL';
    sendDetail = 'canSend=true requires phase=chatting + hasInput + observerReady (got phase=' + short(controls.phase) + ')';
  } else if (!chatting && controls.canSend) {
    sendStatus = 'FAIL';
    sendDetail = 'canSend must be false outside a live chat';
  } else if (chatting && !controls.hasInput) {
    sendStatus = 'SKIP';
    sendDetail = 'chatting, but the page exposes no composer right now';
  }
  rows.push(row('R8 send-gate', sendStatus, sendDetail));

  // R9: control labels must be usable as exact click targets.
  const buttons = Array.isArray(controls.buttons) ? controls.buttons : [];
  const badLabel = buttons.find((b) => typeof b !== 'string' || !b.trim().length || /[\r\n]/.test(b));
  const duplicateLabel = buttons.find((label, index) => buttons.indexOf(label) !== index);
  let labelStatus = 'PASS';
  let labelDetail = buttons.length ? 'labels: ' + buttons.map(short).join(', ') : 'no control exposed on this page state';
  if (!Array.isArray(controls.buttons)) {
    labelStatus = 'FAIL';
    labelDetail = 'buttons must be an array';
  } else if (badLabel !== undefined) {
    labelStatus = 'FAIL';
    labelDetail = 'control label is not a clean single-line string: ' + short(badLabel);
  } else if (duplicateLabel !== undefined) {
    labelStatus = 'FAIL';
    labelDetail = 'duplicate control label: ' + short(duplicateLabel);
  }
  rows.push(row('R9 control-labels', labelStatus, labelDetail));

  // R11: the probe is diagnostic and must not leak page content.
  const leakCandidates = messages
    .map((m) => String((m && m.text) || ''))
    .filter((text) => text.length >= 8)
    .map((text) => text.slice(0, 24));
  const probe = String(sample.probeText || '');
  let probeStatus = 'PASS';
  let probeDetail = 'metadata-only probe';
  if (!probe.length || probe.indexOf('phase=') === -1) {
    probeStatus = 'FAIL';
    probeDetail = 'probe() must return a non-empty line containing phase=';
  } else {
    const leaked = leakCandidates.find((text) => probe.indexOf(text) !== -1);
    if (leaked) {
      probeStatus = 'FAIL';
      probeDetail = 'probe() leaks message text: ' + short(leaked);
    } else if (!leakCandidates.length) {
      probeDetail = 'no long message text on this page to check against';
    }
  }
  rows.push(row('R11 probe-privacy', probeStatus, probeDetail));

  // R13: calling snapshot() twice with no page change must not invent state.
  if (!sample.secondSnapshot) {
    rows.push(row('R13 snapshot-idempotent', 'SKIP', 'no second snapshot provided'));
  } else {
    const a = sample.snapshot;
    const b = sample.secondSnapshot;
    const same =
      JSON.stringify(a.messages) === JSON.stringify(b.messages) &&
      a.revision === b.revision &&
      a.controls.conversation === b.controls.conversation;
    rows.push(
      row(
        'R13 snapshot-idempotent',
        same ? 'PASS' : 'FAIL',
        same ? 'messages + revision + conversation unchanged' : 'snapshot() changed without a page change (phantom rows/replies)'
      )
    );
  }

  return rows;
}

// ----------------------------------------------------------- interaction rules
// outcome = {labels, clicks: [{label, result}], foreign: {canSend, phase, buttons, clickResults}}
function checkInteraction(outcome) {
  const rows = [];
  const clicks = outcome.clicks || [];
  const exact = clicks.filter((c) => c.kind === 'exact');
  const wrong = clicks.filter((c) => c.kind === 'wrong');

  if (!exact.length) {
    rows.push(
      row(
        'R10 click-discipline',
        'SKIP',
        outcome.labels && outcome.labels.length ? 'no exact-label click was performed' : 'page exposes no control to click'
      )
    );
  } else {
    const broken = exact.find((c) => c.result !== c.label);
    const notIdempotent = wrong.find((c) => c.result !== '');
    let status = 'PASS';
    let detail = 'as observed= ' + exact.length + ' exact click(s), ' + wrong.length + ' rejection(s)';
    if (broken) {
      status = 'FAIL';
      detail = 'click([' + short(broken.label) + ']) returned ' + short(broken.result) + ' instead of the label';
    } else if (notIdempotent) {
      status = 'FAIL';
      detail = 'near-miss label ' + short(notIdempotent.label) + ' was accepted (substring/loose matching clicks the site)';
    }
    rows.push(row('R10 click-discipline', status, detail));
  }

  const foreign = outcome.foreign;
  if (!foreign) {
    rows.push(row('R12 fail-closed-foreign', 'SKIP', 'no foreign page provided (--foreign <html>)'));
  } else {
    const claimsChat = foreign.phase === 'chatting' || foreign.canSend === true;
    const clicked = (foreign.clickResults || []).filter((r) => r !== '');
    rows.push(
      row(
        'R12 fail-closed-foreign',
        !claimsChat && !clicked.length ? 'PASS' : 'FAIL',
        !claimsChat && !clicked.length
          ? 'on an unknown page the adapter stays silent (phase=' + short(foreign.phase) + ', canSend=' + short(foreign.canSend) + ')'
          : 'adapter claims a live chat or clicks on a page that is not the target site (phase=' +
            short(foreign.phase) +
            ', canSend=' +
            short(foreign.canSend) +
            ', clicked=' +
            short(clicked.join(',')) +
            ')'
      )
    );
  }

  return rows;
}

function summarize(rows) {
  const count = (status) => rows.filter((r) => r.status === status).length;
  return {pass: count('PASS'), fail: count('FAIL'), skip: count('SKIP'), total: rows.length};
}

module.exports = {PHASES, checkStatic, checkInteraction, summarize, row};
