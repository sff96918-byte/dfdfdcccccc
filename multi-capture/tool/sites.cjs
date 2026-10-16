'use strict';
// Pure multi-site helpers: domain attribution, site folder keys, the per-site
// "how do messages arrive / how do we send" SMS summary and the two Markdown
// reports. No browser, no network, no fs — everything here is unit-testable.
//
// The whole point of this tool: ONE browser run, MANY sites, and every capture
// file lands under the folder of the site (domain) the operator was actually
// chatting on — never mixed together.

const POLICY = {
  // A host belongs to a site when it is that domain or a subdomain of it
  // (app.chitchat.gg / api.chitchat.gg / cdn.chitchat.gg -> chitchat.gg).
  siteSuffix: true,
};

function hostOf(url) {
  try {
    const parsed = new URL(String(url || ''));
    if (!['http:', 'https:', 'ws:', 'wss:'].includes(parsed.protocol)) return '';
    return String(parsed.hostname || '').toLowerCase();
  } catch {
    return '';
  }
}

// Domain = the hostname of the PAGE the operator is on, with a leading "www."
// removed, so www.site.com and site.com share one capture folder.
// A registrable-domain (eTLD+1) rule is deliberately NOT used: without a full
// public-suffix list it would merge unrelated sites (a.co.uk + b.co.uk ->
// co.uk). app.site.com is therefore its own folder, and the sibling/third-party
// hosts it uses are listed inside that folder (manifest hosts + SUMMARY.md).
function domainOf(url) {
  const host = hostOf(url);
  if (!host) return '';
  return host.replace(/^www\./, '');
}

// Folder-safe, stable, lowercase key for a domain. Used as the folder name and
// as the site identity everywhere in this tool.
function siteKey(value) {
  const text = String(value || '')
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '_')
    .replace(/^[._-]+|[._-]+$/g, '')
    .slice(0, 60);
  return text || 'unknown';
}

function isIp(host) {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || host.includes(':');
}

// Common two-label public suffixes we can name explicitly. This is only used to
// LABEL hosts in a report (site vs third-party); it never decides which folder
// an event goes to, so a heuristic here cannot mix two sites together.
const TWO_LABEL_SUFFIXES = new Set([
  'co.uk', 'org.uk', 'ac.uk', 'gov.uk', 'me.uk', 'net.uk',
  'com.au', 'net.au', 'org.au', 'co.nz', 'co.jp', 'ne.jp', 'or.jp',
  'co.kr', 'com.br', 'com.mx', 'com.ar', 'co.in', 'net.in',
  'com.tr', 'co.za', 'co.id', 'com.cn', 'com.tw', 'com.hk', 'co.il', 'com.sg',
]);

function siteBase(key) {
  const labels = String(key || '').split('.');
  if (labels.length >= 3 && TWO_LABEL_SUFFIXES.has(labels.slice(-2).join('.'))) return labels.slice(-3).join('.');
  if (labels.length >= 2) return labels.slice(-2).join('.');
  return String(key || '');
}

function isSameSite(host, domain) {
  const h = String(host || '').toLowerCase();
  const d = String(domain || '').toLowerCase();
  if (!h || !d) return false;
  if (h === d) return true;
  return POLICY.siteSuffix && h.endsWith('.' + d);
}

// "site" = the captured page itself, its subdomains, or a sibling host inside
// the same site base (app.site-a.com <-> api.site-a.com). Everything else is
// third-party (CDNs, video/stream hosts, analytics).
function hostRole(host, domain) {
  const h = String(host || '').toLowerCase();
  const d = String(domain || '').toLowerCase();
  if (!h || !d) return 'third-party';
  if (h === d) return 'site';
  if (isIp(d)) return 'third-party';
  const base = siteBase(d);
  if (h === base || h.endsWith('.' + base)) return 'site';
  return 'third-party';
}

// Collapse ids so repeated calls to the same endpoint group together.
function endpointPattern(url) {
  try {
    const parsed = new URL(String(url || ''));
    const path = (parsed.pathname || '/')
      .replace(/\/[0-9a-f]{8,}(?=\/|$)/gi, '/:id')
      .replace(/\/\d+(?=\/|$)/g, '/:id')
      .replace(/\/[A-Za-z0-9_-]{16,}(?=\/|$)/g, '/:id');
    return `${parsed.host}${path}`;
  } catch {
    return String(url || '');
  }
}

