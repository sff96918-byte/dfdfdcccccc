'use strict';
// jsdom harness: run a site observer against a saved/synthetic page HTML and
// collect exactly what the contract checker needs.
//
// Same policy as root-project/tests/showmode-dom.test.cjs: jsdom has no layout,
// so geometry is emulated (getBoundingClientRect only). Selectors, computed
// style, MutationObserver and parsing are jsdom's real implementations - no
// site-specific shortcut is added here.

const {JSDOM} = require('jsdom');

const RECT = {width: 100, height: 20, top: 0, left: 0, right: 100, bottom: 20, x: 0, y: 0};

function loadPage(html, url) {
  const dom = new JSDOM(html, {runScripts: 'outside-only', url: url || 'https://example.invalid/'});
  dom.window.HTMLElement.prototype.getBoundingClientRect = () => RECT;
  return dom;
}

function loadObserver(dom, source) {
  // Convention for a site observer:
  //   const __sm = window.__siteDOMv1 || (() => { ... })();   // returns the api
  //   window.__siteDOMv1 = __sm;                              // optional but useful
  // The ChitChat helper uses exactly the same shape (with its own global name),
  // so one loader covers both.
  let api = null;
  try {
    api = dom.window.eval(source + '\n;(typeof __sm !== "undefined" ? __sm : null);');
  } catch (error) {
    throw new Error('observer script threw while loading: ' + error.message);
  }
  if (!api || typeof api.snapshot !== 'function') api = dom.window.__siteDOMv1 || dom.window.__chitchatShowModeDOMv2 || null;
  if (!api || typeof api.snapshot !== 'function') {
    throw new Error('observer did not expose snapshot() (declare `const __sm = {...}` or set window.__siteDOMv1)');
  }
  return api;
}

function json(value) {
  return JSON.parse(JSON.stringify(value));
}

function snapshotOf(api) {
  try {
    return json(api.snapshot());
  } catch (error) {
    throw new Error('snapshot() threw: ' + error.message);
  }
}

function probeOf(api) {
  try {
    const value = typeof api.probe === 'function' ? api.probe() : '';
    return typeof value === 'string' ? value : json(value);
  } catch (error) {
    return '';
  }
}

// Click each active label exactly, then with a near miss, then with a label
// that does not exist. Returns what click() answered.
function clickObservations(api, labels) {
  const clicks = [];
  if (typeof api.click !== 'function') return clicks;
  for (const label of labels) {
    const value = String(label);
    const exact = api.click([value]);
    clicks.push({kind: 'exact', label: value, result: typeof exact === 'string' ? exact : ''});
    const nearMiss = value.length > 2 ? value.slice(0, value.length - 1) : value + 'x';
    const wrong = api.click([nearMiss]);
    clicks.push({kind: 'wrong', label: nearMiss, result: typeof wrong === 'string' ? wrong : ''});
  }
  const unknown = api.click(['__NO_SUCH_CONTROL__']);
  clicks.push({kind: 'wrong', label: '__NO_SUCH_CONTROL__', result: typeof unknown === 'string' ? unknown : ''});
  return clicks;
}

function evaluatePage({observerSource, html, url, foreignHtml, foreignUrl}) {
  const dom = loadPage(html, url);
  const api = loadObserver(dom, observerSource);
  const first = snapshotOf(api);
  const probeText = probeOf(api);
  const clicks = clickObservations(api, first.controls && Array.isArray(first.controls.buttons) ? first.controls.buttons : []);
  const second = snapshotOf(api);

  let foreign = null;
  if (foreignHtml) {
    const foreignDom = loadPage(foreignHtml, foreignUrl || 'https://not-the-target.invalid/');
    const foreignApi = loadObserver(foreignDom, observerSource);
    const snapshot = snapshotOf(foreignApi);
    const labels = snapshot.controls && Array.isArray(snapshot.controls.buttons) ? snapshot.controls.buttons : [];
    const results = [];
    for (const label of labels) {
      const answer = typeof foreignApi.click === 'function' ? foreignApi.click([String(label)]) : '';
      results.push(typeof answer === 'string' ? answer : '');
    }
    const known = ['SKIP', 'CONFIRM?', 'NEXT', 'NEXT STRANGER', 'START', 'STOP'];
    for (const label of known) {
      const answer = typeof foreignApi.click === 'function' ? foreignApi.click([label]) : '';
      results.push(typeof answer === 'string' ? answer : '');
    }
    foreign = {phase: snapshot.controls && snapshot.controls.phase, canSend: !!(snapshot.controls && snapshot.controls.canSend), buttons: labels, clickResults: results};
    try {
      if (typeof foreignApi.dispose === 'function') foreignApi.dispose();
    } catch (error) {
      /* dispose is best effort in a throwaway window */
    }
    foreignDom.window.close();
  }

  try {
    if (typeof api.dispose === 'function') api.dispose();
  } catch (error) {
    /* dispose is best effort in a throwaway window */
  }
  dom.window.close();

  return {first, second, probeText, clicks, foreign};
}

module.exports = {loadPage, loadObserver, snapshotOf, probeOf, clickObservations, evaluatePage};
