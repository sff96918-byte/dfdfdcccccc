"""Proxy list parsing, rotation, tunnelling and diagnostics.

Port of the Go package ``internal/proxy`` (``proxy.go``, ``connect.go``,
``testproxy.go``): the accepted line formats, the error strings, the round-robin
rotator and the credential-free ``TunnelError`` text are kept identical so a
Python run and a Go run report the same thing about the same proxy.

Nothing here ever dials a destination directly: every connection is made through
the configured proxy, and credentials never appear in an error message (see
:func:`sanitize_provider_text`).
"""

from __future__ import annotations

import base64
import contextlib
import re
import socket
import ssl
import struct
import threading
import time
from dataclasses import dataclass
from typing import Iterable
from urllib.parse import unquote, urlparse

HTTP_SCHEME = "http"
SOCKS5_SCHEME = "socks5"
DIAL_TIMEOUT_SECONDS = 12.0
MAX_TEST_ENTRIES = 20
MAX_TEST_RAW_BYTES = 64 << 10
PROVIDER_MESSAGE_LIMIT = 200
HANDSHAKE_LIMIT = 64 << 10
TEST_ENDPOINT = "https://app.chitchat.gg/"

_HTML_TAG = re.compile(r"<[^<>]{0,200}>")
_HOST_FORBIDDEN = set(" /\\\t\r\n?#@")


# --------------------------------------------------------------------- errors
class TunnelError(Exception):
    """Explains, in credential-free form, why a proxy tunnel failed.

    ``status`` / ``proxy_auth`` / ``provider_message`` come from the proxy's own
    reply and are the evidence a support conversation needs when a provider
    rejects a connection. ``kind`` is one of http, socks, network, protocol,
    input, canceled.
    """

    def __init__(self, kind: str, detail: str, *, status: int = 0, proxy_auth: str = "", provider_message: str = ""):
        super().__init__(detail)
        self.kind = kind
        self.detail = detail
        self.status = status
        self.proxy_auth = proxy_auth
        self.provider_message = provider_message


# --------------------------------------------------------------------- entry
@dataclass(frozen=True)
class Entry:
    scheme: str = HTTP_SCHEME
    host: str = ""
    port: str = ""
    username: str = ""
    password: str = ""

    def scheme_or_default(self) -> str:
        try:
            return normalize_scheme(self.scheme)
        except ValueError:
            return HTTP_SCHEME

    def address(self) -> str:
        host = self.host
        if ":" in host and not host.startswith("["):
            host = "[" + host + "]"
        return "{}:{}".format(host, self.port)

    def browser_url(self) -> str:
        return self.scheme_or_default() + "://" + self.address()

    def label(self) -> str:
        """Proxy identity shown in logs and test results - never credentials."""
        return self.scheme_or_default() + "://" + self.address()

    def has_auth(self) -> bool:
        return bool(self.username or self.password)

    def supports_udp(self) -> bool:
        return self.scheme_or_default() == SOCKS5_SCHEME

    def basic_token(self) -> str:
        return base64.b64encode("{}:{}".format(self.username, self.password).encode("utf-8")).decode("ascii")


def normalize_scheme(value: str) -> str:
    text = (value or "").strip().lower()
    if text in ("", HTTP_SCHEME):
        return HTTP_SCHEME
    if text in ("socks5", "socks5h", "sock5"):
        return SOCKS5_SCHEME
    raise ValueError("unsupported proxy protocol; use http:// or socks5://")


