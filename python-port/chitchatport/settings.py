"""Settings store, ported from ``internal/model/ui.go`` + ``internal/uistore``.

Only the fields Show Mode needs are kept, with the Go defaults and the same
normalization rules (unknown first-message mode and unknown chat mode fall back
to the original behaviour). Files are written atomically, exactly like the Go
``store.SaveJSON`` fix.
"""

from __future__ import annotations

import json
import math
import os
import tempfile
from dataclasses import asdict, dataclass, field, fields
from pathlib import Path

FIRST_MESSAGE_BOT = "bot"
FIRST_MESSAGE_USER = "user"
FIRST_MESSAGE_RANDOM = "random"

CHAT_MODE_SINGLE = "single"
CHAT_MODE_FOLDER = "folder"
CHAT_MODE_EVA = "eva_chat_flow"
CHAT_MODE_KEYWORD = "keyword"  # legacy v1.0 name, migrated to EVA at read time

CHANNEL_AUTO = "auto"


def normalize_line_list(value: str) -> str:
    """Ported from ``normalizeLineList``: CRLF-safe, trimmed, empty lines dropped."""
    lines = str(value or "").replace("\r", "").split("\n")
    return "\n".join(line.strip() for line in lines if line.strip())


def message_sequence_lines(sequence: str) -> list[str]:
    normalized = normalize_line_list(sequence)
    return normalized.split("\n") if normalized else []


def username_lines(usernames: str) -> list[str]:
    normalized = normalize_line_list(usernames)
    return normalized.split("\n") if normalized else []


def legacy_message_sequence(first_message: str, reply_message: str) -> str:
    """Ported from ``model.LegacyMessageSequence``: the v1 two-field sequence."""
    lines = []
    first = str(first_message or "").strip()
    if first:
        lines.append(first)
    reply = str(reply_message or "").strip()
    if reply:
        lines.append(reply)
    return "\n".join(lines)


# The Go app's model.DefaultMessageSettings() sequence. It is documented here
# (and asserted by the tests) but deliberately NOT injected into a fresh data
# folder: the guided flow finishes its MESSAGE step only when the operator
# loaded a patterns folder or a real sequence is saved, which a silently
# pre-filled default would fake. A v1 file that stored first_message /
# reply_message is still migrated, because that is real operator content.
GO_DEFAULT_MESSAGE_SEQUENCE = "hello how are you\nok i am checking. wait a bit."


