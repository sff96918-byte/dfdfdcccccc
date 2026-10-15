"""Loopback proxy relay: Chrome talks to us, we talk to the real proxy.

Port of the Go ``internal/proxy/relay.go``. Chrome cannot send credentials for
every proxy type (SOCKS5 auth, and inline credentials on the command line were
removed from Chromium), so the browser is pointed at ``http://127.0.0.1:<port>``
with no credentials at all and this process adds them upstream. Credentials
therefore never appear in a browser command line or in the process list.

Guarantees:

* bound ONLY to loopback, one relay per browser, closed together with it,
* never dials a destination directly - every byte goes through the configured
  proxy (a failing proxy means a failing request, never a silent direct one),
* ``CONNECT`` becomes a raw tunnel (HTTPS and websockets); plain ``http://``
  requests are forwarded in absolute form through an HTTP proxy, or origin-form
  through a SOCKS5 tunnel with the name resolved upstream.
"""

from __future__ import annotations

import contextlib
import select
import socket
import socketserver
import threading

from .proxy import DIAL_TIMEOUT_SECONDS, Entry, TunnelError, dial_tunnel

HANDSHAKE_TIMEOUT_SECONDS = 10.0
IO_TIMEOUT_SECONDS = 60.0
HEAD_LIMIT = 64 << 10
BUFFER_SIZE = 65536
MAX_BODY_BYTES = 32 << 20
HOP_BY_HOP = frozenset(
    (
        "connection",
        "proxy-connection",
        "proxy-authorization",
        "proxy-authenticate",
        "keep-alive",
        "te",
        "trailer",
        "transfer-encoding",
        "upgrade",
    )
)


class Relay:
    """One local, unauthenticated HTTP proxy in front of one upstream entry."""

    def __init__(self, upstream: Entry):
        self.upstream = upstream
        self.entry = Entry()  # replaced by start() with the real local address
        self._server: socketserver.ThreadingTCPServer | None = None
        self._thread: threading.Thread | None = None
        self._resources: list[object] = []
        self._lock = threading.Lock()
        self._closed = False

    # -- lifecycle ---------------------------------------------------------
    @property
    def browser_url(self) -> str:
        return self.entry.browser_url()

    @property
    def closed(self) -> bool:
        return self._closed

    def start(self) -> "Relay":
        server = _RelayServer(("127.0.0.1", 0), _RelayHandler, self)
        server.daemon_threads = True
        self._server = server
        self.entry = Entry(scheme="http", host="127.0.0.1", port=str(server.server_address[1]))
        self._thread = threading.Thread(target=server.serve_forever, name="proxy-relay", daemon=True)
        self._thread.start()
        return self

    def close(self) -> None:
        with self._lock:
            if self._closed:
                return
            self._closed = True
            resources = list(self._resources)
            self._resources.clear()
        for resource in resources:
            close = getattr(resource, "close", None)
            shutdown = getattr(resource, "shutdown", None)
            if callable(shutdown):
                with contextlib.suppress(OSError, TypeError):
                    shutdown(socket.SHUT_RDWR)
            if callable(close):
                with contextlib.suppress(OSError, TypeError):
                    close()
        server, self._server = self._server, None
        if server is not None:
            with contextlib.suppress(Exception):
                server.shutdown()
            with contextlib.suppress(Exception):
                server.server_close()

    # -- bookkeeping -------------------------------------------------------
    def track(self, resource: object) -> None:
        with self._lock:
            if self._closed:
                raise OSError("relay is closed")
            self._resources.append(resource)

    def untrack(self, resource: object) -> None:
        with self._lock:
            with contextlib.suppress(ValueError):
                self._resources.remove(resource)

    def open_tunnel(self, target: str):
        """Connect through the configured proxy only (never directly)."""
        return dial_tunnel(self.upstream, target, timeout=DIAL_TIMEOUT_SECONDS)


class _RelayServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    request_queue_size = 64

    def __init__(self, address, handler_class, relay: Relay):
        self.relay = relay
        super().__init__(address, handler_class)

    def handle_error(self, request, client_address) -> None:
        return  # a broken browser connection must not print a traceback