def parse_line(line: str) -> Entry:
    """One proxy line -> :class:`Entry`.

    Supports ``host:port``, ``host:port:user:pass``, ``scheme://user:pass@host:port``,
    ``scheme://host:port:user:pass`` and bracketed IPv6.
    """
    text = (line or "").strip()
    scheme = HTTP_SCHEME
    rest = text
    marker = text.find("://")
    if marker >= 0 and not any(char in text[:marker] for char in ":@[]"):
        scheme = normalize_scheme(text[:marker])
        rest = text[marker + 3 :]

    username = ""
    password = ""
    host = ""
    port = ""

    provider_style = False
    parts_check = rest.split(":", 3)
    if len(parts_check) == 4:
        provider_style = parts_check[1].isdigit()
    if rest.startswith("["):
        end = rest.find("]:")
        if end >= 0:
            provider_style = len(rest[end + 2 :].split(":", 3)) == 3

    if "@" in rest and not provider_style:
        try:
            parsed = urlparse(scheme + "://" + rest)
            host = parsed.hostname or ""
            port = str(parsed.port or "")
        except ValueError:
            raise ValueError("invalid proxy URL; URL-encode special characters in credentials") from None
        if parsed.path not in ("", "/") or parsed.query or parsed.fragment:
            raise ValueError("proxy URL must contain only credentials and host:port")
        if parsed.username is not None:
            username = unquote(parsed.username)
            password = unquote(parsed.password or "")
    elif rest.startswith("["):
        # Bracketed IPv6 with optional provider-style credentials.
        end = rest.find("]:")
        if end < 0:
            raise ValueError("expected [IPv6]:port")
        tail = rest[end + 2 :].split(":", 3)
        if len(tail) == 1:
            tail[0] = tail[0][: -1] if tail[0].endswith("/") else tail[0]
        host = rest[1:end]
        port = tail[0]
        if len(tail) == 3:
            username, password = tail[1], tail[2]
        elif len(tail) != 1:
            raise ValueError("expected host:port:user:pass")
    else:
        parts = rest.split(":", 3)
        if len(parts) == 2:
            parts[1] = parts[1][: -1] if parts[1].endswith("/") else parts[1]
        if len(parts) not in (2, 4):
            raise ValueError("expected host:port, host:port:user:pass or scheme://user:pass@host:port")
        host, port = parts[0], parts[1]
        if len(parts) == 4:
            username, password = parts[2], parts[3]

    try:
        port_number = int(port)
    except (TypeError, ValueError):
        raise ValueError("proxy port must be a number from 1 to 65535") from None
    if port_number < 1 or port_number > 65535:
        raise ValueError("proxy port must be a number from 1 to 65535")
    if host == "" or any(char in _HOST_FORBIDDEN for char in host):
        raise ValueError("invalid proxy host")
    if "\r" in username + password or "\n" in username + password:
        raise ValueError("invalid proxy credentials")
    has_auth = bool(username or password)
    if has_auth and (username == "" or (scheme == SOCKS5_SCHEME and password == "")):
        raise ValueError("proxy authentication requires both username and password")
    if scheme == SOCKS5_SCHEME and (len(username.encode("utf-8")) > 255 or len(password.encode("utf-8")) > 255):
        raise ValueError("SOCKS5 credentials must be at most 255 bytes each")
    return Entry(scheme=scheme, host=host, port=str(port_number), username=username, password=password)


def parse_list(raw: str) -> list[Entry]:
    """Every non-blank line -> an :class:`Entry`; the first bad line fails."""
    result: list[Entry] = []
    for index, line in enumerate((raw or "").replace("\r", "").split("\n")):
        if line.strip() == "":
            continue
        try:
            result.append(parse_line(line))
        except ValueError as error:
            raise ValueError("proxy line {}: {}".format(index + 1, error)) from None
    return result


# ------------------------------------------------------------------- rotator
class Rotator:
    """Round-robin over the configured lines, one proxy per fresh browser."""

    def __init__(self, entries: Iterable[Entry]):
        self._entries = list(entries)
        self._next = 0
        self._lock = threading.Lock()

    def next(self) -> Entry | None:
        with self._lock:
            if not self._entries:
                return None
            entry = self._entries[self._next % len(self._entries)]
            self._next += 1
            return entry

    def __len__(self) -> int:
        return len(self._entries)


# -------------------------------------------------------------------- tunnel
class Tunnel:
    """A connected proxy tunnel.

    Bytes the proxy already sent after its handshake answer are kept in
    ``_buffer`` so the first read never loses pipelined data.
    """

    def __init__(self, sock: socket.socket, buffer: bytes = b""):
        self._sock = sock
        self._buffer = buffer

    @property
    def buffered(self) -> int:
        return len(self._buffer)

    def take_buffer(self) -> bytes:
        data, self._buffer = self._buffer, b""
        return data

    def sendall(self, data: bytes) -> None:
        self._sock.sendall(data)

    def recv(self, size: int) -> bytes:
        if self._buffer:
            chunk = self._buffer[:size]
            self._buffer = self._buffer[size:]
            return chunk
        return self._sock.recv(size)

    def settimeout(self, value: float | None) -> None:
        self._sock.settimeout(value)

    def close(self) -> None:
        with contextlib.suppress(OSError):
            self._sock.close()

    def fileno(self) -> int:
        return self._sock.fileno()


