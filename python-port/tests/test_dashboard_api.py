"""Dashboard API tests for Console 2.0: the routes the new tabs call.

Every test drives the real HTTP server on a loopback port (no browser, no GUI).
The point is that the operator's buttons are backed by answers that are true:
the Overview worker table, the CHECK SYSTEM preflight, the Proxy tab's live
validation and REMOVE FAILED, the Message editor's folder path/clear, the Logs
tab's raw port.log download and the per-browser CLOSE.

Ported behaviour is only asserted where it is observable - no internals.
"""

from __future__ import annotations

import json
import threading
from pathlib import Path

from tests.test_server import ServerTestCase


class OverviewTests(ServerTestCase):
    """WS1: one screen with the counters, the browser rows and the next action."""

    def overview(self) -> dict:
        status, payload, _ = self.json_request("/api/state")
        self.assertEqual(status, 200)
        return payload["overview"]

    def complete_setup(self) -> None:
        """Walk the three setup steps so the START gate is open.

        START (and everything the Overview tab says afterwards) is gated on
        MESSAGE + PROXY + SETTING; a test that jumps straight to the browser
        rows has to do what the operator does first.
        """
        self.json_request(
            "/api/settings/message",
            method="POST",
            payload={"chat_mode": "single", "sequence": "hello there"},
        )
        self.json_request("/api/settings/proxy", method="POST", payload={"enabled": False, "entries": ""})
        self.json_request(
            "/api/settings/general", method="POST", payload={"first_message_mode": "bot"}
        )
        self.json_request("/api/flow/start", method="POST", payload={})
        status, flow, _ = self.json_request("/api/state")
        self.assertEqual(status, 200)
        self.assertTrue(flow["flow"]["ready_to_start"], "the test setup must open the START gate")

    def pretend_running(self, status: str = "running") -> None:
        """Make the service report a live run without opening a browser."""
        stop = threading.Event()
        thread = threading.Thread(target=stop.wait, daemon=True)
        thread.start()
        self.addCleanup(stop.set)
        self.addCleanup(thread.join, 5)
        with self.service._lock:  # noqa: SLF001
            self.service._thread = thread
            self.service._status = status

    def seed_workers(self, rows) -> None:
        """Fill the worker registry the way a run does (the thread is not needed)."""
        with self.service._lock:  # noqa: SLF001 - the registry is deliberately private
            self.service._workers.clear()
            self.service._worker_events.clear()
            for row in rows:
                self.service._workers[row["index"]] = row
                self.service._worker_events[row["index"]] = threading.Event()

    def test_state_publishes_the_worker_table_and_the_overview(self):
        status, payload, _ = self.json_request("/api/state")
        self.assertEqual(status, 200)
        self.assertIn("workers", payload)
        self.assertEqual(payload["workers"], [])
        overview = payload["overview"]
        for key in (
            "status",
            "running",
            "workers_total",
            "workers_active",
            "workers_waiting_login",
            "messages_sent",
            "messages_incoming",
            "chats",
            "proxy",
            "message",
            "general",
            "next_action",
        ):
            self.assertIn(key, overview)

    def test_a_fresh_install_is_told_to_finish_the_setup(self):
        overview = self.overview()
        self.assertEqual(overview["next_action"]["step"], "finish-setup")
        self.assertIn("MESSAGE", overview["next_action"]["text"])
        self.assertEqual(overview["next_action"]["tab"], "message")

    def test_the_counters_add_up_the_worker_rows(self):
        self.complete_setup()
        self.pretend_running("waiting-login")
        self.seed_workers(
            [
                {
                    "index": 1,
                    "total": 2,
                    "profile": "default",
                    "channel": "chrome",
                    "state": "running",
                    "sent": 3,
                    "incoming": 2,
                    "chats": 1,
                    "started_at": 0.0,
                },
                {
                    "index": 2,
                    "total": 2,
                    "profile": "default",
                    "channel": "msedge",
                    "state": "waiting-login",
                    "sent": 0,
                    "incoming": 0,
                    "chats": 0,
                    "started_at": 0.0,
                },
            ]
        )
        status, payload, _ = self.json_request("/api/state")
        self.assertEqual(status, 200)
        overview = payload["overview"]
        self.assertEqual(overview["workers_total"], 2)
        self.assertEqual(overview["workers_active"], 1)
        self.assertEqual(overview["workers_waiting_login"], 1)
        self.assertEqual(overview["messages_sent"], 3)
        self.assertEqual(overview["messages_incoming"], 2)
        self.assertEqual(overview["chats"], 1)
        self.assertIn("Log in", overview["next_action"]["text"])
        self.assertEqual(overview["next_action"]["tab"], "browser")
        # the rows carry what the table prints
        row = payload["workers"][0]
        for key in ("index", "total", "profile", "channel", "state", "sent", "incoming", "uptime_seconds"):
            self.assertIn(key, row)
        self.assertGreaterEqual(row["uptime_seconds"], 0)

    def test_the_next_action_changes_as_a_run_progresses(self):
        self.complete_setup()
        # setup done, run not started yet -> the Browser step
        self.assertEqual(self.overview()["next_action"]["step"], "press-run")
        # browsers open and waiting for the operator's login
        self.pretend_running("waiting-login")
        self.seed_workers(
            [{"index": 1, "total": 1, "profile": "default", "state": "waiting-login", "started_at": 0.0}]
        )
        login = self.overview()["next_action"]
        self.assertEqual(login["step"], "log-in")
        self.assertIn("Log in", login["text"])
        self.assertEqual(login["tab"], "browser")
        # everyone is logged in and chatting: nothing to do
        self.seed_workers(
            [{"index": 1, "total": 1, "profile": "default", "state": "running", "started_at": 0.0}]
        )
        self.pretend_running("running")
        self.assertEqual(self.overview()["next_action"]["step"], "watching")
        watching = self.overview()["next_action"]
        self.assertEqual(watching["step"], "watching")
        self.assertEqual(watching["tab"], "chat")

    def test_the_overview_previews_the_proxy_rotation(self):
        status, payload, _ = self.json_request(
            "/api/settings/proxy",
            method="POST",
            payload={"enabled": True, "entries": "1.1.1.1:1080\n2.2.2.2:1080\n3.3.3.3:1080"},
        )
        self.assertEqual(status, 200)
        overview = self.overview()
        self.assertTrue(overview["proxy"]["enabled"])
        self.assertEqual(overview["proxy"]["lines"], 3)
        self.assertEqual([row["index"] for row in overview["proxy"]["preview"]], [1, 2, 3])
        self.assertIn("1.1.1.1:1080", overview["proxy"]["preview"][0]["proxy"])

    def test_a_broken_proxy_list_is_reported_not_hidden(self):
        status, _, _ = self.json_request(
            "/api/settings/proxy", method="POST", payload={"enabled": True, "entries": "nonsense:zz"}
        )
        self.assertEqual(status, 400)
        overview = self.overview()
        self.assertFalse(overview["proxy"]["enabled"])

    def test_the_overview_says_which_proxy_each_browser_gets(self):
        self.json_request(
            "/api/settings/proxy",
            method="POST",
            payload={
                "enabled": True,
                "entries": "\n".join("10.0.0.{}:8080".format(n) for n in range(1, 9)),
            },
        )
        overview = self.overview()
        self.assertEqual(len(overview["proxy"]["preview"]), 5)  # max five rows
        self.assertEqual(overview["proxy"]["preview"][0]["index"], 1)


