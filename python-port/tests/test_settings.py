"""Settings store tests: defaults, normalization, atomic writes."""

from __future__ import annotations

import json
import os
import tempfile
import unittest
from pathlib import Path

from chitchatport.settings import (
    CHAT_MODE_EVA,
    CHAT_MODE_FOLDER,
    CHAT_MODE_KEYWORD,
    CHAT_MODE_SINGLE,
    FIRST_MESSAGE_BOT,
    FIRST_MESSAGE_RANDOM,
    FIRST_MESSAGE_USER,
    GeneralSettings,
    MessageSettings,
    GO_DEFAULT_MESSAGE_SEQUENCE,
    SettingsStore,
    legacy_message_sequence,
    message_sequence_lines,
    normalize_line_list,
    save_json,
)


class GeneralSettingsTests(unittest.TestCase):
    def test_default_is_bot_first(self):
        self.assertEqual(GeneralSettings().effective_first_message_mode(), FIRST_MESSAGE_BOT)

    def test_known_modes_round_trip_and_unknown_falls_back(self):
        for mode in (FIRST_MESSAGE_BOT, FIRST_MESSAGE_USER, FIRST_MESSAGE_RANDOM):
            self.assertEqual(
                GeneralSettings(first_message_mode=mode).effective_first_message_mode(), mode
            )
        for mode in ("", "USER", "Random ", "garbage"):
            self.assertEqual(
                GeneralSettings(first_message_mode=mode).effective_first_message_mode(),
                FIRST_MESSAGE_BOT,
            )

    def test_store_normalizes_on_save_and_load(self):
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            saved = store.save_general_settings(GeneralSettings(first_message_mode="garbage"))
            self.assertEqual(saved.first_message_mode, FIRST_MESSAGE_BOT)
            raw = json.loads((Path(temp) / "state" / "general.json").read_text())
            self.assertEqual(raw["first_message_mode"], FIRST_MESSAGE_BOT)
            self.assertEqual(store.load_general_settings().first_message_mode, FIRST_MESSAGE_BOT)

            store.save_general_settings(GeneralSettings(first_message_mode=FIRST_MESSAGE_USER))
            self.assertEqual(store.load_general_settings().first_message_mode, FIRST_MESSAGE_USER)

    def test_atomic_save_leaves_no_temp_files(self):
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            store.save_general_settings(GeneralSettings())
            store.save_browser_settings(store.load_browser_settings())
            files = sorted(os.listdir(Path(temp) / "state"))
            self.assertEqual(files, ["browser.json", "general.json"])

    def test_hand_edited_file_is_coerced(self):
        with tempfile.TemporaryDirectory() as temp:
            state = Path(temp) / "state"
            state.mkdir(parents=True)
            (state / "general.json").write_text(
                json.dumps(
                    {
                        "first_message_mode": "RANDOM?",
                        "poll_min_seconds": "not-a-number",
                        "reply_delay_min_seconds": 7,
                        "reply_delay_max_seconds": 9,
                    }
                )
            )
            settings = SettingsStore(temp).load_general_settings()
            self.assertEqual(settings.first_message_mode, FIRST_MESSAGE_BOT)
            self.assertEqual(settings.poll_min_seconds, GeneralSettings().poll_min_seconds)
            self.assertEqual((settings.reply_delay_min_seconds, settings.reply_delay_max_seconds), (7, 9))

    def test_defaults_match_the_go_general_settings(self):
        # Go's model.DefaultGeneralSettings(), field for field.
        settings = GeneralSettings()
        self.assertEqual((settings.poll_min_seconds, settings.poll_max_seconds), (20, 45))
        self.assertEqual((settings.action_min_seconds, settings.action_max_seconds), (20, 45))
        self.assertEqual(
            (settings.match_reply_timeout_min_seconds, settings.match_reply_timeout_max_seconds),
            (60, 60),
        )
        self.assertEqual(
            (settings.match_close_wait_min_seconds, settings.match_close_wait_max_seconds), (0, 0)
        )
        # The reply delay keeps the Go field names and unit, with a safer default
        # than Go's 0-0 so a fresh install cannot answer instantly.
        self.assertEqual((settings.reply_delay_min_seconds, settings.reply_delay_max_seconds), (5, 10))
        # Backend-only cadence: the Go Show Mode constant pollInterval 350ms.
        self.assertEqual(settings.poll_interval_ms, 350)

    def test_go_normalization_is_ported_field_for_field(self):
        # uistore.LoadGeneralSettings/SaveGeneralSettings: a non-positive
        # minimum falls back to the Go default, every maximum is lifted to its
        # minimum, and a negative reply delay never reaches the runner.
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            saved = store.save_general_settings(
                GeneralSettings(
                    poll_min_seconds=0,
                    poll_max_seconds=0,
                    action_min_seconds=0,
                    action_max_seconds=0,
                    match_reply_timeout_min_seconds=0,
                    match_reply_timeout_max_seconds=0,
                    match_close_wait_min_seconds=10,
                    match_close_wait_max_seconds=0,
                    reply_delay_min_seconds=-3,
                    reply_delay_max_seconds=0,
                    skip_if_no_reply_ms=0,
                    skip_if_no_reply_max_ms=-5,
                    poll_interval_ms=1,
                )
            )
            # Go's rule, exactly: a non-positive minimum becomes the default
            # (20) and the maximum is then lifted to that minimum.
            self.assertEqual((saved.poll_min_seconds, saved.poll_max_seconds), (20, 20))
            self.assertEqual((saved.action_min_seconds, saved.action_max_seconds), (20, 20))
            self.assertEqual(
                (saved.match_reply_timeout_min_seconds, saved.match_reply_timeout_max_seconds), (60, 60)
            )
            self.assertEqual(
                (saved.match_close_wait_min_seconds, saved.match_close_wait_max_seconds), (10, 10)
            )
            self.assertEqual((saved.reply_delay_min_seconds, saved.reply_delay_max_seconds), (0, 0))
            self.assertEqual((saved.skip_if_no_reply_ms, saved.skip_if_no_reply_max_ms), (0, 0))
            self.assertEqual(saved.poll_interval_ms, 50)

            # A hand-edited file goes through exactly the same normalization.
            state = Path(temp) / "state"
            state.mkdir(parents=True, exist_ok=True)
            (state / "general.json").write_text(
                json.dumps({"reply_delay_min_seconds": 10, "reply_delay_max_seconds": 4})
            )
            loaded = store.load_general_settings()
            self.assertEqual((loaded.reply_delay_min_seconds, loaded.reply_delay_max_seconds), (10, 10))
            self.assertEqual(loaded.poll_min_seconds, 20)  # missing -> Go default

    def test_an_old_file_without_a_max_still_loads(self):
        with tempfile.TemporaryDirectory() as temp:
            save_json(
                Path(temp) / "state" / "general.json",
                {"reply_delay_min_seconds": 8, "poll_interval_ms": 350, "skip_if_no_reply_ms": 60000},
            )
            settings = SettingsStore(temp).load_general_settings()
            self.assertEqual(settings.reply_delay_min_seconds, 8)
            # The port's own default max (10 s) is above the stored min, so it
            # stays; Go's rule is the same, its default max is simply 0 and
            # therefore lands on the min.
            self.assertEqual(settings.reply_delay_max_seconds, 10)
            self.assertEqual(settings.skip_if_no_reply_max_ms, 90000)  # port default

    def test_legacy_reply_delay_ms_keys_migrate_once_and_are_dropped(self):
        # Earlier builds stored the delay in milliseconds under their own keys.
        # They must be converted (rounded up) and must not come back on save.
        with tempfile.TemporaryDirectory() as temp:
            state = Path(temp) / "state"
            save_json(state / "general.json", {"reply_delay_ms": 4500, "reply_delay_max_ms": 9000})
            store = SettingsStore(temp)
            settings = store.load_general_settings()
            self.assertEqual((settings.reply_delay_min_seconds, settings.reply_delay_max_seconds), (5, 9))
            store.save_general_settings(settings)
            raw = json.loads((state / "general.json").read_text())
            self.assertNotIn("reply_delay_ms", raw)
            self.assertNotIn("reply_delay_max_ms", raw)
            self.assertEqual((raw["reply_delay_min_seconds"], raw["reply_delay_max_seconds"]), (5, 9))

    def test_a_fresh_install_keeps_the_port_default_not_the_legacy_one(self):
        # The migration must not touch a file that has no legacy keys at all,
        # otherwise a fresh install would silently get the old 1-3 s range.
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            self.assertEqual(
                (store.load_general_settings().reply_delay_min_seconds,
                 store.load_general_settings().reply_delay_max_seconds),
                (5, 10),
            )
            saved = store.save_general_settings(store.load_general_settings())
            self.assertEqual((saved.reply_delay_min_seconds, saved.reply_delay_max_seconds), (5, 10))
            raw = json.loads((Path(temp) / "state" / "general.json").read_text())
            self.assertEqual((raw["reply_delay_min_seconds"], raw["reply_delay_max_seconds"]), (5, 10))

    def test_a_legacy_file_with_only_a_max_keeps_the_old_min(self):
        with tempfile.TemporaryDirectory() as temp:
            save_json(Path(temp) / "state" / "general.json", {"reply_delay_max_ms": 8000})
            settings = SettingsStore(temp).load_general_settings()
            self.assertEqual((settings.reply_delay_min_seconds, settings.reply_delay_max_seconds), (1, 8))

    def test_the_go_reply_delay_fields_win_over_the_legacy_keys(self):
        with tempfile.TemporaryDirectory() as temp:
            save_json(
                Path(temp) / "state" / "general.json",
                {"reply_delay_min_seconds": 2, "reply_delay_max_seconds": 3, "reply_delay_ms": 4500},
            )
            settings = SettingsStore(temp).load_general_settings()
            self.assertEqual((settings.reply_delay_min_seconds, settings.reply_delay_max_seconds), (2, 3))


