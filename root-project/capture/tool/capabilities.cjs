"use strict";
// Capability checklist: translates raw capture counts into "what can be built
// from this capture" — shown live in the terminal and saved into SUMMARY.md.

function computeCapabilities(state) {
  const counts = state.counts || {};
  const coverage = state.coverage || {};
  const labeledIncoming = counts.incomingMarkers || 0;
  const labeledOutgoing = counts.outgoingMarkers || 0;
  const frames = (counts.wsFramesSent || 0) + (counts.wsFramesReceived || 0);
  return [
    {
      key: "dom",
      label: "Browser page + DOM capture",
      achieved: Boolean(coverage.domSnapshots),
      enables: "Page-er DOM pore sekhane click/type kore chalano jabe",
      hint: "Browser khullei auto hobe",
    },
    {
      key: "api",
      label: "HTTP / API traffic capture",
      achieved: (counts.httpResponses || 0) > 0,
      enables: "Site kon API diye ki kore, seta bujha jabe",
      hint: "Site browse korlei ashte thakbe",
    },
    {
      key: "websocket",
      label: "WebSocket live frames (realtime chat-er vitti)",
      achieved: (counts.wsConnections || 0) > 0 && frames > 0,
      detail: `socket ${counts.wsConnections || 0} ta, frame pathano ${counts.wsFramesSent || 0}, esheche ${counts.wsFramesReceived || 0}`,
      enables: "Socket diye sorasori realtime chat korar poth paoya jabe",
      hint: "Chat page-e dhuklei socket khule",
    },
    {
      key: "incoming",
      label: "Incoming message method CONFIRMED (apnar marker)",
      achieved: labeledIncoming > 0,
      detail: `incoming candidate ${counts.incomingCandidates || 0} ta dhora poreche`,
      enables: "Onno user-er message kivabe ashe — sei method diye bot incoming detect korte parbe",
      hint: "Onner message asha matro Ctrl+Alt+I chapen",
    },
    {
      key: "outgoing",
      label: "Outgoing message method CONFIRMED (apnar marker)",
      achieved: labeledOutgoing > 0,
      detail: `outgoing candidate ${counts.outgoingCandidates || 0} ta dhora poreche`,
      enables: "Bot kivabe message pathabe — sei method nishchit hobe",
      hint: "Nije message pathiyei Ctrl+Alt+O chapen",
    },
    {
      key: "dommsg",
      label: "DOM message fallback",
      achieved: (counts.domMessages || 0) > 0,
      detail: `DOM-e ${counts.domMessages || 0} ta message node dhora poreche`,
      enables: "Socket bujhte na parleo DOM dekhe chat chalano jete pare",
      hint: "Chat-e message elei auto dhora pore",
    },
    {
      key: "storage",
      label: "Login session (cookies/storage) snapshot",
      achieved: Boolean(coverage.storage),
      enables: "Login kivabe dhore rakhe, session gothon bujha jabe",
      hint: "Snapshot-e auto hoy (Ctrl+Alt+S o nite paren)",
    },
  ];
}

function renderCapabilityLines(caps) {
  return caps.map((c) => {
    const box = c.achieved ? "[HOYE GECHE]" : "[BAKI]      ";
    const detail = c.detail ? ` (${c.detail})` : "";
    const tail = c.achieved ? `=> labh: ${c.enables}` : `=> korte hobe: ${c.hint}`;
    return `${box} ${c.label}${detail}\n             ${tail}`;
  });
}

module.exports = { computeCapabilities, renderCapabilityLines };
