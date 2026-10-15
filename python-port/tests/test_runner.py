"""Runner tests: direct ports of the Go tests in ``live_chat_test.go``.

Same scenarios, same expectations, same log strings - so the Python runner can
be compared with the Go one case by case.
"""

from __future__ import annotations

import random
import threading
import time
import unittest
from dataclasses import replace

from chitchatport.domchat import DOMControls, DOMMessage, DOMSnapshot
from chitchatport.responders import SequenceLiveResponder, make_responder
from chitchatport.runner import (
    LiveRunner,
    has_partner_row,
    newest_partner_row_id,
)


class RecordingLog:
    def __init__(self):
        self._lock = threading.Lock()
        self.lines: list[str] = []

    def __call__(self, level: str, message: str) -> None:
        with self._lock:
            self.lines.append(message)

    def contains(self, text: str) -> bool:
        with self._lock:
            return any(text in line for line in self.lines)

    def count(self, text: str) -> int:
        with self._lock:
            return sum(1 for line in self.lines if text in line)

    def messages(self, text: str) -> list[str]:
        with self._lock:
            return [line for line in self.lines if text in line]


def wait_for(what: str, condition, timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if condition():
            return
        time.sleep(0.02)
    raise AssertionError("timed out waiting for " + what)


class SequenceUsernameGateTests(unittest.TestCase):
    """The rule that stops Show Mode from replying without a username."""

    def test_a_username_line_without_usernames_is_refused_like_go(self):
        from chitchatport.settings import MessageSettings

        settings = MessageSettings(sequence="hello %username%\nhow are you")
        with self.assertRaises(ValueError) as caught:
            make_responder(settings, random.Random(1))
        self.assertEqual(
            str(caught.exception), "Show Mode sequence needs a Message \u2192 Usernames entry"
        )

    def test_a_sequence_without_the_placeholder_needs_no_usernames(self):
        from chitchatport.settings import MessageSettings

        responder, mode = make_responder(MessageSettings(sequence="hello there"), random.Random(1))
        self.assertEqual(mode, "single")
        self.assertIsInstance(responder, SequenceLiveResponder)

    def test_usernames_make_the_same_sequence_usable(self):
        from chitchatport.settings import MessageSettings

        settings = MessageSettings(sequence="hi {snap}", usernames="volcanic flap")
        responder, mode = make_responder(settings, random.Random(1))
        self.assertEqual(mode, "single")
        self.assertIsInstance(responder, SequenceLiveResponder)


class FakeDOMBackend:
    """Port of the Go ``fakeDOMBackend`` (scripted page, no browser)."""

    def __init__(self, controls: DOMControls | None = None, fail_send: bool = False):
        self._lock = threading.Lock()
        self.controls = controls if controls is not None else DOMControls(has_input=True)
        self.rows: list[DOMMessage] = []
        self.sent: list[str] = []
        self.clicks: list[str] = []
        self.serial = 0
        self.generation = 0
        self.fail_send = fail_send
        self.send_hook = None
        self._phase = ""
        self._partner = ""

    def add_row(self, author: str, text: str, order: int = 0) -> None:
        with self._lock:
            self.serial += 1
            self.rows.append(
                DOMMessage(id="row-{}".format(self.serial), author=author, text=text, order=order)
            )

    def read_dom_snapshot(self) -> DOMSnapshot:
        with self._lock:
            controls = self.controls
            phase = controls.phase or (
                "chatting" if (controls.has_input and not controls.chat_ended) else "lobby"
            )
            partner = controls.partner or "partner"
            own_name = controls.own_name or "me"
            if phase != self._phase or partner != self._partner:
                self.generation += 1
                self._phase = phase
                self._partner = partner
            resolved = replace(
                controls,
                phase=phase,
                partner=partner,
                own_name=own_name,
                conversation=controls.conversation or "fake-{}".format(self.generation),
                can_send=phase == "chatting",
                observer_ready=True,
            )
            return DOMSnapshot(controls=resolved, messages=list(self.rows))

    def send_dom_message(self, text: str) -> bool:
        with self._lock:
            self.sent.append(text)
            if self.send_hook is not None:
                self.send_hook()
            if self.fail_send:
                return False
            self.serial += 1
            self.rows.append(
                DOMMessage(id="own-{}".format(self.serial), author="me", text=text, order=self.serial)
            )
            return True

    def click_dom_button(self, labels: list[str]) -> str:
        with self._lock:
            for label in labels or []:
                for button in self.controls.buttons:
                    if button.strip().casefold() != label.strip().casefold():
                        continue
                    self.clicks.append(label)
                    upper = label.strip().upper()
                    if upper == "START":
                        self.controls = replace(
                            self.controls, has_input=True, chat_ended=False, buttons=["Skip"]
                        )
                        self.rows = []
                    elif upper == "SKIP":
                        self.controls = replace(self.controls, has_input=False, buttons=["START"])
                        self.rows = []
                    return label
        return ""

    def resolve_last_author(self, author: str) -> None:
        """Late author resolution, as the Go test pokes its fake rows."""
        with self._lock:
            self.rows[-1] = replace(self.rows[-1], author=author)

    def dom_probe(self) -> str:
        return "fake probe"

    def close(self) -> None:
        pass

    def total_sent(self) -> int:
        with self._lock:
            return len(self.sent)

    def sent_count(self, text: str) -> int:
        with self._lock:
            return sum(1 for item in self.sent if item == text)

    def click_count(self, label: str) -> int:
        with self._lock:
            return sum(1 for item in self.clicks if item == label)


class RecordingResponder:
    def __init__(self, start_lines=("opener",), reply_lines=("ok reply",), end: bool = False):
        self._lock = threading.Lock()
        self.starts = 0
        self.incoming: list[str] = []
        self.start_lines = list(start_lines)
        self.reply_lines = list(reply_lines)
        self.end = end

    def start(self) -> list[str]:
        with self._lock:
            self.starts += 1
        return list(self.start_lines)

    def reply(self, text: str):
        with self._lock:
            self.incoming.append(text)
        return list(self.reply_lines), self.end

    def snapshot(self):
        with self._lock:
            return self.starts, list(self.incoming)


class FakeClock:
    """Deterministic clock: only the test decides when time passes."""

    def __init__(self, now: float = 1000.0):
        self.now = float(now)

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float) -> None:
        self.now += float(seconds)