@dataclass
class GeneralSettings:
    first_message_mode: str = FIRST_MESSAGE_BOT
    # Dashboard extra (user-writes-first only): a fresh chat answers the newest
    # few unread partner rows instead of ignoring everything but the last one.
    # Off by default, so the Go rule (only the newest row is answered) stays the
    # behaviour of a data folder that never touched this switch.
    first_message_answer_all: bool = False
    # The Go field names and units (seconds), with the Go defaults.
    poll_min_seconds: int = 20
    poll_max_seconds: int = 45
    action_min_seconds: int = 20
    action_max_seconds: int = 45
    match_reply_timeout_min_seconds: int = 60
    match_reply_timeout_max_seconds: int = 60
    match_close_wait_min_seconds: int = 0
    match_close_wait_max_seconds: int = 0
    # Show Mode reply delay: the SAME field names and unit (seconds) the Go app
    # saves and the Go Show Mode runner reads. ``0``-``0`` means no intentional
    # wait, equal non-zero endpoints mean that fixed delay (see docs/SHOW_MODE.md).
    # This port keeps a safer 5-10 s default because a fresh install should not
    # answer instantly; it is a normal Setting and can be zeroed on the dashboard.
    reply_delay_min_seconds: int = 5
    reply_delay_max_seconds: int = 10
    # Backend-only (not on the dashboard): page scan cadence, the Go Show Mode
    # constant ``pollInterval 350ms`` in runLiveChat.
    poll_interval_ms: int = 350
    # Show Mode extra: how long a silent partner is tolerated before the runner
    # leaves the conversation and looks for the next one. 0 disables the check.
    # The default matches the Go auto engine's MatchReplyTimeout (60s); Show
    # Mode itself never gave up on a quiet partner, so the dashboard exposes it.
    skip_if_no_reply_ms: int = 60000
    skip_if_no_reply_max_ms: int = 90000

    def normalize_ranges(self) -> "GeneralSettings":
        """Ported from ``uistore.LoadGeneralSettings``/``SaveGeneralSettings``.

        A non-positive poll/action/skip-timeout minimum falls back to the Go
        default, every maximum is lifted to its minimum, negatives never reach
        the runner, and the first-message mode is normalized. Applied on load
        AND on save, so a hand-edited file cannot surprise the runner.
        """
        defaults = GeneralSettings()
        if self.poll_min_seconds <= 0:
            self.poll_min_seconds = defaults.poll_min_seconds
        if self.poll_max_seconds < self.poll_min_seconds:
            self.poll_max_seconds = self.poll_min_seconds
        if self.action_min_seconds <= 0:
            self.action_min_seconds = defaults.action_min_seconds
        if self.action_max_seconds < self.action_min_seconds:
            self.action_max_seconds = self.action_min_seconds
        if self.match_reply_timeout_min_seconds <= 0:
            self.match_reply_timeout_min_seconds = defaults.match_reply_timeout_min_seconds
        if self.match_reply_timeout_max_seconds < self.match_reply_timeout_min_seconds:
            self.match_reply_timeout_max_seconds = self.match_reply_timeout_min_seconds
        self.match_close_wait_min_seconds = max(0, int(self.match_close_wait_min_seconds or 0))
        if self.match_close_wait_max_seconds < self.match_close_wait_min_seconds:
            self.match_close_wait_max_seconds = self.match_close_wait_min_seconds
        # Go's showModeReplyDelayRange clamps a negative minimum to 0 and lifts
        # the maximum to the minimum before the range is used.
        self.reply_delay_min_seconds = max(0, int(self.reply_delay_min_seconds or 0))
        if self.reply_delay_max_seconds < self.reply_delay_min_seconds:
            self.reply_delay_max_seconds = self.reply_delay_min_seconds
        self.skip_if_no_reply_ms = max(0, int(self.skip_if_no_reply_ms or 0))
        self.skip_if_no_reply_max_ms = max(self.skip_if_no_reply_ms, int(self.skip_if_no_reply_max_ms or 0))
        self.poll_interval_ms = max(50, int(self.poll_interval_ms or 0))
        self.first_message_mode = self.effective_first_message_mode()
        return self

    def effective_first_message_mode(self) -> str:
        """Empty/unknown values keep the original bot-writes-first behaviour."""
        if self.first_message_mode in (FIRST_MESSAGE_USER, FIRST_MESSAGE_RANDOM):
            return self.first_message_mode
        return FIRST_MESSAGE_BOT


@dataclass
class BrowserSettings:
    profile: str = "default"
    # auto -> Chrome, then Edge; or pin "chrome"/"msedge" explicitly.
    channel: str = CHANNEL_AUTO
    headless: bool = False


@dataclass
class ProxySettings:
    """Ported from ``model.ProxySettings``.

    ``enabled`` + ``entries`` are the real fields; ``address`` / ``username`` /
    ``password`` are the v1 single-proxy fields kept only so an upgraded data
    folder still works (see :meth:`SettingsStore.load_proxy_settings`).
    """

    enabled: bool = False
    entries: str = ""
    address: str = ""
    username: str = ""
    password: str = ""

    def payload(self) -> dict:
        """Same JSON shape as Go: legacy fields are omitted when empty."""
        data = {"enabled": bool(self.enabled), "entries": str(self.entries or "")}
        for key in ("address", "username", "password"):
            value = str(getattr(self, key) or "")
            if value:
                data[key] = value
        return data


@dataclass
class MessageSettings:
    chat_mode: str = CHAT_MODE_SINGLE
    sequence: str = ""
    usernames: str = ""
    loaded_sequences: list[str] = field(default_factory=list)
    use_loaded_sequences: bool = False

    def effective_chat_mode(self) -> str:
        if self.chat_mode == CHAT_MODE_EVA:
            return CHAT_MODE_EVA
        if self.chat_mode == CHAT_MODE_KEYWORD:
            return CHAT_MODE_EVA  # v1.0 stored the third mode as "keyword"
        if self.chat_mode == CHAT_MODE_FOLDER:
            return CHAT_MODE_FOLDER
        if self.chat_mode == CHAT_MODE_SINGLE:
            return CHAT_MODE_SINGLE
        if self.use_loaded_sequences and self.loaded_sequences:
            return CHAT_MODE_FOLDER
        return CHAT_MODE_SINGLE

    def sequence_lines(self) -> list[str]:
        return message_sequence_lines(self.sequence)

    def username_list(self) -> list[str]:
        return username_lines(self.usernames)


