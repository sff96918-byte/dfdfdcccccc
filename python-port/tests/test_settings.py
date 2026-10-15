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
    SettingsStore,
    message_sequence_lines,
    normalize_line_list,
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
                        "reply_delay_ms": 1234,
                    }
                )
            )
            settings = SettingsStore(temp).load_general_settings()
            self.assertEqual(settings.first_message_mode, FIRST_MESSAGE_BOT)
            self.assertEqual(settings.poll_min_seconds, GeneralSettings().poll_min_seconds)
            self.assertEqual(settings.reply_delay_ms, 1234)


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