function markerConfidence(marker) {
  const candidateText = marker && marker.candidate && marker.candidate.messageText;
  const domText = marker && marker.domMessage && marker.domMessage.text;
  if (!candidateText || !domText) return 'time-window only';
  const a = String(domText);
  const b = String(candidateText);
  return a.includes(b) || b.includes(a) ? 'high (text match)' : 'time-window only';
}

function uniq(list) {
  return [...new Set(list)];
}

// Per-site "root/mut" answer: which network event carries an incoming user
// message, and which call sends our own message. Built ONLY from evidence:
// labeled markers are ground truth, candidates are hypotheses and are labelled
// as such.
function smsSummary({site, domain, markers, candidates, generatedAt}) {
  const rows = Array.isArray(markers) ? markers : [];
  const all = Array.isArray(candidates) ? candidates : [];
  const incomingCandidates = all.filter((c) => String(c.direction || '').startsWith('incoming'));
  const outgoingCandidates = all.filter((c) => String(c.direction || '').startsWith('outgoing'));

  const side = (label, pool) => {
    const labeled = rows.filter((m) => m.label === label);
    const confirmed = labeled
      .filter((m) => m.matched && m.candidate)
      .map((m) => ({
        labeledAt: m.at || m.labeledAt || '',
        source: m.candidate.source,
        eventName: m.candidate.eventName || null,
        method: m.candidate.method || null,
        url: m.candidate.url || '',
        endpoint: m.candidate.eventName ? null : endpointPattern(m.candidate.url),
        messageText: m.candidate.messageText || null,
        author: m.candidate.author || null,
        match: markerConfidence(m),
      }));
    const keyed = new Map();
    for (const example of confirmed) {
      const key = example.eventName || example.url;
      if (!keyed.has(key)) keyed.set(key, example);
    }
    const wsEvents = uniq(pool.filter((c) => c.source === 'websocket' && c.eventName).map((c) => c.eventName));
    const endpoints = uniq(pool.filter((c) => c.source === 'http-request' || c.source === 'http-response').map((c) => endpointPattern(c.url)));
    return {
      markers: labeled.length,
      confirmed: keyed.size,
      confirmed_examples: [...keyed.values()],
      observed_only: {
        websocket_events: wsEvents,
        http_endpoints: endpoints,
        candidates: pool.length,
      },
    };
  };

  const incoming = side('incoming', incomingCandidates);
  const outgoing = side('outgoing', outgoingCandidates);

  const describeExample = (e) =>
    e.eventName
      ? `WS event "${e.eventName}" (${e.match})`
      : `HTTP ${e.method ? e.method + ' ' : ''}${e.endpoint || endpointPattern(e.url)} (${e.match})`;

  const howToReceive = incoming.confirmed
    ? `Confirmed by your marker: ${incoming.confirmed_examples.map(describeExample).join('; ')}`
    : incoming.observed_only.websocket_events.length || incoming.observed_only.http_endpoints.length
      ? 'NOT CONFIRMED — candidates were seen, but no Ctrl+Alt+I marker matched them. Mark one real incoming message next time.'
      : 'NOT CONFIRMED — nothing message-like was received yet on this site.';

  const howToSend = outgoing.confirmed
    ? `Confirmed by your marker: ${outgoing.confirmed_examples.map(describeExample).join('; ')}`
    : outgoing.observed_only.websocket_events.length || outgoing.observed_only.http_endpoints.length
      ? 'NOT CONFIRMED — candidates were seen, but no Ctrl+Alt+O marker matched them. Mark one message you send next time.'
      : 'NOT CONFIRMED — no outgoing message traffic captured yet on this site.';

  return {
    site: siteKey(domain || site),
    domain: domain || '',
    generated_at: generatedAt || new Date().toISOString(),
    ground_truth_note:
      'labeled-*.jsonl entries are what you confirmed with Ctrl+Alt+I / Ctrl+Alt+O in the page. Candidates are automatic guesses; treat them as evidence, not proof.',
    incoming: {...incoming, how_to_detect: howToReceive},
    outgoing: {...outgoing, how_to_send: howToSend},
  };
}

