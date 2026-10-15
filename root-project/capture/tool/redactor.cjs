"use strict";
// Redaction helpers. Default posture: structure is preserved, secrets are not.
// Raw (unredacted) capture is an explicit opt-in via config.rawMode.

const SENSITIVE_KEY =
  /(pass(word)?|token|secret|auth|session|cookie|api[-_]?key|access[-_]?key|refresh|bearer|credential|otp|pin)/i;
const SENSITIVE_HEADER =
  /^(authorization|cookie|set-cookie|x-api-key|x-auth-token|proxy-authorization)$/i;

// "author" contains "auth" but is a public sender id, not a credential.
// Losing it made own-vs-partner detection impossible in captures (v1.2.0).
const NEVER_SENSITIVE = new Set(["author", "authors"]);
function isSensitiveKey(key) {
  const k = String(key || "").toLowerCase();
  if (NEVER_SENSITIVE.has(k)) return false;
  return SENSITIVE_KEY.test(k);
}

function truncate(text, max) {
  const s = String(text == null ? "" : text);
  return s.length > max ? s.slice(0, max) + `…[truncated ${s.length - max} chars]` : s;
}

function redactUrl(rawUrl, enabled) {
  try {
    const u = new URL(rawUrl);
    if (enabled) {
      for (const key of [...u.searchParams.keys()]) {
        if (isSensitiveKey(key)) u.searchParams.set(key, "REDACTED");
      }
    }
    return u.toString();
  } catch {
    return String(rawUrl || "");
  }
}

function redactHeaders(headers, enabled) {
  const out = {};
  for (const [k, v] of Object.entries(headers || {})) {
    out[k] = enabled && SENSITIVE_HEADER.test(k) ? "REDACTED" : v;
  }
  return out;
}

function redactValue(value, enabled, depth = 0) {
  if (!enabled || value == null || depth > 6) return value;
  if (Array.isArray(value)) return value.map((v) => redactValue(v, enabled, depth + 1));
  if (typeof value === "object") {
    const out = {};
    for (const [k, v] of Object.entries(value)) {
      out[k] = isSensitiveKey(k) ? "REDACTED" : redactValue(v, enabled, depth + 1);
    }
    return out;
  }
  return value;
}

// Parse text as JSON when possible, redact, and return {parsed, text}.
function processBody(text, enabled, maxBytes) {
  const raw = String(text == null ? "" : text);
  let parsed = null;
  let isJson = false;
  try {
    parsed = JSON.parse(raw);
    isJson = true;
  } catch {
    /* not JSON */
  }
  if (isJson) {
    const safe = redactValue(parsed, enabled);
    return { parsed: safe, text: truncate(JSON.stringify(safe), maxBytes), isJson: true };
  }
  return { parsed: null, text: truncate(raw, maxBytes), isJson };
}

module.exports = {
  isSensitiveKey,
  truncate,
  redactUrl,
  redactHeaders,
  redactValue,
  processBody,
};