def _connect_proxy(host: str, port: int, timeout: float) -> tuple[socket.socket, float]:
    try:
        sock = socket.create_connection((host, port), timeout=timeout)
    except OSError:
        raise TunnelError(
            "network", "proxy TCP connection failed (offline, DNS, firewall or timeout)"
        ) from None
    return sock, time.monotonic() + timeout


def _read_handshake_head(sock: socket.socket, limit: int = HANDSHAKE_LIMIT) -> tuple[bytes, bytes]:
    """Read up to and including ``\\r\\n\\r\\n``; returns (head, leftover)."""
    data = b""
    while b"\r\n\r\n" not in data:
        if len(data) >= limit:
            raise OSError("proxy handshake header is too large")
        chunk = sock.recv(4096)
        if not chunk:
            raise OSError("proxy closed the connection during the handshake")
        data += chunk
    head, _, leftover = data.partition(b"\r\n\r\n")
    return head + b"\r\n\r\n", leftover


def _read_provider_message(sock: socket.socket, buffer: bytes, entry: Entry) -> bytes:
    """Bounded (1.2 s) excerpt of the proxy's rejection body."""
    if buffer:
        return sanitize_provider_text(buffer.decode("utf-8", "replace"), entry, PROVIDER_MESSAGE_LIMIT).encode()
    try:
        sock.settimeout(1.2)
        chunk = sock.recv(512)
    except OSError:
        chunk = b""
    finally:
        with contextlib.suppress(OSError):
            sock.settimeout(None)
    return sanitize_provider_text(chunk.decode("utf-8", "replace"), entry, PROVIDER_MESSAGE_LIMIT).encode()


def sanitize_provider_text(raw: str, entry: Entry, limit: int) -> str:
    """Short, single-line, credential-free excerpt of whatever the proxy sent."""
    text = _HTML_TAG.sub(" ", raw or "")
    builder: list[str] = []
    space = False
    for char in text:
        code = ord(char)
        if code < 0x20 or code == 0x7F:
            char = " "
        if char.isspace():
            if space:
                continue
            space = True
            builder.append(" ")
            continue
        space = False
        builder.append(char)
    cleaned = "".join(builder).strip()
    secrets = [entry.username, entry.password]
    if entry.has_auth():
        secrets.append(entry.basic_token())
    for secret in secrets:
        if len(secret) >= 3:
            cleaned = cleaned.replace(secret, "***")
    cleaned = cleaned.strip()
    if len(cleaned) > limit:
        cleaned = cleaned[:limit].strip() + "..."
    return cleaned


def dial_tunnel(entry: Entry, target: str, timeout: float = DIAL_TIMEOUT_SECONDS) -> Tunnel:
    """Connect ONLY through ``entry``; target DNS is resolved upstream."""
    host, _, port_text = target.rpartition(":")
    if host == "":
        raise TunnelError("input", "invalid tunnel target")
    if not port_text.isdigit() or not (1 <= int(port_text) <= 65535):
        raise TunnelError("input", "invalid tunnel target port")
    if host.startswith("[") and host.endswith("]"):
        host = host[1:-1]

    sock, deadline = _connect_proxy(entry.host, int(entry.port), timeout)
    success = False
    try:
        remaining = max(0.5, deadline - time.monotonic())
        sock.settimeout(remaining)
        if entry.scheme_or_default() == HTTP_SCHEME:
            tunnel = _http_connect(entry, sock, target)
        else:
            tunnel = _socks5_connect(entry, sock, host, int(port_text))
        if time.monotonic() > deadline:
            raise TunnelError("canceled", "proxy test cancelled or timed out")
        tunnel.settimeout(None)
        success = True
        return tunnel
    finally:
        if not success:
            with contextlib.suppress(OSError):
                sock.close()