class RunnerTestCase(unittest.TestCase):
    def start_runner(self, runner: LiveRunner, stop: threading.Event) -> None:
        thread = threading.Thread(target=runner.run, daemon=True)
        thread.start()

        def cleanup() -> None:
            stop.set()
            thread.join(timeout=3)

        self.addCleanup(cleanup)

    def make_runner(
        self,
        fake: FakeDOMBackend,
        log: RecordingLog,
        responder: RecordingResponder,
        mode: str = "bot",
        rng=None,
        reply_delay: float = 0.01,
        poll_interval: float = 0.02,
        no_reply_seconds: float = 0.0,
        clock=None,
        reply_delay_max=None,
        no_reply_max_seconds=None,
    ):
        stop = threading.Event()
        kwargs = {}
        if clock is not None:
            kwargs["clock"] = clock
        if reply_delay_max is not None:
            kwargs["reply_delay_max"] = reply_delay_max
        if no_reply_max_seconds is not None:
            kwargs["no_reply_max_seconds"] = no_reply_max_seconds
        runner = LiveRunner(
            dom=fake,
            prefix="[test show-live 1/1]",
            logf=log,
            new_responder=lambda: (responder, "test"),
            first_message_mode=lambda: mode,
            rng=rng,
            reply_delay=reply_delay,
            poll_interval=poll_interval,
            no_reply_seconds=no_reply_seconds,
            stop=stop,
            **kwargs,
        )
        return runner, stop