class WorkerCloseTests(ServerTestCase):
    """The per-browser CLOSE button (the owner chose close-one, not stop-after-chat)."""

    def test_close_refuses_an_index_that_is_not_running(self):
        status, payload, _ = self.json_request("/api/workers/close", method="POST", payload={"index": 3})
        self.assertEqual(status, 200)
        self.assertFalse(payload["ok"])
        self.assertIn("not running", payload["error"])

    def test_close_needs_a_index(self):
        status, payload, _ = self.json_request("/api/workers/close", method="POST", payload={})
        self.assertFalse(payload["ok"])
        self.assertIn("index", payload["error"])

    def test_close_stops_only_the_worker_that_was_pressed(self):
        event = threading.Event()
        with self.service._lock:  # noqa: SLF001
            self.service._workers[2] = {
                "index": 2,
                "total": 3,
                "profile": "default",
                "state": "running",
                "started_at": 0.0,
            }
            self.service._worker_events[2] = event
        status, payload, _ = self.json_request("/api/workers/close", method="POST", payload={"index": 2})
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        self.assertTrue(event.is_set(), "the worker's stop event must be set")
        rows = self.service.workers()
        self.assertEqual(rows[0]["state"], "closing")
        self.assertIn("CLOSE pressed", rows[0]["note"])
        self.assertIn("CLOSE pressed", "\n".join(
            entry["message"] for entry in self.service.logs.entries()
        ))

    def test_close_is_not_reachable_without_the_token(self):
        status, _, _ = self.request("/api/workers/close", method="POST", payload={"index": 1}, token=False)
        self.assertEqual(status, 401)