def _http_connect(entry: Entry, sock: socket.socket, target: str) -> Tunnel:
    request = ["CONNECT {} HTTP/1.1".format(target), "Host: {}".format(target)]
    if entry.has_auth():
        request.append("Proxy-Authorization: Basic " + entry.basic_token())
    request.extend(["Proxy-Connection: keep-alive", "", ""])
    try:
        sock.sendall("\r\n".join(request).encode("ascii"))
    except OSError:
        raise TunnelError("protocol", "HTTP proxy CONNECT write failed") from None
    try:
        head, leftover = _read_handshake_head(sock)
    except OSError:
        raise TunnelError("protocol", "HTTP proxy did not return a valid CONNECT response (protocol or timeout)") from None
    status_line = head.split(b"\r\n", 1)[0].decode("latin-1")
    fields = status_line.split(" ", 2)
    if len(fields) < 2 or not fields[1].isdigit():
        raise TunnelError("protocol", "HTTP proxy did not return a valid CONNECT response (protocol or timeout)")
    status = int(fields[1])
    if status != 200:
        proxy_auth = _header_value(head, "proxy-authenticate")
        detail = "HTTP proxy rejected CONNECT ({}): check provider permissions, IP allowlist and target access".format(status)
        if status == 407:
            detail = "HTTP proxy authentication failed (407): check username/password"
        raise TunnelError(
            HTTP_SCHEME,
            detail,
            status=status,
            proxy_auth=sanitize_provider_text(proxy_auth, entry, 80),
            provider_message=_read_provider_message(sock, leftover, entry).decode("utf-8", "replace"),
        )
    return Tunnel(sock, leftover)


def _header_value(head: bytes, name: str) -> str:
    for line in head.split(b"\r\n"):
        key, separator, value = line.partition(b":")
        if separator and key.decode("latin-1").strip().lower() == name:
            return value.decode("latin-1").strip()
    return ""


def _socks5_connect(entry: Entry, sock: socket.socket, host: str, port: int) -> Tunnel:
    methods = b"\x05\x01\x02" if entry.has_auth() else b"\x05\x01\x00"
    try:
        sock.sendall(methods)
        reply = _recv_exactly(sock, 2)
    except OSError:
        raise TunnelError("protocol", "SOCKS5 greeting failed") from None
    if len(reply) != 2 or reply[0] != 5:
        raise TunnelError("protocol", "invalid SOCKS5 response (protocol or timeout)")
    if reply[1] == 2 and entry.has_auth():
        username = entry.username.encode("utf-8")
        password = entry.password.encode("utf-8")
        auth = bytes([1, len(username)]) + username + bytes([len(password)]) + password
        try:
            sock.sendall(auth)
            reply = _recv_exactly(sock, 2)
        except OSError:
            raise TunnelError("protocol", "SOCKS5 authentication write failed") from None
        if len(reply) != 2 or reply[0] != 1 or reply[1] != 0:
            raise TunnelError("socks", "SOCKS5 authentication failed: check username/password")
    elif reply[1] != 0 or entry.has_auth():
        raise TunnelError("protocol", "SOCKS5 proxy rejected the configured authentication method")

    request = bytearray([5, 1, 0])
    packed = _pack_address(host)
    if packed is None:
        if len(host) > 255:
            raise TunnelError("input", "SOCKS5 target hostname too long")
        request += bytes([3, len(host)]) + host.encode("utf-8")
    else:
        request += packed
    request += struct.pack("!H", port)
    try:
        sock.sendall(bytes(request))
        header = _recv_exactly(sock, 4)
    except OSError:
        raise TunnelError("protocol", "SOCKS5 CONNECT write failed") from None
    if len(header) != 4 or header[0] != 5 or header[2] != 0:
        raise TunnelError("protocol", "invalid SOCKS5 CONNECT response")
    if header[1] != 0:
        raise TunnelError(
            "socks",
            "SOCKS5 CONNECT rejected (code {}): target/provider unavailable".format(header[1]),
            provider_message="SOCKS5 reply code {}".format(header[1]),
        )
    length = {1: 4, 4: 16}.get(header[3])
    if length is None:
        if header[3] != 3:
            raise TunnelError("protocol", "invalid SOCKS5 address type")
        length = _recv_exactly(sock, 1)[0]
    try:
        _recv_exactly(sock, length + 2)
    except OSError:
        raise TunnelError("protocol", "incomplete SOCKS5 response") from None
    return Tunnel(sock)


def _recv_exactly(sock: socket.socket, size: int) -> bytes:
    data = b""
    while len(data) < size:
        chunk = sock.recv(size - len(data))
        if not chunk:
            raise OSError("connection closed")
        data += chunk
    return data


def _pack_address(host: str) -> bytes | None:
    try:
        packed = socket.inet_pton(socket.AF_INET, host)
        return bytes([1]) + packed
    except OSError:
        pass
    try:
        packed = socket.inet_pton(socket.AF_INET6, host)
        return bytes([4]) + packed
    except OSError:
        return None


