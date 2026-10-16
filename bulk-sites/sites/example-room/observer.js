// Example site adapter: a synthetic room-style chat page.
//
// COPY THIS FILE for a new DOM site and change ONLY the selectors/phase logic
// marked below. The emitted JSON shape must stay identical - that is the whole
// contract (see ../README.md and kit/contract.cjs, rules R1-R13).
//
// Rules that a real site adapter has to respect:
//   - never guess an author: inherit it from the previous row when the site
//     hides it on grouped rows (R7),
//   - reserve a stable, positive display order per row id (R6),
//   - report our own display name once the page knows it (R5),
//   - canSend is true ONLY while a live chat exists and the composer is usable (R8),
//   - click() accepts an exact label match and nothing else (R10),
//   - probe() is metadata only: no message text, no usernames, no tokens (R11),
//   - on an unknown page every field stays empty/false: fail closed (R12).

const __sm = window.__siteDOMv1 || (() => {
  const clean = s => (s || '').replace(/\s+/g, ' ').trim();
  const visible = el => {
    if (!el || el.closest('[hidden], [aria-hidden="true"]')) return false;
    const st = getComputedStyle(el), r = el.getBoundingClientRect();
    return st.display !== 'none' && st.visibility !== 'hidden' && r.width > 0 && r.height > 0;
  };
  const usable = el => visible(el) && !el.disabled && el.getAttribute('aria-disabled') !== 'true';

  // ---- selectors: the only site-specific part ---------------------------------
  const logEl = () => document.querySelector('#log');                       // message list container
  const titleEl = () => document.querySelector('#room-title');              // room/chat identity
  const input = () => { const el = document.querySelector('#say'); return usable(el) ? el : null; };
  const nextEl = () => { const el = document.querySelector('#next'); return usable(el) ? el : null; };
  const ownName = () => clean(document.querySelector('[data-me]')?.textContent);
  const rowEls = () => (logEl() ? [...logEl().querySelectorAll('li[data-msg-id]')] : []);
  const explicitAuthor = el => clean(el.querySelector('.who')?.textContent);
  const rowText = el => (el.querySelector('p.body')?.textContent || '').replace(/\r/g, '').trim();
  const NEEDS_RESPONSE_ON = ['NEXT'];   // exact label(s) of the "next partner" control
  // ----------------------------------------------------------------------------

  const state = {document: `${Date.now()}-${Math.random()}`, generation: 0, phase: '', room: '',
    rows: new Map(), positions: new Map(), nextPosition: 0, revision: 0, overflow: false, observer: null};
  const MAX_ROWS = 2000; // fail closed rather than silently dropping unread rows

  function scan() {
    const title = clean(titleEl()?.textContent) || state.room || 'room';
    // A new room (or a replaced log) starts a new conversation key: replies must
    // never be attributed to the previous conversation.
    if (state.room && title !== state.room) { state.generation++; state.rows.clear(); state.positions.clear(); state.nextPosition = 0; state.overflow = false; }
    state.room = title;

    const rows = rowEls();
    const phase = !logEl() ? 'lobby' : rows.length ? 'chatting' : 'matching';
    if (phase === 'lobby' && state.phase === 'chatting') { state.generation++; state.rows.clear(); state.positions.clear(); state.nextPosition = 0; }

    const mine = ownName();
    let previousAuthor = '';
    for (const el of rows) {
      const id = el.getAttribute('data-msg-id');
      if (!id) continue;
      if (!state.positions.has(id) && state.positions.size < MAX_ROWS) state.positions.set(id, ++state.nextPosition);
      const text = rowText(el);
      const author = explicitAuthor(el) || previousAuthor;
      previousAuthor = author;
      if (!text) continue; // text may hydrate in a later mutation
      const old = state.rows.get(id);
      if (!old && state.rows.size >= MAX_ROWS) { state.overflow = true; break; }
      if (!old || old.text !== text || old.author !== author) {
        state.rows.set(id, {id, author, text, order: state.positions.get(id) || old?.order || ++state.nextPosition});
        state.revision++;
      }
    }
    const messages = [...state.rows.values()];
    const partner = [...messages].reverse().find(m => m.author && m.author.toLowerCase() !== mine.toLowerCase());
    const composer = input(), next = nextEl(), label = next ? clean(next.textContent).toUpperCase() : '';
    const canSend = phase === 'chatting' && !!composer && !!next && !!partner && label === NEEDS_RESPONSE_ON[0];
    state.phase = phase;
    return {
      controls: {
        hasInput: !!composer, inputDesc: composer ? '#say' : '', buttons: next && label ? [label] : [],
        chatEnded: phase !== 'chatting', partner: partner?.author || '', ownName: mine,
        conversation: `${state.document}:${state.generation}:${state.room}`, phase, canSend,
        observerReady: !!state.observer, overflow: state.overflow,
      },
      messages, revision: state.revision,
    };
  }

  function ensureObserver() {
    if (state.observer || !document.documentElement) return;
    state.observer = new MutationObserver(() => scan());
    state.observer.observe(document.documentElement, {subtree: true, childList: true, characterData: true,
      attributes: true, attributeFilter: ['hidden', 'aria-disabled', 'disabled', 'data-msg-id']});
  }

  const api = {
    dispose() { state.observer?.disconnect(); state.observer = null; },
    snapshot() { ensureObserver(); return scan(); }, // a periodic rescan is the watchdog
    input,
    click(labels) {
      const el = nextEl();
      if (!el) return '';
      const active = clean(el.textContent).toUpperCase();
      if (!labels.some(w => clean(w).toUpperCase() === active)) return ''; // exact match only
      el.click();
      return active;
    },
    // Metadata only: no message text, no usernames, no URLs, no tokens.
    probe() {
      const s = api.snapshot();
      return `phase=${s.controls.phase} input=${s.controls.hasInput} ` +
        `action=${s.controls.buttons.join(',')} rows=${s.messages.length} ` +
        `unknownAuthors=${s.messages.filter(m => !m.author).length} observer=${s.controls.observerReady} overflow=${s.controls.overflow}`;
    },
  };
  window.__siteDOMv1 = api;
  return api;
})();