class UserFirstTests(RunnerTestCase):
    def test_user_first_waits_for_partner_and_replies_once(self):
        fake = FakeDOMBackend()
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="user")
        self.start_runner(runner, stop)

        wait_for("chat start without opener", lambda: log.contains("first=user"))
        self.assertTrue(log.contains("WAITING FIRST SMS | user writes first; no opener sent"))
        self.assertEqual(responder.snapshot(), (0, []))
        self.assertEqual(fake.total_sent(), 0)

        fake.add_row("partner", "hi there")
        wait_for("reply to the partner", lambda: fake.sent_count("ok reply") == 1)
        time.sleep(0.2)
        self.assertEqual(responder.snapshot(), (0, ["hi there"]))
        self.assertEqual(fake.total_sent(), 1)

    def test_user_first_answers_only_newest_partner_row(self):
        fake = FakeDOMBackend()
        fake.add_row("partner", "old message")
        fake.add_row("partner", "newest message")
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="user")
        self.start_runner(runner, stop)

        wait_for("reply to the newest row", lambda: fake.sent_count("ok reply") == 1)
        time.sleep(0.2)
        self.assertEqual(responder.snapshot()[1], ["newest message"])
        self.assertEqual(fake.total_sent(), 1)

    def test_user_first_does_not_repeat_an_answered_row(self):
        fake = FakeDOMBackend()
        fake.add_row("partner", "already handled")
        fake.add_row("me", "my earlier reply")
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="user")
        self.start_runner(runner, stop)

        wait_for("conversation with an answered row", lambda: log.contains("WAITING NEXT SMS"))
        self.assertFalse(log.contains("WAITING FIRST SMS"))
        time.sleep(0.2)
        self.assertEqual(responder.snapshot()[1], [])
        self.assertEqual(fake.total_sent(), 0)

    def test_user_first_logs_first_sms_answered_once(self):
        fake = FakeDOMBackend()
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="user")
        self.start_runner(runner, stop)

        wait_for("waiting for the first partner message", lambda: log.contains("WAITING FIRST SMS"))
        fake.add_row("partner", "hello first")
        wait_for("first reply confirmed", lambda: fake.sent_count("ok reply") == 1)
        wait_for("first sms log", lambda: log.count("FIRST SMS ANSWERED") == 1)

        fake.add_row("partner", "second message")
        wait_for("second reply confirmed", lambda: fake.sent_count("ok reply") == 2)
        time.sleep(0.15)
        self.assertEqual(log.count("FIRST SMS ANSWERED"), 1)
        self.assertEqual(fake.total_sent(), 2)


class ReplyDelayLogTests(RunnerTestCase):
    """The Go Show Mode runner logs the delay it picked; the port must too."""

    def test_every_accepted_row_logs_the_selected_delay_and_the_setting(self):
        fake = FakeDOMBackend()
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", reply_delay=5.0, reply_delay_max=10.0
        )
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        fake.add_row("partner", "hello there")
        wait_for("the reply delay line", lambda: log.contains("REPLY DELAY |"))

        line = log.messages("REPLY DELAY |")[0]
        self.assertIn("setting=5-10s", line)
        selected = float(line.split("selected=")[1].split("s ")[0])
        self.assertGreaterEqual(selected, 5.0)
        self.assertLessEqual(selected, 10.0)

    def test_a_fixed_range_logs_the_same_value_twice(self):
        fake = FakeDOMBackend()
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", reply_delay=7.0, reply_delay_max=7.0
        )
        self.start_runner(runner, stop)
        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        fake.add_row("partner", "hi")
        wait_for("the reply delay line", lambda: log.contains("REPLY DELAY |"))
        self.assertIn("selected=7s setting=7-7s", log.messages("REPLY DELAY |")[0])


class BotFirstTests(RunnerTestCase):
    def test_default_mode_keeps_bot_opener(self):
        fake = FakeDOMBackend()
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="bot")
        self.start_runner(runner, stop)

        wait_for("opener sent", lambda: fake.sent_count("opener") == 1)
        self.assertTrue(log.contains("first=bot"))
        time.sleep(0.2)
        self.assertEqual(responder.snapshot()[0], 1)
        self.assertEqual(fake.total_sent(), 1)

    def test_send_unconfirmed_pauses_without_resend(self):
        fake = FakeDOMBackend(fail_send=True)
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="bot")
        self.start_runner(runner, stop)

        wait_for("unconfirmed send", lambda: log.contains("SEND UNCONFIRMED"))
        self.assertTrue(runner.paused)
        time.sleep(0.3)
        self.assertEqual(fake.total_sent(), 1)  # never resends automatically

    def test_unknown_author_is_never_answered(self):
        fake = FakeDOMBackend()
        log = RecordingLog()
        responder = RecordingResponder()
        runner, stop = self.make_runner(fake, log, responder, mode="bot")
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        # Rows that arrive while the chat runs are not history.
        fake.add_row("stranger", "who are you?")
        wait_for("unknown author guard", lambda: log.contains("AUTHOR UNKNOWN"))
        time.sleep(0.2)
        self.assertEqual(responder.snapshot()[1], [])
        self.assertEqual(fake.total_sent(), 1)  # only the opener

        # A late author resolution must not lose that unread message.
        fake.resolve_last_author("partner")
        wait_for("late identity answered", lambda: fake.sent_count("ok reply") == 1)
        time.sleep(0.15)
        self.assertEqual(fake.total_sent(), 2)

    def test_end_of_sequence_skips_once(self):
        fake = FakeDOMBackend(controls=DOMControls(has_input=True, buttons=["SKIP"]))
        log = RecordingLog()
        responder = RecordingResponder(start_lines=("only line",), reply_lines=(), end=True)
        runner, stop = self.make_runner(fake, log, responder, mode="bot")
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("only line") == 1)
        fake.add_row("partner", "hello")
        wait_for("skip clicked", lambda: fake.click_count("SKIP") == 1)
        time.sleep(0.2)
        self.assertIn("SKIP | clicked active SKIP", "\n".join(log.lines))
        self.assertEqual(fake.click_count("SKIP"), 1)


