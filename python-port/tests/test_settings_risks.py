"""The silent-bot detector: which saved settings make a run look dead.

Every rule is traced to the runner code it protects (responders.py: an empty
sequence / EVA mode / %username% without usernames; runner.py: the silence limit
and the page scan). The tests pin both directions: the risk is found, and the
one-click repair really removes it.
"""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from chitchatport.settings import (
    CHAT_MODE_EVA,
    CHAT_MODE_FOLDER,
    CHAT_MODE_SINGLE,
    FIRST_MESSAGE_BOT,
    FIRST_MESSAGE_USER,
    GO_DEFAULT_MESSAGE_SEQUENCE,
    RISK_BLOCK,
    RISK_NOTE,
    RISK_WARN,
    SETTINGS_RULES,
    GeneralSettings,
    MessageSettings,
    SettingsStore,
    rule_pass_detail,
    settings_facts,
    silent_bot_risks,
)


def risks(general: GeneralSettings | None = None, message: MessageSettings | None = None):
    general = (general or GeneralSettings()).normalize_ranges()
    message = message or MessageSettings()
    return {risk.code: risk for risk in silent_bot_risks(general, message)}


class EmptySequenceTests(unittest.TestCase):
    def test_a_fresh_install_cannot_send_anything(self):
        found = risks()
        self.assertEqual(list(found), ["empty-sequence"])
        risk = found["empty-sequence"]
        self.assertEqual(risk.level, RISK_BLOCK)
        self.assertIn("sequence box is empty", risk.detail)
        self.assertIn("skipping chats", risk.effect)
        self.assertEqual(risk.fix_label, "LOAD THE GO DEFAULT LINES")
        self.assertEqual(risk.goto, "message")

    def test_the_repair_is_the_two_go_default_lines(self):
        risk = risks()["empty-sequence"]
        self.assertEqual(risk.fix.message["chat_mode"], CHAT_MODE_SINGLE)
        self.assertEqual(risk.fix.message["sequence"], GO_DEFAULT_MESSAGE_SEQUENCE)
        self.assertEqual(len(GO_DEFAULT_MESSAGE_SEQUENCE.split("\n")), 2)

    def test_a_folder_whose_files_are_blank_is_blocked_too(self):
        found = risks(message=MessageSettings(chat_mode=CHAT_MODE_FOLDER, loaded_sequences=[" \n\t "]))
        self.assertEqual(found["empty-sequence"].level, RISK_BLOCK)
        self.assertIn("none of the loaded files", found["empty-sequence"].detail)
        self.assertEqual(found["empty-sequence"].fix.message["chat_mode"], CHAT_MODE_SINGLE)
        # and the folder risk names the same fallback
        self.assertIn("falls back to the single sequence", found["message-mode"].effect)

    def test_a_real_sequence_has_no_risk_at_all(self):
        found = risks(message=MessageSettings(sequence="hello there\nhow are you"))
        self.assertEqual(found, {})


class ModeTests(unittest.TestCase):
    def test_eva_mode_is_blocked_and_repaired_to_the_working_mode(self):
        found = risks(message=MessageSettings(chat_mode=CHAT_MODE_EVA, sequence="hello\nbye"))
        self.assertEqual(found["eva-mode"].level, RISK_BLOCK)
        self.assertIn("Phase 2", found["eva-mode"].detail)
        self.assertEqual(found["eva-mode"].fix.message["chat_mode"], CHAT_MODE_SINGLE)
        self.assertNotIn("empty-sequence", found, "the single sequence has lines")

    def test_eva_mode_with_loaded_files_repairs_to_folder_mode(self):
        found = risks(
            message=MessageSettings(
                chat_mode=CHAT_MODE_EVA, loaded_sequences=["hello\nbye"], use_loaded_sequences=True
            )
        )
        self.assertEqual(found["eva-mode"].fix.message["chat_mode"], CHAT_MODE_FOLDER)

    def test_folder_mode_without_files_warns_and_can_switch_to_single(self):
        found = risks(message=MessageSettings(chat_mode=CHAT_MODE_FOLDER, sequence="hello"))
        risk = found["message-mode"]
        self.assertEqual(risk.level, RISK_WARN)
        self.assertIn("folder_files=0", risk.detail)
        self.assertEqual(risk.fix.message["chat_mode"], CHAT_MODE_SINGLE)
        self.assertNotIn("empty-sequence", found)

    def test_folder_mode_with_files_is_a_pass_not_a_risk(self):
        found = risks(message=MessageSettings(chat_mode=CHAT_MODE_FOLDER, loaded_sequences=["hi\nthere"]))
        self.assertEqual(found, {})
        facts = settings_facts(GeneralSettings().normalize_ranges(), MessageSettings(
            chat_mode=CHAT_MODE_FOLDER, loaded_sequences=["hi\nthere"]
        ))
        self.assertEqual(facts["effective_source"], "folder")
        self.assertEqual(facts["effective_lines"], ["hi", "there"])