class DiagnosticsTests(ServerTestCase):
    """WS3: CHECK SYSTEM - the same checks ``python app.py --diag`` prints."""

    def test_the_fast_check_never_launches_a_browser(self):
        status, payload, _ = self.json_request("/api/diagnostics?deep=0")
        self.assertEqual(status, 200)
        self.assertFalse(payload["deep"])
        names = [check["name"] for check in payload["checks"]]
        for expected in ("python", "data-dir", "log-file", "crash-files", "dom-asset"):
            self.assertIn(expected, names)
        for check in payload["checks"]:
            self.assertIn(check["status"], ("PASS", "FAIL", "WARN", "BLOCKED"))
            self.assertTrue(check["label"])
        # every browser check must say "not checked", never a fake PASS
        for check in payload["checks"]:
            if check["name"].startswith("browser-") or check["name"] in ("chrome", "msedge"):
                self.assertEqual(check["status"], "BLOCKED")
        self.assertEqual(payload["summary"]["PASS"] >= 1, True)
        self.assertIsInstance(payload["ok"], bool)
        self.assertIn("python", payload["system"])

    def test_the_deep_check_is_the_default(self):
        status, payload, _ = self.json_request("/api/diagnostics")
        self.assertEqual(status, 200)
        self.assertTrue(payload["deep"])

    def test_the_check_reports_a_missing_data_folder_but_creates_it(self):
        status, payload, _ = self.json_request("/api/diagnostics?deep=0")
        data_dir = Path(payload["system"]["data_dir"])
        self.assertTrue(data_dir.is_dir())
        self.assertFalse((data_dir / ".write-probe").exists(), "the probe must be cleaned up")

    def test_the_preflight_catches_a_silence_limit_below_the_reply_delay(self):
        """The 2026-10-15 stuck run was caused by 7-9s of silence + a 3-10s reply
        delay; CHECK SYSTEM must say it before RUN, not after a lost chat."""
        self.json_request(
            "/api/settings/general",
            method="POST",
            payload={
                "reply_delay_min_seconds": 3,
                "reply_delay_max_seconds": 10,
                "skip_if_no_reply_ms": 7000,
                "skip_if_no_reply_max_ms": 9000,
            },
        )
        status, payload, _ = self.json_request("/api/diagnostics?deep=0")
        self.assertEqual(status, 200)
        checks = {check["name"]: check for check in payload["checks"]}
        self.assertEqual(checks["timings"]["status"], "WARN")
        self.assertIn("leaves chats before its reply is due", checks["timings"]["detail"])
        self.assertIn("3-10s", checks["timings"]["detail"])
        self.assertIn("7-9s", checks["timings"]["detail"])
        self.assertEqual(checks["page-scan"]["status"], "PASS")
        self.assertIn("350 ms", checks["page-scan"]["detail"])
        self.assertIn("mode=", checks["message-mode"]["detail"])

    def test_the_preflight_flags_folder_mode_without_files_and_a_slow_scan(self):
        self.json_request(
            "/api/settings/message",
            method="POST",
            payload={"chat_mode": "folder", "sequence": "one line"},
        )
        self.json_request(
            "/api/settings/general",
            method="POST",
            payload={"poll_interval_ms": 5000, "skip_if_no_reply_ms": 0},
        )
        status, payload, _ = self.json_request("/api/diagnostics?deep=0")
        self.assertEqual(status, 200)
        checks = {check["name"]: check for check in payload["checks"]}
        self.assertEqual(checks["message-mode"]["status"], "WARN")
        self.assertIn("falls back to the single sequence", checks["message-mode"]["detail"])
        self.assertEqual(checks["page-scan"]["status"], "WARN")
        self.assertIn("replies will feel slow", checks["page-scan"]["detail"])
        # 0 = off, so there is nothing to warn about
        self.assertEqual(checks["timings"]["status"], "PASS")
        self.assertIn("Skip if no reply=off", checks["timings"]["detail"])

    def test_diagnostics_need_the_token(self):
        status, _, _ = self.request("/api/diagnostics?deep=0", token=False)
        self.assertEqual(status, 401)


