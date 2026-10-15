// Show Mode only. No network, storage, credentials or application internals.
// Evaluated in the visible page by domchat.go; the observer survives evaluations.
const __sm = window.__chitchatShowModeDOMv2 || (() => {
  const clean = s => (s || '').replace(/\s+/g, ' ').trim();
  const name = s => clean(s).replace(/^@/, '').toLowerCase();
  const visible = el => {
    if (!el || el.closest('[hidden], [aria-hidden="true"]')) return false;
    const st = getComputedStyle(el), r = el.getBoundingClientRect();
    return st.display !== 'none' && st.visibility !== 'hidden' && r.width > 0 && r.height > 0;
  };
  const usable = el => visible(el) && !el.disabled && el.getAttribute('aria-disabled') !== 'true';
  const label = el => clean(el.getAttribute('aria-label') ||
    [...el.querySelectorAll('[aria-hidden="false"]')].map(n => n.textContent).join(' ') || el.innerText || el.textContent);
  const input = () => [...document.querySelectorAll(
    '[data-chat-composer="true"] textarea[aria-label="Send a message"]'
  )].find(usable) || null;
  const cycle = () => [...document.querySelectorAll(
    '[data-chat-composer="true"] button[aria-keyshortcuts="Escape"]'
  )].find(visible) || null;
  const profileName = el => {
    const m = (el?.getAttribute('aria-label') || '').match(/^View @(.+) profile$/i);
    return m ? clean(m[1]) : '';
  };
  const ownName = () => {
    // Captured account control: sr-only "Open profile for " + avatar label.
    const account = [...document.querySelectorAll('button[aria-haspopup="dialog"]')]
      .find(b => [...b.querySelectorAll('.sr-only')].some(s => clean(s.textContent) === 'Open profile for'));
    return clean(account?.querySelector('[role="img"][aria-label]')?.getAttribute('aria-label'));
  };
  const state = { document: `${Date.now()}-${Math.random()}`, generation: 0,
    phase: '', partner: '', intro: null, rows: new Map(), positions: new Map(), nextPosition: 0, revision: 0,
    overflow: false, observer: null, scheduled: false, disposed: false };
  const MAX_ROWS = 2000; // fail closed rather than silently dropping unread rows
  function scan() {
    const viewport = document.querySelector('[data-message-viewport="true"]');
    const history = viewport?.querySelector('ol[aria-label="Message history"]');
    const composer = input(), action = cycle(), actionLabel = action ? label(action).toUpperCase() : '';
    const header = document.querySelector('[data-conversation-header]');
    const intro = history?.querySelector('[data-testid="match-found"]') || null;
    const partner = profileName(header?.querySelector('button[aria-label^="View @"]')) ||
      profileName(intro?.querySelector('button[aria-label^="View @"]'));
    const phase = ['SKIP', 'CONFIRM?'].includes(actionLabel) && history && partner ? 'chatting' :
      actionLabel === 'STOP' ? 'matching' : 'lobby';
    const elements = history ? [...history.querySelectorAll('li[data-message-id]')] : [];
    const ids = elements.map(e => e.getAttribute('data-message-id'));
    // A same-name rematch may replace the intro without an observable idle poll.
    // Ordinary rerender with surviving IDs must NOT greet or replay history.
    const disjointRematch = phase === 'chatting' && state.phase === phase &&
      intro && state.intro && intro !== state.intro && state.rows.size &&
      !ids.some(id => state.rows.has(id));
    if (phase !== state.phase || (partner && state.partner && name(partner) !== name(state.partner)) || disjointRematch) {
      state.generation++;
      state.rows.clear();
      state.positions.clear(); state.nextPosition = 0;
      state.overflow = false;
    }
    state.phase = phase;
    state.partner = partner;
    state.intro = intro;
    let previousAuthor = '';
    if (phase === 'chatting') for (const el of elements) {
      const id = el.getAttribute('data-message-id');
      if (!id) continue;
      // Display ordering only; reserve a position before delayed text hydration.
      if (!state.positions.has(id) && state.positions.size < MAX_ROWS) state.positions.set(id, ++state.nextPosition);
      const body = el.querySelector('p[data-from]');
      // data-from equals the MESSAGE id in the capture; never treat it as sender id.
      const text = (body?.textContent || '').replace(/\r/g, '').trim();
      const explicit = clean(el.querySelector('h3 button')?.textContent) ||
        profileName(el.querySelector('button[aria-label^="View @"]'));
      const author = explicit || state.rows.get(id)?.author || previousAuthor;
      previousAuthor = author;
      if (!text) continue; // hydrated text may arrive in a later mutation
      const old = state.rows.get(id);
      if (!old && state.rows.size >= MAX_ROWS) { state.overflow = true; break; }
      if (!old || old.text !== text || old.author !== author) {
        state.rows.set(id, { id, author, text, order: state.positions.get(id) || old?.order || ++state.nextPosition });
        state.revision++;
      }
    }
    return {
      controls: {
        hasInput: !!composer, inputDesc: composer ? 'textarea:Send a message' : '',
        buttons: action && usable(action) && action.getAttribute('aria-busy') !== 'true' ? [actionLabel] : [],
        chatEnded: phase !== 'chatting', partner, ownName: ownName(),
        conversation: `${state.document}:${state.generation}`, phase,
        canSend: phase === 'chatting' && !!composer && actionLabel === 'SKIP' &&
          usable(action) && action.getAttribute('aria-busy') !== 'true',
        observerReady: !!state.observer, overflow: state.overflow,
      },
      messages: [...state.rows.values()], revision: state.revision,
    };
  }
  function ensureObserver() {
    if (state.disposed || state.observer || !document.documentElement) return;
    state.observer = new MutationObserver(() => {
      if (state.scheduled) return;
      state.scheduled = true;
      queueMicrotask(() => { state.scheduled = false; if (!state.disposed) scan(); });
    });
    // Document root survives SPA history/composer replacement. Character data
    // catches text-node hydration; attributes catch button state transitions.
    state.observer.observe(document.documentElement, { subtree: true, childList: true,
      characterData: true, attributes: true,
      attributeFilter: ['aria-label', 'aria-busy', 'aria-disabled', 'disabled', 'data-message-id'] });
  }
  const api = {
    dispose() { state.disposed = true; state.observer?.disconnect(); state.observer = null; },
    snapshot() { ensureObserver(); return scan(); }, // periodic rescan is the watchdog
    input,
    click(labels) {
      const el = cycle();
      if (!el || !usable(el) || el.getAttribute('aria-busy') === 'true') return '';
      const active = label(el);
      if (!labels.some(w => clean(w).toUpperCase() === active.toUpperCase())) return '';
      el.click();
      return active;
    },
    // Metadata only: no URL query, message text, cookies, tokens or usernames.
    probe() {
      const s = api.snapshot();
      return `phase=${s.controls.phase} viewport=${!!document.querySelector('[data-message-viewport="true"]')} ` +
        `input=${s.controls.hasInput} action=${s.controls.buttons.join(',')} rows=${s.messages.length} ` +
        `unknownAuthors=${s.messages.filter(m => !m.author).length} observer=${s.controls.observerReady} overflow=${s.controls.overflow}`;
    },
  };
  window.__chitchatShowModeDOMv2 = api;
  return api;
})();