# ----------------------------------------------------------------- tls/stream
class TlsTunnel:
    """TLS over a :class:`Tunnel`, driven with memory BIOs.

    Python's ``ssl`` needs a real socket for ``wrap_socket``; a BIO pair keeps
    the handshake bytes flowing through any transport and never loses bytes the
    proxy already sent.
    """

    def __init__(self, tunnel: Tunnel, host: str, context: ssl.SSLContext | None = None):
        self._tunnel = tunnel
        self._incoming = ssl.MemoryBIO()
        self._outgoing = ssl.MemoryBIO()
        self._context = context or ssl.create_default_context()
        self._ssl = self._context.wrap_bio(self._incoming, self._outgoing, server_hostname=host)
        self._drive(self._ssl.do_handshake)

    def _flush(self) -> None:
        data = self._outgoing.read()
        if data:
            self._tunnel.sendall(data)

    def _pump(self) -> None:
        chunk = self._tunnel.recv(65536)
        if not chunk:
            raise OSError("tunnel closed during TLS")
        self._incoming.write(chunk)

    def _drive(self, operation, *args):
        while True:
            try:
                result = operation(*args)
                self._flush()
                return result
            except ssl.SSLWantReadError:
                self._flush()
                self._pump()
            except ssl.SSLWantWriteError:
                self._flush()

    def sendall(self, data: bytes) -> None:
        self._drive(self._ssl.write, data)

    def recv(self, size: int) -> bytes:
        return self._drive(self._ssl.read, size)

    def close(self) -> None:
        self._tunnel.close()


# --------------------------------------------------------------------- tests
@dataclass
class TestResult:
    line: int = 0
    label: str = ""
    ok: bool = False
    tunnel_ok: bool = False
    status: int = 0
    latency_ms: int = 0
    detail: str = ""
    hint: str = ""
    provider_message: str = ""
    proxy_authenticate: str = ""
    authenticated: bool = False

    def to_dict(self) -> dict:
        payload = {
            "line": self.line,
            "label": self.label,
            "ok": self.ok,
            "tunnel_ok": self.tunnel_ok,
            "status": self.status,
            "latency_ms": self.latency_ms,
            "detail": self.detail,
            "authenticated": self.authenticated,
        }
        for key in ("hint", "provider_message", "proxy_authenticate"):
            value = getattr(self, key)
            if value:
                payload[key] = value
        return payload


def hint_for(entry: Entry, status: int, snippet: str, network_error: bool) -> str:
    """One plain-language next step, derived only from the actual answer."""
    lower = (snippet or "").lower()
    if status == 407:
        if not entry.has_auth():
            return "This line has no username:password. Use host:port:user:pass or allow your IP with the provider."
        return "The proxy rejected the credentials. Re-copy username and password for this exact endpoint (a rotating/backbone username differs from a direct one)."
    if status == 402:
        if not entry.has_auth():
            return "The proxy answered 402 for a line without credentials. Add user:pass (host:port:user:pass) or authorize this machine's IP with the provider."
        if any(word in lower for word in ("authenticat", "credential", "password", "username")):
            return "The proxy answered 402 and its own message points at authentication. Re-copy the username/password that belong to this exact endpoint."
        return (
            "402 is a provider account/entitlement answer: expired plan, exhausted bandwidth, or credentials "
            "that do not match this endpoint (e.g. a direct-connection login used against the rotating endpoint)."
        )
    if status == 403:
        return "The proxy refused this destination. Some providers block specific sites on some proxy types; test another proxy or another proxy plan."
    if status == 429:
        return "Provider rate/concurrency limit reached. Wait, or use fewer parallel browsers through this proxy."
    if status >= 500:
        return "The provider had trouble reaching the destination. Retry or pick another proxy from the list."
    if status != 0:
        return "The proxy answered HTTP {}. Test the same line with curl or a browser to confirm the provider side.".format(status)
    if network_error:
        return "No HTTP answer at all: wrong host/port, blocked local network, or the proxy is offline."
    return ""