def coerce_settings(cls, data: dict):
    """Build a settings dataclass from untrusted JSON: wrong types are skipped."""
    known = {f.name: f for f in fields(cls)}
    clean = {}
    for key, value in (data or {}).items():
        if key not in known or value is None:
            continue
        target = known[key].type
        if isinstance(value, bool):
            clean[key] = bool(value)
        elif target in ("int", int) or key.endswith(("_seconds", "_ms")):
            try:
                clean[key] = int(value)
            except (TypeError, ValueError):
                continue
        elif key == "loaded_sequences":
            clean[key] = [str(item) for item in (value or [])]
        else:
            clean[key] = str(value)
    return cls(**clean)


# Backwards-compatible alias (older scripts imported the private name).
_from_dict = coerce_settings


def save_json(path: Path, payload: object) -> None:
    """Atomic write (temp + fsync + replace), mirroring the Go SaveJSON fix."""
    path.parent.mkdir(parents=True, exist_ok=True)
    data = json.dumps(payload, indent=2, ensure_ascii=False).encode("utf-8")
    handle = tempfile.NamedTemporaryFile(
        mode="wb", dir=str(path.parent), prefix=path.name + ".tmp-", delete=False
    )
    temp_name = handle.name
    try:
        handle.write(data)
        handle.flush()
        os.fsync(handle.fileno())
        handle.close()
        os.replace(temp_name, path)
    except BaseException:
        handle.close()
        try:
            os.remove(temp_name)
        except OSError:
            pass
        raise


def load_json(path: Path) -> dict | None:
    try:
        with open(path, "rb") as handle:
            return json.loads(handle.read().decode("utf-8"))
    except FileNotFoundError:
        return None


# The reply-delay defaults the earlier builds of this port shipped (milliseconds).
LEGACY_REPLY_DELAY_MIN_MS = 1000
LEGACY_REPLY_DELAY_MAX_MS = 3000


def migrate_legacy_reply_delay(raw: dict) -> dict:
    """One-time move from this port's old ``reply_delay_ms`` keys to the Go fields.

    Earlier builds stored the Show Mode reply delay in milliseconds under their
    own keys. The Go field names and the seconds unit now win, so a data folder
    written by an older build is converted once (rounded UP, never faster than
    the value the operator had configured) and the old keys are dropped.
    """
    raw = dict(raw or {})
    legacy_min = raw.pop("reply_delay_ms", None)
    legacy_max = raw.pop("reply_delay_max_ms", None)
    if legacy_min is None and legacy_max is None:
        return raw  # nothing to migrate: a fresh/Go-shaped file keeps its own values
    if "reply_delay_min_seconds" in raw or "reply_delay_max_seconds" in raw:
        return raw  # already saved with the Go names; they are the truth

    def to_seconds(value) -> int | None:
        try:
            milliseconds = int(value)
        except (TypeError, ValueError):
            return None
        return int(math.ceil(milliseconds / 1000.0)) if milliseconds > 0 else 0

    # The old build's own defaults, so a file that set only one end of the
    # range keeps the range it effectively had.
    minimum = to_seconds(legacy_min) if legacy_min is not None else to_seconds(LEGACY_REPLY_DELAY_MIN_MS)
    maximum = to_seconds(legacy_max) if legacy_max is not None else to_seconds(LEGACY_REPLY_DELAY_MAX_MS)
    if minimum is None and maximum is None:
        return raw
    if minimum is None:
        minimum = to_seconds(LEGACY_REPLY_DELAY_MIN_MS)
    if maximum is None:
        maximum = to_seconds(LEGACY_REPLY_DELAY_MAX_MS)
    raw["reply_delay_min_seconds"] = minimum
    raw["reply_delay_max_seconds"] = max(minimum, maximum)
    return raw


DEFAULT_FLOW_STATE = {
    "proxy_decided": False,
    "setting_saved": False,
    "console_unlocked": False,
    "hold": False,
    "rail_side": "right",
    # START pressed: only then does the Browser step (RUN) exist. STOP clears it.
    "started": False,
}


