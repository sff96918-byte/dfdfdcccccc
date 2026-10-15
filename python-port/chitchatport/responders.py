"""Reply responders, ported from the responder section of ``live_chat.go``.

The ``begin()`` seam is preserved: ``start()`` (bot writes first) and the first
``reply()`` (user writes first) both pick the sequence, so a conversation never
skips its first line and never changes sequence halfway.
"""

from __future__ import annotations

import random
from typing import Protocol, Sequence

from .settings import (
    CHAT_MODE_EVA,
    CHAT_MODE_FOLDER,
    MessageSettings,
    message_sequence_lines,
)


class UnsupportedModeError(RuntimeError):
    """Raised for modes that are not ported yet (EVAChat Flow lands in Phase 2)."""


class LiveResponder(Protocol):
    def start(self) -> list[str]: ...

    def reply(self, incoming: str) -> tuple[list[str], bool]: ...


def render_username(line: str, username: str) -> str:
    return line.replace("%username%", username).replace("{snap}", username)


class SequenceLiveResponder:
    """Single/Folder sequences, 1:1 with ``sequenceLiveResponder``."""

    def __init__(self, sequences: Sequence[Sequence[str]], rng: random.Random | None = None):
        self.sequences = [list(seq) for seq in sequences]
        self.lines: list[str] = []
        self.next = 0
        self.picked = False
        self.rng = rng

    def begin(self) -> None:
        if self.picked:
            return
        self.picked = True
        usable = [seq for seq in self.sequences if seq]
        if not usable:
            return
        pick = 0
        if len(usable) > 1 and self.rng is not None:
            pick = self.rng.randrange(len(usable))
        self.lines = usable[pick]
        self.next = 0

    def start(self) -> list[str]:
        self.begin()
        if not self.lines:
            return []
        self.next = 1
        return [self.lines[0]]

    def reply(self, incoming: str) -> tuple[list[str], bool]:
        self.begin()
        if self.next < len(self.lines):
            line = self.lines[self.next]
            self.next += 1
            return [line], False
        return [], True


def live_sequences(settings: MessageSettings) -> list[list[str]]:
    """Ported from ``liveSequences``: folder picks from loaded files, else single."""
    if settings.effective_chat_mode() == CHAT_MODE_FOLDER and settings.loaded_sequences:
        return [message_sequence_lines(seq) for seq in settings.loaded_sequences]
    return [settings.sequence_lines()]


def make_responder(
    settings: MessageSettings, rng: random.Random | None = None
) -> tuple[LiveResponder, str]:
    """Ported from ``newLiveResponder`` (EVA Flow intentionally not ported yet)."""
    mode = settings.effective_chat_mode()
    handles = settings.username_list()
    username = ""
    if handles:
        username = handles[rng.randrange(len(handles))] if rng is not None else handles[0]

    if mode == CHAT_MODE_EVA:
        raise UnsupportedModeError(
            "EVAChat Flow is not ported yet in Phase 1; choose the Single or Folder mode"
        )

    sequences = live_sequences(settings)
    for seq in sequences:
        for line in seq:
            if username == "" and ("%username%" in line or "{snap}" in line):
                raise ValueError("Show Mode sequence needs a Message -> Usernames entry")
    sequences = [[render_username(line, username) for line in seq] for seq in sequences]
    return SequenceLiveResponder(sequences, rng), mode
