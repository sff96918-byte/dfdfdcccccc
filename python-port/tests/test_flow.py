"""Guided-flow tests: the stage machine behind the sliding dashboard.

The flow is data-driven (never timed), so these tests drive the real service and
assert which stage it reports and when the console unlocks.
"""

from __future__ import annotations

import json
import tempfile
import threading
import unittest
import urllib.error
import urllib.request
from pathlib import Path

from chitchatport.domchat import DOMControls, DOMMessage, DOMSnapshot
from chitchatport.server import DashboardServer
from chitchatport.service import ShowModeService

STAGES = ("message", "proxy", "setting", "browser", "live")


def snapshot(conversation: str, rows, phase: str = "chatting") -> DOMSnapshot:
    return DOMSnapshot(
        controls=DOMControls(
            conversation=conversation,
            phase=phase,
            own_name="self",
            partner="partner",
            can_send=phase == "chatting",
        ),
        messages=list(rows),
    )


def row(identifier: str, author: str, text: str, order: int = 0) -> DOMMessage:
    return DOMMessage(id=identifier, author=author, text=text, order=order)


class FlowStageTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.service = ShowModeService(Path(self.temp.name))

    def stages(self) -> tuple[str, dict]:
        flow = self.service.flow_state()
        return flow["stage"], flow["complete"]

    def test_a_fresh_install_starts_at_the_message_step(self):
        stage, complete = self.stages()
        self.assertEqual(stage, "message")
        self.assertEqual(complete, {name: False for name in STAGES})

    def test_flow_order_follows_the_requested_sequence(self):
        self.assertEqual(self.stages()[0], "message")
        self.service.save_message_settings({"sequence": "hi there\nhow are you"})
        self.assertEqual(self.stages()[0], "proxy")
        self.service.save_proxy_settings({"enabled": False, "entries": ""})
        self.assertEqual(self.stages()[0], "setting")
        self.service.save_general_settings({})
        self.assertEqual(self.stages()[0], "browser")

    def test_polling_alone_never_moves_the_stage(self):
        for _ in range(5):
            self.service.flow_state()
        self.assertEqual(self.stages()[0], "message")

    def test_an_empty_sequence_does_not_finish_the_message_step(self):
        self.service.save_message_settings({"sequence": "   \n\n"})
        self.assertFalse(self.stages()[1]["message"])

    def test_a_loaded_folder_finishes_the_message_step(self):
        folder = Path(self.temp.name) / "seqs"
        folder.mkdir()
        (folder / "one.txt").write_text("hello\n", encoding="utf-8")
        self.service.load_message_folder(str(folder))
        self.assertTrue(self.stages()[1]["message"])

    def test_message_stage_waits_for_a_username_the_sequence_asks_for(self):
        self.service.save_message_settings({"sequence": "hello %username%"})
        stage, complete = self.stages()
        self.assertFalse(complete["message"], "a %username% line is unusable without usernames")
        self.assertEqual(stage, "message")
        self.assertTrue(self.service.flow_state()["needs_usernames"])

        self.service.save_message_settings({"usernames": "volcanic flap"})
        self.assertTrue(self.stages()[1]["message"])
        self.assertFalse(self.service.flow_state()["needs_usernames"])
        self.assertEqual(self.stages()[0], "proxy")

    def test_loaded_folder_lines_count_too(self):
        self.service.load_message_files([{"name": "a.txt", "text": "hej {snap}"}])
        self.assertTrue(self.service.flow_state()["needs_usernames"])
        self.assertFalse(self.stages()[1]["message"])

    def test_saving_a_proxy_is_a_decision_even_when_disabled(self):
        self.service.save_proxy_settings({"enabled": False, "entries": ""})
        stage, complete = self.stages()
        self.assertTrue(complete["proxy"])
        self.assertEqual(stage, "message", "an unfinished earlier step is never skipped")

    def test_an_existing_proxy_list_counts_as_a_decision(self):
        # A list saved before this feature existed still means "decided".
        self.service.store.save_proxy_settings(
            self.service.store.load_proxy_settings().__class__(enabled=True, entries="1.2.3.4:8080")
        )
        self.assertEqual(self.service.reset_flow()["stage"], "message")
        self.service.save_message_settings({"sequence": "hello"})
        stage, complete = self.stages()
        self.assertTrue(complete["proxy"])
        self.assertEqual(stage, "setting")