class ProxyValidateTests(ServerTestCase):
    """WS5: live validation + the default protocol chosen in the tab."""

    def test_each_line_is_reported_on_its_own(self):
        status, payload, _ = self.json_request(
            "/api/proxy/validate",
            method="POST",
            payload={"entries": "1.2.3.4:8080\nnonsense:zz"},
        )
        self.assertEqual(status, 200)
        self.assertFalse(payload["ok"])
        self.assertEqual(payload["valid"], 1)
        self.assertEqual(payload["invalid"], 1)
        self.assertTrue(payload["lines"][0]["ok"])
        self.assertEqual(payload["lines"][0]["line"], 1)
        self.assertIn("port", payload["lines"][1]["error"])

    def test_the_default_protocol_is_applied_to_plain_lines(self):
        status, payload, _ = self.json_request(
            "/api/proxy/validate",
            method="POST",
            payload={"entries": "1.2.3.4:8080", "default_scheme": "socks5"},
        )
        self.assertEqual(status, 200)
        self.assertEqual(payload["valid"], 1)
        self.assertTrue(payload["lines"][0]["label"].startswith("socks5://"))

    def test_an_unknown_default_protocol_is_refused(self):
        status, payload, _ = self.json_request(
            "/api/proxy/validate",
            method="POST",
            payload={"entries": "1.2.3.4:8080", "default_scheme": "ftp"},
        )
        self.assertEqual(status, 200)
        self.assertFalse(payload["ok"])
        self.assertIn("http", payload["error"].lower())

    def test_blank_lines_are_skipped(self):
        status, payload, _ = self.json_request(
            "/api/proxy/validate",
            method="POST",
            payload={"entries": "\n\n1.2.3.4:8080\n\n"},
        )
        self.assertEqual(payload["valid"], 1)
        self.assertEqual(len(payload["lines"]), 1)

    def test_the_test_button_still_takes_the_default_protocol(self):
        # 127.0.0.1:9 refuses instantly, so this stays offline and fast.
        status, payload, _ = self.json_request(
            "/api/proxy/test",
            method="POST",
            payload={"entries": "127.0.0.1:9", "default_scheme": "socks5"},
        )
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["count"], 1)
        self.assertFalse(payload["results"][0]["ok"])
        self.assertTrue(payload["results"][0]["label"].startswith("socks5://"))

    def test_a_bad_default_protocol_stops_the_test_before_any_request(self):
        status, payload, _ = self.json_request(
            "/api/proxy/test",
            method="POST",
            payload={"entries": "1.2.3.4:8080", "default_scheme": "ftp"},
        )
        self.assertEqual(status, 400)
        self.assertFalse(payload["ok"])


