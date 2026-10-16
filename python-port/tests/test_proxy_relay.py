"""End-to-end proxy tests with local fake proxies (no internet needed).

Runs the real relay, the real tunnel handshakes and the real diagnostics
against in-process HTTP and SOCKS5 proxies, so credential injection, fail-closed
behaviour, remote DNS and the 407/network hints are all proven on this machine.
"""

from __future__ import annotations

import base64
import contextlib
import select
import socket
import ssl
import tempfile
import threading
import time
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from chitchatport.proxy import Entry, TunnelError, dial_tunnel, test_endpoint
from chitchatport.proxyrelay import Relay

# Throwaway self-signed certificate for 127.0.0.1, generated for these tests only.
# It is not a secret and it is not used anywhere outside this test process.
TEST_CERT = """-----BEGIN CERTIFICATE-----
MIIDJTCCAg2gAwIBAgIUX9RRgeEEoXvx2ArH2v3CB7K7HPMwDQYJKoZIhvcNAQEL
BQAwFDESMBAGA1UEAwwJMTI3LjAuMC4xMB4XDTI2MTAxNTA5MjQ1M1oXDTM2MTAx
MjA5MjQ1M1owFDESMBAGA1UEAwwJMTI3LjAuMC4xMIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAzLGsSuYec6J4ZZJfrf9Evyt4aqJq7fOi2KQaBoJtvwwc
r+lydKXchBWQ7HWu9slpm9RSWTWmHfx7iewbKxrh3bnU9AhUqODiRJdMg57Oife6
hDHLPv+Kbuc8Zj4yIKWyQST+rbhi0ocDbrckZ/XefbOQ2oxHmjPpMN0ACu8kfPEU
5FdT01gtUHedVH5Z3+Vl6edN+rhdqyPr4LH76ACgSRjCgFZykAwechUajsvOd7F1
wUoSi6adAn2vJWyL5tptcRAz/9Cr4wEro5ECBVrl9P9R92I9QvoytT8zZHqfJMnl
uhZlUzDUbjAak++N8oFZOsRcf/sAIuOjQGTqV/D1HwIDAQABo28wbTAdBgNVHQ4E
FgQU0V1rqIFVvtNNCLqhxRpl3SkxIAAwHwYDVR0jBBgwFoAU0V1rqIFVvtNNCLqh
xRpl3SkxIAAwGgYDVR0RBBMwEYIJbG9jYWxob3N0hwR/AAABMA8GA1UdEwEB/wQF
MAMBAf8wDQYJKoZIhvcNAQELBQADggEBAHBEuVJtUzpdMMqTZFsHrMYGKDMUdVSh
e+CcIFOxoWfTqmOqm6lFNUSNQ7WLcmmKeoft2EzXT0/emudWOwyNpOnAXcaqt8ru
QBqRNPhSDYetZT6hAja3y1qnQRvQoVmEcdQAf2tY3Se1rPrAvYNjtxDEEmNwmyiD
E45nUgP82eQz45fC8UmFpxuUZMQDEpiX6S4+ixhehySylXqeGvvjPw2IWnGozK5X
/jTukxQLV9s1NW6xYdnsQ4YAxkFDVgprZMM0UQTj2q04iimnNWuaBEgP7QOzYM0L
PrS4RtkWCTvzHiNGjZaLYDKrQmKGtvPGSLj1PyQg3QhHSUGFmXV0J4g=
-----END CERTIFICATE-----
"""