class SettingsStore:
    """JSON-backed settings under ``<data_dir>/state`` (Badger's replacement)."""

    def __init__(self, data_dir: str | os.PathLike[str]):
        self.data_dir = Path(data_dir)
        self.state_dir = self.data_dir / "state"

    def _path(self, name: str) -> Path:
        return self.state_dir / f"{name}.json"

    def load_browser_settings(self) -> BrowserSettings:
        settings = coerce_settings(BrowserSettings, load_json(self._path("browser")) or {})
        if not settings.profile.strip():
            settings.profile = "default"
        return settings

    def load_general_settings(self) -> GeneralSettings:
        raw = migrate_legacy_reply_delay(load_json(self._path("general")) or {})
        settings = coerce_settings(GeneralSettings, raw)
        # Normalize on load and on save, so a hand-edited file cannot surprise us.
        settings.normalize_ranges()
        return settings

    def load_message_settings(self) -> MessageSettings:
        raw = load_json(self._path("message")) or {}
        settings = coerce_settings(MessageSettings, raw)
        # Ported from uistore.LoadMessageSettings: a v1 file stored the first
        # and reply line separately, so those two fields still restore the
        # sequence they describe.
        if not message_sequence_lines(settings.sequence):
            settings.sequence = legacy_message_sequence(
                raw.get("first_message", ""), raw.get("reply_message", "")
            )
        settings.usernames = normalize_line_list(settings.usernames)
        return settings

    def load_proxy_settings(self) -> ProxySettings:
        settings = coerce_settings(ProxySettings, load_json(self._path("proxy")) or {})
        # Ported migration: a v1 single proxy becomes the first list line.
        if not settings.entries.strip() and settings.address.strip():
            address = settings.address.strip()
            for prefix in ("http://", "https://"):
                if address.startswith(prefix):
                    address = address[len(prefix) :]
            if settings.username.strip() or settings.password.strip():
                address = "{}:{}:{}".format(address, settings.username.strip(), settings.password.strip())
            settings.entries = address
        return settings

    def load_flow_state(self) -> dict:
        """Dashboard flow markers: which guided steps the operator finished."""
        stored = load_json(self._path("flow")) or {}
        state = dict(DEFAULT_FLOW_STATE)
        for key, default in DEFAULT_FLOW_STATE.items():
            value = stored.get(key, default)
            if isinstance(default, bool):
                state[key] = bool(value)
            else:
                state[key] = str(value)
        if state["rail_side"] not in ("left", "right"):
            state["rail_side"] = "right"
        return state

    def save_flow_state(self, state: dict) -> dict:
        merged = self.load_flow_state()
        merged.update(
            {key: value for key, value in (state or {}).items() if key in DEFAULT_FLOW_STATE}
        )
        # Normalize on save as well as on load: a hand-edited value cannot stick.
        if merged["rail_side"] not in ("left", "right"):
            merged["rail_side"] = "right"
        save_json(self._path("flow"), merged)
        return merged

    def save_proxy_settings(self, settings: ProxySettings) -> ProxySettings:
        # The list is the single source of truth from now on.
        settings.address = ""
        settings.username = ""
        settings.password = ""
        save_json(self._path("proxy"), settings.payload())
        return settings

    def save_browser_settings(self, settings: BrowserSettings) -> BrowserSettings:
        if not settings.profile.strip():
            settings.profile = "default"
        save_json(self._path("browser"), asdict(settings))
        return settings

    def save_general_settings(self, settings: GeneralSettings) -> GeneralSettings:
        settings.normalize_ranges()  # includes the first-message normalization
        save_json(self._path("general"), asdict(settings))
        return settings

    def save_message_settings(self, settings: MessageSettings) -> MessageSettings:
        if settings.chat_mode == CHAT_MODE_KEYWORD:
            settings.chat_mode = CHAT_MODE_EVA
        if settings.chat_mode == CHAT_MODE_FOLDER:
            settings.use_loaded_sequences = True
        elif settings.chat_mode in (CHAT_MODE_SINGLE, CHAT_MODE_EVA):
            settings.use_loaded_sequences = False
        elif settings.use_loaded_sequences and settings.loaded_sequences:
            settings.chat_mode = CHAT_MODE_FOLDER
        else:
            settings.chat_mode = CHAT_MODE_SINGLE
            settings.use_loaded_sequences = False
        # An empty sequence stays empty on purpose: the guided MESSAGE step must
        # still be able to tell "nothing configured yet" from "a sequence is
        # saved" (the v1 first_message/reply_message fields are read on load).
        settings.sequence = normalize_line_list(settings.sequence)
        settings.usernames = normalize_line_list(settings.usernames)
        save_json(self._path("message"), asdict(settings))
        return settings
