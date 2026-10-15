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
from chitchatport.settings import GeneralSettings, save_json
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
            payload={"first_message_mode": "user", "reply_delay_min_seconds": 5, "reply_delay_max_seconds": 10},
        )
        self.assertEqual(status, 200)
        self.assertEqual(saved["first_message_mode"], "user")
        self.assertEqual(saved["reply_delay_min_seconds"], 5)
        self.assertEqual(saved["reply_delay_max_seconds"], 10)

        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["general"]["first_message_mode"], "user")
        self.assertEqual(state["general"]["reply_delay_min_seconds"], 5)
        # The runner reads these same Go fields (seconds), never a second copy.
        self.assertEqual(state["general"]["reply_delay_max_seconds"], 10)

        # Unknown/legacy values fall back to bot-writes-first, like the Go app.
        _, saved, _ = self.json_request(
            "/api/settings/general", method="POST", payload={"first_message_mode": "garbage"}
        )
        self.assertEqual(saved["first_message_mode"], "bot")

    def test_general_settings_from_the_dashboard_keep_the_hidden_go_values(self):
        # The Settings tab now edits three seconds fields; the poll/action values
        # it no longer shows must survive a save instead of being reset.
        _, before, _ = self.json_request("/api/state")
        original = before["general"]
        status, saved, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={
                "first_message_mode": "bot",
                "reply_delay_min_seconds": 6,
                "reply_delay_max_seconds": 12,
                "skip_if_no_reply_ms": 60000,
                "skip_if_no_reply_max_ms": 90000,
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(saved["reply_delay_min_seconds"], 6)
        self.assertEqual(saved["reply_delay_max_seconds"], 12)
        self.assertEqual(saved["skip_if_no_reply_ms"], 60000)
        self.assertEqual(saved["skip_if_no_reply_max_ms"], 90000)
        # poll interval is backend-only now: never sent by the UI, never lost
        self.assertEqual(saved["poll_interval_ms"], original["poll_interval_ms"])
        self.assertEqual(saved["poll_min_seconds"], original["poll_min_seconds"])
        self.assertEqual(saved["action_max_seconds"], original["action_max_seconds"])
        # The log line speaks seconds, like the dashboard.
        self.assertTrue(
            self.service.logs.contains(
                "General settings saved. Reply delay=6-12s Skip if no reply=60-90s First=bot"
            )
        )

    def test_skip_if_no_reply_can_be_switched_off_from_the_dashboard(self):
        _, saved, _ = self.json_request(
            "/api/settings/general", method="POST", payload={"skip_if_no_reply_ms": 0}
        )
        self.assertEqual(saved["skip_if_no_reply_ms"], 0)
        self.assertTrue(self.service.logs.contains("Skip if no reply=off"))

    def test_general_settings_ignore_wrong_types(self):
        # A wrong type is skipped by the settings coercion (documented, unchanged
        # behaviour): the stored value falls back to the field default and the
        # request still succeeds instead of failing the dashboard.
        status, saved, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={"reply_delay_min_seconds": "not-a-number", "poll_interval_ms": None},
        )
        self.assertEqual(status, 200)
        self.assertEqual(
            saved["reply_delay_min_seconds"], GeneralSettings().reply_delay_min_seconds
        )
        self.assertEqual(saved["poll_interval_ms"], 350)

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

    def test_folder_files_endpoint_takes_the_picked_files(self):
        # What the browser folder picker sends: names relative to the picked
        # folder plus the file text.
        payload = {
            "files": [
                {"name": "patterns/a.txt", "text": "first\nsecond\n"},
                {"name": "patterns/b.txt", "text": "  only  "},
                {"name": "patterns/notes.md", "text": "not a sequence"},
                {"name": "patterns/sub/c.txt", "text": "nested: the Go app never reads it"},
                {"name": "patterns/empty.txt", "text": "   \n"},
            ]
        }
        status, result, _ = self.json_request("/api/folder/files", method="POST", payload=payload)
        self.assertEqual(status, 200)
        self.assertTrue(result["ok"])
        self.assertEqual(result["count"], 2)
        self.assertEqual(result["skipped"], 3)
        self.assertEqual(result["source"], "the browser folder picker")

        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["message"]["chat_mode"], "folder")
        self.assertTrue(state["message"]["use_loaded_sequences"])
        self.assertEqual(state["message"]["loaded_sequences"], ["first\nsecond", "only"])

    def test_state_says_when_a_username_is_missing(self):
        # The dashboard shows this before the run: Go refuses at reply time.
        status, payload, _ = self.json_request(
            "/api/settings/message", method="POST", payload={"sequence": "hi %username%", "usernames": ""}
        )
        self.assertEqual(status, 200)
        _, state, _ = self.json_request("/api/state")
        self.assertTrue(state["message"]["needs_usernames"])
        self.assertFalse(state["flow"]["complete"]["message"])
        self.assertTrue(state["flow"]["needs_usernames"])

        self.json_request(
            "/api/settings/message", method="POST", payload={"usernames": "volcanic flap"}
        )
        _, state, _ = self.json_request("/api/state")
        self.assertFalse(state["message"]["needs_usernames"])
        self.assertTrue(state["flow"]["complete"]["message"])

    def test_folder_files_endpoint_validates_the_payload(self):
        status, payload, _ = self.json_request("/api/folder/files", method="POST", payload={"files": "nope"})
        self.assertEqual(status, 400)
        self.assertIn("list", payload["error"])

        status, payload, _ = self.json_request(
            "/api/folder/files", method="POST", payload={"files": [{"name": "a.md", "text": "x"}]}
        )
        self.assertEqual(status, 200)
        self.assertEqual(
            payload, {"ok": False, "error": "no valid .txt files found in the selected folder"}
        )

        status, payload, _ = self.json_request(
            "/api/folder/files", method="POST", payload={"files": [{"name": "a.txt", "text": "x"}] * 1001}
        )
        self.assertEqual(status, 400)
        self.assertIn("too many files", payload["error"])

    def test_saving_usernames_keeps_the_loaded_folder(self):
        # The MESSAGE tab only edits usernames now, so saving must never drop
        # the folder that was just loaded.
        self.json_request(
            "/api/folder/files",
            method="POST",
            payload={"files": [{"name": "a.txt", "text": "one\ntwo"}]},
        )
        status, _, _ = self.json_request(
            "/api/settings/message", method="POST", payload={"usernames": "alice"}
        )
        self.assertEqual(status, 200)
        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["message"]["usernames"], "alice")
        self.assertEqual(state["message"]["chat_mode"], "folder")
        self.assertEqual(state["message"]["loaded_sequences"], ["one\ntwo"])
        self.assertTrue(state["message"]["use_loaded_sequences"])

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

