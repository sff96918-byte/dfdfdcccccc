"use strict";
// Tiny self-test server: serves page.html and a minimal WebSocket echo/chat
// endpoint using only Node built-ins. NOT part of the production capture flow.

const http = require("node:http");
const crypto = require("node:crypto");
const fs = require("node:fs");
const path = require("node:path");

function encodeTextFrame(text) {
  const payload = Buffer.from(text, "utf8");
  let header;
  if (payload.length < 126) {
    header = Buffer.from([0x81, payload.length]);
  } else if (payload.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x81;
    header[1] = 126;
    header.writeUInt16BE(payload.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x81;
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(payload.length), 2);
  }
  return Buffer.concat([header, payload]);
}

function createFrameParser(onMessage, onClose) {
  let buffer = Buffer.alloc(0);
  return (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
    for (;;) {
      if (buffer.length < 2) return;
      const opcode = buffer[0] & 0x0f;
      const masked = (buffer[1] & 0x80) !== 0;
      let len = buffer[1] & 0x7f;
      let offset = 2;
      if (len === 126) {
        if (buffer.length < 4) return;
        len = buffer.readUInt16BE(2);
        offset = 4;
      } else if (len === 127) {
        if (buffer.length < 10) return;
        len = Number(buffer.readBigUInt64BE(2));
        offset = 10;
      }
      const maskLen = masked ? 4 : 0;
      if (buffer.length < offset + maskLen + len) return;
      let payload = buffer.subarray(offset + maskLen, offset + maskLen + len);
      if (masked) {
        const mask = buffer.subarray(offset, offset + 4);
        payload = Buffer.from(payload);
        for (let i = 0; i < payload.length; i++) payload[i] ^= mask[i % 4];
      } else {
        payload = Buffer.from(payload);
      }
      buffer = buffer.subarray(offset + maskLen + len);
      if (opcode === 0x8) return onClose && onClose();
      if (opcode === 0x9) continue; // ping: ignored by the test server
      if (opcode === 0x1 || opcode === 0x2) onMessage(payload.toString("utf8"));
    }
  };
}

function startSelfTestServer(port = 0) {
  const page = fs.readFileSync(path.join(__dirname, "page.html"), "utf8");
  const server = http.createServer((req, res) => {
    if (req.url === "/" || req.url === "/index.html") {
      res.writeHead(200, { "content-type": "text/html; charset=utf-8" });
      res.end(page);
    } else if (req.url === "/api/hello") {
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ ok: true, service: "selftest" }));
    } else {
      res.writeHead(404);
      res.end("not found");
    }
  });

  server.on("upgrade", (req, socket) => {
    if (req.url !== "/chat") {
      socket.destroy();
      return;
    }
    const key = req.headers["sec-websocket-key"];
    const accept = crypto
      .createHash("sha1")
      .update(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
      .digest("base64");
    socket.write(
      "HTTP/1.1 101 Switching Protocols\r\n" +
        "Upgrade: websocket\r\n" +
        "Connection: Upgrade\r\n" +
        `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    const send = (obj) => socket.write(encodeTextFrame(JSON.stringify(obj)));
    send({ type: "welcome", message: { from: "server", text: "connected to selftest" } });
    const parse = createFrameParser((text) => {
      let data = null;
      try {
        data = JSON.parse(text);
      } catch {
        /* plain text */
      }
      // Echo the sender's message back, then simulate a reply from another user.
      send({ type: "echo", message: { from: "me", text: data && data.text ? data.text : text } });
      setTimeout(() => {
        send({ type: "message", message: { from: "other-user", text: "hello incoming (selftest)" } });
      }, 250);
    });
    socket.on("data", parse);
    socket.on("error", () => {});
  });

  return new Promise((resolve) => {
    server.listen(port, "127.0.0.1", () => {
      resolve({ server, port: server.address().port });
    });
  });
}

module.exports = { startSelfTestServer, encodeTextFrame, createFrameParser };