class RunAndLoginMixin:
    """Pretends a Show Mode thread is alive and a login JSON was saved."""

    def _pretend_running_with_login(self):
        self.service._login_saved = str(Path(self.temp.name) / "login_info_x.json")
        stop = threading.Event()

        def wait():
            stop.wait(5)

        worker = threading.Thread(target=wait, daemon=True)
        worker.start()
        self.addCleanup(stop.set)
        self.addCleanup(worker.join, 5)
        self.service._thread = worker

    def test_browser_step_needs_a_run_and_a_saved_login(self):
        stage, complete = self.stages()
        self.assertFalse(complete["browser"], "nothing is running yet")
        self.assertEqual(stage, "message")

        self._pretend_running_with_login()
        stage, complete = self.stages()
        # A run alone is not the whole ladder: the earlier steps still gate it.
        self.assertTrue(complete["browser"])
        self.assertEqual(stage, "message")

        self.service.save_message_settings({"sequence": "hello"})
        self.service.save_proxy_settings({"enabled": False, "entries": ""})
        self.service.save_general_settings({})
        stage, complete = self.stages()
        self.assertEqual(stage, "live", "everything is ready: waiting for the first message")
        self.assertFalse(complete["live"])
        self.assertFalse(self.service.flow_state()["unlocked"])

    def test_first_chat_row_unlocks_the_console_once_and_for_good(self):
        run = self.service.feed.start("test")
        self.assertFalse(self.stages()[1]["live"])
        self.service.feed.observe(run, snapshot("one", [row("own", "SELF", "hi", 1)]), False)
        flow = self.service.flow_state()
        self.assertTrue(flow["complete"]["live"])
        self.assertTrue(flow["unlocked"])

        # Stored, so a reload (and an emptied log) keeps the console open.
        stored = json.loads((self.service.store._path("flow")).read_text(encoding="utf-8"))
        self.assertTrue(stored["console_unlocked"])
        reopened = ShowModeService(Path(self.temp.name))
        self.assertTrue(reopened.flow_state()["unlocked"])

    def test_a_lobby_snapshot_does_not_unlock(self):
        run = self.service.feed.start("test")
        self.service.feed.observe(run, snapshot("one", [], phase="lobby"), False)
        self.assertFalse(self.service.flow_state()["complete"]["live"])

    def test_reset_reasks_the_decisions_but_keeps_the_content(self):
        self.service.save_message_settings({"sequence": "hello"})
        self.service.save_proxy_settings({"enabled": False, "entries": ""})
        self.service.save_general_settings({})
        before = self.service.store.load_general_settings()
        flow = self.service.reset_flow()
        self.assertFalse(flow["complete"]["proxy"])
        self.assertFalse(flow["complete"]["setting"])
        self.assertTrue(flow["complete"]["message"], "content is not thrown away")
        self.assertEqual(flow["stage"], "proxy")
        self.assertEqual(self.service.store.load_general_settings(), before)

    def test_skipping_the_flow_unlocks_the_console_for_good(self):
        # SKIP / Esc sends console_unlocked: a reload must not lock it again.
        flow = self.service.save_flow_prefs({"console_unlocked": True})
        self.assertTrue(flow["unlocked"])
        self.assertTrue(ShowModeService(Path(self.temp.name)).flow_state()["unlocked"])
        self.assertEqual(self.service.reset_flow()["unlocked"], False, "flow reset re-locks")

    def test_preferences_round_trip_and_are_normalized(self):
        flow = self.service.save_flow_prefs({"hold": True, "rail_side": "left"})
        self.assertTrue(flow["hold"])
        self.assertEqual(flow["rail_side"], "left")
        self.assertEqual(self.service.save_flow_prefs({"rail_side": "middle"})["rail_side"], "right")
        self.assertEqual(self.service.save_flow_prefs({"nonsense": 1})["rail_side"], "right")
        self.assertTrue(self.service.flow_state()["hold"], "hold survives a reload")


class FlowBrowserAndLiveTests(RunAndLoginMixin, unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.service = ShowModeService(Path(self.temp.name))

    def stages(self):
        flow = self.service.flow_state()
        return flow["stage"], flow["complete"]


class FlowRouteTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.service = ShowModeService(Path(self.temp.name))
        self.server = DashboardServer(self.service)
        self.port = self.server.start(0)
        self.addCleanup(self.server.stop)

    def request(self, path, payload=None, method="GET"):
        url = "http://127.0.0.1:{}{}".format(self.port, path)
        data = None if payload is None else json.dumps(payload).encode()
        request = urllib.request.Request(url, data=data, method=method)
        request.add_header("X-API-Token", self.server.token)
        if payload is not None:
            request.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                return response.status, json.loads(response.read().decode("utf-8"))
        except urllib.error.HTTPError as error:
            return error.code, json.loads(error.read().decode("utf-8"))

    def test_state_carries_the_flow_block(self):
        status, state = self.request("/api/state")
        self.assertEqual(status, 200)
        self.assertEqual(state["flow"]["stage"], "message")
        self.assertEqual(state["flow"]["rail_side"], "right")
        self.assertFalse(state["flow"]["unlocked"])

    def test_prefs_and_reset_routes(self):
        status, flow = self.request("/api/flow/prefs", {"hold": True, "rail_side": "left"}, "POST")
        self.assertEqual(status, 200)
        self.assertTrue(flow["hold"])
        self.assertEqual(flow["rail_side"], "left")

        self.request("/api/settings/message", {"sequence": "hello"}, "POST")
        self.request("/api/settings/proxy", {"enabled": False, "entries": ""}, "POST")
        status, flow = self.request("/api/flow/reset", {}, "POST")
        self.assertEqual(status, 200)
        self.assertEqual(flow["stage"], "proxy")

    def test_the_flow_block_is_read_only_state_not_a_settings_write(self):
        before = (self.service.store._path("flow")).exists()
        self.request("/api/state")
        after = (self.service.store._path("flow")).exists()
        self.assertEqual(before, after, "polling must not create the flow file")


if __name__ == "__main__":
    unittest.main()
