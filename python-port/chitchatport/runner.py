"""The DOM runner, ported 1:1 from ``live_chat.go`` (Show Mode only).

Behaviour kept identical on purpose:

* one decision per conversation for who writes first (``bot`` / ``user`` /
  ``random``), read through a callable so a saved Settings change applies from
  the next chat on;
* the first snapshot of a conversation is history; in user-first mode only the
  newest partner row is left unread (order-aware when the page reports order)
  and an already answered row is never answered twice;
* single-consumer queue with the same unknown-author guard, same Skip/START
  rotation, same send confirmation (a NEW own row), same "no automatic resend";
* the exact same activity log strings as the Go runner.
"""

from __future__ import annotations

import random
import threading
import time
from typing import Callable

from .domchat import (
    DOMControls,
    DOMMessage,
    DOMSnapshot,
    PageGoneError,
    message_line_count,
)
from .settings import FIRST_MESSAGE_RANDOM, FIRST_MESSAGE_USER

REPLY_DELAY_DEFAULT = 0.9  # replyDelay in runLiveChat
POLL_INTERVAL_DEFAULT = 0.35  # pollInterval in runLiveChat
START_CLICK_THROTTLE_SECONDS = 6.0
# A site that never lands a chat must not be hammered: each START that does not
# start a conversation doubles the wait up to this ceiling. Clicking START in a
# tight loop can restart the site's own match search, so the bot would keep
# cancelling the match it is waiting for (seen in the 2026-10-15 live log).
START_CLICK_MAX_SECONDS = 60.0
ROTATE_THROTTLE_SECONDS = 2.0
LINE_SPACING_SECONDS = 0.4
# Opt-in (dashboard): in user-writes-first mode a fresh chat may answer this many
# newest unread partner rows instead of treating all of them as history.
FIRST_BURST_MAX_ROWS = 3


def _go_bool(value: bool) -> str:
    return "true" if value else "false"


def seconds_label(seconds: float) -> str:
    """Readable seconds for logs: 60 stays 60, half a second stays 0.5."""
    return "{:g}".format(float(seconds))


def has_dom_button(buttons: list[str], label: str) -> bool:
    target = label.strip().casefold()
    return any(str(button).strip().casefold() == target for button in buttons or [])


def has_partner_row(messages: list[DOMMessage], controls: DOMControls) -> bool:
    """True when the snapshot already holds a partner message anywhere."""
    partner = controls.partner.casefold()
    return any(row.id and row.author.casefold() == partner for row in messages)


def newest_partner_row_id(messages: list[DOMMessage], controls: DOMControls) -> str:
    """Newest partner row while no own row follows it (else nothing to answer)."""
    by_order, decided = newest_partner_row_id_by_order(messages, controls)
    if decided:
        return by_order
    own = controls.own_name.casefold()
    partner = controls.partner.casefold()
    for row in reversed(messages):
        if row.author and row.author.casefold() == own:
            return ""
        if row.id and row.author.casefold() == partner:
            return row.id
    return ""


def newest_partner_row_id_by_order(
    messages: list[DOMMessage], controls: DOMControls
) -> tuple[str, bool]:
    """Order-aware answer; decided=False keeps the slice-order fallback."""
    if not any(row.order > 0 for row in messages):
        return "", False
    own = controls.own_name.casefold()
    partner = controls.partner.casefold()
    newest_id, newest_order, own_order = "", 0, 0
    for row in messages:
        if row.author and row.author.casefold() == own:
            own_order = max(own_order, row.order)
            continue
        if row.id and row.author.casefold() == partner and row.order > newest_order:
            newest_id, newest_order = row.id, row.order
    if newest_id == "":
        return "", False
    if own_order > newest_order:
        return "", True
    return newest_id, True


