"""DOM-only contracts, ported from ``internal/browser/domchat.go``.

The JSON keys are the ones the shared in-page helper emits (camelCase), so the
exact same ``showmode_dom.js`` can feed both the Go and the Python runners.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Protocol


class PageGoneError(RuntimeError):
    """The browser page/window is gone: stop the runner instead of retrying."""


class DOMUnavailableError(RuntimeError):
    """The DOM could not answer (not a fatal page loss); the runner logs it."""


@dataclass(frozen=True)
class DOMMessage:
    id: str = ""
    order: int = 0
    author: str = ""
    text: str = ""

    @classmethod
    def from_json(cls, data: dict) -> "DOMMessage":
        raw_order = data.get("order") or 0
        try:
            order = int(raw_order)
        except (TypeError, ValueError):
            order = 0
        return cls(
            id=str(data.get("id") or ""),
            order=order,
            author=str(data.get("author") or ""),
            text=str(data.get("text") or ""),
        )


@dataclass(frozen=True)
class DOMControls:
    has_input: bool = False
    input_desc: str = ""
    buttons: list[str] = field(default_factory=list)
    # Go's struct zero value is false; the page reports chatEnded=true only when
    # the conversation is over, so the default must stay false here as well.
    chat_ended: bool = False
    partner: str = ""
    own_name: str = ""
    conversation: str = ""
    phase: str = ""
    can_send: bool = False
    observer_ready: bool = False
    overflow: bool = False

    @classmethod
    def from_json(cls, data: dict) -> "DOMControls":
        buttons = data.get("buttons") or []
        return cls(
            has_input=bool(data.get("hasInput")),
            input_desc=str(data.get("inputDesc") or ""),
            buttons=[str(b) for b in buttons],
            chat_ended=bool(data.get("chatEnded")),
            partner=str(data.get("partner") or ""),
            own_name=str(data.get("ownName") or ""),
            conversation=str(data.get("conversation") or ""),
            phase=str(data.get("phase") or ""),
            can_send=bool(data.get("canSend")),
            observer_ready=bool(data.get("observerReady")),
            overflow=bool(data.get("overflow")),
        )


@dataclass(frozen=True)
class DOMSnapshot:
    controls: DOMControls = field(default_factory=DOMControls)
    messages: list[DOMMessage] = field(default_factory=list)
    revision: int = 0

    @classmethod
    def from_json(cls, data: dict | None) -> "DOMSnapshot":
        data = data or {}
        messages = data.get("messages") or []
        return cls(
            controls=DOMControls.from_json(data.get("controls") or {}),
            messages=[DOMMessage.from_json(m or {}) for m in messages],
            revision=int(data.get("revision") or 0),
        )


class DOMChatBackend(Protocol):
    """Minimal surface the runner needs; the Playwright backend implements it."""

    def read_dom_snapshot(self) -> DOMSnapshot: ...

    def send_dom_message(self, text: str) -> bool: ...

    def click_dom_button(self, labels: list[str]) -> str: ...

    def dom_probe(self) -> str: ...

    def close(self) -> None: ...


def message_line_count(text: str) -> int:
    """Non-empty lines one rendered message carries.

    The runner and the Home transcript both count lines, so the rule (CRLF safe,
    blank lines never counted) lives next to the message shape itself.
    """
    lines = str(text or "").replace("\r", "").split("\n")
    return len([line for line in lines if line.strip()])


def same_dom_conversation(before: DOMControls, after: DOMControls) -> bool:
    """Ported 1:1 from ``sameDOMConversation`` in domchat.go."""
    return (
        before.conversation != ""
        and before.conversation == after.conversation
        and after.phase == "chatting"
        and before.partner.casefold() == after.partner.casefold()
    )


def confirmed_dom_send(before: DOMSnapshot, after: DOMSnapshot, text: str) -> bool:
    """Ported 1:1 from ``confirmedDOMSend``: a NEW own row with exactly ``text``."""
    if not same_dom_conversation(before.controls, after.controls) or before.controls.own_name == "":
        return False
    known = {m.id for m in before.messages}
    for message in after.messages:
        if (
            message.id != ""
            and message.id not in known
            and message.text == text
            and message.author.casefold() == before.controls.own_name.casefold()
        ):
            return True
    return False