class NoReplySkipTests(RunnerTestCase):
    """SKIP IF NO REPLY: leave a quiet partner instead of waiting forever."""

    def chatting(self) -> FakeDOMBackend:
        return FakeDOMBackend(controls=DOMControls(has_input=True, buttons=["SKIP"]))

    def test_a_silent_partner_is_left_and_the_next_match_starts(self):
        fake = self.chatting()
        log = RecordingLog()
        responder = RecordingResponder(start_lines=("opener",))
        clock = FakeClock()
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", no_reply_seconds=10.0, clock=clock
        )
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        time.sleep(0.1)
        self.assertEqual(fake.click_count("SKIP"), 0, "skipped before the partner went quiet")

        clock.advance(10.0)
        wait_for("skip after the silence", lambda: fake.click_count("SKIP") == 1)
        self.assertTrue(log.contains("NO REPLY | partner silent for 10s"))
        # The next conversation starts clean, with a fresh opener.
        wait_for("next match", lambda: fake.sent_count("opener") == 2)

    def test_an_incoming_message_restarts_the_silence_clock(self):
        fake = self.chatting()
        log = RecordingLog()
        responder = RecordingResponder(start_lines=("opener",))
        clock = FakeClock()
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", no_reply_seconds=10.0, clock=clock
        )
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        clock.advance(6.0)
        fake.add_row("partner", "hi there")
        wait_for("answer to the partner", lambda: fake.sent_count("ok reply") == 1)

        # 12s total, but only 6s since the partner spoke: not silent yet.
        clock.advance(6.0)
        time.sleep(0.2)
        self.assertEqual(fake.click_count("SKIP"), 0)
        self.assertTrue(responder.snapshot()[1] == ["hi there"])

    def test_zero_keeps_waiting_forever(self):
        fake = self.chatting()
        log = RecordingLog()
        responder = RecordingResponder(start_lines=("opener",))
        clock = FakeClock()
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", no_reply_seconds=0.0, clock=clock
        )
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        clock.advance(3600.0)
        time.sleep(0.2)
        self.assertEqual(fake.click_count("SKIP"), 0)
        self.assertFalse(log.contains("NO REPLY"))

    def test_user_first_mode_also_leaves_a_partner_who_never_writes(self):
        fake = self.chatting()
        log = RecordingLog()
        responder = RecordingResponder()
        clock = FakeClock()
        runner, stop = self.make_runner(
            fake, log, responder, mode="user", no_reply_seconds=10.0, clock=clock
        )
        self.start_runner(runner, stop)

        wait_for("waiting for the first message", lambda: log.contains("WAITING FIRST SMS"))
        time.sleep(0.1)
        self.assertEqual(fake.click_count("SKIP"), 0)
        clock.advance(10.0)
        wait_for("skip without an opener", lambda: fake.click_count("SKIP") == 1)
        self.assertEqual(fake.total_sent(), 0)

    def test_a_queued_message_is_never_cut_off(self):
        fake = self.chatting()
        log = RecordingLog()
        responder = RecordingResponder()
        clock = FakeClock()
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", no_reply_seconds=10.0, clock=clock
        )
        self.start_runner(runner, stop)

        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        fake.add_row("partner", "still here")
        clock.advance(600.0)  # the queue must be answered, not abandoned
        wait_for("queued row answered", lambda: fake.sent_count("ok reply") == 1)
        self.assertEqual(fake.click_count("SKIP"), 0)


