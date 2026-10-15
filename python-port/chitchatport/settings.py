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


# --------------------------------------------------------------------------
# Silent-bot risks: the settings combinations that make a run look broken.
#
# Every rule below is traced to the runner code that acts on it, so the
# dashboard can say WHY a run stays quiet instead of showing an empty log:
#   * an empty effective sequence -> SequenceLiveResponder has no line to send:
#     the bot opens no chat and every partner message ends the reply at once
#     (end=True), so chat after chat is skipped with nothing sent.
#   * EVA / Keyword mode -> make_responder raises UnsupportedModeError in
#     Phase 1, the runner logs "REPLY ENGINE FAILED" and pauses.
#   * %username% / {snap} without a Message -> Usernames entry ->
#     make_responder raises ValueError -> the same pause.
#   * skip-if-no-reply shorter than the reply delay -> the runner leaves a chat
#     before its own reply is due, so partners never see an answer.
#   * a page scan slower than 2 s -> the site changes faster than the bot looks.
#   * skip-if-no-reply 0 -> the runner never leaves a silent partner by itself.
#   * a single line with bot-writes-first -> the opener uses it and the first
#     partner reply ends the chat.
#
# A fix payload is what ShowModeService.apply_settings_fix saves in one click,
# with the same merge semantics as the manual save buttons: a field the fix does
# not mention keeps its stored value.
# --------------------------------------------------------------------------

RISK_BLOCK = "BLOCK"
RISK_WARN = "WARN"
RISK_NOTE = "NOTE"
RISK_ORDER = {RISK_BLOCK: 0, RISK_WARN: 1, RISK_NOTE: 2}

# The Go Show Mode silence limit (auto engine MatchReplyTimeout) and the Go page
# scan constant, used as the repair values.
SAFE_SKIP_IF_NO_REPLY_MS = 60000
SAFE_SKIP_IF_NO_REPLY_MAX_MS = 90000
SAFE_POLL_INTERVAL_MS = 350
SLOW_PAGE_SCAN_MS = 2000

# Every rule the preflight judges, in the order CHECK SYSTEM shows them, with
# the label the operator reads. A rule with no risk is reported as PASS with the
# values it judged - PASS always means "this was really looked at".
SETTINGS_RULES = (
    ("empty-sequence", "Something to send"),
    ("eva-mode", "Chat mode runnable in Phase 1"),
    ("usernames-missing", "Usernames for %username% lines"),
    ("message-mode", "Folder mode has loaded files"),
    ("timings", "Reply delay / skip if no reply"),
    ("page-scan", "Page scan interval"),
    ("skip-off", "Skip if no reply is on"),
    ("one-line-sequence", "More than one line to send"),
)


def risk_seconds(value: float) -> str:
    """350 -> "350", 60 -> "60", 7.5 -> "7.5" (the same idea as seconds_label)."""
    number = float(value)
    return str(int(number)) if number == int(number) else str(round(number, 2))


def risk_ms_range(low: int, high: int) -> str:
    if not low:
        return "off"
    return "{}-{}s".format(risk_seconds(low / 1000.0), risk_seconds(high / 1000.0))


def settings_facts(general: "GeneralSettings", message: "MessageSettings") -> dict:
    """What the runner is really told to do, as plain JSON-safe values.

    One place computes it, so the risk rules, the PASS details of the preflight,
    the debug bundle and the Overview panel can never disagree.
    """
    mode = message.effective_chat_mode()
    folder_files = len([seq for seq in message.loaded_sequences if str(seq).strip()])
    single_lines = message.sequence_lines()
    folder_lines = [
        line for seq in message.loaded_sequences for line in message_sequence_lines(str(seq))
    ]
    uses_folder = mode == CHAT_MODE_FOLDER and folder_files > 0
    wants_username = any(
        ("%username%" in seq) or ("{snap}" in seq)
        for seq in list(message.loaded_sequences) + [message.sequence]
    )
    return {
        "mode": mode,
        "folder_files": folder_files,
        "single_lines": single_lines,
        "folder_lines": folder_lines,
        "effective_lines": folder_lines if uses_folder else single_lines,
        "effective_source": "folder" if uses_folder else "single",
        "usernames": message.username_list(),
        "wants_username": wants_username,
        "reply_delay_seconds": [general.reply_delay_min_seconds, general.reply_delay_max_seconds],
        "skip_if_no_reply_ms": [general.skip_if_no_reply_ms, general.skip_if_no_reply_max_ms],
        "page_scan_ms": general.poll_interval_ms,
        "first_message_mode": general.effective_first_message_mode(),
    }


