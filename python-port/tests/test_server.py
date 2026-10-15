"""Dashboard server tests: routes, token, static files, path safety.

These run the real stdlib HTTP server on a free loopback port - no browser and
no GUI needed, so they work in CI and in the authoring sandbox.
"""

from __future__ import annotations

import http.client
import json
import tempfile
import unittest
import urllib.error
import urllib.request
from pathlib import Path

from chitchatport.server import DashboardServer
from chitchatport.service import ShowModeService


class ServerTestCase(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.service = ShowModeService(Path(self.temp.name))
        self.server = DashboardServer(self.service)
        self.port = self.server.start(0)
        self.addCleanup(self.server.stop)

    # -- request helpers ---------------------------------------------------
    def request(self, path, *, method="GET", payload=None, token=True, headers=None):
        url = "http://127.0.0.1:{}{}".format(self.port, path)
        data = None if payload is None else json.dumps(payload).encode()
        request = urllib.request.Request(url, data=data, method=method)
        if payload is not None:
            request.add_header("Content-Type", "application/json")
        if token:
            request.add_header("X-API-Token", self.server.token)
        for key, value in (headers or {}).items():
            request.add_header(key, value)
        try:
            with urllib.request.urlopen(request, timeout=5) as response:
                return response.status, response.read(), dict(response.headers)
        except urllib.error.HTTPError as error:
            return error.code, error.read(), dict(error.headers)

    def json_request(self, path, **kwargs):
        status, body, headers = self.request(path, **kwargs)
        return status, json.loads(body.decode("utf-8")), headers


class DashboardServerTests(ServerTestCase):
    def test_server_binds_to_loopback_and_reports_url(self):
        self.assertEqual(self.server.host, "127.0.0.1")
        self.assertGreater(self.port, 0)
        self.assertEqual(self.server.url, "http://127.0.0.1:{}/".format(self.port))

    def test_ping_needs_the_token(self):
        status, _, _ = self.request("/api/ping", token=False)
        self.assertEqual(status, 401)
        status, payload, _ = self.json_request("/api/ping")
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])

    def test_state_shape(self):
        status, payload, _ = self.json_request("/api/state")
        self.assertEqual(status, 200)
        for key in ("running", "status", "browser", "general", "message", "logs", "chat", "system"):
            self.assertIn(key, payload)
        self.assertFalse(payload["running"])
        self.assertEqual(payload["general"]["first_message_mode"], "bot")
        self.assertIn("python", payload["system"])
        self.assertIn("log_file", payload["system"])

    def test_index_serves_the_ui_with_an_injected_token(self):
        status, body, headers = self.request("/")
        self.assertEqual(status, 200)
        text = body.decode("utf-8")
        self.assertIn('name="api-token"', text)
        self.assertIn(self.server.token, text)
        self.assertNotIn("__API_TOKEN__", text)
        self.assertIn("theme.css", text)
        self.assertIn("app.js", text)
        self.assertEqual(headers.get("Cache-Control"), "no-store")

    def test_static_assets_are_served(self):
        for name, marker in (("app.js", "api/state"), ("theme.css", "--accent")):
            status, body, _ = self.request("/" + name)
            self.assertEqual(status, 200, name)
            self.assertIn(marker, body.decode("utf-8"), name)

    def test_unknown_static_file_is_404(self):
        status, payload, _ = self.json_request("/nope.txt")
        self.assertEqual(status, 404)
        self.assertIn("error", payload)

    def test_path_traversal_is_blocked(self):
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=5)
        for raw_path in ("/../app.py", "/..%2fapp.py", "/ui/../../app.py", "/%2e%2e/README.md"):
            connection.request("GET", raw_path, headers={"X-API-Token": self.server.token})
            response = connection.getresponse()
            response.read()
            self.assertIn(response.status, (400, 404), raw_path)
        connection.close()

    def test_general_settings_round_trip_and_normalization(self):
        status, saved, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={"first_message_mode": "user", "reply_delay_ms": 1500},
        )
        self.assertEqual(status, 200)
        self.assertEqual(saved["first_message_mode"], "user")
        self.assertEqual(saved["reply_delay_ms"], 1500)

        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["general"]["first_message_mode"], "user")
        self.assertEqual(state["general"]["reply_delay_ms"], 1500)

        # Unknown/legacy values fall back to bot-writes-first, like the Go app.
        _, saved, _ = self.json_request(
            "/api/settings/general", method="POST", payload={"first_message_mode": "garbage"}
        )
        self.assertEqual(saved["first_message_mode"], "bot")

    def test_general_settings_ignore_wrong_types(self):
        _, before, _ = self.json_request("/api/state")
        default_delay = before["general"]["reply_delay_ms"]
        status, saved, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={"reply_delay_ms": "not-a-number", "poll_interval_ms": None},
        )
        self.assertEqual(status, 200)
        self.assertEqual(saved["reply_delay_ms"], default_delay)

    def test_browser_settings_round_trip(self):
        status, saved, _ = self.json_request(
            "/api/settings/browser",
            method="POST",
            payload={"profile": "  ", "channel": "msedge", "headless": True},
        )
        self.assertEqual(status, 200)
        self.assertEqual(saved["profile"], "default")  # blank profile falls back
        self.assertEqual(saved["channel"], "msedge")
        self.assertTrue(saved["headless"])

    def test_message_settings_round_trip(self):
        status, saved, _ = self.json_request(
            "/api/settings/message",
            method="POST",
            payload={"chat_mode": "single", "sequence": "  hello \r\n\r\n world ", "usernames": "alice"},
        )
        self.assertEqual(status, 200)
        self.assertEqual(saved["sequence"], "hello\nworld")
        self.assertEqual(saved["usernames"], "alice")

    def test_folder_endpoint_validates_the_path(self):
        status, payload, _ = self.json_request("/api/folder", method="POST", payload={})
        self.assertEqual(status, 400)
        self.assertIn("required", payload["error"])

        status, payload, _ = self.json_request(
            "/api/folder", method="POST", payload={"path": str(Path(self.temp.name) / "missing")}
        )
        self.assertEqual(status, 200)
        self.assertFalse(payload["ok"])

        folder = Path(self.temp.name) / "seqs"
        folder.mkdir()
        (folder / "a.txt").write_text("first\nsecond\n", encoding="utf-8")
        (folder / "b.txt").write_text("only\n", encoding="utf-8")
        (folder / "ignore.md").write_text("not a sequence\n", encoding="utf-8")
        status, payload, _ = self.json_request("/api/folder", method="POST", payload={"path": str(folder)})
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["count"], 2)
        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["message"]["chat_mode"], "folder")
        self.assertEqual(len(state["message"]["loaded_sequences"]), 2)

    def test_stop_is_safe_when_nothing_runs(self):
        status, payload, _ = self.json_request("/api/stop", method="POST", payload={})
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])

    def test_invalid_json_body_is_rejected(self):
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=5)
        connection.request(
            "POST",
            "/api/settings/general",
            body=b"{not json",
            headers={"X-API-Token": self.server.token, "Content-Type": "application/json"},
        )
        response = connection.getresponse()
        body = response.read()
        connection.close()
        self.assertEqual(response.status, 400)
        self.assertIn("invalid JSON", body.decode("utf-8"))

    def test_unknown_api_route_and_wrong_method(self):
        status, payload, _ = self.json_request("/api/does-not-exist")
        self.assertEqual(status, 404)
        self.assertIn("error", payload)
        status, payload, _ = self.json_request("/api/start")  # GET on a POST-only route
        self.assertEqual(status, 404)

    def test_logs_clear_endpoint(self):
        self.service.logs.append("info", "hello from the test")
        _, state, _ = self.json_request("/api/state")
        self.assertTrue(any("hello from the test" in entry["message"] for entry in state["logs"]))
        status, payload, _ = self.json_request("/api/logs/clear", method="POST", payload={})
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        _, state, _ = self.json_request("/api/state")
        self.assertFalse(any("hello from the test" in entry["message"] for entry in state["logs"]))

    def test_token_is_required_for_every_api_route(self):
        for path, method in (
            ("/api/state", "GET"),
            ("/api/settings/general", "POST"),
            ("/api/start", "POST"),
            ("/api/logs/clear", "POST"),
        ):
            status, _, _ = self.request(path, method=method, payload={} if method == "POST" else None, token=False)
            self.assertEqual(status, 401, path)


if __name__ == "__main__":
    unittest.main()