class DecisionTests(unittest.TestCase):
    def make_bare_runner(self, mode: str, rng=None) -> LiveRunner:
        return LiveRunner(
            dom=FakeDOMBackend(),
            prefix="[test]",
            logf=lambda level, message: None,
            new_responder=lambda: (None, "test"),
            first_message_mode=lambda: mode,
            rng=rng,
        )

    def test_random_mode_decides_per_conversation(self):
        runner = self.make_bare_runner("random", random.Random(7))
        results = [runner.bot_first_for_new_conversation() for _ in range(40)]
        self.assertTrue(any(results), "random never chose the bot opener")
        self.assertTrue(not all(results), "random never waited for the partner")

    def test_random_without_rng_and_unknown_modes_keep_the_opener(self):
        self.assertTrue(self.make_bare_runner("random").bot_first_for_new_conversation())
        for mode in ("", "garbage", "bot", "USER"):
            self.assertTrue(self.make_bare_runner(mode).bot_first_for_new_conversation())
        self.assertFalse(self.make_bare_runner("user").bot_first_for_new_conversation())

    def test_mode_is_read_once_per_conversation(self):
        current = {"mode": "bot"}
        stop = threading.Event()
        runner = LiveRunner(
            dom=FakeDOMBackend(),
            prefix="[test]",
            logf=lambda level, message: None,
            new_responder=lambda: (None, "test"),
            first_message_mode=lambda: current["mode"],
            stop=stop,
        )
        runner.reset(DOMControls(conversation="c1", partner="p", own_name="me"))
        self.assertTrue(runner.bot_first)
        current["mode"] = "user"
        self.assertTrue(runner.bot_first)  # unchanged inside the same conversation
        runner.reset(DOMControls(conversation="c2", partner="p", own_name="me"))
        self.assertFalse(runner.bot_first)  # next chat picks up the saved change


class SequenceResponderTests(unittest.TestCase):
    def test_user_first_starts_from_first_line(self):
        responder = SequenceLiveResponder([["greet one", "reply one"]], random.Random(1))
        self.assertEqual(responder.reply("hi"), (["greet one"], False))
        self.assertEqual(responder.reply("ok"), (["reply one"], False))
        self.assertEqual(responder.reply("still there?"), ([], True))

    def test_bot_first_uses_line_one_then_continues(self):
        responder = SequenceLiveResponder([["first", "second", "third"]], random.Random(1))
        self.assertEqual(responder.start(), ["first"])
        self.assertEqual(responder.reply("hi"), (["second"], False))
        self.assertEqual(responder.reply("hi"), (["third"], False))
        self.assertEqual(responder.reply("hi"), ([], True))

    def test_one_sequence_per_conversation(self):
        responder = SequenceLiveResponder(
            [["a1", "a2"], ["b1", "b2"], ["c1", "c2"]], random.Random(3)
        )
        first = responder.start()
        second = responder.reply("x")[0]
        self.assertEqual(first[0][0], second[0][0])  # same chosen sequence
        self.assertTrue(first[0].startswith(second[0][0]))

    def test_empty_sequences_end_immediately(self):
        responder = SequenceLiveResponder([[], []], random.Random(1))
        self.assertEqual(responder.start(), [])
        self.assertEqual(responder.reply("hi"), ([], True))