TEST_KEY = """-----BEGIN PRIVATE KEY-----
MIIEvgIBADANBgkqhkiG9w0BAQEFAASCBKgwggSkAgEAAoIBAQDMsaxK5h5zonhl
kl+t/0S/K3hqomrt86LYpBoGgm2/DByv6XJ0pdyEFZDsda72yWmb1FJZNaYd/HuJ
7BsrGuHdudT0CFSo4OJEl0yDns6J97qEMcs+/4pu5zxmPjIgpbJBJP6tuGLShwNu
tyRn9d59s5DajEeaM+kw3QAK7yR88RTkV1PTWC1Qd51Uflnf5WXp5036uF2rI+vg
sfvoAKBJGMKAVnKQDB5yFRqOy853sXXBShKLpp0Cfa8lbIvm2m1xEDP/0KvjASuj
kQIFWuX0/1H3Yj1C+jK1PzNkep8kyeW6FmVTMNRuMBqT743ygVk6xFx/+wAi46NA
ZOpX8PUfAgMBAAECggEAAj+xdOEyVHLkf7kr8+1qA++mUwWVxyd2Eos6/fdQ3Vh0
uDoIV9+6nHjgLrjcQznTPzMF9JTyTkIH2WwOoFqbQKRaApRU2cYetkIzRP7ppfpi
wjEJ2pdUHgPMVhM42cL4Ql71rVLpQwbYss6wR7QbzaW7YatI3hjT5wWGipg1/mli
lX6R/5HHyn21jJTzBy4FbqA3RI8uNW1ugrJ1sFjwL/gOObNsWqg79kxRvjl4aa+6
3fG8WF8Fvwm44qxkuI/tLWRqMAYBW0C0r3qs0Ix9CodkfsrUAEt2SoFF3e/HTV9X
IhTKvFf98H25l1i41OG1pRXHQZLkIwEcPGQBcBucgQKBgQD4QOIxk+wye32na4ZV
b+FyOVpfVcRNR5+DfUN8Rz22K0ygRdJZR/3Gj+ib5doNfY3pxnrZy9iCPZbTIAFq
JqD0XCilSbAn8VZZGmf4gtC2AaORdEPAp0prHruIuJOSFvcRLKr8vUTyX/mInxdK
kVq3bTAIdH2f56vmf1Kiyx5xwwKBgQDTFNMuSXS9lHFpt7mUCT4LoyogJYFNVAAp
w8w6XPuc/mwQiZMDNVuiXGKGSe9k1Rw9KYdL6UY9l5Rl1gfKp9EIQdl8ERSloUoi
HUx8/GM41VZo+tOJuFJqcbELqeUwM0ix6eQByCbN+P/JCsciGSLjp2Jt4A9BsNGb
gmhiiC69dQKBgQDAjRLkgWdigIYlpTC8c9rZTcLazxtDFZqT0kTV0Cv9b/gvF/jf
ZwdfS5+Py+glLo00Fc6Tv5he7IZfmyN9whh1cEXWbiFD2dyvn9uk1VIs2XiVCSjD
9P2zuFKc6Q0M8uMPXyYAgo1HsUlKvprCK5vBSd9Hr5MAxCCA16Cww/sJIQKBgGik
yFUIIZHix57rcFsNge7vAvFQ5ptZa4NJCDRe6HZaQ2Snb3SFvVnuhwGgIchRRmCj
9+kQSrw6K12ZD9/N9nkrfBBfv2alYUUsVUEZNm90hBQjVT/PWzxeBLGNlougwQdC
tsCTY3l1WglnCHXFFGDcGJX0IFwmtqNrsSg370QJAoGBAMD7kfYAdD3zcAmmU50I
H8LSbOSomYqaLG9aGZ6CTRwboe/SUma/AKifFHuGVQdmuTroEV8aa9cDZ0ZCvcXv
wg2w6yvOGQFqcpsuxZytgMnRlEpe5+6ltuUu+aSrLeFJw5LIaU0xZO6xlBGppZHV
/fVyXn3dWOAmKKcxs/D/iImp
-----END PRIVATE KEY-----
"""


def _pump(left: socket.socket, right: socket.socket, timeout: float = 5.0) -> None:
    while True:
        readable, _, _ = select.select([left, right], [], [], timeout)
        if not readable:
            return
        for source in readable:
            try:
                data = source.recv(65536)
            except OSError:
                return
            if not data:
                return
            destination = right if source is left else left
            try:
                destination.sendall(data)
            except OSError:
                return


def _read_head(sock: socket.socket, limit: int = 65536) -> bytes:
    data = b""
    while b"\r\n\r\n" not in data:
        chunk = sock.recv(4096)
        if not chunk:
            raise OSError("closed")
        data += chunk
        if len(data) > limit:
            raise OSError("too large")
    return data.split(b"\r\n\r\n", 1)[0].decode("latin-1")


def _headers_of(head: str) -> dict[str, str]:
    fields = {}
    for line in head.split("\r\n")[1:]:
        name, _, value = line.partition(":")
        if name:
            fields[name.strip().lower()] = value.strip()
    return fields