function renderSiteSummary({site, domain, storeDir, counts, capLines, hosts, visits, reason, startedAt, endedAt, redactEnabled}) {
  const c = counts || {};
  const hostList = uniq(Array.isArray(hosts) ? hosts.map((h) => hostRole(h, domain) + ': ' + h) : []);
  return [
    `# Capture summary — ${site}`,
    '',
    `- Site/domain: ${domain || '(unknown)'}`,
    `- Folder: ${storeDir || ''}`,
    `- Started: ${startedAt || ''}`,
    `- Ended: ${endedAt || ''}`,
    `- Stop reason: ${reason || ''}`,
    `- Redaction: ${redactEnabled === false ? 'OFF (rawMode) — do NOT share this folder' : 'ON (tokens/cookies/passwords hidden)'}`,
    `- Tab visits recorded: ${visits || 0}`,
    '',
    '## Capability checklist (this site only)',
    '',
    ...(Array.isArray(capLines) ? capLines : []),
    '',
    '## Numbers (this site only)',
    '',
    `- HTTP request/response: ${c.httpRequests || 0}/${c.httpResponses || 0}`,
    `- WebSocket: ${c.wsConnections || 0} socket, sent ${c.wsFramesSent || 0}, received ${c.wsFramesReceived || 0} frames`,
    `- DOM messages: ${c.domMessages || 0}, snapshots: ${c.snapshots || 0}`,
    `- Markers: incoming ${c.incomingMarkers || 0}, outgoing ${c.outgoingMarkers || 0}`,
    `- Candidates: incoming ${c.incomingCandidates || 0}, outgoing ${c.outgoingCandidates || 0}`,
    '',
    '## Hosts seen on this site',
    '',
    ...(hostList.length ? hostList.map((h) => '- ' + h) : ['- (none yet)']),
    '',
    '## Bot banate ja lagbe (ei site er jonno)',
    '',
    '- Incoming detect: `messages/sms-summary.json` -> `incoming.how_to_detect`',
    '- Outgoing send: `messages/sms-summary.json` -> `outgoing.how_to_send`',
    '- DOM socket/API schema: `websockets/catalog.json`, `api-catalog.json`',
    '- DOM evidence for the in-page observer: `dom/*.html`, `messages/dom-messages.jsonl`',
    '',
  ].join('\n');
}

function renderRunSummary(index) {
  const sites = (index && index.sites) || [];
  const lines = [
    '# Multi-site capture run — kon site er capture konta',
    '',
    `- Tool: ${index.tool} v${index.version}`,
    `- Started: ${index.startedAt}`,
    `- Ended: ${index.endedAt || '(running)'}`,
    `- Stop reason: ${index.reason || ''}`,
    `- Browser: ${index.browser || ''}`,
    `- Profile used: ${index.profileDir || ''}`,
    `- Redaction: ${index.redactEnabled === false ? 'OFF (rawMode)' : 'ON'}`,
    `- Unattributed events (before any site was known): ${index.unattributed || 0}`,
    '',
    '## Sites captured in this run',
    '',
    '| Site (domain) | Folder | Tabs/visits | HTTP | WS frames (sent/recv) | DOM msgs | Markers I/O | Incoming | Outgoing |',
    '|---|---|---|---:|---|---:|---:|---|---|',
  ];
  for (const site of sites) {
    const c = site.counts || {};
    const caps = site.capabilities || [];
    const has = (key) => (caps.find((x) => x.key === key && x.achieved) ? 'YES' : 'no');
    lines.push(
      `| ${site.domain || site.key} | \`sites/${site.key}/\` | ${site.visits || 0} | ${c.httpRequests || 0}/${c.httpResponses || 0} | ${c.wsFramesSent || 0}/${c.wsFramesReceived || 0} | ${c.domMessages || 0} | ${c.incomingMarkers || 0}/${c.outgoingMarkers || 0} | ${has('incoming')} | ${has('outgoing')} |`
    );
  }
  if (!sites.length) lines.push('| (no site was captured) | | | | | | | | |');
  lines.push('', '## Per-site folders', '');
  for (const site of sites) {
    lines.push(`- **${site.domain || site.key}** → \`sites/${site.key}/\``);
    lines.push(`  - start here: \`sites/${site.key}/SUMMARY.md\`, then \`messages/sms-summary.json\``);
    if (site.hosts && site.hosts.length) lines.push(`  - hosts: ${site.hosts.join(', ')}`);
  }
  lines.push('', '## Next step per site', '');
  lines.push('- Open `sites/<domain>/messages/sms-summary.json`: it says how incoming messages arrive and how to send, from YOUR markers.');
  lines.push('- No `incoming: CONFIRMED` yet? Chat on that site with markers (Ctrl+Alt+I / Ctrl+Alt+O) and capture again.');
  lines.push('- One browser run can contain many sites; compare a folder against an older one with the existing `compare` tool.');
  lines.push('');
  return lines.join('\n');
}

module.exports = {
  POLICY,
  hostOf,
  domainOf,
  siteKey,
  isSameSite,
  hostRole,
  siteBase,
  endpointPattern,
  markerConfidence,
  smsSummary,
  renderSiteSummary,
  renderRunSummary,
};