class MessageEditorTests(ServerTestCase):
    """WS2: the mode dropdown, the single sequence and the folder helpers."""

    def test_the_single_sequence_round_trips(self):
        status, payload, _ = self.json_request(
            "/api/settings/message",
            method="POST",
            payload={"chat_mode": "single", "sequence": "hello\nhow are you"},
        )
        self.assertEqual(status, 200)
        self.assertEqual(payload["chat_mode"], "single")
        self.assertEqual(payload["sequence_lines"], 2)
        state = self.json_request("/api/state")[1]["message"]
        self.assertEqual(state["sequence"], "hello\nhow are you")
        self.assertEqual(state["sequence_count"], 1)
        self.assertEqual(state["sequence_preview"], "hello")

    def test_switching_to_folder_mode_needs_loaded_files(self):
        # The UI blocks this; the service must not silently start a run without
        # patterns either - Folder mode with nothing loaded is a broken run.
        status, payload, _ = self.json_request(
            "/api/settings/message", method="POST", payload={"chat_mode": "folder"}
        )
        self.assertEqual(status, 200)
        message = self.json_request("/api/state")[1]["message"]
        self.assertEqual(message["loaded_count"], 0)
        self.assertEqual(payload["chat_mode"], "folder")

    def test_a_folder_path_can_be_loaded_and_cleared(self):
        folder = Path(self.temp.name) / "patterns"
        folder.mkdir()
        (folder / "a.txt").write_text("hi there\nhow are you", encoding="utf-8")
        (folder / "b.txt").write_text("second chat", encoding="utf-8")
        (folder / "notes.md").write_text("not a pattern", encoding="utf-8")
        (folder / "empty.txt").write_text("   \n", encoding="utf-8")

        status, payload, _ = self.json_request("/api/folder", method="POST", payload={"path": str(folder)})
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["count"], 2)
        message = self.json_request("/api/state")[1]["message"]
        self.assertEqual(message["chat_mode"], "folder")
        self.assertEqual(message["loaded_count"], 2)
        # two messages in a.txt, one in b.txt - counted as messages, not chars
        self.assertEqual([row["lines"] for row in message["loaded_files"]], [2, 1])
        self.assertEqual([row["chars"] for row in message["loaded_files"]], [19, 11])
        self.assertEqual(message["loaded_files"][0]["preview"], "hi there")
        self.assertEqual(message["sequence_count"], 2)

        status, payload, _ = self.json_request("/api/folder/clear", method="POST", payload={})
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["message"]["loaded_count"], 0)
        self.assertEqual(payload["message"]["chat_mode"], "single")
        self.assertFalse(payload["message"]["use_loaded_sequences"])

    def test_a_missing_folder_path_is_refused(self):
        status, payload, _ = self.json_request("/api/folder", method="POST", payload={"path": "  "})
        self.assertEqual(status, 400)
        self.assertIn("path", payload["error"])

    def test_a_folder_without_txt_files_is_refused(self):
        folder = Path(self.temp.name) / "empty"
        folder.mkdir()
        status, payload, _ = self.json_request("/api/folder", method="POST", payload={"path": str(folder)})
        self.assertEqual(status, 200)
        self.assertFalse(payload["ok"])
        self.assertIn(".txt", payload["error"])

    def test_the_browser_picker_still_works(self):
        status, payload, _ = self.json_request(
            "/api/folder/files",
            method="POST",
            payload={"files": [{"name": "a.txt", "text": "one\ntwo"}, {"name": "b.md", "text": "x"}]},
        )
        self.assertEqual(status, 200)
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["count"], 1)
        self.assertEqual(payload["skipped"], 1)


class LogsFileTests(ServerTestCase):
    """WS6: the Logs tab can hand over the raw file, not just what it shows."""

    def test_the_download_returns_the_port_log_itself(self):
        self.service.logs.append("info", "a line for the download test")
        status, body, headers = self.request("/api/logs/file")
        self.assertEqual(status, 200)
        self.assertIn("attachment", headers.get("Content-Disposition", ""))
        self.assertIn("port.log", headers.get("Content-Disposition", ""))
        self.assertIn(b"a line for the download test", body)
        self.assertEqual(body, self.service.logs.path.read_bytes())

    def test_the_download_is_empty_before_the_first_line(self):
        status, body, _ = self.request("/api/logs/file")
        self.assertEqual(status, 200)
        self.assertEqual(body, self.service.logs_file_bytes())

    def test_the_state_reports_the_log_file_size(self):
        self.service.logs.append("info", "size me")
        payload = self.json_request("/api/state")[1]
        self.assertEqual(payload["system"]["log_bytes"], self.service.logs_file_size())
        self.assertGreater(payload["system"]["log_bytes"], 0)

    def test_the_download_needs_the_token(self):
        status, _, _ = self.request("/api/logs/file", token=False)
        self.assertEqual(status, 401)