def rule_pass_detail(code: str, facts: dict) -> str:
    """The measured values behind a PASS: never a bare "ok"."""
    mode = facts["mode"]
    usernames = len(facts["usernames"])
    if code == "empty-sequence":
        return "{} line(s) from the {} sequence".format(
            len(facts["effective_lines"]), facts["effective_source"]
        )
    if code == "eva-mode":
        return "mode={} (Single and Folder are runnable)".format(mode)
    if code == "usernames-missing":
        if not facts["wants_username"]:
            return "no %username% / {snap} line, so nothing is required"
        return "{} username(s) saved for the %username% lines".format(usernames)
    if code == "message-mode":
        if mode != CHAT_MODE_FOLDER:
            return "mode={} needs no folder".format(mode)
        return "mode=folder folder_files={} usernames={}".format(facts["folder_files"], usernames)
    if code == "timings":
        low, high = facts["skip_if_no_reply_ms"]
        return "Reply delay={}-{}s Skip if no reply={}".format(
            risk_seconds(facts["reply_delay_seconds"][0]),
            risk_seconds(facts["reply_delay_seconds"][1]),
            risk_ms_range(low, high),
        )
    if code == "page-scan":
        return "{} ms".format(facts["page_scan_ms"])
    if code == "skip-off":
        low, high = facts["skip_if_no_reply_ms"]
        return "Skip if no reply={}".format(risk_ms_range(low, high))
    if code == "one-line-sequence":
        return "{} line(s) to send, first message={}".format(
            len(facts["effective_lines"]), facts["first_message_mode"]
        )
    return "checked"


@dataclass
class SettingsFix:
    """The one-click repair for a risk: the payloads the save buttons receive."""

    summary: str = ""
    general: dict = field(default_factory=dict)
    message: dict = field(default_factory=dict)

    def payload(self) -> dict:
        return {
            "summary": self.summary,
            "general": dict(self.general),
            "message": dict(self.message),
        }


@dataclass
class SettingsRisk:
    """One way the saved settings can silence the bot, with the repair."""

    code: str
    level: str
    title: str
    detail: str
    effect: str
    fix_label: str = ""
    fix: SettingsFix | None = None
    goto: str = ""  # the tab to edit when there is no automatic fix

    def payload(self) -> dict:
        return {
            "code": self.code,
            "level": self.level,
            "title": self.title,
            "detail": self.detail,
            "effect": self.effect,
            "fix_label": self.fix_label,
            "fix": self.fix.payload() if self.fix else None,
            "goto": self.goto,
        }


