// DELIBERATELY BROKEN adapter, used only as a negative test fixture.
//
// It repeats every classic mistake that makes an adapter look like it works and
// then misfires on a live site:
//   1. the message id column is read as the sender (data-from = row id),
//   2. partner/own identity are never reported,
//   3. click() accepts substring labels, so a "SKIP" request can press a
//      different control ("STOP"),
//   4. canSend is hard-coded true, even outside a chat,
//   5. probe() echoes page content.
// tests/checker.test.cjs asserts that the checker catches all of it.

const __sm = window.__brokenAdapter || (() => {
  const state = {order: 0, rows: new Map(), revision: 0};
  function snapshot() {
    for (const el of document.querySelectorAll('li[data-message-id], li[data-msg-id]')) {
      const id = el.getAttribute('data-message-id') || el.getAttribute('data-msg-id');
      const body = el.querySelector('p[data-from]') || el.querySelector('p');
      const text = (body?.textContent || '').trim();
      if (!id || !text) continue;
      if (!state.rows.has(id)) state.rows.set(id, {id, author: '', text, order: ++state.order});
      state.rows.get(id).author = body?.getAttribute('data-from') || ''; // mistake 1
      state.revision++;
    }
    const messages = [...state.rows.values()];
    const button = document.querySelector('[data-chat-composer] button[aria-keyshortcuts="Escape"]');
    const label = (button?.getAttribute('aria-label') || '').toUpperCase();
    return {
      controls: {
        hasInput: !!document.querySelector('textarea'), buttons: label ? [label] : [], chatEnded: false,
        partner: '', ownName: '', conversation: 'broken:0', phase: 'chatting', // mistake 2
        canSend: true, observerReady: false, overflow: false,                        // mistake 4
      },
      messages, revision: state.revision,
    };
  }
  return {
    dispose() {},
    snapshot,
    input: () => document.querySelector('textarea'),
    click(labels) {
      const button = document.querySelector('[data-chat-composer] button[aria-keyshortcuts="Escape"]');
      if (!button) return '';
      const active = (button.getAttribute('aria-label') || '').toUpperCase();
      if (!labels.some(w => active.startsWith(String(w).toUpperCase()))) return ''; // mistake 3
      button.click();
      return active;
    },
    probe() {
      const messages = [...state.rows.values()];
      return 'phase=chatting last=' + (messages[messages.length - 1]?.text || ''); // mistake 5
    },
  };
})();
