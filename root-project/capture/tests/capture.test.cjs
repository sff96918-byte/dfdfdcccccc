"use strict";
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const red = require("../tool/redactor.cjs");
const { parsePayload, findMessageInfo, classifyDomHint, classifyFrame } = require("../tool/classifier.cjs");
const { normalizePath, Catalog } = require("../tool/catalog.cjs");
const { buildHar } = require("../tool/har.cjs");
const { SessionStore } = require("../tool/store.cjs");
const { createFrameParser, encodeTextFrame } = require("../selftest/server.cjs");
const { WsCatalog } = require("../tool/wscatalog.cjs");
const { computeCapabilities } = require("../tool/capabilities.cjs");
const { getFreePort } = require("../tool/browser.cjs");
const net = require("node:net");
const { loadSession, diffSessions } = require("../tool/compare.cjs");

test("redactor hides secrets but keeps structure", () => {
  const out = red.processBody('{"token":"abc123","message":{"text":"hi","password":"x"}}', true, 10000);
  assert.equal(out.parsed.token, "REDACTED");
  assert.equal(out.parsed.message.text, "hi");
  assert.equal(out.parsed.message.password, "REDACTED");
  const raw = red.processBody('{"token":"abc123"}', false, 10000);
  assert.equal(raw.parsed.token, "abc123"); // rawMode is explicit opt-in
  assert.equal(red.redactUrl("https://x.test/a?token=zzz&page=2", true), "https://x.test/a?token=REDACTED&page=2");
  const msg = red.processBody('{"author":"user-123","content":"hi"}', true, 10000);
  assert.equal(msg.parsed.author, "user-123"); // sender ids are not secrets
  assert.deepEqual(red.redactHeaders({ Authorization: "Bearer q", "x-ok": "1" }, true), {
    Authorization: "REDACTED",
    "x-ok": "1",
  });
});

test("classifier finds message text/author/event in nested payloads", () => {
  const parsed = parsePayload('{"event":"chat","data":{"message":{"text":"hello","from":{"name":"raju"}}}}');
  const info = findMessageInfo(parsed.value);
  assert.equal(info.text, "hello");
  assert.equal(info.author, "raju");
  assert.equal(info.eventName, "chat");
  const sio = parsePayload('42["new_message",{"text":"hi","sender":"u2"}]');
  assert.equal(sio.kind, "socketio");
  assert.equal(sio.eventName, "new_message");
  assert.equal(classifyFrame("sent", parsed), "outgoing-candidate");
  assert.equal(classifyFrame("received", parsed), "incoming-candidate");
  assert.equal(classifyFrame("received", parsePayload("plain text")), null);
});

test("DOM hint classification uses own/other markers", () => {
  assert.equal(classifyDomHint("msg msg-own"), "outgoing");
  assert.equal(classifyDomHint("message incoming from-them"), "incoming");
  assert.equal(classifyDomHint("message"), "unknown");
});

test("catalog normalizes ids and aggregates key shapes", () => {
  assert.equal(normalizePath("/rooms/123/messages/9f2ab31c44deadbeef"), "/rooms/:id/messages/:hex");
  const c = new Catalog();
  c.add({ method: "POST", url: "https://api.test/rooms/42/messages", status: 200, requestBody: { text: "hi" }, responseBody: { id: 1, text: "hi" }, ts: "t1" });
  c.add({ method: "POST", url: "https://api.test/rooms/43/messages", status: 200, requestBody: { text: "yo" }, responseBody: { id: 2, text: "yo" }, ts: "t2" });
  const [entry] = c.toJSON();
  assert.equal(entry.pattern, "POST https://api.test/rooms/:id/messages");
  assert.equal(entry.count, 2);
  assert.deepEqual(entry.statuses, { 200: 2 });
  assert.ok(entry.requestKeys.includes("text"));
});

test("HAR builder emits ordered entries with redacted bodies intact", () => {
  const har = buildHar(
    [
      { id: "2", startedAt: "2026-01-01T00:00:02Z", method: "GET", url: "https://a.test/2", status: 200, responseBodyText: "{}" },
      { id: "1", startedAt: "2026-01-01T00:00:01Z", method: "GET", url: "https://a.test/1", status: 404 },
    ],
    "test",
  );
  assert.equal(har.log.entries.length, 2);
  assert.equal(har.log.entries[0].request.url, "https://a.test/1");
  assert.equal(har.log.entries[1].response.status, 200);
});

test("session store writes real-time JSONL and counts", () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), "cap-test-"));
  const store = new SessionStore(base, "https://chitchat.gg/");
  store.event("t", { a: 1 });
  store.appendJsonl("messages/dom-messages.jsonl", { text: "hi" });
  assert.ok(fs.existsSync(path.join(store.dir, "events.jsonl")));
  assert.ok(fs.readFileSync(path.join(store.dir, "events.jsonl"), "utf8").includes('"a":1'));
  assert.ok(fs.existsSync(path.join(store.dir, "websockets")));
});