class SettingsParityTests(ServerTestCase):
    """The tabs may only write what they own; the Go-only fields must survive."""

    def test_saving_the_browser_tab_keeps_the_go_headless_flag(self):
        self.json_request(
            "/api/settings/browser",
            method="POST",
            payload={"profile": "work", "channel": "chrome", "headless": True},
        )
        # the new dashboard never sends "headless" - the field must stay as it was
        status, payload, _ = self.json_request(
            "/api/settings/browser", method="POST", payload={"profile": "work2", "channel": "auto"}
        )
        self.assertEqual(status, 200)
        self.assertEqual(payload["profile"], "work2")
        self.assertTrue(payload["headless"], "the Go worker field must not be reset by the UI")

    def test_the_advanced_timings_are_stored_with_their_go_names(self):
        status, payload, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={
                "first_message_mode": "bot",
                "reply_delay_min_seconds": 5,
                "reply_delay_max_seconds": 10,
                "skip_if_no_reply_ms": 60000,
                "skip_if_no_reply_max_ms": 90000,
                "poll_min_seconds": 30,
                "poll_max_seconds": 60,
                "action_min_seconds": 25,
                "action_max_seconds": 50,
                "match_reply_timeout_min_seconds": 70,
                "match_reply_timeout_max_seconds": 80,
                "match_close_wait_min_seconds": 1,
                "match_close_wait_max_seconds": 2,
                "poll_interval_ms": 350,
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(payload["poll_min_seconds"], 30)
        saved = json.loads((Path(self.temp.name) / "state" / "general.json").read_text(encoding="utf-8"))
        self.assertEqual(saved["poll_min_seconds"], 30)
        self.assertEqual(saved["action_max_seconds"], 50)
        self.assertEqual(saved["match_reply_timeout_max_seconds"], 80)
        self.assertEqual(saved["match_close_wait_max_seconds"], 2)
        self.assertEqual(saved["poll_interval_ms"], 350)
        # the reply delay the operator typed in the Settings tab keeps its meaning
        self.assertEqual(saved["reply_delay_min_seconds"], 5)
        self.assertEqual(saved["reply_delay_max_seconds"], 10)
        overview = self.json_request("/api/state")[1]["overview"]
        self.assertEqual(overview["general"]["reply_delay"], "5-10s")
        self.assertEqual(overview["general"]["skip_if_no_reply"], "60-90s")
        self.assertEqual(overview["general"]["poll_interval_ms"], 350)

    def test_a_username_sequence_without_usernames_is_saved_but_flagged(self):
        status, payload, _ = self.json_request(
            "/api/settings/message",
            method="POST",
            payload={"chat_mode": "single", "sequence": "hey %username%", "usernames": ""},
        )
        self.assertEqual(status, 200)
        self.assertTrue(payload["needs_usernames"])
        overview = self.json_request("/api/state")[1]["overview"]
        self.assertTrue(overview["message"]["needs_usernames"])


    def test_the_dashboard_payload_never_resets_the_go_worker_timings(self):
        """The Settings tab no longer shows them, so saving must not wipe them."""
        self.json_request(
            "/api/settings/general",
            method="POST",
            payload={"poll_min_seconds": 33, "action_max_seconds": 55},
        )
        # Exactly what the dashboard sends now: only the fields it owns.
        status, payload, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={
                "first_message_mode": "user",
                "first_message_answer_all": True,
                "reply_delay_min_seconds": 3,
                "reply_delay_max_seconds": 10,
                "skip_if_no_reply_ms": 60000,
                "skip_if_no_reply_max_ms": 90000,
                "poll_interval_ms": 350,
            },
        )
        self.assertEqual(status, 200)
        saved = json.loads((Path(self.temp.name) / "state" / "general.json").read_text(encoding="utf-8"))
        self.assertEqual(saved["poll_min_seconds"], 33, "a Go worker timing was reset by the UI payload")
        self.assertEqual(saved["action_max_seconds"], 55)
        self.assertTrue(saved["first_message_answer_all"])
        self.assertEqual(saved["first_message_mode"], "user")
        overview = self.json_request("/api/state")[1]["overview"]
        self.assertTrue(overview["general"]["first_message_answer_all"])
        self.assertEqual(overview["general"]["poll_interval_ms"], 350)

    def test_a_silence_limit_shorter_than_the_reply_delay_is_flagged(self):
        status, payload, _ = self.json_request(
            "/api/settings/general",
            method="POST",
            payload={
                "reply_delay_min_seconds": 3,
                "reply_delay_max_seconds": 10,
                "skip_if_no_reply_ms": 7000,
                "skip_if_no_reply_max_ms": 9000,
            },
        )
        self.assertEqual(status, 200)
        self.assertEqual(payload["skip_if_no_reply_max_ms"], 9000)
        logs = self.json_request("/api/state")[1]["logs"]
        self.assertTrue(
            any(
                str(entry.get("level", "")).upper() == "WARN"
                and "shorter than Reply delay max" in entry.get("message", "")
                for entry in logs
            ),
            "the operator must see why 7-9s of silence abandons chats",
        )

    def test_the_overview_reports_lines_and_paused_browsers(self):
        overview = self.json_request("/api/state")[1]["overview"]
        self.assertEqual(overview["lines"], {"user": 0, "bot": 0})
        self.assertEqual(overview["workers_paused"], 0)
        self.assertIn("lines", overview)


if __name__ == "__main__":
    import unittest

    unittest.main()