def test_endpoint(
    entry: Entry,
    endpoint: str = TEST_ENDPOINT,
    timeout: float = DIAL_TIMEOUT_SECONDS,
    context: ssl.SSLContext | None = None,
) -> TestResult:
    """HEAD request through one proxy. Never saves, rotates or logs in."""
    result = TestResult(label=entry.label(), authenticated=entry.has_auth())
    started = time.monotonic()
    try:
        parsed = urlparse(endpoint)
        if parsed.scheme != "https" or not parsed.hostname:
            result.detail = "invalid HTTPS test endpoint"
            return result
        port = parsed.port or 443
        target = "{}:{}".format(parsed.hostname, port)

        try:
            tunnel = dial_tunnel(entry, target, timeout=timeout)
        except TunnelError as error:
            result.detail = error.detail
            result.status = error.status
            result.provider_message = error.provider_message
            result.proxy_authenticate = error.proxy_auth
            result.hint = hint_for(entry, error.status, error.provider_message, error.kind == "network")
            return result

        try:
            try:
                secure = TlsTunnel(tunnel, parsed.hostname, context)
            except (ssl.SSLError, OSError):
                result.detail = "proxy tunnel opened, but destination TLS verification/handshake failed"
                result.hint = (
                    "The tunnel opened, so the credentials work. The HTTPS handshake failed: re-test in a "
                    "moment, or the destination may be intercepted/blocked for this proxy."
                )
                return result
            result.tunnel_ok = True
            path = parsed.path or "/"
            if parsed.query:
                path += "?" + parsed.query
            request = (
                "HEAD {} HTTP/1.1\r\nHost: {}\r\nUser-Agent: ChitChat-Proxy-Test/1.0\r\n"
                "Accept: */*\r\nConnection: close\r\n\r\n".format(path, parsed.netloc)
            )
            try:
                secure.sendall(request.encode("ascii"))
                head = _read_until(secure, b"\r\n\r\n", 64 << 10)
            except (OSError, ssl.SSLError):
                result.detail = "tunnel/TLS succeeded; test request failed"
                return result
            status_line = head.split(b"\r\n", 1)[0].decode("latin-1")
            fields = status_line.split(" ", 2)
            if len(fields) < 2 or not fields[1].isdigit():
                result.detail = "tunnel/TLS succeeded; destination response failed or timed out"
                return result
            result.status = int(fields[1])
            result.ok = 200 <= result.status < 400
            if result.ok:
                result.detail = "PASS: proxy authentication, HTTPS tunnel and destination response succeeded"
            else:
                result.detail = "proxy tunnel/TLS works, but destination returned HTTP {} (site/provider access restriction)".format(
                    result.status
                )
                result.hint = hint_for(Entry(), result.status, "", False)
            return result
        finally:
            tunnel.close()
    finally:
        result.latency_ms = int((time.monotonic() - started) * 1000)


def _read_until(stream, marker: bytes, limit: int) -> bytes:
    data = b""
    while marker not in data:
        if len(data) >= limit:
            raise OSError("response head is too large")
        chunk = stream.recv(4096)
        if not chunk:
            break
        data += chunk
    return data


def test_list(raw: str, endpoint: str = TEST_ENDPOINT, *, timeout: float = 12.0, workers: int = 4) -> list[TestResult]:
    """Test every line in parallel (max 20). Errors never include credentials."""
    if len((raw or "").encode("utf-8", "replace")) > MAX_TEST_RAW_BYTES:
        raise ValueError("proxy list is too large (maximum 64 KiB)")
    results: list[TestResult] = []
    entries: list[Entry] = []
    for index, line in enumerate((raw or "").replace("\r", "").split("\n")):
        if line.strip() == "":
            continue
        if len(results) >= MAX_TEST_ENTRIES:
            raise ValueError("test at most {} proxy lines at a time".format(MAX_TEST_ENTRIES))
        result = TestResult(line=index + 1)
        try:
            entry = parse_line(line)
            result.label = entry.label()
        except ValueError as error:
            entry = Entry()
            result.detail = str(error)
        results.append(result)
        entries.append(entry)
    if not results:
        raise ValueError("enter at least one proxy to test")

    pending = [index for index, result in enumerate(results) if not result.detail]
    lock = threading.Lock()
    queue = list(pending)

    def worker() -> None:
        while True:
            with lock:
                if not queue:
                    return
                position = queue.pop(0)
            tested = test_endpoint(entries[position], endpoint, timeout=timeout)
            tested.line = results[position].line
            results[position] = tested

    threads = [threading.Thread(target=worker, name="proxy-test", daemon=True) for _ in range(max(1, min(workers, len(pending) or 1)))]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return results