class ProxyRouteTests(ServerTestCase):
    def test_state_exposes_the_proxy_block(self):
        _, state, _ = self.json_request("/api/state")
        self.assertIn("proxy", state)
        self.assertEqual(state["proxy"]["enabled"], False)
        self.assertEqual(state["proxy"]["entries"], "")
        self.assertEqual(state["proxy"]["lines"], 0)

    def test_proxy_settings_round_trip_and_rotation_count(self):
        status, saved, _ = self.json_request(
            "/api/settings/proxy",
            method="POST",
            payload={"enabled": True, "entries": "1.2.3.4:8080\n1.2.3.5:8080:user:pass\n\n"},
        )
        self.assertEqual(status, 200)
        self.assertTrue(saved["enabled"])
        self.assertEqual(saved["entries"], "1.2.3.4:8080\n1.2.3.5:8080:user:pass\n\n")
        self.assertNotIn("password", saved)  # legacy single-proxy fields stay gone

        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["proxy"]["lines"], 2)
        self.assertEqual(state["proxy"]["error"], "")

        rotator, count = self.service.load_proxy_rotator()
        self.assertEqual(count, 2)
        self.assertEqual([rotator.next().host for _ in range(3)], ["1.2.3.4", "1.2.3.5", "1.2.3.4"])

    def test_save_logs_the_go_lines(self):
        self.json_request(
            "/api/settings/proxy", method="POST", payload={"enabled": True, "entries": "1.2.3.4:8080\n1.2.3.5:8080\n"}
        )
        self.assertTrue(self.service.logs.contains("Proxy settings saved. 2 proxy line(s) enabled for Rod, ChromeDP, and Go request rotation."))
        self.json_request("/api/settings/proxy", method="POST", payload={"enabled": False, "entries": ""})
        self.assertTrue(self.service.logs.contains("Proxy settings saved. Proxy use is disabled."))

    def test_enabling_an_empty_list_is_refused(self):
        status, payload, _ = self.json_request(
            "/api/settings/proxy", method="POST", payload={"enabled": True, "entries": "   \n\n"}
        )
        self.assertEqual(status, 400)
        self.assertEqual(payload["error"], "proxy list is enabled but empty")
        _, state, _ = self.json_request("/api/state")
        self.assertFalse(state["proxy"]["enabled"], "a refused save must not change the stored settings")

    def test_invalid_line_is_reported_with_its_number(self):
        status, payload, _ = self.json_request(
            "/api/settings/proxy", method="POST", payload={"enabled": True, "entries": "1.2.3.4:8080\nnot-a-proxy\n"}
        )
        self.assertEqual(status, 400)
        self.assertIn("proxy line 2", payload["error"])

    def test_proxy_test_endpoint_rejects_an_empty_list(self):
        status, payload, _ = self.json_request("/api/proxy/test", method="POST", payload={"entries": ""})
        self.assertEqual(status, 400)
        self.assertIn("at least one proxy", payload["error"])

    def test_proxy_test_endpoint_reports_a_dead_proxy_without_credentials(self):
        status, payload, _ = self.json_request(
            "/api/proxy/test",
            method="POST",
            payload={"entries": "127.0.0.1:9:alice:s3cret"},
        )
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        row = payload["results"][0]
        self.assertEqual(row["line"], 1)
        self.assertFalse(row["ok"])
        self.assertEqual(row["label"], "http://127.0.0.1:9")
        self.assertNotIn("s3cret", json.dumps(payload), "the API must never echo credentials")
        self.assertIn("No HTTP answer at all", row["hint"])

    def test_legacy_single_proxy_is_migrated_on_load(self):
        store = self.service.store
        save_json(store._path("proxy"), {"enabled": True, "address": "http://1.2.3.4:8080", "username": "u", "password": "p"})
        settings = store.load_proxy_settings()
        self.assertEqual(settings.entries, "1.2.3.4:8080:u:p")
        _, state, _ = self.json_request("/api/state")
        self.assertEqual(state["proxy"]["lines"], 1)


