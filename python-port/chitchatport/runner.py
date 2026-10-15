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

from .domchat import DOMControls, DOMMessage, DOMSnapshot, PageGoneError
from .settings import FIRST_MESSAGE_RANDOM, FIRST_MESSAGE_USER

REPLY_DELAY_DEFAULT = 0.9  # replyDelay in runLiveChat
POLL_INTERVAL_DEFAULT = 0.35  # pollInterval in runLiveChat
START_CLICK_THROTTLE_SECONDS = 6.0
ROTATE_THROTTLE_SECONDS = 2.0
LINE_SPACING_SECONDS = 0.4


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
    ):
        self.dom = dom
        self.prefix = prefix
        self.logf = logf
        self.new_responder = new_responder
        self.first_message_mode = first_message_mode
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
        self.first_sms_answered = False
        self.last_start_at = 0.0
        self.last_rotate_at = 0.0
        self.last_step_error = ""
        self.probe_logged = False
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

    def reset(self, controls: DOMControls) -> None:
        self.conversation = controls.conversation
        self.controls = controls
        self.responder = None
        # One decision per conversation: a saved Settings change applies from the
        # next chat on.
        self.bot_first = self.bot_first_for_new_conversation()
        self.first_sms_answered = False
        self.seen_ids = set()
        self.queued_ids = set()
        self.queue = []
        self.paused = False
        self.rotating = False
        self.probe_logged = False
        self.last_step_error = ""
        self.last_rotate_at = 0.0
        self.last_start_at = 0.0
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
            self.log_step_error("QUEUE FULL | paused safely; start a new chat")
            return False

        if controls.phase != "chatting":
            self.responder = None
            self.queue = []
            if (
                has_dom_button(controls.buttons, "START")
                and (self.clock() - self.last_start_at) > START_CLICK_THROTTLE_SECONDS
            ):
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
            return False
        if (
            not controls.can_send
            or not controls.partner
            or not controls.own_name
            or controls.partner.casefold() == controls.own_name.casefold()
        ):
            self.log_step_error(
                "DOM WAIT | composer or own/partner identity not ready; no guessed replies"
            )
            return False

        if self.responder is None:
            # Only the first snapshot is history. In user-writes-first mode the
            # newest partner row stays unread so the queue below answers it.
            opening_row = ""
            if not self.bot_first:
                opening_row = newest_partner_row_id(snapshot.messages, controls)
            for row in snapshot.messages:
                if row.id and row.id != opening_row:
                    self.seen_ids.add(row.id)
            try:
                responder, mode = self.new_responder()
            except Exception as error:  # noqa: BLE001 - surfaced to the operator
                self.paused = True
                self.log_step_error("REPLY ENGINE FAILED | " + str(error))
                return False
            self.responder = responder
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
            # The partner is talking again: restart the silence clock.
            self.arm_silence_clock()

        if not self.queue:
            # Nothing to answer: only here may a quiet partner be left behind
            # (the dashboard's SKIP IF NO REPLY option).
            if self.partner_stayed_silent():
                self.logf(
                    "activity",
                    "{} NO REPLY | partner silent for {}s; leaving the conversation and finding a new match".format(
                        self.prefix, seconds_label(self.no_reply_limit)
                    ),
                )
                self.idle_since = None
                self.rotating = True
                return self.rotate()
            return False
        message = self.queue[0]
        self.logf(
            "activity",
            "{} INCOMING | partner row accepted; pending={}".format(self.prefix, len(self.queue)),
        )
        # The same ACTIVITY line the Go Show Mode runner writes for every
        # accepted partner row, so a Python run and a Go run can be compared
        # (Go: "REPLY DELAY | selected=... setting=...-...").
        reply_delay = self.pick_seconds(self.reply_delay, self.reply_delay_max)
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
            self.log_step_error("SEND PAUSED | composer is not ready; inspect the page")
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
            try:
                sent = self.dom.send_dom_message(text)
            except PageGoneError:
                return True, delivered
            except Exception:  # noqa: BLE001 - unconfirmed send, never a resend
                sent = False
            if not sent:
                self.paused = True
                self.log_step_error(
                    "SEND UNCONFIRMED | automation paused; inspect the page before starting a new chat (no automatic resend)"
                )
                return False, delivered
            delivered += 1
            self.arm_silence_clock()
            self.logf("activity", self.prefix + " SEND CONFIRMED | new own message row observed")
        return False, delivered

    def rotate(self) -> bool:
        if (self.clock() - self.last_rotate_at) < ROTATE_THROTTLE_SECONDS:
            return False
        self.last_rotate_at = self.clock()
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