def unread_partner_row_ids(
    messages: list[DOMMessage], controls: DOMControls, limit: int
) -> set[str]:
    """The newest ``limit`` partner rows that no own row follows (order-aware).

    ``newest_partner_row_id`` answers exactly one row (the Go rule). The
    dashboard's optional "answer the opening burst" mode needs the same safety -
    only rows after the newest own row - but for the newest few rows at once, so
    a partner who sends three lines before the first poll is not half ignored.
    """
    if limit <= 0:
        return set()
    partner = controls.partner.casefold()
    own = controls.own_name.casefold()
    rows = [
        (position, row)
        for position, row in enumerate(messages)
        if row.id and row.author.casefold() == partner
    ]
    if not rows:
        return set()
    if any(row.order > 0 for row in messages):
        own_order = 0
        for row in messages:
            if row.author and row.author.casefold() == own:
                own_order = max(own_order, row.order)
        rows = [(position, row) for position, row in rows if row.order > own_order]
        rows.sort(key=lambda item: (item[1].order, item[0]))
    else:
        rows.sort(key=lambda item: item[0])
    return {row.id for _, row in rows[-limit:]}


class LiveRunner:
    def __init__(
        self,
        dom,
        prefix: str,
        logf: Callable[[str, str], None],
        new_responder: Callable[[], tuple[object, str]],
        first_message_mode: Callable[[], str] | None = None,
        rng=None,
        reply_delay: float = REPLY_DELAY_DEFAULT,
        poll_interval: float = POLL_INTERVAL_DEFAULT,
        no_reply_seconds: float = 0.0,
        stop: threading.Event | None = None,
        clock: Callable[[], float] = time.monotonic,
        reply_delay_max: float | None = None,
        no_reply_max_seconds: float | None = None,
        answer_all_rows: Callable[[], bool] | None = None,
        on_state: Callable[[str, str], None] | None = None,
    ):
        self.dom = dom
        self.prefix = prefix
        self.logf = logf
        self.new_responder = new_responder
        self.first_message_mode = first_message_mode
        # Optional dashboard rule (user writes first): answer the partner's
        # opening burst instead of only the newest row. Read once per chat, like
        # the who-writes-first decision.
        self.answer_all_rows = answer_all_rows
        # Optional status sink: the dashboard shows lobby / matching / chatting /
        # paused instead of a bare RUNNING.
        self.on_state = on_state
        self.rng = rng
        # Both waits are MIN-MAX ranges (like Go's sleepRandom): a fresh random
        # value inside the range per reply / per conversation, so the bot never
        # answers or gives up with the same rhythm twice. max < min means "fixed".
        self.reply_delay = max(0.0, float(reply_delay or 0.0))
        self.reply_delay_max = max(self.reply_delay, float(reply_delay_max if reply_delay_max is not None else reply_delay) or 0.0)
        self.poll_interval = poll_interval
        # Show Mode extra (dashboard SKIP IF NO REPLY): 0 keeps the old
        # behaviour of waiting forever for a quiet partner.
        self.no_reply_seconds = max(0.0, float(no_reply_seconds or 0.0))
        self.no_reply_max_seconds = max(
            self.no_reply_seconds, float(no_reply_max_seconds if no_reply_max_seconds is not None else no_reply_seconds) or 0.0
        )
        # The limit drawn for the conversation currently being served.
        self.no_reply_limit = self.no_reply_seconds
        self._stop = stop if stop is not None else threading.Event()
        self.clock = clock

        self.conversation = ""
        self.controls = DOMControls()
        self.responder = None
        self.seen_ids: set[str] = set()
        self.queued_ids: set[str] = set()
        self.queue: list[DOMMessage] = []
        self.paused = False
        self.rotating = False
        self.bot_first = False
        self.answer_first_burst = False
        self.first_sms_answered = False
        # Run-wide: the START throttle must survive a conversation change, else a
        # page that flips phase every few seconds lets the bot click START again
        # immediately (that restarts the site's own match search).
        self.last_start_at = 0.0
        self.start_attempts = 0
        self.last_rotate_at = 0.0
        self.last_step_error = ""
        # Cached in-page probe for the condition currently being logged.
        self.probe_key = ""
        self.probe_text = ""
        self.probe_logged = False
        # What the operator sees instead of a bare RUNNING, plus how many lines
        # the partner really sent and how many the bot really sent.
        self.activity = ""
        self.activity_note = ""
        self.user_lines = 0
        self.bot_lines = 0
        # Last time either side spoke; None while no conversation is being served.
        self.idle_since: float | None = None

    # -- lifecycle ---------------------------------------------------------

    def cancelled(self) -> bool:
        return self._stop.is_set()

    def run(self) -> None:
        while True:
            if self.cancelled() or self.step():
                return
            if self._stop.wait(self.poll_interval):
                return

    def sleep_live(self, seconds: float) -> bool:
        """True when the run was cancelled (mirrors ``sleepLive``)."""
        if seconds <= 0:
            return self.cancelled()
        return self._stop.wait(seconds)

    def pick_seconds(self, low: float, high: float) -> float:
        """Uniform value inside [low, high]; a degenerate range is fixed."""
        if high <= low:
            return low
        rng = self.rng if self.rng is not None else random
        return rng.uniform(low, high)

    def arm_silence_clock(self) -> None:
        """(Re)start the no-reply clock with a fresh random limit."""
        self.idle_since = self.clock()
        self.no_reply_limit = self.pick_seconds(self.no_reply_seconds, self.no_reply_max_seconds)

    # -- per-conversation decision ----------------------------------------

    def bot_first_for_new_conversation(self) -> bool:
        mode = ""
        if self.first_message_mode is not None:
            mode = self.first_message_mode() or ""
        if mode == FIRST_MESSAGE_USER:
            return False
        if mode == FIRST_MESSAGE_RANDOM:
            if self.rng is not None:
                return self.rng.randrange(2) == 0
            return True  # never silently wait forever without an rng
        return True

    def first_message_label(self) -> str:
        return "bot" if self.bot_first else "user"

    def answer_all_for_new_conversation(self) -> bool:
        """Dashboard option: answer a fresh chat's opening burst (or not)."""
        if self.answer_all_rows is None:
            return False
        try:
            return bool(self.answer_all_rows())
        except Exception:  # noqa: BLE001 - a broken setting never stops a run
            return False

    def publish(self, activity: str, note: str = "") -> None:
        """Report lobby / matching / chatting / paused to the dashboard once."""
        note = str(note or "")
        if activity == self.activity and note == self.activity_note:
            return
        self.activity = activity
        self.activity_note = note
        if self.on_state is None:
            return
        try:
            self.on_state(activity, note)
        except Exception:  # noqa: BLE001 - status is display only
            pass

    def control_detail(self, controls: DOMControls, rows: int) -> str:
        """Why a chat could not be served: the exact page facts, not a guess."""
        label = ",".join(controls.buttons) if controls.buttons else "none-or-busy"
        return (
            "[phase={} can_send={} label={} composer={} own={!r} partner={!r} rows={} overflow={}]".format(
                controls.phase or "-",
                _go_bool(controls.can_send),
                label,
                "yes" if controls.has_input else "no",
                controls.own_name,
                controls.partner,
                rows,
                _go_bool(controls.overflow),
            )
        )

    def probe_detail(self, key: str) -> str:
        """Page metadata for a log line: ``" probe[...]"``, cached per condition.

        ``log_step_error`` suppresses an identical line, so the probe must be read
        once per *new* condition (never per poll) and reused while that condition
        lasts: the log line stays stable, and a stuck runner is not made slower by
        an extra page read every 350 ms.
        """
        if key == self.probe_key:
            return self.probe_text
        self.probe_key = key
        try:
            probe = str(self.dom.dom_probe() or "")
        except Exception:  # noqa: BLE001 - diagnostics only, never fatal
            probe = ""
        self.probe_text = " probe[" + probe + "]" if probe else ""
        return self.probe_text

    def start_click_wait(self) -> float:
        """How long to wait before the next START click (grows on failure)."""
        if self.start_attempts <= 1:
            return START_CLICK_THROTTLE_SECONDS
        return min(START_CLICK_MAX_SECONDS, START_CLICK_THROTTLE_SECONDS * (2 ** (self.start_attempts - 1)))

    def reset(self, controls: DOMControls) -> None:
        self.conversation = controls.conversation
        self.controls = controls
        self.responder = None
        # One decision per conversation: a saved Settings change applies from the
        # next chat on.
        self.bot_first = self.bot_first_for_new_conversation()
        self.answer_first_burst = self.answer_all_for_new_conversation()
        self.first_sms_answered = False
        self.seen_ids = set()
        self.queued_ids = set()
        self.queue = []
        self.paused = False
        self.rotating = False
        self.probe_logged = False
        self.last_step_error = ""
        self.last_rotate_at = 0.0
        # last_start_at is deliberately NOT cleared: the START throttle is
        # run-wide so a flipping page cannot turn it into a click loop.
        # The clock restarts when the next chat really starts.
        self.idle_since = None

    # -- main step ---------------------------------------------------------

    def step(self) -> bool:
        try:
            snapshot = self.dom.read_dom_snapshot()
        except PageGoneError:
            return True
        except Exception as error:  # noqa: BLE001 - logged, never fatal here
            self.log_step_error("DOM READ FAILED | " + str(error))
            return False

        controls = snapshot.controls
        if not controls.conversation:
            self.log_step_error("DOM NOT READY | missing conversation identity")
            return False
        if controls.conversation != self.conversation:
            self.reset(controls)
        self.controls = controls

        if controls.overflow:
            self.paused = True
            self.publish("paused", "QUEUE FULL | paused safely; start a new chat")
            self.log_step_error("QUEUE FULL | paused safely; start a new chat")
            return False

        if controls.phase != "chatting":
            self.responder = None
            self.queue = []
            if self.rotating:
                self.publish("skipping", "leaving the old conversation")
            else:
                self.publish(
                    "matching" if controls.phase == "matching" else "lobby",
                    "waiting for a conversation",
                )
            wait = self.start_click_wait()
            if (
                has_dom_button(controls.buttons, "START")
                and (self.clock() - self.last_start_at) > wait
            ):
                self.start_attempts += 1
                self.last_start_at = self.clock()
                try:
                    clicked = self.dom.click_dom_button(["START"])
                except PageGoneError:
                    return True
                except Exception as error:  # noqa: BLE001
                    self.log_step_error("START FAILED | " + str(error))
                    clicked = ""
                if clicked:
                    self.logf(
                        "activity",
                        self.prefix + " START clicked | waiting for a new conversation",
                    )
                    # Backing off is a real event: a START that never lands a chat
                    # is why a run can sit in the lobby for minutes.
                    if self.start_attempts >= 3:
                        self.logf(
                            "activity",
                            "{} START WAIT | {} click(s) and still no conversation; "
                            "next click in {}s{}".format(
                                self.prefix,
                                self.start_attempts,
                                seconds_label(self.start_click_wait()),
                                self.probe_detail("start-wait-{}".format(self.start_attempts)),
                            ),
                        )
            elif not self.probe_logged:
                self.probe_logged = True
                try:
                    self.logf("activity", self.prefix + " PAGE PROBE | " + self.dom.dom_probe())
                except Exception:  # noqa: BLE001 - probe is diagnostics only
                    pass
            return False

        if self.rotating:
            return self.rotate()
        if self.paused:
            self.publish("paused", self.last_step_error or "paused")
            return False
        if (
            not controls.can_send
            or not controls.partner
            or not controls.own_name
            or controls.partner.casefold() == controls.own_name.casefold()
        ):
            base = self.control_detail(controls, len(snapshot.messages))
            detail = base + self.probe_detail(base)
            self.publish("waiting", detail)
            self.log_step_error(
                "DOM WAIT | composer or own/partner identity not ready; no guessed replies " + detail
            )
            return False

        if self.responder is None:
            # Only the first snapshot is history. In user-writes-first mode the
            # newest partner row stays unread so the queue below answers it; the
            # optional "answer the opening burst" mode keeps the newest few rows
            # unread instead, and nothing else is ever replayed.
            opening_row = ""
            unread: set[str] = set()
            if not self.bot_first:
                opening_row = newest_partner_row_id(snapshot.messages, controls)
                if self.answer_first_burst:
                    unread = unread_partner_row_ids(snapshot.messages, controls, FIRST_BURST_MAX_ROWS)
                elif opening_row:
                    unread = {opening_row}
            for row in snapshot.messages:
                if row.id and row.id not in unread:
                    self.seen_ids.add(row.id)
            try:
                responder, mode = self.new_responder()
            except Exception as error:  # noqa: BLE001 - surfaced to the operator
                self.paused = True
                self.publish("paused", "REPLY ENGINE FAILED | " + str(error))
                self.log_step_error("REPLY ENGINE FAILED | " + str(error))
                return False
            self.responder = responder
            # A real conversation is being served: the START backoff starts over.
            self.start_attempts = 0
            self.publish("chatting", "mode={} first={}".format(mode, self.first_message_label()))
            self.logf(
                "activity",
                "{} CHAT STARTED | mode={} history={} observer={} first={}".format(
                    self.prefix,
                    mode,
                    len(self.seen_ids),
                    _go_bool(controls.observer_ready),
                    self.first_message_label(),
                ),
            )
            # Either side talking again restarts the silence clock.
            self.arm_silence_clock()
            if self.bot_first:
                stop, _ = self.send_lines(responder.start())
                return stop
            if opening_row == "":
                if has_partner_row(snapshot.messages, controls):
                    self.logf(
                        "activity",
                        self.prefix
                        + " WAITING NEXT SMS | user wrote first earlier; only a new partner message is answered",
                    )
                else:
                    self.logf(
                        "activity",
                        self.prefix + " WAITING FIRST SMS | user writes first; no opener sent",
                    )

        for row in snapshot.messages:
            if (
                not row.id
                or row.id in self.seen_ids
                or row.id in self.queued_ids
                or not row.text.strip()
            ):
                continue
            if row.author.casefold() == controls.own_name.casefold():
                self.seen_ids.add(row.id)
                continue
            if row.author.casefold() != controls.partner.casefold():
                self.log_step_error(
                    "AUTHOR UNKNOWN | unread row retained; no text-only echo guessing"
                )
                continue
            self.queue.append(row)
            self.queued_ids.add(row.id)
            self.user_lines += message_line_count(row.text)
            # The partner is talking again: restart the silence clock.
            self.arm_silence_clock()

        if not self.queue:
            # Nothing to answer: only here may a quiet partner be left behind
            # (the dashboard's SKIP IF NO REPLY option).
            if self.partner_stayed_silent():
                self.publish("skipping", "partner silent for {}s".format(seconds_label(self.no_reply_limit)))
                self.logf(
                    "activity",
                    "{} NO REPLY | partner silent for {}s; leaving the conversation and finding a new match".format(
                        self.prefix, seconds_label(self.no_reply_limit)
                    ),
                )
                self.idle_since = None
                self.rotating = True
                return self.rotate()
            self.publish("chatting", self.activity_note)
            return False
        message = self.queue[0]
        # The counters the dashboard shows: SENT counts the lines the bot really
        # sent, and these two count the partner's lines the bot really read - the
        # bubble count alone (Go's numbers) hides a 3-line message.
        self.logf(
            "activity",
            "{} INCOMING | partner row accepted; lines={} total_user_lines={} pending={}".format(
                self.prefix,
                message_line_count(message.text),
                self.user_lines,
                len(self.queue),
            ),
        )
        # The same ACTIVITY line the Go Show Mode runner writes for every
        # accepted partner row, so a Python run and a Go run can be compared
        # (Go: "REPLY DELAY | selected=... setting=...-...").
        reply_delay = self.pick_seconds(self.reply_delay, self.reply_delay_max)
        self.publish(
            "reply",
            "selected={}s setting={}-{}s".format(
                seconds_label(round(reply_delay, 2)),
                seconds_label(self.reply_delay),
                seconds_label(self.reply_delay_max),
            ),
        )
        self.logf(
            "activity",
            "{} REPLY DELAY | selected={}s setting={}-{}s".format(
                self.prefix,
                seconds_label(round(reply_delay, 2)),
                seconds_label(self.reply_delay),
                seconds_label(self.reply_delay_max),
            ),
        )
        if self.sleep_live(reply_delay):
            return True
        # Do not let a slow reply or an old batch leak into the next partner's chat.
        if not self.still_current():
            return self.cancelled()
        generation = self.conversation
        lines, end = self.responder.reply(message.text)
        self.seen_ids.add(message.id)
        self.queued_ids.discard(message.id)
        self.queue.pop(0)
        stop, delivered = self.send_lines(lines)
        if stop:
            return True
        if self.conversation != generation or self.responder is None:
            return False
        if not self.bot_first and not self.first_sms_answered and delivered > 0:
            self.first_sms_answered = True
            self.logf(
                "activity",
                self.prefix + " FIRST SMS ANSWERED | user wrote first; reply confirmed on screen",
            )
        if end and not self.paused:
            self.queue = []
            self.rotating = True
            return self.rotate()
        return False

    # -- guards ------------------------------------------------------------

    def partner_stayed_silent(self) -> bool:
        """True when a served conversation went quiet past ``no_reply_seconds``.

        The clock starts when a chat begins and restarts whenever either side
        writes; a queued message means there is still work, so it never fires.
        """
        if self.no_reply_seconds <= 0 or self.idle_since is None or self.queue:
            return False
        return (self.clock() - self.idle_since) >= self.no_reply_limit

    def still_current(self) -> bool:
        try:
            snapshot = self.dom.read_dom_snapshot()
        except Exception as error:  # noqa: BLE001 - including page loss, like Go
            self.paused = True
            self.publish("paused", "DOM RECHECK FAILED | " + str(error))
            self.log_step_error("DOM RECHECK FAILED | " + str(error))
            return False
        if (
            snapshot.controls.conversation != self.conversation
            or snapshot.controls.phase != "chatting"
        ):
            self.reset(snapshot.controls)
            return False
        if not snapshot.controls.can_send or snapshot.controls.overflow:
            self.paused = True
            base = self.control_detail(snapshot.controls, len(snapshot.messages))
            detail = base + self.probe_detail(base)
            self.publish("paused", "SEND PAUSED " + detail)
            self.log_step_error("SEND PAUSED | composer is not ready; inspect the page " + detail)
            return False
        return True

    def send_lines(self, lines: list[str] | None) -> tuple[bool, int]:
        delivered = 0
        for index, line in enumerate(lines or []):
            text = line.strip()
            if not text:
                continue
            if index > 0 and self.sleep_live(LINE_SPACING_SECONDS):
                return True, delivered
            if not self.still_current():
                return self.cancelled(), delivered
            self.publish("sending", text[:60])
            try:
                sent = self.dom.send_dom_message(text)
            except PageGoneError:
                return True, delivered
            except Exception as error:  # noqa: BLE001 - unconfirmed send, never a resend
                # The reason used to be thrown away; the operator needs to know
                # whether it was a draft, a reload or a real timeout.
                sent = False
                reason = str(error).strip().splitlines()[0][:160] if str(error).strip() else "not confirmed"
            else:
                reason = "no new own row inside the confirmation window"
            if not sent:
                self.paused = True
                self.publish("paused", "SEND UNCONFIRMED | " + reason)
                self.log_step_error(
                    "SEND UNCONFIRMED | {}; automation paused; inspect the page before starting a new chat "
                    "(no automatic resend)".format(reason)
                )
                return False, delivered
            delivered += 1
            self.bot_lines += message_line_count(text)
            self.arm_silence_clock()
            self.logf(
                "activity",
                "{} SEND CONFIRMED | new own message row observed; lines={} total_bot_lines={}".format(
                    self.prefix, message_line_count(text), self.bot_lines
                ),
            )
            self.publish("chatting", self.activity_note)
        return False, delivered

    def rotate(self) -> bool:
        if (self.clock() - self.last_rotate_at) < ROTATE_THROTTLE_SECONDS:
            return False
        self.last_rotate_at = self.clock()
        self.publish("skipping", self.activity_note or "leaving the conversation")
        try:
            snapshot = self.dom.read_dom_snapshot()
        except PageGoneError:
            return True
        except Exception as error:  # noqa: BLE001
            self.log_step_error("SKIP CHECK FAILED | " + str(error))
            return False
        if snapshot.controls.conversation != self.conversation:
            self.reset(snapshot.controls)
            return False
        # Exact active aria-label only; hidden START/STOP labels never participate.
        try:
            clicked = self.dom.click_dom_button(["SKIP", "CONFIRM?", "START"])
        except PageGoneError:
            return True
        except Exception as error:  # noqa: BLE001
            self.log_step_error("SKIP FAILED | " + str(error))
        else:
            if clicked == "":
                self.log_step_error(
                    "SKIP WAIT | active cycle button unavailable; use the page manually"
                )
            else:
                self.logf(
                    "activity",
                    self.prefix + " SKIP | clicked active " + clicked + "; awaiting conversation change",
                )
                self.last_start_at = 0.0
        # Do not greet the old page again just because click() returned clickable.
        return False

    def log_step_error(self, message: str) -> None:
        if message == self.last_step_error:
            return
        self.last_step_error = message
        self.logf("activity", self.prefix + " " + message)
