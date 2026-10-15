"""Settings store, ported from ``internal/model/ui.go`` + ``internal/uistore``.

Only the fields Show Mode needs are kept, with the Go defaults and the same
normalization rules (unknown first-message mode and unknown chat mode fall back
to the original behaviour). Files are written atomically, exactly like the Go
``store.SaveJSON`` fix.
"""

from __future__ import annotations

import json
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


@dataclass
class GeneralSettings:
    first_message_mode: str = FIRST_MESSAGE_BOT
    poll_min_seconds: int = 20
    poll_max_seconds: int = 40
    action_min_seconds: int = 5
    action_max_seconds: int = 10
    match_reply_timeout_min_seconds: int = 30
    match_reply_timeout_max_seconds: int = 60
    match_close_wait_min_seconds: int = 0
    match_close_wait_max_seconds: int = 0
    reply_delay_min_seconds: int = 0
    reply_delay_max_seconds: int = 0
    # Debug knobs for the DOM runner; the defaults match the Go constants
    # (replyDelay 900ms, pollInterval 350ms in runLiveChat).
    reply_delay_ms: int = 900
    poll_interval_ms: int = 350

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
        settings = coerce_settings(GeneralSettings, load_json(self._path("general")) or {})
        # Normalize on load and on save, so a hand-edited file cannot surprise us.
        settings.first_message_mode = settings.effective_first_message_mode()
        return settings

    def load_message_settings(self) -> MessageSettings:
        return coerce_settings(MessageSettings, load_json(self._path("message")) or {})

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
        settings.first_message_mode = settings.effective_first_message_mode()
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
        settings.sequence = normalize_line_list(settings.sequence)
        settings.usernames = normalize_line_list(settings.usernames)
        save_json(self._path("message"), asdict(settings))
        return settings