class NewestRowTests(unittest.TestCase):
    def setUp(self):
        self.controls = DOMControls(partner="partner", own_name="me")

    def test_reported_order_wins(self):
        messages = [
            DOMMessage(id="row-new", author="partner", text="new", order=5),
            DOMMessage(id="row-old", author="partner", text="old", order=2),
        ]
        self.assertEqual(newest_partner_row_id(messages, self.controls), "row-new")

    def test_own_row_after_newest_partner_row_is_not_answered(self):
        messages = [
            DOMMessage(id="row-new", author="partner", text="new", order=5),
            DOMMessage(id="row-old", author="partner", text="old", order=2),
            DOMMessage(id="own", author="me", text="mine", order=6),
        ]
        self.assertEqual(newest_partner_row_id(messages, self.controls), "")

    def test_older_own_row_does_not_change_the_answer(self):
        messages = [
            DOMMessage(id="row-new", author="partner", text="new", order=5),
            DOMMessage(id="row-old", author="partner", text="old", order=2),
            DOMMessage(id="own-old", author="me", text="mine", order=1),
        ]
        self.assertEqual(newest_partner_row_id(messages, self.controls), "row-new")

    def test_orderless_snapshot_keeps_slice_order_fallback(self):
        plain = [
            DOMMessage(id="p1", author="partner", text="one"),
            DOMMessage(id="p2", author="partner", text="two"),
        ]
        self.assertEqual(newest_partner_row_id(plain, self.controls), "p2")
        with_own = plain + [DOMMessage(id="own", author="me", text="mine")]
        self.assertEqual(newest_partner_row_id(with_own, self.controls), "")

    def test_has_partner_row_ignores_empty_ids(self):
        self.assertFalse(
            has_partner_row([DOMMessage(id="", author="partner", text="x")], self.controls)
        )
        self.assertTrue(
            has_partner_row([DOMMessage(id="r1", author="Partner", text="x")], self.controls)
        )


if __name__ == "__main__":
    unittest.main()


class RandomRangeTests(RunnerTestCase):
    """Reply delay and the silence limit are drawn from min-max ranges."""

    class FixedRng:
        """random.Random stand-in that returns a chosen fraction of the range."""

        def __init__(self, fraction: float):
            self.fraction = fraction
            self.calls: list[tuple[float, float]] = []

        def uniform(self, low, high):
            self.calls.append((low, high))
            return low + (high - low) * self.fraction

        def random(self):
            return 0.0

        def choice(self, seq):
            return seq[0]

        def randint(self, a, b):
            return a

    def test_pick_seconds_stays_inside_the_range_and_fixed_when_equal(self):
        fake = FakeDOMBackend(controls=DOMControls(has_input=True, buttons=["SKIP"]))
        rng = self.FixedRng(0.5)
        runner, _ = self.make_runner(
            fake, RecordingLog(), RecordingResponder(), rng=rng,
            reply_delay=1.0, reply_delay_max=3.0, no_reply_seconds=60.0, no_reply_max_seconds=90.0,
        )
        self.assertEqual(runner.pick_seconds(runner.reply_delay, runner.reply_delay_max), 2.0)
        self.assertEqual(runner.pick_seconds(runner.no_reply_seconds, runner.no_reply_max_seconds), 75.0)
        self.assertEqual(runner.pick_seconds(5.0, 5.0), 5.0)
        self.assertEqual(rng.calls, [(1.0, 3.0), (60.0, 90.0)])  # the fixed case never asks the rng

    def test_a_max_below_min_becomes_a_fixed_value(self):
        fake = FakeDOMBackend(controls=DOMControls(has_input=True, buttons=["SKIP"]))
        runner, _ = self.make_runner(
            fake, RecordingLog(), RecordingResponder(), reply_delay=2.0, reply_delay_max=0.5,
            no_reply_seconds=30.0, no_reply_max_seconds=1.0,
        )
        self.assertEqual((runner.reply_delay, runner.reply_delay_max), (2.0, 2.0))
        self.assertEqual((runner.no_reply_seconds, runner.no_reply_max_seconds), (30.0, 30.0))

    def test_the_silence_limit_is_drawn_per_conversation(self):
        fake = FakeDOMBackend(controls=DOMControls(has_input=True, buttons=["SKIP"]))
        log = RecordingLog()
        responder = RecordingResponder(start_lines=("opener",))
        clock = FakeClock()
        rng = self.FixedRng(1.0)  # always the top of the range
        runner, stop = self.make_runner(
            fake, log, responder, mode="bot", rng=rng, no_reply_seconds=10.0, no_reply_max_seconds=20.0, clock=clock,
        )
        self.start_runner(runner, stop)
        wait_for("opener", lambda: fake.sent_count("opener") == 1)
        time.sleep(0.1)
        clock.advance(12.0)  # past the minimum, below the drawn limit (20)
        time.sleep(0.2)
        self.assertEqual(fake.click_count("SKIP"), 0, "left at the minimum although 20s was drawn")
        clock.advance(8.0)
        wait_for("skip at the drawn limit", lambda: fake.click_count("SKIP") == 1)
        self.assertTrue(log.contains("NO REPLY | partner silent for 20s"))
