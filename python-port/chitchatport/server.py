"""Local dashboard server: stdlib HTTP + JSON API + static UI.

This is the "another method" dashboard: instead of a native WebView window, the
app starts a loopback-only HTTP server and the dashboard runs in the user's own
browser (or inside a pywebview window pointing at the same URL - one code path).

Security for a local tool:

* binds to 127.0.0.1 only,
* every ``/api/*`` call needs the per-run token that is injected into the served
  ``index.html`` (``X-API-Token`` header, compared with :func:`secrets.compare_digest`),
* static files can only be served from the ``ui`` directory (resolved path check),
* responses are ``no-store`` so an updated UI is never served stale.
"""

from __future__ import annotations

import json
import mimetypes
import secrets
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, unquote, urlparse

UI_DIR = Path(__file__).resolve().parent / "ui"
TOKEN_PLACEHOLDER = "__API_TOKEN__"


class DashboardServer:
    """Serves ``ui/`` plus the JSON API for one :class:`ShowModeService`."""

    def __init__(self, service, ui_dir: Path | str = UI_DIR, host: str = "127.0.0.1"):
        self.service = service
        self.ui_dir = Path(ui_dir).resolve()
        self.host = host
        self.token = secrets.token_urlsafe(24)
        self.port = 0
        self._httpd: ThreadingHTTPServer | None = None
        self._thread: threading.Thread | None = None

    # -- lifecycle ---------------------------------------------------------

    @property
    def url(self) -> str:
        return "http://{}:{}/".format(self.host, self.port)

    def start(self, port: int = 0) -> int:
        if self._httpd is not None:
            return self.port
        handler = self._build_handler()
        self._httpd = ThreadingHTTPServer((self.host, int(port)), handler)
        self._httpd.daemon_threads = True
        self.port = int(self._httpd.server_address[1])
        self._thread = threading.Thread(
            target=self._httpd.serve_forever, name="dashboard-server", daemon=True
        )
        self._thread.start()
        return self.port

    def stop(self) -> None:
        httpd, self._httpd = self._httpd, None
        if httpd is not None:
            httpd.shutdown()
            httpd.server_close()

    # -- routing -----------------------------------------------------------

    def _build_handler(self):
        dashboard = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"
            server_version = "ChitchatDashboard/0.3"

            # --- plumbing ------------------------------------------------
            def log_message(self, fmt, *args):  # noqa: A003 - stdlib signature
                return  # activity goes to the dashboard log, not to stderr

            def _json(self, payload, status: int = 200) -> None:
                body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
                self._send(body, status, "application/json; charset=utf-8")

            def _send(self, body: bytes, status: int = 200, ctype: str = "text/plain; charset=utf-8"):
                self.send_response(status)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Cache-Control", "no-store")
                self.send_header("X-Content-Type-Options", "nosniff")
                self.send_header("Referrer-Policy", "no-referrer")
                self.end_headers()
                if self.command != "HEAD":
                    self.wfile.write(body)

            def _authorized(self) -> bool:
                supplied = self.headers.get("X-API-Token") or ""
                return secrets.compare_digest(supplied, dashboard.token)

            def _read_json(self):
                try:
                    length = int(self.headers.get("Content-Length") or 0)
                except ValueError:
                    return None
                if length <= 0:
                    return {}
                if length > 1_000_000:
                    return None
                raw = self.rfile.read(length)
                try:
                    payload = json.loads(raw.decode("utf-8") or "{}")
                except (UnicodeDecodeError, json.JSONDecodeError):
                    return None
                return payload if isinstance(payload, dict) else None

            # --- static UI -----------------------------------------------
            def _serve_ui(self, path: str) -> None:
                relative = unquote(path).lstrip("/") or "index.html"
                candidate = (dashboard.ui_dir / relative).resolve()
                try:
                    inside = candidate == dashboard.ui_dir or dashboard.ui_dir in candidate.parents
                except OSError:
                    inside = False
                if not inside or not candidate.is_file():
                    return self._json({"error": "not found"}, 404)
                data = candidate.read_bytes()
                if candidate.name == "index.html":
                    data = data.replace(
                        TOKEN_PLACEHOLDER.encode("ascii"), dashboard.token.encode("ascii")
                    )
                ctype = mimetypes.guess_type(candidate.name)[0] or "application/octet-stream"
                if ctype.startswith("text/") or ctype in ("application/javascript", "application/json"):
                    ctype += "; charset=utf-8"
                self._send(data, 200, ctype)

            # --- GET ------------------------------------------------------
            def do_GET(self):  # noqa: N802 - stdlib naming
                parsed = urlparse(self.path)
                route = parsed.path
                if route.startswith("/api/"):
                    if not self._authorized():
                        return self._json({"error": "unauthorized"}, 401)
                    if route == "/api/ping":
                        return self._json({"ok": True, "component": "python-show-mode-dashboard"})
                    if route == "/api/state":
                        selected = (parse_qs(parsed.query).get("selected") or [""])[0]
                        return self._json(dashboard.service.state(selected))
                    return self._json({"error": "not found"}, 404)
                return self._serve_ui(route)

            def do_HEAD(self):  # noqa: N802
                self.do_GET()

            # --- POST -----------------------------------------------------
            def do_POST(self):  # noqa: N802
                route = urlparse(self.path).path
                if not route.startswith("/api/"):
                    return self._json({"error": "not found"}, 404)
                if not self._authorized():
                    return self._json({"error": "unauthorized"}, 401)
                payload = self._read_json()
                if payload is None:
                    return self._json({"error": "invalid JSON body"}, 400)
                service = dashboard.service
                try:
                    if route == "/api/settings/browser":
                        return self._json(service.save_browser_settings(payload))
                    if route == "/api/settings/general":
                        return self._json(service.save_general_settings(payload))
                    if route == "/api/settings/message":
                        return self._json(service.save_message_settings(payload))
                    if route == "/api/settings/proxy":
                        return self._json(service.save_proxy_settings(payload))
                    if route == "/api/proxy/test":
                        raw = str(payload.get("entries") or "")
                        results = service.test_proxy_entries(raw)
                        return self._json({"ok": True, "count": len(results), "results": results})
                    if route == "/api/start":
                        return self._json(service.start_show_mode(payload.get("count", 1)))
                    if route == "/api/stop":
                        return self._json(service.stop_show_mode())
                    if route == "/api/logs/clear":
                        return self._json(service.clear_logs())
                    if route == "/api/folder":
                        folder = str(payload.get("path") or "").strip()
                        if not folder:
                            return self._json({"ok": False, "error": "folder path is required"}, 400)
                        return self._json(service.load_message_folder(folder))
                except ValueError as error:  # validation: the message is for the operator
                    return self._json({"error": str(error)}, 400)
                except Exception as error:  # noqa: BLE001 - reported to the UI
                    return self._json({"error": str(error)}, 500)
                return self._json({"error": "not found"}, 404)

            # --- anything else --------------------------------------------
            def do_PUT(self):  # noqa: N802
                self._json({"error": "method not allowed"}, 405)

            do_DELETE = do_PUT
            do_PATCH = do_PUT

        return Handler