class SilenceSettingTests(unittest.TestCase):
    """SKIP IF NO REPLY: stored in milliseconds like every other timing."""

    def test_default_matches_the_go_auto_engine_timeout(self):
        self.assertEqual(GeneralSettings().skip_if_no_reply_ms, 60000)

    def test_zero_means_off_and_survives_a_round_trip(self):
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            store.save_general_settings(GeneralSettings(skip_if_no_reply_ms=0))
            raw = json.loads((Path(temp) / "state" / "general.json").read_text())
            self.assertEqual(raw["skip_if_no_reply_ms"], 0)
            self.assertEqual(store.load_general_settings().skip_if_no_reply_ms, 0)

    def test_negative_values_are_clamped_on_save_and_on_load(self):
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            saved = store.save_general_settings(GeneralSettings(skip_if_no_reply_ms=-5000))
            self.assertEqual(saved.skip_if_no_reply_ms, 0)
            state = Path(temp) / "state"
            state.mkdir(parents=True, exist_ok=True)
            (state / "general.json").write_text(json.dumps({"skip_if_no_reply_ms": -1}))
            self.assertEqual(store.load_general_settings().skip_if_no_reply_ms, 0)


class MessageSettingsTests(unittest.TestCase):
    def test_line_normalization_matches_go(self):
        self.assertEqual(normalize_line_list("  a  \r\n\r\n b \n"), "a\nb")
        self.assertEqual(normalize_line_list("   "), "")
        self.assertEqual(message_sequence_lines("one\r\ntwo\n\nthree"), ["one", "two", "three"])

    def test_effective_chat_mode_matches_go(self):
        self.assertEqual(MessageSettings(chat_mode=CHAT_MODE_SINGLE).effective_chat_mode(), CHAT_MODE_SINGLE)
        self.assertEqual(MessageSettings(chat_mode=CHAT_MODE_FOLDER).effective_chat_mode(), CHAT_MODE_FOLDER)
        self.assertEqual(MessageSettings(chat_mode=CHAT_MODE_EVA).effective_chat_mode(), CHAT_MODE_EVA)
        # v1.0 keyword mode migrates to EVA at read time
        self.assertEqual(MessageSettings(chat_mode=CHAT_MODE_KEYWORD).effective_chat_mode(), CHAT_MODE_EVA)
        legacy = MessageSettings(chat_mode="", use_loaded_sequences=True, loaded_sequences=["a"])
        self.assertEqual(legacy.effective_chat_mode(), CHAT_MODE_FOLDER)
        self.assertEqual(MessageSettings(chat_mode="").effective_chat_mode(), CHAT_MODE_SINGLE)

    def test_folder_save_marks_loaded_sequences(self):
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            saved = store.save_message_settings(
                MessageSettings(chat_mode=CHAT_MODE_FOLDER, loaded_sequences=["a\nb"])
            )
            self.assertTrue(saved.use_loaded_sequences)
            self.assertEqual(saved.effective_chat_mode(), CHAT_MODE_FOLDER)
            reloaded = store.load_message_settings()
            self.assertEqual(reloaded.loaded_sequences, ["a\nb"])

    def test_username_placeholders_need_usernames(self):
        settings = MessageSettings(sequence="hi %username%", usernames="")
        self.assertIn("%username%", settings.sequence)
        self.assertEqual(settings.username_list(), [])
        with_handles = MessageSettings(sequence="hi {snap}", usernames="alice\nbob")
        self.assertEqual(with_handles.username_list(), ["alice", "bob"])

    def test_a_fresh_install_keeps_an_empty_sequence(self):
        # Go's model.DefaultMessageSettings() sequence is documented, but it is
        # deliberately not injected: the guided MESSAGE step is finished by a
        # loaded folder or a saved sequence, and a pre-filled default would fake
        # that ("An empty sequence does not finish the message step").
        self.assertEqual(
            GO_DEFAULT_MESSAGE_SEQUENCE.split("\n"),
            ["hello how are you", "ok i am checking. wait a bit."],
        )
        with tempfile.TemporaryDirectory() as temp:
            self.assertEqual(SettingsStore(temp).load_message_settings().sequence, "")
            self.assertEqual(MessageSettings().sequence_lines(), [])

    def test_a_v1_file_with_first_and_reply_message_still_loads(self):
        with tempfile.TemporaryDirectory() as temp:
            save_json(
                Path(temp) / "state" / "message.json",
                {"first_message": "hello", "reply_message": "how are you"},
            )
            settings = SettingsStore(temp).load_message_settings()
            self.assertEqual(settings.sequence_lines(), ["hello", "how are you"])
            self.assertEqual(legacy_message_sequence("hello", "  "), "hello")

    def test_an_empty_sequence_stays_empty_on_save(self):
        with tempfile.TemporaryDirectory() as temp:
            store = SettingsStore(temp)
            saved = store.save_message_settings(MessageSettings(sequence="   "))
            self.assertEqual(saved.sequence, "")
            raw = json.loads((Path(temp) / "state" / "message.json").read_text())
            self.assertEqual(raw["sequence"], "")