class _FakeServer:
    """A threaded TCP server that runs one handler per connection."""

    def __init__(self):
        self._listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self._listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self._listener.bind(("127.0.0.1", 0))
        self._listener.listen(32)
        self._listener.settimeout(1.0)
        self.port = self._listener.getsockname()[1]
        self.requests: list[dict] = []
        self.connections = 0
        self._lock = threading.Lock()
        self._closed = False
        self._thread = threading.Thread(target=self._accept, daemon=True)

    def start(self):
        self._thread.start()
        return self

    def close(self):
        self._closed = True
        with contextlib.suppress(OSError):
            self._listener.close()
        # A thread blocked in accept() keeps the fd alive until the call returns,
        # so wake it up (and drop the throwaway connection) before returning.
        with contextlib.suppress(OSError):
            socket.create_connection(("127.0.0.1", self.port), timeout=0.2).close()
        self._thread.join(timeout=3)

    def _accept(self):
        while not self._closed:
            try:
                connection, _ = self._listener.accept()
            except (socket.timeout, OSError):
                continue
            with self._lock:
                self.connections += 1
            threading.Thread(target=self._serve, args=(connection,), daemon=True).start()

    def record(self, payload: dict) -> dict:
        with self._lock:
            self.requests.append(payload)
        return payload

    def _serve(self, connection: socket.socket) -> None:  # pragma: no cover - overridden
        connection.close()