class _RelayHandler(socketserver.BaseRequestHandler):
    """Speaks just enough HTTP proxy for a browser."""

    def setup(self) -> None:
        self.relay: Relay = self.server.relay  # type: ignore[attr-defined]
        self.connection = self.request
        self.relay.track(self.connection)

    def finish(self) -> None:
        self.relay.untrack(self.connection)
        with contextlib.suppress(OSError):
            self.connection.close()

    def handle(self) -> None:
        try:
            self.connection.settimeout(HANDSHAKE_TIMEOUT_SECONDS)
            head, _leftover = _read_head(self.connection, HEAD_LIMIT)
        except OSError:
            return
        lines = head.decode("latin-1").split("\r\n")
        request_line = lines[0].split(" ")
        if len(request_line) < 3:
            return _fail(self.connection, 400, "malformed proxy request line")
        method, target = request_line[0].upper(), request_line[1]
        headers = _parse_headers(lines[1:])
        if method == "CONNECT":
            self._tunnel(target)
            return
        self._forward(method, target, headers)

    # -- CONNECT: raw tunnel ----------------------------------------------
    def _tunnel(self, target: str) -> None:
        try:
            remote = self.relay.open_tunnel(target)
        except TunnelError as error:
            return _fail(self.connection, 502, error.detail)
        except OSError:
            return _fail(self.connection, 502, "upstream proxy connection failed")
        client = self.connection
        try:
            self.relay.track(remote)
            client.settimeout(None)
            client.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
            pipelined = remote.take_buffer()
            if pipelined:
                client.sendall(pipelined)
            _pump(client, remote, self.relay)
        except OSError:
            return
        finally:
            self.relay.untrack(remote)
            with contextlib.suppress(OSError):
                remote.close()

    # -- plain HTTP: forward with credentials ------------------------------
    def _forward(self, method: str, target: str, headers: dict[str, str]) -> None:
        if not target.lower().startswith("http://"):
            return _fail(self.connection, 400, "absolute HTTP proxy URL required")
        authority, _, path = target[len("http://") :].partition("/")
        path = "/" + path if path else "/"
        host, _, port_text = authority.partition(":")
        if not host:
            return _fail(self.connection, 400, "absolute HTTP proxy URL required")
        try:
            port = int(port_text) if port_text else 80
        except ValueError:
            return _fail(self.connection, 400, "invalid proxy target port")

        body = self._read_body(headers)
        if body is None:
            return _fail(self.connection, 400, "invalid request body")

        upstream = self.relay.upstream
        http_upstream = upstream.scheme_or_default() == "http"
        request_lines = ["{} {} HTTP/1.1".format(method, target if http_upstream else path)]
        forwarded_host = False
        for name, value in headers.items():
            lowered = name.lower()
            if lowered in HOP_BY_HOP:
                continue
            if lowered == "host":
                forwarded_host = True
            request_lines.append("{}: {}".format(name, value))
        if not forwarded_host:
            request_lines.append("Host: " + authority)
        request_lines.append("Connection: close")
        if http_upstream and upstream.has_auth():
            request_lines.append("Proxy-Authorization: Basic " + upstream.basic_token())
        payload = ("\r\n".join(request_lines) + "\r\n\r\n").encode("latin-1") + body

        try:
            if http_upstream:
                remote = socket.create_connection(
                    (upstream.host, int(upstream.port)), timeout=DIAL_TIMEOUT_SECONDS
                )
            else:
                remote = self.relay.open_tunnel("{}:{}".format(host, port))
        except (TunnelError, OSError, ValueError):
            return _fail(self.connection, 502, "upstream proxy connection failed")
        try:
            self.relay.track(remote)
            remote.settimeout(IO_TIMEOUT_SECONDS)
            remote.sendall(payload)
            self.connection.settimeout(IO_TIMEOUT_SECONDS)
            while True:
                chunk = remote.recv(BUFFER_SIZE)
                if not chunk:
                    break
                self.connection.sendall(chunk)
        except OSError:
            return
        finally:
            self.relay.untrack(remote)
            with contextlib.suppress(OSError):
                remote.close()

    def _read_body(self, headers: dict[str, str]) -> bytes | None:
        lowered = {name.lower(): value for name, value in headers.items()}
        if "chunked" in (lowered.get("transfer-encoding") or "").lower():
            return _read_chunked(self.connection)
        length_text = lowered.get("content-length")
        if not length_text:
            return b""
        try:
            length = int(length_text)
        except ValueError:
            return None
        if length < 0 or length > MAX_BODY_BYTES:
            return None
        return _read_exactly(self.connection, length)


