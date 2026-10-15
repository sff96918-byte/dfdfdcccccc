"use strict";
// Message classification helpers shared by the CDP layer, the in-page hooks
// and the final correlation report. Everything here is a heuristic plus
// evidence; user markers (Ctrl+Alt+I / Ctrl+Alt+O) are the ground truth.

const MESSAGE_KEYS = ["message", "text", "content", "body", "msg", "value"];
const AUTHOR_KEYS = ["author", "sender", "from", "user", "username", "senderid", "userid", "fromid"];
const EVENT_KEYS = ["event", "type", "action", "name", "op", "channel"];

function parsePayload(text) {
  const raw = String(text == null ? "" : text);
  try {
    return { kind: "json", value: JSON.parse(raw) };
  } catch {
    /* fall through */
  }
  // Socket.IO style: 42["event", {...}]
  const sio = raw.match(/^\d+\s*(\[.*\])$/s);
  if (sio) {
    try {
      const arr = JSON.parse(sio[1]);
      if (Array.isArray(arr) && typeof arr[0] === "string") {
        return { kind: "socketio", value: arr[1], eventName: arr[0], raw: arr };
      }
    } catch {
      /* ignore */
    }
  }
  return { kind: "text", value: raw };
}

function findMessageInfo(value, depth = 0) {
  if (!value || typeof value !== "object" || depth > 5) return null;
  const info = { hasMessage: false, text: null, author: null, eventName: null };
  for (const key of EVENT_KEYS) {
    if (typeof value[key] === "string" && !info.eventName) info.eventName = value[key];
  }
  for (const key of MESSAGE_KEYS) {
    if (typeof value[key] === "string" && value[key].length > 0) {
      info.hasMessage = true;
      info.text = value[key];
      break;
    }
  }
  for (const key of AUTHOR_KEYS) {
    const v = value[key];
    if (typeof v === "string" || typeof v === "number") info.author = String(v);
    else if (v && typeof v === "object" && (v.name || v.username || v.id)) {
      info.author = String(v.name || v.username || v.id);
    }
    if (info.author) break;
  }
  if (!info.hasMessage) {
    for (const v of Object.values(value)) {
      const nested = findMessageInfo(v, depth + 1);
      if (nested && nested.hasMessage) {
        return {
          hasMessage: true,
          text: nested.text,
          author: info.author || nested.author,
          eventName: info.eventName || nested.eventName,
        };
      }
    }
  }
  return info.hasMessage || info.eventName ? info : null;
}

function extractEventName(parsed) {
  if (!parsed) return null;
  if (parsed.kind === "socketio") return parsed.eventName || null;
  if (parsed.kind === "json") {
    const info = findMessageInfo(parsed.value);
    if (info && info.eventName) return info.eventName;
    if (parsed.value && typeof parsed.value === "object") {
      for (const key of EVENT_KEYS) {
        if (typeof parsed.value[key] === "string") return parsed.value[key];
      }
    }
  }
  return null;
}

const OWN_HINT = /(own|self|mine|sent|outgoing|from-me|by-me|(^|[-_ ])me([-_ ]|$)|right)/i;
const OTHER_HINT = /(other|incoming|received|from-them|by-them|(^|[-_ ])them([-_ ]|$)|left|peer|partner)/i;

function classifyDomHint(classText) {
  const s = String(classText || "");
  if (OWN_HINT.test(s)) return "outgoing";
  if (OTHER_HINT.test(s)) return "incoming";
  return "unknown";
}

function classifyFrame(direction, parsed) {
  const info = parsed && parsed.kind !== "text" ? findMessageInfo(parsed.value) : null;
  if (!info || !info.hasMessage) return null;
  // A sent frame carrying message text is an outgoing candidate; a received
  // frame carrying message text is an incoming candidate. Markers confirm.
  return direction === "sent" ? "outgoing-candidate" : "incoming-candidate";
}

module.exports = { parsePayload, findMessageInfo, extractEventName, classifyDomHint, classifyFrame };