def _wait_until_closed(port: int, timeout: float = 5.0) -> bool:
    """True once nothing listens on the port any more."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            socket.create_connection(("127.0.0.1", port), timeout=0.3).close()
        except OSError:
            return True
        time.sleep(0.05)
    return False


class FakeHttpProxy(_FakeServer):
    """Tiny HTTP proxy: optional authentication, CONNECT tunnels, absolute GETs."""

    def __init__(self, *, require_auth: tuple[str, str] | None = None, reject_status: int = 0):
        super().__init__()
        self.require_auth = require_auth
        self.reject_status = reject_status
        self.response_body = b"canned-body"

    def _expected_basic(self) -> str:
        user, password = self.require_auth or ("", "")
        return "Basic " + base64.b64encode("{}:{}".format(user, password).encode()).decode()

    def _serve(self, connection: socket.socket) -> None:
        try:
            connection.settimeout(10)
            head = _read_head(connection)
            request_line = head.split("\r\n", 1)[0]
            headers = _headers_of(head)
            method, target = (request_line.split(" ") + ["", ""])[:2]
            self.record({"method": method, "target": target, "headers": headers})

            if self.reject_status:
                body = b"<html>Auth <b>Required</b> for this plan</html>"
                connection.sendall(
                    (
                        "HTTP/1.1 {} Proxy Authentication Required\r\n".format(self.reject_status)
                        + 'Proxy-Authenticate: Basic realm="provider"\r\n'
                        + "Content-Length: {}\r\nConnection: close\r\n\r\n".format(len(body))
                    ).encode()
                    + body
                )
                return

            if self.require_auth and headers.get("proxy-authorization") != self._expected_basic():
                connection.sendall(
                    b"HTTP/1.1 407 Proxy Authentication Required\r\n"
                    b'Proxy-Authenticate: Basic realm="provider"\r\n'
                    b"Content-Length: 0\r\nConnection: close\r\n\r\n"
                )
                return

            if method.upper() == "CONNECT":
                host, _, port = target.rpartition(":")
                remote = socket.create_connection((host, int(port)), timeout=5)
                connection.sendall(b"HTTP/1.1 200 Connection Established\r\n\r\n")
                try:
                    _pump(connection, remote)
                finally:
                    with contextlib.suppress(OSError):
                        remote.close()
                return

            body = self.response_body
            connection.sendall(
                (
                    "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n"
                    "Content-Length: {}\r\nConnection: close\r\n\r\n".format(len(body))
                ).encode()
                + body
            )
        except OSError:
            return
        finally:
            with contextlib.suppress(OSError):
                connection.close()


class FakeSocks5Proxy(_FakeServer):
    """Tiny SOCKS5 proxy that records the handshake and the target address type."""

    def __init__(self, *, username: str | None = None, password: str = ""):
        super().__init__()
        self.username = username
        self.password = password
        self.auth_seen: list[tuple[str, str]] = []
        self.targets: list[tuple[int, str, int]] = []

    def _serve(self, connection: socket.socket) -> None:
        try:
            connection.settimeout(10)
            greeting = connection.recv(3)
            if len(greeting) != 3 or greeting[0] != 5:
                return
            if self.username is not None:
                connection.sendall(b"\x05\x02")  # username/password
                version, user_length = connection.recv(2)
                user = connection.recv(user_length).decode()
                password_length = connection.recv(1)[0]
                password = connection.recv(password_length).decode()
                self.auth_seen.append((user, password))
                if user != self.username or password != self.password:
                    connection.sendall(b"\x01\x01")
                    return
                connection.sendall(b"\x01\x00")
            else:
                connection.sendall(b"\x05\x00")

            header = connection.recv(4)
            address_type = header[3]
            if address_type == 1:
                host = socket.inet_ntoa(connection.recv(4))
            elif address_type == 3:
                length = connection.recv(1)[0]
                host = connection.recv(length).decode()
            else:
                connection.sendall(b"\x05\x08\x00\x01\x00\x00\x00\x00\x00\x00")
                return
            port = int.from_bytes(connection.recv(2), "big")
            self.targets.append((address_type, host, port))

            remote = socket.create_connection((host, port), timeout=5)
            connection.sendall(b"\x05\x00\x00\x01\x00\x00\x00\x00\x00\x00")
            try:
                _pump(connection, remote)
            finally:
                with contextlib.suppress(OSError):
                    remote.close()
        except OSError:
            return
        finally:
            with contextlib.suppress(OSError):
                connection.close()


class _TlsDestination:
    """HTTPS destination that answers HEAD / with 200 using the test certificate."""

    def __init__(self):
        self.hits = 0
        self._context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        self._context.load_cert_chain(_write_pem(TEST_CERT), _write_pem(TEST_KEY))
        destination = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_HEAD(self):  # noqa: N802
                destination.hits += 1
                self.send_response(200)
                self.send_header("Content-Length", "0")
                self.end_headers()

            def do_GET(self):  # noqa: N802
                destination.hits += 1
                body = b"ok"
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, fmt, *args):
                return

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._server.daemon_threads = True
        self._server.socket = self._context.wrap_socket(self._server.socket, server_side=True)
        self.port = self._server.server_address[1]
        self._thread = threading.Thread(target=self._server.serve_forever, daemon=True)

    def start(self):
        self._thread.start()
        return self

    def close(self):
        with contextlib.suppress(Exception):
            self._server.shutdown()
        with contextlib.suppress(Exception):
            self._server.server_close()

    @property
    def context(self) -> ssl.SSLContext:
        return ssl.create_default_context(cadata=TEST_CERT)


_PEM_FILES: dict[str, str] = {}


def _write_pem(text: str) -> str:
    """Write the throwaway certificate once, into this machine's temp folder.

    tempfile, not "/tmp": a leading-slash path means <current drive>:\tmp on
    Windows, which does not exist on a fresh machine - the relay tests then fail
    on a Windows PC even though nothing is wrong with the code.
    """
    if text not in _PEM_FILES:
        folder = Path(tempfile.mkdtemp(prefix="chitchat-relay-pem-"))
        path = folder / "chitchat-test-{}.pem".format(len(_PEM_FILES))
        path.write_text(text, encoding="ascii")
        _PEM_FILES[text] = str(path)
    return _PEM_FILES[text]


class RelayTests(unittest.TestCase):
    def setUp(self):
        self.destination = _TlsDestination().start()
        self.addCleanup(self.destination.close)
        self.cleanups: list = []

    def tearDown(self):
        for item in reversed(self.cleanups):
            with contextlib.suppress(Exception):
                item()

    def _relay(self, upstream: Entry) -> Relay:
        relay = Relay(upstream).start()
        self.cleanups.append(relay.close)
        return relay

    def _client(self, relay: Relay) -> socket.socket:
        sock = socket.create_connection(("127.0.0.1", int(relay.entry.port)), timeout=5)
        self.cleanups.append(sock.close)
        return sock

    def test_relay_local_entry_has_no_credentials(self):
        proxy = FakeHttpProxy(require_auth=("alice", "s3cret")).start()
        self.cleanups.append(proxy.close)
        relay = self._relay(Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="s3cret"))
        self.assertEqual(relay.browser_url, "http://127.0.0.1:" + relay.entry.port)
        self.assertNotIn("s3cret", relay.browser_url)
        self.assertNotIn("alice", relay.browser_url)

    def test_credentials_reach_the_proxy_and_the_tunnel_carries_tls(self):
        proxy = FakeHttpProxy(require_auth=("alice", "s3cret")).start()
        self.cleanups.append(proxy.close)
        relay = self._relay(Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="s3cret"))

        client = self._client(relay)
        target = "127.0.0.1:{}".format(self.destination.port)
        client.sendall("CONNECT {} HTTP/1.1\r\nHost: {}\r\n\r\n".format(target, target).encode())
        answer = client.recv(4096)
        self.assertTrue(answer.startswith(b"HTTP/1.1 200"), answer)

        secure = self.destination.context.wrap_socket(client, server_hostname="127.0.0.1")
        secure.sendall(b"HEAD / HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
        self.assertIn(b"200", secure.recv(4096))
        secure.close()

        first = proxy.requests[0]
        self.assertEqual(first["method"], "CONNECT")
        self.assertEqual(first["target"], target)
        self.assertEqual(first["headers"]["proxy-authorization"], proxy._expected_basic())
        self.assertEqual(self.destination.hits, 1)

    def test_relay_never_falls_back_to_a_direct_connection(self):
        dead = FakeHttpProxy().start()
        dead_port = dead.port
        dead.close()
        self.assertTrue(_wait_until_closed(dead_port))
        relay = self._relay(Entry(host="127.0.0.1", port=str(dead_port)))
        client = self._client(relay)
        target = "127.0.0.1:{}".format(self.destination.port)
        client.sendall("CONNECT {} HTTP/1.1\r\nHost: {}\r\n\r\n".format(target, target).encode())
        answer = client.recv(4096).decode("latin-1", "replace")
        self.assertIn("502", answer)
        self.assertEqual(self.destination.hits, 0, "relay must never reach the destination without the proxy")

    def test_relay_forwards_plain_http_through_the_authenticated_proxy(self):
        proxy = FakeHttpProxy(require_auth=("alice", "s3cret")).start()
        self.cleanups.append(proxy.close)
        relay = self._relay(Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="s3cret"))
        client = self._client(relay)
        client.sendall(b"GET http://example.test/hello HTTP/1.1\r\nHost: example.test\r\nConnection: close\r\n\r\n")
        answer = b""
        while True:
            chunk = client.recv(4096)
            if not chunk:
                break
            answer += chunk
        self.assertIn(b"200 OK", answer)
        self.assertIn(b"canned-body", answer)
        request = proxy.requests[0]
        self.assertEqual(request["method"], "GET")
        self.assertEqual(request["target"], "http://example.test/hello", "absolute form is required upstream")
        self.assertEqual(request["headers"]["proxy-authorization"], proxy._expected_basic())

    def test_relay_without_credentials_still_works_through_an_open_proxy(self):
        proxy = FakeHttpProxy().start()
        self.cleanups.append(proxy.close)
        relay = self._relay(Entry(host="127.0.0.1", port=str(proxy.port)))
        client = self._client(relay)
        client.sendall(b"GET http://example.test/x HTTP/1.1\r\nHost: example.test\r\n\r\n")
        self.assertIn(b"canned-body", client.recv(4096))
        self.assertNotIn("proxy-authorization", proxy.requests[0]["headers"])

    def test_socks5_upstream_authenticates_and_resolves_remotely(self):
        socks = FakeSocks5Proxy(username="user", password="pass").start()
        self.cleanups.append(socks.close)
        relay = self._relay(Entry(scheme="socks5", host="127.0.0.1", port=str(socks.port), username="user", password="pass"))
        client = self._client(relay)
        host = "localhost"  # a name, not an IP: it must be sent to the proxy, not resolved here
        target = "{}:{}".format(host, self.destination.port)
        client.sendall("CONNECT {} HTTP/1.1\r\nHost: {}\r\n\r\n".format(target, target).encode())
        self.assertTrue(client.recv(4096).startswith(b"HTTP/1.1 200"))
        secure = self.destination.context.wrap_socket(client, server_hostname="localhost")
        secure.sendall(b"HEAD / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        self.assertIn(b"200", secure.recv(4096))
        secure.close()
        self.assertEqual(socks.auth_seen, [("user", "pass")])
        address_type, host_seen, port_seen = socks.targets[0]
        self.assertEqual(address_type, 3, "domain names must be forwarded for upstream DNS")
        self.assertEqual(host_seen, "localhost")
        self.assertEqual(port_seen, self.destination.port)

    def test_socks5_rejects_wrong_credentials(self):
        socks = FakeSocks5Proxy(username="user", password="pass").start()
        self.cleanups.append(socks.close)
        with self.assertRaises(TunnelError) as caught:
            dial_tunnel(Entry(scheme="socks5", host="127.0.0.1", port=str(socks.port), username="user", password="wrong"), "example.test:443", timeout=5)
        self.assertEqual(caught.exception.kind, "socks")
        self.assertIn("authentication failed", str(caught.exception))

    def test_relay_close_stops_accepting_and_drops_tunnels(self):
        proxy = FakeHttpProxy().start()
        self.cleanups.append(proxy.close)
        relay = Relay(Entry(host="127.0.0.1", port=str(proxy.port))).start()
        port = int(relay.entry.port)
        client = socket.create_connection(("127.0.0.1", port), timeout=5)
        self.cleanups.append(client.close)
        target = "127.0.0.1:{}".format(self.destination.port)
        client.sendall("CONNECT {} HTTP/1.1\r\n\r\n".format(target).encode())
        self.assertTrue(client.recv(4096).startswith(b"HTTP/1.1 200"))
        relay.close()
        self.assertTrue(relay.closed)
        deadline = time.time() + 3
        closed = False
        while time.time() < deadline and not closed:
            try:
                closed = client.recv(4096) == b""
            except OSError:
                closed = True
            time.sleep(0.05)
        self.assertTrue(closed, "closing the relay must drop the tunnel")
        with self.assertRaises(OSError):
            socket.create_connection(("127.0.0.1", port), timeout=1)


class DiagnosticsTests(unittest.TestCase):
    def setUp(self):
        self.destination = _TlsDestination().start()
        self.addCleanup(self.destination.close)
        self.cleanups: list = []

    def tearDown(self):
        for item in reversed(self.cleanups):
            with contextlib.suppress(Exception):
                item()

    def _endpoint(self) -> str:
        return "https://127.0.0.1:{}/".format(self.destination.port)

    def test_successful_line_reports_pass(self):
        proxy = FakeHttpProxy(require_auth=("alice", "s3cret")).start()
        self.cleanups.append(proxy.close)
        entry = Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="s3cret")
        result = test_endpoint(entry, self._endpoint(), context=self.destination.context)
        self.assertTrue(result.ok, result.detail)
        self.assertTrue(result.tunnel_ok)
        self.assertTrue(result.authenticated)
        self.assertEqual(result.status, 200)
        self.assertTrue(result.detail.startswith("PASS"))
        self.assertNotIn("s3cret", result.label)
        self.assertGreaterEqual(result.latency_ms, 0)

    def test_407_reports_the_credentials_hint_and_the_provider_words(self):
        proxy = FakeHttpProxy(reject_status=407).start()
        self.cleanups.append(proxy.close)
        entry = Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="s3cret")
        result = test_endpoint(entry, self._endpoint(), context=self.destination.context)
        self.assertFalse(result.ok)
        self.assertEqual(result.status, 407)
        self.assertIn("407", result.detail)
        self.assertIn("check username/password", result.detail)
        self.assertIn("rejected the credentials", result.hint)
        self.assertIn("provider", result.proxy_authenticate)
        self.assertNotIn("s3cret", result.provider_message)

    def test_401_style_status_from_provider_is_sanitized(self):
        proxy = FakeHttpProxy(reject_status=402).start()
        self.cleanups.append(proxy.close)
        entry = Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="s3cret")
        result = test_endpoint(entry, self._endpoint(), context=self.destination.context)
        self.assertEqual(result.status, 402)
        self.assertNotIn("<", result.provider_message, "HTML must be stripped")
        self.assertTrue(result.hint)

    def test_dead_proxy_reports_a_network_hint(self):
        proxy = FakeHttpProxy().start()
        port = proxy.port
        proxy.close()
        self.assertTrue(_wait_until_closed(port))
        result = test_endpoint(Entry(host="127.0.0.1", port=str(port)), self._endpoint(), timeout=2)
        self.assertEqual(result.status, 0)
        self.assertIn("No HTTP answer at all", result.hint)

    def test_proxy_that_answers_but_wrong_credentials_is_rejected(self):
        proxy = FakeHttpProxy(require_auth=("alice", "s3cret")).start()
        self.cleanups.append(proxy.close)
        entry = Entry(host="127.0.0.1", port=str(proxy.port), username="alice", password="wrong")
        result = test_endpoint(entry, self._endpoint(), context=self.destination.context)
        self.assertFalse(result.ok)
        self.assertEqual(result.status, 407)

    def test_tls_failure_after_a_good_tunnel_is_reported(self):
        proxy = FakeHttpProxy().start()
        self.cleanups.append(proxy.close)
        entry = Entry(host="127.0.0.1", port=str(proxy.port))
        # Self-signed destination, verified against the system store: the tunnel opens, TLS must fail.
        result = test_endpoint(entry, self._endpoint())
        self.assertFalse(result.ok)
        self.assertFalse(result.tunnel_ok)
        self.assertIn("TLS", result.detail)


if __name__ == "__main__":
    unittest.main()