test("self-test websocket frame codec round-trips masked client frames", () => {
  const frames = [];
  const parse = createFrameParser((t) => frames.push(t));
  // Build a masked client text frame for "hello".
  const payload = Buffer.from("hello");
  const mask = Buffer.from([1, 2, 3, 4]);
  const masked = Buffer.from(payload);
  for (let i = 0; i < masked.length; i++) masked[i] ^= mask[i % 4];
  parse(Buffer.concat([Buffer.from([0x81, 0x80 | payload.length]), mask, masked]));
  assert.deepEqual(frames, ["hello"]);
  const serverFrame = encodeTextFrame("world");
  assert.equal(serverFrame[0], 0x81);
  assert.equal(serverFrame.subarray(2).toString(), "world");
});

test("websocket catalog aggregates directions and payload key shapes", () => {
  const c = new WsCatalog();
  c.add({ url: "wss://rt.test/ws", eventName: "new_message", direction: "received", payload: { type: "new_message", message: { text: "hi" } }, ts: "t1" });
  c.add({ url: "wss://rt.test/ws", eventName: "new_message", direction: "received", payload: { type: "new_message", message: { text: "yo", id: 3 } }, ts: "t2" });
  c.add({ url: "wss://rt.test/ws", eventName: "send_message", direction: "sent", payload: { text: "out" }, ts: "t3" });
  const rows = c.toJSON();
  assert.equal(rows.length, 2);
  const incoming = rows.find((r) => r.eventName === "new_message");
  assert.equal(incoming.received, 2);
  assert.ok(incoming.payloadKeys.includes("message.text"));
  assert.ok(incoming.payloadKeys.includes("message.id"));
});

function fakeSession(api, ws) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "cap-diff-"));
  if (api) fs.writeFileSync(path.join(dir, "api-catalog.json"), JSON.stringify(api));
  if (ws) {
    fs.mkdirSync(path.join(dir, "websockets"), { recursive: true });
    fs.writeFileSync(path.join(dir, "websockets", "catalog.json"), JSON.stringify(ws));
  }
  return dir;
}

test("compare diff detects added/removed/changed endpoints and websocket events", () => {
  const oldDir = fakeSession(
    [{ pattern: "POST https://api.test/rooms/:id/messages", statuses: { 200: 3 }, requestKeys: ["text"], responseKeys: ["id", "text"] }],
    [{ key: "wss://rt.test/ws :: new_message", url: "wss://rt.test/ws", eventName: "new_message", sent: 0, received: 2, payloadKeys: ["message.text"] }],
  );
  const newDir = fakeSession(
    [
      { pattern: "POST https://api.test/rooms/:id/messages", statuses: { 200: 1, 429: 1 }, requestKeys: ["text", "clientId"], responseKeys: ["id", "text"] },
      { pattern: "GET https://api.test/presence", statuses: { 200: 1 }, requestKeys: [], responseKeys: ["online"] },
    ],
    [{ key: "wss://rt.test/ws :: message_created", url: "wss://rt.test/ws", eventName: "message_created", sent: 0, received: 1, payloadKeys: ["message.text"] }],
  );
  const diff = diffSessions(loadSession(oldDir), loadSession(newDir));
  assert.deepEqual(diff.endpoints.added, ["GET https://api.test/presence"]);
  assert.equal(diff.endpoints.changed.length, 1);
  assert.ok(diff.endpoints.changed[0].changes.some((c) => c.includes("clientId")));
  assert.ok(diff.endpoints.changed[0].changes.some((c) => c.includes("429")));
  assert.deepEqual(diff.websockets.added, ["wss://rt.test/ws :: message_created"]);
  assert.deepEqual(diff.websockets.removed, ["wss://rt.test/ws :: new_message"]);
  assert.equal(diff.summary.wsAdded, 1);
});

test("identical sessions produce an empty diff and identical fingerprint flag stays honest", () => {
  const api = [{ pattern: "GET https://api.test/a", statuses: { 200: 1 }, requestKeys: [], responseKeys: [] }];
  const a = fakeSession(api, []);
  const b = fakeSession(api, []);
  const diff = diffSessions(loadSession(a), loadSession(b));
  assert.equal(diff.summary.endpointsAdded + diff.summary.endpointsRemoved + diff.summary.endpointsChanged, 0);
  assert.equal(diff.schemaFingerprint.identical, false); // no manifests in fake sessions: never claim identical
});

test("capability checklist turns capture counts into what-can-be-built", () => {
  const empty = computeCapabilities({ counts: {}, coverage: {} });
  assert.ok(empty.every((c) => !c.achieved));
  const state = {
    counts: { httpResponses: 5, wsConnections: 1, wsFramesSent: 2, wsFramesReceived: 3, domMessages: 4, incomingMarkers: 1, outgoingMarkers: 0, incomingCandidates: 2, outgoingCandidates: 1 },
    coverage: { domSnapshots: true, storage: true },
  };
  const caps = Object.fromEntries(computeCapabilities(state).map((c) => [c.key, c]));
  assert.equal(caps.websocket.achieved, true);
  assert.equal(caps.incoming.achieved, true);
  assert.equal(caps.outgoing.achieved, false); // detected is not confirmed
  assert.equal(caps.dommsg.achieved, true);
  assert.match(caps.incoming.enables, /detect/);
});

test("getFreePort returns a port that can actually be bound", async () => {
  const port = await getFreePort();
  assert.ok(Number.isInteger(port) && port > 0 && port < 65536);
  const srv = net.createServer();
  await new Promise((resolve, reject) => {
    srv.once("error", reject);
    srv.listen(port, "127.0.0.1", resolve);
  });
  await new Promise((r) => srv.close(r));
});
