"""Transcript feed tests (core rules of the ported ``homeChatFeed``)."""

from __future__ import annotations

import unittest

from chitchatport.domchat import DOMControls, DOMMessage, DOMSnapshot
from chitchatport.homefeed import HOME_CHAT_ROWS, HomeChatFeed


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


class HomeFeedTests(unittest.TestCase):
    def test_roles_order_and_dedup(self):
        feed = HomeChatFeed()
        run = feed.start("test")
        first = snapshot(
            "one",
            [
                row("u2", "PARTNER", "same", 3),
                row("u1", "partner", "same", 1),
                row("own", "SELF", "hi", 2),
                row("unknown", "", "not guessed", 4),
            ],
        )
        feed.observe(run, first, False)
        before = feed.snapshot("")
        feed.observe(run, first, False)
        after = feed.snapshot("")
        self.assertEqual([m["id"] for m in before["messages"]], ["u1", "own", "u2"])
        self.assertEqual([m["role"] for m in before["messages"]], ["user", "bot", "user"])
        self.assertEqual(before["version"], after["version"], "a rerender duplicated rows")
        self.assertEqual(len(after["messages"]), 3)

    def test_lobby_snapshot_never_replaces_the_transcript(self):
        feed = HomeChatFeed()
        run = feed.start("test")
        feed.observe(run, snapshot("one", [row("u1", "partner", "hello", 1)]), False)
        feed.observe(run, snapshot("one", [], phase="lobby"), False)
        state = feed.snapshot("")
        self.assertEqual(len(state["messages"]), 1)
        self.assertFalse(state["sessions"][0]["active"])

    def test_rows_are_bounded(self):
        feed = HomeChatFeed()
        run = feed.start("test")
        rows = [row("r{}".format(i), "partner", "m{}".format(i), i + 1) for i in range(HOME_CHAT_ROWS + 1)]
        feed.observe(run, snapshot("one", rows), False)
        state = feed.snapshot("")
        self.assertEqual(len(state["messages"]), HOME_CHAT_ROWS)
        self.assertEqual(state["sessions"][0]["dropped"], 1)

    def test_sessions_are_bounded_and_evicted(self):
        feed = HomeChatFeed()
        for index in range(14):
            run = feed.start("run{}".format(index))
            feed.observe(run, snapshot("conv{}".format(index), [row("u", "partner", "hi", 1)]), False)
        state = feed.snapshot("")
        self.assertLessEqual(len(state["sessions"]), 12)
        self.assertGreaterEqual(state["evicted"], 1)

    def test_late_receipt_never_switches_the_view(self):
        feed = HomeChatFeed()
        run = feed.start("test")
        feed.observe(run, snapshot("one", [row("u1", "partner", "hello", 1)]), False)
        # A receipt for a conversation that was never seeded must not open a session.
        feed.observe(run, snapshot("two", [row("u2", "partner", "late", 1)]), True)
        self.assertEqual(feed.snapshot("")["sessions"][0]["id"], "1/one")

    def test_line_stats_count_every_line_of_a_multiline_message(self):
        """USER lines are counted per line, not per bubble."""
        """USER lines must not be counted by bubble: 5 messages can be 9 lines."""
        feed = HomeChatFeed()
        run = feed.start("test")
        feed.observe(
            run,
            snapshot(
                "one",
                [
                    row("u1", "partner", "Hi\nM 27\nU?", 1),
                    row("own1", "self", "f21", 2),
                    row("u2", "partner", "From?", 3),
                ],
            ),
            False,
        )
        stats = feed.snapshot("")["stats"]
        self.assertEqual(stats["user_bubbles"], 2)
        self.assertEqual(stats["user_lines"], 4)
        self.assertEqual(stats["bot_bubbles"], 1)
        self.assertEqual(stats["bot_lines"], 1)
        self.assertEqual(stats["rows"], 3)

    def test_line_stats_are_empty_without_a_session(self):
        stats = HomeChatFeed().snapshot("")["stats"]
        self.assertEqual(
            stats, {"user_lines": 0, "user_bubbles": 0, "bot_lines": 0, "bot_bubbles": 0, "rows": 0}
        )


if __name__ == "__main__":
    unittest.main()