class UsernameTests(unittest.TestCase):
    def test_a_username_line_without_usernames_is_blocked_and_has_no_auto_fix(self):
        found = risks(message=MessageSettings(sequence="hi %username%\nbye"))
        risk = found["usernames-missing"]
        self.assertEqual(risk.level, RISK_BLOCK)
        self.assertIsNone(risk.fix, "only the operator knows the handle")
        self.assertEqual(risk.fix_label, "")
        self.assertEqual(risk.goto, "message")
        self.assertIn("REPLY ENGINE FAILED", risk.effect)

    def test_the_snap_token_counts_as_well(self):
        found = risks(message=MessageSettings(sequence="hi there", loaded_sequences=["{snap} time"]))
        self.assertIn("usernames-missing", found)

    def test_saved_usernames_clear_the_risk(self):
        found = risks(message=MessageSettings(sequence="hi %username%\nbye", usernames="alice"))
        self.assertEqual(found, {})


class TimingTests(unittest.TestCase):
    def test_a_silence_limit_under_the_reply_delay_is_flagged_with_its_numbers(self):
        general = GeneralSettings(
            reply_delay_min_seconds=3,
            reply_delay_max_seconds=10,
            skip_if_no_reply_ms=7000,
            skip_if_no_reply_max_ms=9000,
        ).normalize_ranges()
        found = risks(general, MessageSettings(sequence="a\nb"))
        risk = found["timings"]
        self.assertEqual(risk.level, RISK_WARN)
        self.assertEqual(risk.fix.general["skip_if_no_reply_ms"], 60000)
        self.assertEqual(risk.fix.general["skip_if_no_reply_max_ms"], 90000)
        self.assertIn("Reply delay=3-10s Skip if no reply=7-9s", risk.detail)

    def test_the_go_defaults_pass(self):
        general = GeneralSettings().normalize_ranges()
        found = risks(general, MessageSettings(sequence="a\nb"))
        self.assertEqual(found, {})
        detail = rule_pass_detail("timings", settings_facts(general, MessageSettings(sequence="a\nb")))
        self.assertEqual(detail, "Reply delay=5-10s Skip if no reply=60-90s")

    def test_zero_means_off_and_is_only_a_note(self):
        general = GeneralSettings(skip_if_no_reply_ms=0, skip_if_no_reply_max_ms=0).normalize_ranges()
        found = risks(general, MessageSettings(sequence="a\nb"))
        self.assertEqual(found["skip-off"].level, RISK_NOTE)
        self.assertIn("off", found["skip-off"].detail)
        self.assertNotIn("timings", found, "off is not a timing mistake")

    def test_a_slow_page_scan_is_flagged_with_the_go_value(self):
        general = GeneralSettings(poll_interval_ms=5000).normalize_ranges()
        found = risks(general, MessageSettings(sequence="a\nb"))
        self.assertEqual(found["page-scan"].level, RISK_WARN)
        self.assertEqual(found["page-scan"].fix.general["poll_interval_ms"], 350)

    def test_one_line_with_bot_writes_first_is_only_a_note(self):
        found = risks(message=MessageSettings(sequence="only line"))
        self.assertEqual(found["one-line-sequence"].level, RISK_NOTE)
        self.assertIsNone(found["one-line-sequence"].fix)
        # the same single line is fine when the partner writes first
        found = risks(
            GeneralSettings(first_message_mode=FIRST_MESSAGE_USER).normalize_ranges(),
            MessageSettings(sequence="only line"),
        )
        self.assertNotIn("one-line-sequence", found)