if __name__ == "__main__":
    unittest.main()


class ConsoleEchoTests(unittest.TestCase):
    def test_debug_echo_prints_the_same_line_the_dashboard_shows(self):
        import contextlib
        import io
        import tempfile

        from chitchatport.logging_util import ActivityLog

        with tempfile.TemporaryDirectory() as temp:
            quiet = ActivityLog(temp)
            loud = ActivityLog(temp, echo=True)
            out = io.StringIO()
            with contextlib.redirect_stdout(out):
                quiet.append("info", "silent by default")
                loud.append("warn", "NO REPLY | partner silent for 60s")
            printed = out.getvalue()
        self.assertNotIn("silent by default", printed)
        self.assertIn("WARN", printed)
        self.assertIn("NO REPLY | partner silent for 60s", printed)


class RangeSettingTests(unittest.TestCase):
    """Reply delay and skip-if-no-reply are min-max ranges; max is never below min."""

    def test_the_legacy_ms_range_is_still_read(self):
        # A file written by an older build keeps its configured delay.
        import tempfile
        from pathlib import Path

        from chitchatport.settings import SettingsStore, save_json

        with tempfile.TemporaryDirectory() as temp:
            save_json(Path(temp) / "state" / "general.json", {"reply_delay_ms": 500})
            settings = SettingsStore(temp).load_general_settings()
            # 0.5 s min with the old build's 3 s default max -> 1-3 s, rounded up
            self.assertEqual((settings.reply_delay_min_seconds, settings.reply_delay_max_seconds), (1, 3))
            self.assertEqual(settings.skip_if_no_reply_max_ms, 90000)  # untouched default
