// In-page capture hooks. Injected via CDP Page.addScriptToEvaluateOnNewDocument
// (and evaluated once for already-loaded documents). Emits single-line console
// messages with the __CAPTURE_HOOK__ prefix; the Node side parses them from
// Runtime.consoleAPICalled. This is a SECONDARY capture layer: the CDP Network
// domain remains the source of truth.
(function () {
  if (window.__captureHooksInstalled) return;
  window.__captureHooksInstalled = true;
  var PREFIX = "__CAPTURE_HOOK__";
  var MAX = 8000;

  function str(v) {
    try {
      if (v == null) return "";
      if (typeof v === "string") return v.length > MAX ? v.slice(0, MAX) + "…[truncated]" : v;
      if (v instanceof ArrayBuffer) return "[ArrayBuffer " + v.byteLength + " bytes]";
      if (ArrayBuffer.isView(v)) return "[" + v.constructor.name + " " + v.byteLength + " bytes]";
      if (typeof Blob !== "undefined" && v instanceof Blob) return "[Blob " + v.type + " " + v.size + " bytes]";
      var s = JSON.stringify(v);
      return s && s.length > MAX ? s.slice(0, MAX) + "…[truncated]" : s || String(v);
    } catch (e) {
      return "[unserializable]";
    }
  }

  function emit(type, data) {
    try {
      var payload = { type: type, ts: Date.now(), pageUrl: location.href };
      for (var k in data || {}) payload[k] = data[k];
      console.log(PREFIX + " " + JSON.stringify(payload));
    } catch (e) {
      /* never break the page */
    }
  }

  // ---- WebSocket -----------------------------------------------------------
  try {
    var OrigWS = window.WebSocket;
    if (OrigWS) {
      var origSend = OrigWS.prototype.send;
      OrigWS.prototype.send = function (data) {
        emit("ws_send", { url: this.url || "", data: str(data) });
        return origSend.call(this, data);
      };
      var WrappedWS = function (url, protocols) {
        var ws = protocols !== undefined ? new OrigWS(url, protocols) : new OrigWS(url);
        ws.addEventListener("open", function () { emit("ws_open", { url: ws.url || "" }); });
        ws.addEventListener("message", function (ev) { emit("ws_message", { url: ws.url || "", data: str(ev.data) }); });
        ws.addEventListener("close", function (ev) { emit("ws_close", { url: ws.url || "", code: ev.code, reason: ev.reason || "" }); });
        ws.addEventListener("error", function () { emit("ws_error", { url: ws.url || "" }); });
        return ws;
      };
      WrappedWS.prototype = OrigWS.prototype;
      WrappedWS.CONNECTING = OrigWS.CONNECTING;
      WrappedWS.OPEN = OrigWS.OPEN;
      WrappedWS.CLOSING = OrigWS.CLOSING;
      WrappedWS.CLOSED = OrigWS.CLOSED;
      window.WebSocket = WrappedWS;
    }
  } catch (e) { /* ignore */ }

  // ---- fetch ---------------------------------------------------------------
  try {
    if (window.fetch) {
      var origFetch = window.fetch;
      window.fetch = function (input, init) {
        var url = typeof input === "string" ? input : (input && input.url) || "";
        var method = (init && init.method) || (input && input.method) || "GET";
        var body = init && init.body != null ? str(init.body) : "";
        emit("fetch_request", { url: url, method: method, data: body });
        return origFetch.apply(this, arguments).then(function (res) {
          emit("fetch_response", { url: res.url || url, method: method, status: res.status });
          return res;
        }, function (err) {
          emit("fetch_error", { url: url, method: method, error: String(err && err.message || err) });
          throw err;
        });
      };
    }
  } catch (e) { /* ignore */ }

  // ---- XMLHttpRequest --------------------------------------------------------
  try {
    var OrigXHR = window.XMLHttpRequest;
    if (OrigXHR) {
      var origOpen = OrigXHR.prototype.open;
      var origXhrSend = OrigXHR.prototype.send;
      OrigXHR.prototype.open = function (method, url) {
        this.__capMethod = method;
        this.__capUrl = url;
        return origOpen.apply(this, arguments);
      };
      OrigXHR.prototype.send = function (body) {
        var self = this;
        emit("xhr_request", { url: self.__capUrl || "", method: self.__capMethod || "", data: str(body) });
        this.addEventListener("load", function () {
          var text = "";
          try {
            if (!self.responseType || self.responseType === "text") text = str(self.responseText);
          } catch (e) { /* ignore */ }
          emit("xhr_response", { url: self.responseURL || self.__capUrl || "", method: self.__capMethod || "", status: self.status, data: text });
        });
        this.addEventListener("error", function () {
          emit("xhr_error", { url: self.__capUrl || "", method: self.__capMethod || "" });
        });
        return origXhrSend.apply(this, arguments);
      };
    }
  } catch (e) { /* ignore */ }

  // ---- EventSource (SSE) -----------------------------------------------------
  try {
    var OrigES = window.EventSource;
    if (OrigES) {
      var WrappedES = function (url, opts) {
        var es = opts !== undefined ? new OrigES(url, opts) : new OrigES(url);
        es.addEventListener("message", function (ev) { emit("sse_message", { url: String(url), data: str(ev.data) }); });
        return es;
      };
      WrappedES.prototype = OrigES.prototype;
      WrappedES.CONNECTING = OrigES.CONNECTING;
      WrappedES.OPEN = OrigES.OPEN;
      WrappedES.CLOSED = OrigES.CLOSED;
      window.EventSource = WrappedES;
    }
  } catch (e) { /* ignore */ }

  // ---- DOM message observer (top frame only) ---------------------------------
  var OWN = /(own|self|mine|sent|outgoing|from-me|by-me|(^|[-_ ])me([-_ ]|$)|right)/i;
  var OTHER = /(other|incoming|received|from-them|by-them|(^|[-_ ])them([-_ ]|$)|left|peer|partner)/i;
  var MSGISH = /(message|msg|chat|bubble|balloon|row|item)/i;

  function selectorOf(el) {
    var parts = [];
    var node = el;
    while (node && node.nodeType === 1 && parts.length < 4) {
      var part = node.tagName.toLowerCase();
      if (node.id) part += "#" + node.id;
      else if (typeof node.className === "string" && node.className.trim()) {
        part += "." + node.className.trim().split(/\s+/).slice(0, 3).join(".");
      }
      parts.unshift(part);
      node = node.parentElement;
    }
    return parts.join(" > ");
  }

  function maybeMessage(el, seen) {
    if (!el || el.nodeType !== 1) return;
    var text = (el.innerText || el.textContent || "").trim().replace(/\s+/g, " ");
    if (!text || text.length < 1 || text.length > 2000) return;
    var cls = (typeof el.className === "string" ? el.className : "") + " " + (el.id || "");
    var parentCls = "";
    var p = el.parentElement;
    for (var i = 0; p && i < 3; i++, p = p.parentElement) {
      parentCls += " " + (typeof p.className === "string" ? p.className : "");
    }
    if (!MSGISH.test(cls) && !MSGISH.test(parentCls) && !OWN.test(cls) && !OTHER.test(cls)) return;
    var key = text + "|" + cls;
    if (seen.has(key)) return;
    seen.add(key);
    var hint = "unknown";
    if (OWN.test(cls)) hint = "outgoing";
    else if (OTHER.test(cls)) hint = "incoming";
    else if (OWN.test(parentCls)) hint = "outgoing";
    else if (OTHER.test(parentCls)) hint = "incoming";
    var html = "";
    try { html = (el.outerHTML || "").slice(0, 2000); } catch (e) { /* ignore */ }
    emit("dom_message", {
      directionHint: hint,
      text: text.slice(0, 1000),
      className: cls.trim().slice(0, 300),
      selector: selectorOf(el),
      html: html,
    });
  }

  function startObserver() {
    try {
      if (window.top !== window) return;
      var seen = new Map();
      var observer = new MutationObserver(function (mutations) {
        var now = Date.now();
        for (var entries = seen.entries(), e = entries.next(); !e.done; e = entries.next()) {
          if (now - e.value > 5000) seen.delete(e.key);
        }
        var budget = 50;
        for (var i = 0; i < mutations.length && budget > 0; i++) {
          var added = mutations[i].addedNodes;
          for (var j = 0; j < added.length && budget > 0; j++) {
            var node = added[j];
            if (node.nodeType !== 1) continue;
            var wrap = new Map();
            maybeMessage(node, wrap);
            if (wrap.size) budget--;
            var descendants = node.querySelectorAll ? node.querySelectorAll("*") : [];
            for (var k = 0; k < descendants.length && k < 20 && budget > 0; k++) {
              var w2 = new Map();
              maybeMessage(descendants[k], w2);
              if (w2.size) budget--;
            }
          }
        }
      });
      observer.observe(document.documentElement, { childList: true, subtree: true });
      emit("observer_started", {});
    } catch (e) { /* ignore */ }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", startObserver);
  } else {
    startObserver();
  }

  // ---- Manual markers: Ctrl+Alt+I / O / S ------------------------------------
  try {
    document.addEventListener("keydown", function (ev) {
      if (!ev.ctrlKey || !ev.altKey) return;
      var k = String(ev.key || "").toLowerCase();
      if (k === "i") emit("marker", { label: "incoming" });
      else if (k === "o") emit("marker", { label: "outgoing" });
      else if (k === "s") emit("marker", { label: "snapshot" });
    }, true);
  } catch (e) { /* ignore */ }

  // Used by the Node side via Runtime.evaluate for DOM/storage snapshots.
  window.__captureDump = function () {
    function dump(storage) {
      var out = {};
      try {
        for (var i = 0; i < storage.length; i++) {
          var k = storage.key(i);
          out[k] = storage.getItem(k);
        }
      } catch (e) { /* ignore */ }
      return out;
    }
    return {
      url: location.href,
      title: document.title || "",
      html: document.documentElement ? document.documentElement.outerHTML : "",
      localStorage: dump(window.localStorage),
      sessionStorage: dump(window.sessionStorage),
    };
  };

  emit("hooks_installed", {});
})();