class OrderingAndShapeTests(unittest.TestCase):
    def test_blocks_come_first_then_warnings_then_notes(self):
        general = GeneralSettings(
            reply_delay_max_seconds=30, skip_if_no_reply_ms=1000, skip_if_no_reply_max_ms=1000
        ).normalize_ranges()
        message = MessageSettings(chat_mode=CHAT_MODE_FOLDER)
        levels = [risk.level for risk in silent_bot_risks(general, message)]
        self.assertEqual(levels, [RISK_BLOCK, RISK_WARN, RISK_WARN])

    def test_the_payload_is_json_safe(self):
        for risk in silent_bot_risks(GeneralSettings().normalize_ranges(), MessageSettings()):
            json.dumps(risk.payload())

    def test_every_rule_has_a_pass_detail_and_the_engine_knows_it(self):
        facts = settings_facts(GeneralSettings().normalize_ranges(), MessageSettings(sequence="a\nb"))
        produced = set()
        for code, label in SETTINGS_RULES:
            self.assertTrue(code and label)
            self.assertTrue(rule_pass_detail(code, facts).strip(), code)
        for general, message in (
            (GeneralSettings(), MessageSettings()),
            (GeneralSettings(), MessageSettings(chat_mode=CHAT_MODE_FOLDER)),
            (GeneralSettings(), MessageSettings(chat_mode=CHAT_MODE_EVA, sequence="a\nb")),
            (GeneralSettings(), MessageSettings(sequence="hi %username%")),
            (GeneralSettings(skip_if_no_reply_ms=0, skip_if_no_reply_max_ms=0), MessageSettings(sequence="a")),
            (
                GeneralSettings(reply_delay_max_seconds=20, skip_if_no_reply_ms=1000, skip_if_no_reply_max_ms=1000),
                MessageSettings(sequence="a\nb"),
            ),
            (GeneralSettings(poll_interval_ms=4000), MessageSettings(sequence="a\nb")),
        ):
            for risk in silent_bot_risks(general.normalize_ranges(), message):
                produced.add(risk.code)
        known = {code for code, _label in SETTINGS_RULES}
        self.assertTrue(
            produced <= known,
            "these risk codes have no CHECK SYSTEM rule: {}".format(sorted(produced - known)),
        )


class RepairRemovesTheRiskTests(unittest.TestCase):
    """The one-click repair has to survive a real save (normalize + store)."""

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.store = SettingsStore(Path(self.temp.name))

    def repair(self, code, general: GeneralSettings, message: MessageSettings) -> list[str]:
        risk = {item.code: item for item in silent_bot_risks(general, message)}[code]
        self.assertIsNotNone(risk.fix, "{} carries no repair".format(code))
        if risk.fix.general:
            current = self.store.load_general_settings().__dict__
            current.update(risk.fix.general)
            general = self.store.save_general_settings(GeneralSettings(**current))
        if risk.fix.message:
            current = self.store.load_message_settings().__dict__
            current.update(risk.fix.message)
            message = self.store.save_message_settings(MessageSettings(**current))
        return [item.code for item in silent_bot_risks(general, message)]

    def test_every_repair_removes_its_own_risk(self):
        cases = [
            ("empty-sequence", GeneralSettings(), MessageSettings()),
            ("eva-mode", GeneralSettings(), MessageSettings(chat_mode=CHAT_MODE_EVA, sequence="a\nb")),
            ("message-mode", GeneralSettings(), MessageSettings(chat_mode=CHAT_MODE_FOLDER, sequence="a\nb")),
            (
                "timings",
                GeneralSettings(reply_delay_max_seconds=10, skip_if_no_reply_ms=7000, skip_if_no_reply_max_ms=9000),
                MessageSettings(sequence="a\nb"),
            ),
            ("page-scan", GeneralSettings(poll_interval_ms=5000), MessageSettings(sequence="a\nb")),
            ("skip-off", GeneralSettings(skip_if_no_reply_ms=0, skip_if_no_reply_max_ms=0), MessageSettings(sequence="a\nb")),
        ]
        for code, general, message in cases:
            with self.subTest(code=code):
                general = general.normalize_ranges()
                left = self.repair(code, general, message)
                self.assertNotIn(code, left, "{} is still a risk after its own repair".format(code))

    def test_the_message_repairs_never_lose_the_saved_sequence(self):
        self.store.save_message_settings(
            MessageSettings(chat_mode=CHAT_MODE_EVA, sequence="keep me\nand me")
        )
        stored = self.store.load_message_settings()
        risk = {item.code: item for item in silent_bot_risks(
            GeneralSettings().normalize_ranges(), stored
        )}["eva-mode"]
        current = stored.__dict__
        current.update(risk.fix.message)
        saved = self.store.save_message_settings(MessageSettings(**current))
        self.assertEqual(saved.sequence_lines(), ["keep me", "and me"])
        self.assertEqual(saved.effective_chat_mode(), CHAT_MODE_SINGLE)


if __name__ == "__main__":
    unittest.main()