# ----------------------------------------------------------------- utilities
def _read_head(sock: socket.socket, limit: int) -> tuple[bytes, bytes]:
    data = b""
    while b"\r\n\r\n" not in data:
        if len(data) >= limit:
            raise OSError("proxy request head is too large")
        chunk = sock.recv(4096)
        if not chunk:
            raise OSError("client closed during the request head")
        data += chunk
    head, _, leftover = data.partition(b"\r\n\r\n")
    return head + b"\r\n\r\n", leftover


def _parse_headers(lines: list[str]) -> dict[str, str]:
    headers: dict[str, str] = {}
    for line in lines:
        if not line:
            continue
        name, separator, value = line.partition(":")
        if not separator:
            continue
        key = name.strip()
        if key and key not in headers:  # first value wins, like Go's Header.Set
            headers[key] = value.strip()
    return headers


def _read_exactly(sock: socket.socket, size: int) -> bytes | None:
    data = b""
    try:
        while len(data) < size:
            chunk = sock.recv(min(BUFFER_SIZE, size - len(data)))
            if not chunk:
                return None
            data += chunk
    except OSError:
        return None
    return data


def _read_line(sock: socket.socket) -> bytes:
    data = b""
    while not data.endswith(b"\r\n"):
        chunk = sock.recv(1)
        if not chunk:
            raise OSError("client closed inside a chunked body")
        data += chunk
        if len(data) > 4096:
            raise OSError("chunk header too long")
    return data


def _read_chunked(sock: socket.socket) -> bytes | None:
    """Consume a chunked request body verbatim (sizes, extensions, trailers)."""
    body = bytearray()
    try:
        while True:
            line = _read_line(sock)
            body += line
            size = int(line[:-2].split(b";", 1)[0].strip() or b"0", 16)
            if size == 0:
                while True:
                    trailer = _read_line(sock)
                    body += trailer
                    if trailer == b"\r\n":
                        return bytes(body)
            chunk = _read_exactly(sock, size + 2)
            if chunk is None:
                return None
            body += chunk
    except (OSError, ValueError):
        return None


def _fail(sock: socket.socket, status: int, message: str) -> None:
    reason = {400: "Bad Request", 502: "Bad Gateway", 500: "Internal Server Error"}.get(status, "Error")
    body = message.encode("utf-8")
    with contextlib.suppress(OSError):
        sock.sendall(
            (
                "HTTP/1.1 {} {}\r\nContent-Type: text/plain; charset=utf-8\r\n"
                "Content-Length: {}\r\nConnection: close\r\n\r\n".format(status, reason, len(body))
            ).encode("latin-1")
            + body
        )


def _pump(client, remote, relay: Relay) -> None:
    """Raw byte relay for an established CONNECT tunnel."""
    client.settimeout(None)
    with contextlib.suppress(OSError):
        remote.settimeout(None)
    while True:
        try:
            readable, _, _ = select.select([client, remote], [], [], 1.0)
        except (OSError, ValueError):
            return
        if not readable:
            if relay.closed:
                return
            continue
        for source in readable:
            try:
                data = source.recv(BUFFER_SIZE)
            except (BlockingIOError, InterruptedError):
                continue
            except OSError:
                return
            if not data:
                return
            destination = remote if source is client else client
            try:
                destination.sendall(data)
            except OSError:
                return