def silent_bot_risks(
    general: "GeneralSettings", message: "MessageSettings"
) -> list[SettingsRisk]:
    """Every saved-settings combination that can make the bot look dead.

    Pure: it only reads the two settings objects and returns the risks sorted by
    severity. The dashboard shows them before RUN, CHECK SYSTEM reports them,
    and each repair carries the exact payload to save.
    """
    facts = settings_facts(general, message)
    mode = facts["mode"]
    folder_files = facts["folder_files"]
    single_lines = facts["single_lines"]
    usernames = facts["usernames"]
    effective_lines = facts["effective_lines"]
    risks: list[SettingsRisk] = []

    # 1. Nothing to send at all: the strongest silence there is.
    if not effective_lines:
        if mode == CHAT_MODE_FOLDER:
            detail = "Folder mode is selected but none of the loaded files has a usable line"
            default_fix = SettingsFix(
                summary="switched to the single sequence with the two Go default lines",
                message={
                    "chat_mode": CHAT_MODE_SINGLE,
                    "sequence": GO_DEFAULT_MESSAGE_SEQUENCE,
                    "use_loaded_sequences": False,
                },
            )
        else:
            detail = "The sequence box is empty"
            default_fix = SettingsFix(
                summary="loaded the two Go default lines as the single sequence",
                message={
                    "chat_mode": CHAT_MODE_SINGLE,
                    "sequence": GO_DEFAULT_MESSAGE_SEQUENCE,
                },
            )
        risks.append(
            SettingsRisk(
                code="empty-sequence",
                level=RISK_BLOCK,
                title="No message to send",
                detail=detail,
                effect=(
                    "the bot opens no chat and answers nothing; every partner row ends the "
                    "reply at once, so the runner keeps skipping chats"
                ),
                fix_label="LOAD THE GO DEFAULT LINES",
                fix=default_fix,
                goto="message",
            )
        )

    # 2. A mode Phase 1 cannot run: the runner pauses instead of talking.
    if mode == CHAT_MODE_EVA:
        message_payload = {"chat_mode": CHAT_MODE_FOLDER if folder_files else CHAT_MODE_SINGLE}
        if folder_files:
            summary = "switched to Folder mode ({} loaded file(s))".format(folder_files)
        elif single_lines:
            summary = "switched to Single mode ({} saved line(s))".format(len(single_lines))
        else:
            summary = "switched to Single mode and loaded the Go default lines"
            message_payload["sequence"] = GO_DEFAULT_MESSAGE_SEQUENCE
        risks.append(
            SettingsRisk(
                code="eva-mode",
                level=RISK_BLOCK,
                title="Chat mode is not runnable in Phase 1",
                detail=(
                    "mode={} - EVAChat Flow is Phase 2; the Single and Folder modes work today"
                ).format(mode),
                effect='the runner logs "REPLY ENGINE FAILED" and pauses the browser',
                fix_label="USE A WORKING MODE",
                fix=SettingsFix(summary=summary, message=message_payload),
                goto="message",
            )
        )

    # 3. %username% / {snap} with an empty list: the runner refuses at reply time.
    if facts["wants_username"] and not usernames:
        risks.append(
            SettingsRisk(
                code="usernames-missing",
                level=RISK_BLOCK,
                title="Sequence needs a username, none saved",
                detail="a sequence line uses %username% / {snap} but Message -> Usernames is empty",
                effect=(
                    'the runner logs "REPLY ENGINE FAILED" (sequence needs a Message -> '
                    "Usernames entry) and pauses"
                ),
                fix_label="",
                fix=None,
                goto="message",
            )
        )

    # 4. Folder mode with nothing loaded: silently falls back to the sequence box.
    if mode == CHAT_MODE_FOLDER and folder_files == 0:
        risks.append(
            SettingsRisk(
                code="message-mode",
                level=RISK_WARN,
                title="Folder mode with no loaded file",
                detail="mode={} folder_files=0 usernames={}".format(mode, len(usernames)),
                effect="the runner falls back to the single sequence, which may be empty (see above)",
                fix_label="USE THE SINGLE SEQUENCE",
                fix=SettingsFix(
                    summary="switched to Single mode while the folder is empty",
                    message={"chat_mode": CHAT_MODE_SINGLE, "use_loaded_sequences": False},
                ),
                goto="message",
            )
        )

    # 5. Silence limit shorter than the reply delay: chats are left mid-thought.
    delay_max = facts["reply_delay_seconds"][1]
    skip_low, skip_high = facts["skip_if_no_reply_ms"]
    if skip_low and skip_high < delay_max * 1000:
        risks.append(
            SettingsRisk(
                code="timings",
                level=RISK_WARN,
                title="Skip if no reply is shorter than the reply delay",
                detail="Reply delay={}-{}s Skip if no reply={}".format(
                    risk_seconds(facts["reply_delay_seconds"][0]),
                    risk_seconds(delay_max),
                    risk_ms_range(skip_low, skip_high),
                ),
                effect=(
                    "the bot leaves chats before its own reply is due, so partners never "
                    "get an answer"
                ),
                fix_label="USE THE GO 60-90s",
                fix=SettingsFix(
                    summary="skip if no reply 60-90s (the Go Show Mode default)",
                    general={
                        "skip_if_no_reply_ms": SAFE_SKIP_IF_NO_REPLY_MS,
                        "skip_if_no_reply_max_ms": SAFE_SKIP_IF_NO_REPLY_MAX_MS,
                    },
                ),
                goto="setting",
            )
        )

    # 6. A slow page scan: the page changes between two looks.
    if facts["page_scan_ms"] > SLOW_PAGE_SCAN_MS:
        risks.append(
            SettingsRisk(
                code="page-scan",
                level=RISK_WARN,
                title="Page scan interval is slow",
                detail="{} ms".format(facts["page_scan_ms"]),
                effect=(
                    "replies will feel slow and short-lived rows can be missed; 350 ms is "
                    "the Go value"
                ),
                fix_label="USE 350 MS",
                fix=SettingsFix(
                    summary="page scan interval 350 ms (the Go value)",
                    general={"poll_interval_ms": SAFE_POLL_INTERVAL_MS},
                ),
                goto="setting",
            )
        )

    # 7. Skip-if-no-reply switched off: a dead chat is never left behind.
    if not skip_low:
        risks.append(
            SettingsRisk(
                code="skip-off",
                level=RISK_NOTE,
                title="Skip if no reply is off",
                detail="Skip if no reply=off",
                effect="the bot waits in a silent chat forever instead of looking for the next one",
                fix_label="USE THE GO 60-90s",
                fix=SettingsFix(
                    summary="skip if no reply 60-90s (the Go Show Mode default)",
                    general={
                        "skip_if_no_reply_ms": SAFE_SKIP_IF_NO_REPLY_MS,
                        "skip_if_no_reply_max_ms": SAFE_SKIP_IF_NO_REPLY_MAX_MS,
                    },
                ),
                goto="setting",
            )
        )

    # 8. A single line with bot-writes-first: the opener uses it and the first
    #    partner reply ends the chat, so one partner never gets a real answer.
    if len(effective_lines) == 1 and facts["first_message_mode"] == FIRST_MESSAGE_BOT:
        risks.append(
            SettingsRisk(
                code="one-line-sequence",
                level=RISK_NOTE,
                title="Only one line to send",
                detail="the effective sequence has 1 line",
                effect=(
                    "the opener uses it and the first partner reply ends the chat (nothing "
                    "left to answer with)"
                ),
                fix_label="",
                fix=None,
                goto="message",
            )
        )

    return sorted(risks, key=lambda risk: (RISK_ORDER.get(risk.level, 9), risk.code))
