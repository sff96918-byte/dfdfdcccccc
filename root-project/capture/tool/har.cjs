"use strict";
// Minimal HAR 1.2 builder from captured request records (already redacted).

function buildHar(records, creatorName) {
  const entries = [];
  for (const r of records) {
    if (!r.url) continue;
    entries.push({
      startedDateTime: r.startedAt || new Date().toISOString(),
      time: r.durationMs != null ? r.durationMs : -1,
      request: {
        method: r.method || "GET",
        url: r.url,
        httpVersion: r.protocol || "",
        headers: Object.entries(r.requestHeaders || {}).map(([name, value]) => ({ name, value: String(value) })),
        queryString: [],
        postData: r.requestBodyText
          ? { mimeType: (r.requestHeaders || {})["content-type"] || "", text: r.requestBodyText }
          : undefined,
        headersSize: -1,
        bodySize: r.requestBodyText ? r.requestBodyText.length : 0,
      },
      response: {
        status: r.status != null ? r.status : 0,
        statusText: r.statusText || "",
        httpVersion: r.protocol || "",
        headers: Object.entries(r.responseHeaders || {}).map(([name, value]) => ({ name, value: String(value) })),
        content: {
          size: r.responseBodyText ? r.responseBodyText.length : 0,
          mimeType: r.mimeType || "",
          text: r.responseBodyText || undefined,
        },
        redirectURL: "",
        headersSize: -1,
        bodySize: r.responseBodyText ? r.responseBodyText.length : 0,
        _remoteIPAddress: r.remoteIPAddress || undefined,
      },
      cache: {},
      timings: { send: -1, wait: -1, receive: -1 },
      _resourceType: r.resourceType || undefined,
      _requestId: r.id,
    });
  }
  entries.sort((a, b) => String(a.startedDateTime).localeCompare(String(b.startedDateTime)));
  return {
    log: {
      version: "1.2",
      creator: { name: creatorName || "root-project capture tool", version: "1.0.0" },
      entries,
    },
  };
}

module.exports = { buildHar };