if __name__ == "__main__":
    unittest.main()


class DebugSupportTests(ServerTestCase):
    """Crash reports on disk and the one-file debug bundle, with no secrets."""

    def test_a_crash_writes_the_full_traceback_to_disk(self):
        try:
            {}["profile"]  # noqa: B018 - a realistic KeyError
        except KeyError as error:
            message = self.service.report_crash("[browser 1]", error)
        self.assertIn("KeyError", message)
        self.assertIn("traceback: crash-", message)
        files = list((Path(self.temp.name) / "logs").glob("crash-*.txt"))
        self.assertEqual(len(files), 1)
        text = files[0].read_text(encoding="utf-8")
        self.assertIn("Traceback (most recent call last)", text)
        self.assertIn("KeyError: 'profile'", text)
        self.assertIn("where  : [browser 1]", text)
        self.assertEqual(self.service.logs.entries()[-1]["level"], "ERROR")

    def test_two_crashes_in_one_second_keep_both_files(self):
        for _ in range(2):
            try:
                raise RuntimeError("boom")
            except RuntimeError as error:
                self.service.report_crash("Show Mode failed:", error)
        self.assertEqual(len(list((Path(self.temp.name) / "logs").glob("crash-*.txt"))), 2)

    def test_the_debug_bundle_has_no_secrets(self):
        self.json_request(
            "/api/settings/proxy",
            method="POST",
            payload={
                "enabled": True,
                "entries": "http://alice:TopSecret1@1.2.3.4:8080\n5.6.7.8:3128:bob:Hunter22\n",
            },
        )
        self.json_request(
            "/api/settings/message",
            method="POST",
            payload={"usernames": "volcanic flap\nquiet otter"},
        )
        status, bundle, _ = self.json_request("/api/debug")
        self.assertEqual(status, 200)
        dumped = json.dumps(bundle)
        for secret in ("TopSecret1", "Hunter22", self.server.token, "volcanic flap"):
            self.assertNotIn(secret, dumped, secret)
        masked = bundle["settings"]["proxy"]["entries_masked"]
        self.assertIn("alice:***@1.2.3.4:8080", masked)
        self.assertIn("5.6.7.8:3128:bob:***", masked)
        self.assertEqual(bundle["settings"]["message"]["usernames_count"], 2)
        for key in (
            "system", "run", "flow", "settings", "crash_files", "logs",
            # The additions that make a stuck run diagnosable in one file.
            "privacy", "checks", "effective", "workers", "chat", "risks",
        ):
            self.assertIn(key, bundle)
        self.assertEqual(bundle["settings"]["general"]["skip_if_no_reply_ms"], 60000)
        # The bundle states what the runner was told to do, in runner units.
        effective = bundle["effective"]
        self.assertEqual(effective["reply_delay_seconds"], [5, 10])
        self.assertEqual(effective["skip_if_no_reply_seconds"], [60.0, 90.0])
        self.assertEqual(effective["page_scan_interval_seconds"], 0.35)
        self.assertEqual(effective["first_message_mode"], "bot")
        self.assertFalse(effective["first_message_answer_all"])
        self.assertIn("message bodies", bundle["privacy"])
        # The cheap preflight travels with the bundle, so one file is enough.
        self.assertFalse(bundle["checks"]["deep"])
        self.assertTrue(bundle["checks"]["checks"])
        self.assertEqual(bundle["workers"], [])
        self.assertIn("stats", bundle["chat"])

    def test_the_debug_route_needs_the_token(self):
        status, _, _ = self.request("/api/debug", token=False)
        self.assertEqual(status, 401)
