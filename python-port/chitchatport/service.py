"""Application service: settings + logs + transcript + the Show Mode run loop.

Mirrors the Go wiring in ``app.go`` / ``live_chat.go``:
fresh temporary browser data, login wait with the same log messages, login JSON
written to ``profiles/<profile>/``, then the DOM runner until the window closes.
"""

from __future__ import annotations

import platform
import random
import re
import shutil
import sys
import tempfile
import threading
import time
import traceback
from dataclasses import asdict
from pathlib import Path
from typing import Callable

from . import __version__
from . import proxy
from .responders import live_sequences
from .browser import (
    PlaywrightDOMBackend,
    current_user_id,
    fresh_login_info_path,
    token_from_login,
)
from .domchat import PageGoneError
from .homefeed import HomeChatFeed
from .logging_util import ActivityLog
from .responders import make_responder
from .runner import LiveRunner, seconds_label
from .settings import (
    BrowserSettings,
    GeneralSettings,
    MessageSettings,
    ProxySettings,
    SettingsStore,
    coerce_settings,
    message_sequence_lines,
    save_json,
)

WINDOWS_RESERVED = {"CON", "PRN", "AUX", "NUL"}

GO_REFERENCE = Path(__file__).resolve().parents[2] / "root-project"
DIAGNOSTIC_CHANNELS = ("chrome", "msedge")


def run_diagnostics(data_dir: str | Path, deep: bool = True) -> dict:
    """Environment preflight, shared by the Overview tab and ``app.py --diag``.

    ``deep`` also starts each system browser headless once (a few seconds) to
    prove Show Mode can really open a window. Every check reports PASS / FAIL /
    WARN / BLOCKED - BLOCKED means "could not check", never PASS.
    """
    data_dir = Path(data_dir)
    checks: list[dict] = []

    def record(name: str, label: str, status: str, detail: str = "") -> None:
        checks.append({"name": name, "label": label, "status": status, "detail": detail})

    version = platform.python_version()
    parts = tuple(int(part) for part in re.findall(r"\d+", version)[:3] or [0])
    record(
        "python",
        "Python runtime",
        "PASS" if parts >= (3, 9) else "FAIL",
        "{} ({})".format(version, sys.executable),
    )

    try:
        import playwright  # noqa: F401

        from playwright.sync_api import sync_playwright  # noqa: F401

        record("playwright", "Playwright package", "PASS", "importable")
        playwright_ready = True
    except Exception as error:  # noqa: BLE001 - reported as a check, not a crash
        record(
            "playwright",
            "Playwright package",
            "FAIL",
            "not installed ({}) - run install.bat / pip install -r requirements.txt".format(error),
        )
        playwright_ready = False

    if not playwright_ready:
        for channel in DIAGNOSTIC_CHANNELS:
            record(channel, "Browser: " + channel, "BLOCKED", "needs the Playwright package")
    elif deep:
        try:
            from playwright.sync_api import sync_playwright

            with sync_playwright() as playwright:
                for channel in DIAGNOSTIC_CHANNELS:
                    try:
                        browser = playwright.chromium.launch(channel=channel, headless=True)
                        browser.close()
                        record(channel, "Browser: " + channel, "PASS", "starts headless")
                    except Exception as error:  # noqa: BLE001
                        record(
                            channel,
                            "Browser: " + channel,
                            "FAIL",
                            str(error).splitlines()[0][:120],
                        )
        except Exception as error:  # noqa: BLE001
            for channel in DIAGNOSTIC_CHANNELS:
                record(channel, "Browser: " + channel, "BLOCKED", str(error)[:120])
    else:
        for channel in DIAGNOSTIC_CHANNELS:
            record(channel, "Browser: " + channel, "BLOCKED", "not checked (run the deep check)")

    try:
        data_dir.mkdir(parents=True, exist_ok=True)
        probe = data_dir / ".write-probe"
        probe.write_text("ok", encoding="utf-8")
        probe.unlink()
        record("data-dir", "Data folder writable", "PASS", str(data_dir))
    except OSError as error:
        record("data-dir", "Data folder writable", "FAIL", "{} ({})".format(data_dir, error))

    log_file = data_dir / "logs" / "port.log"
    try:
        size = log_file.stat().st_size if log_file.exists() else 0
        record("log-file", "Log file", "PASS", "{} ({} bytes)".format(log_file, size))
    except OSError as error:
        record("log-file", "Log file", "WARN", str(error))

    crashes = sorted((data_dir / "logs").glob("crash-*.txt")) if (data_dir / "logs").exists() else []
    record(
        "crash-files",
        "Crash reports",
        "WARN" if crashes else "PASS",
        "{} file(s) in {} - attach the newest to a report".format(len(crashes), data_dir / "logs")
        if crashes
        else "none",
    )

    # The runner's own settings: a wrong one (too short a silence limit, a very
    # slow page scan, Folder mode without files) used to be invisible until a run
    # behaved strangely. The preflight now says it before RUN.
    try:
        store = SettingsStore(data_dir)
        general = store.load_general_settings()
        message = store.load_message_settings()
    except Exception as error:  # noqa: BLE001 - a broken file is a check, not a crash
        record("settings", "Saved settings", "WARN", "could not be read ({})".format(error))
    else:
        timings = "Reply delay={}-{}s Skip if no reply={}".format(
            seconds_label(general.reply_delay_min_seconds),
            seconds_label(general.reply_delay_max_seconds),
            "{}-{}s".format(
                seconds_label(general.skip_if_no_reply_ms / 1000.0),
                seconds_label(general.skip_if_no_reply_max_ms / 1000.0),
            )
            if general.skip_if_no_reply_ms
            else "off",
        )
        short_skip = bool(
            general.skip_if_no_reply_ms
            and general.skip_if_no_reply_max_ms < general.reply_delay_max_seconds * 1000
        )
        record(
            "timings",
            "Reply delay / skip if no reply",
            "WARN" if short_skip else "PASS",
            timings
            + (
                " - the bot leaves chats before its reply is due; raise it (Go default 60-90s) or use 0"
                if short_skip
                else ""
            ),
        )
        slow_scan = general.poll_interval_ms > 2000
        record(
            "page-scan",
            "Page scan interval",
            "WARN" if slow_scan else "PASS",
            "{} ms{}".format(
                general.poll_interval_ms,
                " - replies will feel slow; 350 ms is the Go value" if slow_scan else "",
            ),
        )
        mode = message.effective_chat_mode()
        folder_files = len([seq for seq in message.loaded_sequences if str(seq).strip()])
        needs_usernames = bool(
            any(("%username%" in seq) or ("{snap}" in seq) for seq in message.loaded_sequences + [message.sequence])
        ) and not message.username_list()
        detail = "mode={} folder_files={} usernames={}".format(
            mode, folder_files, len(message.username_list())
        )
        if mode == "folder" and folder_files == 0:
            record(
                "message-mode",
                "Chat mode / sequences",
                "WARN",
                detail + " - Folder mode with no loaded .txt falls back to the single sequence",
            )
        elif needs_usernames:
            record(
                "message-mode",
                "Chat mode / sequences",
                "WARN",
                detail + " - the sequence needs %username% / {snap}; the runner refuses to reply",
            )
        else:
            record("message-mode", "Chat mode / sequences", "PASS", detail)

    go_dom = GO_REFERENCE / "internal" / "browser" / "showmode_dom.js"
    ours = Path(__file__).resolve().parent / "assets" / "showmode_dom.js"
    if go_dom.exists() and ours.exists():
        import hashlib

        same = hashlib.sha256(go_dom.read_bytes()).digest() == hashlib.sha256(ours.read_bytes()).digest()
        record(
            "dom-asset",
            "DOM observer script matches Go",
            "PASS" if same else "FAIL",
            "sha256 {} vs {}".format(
                hashlib.sha256(ours.read_bytes()).hexdigest()[:12],
                hashlib.sha256(go_dom.read_bytes()).hexdigest()[:12],
            ),
        )
    else:
        record(
            "dom-asset",
            "DOM observer script matches Go",
            "BLOCKED",
            "Go source tree not next to this folder",
        )

    counts = {status: sum(1 for check in checks if check["status"] == status) for status in ("PASS", "FAIL", "WARN", "BLOCKED")}
    return {
        "ok": counts["FAIL"] == 0,
        "deep": bool(deep),
        "summary": counts,
        "checks": checks,
        "system": {
            "app": __version__,
            "python": version,
            "platform": platform.platform(),
            "executable": sys.executable,
            "data_dir": str(data_dir),
        },
    }


def safe_profile_name(name: str) -> str:
    """Ported from ``safeProfileName``: Win32-safe folder name."""
    name = str(name or "").strip()
    name = name.replace("..", "_")
    for char in ("\\", "/", ":", "*", "?", '"', "<", ">", "|"):
        name = name.replace(char, "_")
    name = "".join(ch if ord(ch) >= 32 else "_" for ch in name)
    name = name.rstrip(". ")
    if not name:
        return "default"
    stem = name.split(".", 1)[0].upper()
    reserved = stem in WINDOWS_RESERVED
    if len(stem) == 4 and stem[:3] in ("COM", "LPT") and stem[3] in "123456789":
        reserved = True
    return "_" + name if reserved else name


class ObservedDOM:
    """Wraps the backend so the Home transcript sees every real read (like Go)."""

    def __init__(
        self,
        backend: PlaywrightDOMBackend,
        feed: HomeChatFeed,
        run: int,
        on_snapshot: Callable[[object], None] | None = None,
    ):
        self.backend = backend
        self.feed = feed
        self.run = run
        # The worker registry listens here: every real DOM read doubles as a
        # live status update (conversation, partner, phase) for the Overview.
        self.on_snapshot = on_snapshot

    def read_dom_snapshot(self):
        snapshot = self.backend.read_dom_snapshot()
        self.feed.observe(self.run, snapshot, False)
        if self.on_snapshot is not None:
            self.on_snapshot(snapshot)
        return snapshot

    def send_dom_message(self, text: str) -> bool:
        hook = getattr(self.backend, "send_dom_message_observed", None)
        if callable(hook):
            return hook(text, lambda snapshot: self.feed.observe(self.run, snapshot, True))
        return self.backend.send_dom_message(text)

    def click_dom_button(self, labels):
        return self.backend.click_dom_button(labels)

    def dom_probe(self):
        return self.backend.dom_probe()


class ShowModeService:
    def __init__(self, data_dir: str | Path, echo_logs: bool = False):
        self.data_dir = Path(data_dir)
        self.store = SettingsStore(self.data_dir)
        self.logs = ActivityLog(self.data_dir / "logs", echo=echo_logs)
        self.feed = HomeChatFeed()
        self._lock = threading.Lock()
        self._stop: threading.Event | None = None
        self._thread: threading.Thread | None = None
        self._status = "idle"
        self._login_saved = ""
        self.started_at = time.time()
        # Per-browser worker registry: the Overview tab shows one row per
        # browser and its CLOSE button stops exactly that browser.
        self._workers: dict[int, dict] = {}
        self._worker_events: dict[int, threading.Event] = {}

    # -- UI state ----------------------------------------------------------

    def state(self, selected: str = "") -> dict:
        with self._lock:
            running = bool(self._thread and self._thread.is_alive())
            status = self._status
            login_saved = self._login_saved
        return {
            "running": running,
            "status": status,
            "login_saved": login_saved,
            "data_dir": str(self.data_dir),
            "proxy": self.proxy_state(),
            "flow": self.flow_state(),
            "system": self.system_info(),
            "browser": asdict(self.store.load_browser_settings()),
            "general": asdict(self.store.load_general_settings()),
            "message": self.message_state(),
            "logs": self.logs.entries(300),
            "chat": self.feed.snapshot(selected),
            # The Overview tab: one row per browser plus counters and the
            # single next action. Everything here is derived, never guessed.
            "workers": self.workers(),
            "overview": self.overview(),
        }

    # -- worker registry (Overview tab) ------------------------------------

    WORKER_STATES = ("starting", "opening", "waiting-login", "running", "closing", "done", "error")

    def _worker_update(self, index: int, **fields) -> None:
        """Record one live fact about a browser worker (thread-safe)."""
        with self._lock:
            worker = self._workers.get(index)
            if worker is None:
                return
            worker.update(fields)
            worker["updated_at"] = time.time()

    def workers(self) -> list[dict]:
        with self._lock:
            rows = [dict(worker) for worker in self._workers.values()]
        rows.sort(key=lambda worker: int(worker.get("index") or 0))
        now = time.time()
        for worker in rows:
            worker["uptime_seconds"] = int(max(0, now - float(worker.get("started_at") or now)))
            worker["login_saved_name"] = Path(str(worker.get("login_saved") or "")).name
        return rows

    def close_worker(self, index) -> dict:
        """CLOSE on one worker: only that browser stops, the rest keep running."""
        try:
            index = int(index)
        except (TypeError, ValueError):
            return {"ok": False, "error": "worker index is required"}
        with self._lock:
            worker = self._workers.get(index)
            event = self._worker_events.get(index)
        if worker is None or event is None:
            return {"ok": False, "error": "browser {} is not running".format(index)}
        event.set()
        self._worker_update(index, state="closing", note="CLOSE pressed")
        self.logs.append(
            "info",
            "[{} fresh {}/{}] CLOSE pressed: this browser stops after the current step; "
            "the other browsers keep running.".format(
                worker.get("profile") or "default",
                worker.get("index"),
                worker.get("total") or 1,
            ),
        )
        return {"ok": True}

    def overview(self) -> dict:
        """The one screen an operator needs: state, counters, proxy, next action."""
        workers = self.workers()
        flow = self.flow_state()
        message = self.store.load_message_settings()
        general = self.store.load_general_settings()
        proxy_settings = self.store.load_proxy_settings()
        with self._lock:
            running = bool(self._thread and self._thread.is_alive())
            status = self._status
        proxy_lines = 0
        proxy_error = ""
        if proxy_settings.enabled:
            try:
                proxy_lines = len(proxy.parse_list(proxy_settings.entries))
            except ValueError as error:
                proxy_error = str(error)
        waiting_login = sum(1 for w in workers if w.get("state") == "waiting-login")
        active = sum(1 for w in workers if w.get("state") == "running")
        sent = sum(int(w.get("sent") or 0) for w in workers)
        incoming = sum(int(w.get("incoming") or 0) for w in workers)
        chats = sum(int(w.get("chats") or 0) for w in workers)
        user_lines = sum(int(w.get("user_lines") or 0) for w in workers)
        bot_lines = sum(int(w.get("bot_lines") or 0) for w in workers)
        paused = sum(1 for w in workers if w.get("paused"))
        next_action = self._next_action(flow, running, workers, waiting_login)
        return {
            "status": status,
            "running": running,
            "workers_total": len(workers),
            "workers_waiting_login": waiting_login,
            "workers_active": active,
            "workers_paused": paused,
            "messages_sent": sent,
            "messages_incoming": incoming,
            # Lines, not bubbles: SENT counts the lines the bot sent, user_lines
            # the partner lines the runner really read.
            "lines": {"user": user_lines, "bot": bot_lines},
            "chats": chats,
            "rows_observed": self.feed.observed_rows(),
            "proxy": {
                "enabled": bool(proxy_settings.enabled),
                "lines": proxy_lines,
                "error": proxy_error,
                # The rotation each fresh browser will get, in order.
                "preview": self._proxy_preview(proxy_settings, proxy_lines),
            },
            "message": {
                "mode": message.effective_chat_mode(),
                # How many chat patterns this run will really use: the loaded
                # .txt files in Folder mode, otherwise the single sequence.
                "sequences": len([seq for seq in live_sequences(message) if seq]),
                "usernames": len(message.username_list()),
                "needs_usernames": bool(
                    self.sequence_wants_a_username(message) and not message.username_list()
                ),
            },
            "general": {
                "first_message_mode": general.effective_first_message_mode(),
                "first_message_answer_all": bool(general.first_message_answer_all),
                "reply_delay": "{}-{}s".format(
                    seconds_label(general.reply_delay_min_seconds),
                    seconds_label(general.reply_delay_max_seconds),
                ),
                "skip_if_no_reply": (
                    "{}-{}s".format(
                        seconds_label(general.skip_if_no_reply_ms / 1000.0),
                        seconds_label(general.skip_if_no_reply_max_ms / 1000.0),
                    )
                    if general.skip_if_no_reply_ms
                    else "off"
                ),
                "poll_interval_ms": general.poll_interval_ms,
            },
            "flow": flow,
            "next_action": next_action,
        }

    def _proxy_preview(self, settings, lines: int) -> list[dict]:
        """Which proxy each fresh browser gets, in rotation order (max 5 shown)."""
        if not settings.enabled or lines == 0:
            return []
        try:
            entries = proxy.parse_list(settings.entries)
        except ValueError:
            return []
        return [
            {"index": position + 1, "proxy": entry.label()}
            for position, entry in enumerate(entries[:5])
        ]

    @staticmethod
    def _next_action(flow: dict, running: bool, workers: list[dict], waiting_login: int) -> dict:
        """One plain-language next step, derived from the real state."""
        missing = flow.get("missing") or []
        if missing:
            labels = {"message": "MESSAGE", "proxy": "PROXY", "setting": "SETTING"}
            return {
                "step": "finish-setup",
                "text": "Finish " + ", ".join(labels.get(name, name) for name in missing),
                "tab": missing[0],
            }
        if not flow.get("started"):
            return {"step": "press-start", "text": "Press START to open the Browser step", "tab": "settings"}
        if not running:
            return {"step": "press-run", "text": "Press RUN in the Browser tab to open the browser(s)", "tab": "browser"}
        if waiting_login:
            return {
                "step": "log-in",
                "text": "Log in inside the {} browser window(s) - the rest is automatic".format(waiting_login),
                "tab": "browser",
            }
        active = sum(1 for worker in workers if worker.get("state") == "running")
        return {"step": "watching", "text": "Watching {} chat(s) - nothing to do".format(active), "tab": "chat"}

    @staticmethod
    def sequence_wants_a_username(settings) -> bool:
        """True when a sequence line uses %username% / {snap} (Go: same check).

        Without a username line the Go app and this port both refuse at reply
        time with "Show Mode sequence needs a Message → Usernames entry", so the
        dashboard warns as soon as the folder is loaded instead.
        """
        for sequence in live_sequences(settings):
            for line in sequence:
                if "%username%" in line or "{snap}" in line:
                    return True
        return False

    def message_state(self) -> dict:
        """The Message tab: settings plus what the operator still has to fill in."""
        settings = self.store.load_message_settings()
        wants = self.sequence_wants_a_username(settings)
        sequences = live_sequences(settings)
        # One entry per loaded .txt: "lines" is the number of messages the chat
        # will send, counted with the same rule the composer uses (never the
        # character count of the file).
        loaded = []
        for position, sequence in enumerate(settings.loaded_sequences):
            if not str(sequence).strip():
                continue
            messages = message_sequence_lines(str(sequence))
            loaded.append(
                {
                    "index": position + 1,
                    "lines": len(messages),
                    "chars": sum(len(line) for line in messages),
                    "preview": messages[0] if messages else "",
                }
            )
        return {
            **asdict(settings),
            "needs_usernames": bool(wants and not settings.username_list()),
            "sequence_lines": len(settings.sequence_lines()),
            "sequence_preview": (settings.sequence_lines() or [""])[0],
            "sequence_count": len([sequence for sequence in sequences if sequence]),
            "loaded_files": loaded,
            "loaded_count": len(loaded),
        }

    def clear_loaded_sequences(self) -> dict:
        """Empty the folder-loaded sequences so the single sequence is used again."""
        settings = self.store.load_message_settings()
        settings.loaded_sequences = []
        settings.use_loaded_sequences = False
        if settings.chat_mode == "folder":
            settings.chat_mode = "single"
        self.store.save_message_settings(settings)
        self.logs.append("info", "Loaded chat-pattern folder cleared; the single sequence is used again.")
        return {"ok": True, "message": self.message_state()}

    # -- debugging ---------------------------------------------------------

    def report_crash(self, where: str, error: BaseException) -> str:
        """Log an unexpected error AND keep its full traceback on disk.

        The dashboard log only shows one line; the operator cannot debug a
        ``KeyError: 'x'`` from that. Every crash therefore writes
        ``data/logs/crash-<time>.txt`` with the traceback and the log line says
        where it is. Returns the message that was logged.
        """
        stamp = time.strftime("%Y%m%d-%H%M%S", time.gmtime())
        path = self.logs.log_dir / "crash-{}.txt".format(stamp)
        try:
            self.logs.log_dir.mkdir(parents=True, exist_ok=True)
            # a second crash in the same second must not overwrite the first
            counter = 1
            while path.exists():
                counter += 1
                path = self.logs.log_dir / "crash-{}-{}.txt".format(stamp, counter)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write("ChitChat Bot - Python Show Mode - crash report\n")
                handle.write("when   : {}Z\nwhere  : {}\napp    : {}\npython : {}\nos     : {}\n\n".format(
                    time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()),
                    where, __version__, platform.python_version(), platform.platform(),
                ))
                handle.write("".join(traceback.format_exception(type(error), error, error.__traceback__)))
            hint = " (traceback: {})".format(path.name)
        except OSError:
            hint = ""
        message = "{} {}: {}{}".format(where, type(error).__name__, error, hint)
        self.logs.append("error", message)
        return message

    @staticmethod
    def _mask_proxy_lines(entries: str) -> str:
        """The debug bundle is meant to be pasted into a chat: strip passwords."""
        masked = []
        for line in str(entries or "").splitlines():
            line = re.sub(r"://([^:/@\s]+):([^@\s]+)@", r"://\1:***@", line)   # scheme://user:pass@host
            parts = line.split(":")
            if len(parts) == 4 and "://" not in line:                              # host:port:user:pass
                parts[3] = "***"
                line = ":".join(parts)
            masked.append(line)
        return "\n".join(masked)

    def debug_bundle(self) -> dict:
        """Everything needed to diagnose a report, with no secrets.

        Masks proxy passwords, never includes the API token, cookies or message
        bodies. Log lines may name the partner and the page route (that is what
        makes a stuck run diagnosable), which the header below states so nobody
        shares a bundle by accident.
        """
        general_settings = self.store.load_general_settings()
        general = asdict(general_settings)
        browser = asdict(self.store.load_browser_settings())
        message = asdict(self.store.load_message_settings())
        proxy_settings = self.store.load_proxy_settings()
        message["loaded_sequences"] = len(message.get("loaded_sequences") or [])
        message["usernames_count"] = len(self.store.load_message_settings().username_list())
        message.pop("usernames", None)
        crash_files = sorted(p.name for p in self.logs.log_dir.glob("crash-*.txt")) if self.logs.log_dir.exists() else []
        try:
            import playwright  # noqa: F401

            playwright_ok = True
        except Exception:  # noqa: BLE001 - diagnostics only
            playwright_ok = False
        with self._lock:
            running = bool(self._thread and self._thread.is_alive())
            status = self._status
        overview = self.overview()
        chat = self.feed.snapshot("")
        # What the runner was really told to do, in the same units the runner
        # uses - the fastest way to see that a setting and the behaviour differ.
        effective = {
            "chat_mode": message.get("chat_mode") or "",
            "sequences": overview["message"]["sequences"],
            "usernames_saved": overview["message"]["usernames"],
            "usernames_needed": overview["message"]["needs_usernames"],
            "first_message_mode": general_settings.effective_first_message_mode(),
            "first_message_answer_all": bool(general_settings.first_message_answer_all),
            "reply_delay_seconds": [
                general_settings.reply_delay_min_seconds,
                general_settings.reply_delay_max_seconds,
            ],
            "skip_if_no_reply_seconds": (
                [
                    general_settings.skip_if_no_reply_ms / 1000.0,
                    general_settings.skip_if_no_reply_max_ms / 1000.0,
                ]
                if general_settings.skip_if_no_reply_ms
                else "off"
            ),
            "page_scan_interval_seconds": general_settings.poll_interval_ms / 1000.0,
        }
        return {
            "bundle": "chitchat-python-showmode-debug",
            "generated": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + "Z",
            "privacy": (
                "no API token, no cookies, no login data, no proxy passwords, no message bodies; "
                "log lines and the browser rows can contain partner display names and page routes"
            ),
            "system": dict(self.system_info(), executable=sys.executable, playwright_installed=playwright_ok),
            "checks": self.diagnostics(deep=False),
            "run": {
                "running": running,
                "status": status,
                "observed_rows": self.feed.observed_rows(),
                "lines": overview["lines"],
                "workers_paused": overview["workers_paused"],
                "chats": overview["chats"],
                "next_action": overview["next_action"],
            },
            "effective": effective,
            "workers": [
                {key: worker.get(key) for key in (
                    "index", "total", "profile", "channel", "proxy", "state", "note",
                    "activity", "activity_note", "paused", "phase", "chats", "sent", "incoming",
                    "user_lines", "bot_lines", "skips", "no_reply", "last_activity",
                )}
                for worker in self.workers()
            ],
            "chat": {"stats": chat.get("stats") or {}, "sessions": len(chat.get("sessions") or [])},
            "flow": self.flow_state(),
            "settings": {
                "general": general,
                "browser": browser,
                "message": message,
                "proxy": {
                    "enabled": bool(proxy_settings.enabled),
                    "entries_masked": self._mask_proxy_lines(proxy_settings.entries),
                },
            },
            "crash_files": crash_files[-20:],
            "logs": self.logs.entries(300),
        }

    def system_info(self) -> dict:
        return {
            "app": __version__,
            "python": platform.python_version(),
            "platform": platform.platform(),
            "log_file": str(self.logs.path),
            "data_dir": str(self.data_dir),
            "uptime_seconds": int(time.time() - self.started_at),
            "log_bytes": self.logs_file_size(),
        }

    # -- diagnostics (shared with `python app.py --diag`) -------------------

    def diagnostics(self, deep: bool = True) -> dict:
        """Preflight: every reason a run could fail is checked before RUN."""
        return run_diagnostics(self.data_dir, deep=deep)

    def logs_file_size(self) -> int:
        try:
            return self.logs.path.stat().st_size
        except OSError:
            return 0

    def logs_file_bytes(self) -> bytes:
        """The raw port.log, for the Logs tab's DOWNLOAD button."""
        try:
            with open(self.logs.path, "rb") as handle:
                return handle.read()
        except FileNotFoundError:
            return b""

    def validate_proxy_entries(self, raw: str, default_scheme: str = "") -> dict:
        """Live validation for the Proxy tab: per-line result, nothing saved."""
        text, error = self.apply_default_scheme(raw, default_scheme)
        if error:
            return {"ok": False, "error": error, "lines": []}
        rows = []
        for number, line in enumerate((text or "").replace("\r", "").split("\n"), start=1):
            if not line.strip():
                continue
            try:
                entry = proxy.parse_line(line)
            except ValueError as problem:
                rows.append({"line": number, "ok": False, "error": str(problem)})
                continue
            rows.append(
                {
                    "line": number,
                    "ok": True,
                    "label": entry.label(),
                    "authenticated": entry.has_auth(),
                }
            )
        return {
            "ok": all(row["ok"] for row in rows),
            "valid": sum(1 for row in rows if row["ok"]),
            "invalid": sum(1 for row in rows if not row["ok"]),
            "lines": rows,
        }

    @staticmethod
    def apply_default_scheme(raw: str, default_scheme: str) -> tuple[str, str]:
        """Turn bare ``host:port`` lines into the selected protocol (like Go).

        The Go dashboard applies its default-protocol dropdown to plain lines
        only; explicit ``http://`` / ``socks5://`` prefixes always win. This is
        the single place the rule lives on the server (the UI sends raw text).
        """
        text = str(raw or "")
        scheme = str(default_scheme or "").strip().lower()
        if not scheme or scheme == proxy.HTTP_SCHEME:
            return text, ""
        if scheme not in ("http", "socks5", "socks5h", "sock5"):
            return text, "unsupported default protocol; use HTTP or SOCKS5"
        prefix = proxy.SOCKS5_SCHEME + "://"
        fixed = []
        for line in text.replace("\r", "").split("\n"):
            stripped = line.strip()
            if stripped and "://" not in stripped:
                fixed.append(prefix + stripped)
            else:
                fixed.append(line)
        return "\n".join(fixed), ""

    # -- guided flow -------------------------------------------------------
    FLOW_STAGES = ("message", "proxy", "setting", "browser", "live")

    def flow_state(self) -> dict:
        """Which guided step the operator is on. Data-driven, never timed."""
        flow = self.store.load_flow_state()
        message = self.store.load_message_settings()
        proxy_settings = self.store.load_proxy_settings()
        with self._lock:
            running = bool(self._thread and self._thread.is_alive())
            login_saved = bool(self._login_saved)
        live = self.feed.observed_rows() > 0
        needs_usernames = self.sequence_wants_a_username(message) and not message.username_list()
        complete = {
            # A sequence that uses %username% is not usable without a username.
            "message": bool(message.sequence_lines() or message.loaded_sequences) and not needs_usernames,
            "proxy": bool(flow["proxy_decided"] or (proxy_settings.enabled and proxy_settings.entries.strip())),
            "setting": bool(flow["setting_saved"]),
            "browser": bool(running and login_saved),
            "live": live,
        }
        # Unlocking the console is one-way: the run may stop or the log may be
        # cleared, and the operator should not be pushed back into the wizard.
        if live and not flow["console_unlocked"]:
            flow = self.store.save_flow_state({"console_unlocked": True})
        # START gate: Message -> Proxy -> Setting must be done, then the
        # operator presses START; only then does the Browser step (RUN) exist.
        missing = [name for name in ("message", "proxy", "setting") if not complete[name]]
        ready = not missing
        started = bool(flow["started"]) and ready
        stage = "live"
        for name in self.FLOW_STAGES:
            if not complete[name]:
                stage = name
                break
        if ready and not started and stage in ("browser", "live"):
            stage = "setting"  # parked on the last setup step until START
        return {
            "stage": stage,
            "complete": complete,
            "needs_usernames": needs_usernames,
            "ready_to_start": ready,
            "missing": missing,
            "started": started,
            "unlocked": bool(flow["console_unlocked"]),
            "hold": bool(flow["hold"]),
            "rail_side": flow["rail_side"],
        }

    def start_gate_error(self) -> str:
        """Empty when START / RUN is allowed, else the operator-facing reason."""
        missing = self.flow_state()["missing"]
        if not missing:
            return ""
        labels = {"message": "MESSAGE", "proxy": "PROXY", "setting": "SETTING"}
        return "Finish these first: " + ", ".join(labels[name] for name in missing)

    def press_start(self) -> dict:
        """The dashboard START button: opens the Browser step."""
        error = self.start_gate_error()
        if error:
            return {"ok": False, "error": error, "flow": self.flow_state()}
        self.store.save_flow_state({"started": True})
        self.logs.append("info", "START pressed. Browser step is open: choose the browser count and press RUN.")
        return {"ok": True, "flow": self.flow_state()}

    def press_stop(self) -> dict:
        """The dashboard STOP button: stops a run and closes the Browser step."""
        result = self.stop_show_mode()
        self.store.save_flow_state({"started": False})
        result["flow"] = self.flow_state()
        return result

    def save_flow_prefs(self, payload: dict) -> dict:
        """HOLD, rail side and the SKIP decision; all persisted for the reload.

        ``console_unlocked`` is stored when the operator skips the guided flow,
        so a reload does not lock the console again.
        """
        changes = {}
        for key in ("hold", "rail_side", "console_unlocked"):
            if key in (payload or {}) and payload[key] is not None:
                changes[key] = payload[key]
        if (payload or {}).get("started") is not None:
            if payload["started"] and self.start_gate_error():
                raise ValueError(self.start_gate_error())
            changes["started"] = bool(payload["started"])
        self.store.save_flow_state(changes)
        return self.flow_state()

    def reset_flow(self) -> dict:
        """Start the guided flow over; saved settings are left untouched."""
        flow = self.store.load_flow_state()
        self.store.save_flow_state(
            {
                "proxy_decided": False,
                "setting_saved": False,
                "console_unlocked": False,
                "hold": False,
                "rail_side": flow["rail_side"],
                "started": False,
            }
        )
        return self.flow_state()

    def save_browser_settings(self, payload: dict) -> dict:
        current = asdict(self.store.load_browser_settings())
        current.update({k: v for k, v in (payload or {}).items() if k in current})
        settings = self.store.save_browser_settings(coerce_settings(BrowserSettings, current))
        self.logs.append(
            "info",
            "Browser settings saved. Channel={} Profile={}".format(settings.channel, settings.profile),
        )
        return asdict(settings)

    def save_general_settings(self, payload: dict) -> dict:
        current = asdict(self.store.load_general_settings())
        # coerce_settings skips bad types instead of crashing on form input.
        current.update({k: v for k, v in (payload or {}).items() if k in current})
        settings = self.store.save_general_settings(coerce_settings(GeneralSettings, current))
        self.store.save_flow_state({"setting_saved": True})
        # A silence limit shorter than the reply delay abandons chats before the
        # bot has even answered: the operator must see that, not a mystery.
        if (
            settings.skip_if_no_reply_ms
            and settings.skip_if_no_reply_max_ms < settings.reply_delay_max_seconds * 1000
        ):
            self.logs.append(
                "warn",
                "Skip if no reply={}-{}s is shorter than Reply delay max={}s: the bot will leave "
                "chats before its own reply is due. Set it well above the reply delay (Go default "
                "60-90s) or use 0 to never leave a quiet chat.".format(
                    seconds_label(settings.skip_if_no_reply_ms / 1000.0),
                    seconds_label(settings.skip_if_no_reply_max_ms / 1000.0),
                    seconds_label(settings.reply_delay_max_seconds),
                ),
            )
        # The dashboard edits the same seconds fields the Go app saves; the log
        # says the same thing in seconds (Go: "General settings saved. ...").
        self.logs.append(
            "info",
            "General settings saved. Reply delay={}-{}s Skip if no reply={} First={}".format(
                seconds_label(settings.reply_delay_min_seconds),
                seconds_label(settings.reply_delay_max_seconds),
                "{}-{}s".format(
                    seconds_label(settings.skip_if_no_reply_ms / 1000.0),
                    seconds_label(settings.skip_if_no_reply_max_ms / 1000.0),
                )
                if settings.skip_if_no_reply_ms
                else "off",
                settings.effective_first_message_mode(),
            )
            + " AnswerAll={}".format("on" if settings.first_message_answer_all else "off"),
        )
        return asdict(settings)

    def proxy_state(self) -> dict:
        """What the dashboard needs to draw the Proxy tab."""
        settings = self.store.load_proxy_settings()
        state = {"enabled": bool(settings.enabled), "entries": settings.entries, "lines": 0, "error": ""}
        if settings.enabled:
            try:
                state["lines"] = len(proxy.parse_list(settings.entries))
            except ValueError as error:
                state["error"] = str(error)
        return state

    def load_proxy_rotator(self):
        """Ported from ``loadProxyRotator`` / ``loadProxyEntries``."""
        settings = self.store.load_proxy_settings()
        if not settings.enabled:
            return None, 0
        entries = proxy.parse_list(settings.entries)
        if not entries:
            raise ValueError("proxy list is enabled but empty")
        return proxy.Rotator(entries), len(entries)

    def save_proxy_settings(self, payload: dict) -> dict:
        """Ported from ``SaveProxySettings``: same validation and the same log lines.

        The default-protocol dropdown applies to plain lines only (Go rule);
        the UI sends the raw text plus ``default_scheme``, so this is the one
        place the rule lives and the saved text carries the protocol.
        """
        current = asdict(self.store.load_proxy_settings())
        current.update({key: value for key, value in (payload or {}).items() if key in current})
        settings = coerce_settings(ProxySettings, current)
        text, scheme_error = self.apply_default_scheme(
            settings.entries, str((payload or {}).get("default_scheme") or "")
        )
        if scheme_error:
            raise ValueError(scheme_error)
        settings.entries = text
        if settings.enabled:
            entries = proxy.parse_list(settings.entries)
            if not entries:
                raise ValueError("proxy list is enabled but empty")
        saved = self.store.save_proxy_settings(settings)
        self.store.save_flow_state({"proxy_decided": True})
        if saved.enabled:
            entries = proxy.parse_list(saved.entries)
            self.logs.append(
                "info",
                "Proxy settings saved. {} proxy line(s) enabled for Rod, ChromeDP, and Go request "
                "rotation.".format(len(entries)),
            )
        else:
            self.logs.append("info", "Proxy settings saved. Proxy use is disabled.")
        return saved.payload()

    def test_proxy_entries(self, raw: str, endpoint: str = proxy.TEST_ENDPOINT) -> list:
        """Test the form WITHOUT saving it and without exposing credentials."""
        return [result.to_dict() for result in proxy.test_list(raw, endpoint)]

    def save_message_settings(self, payload: dict) -> dict:
        current = asdict(self.store.load_message_settings())
        current.update({k: v for k, v in (payload or {}).items() if k in current})
        settings = self.store.save_message_settings(coerce_settings(MessageSettings, current))
        # A sequence that needs a username but has none cannot reply. The save is
        # allowed (the operator may still be typing the handles), but the state
        # says so and the guided MESSAGE step / RUN gate refuse to move on.
        if self.sequence_wants_a_username(settings) and not settings.username_list():
            self.logs.append(
                "warn",
                "Message settings saved, but the sequence uses %username% / {snap} "
                "and no Username line is saved: Show Mode will refuse to reply.",
            )
        single = len(settings.sequence_lines())
        folder = len([seq for seq in settings.loaded_sequences if str(seq).strip()])
        self.logs.append(
            "info",
            "Message settings saved. Mode={} single_lines={} folder_files={} usernames={}".format(
                settings.effective_chat_mode(), single, folder, len(settings.username_list())
            ),
        )
        return self.message_state()

    def clear_logs(self) -> dict:
        self.logs.clear()
        self.logs.append("info", "Logs cleared.")
        return {"ok": True}

    def _apply_loaded_sequences(self, sequences: list[str], source: str) -> dict:
        """Shared tail of the folder loaders: folder mode on, sequences stored."""
        settings = self.store.load_message_settings()
        settings.chat_mode = "folder"
        settings.loaded_sequences = sequences
        settings.use_loaded_sequences = True
        settings = self.store.save_message_settings(settings)
        self.logs.append(
            "info", "Loaded {} sequence file(s) from {}.".format(len(settings.loaded_sequences), source)
        )
        return {"ok": True, "count": len(settings.loaded_sequences), "source": source}

    def load_message_folder(self, directory: str) -> dict:
        """Ported from ``LoadMessageFolder``: every non-empty .txt is one sequence."""
        folder = Path(directory)
        sequences: list[str] = []
        try:
            for entry in sorted(folder.iterdir()):
                if entry.is_dir() or entry.suffix.lower() != ".txt":
                    continue
                try:
                    content = entry.read_text(encoding="utf-8", errors="replace").strip()
                except OSError:
                    continue
                if content:
                    sequences.append(content)
        except OSError as error:
            return {"ok": False, "error": str(error)}
        if not sequences:
            return {"ok": False, "error": "no valid .txt files found in the selected folder"}
        return self._apply_loaded_sequences(sequences, str(folder))

    def load_message_files(self, files: list) -> dict:
        """The browser folder picker: same rules, contents instead of a path.

        The browser cannot hand over a path, so the picked files arrive as
        ``[{"name": "chat1.txt", "text": "..."}]``. The rule set stays the one
        from ``LoadMessageFolder``: top level only, ``.txt`` only, non-empty
        after trimming.
        """
        if len(files) > 1000:
            raise ValueError("too many files (max 1000 per load)")
        sequences: list[str] = []
        skipped = 0
        for item in files:
            if not isinstance(item, dict):
                skipped += 1
                continue
            name = str(item.get("name") or "")
            # A picked sub-folder arrives as "top/sub/file.txt": Go reads the
            # selected folder only, never its sub-folders.
            if name.replace("\\", "/").count("/") > 1 or not name.lower().endswith(".txt"):
                skipped += 1
                continue
            text = str(item.get("text") or "").strip()
            if text:
                sequences.append(text)
            else:
                skipped += 1
        if not sequences:
            return {"ok": False, "error": "no valid .txt files found in the selected folder"}
        result = self._apply_loaded_sequences(sequences, "the browser folder picker")
        result["skipped"] = skipped
        return result

    # -- run control -------------------------------------------------------

    def start_show_mode(self, count: int = 1) -> dict:
        try:
            count = int(count)
        except (TypeError, ValueError):
            count = 1
        count = max(1, min(5, count))
        # The server enforces the order too: RUN cannot be reached by an old
        # tab, a shortcut or an API call before Message, Proxy and Setting.
        gate = self.start_gate_error()
        if gate:
            self.logs.append("warn", "RUN refused. " + gate)
            return {"ok": False, "error": gate}
        with self._lock:
            if self._thread and self._thread.is_alive():
                return {"ok": False, "error": "Show Mode is already running"}
            self._stop = threading.Event()
            self._status = "starting"
            self._login_saved = ""
            # A new run starts with a clean Overview table.
            self._workers.clear()
            self._worker_events.clear()
            self._thread = threading.Thread(
                target=self._run_batch, args=(count,), name="show-mode", daemon=True
            )
            self._thread.start()
        self.store.save_flow_state({"started": True})
        return {"ok": True}

    def stop_show_mode(self) -> dict:
        with self._lock:
            stop = self._stop
            events = list(self._worker_events.values())
            self._status = "stopping"
            for worker in self._workers.values():
                if worker.get("state") not in ("done", "error"):
                    worker["state"] = "closing"
        if stop is not None:
            stop.set()
        for event in events:
            event.set()
        if events:
            self.logs.append(
                "info",
                "Stopping Show Mode: every browser closes after its current step "
                "({} worker(s)).".format(len(events)),
            )
        else:
            self.logs.append("info", "Stopping Show Mode (no browser is running).")
        return {"ok": True}

    def shutdown(self) -> None:
        self.stop_show_mode()

    # -- internals ---------------------------------------------------------

    def _run_batch(self, count: int) -> None:
        try:
            browser = self.store.load_browser_settings()
            rotator, proxy_count = self.load_proxy_rotator()
            if browser.headless:
                self.logs.append(
                    "warn",
                    "Show Mode needs a visible browser window; ignoring the Headless checkbox for this run.",
                )
                browser.headless = False
            profile = safe_profile_name(browser.profile)
            profile_dir = self.data_dir / "profiles" / profile
            profile_dir.mkdir(parents=True, exist_ok=True)
            self.logs.append(
                "info",
                "Starting {} fresh {} browser(s) for profile '{}'.".format(
                    count, browser.channel, profile
                ),
            )
            self.logs.append("info", "Fresh login JSON files will be saved under {}".format(profile_dir))
            if proxy_count > 0:
                self.logs.append(
                    "info",
                    "Fresh browsers will use {} proxy line(s) in rotation.".format(proxy_count),
                )
            workers = []
            for index in range(1, count + 1):
                entry = rotator.next() if rotator is not None else None
                event = threading.Event()
                with self._lock:
                    self._worker_events[index] = event
                    self._workers[index] = {
                        "index": index,
                        "total": count,
                        "profile": profile,
                        "channel": browser.channel,
                        "headless": bool(browser.headless),
                        "proxy": entry.label() if entry is not None else "direct",
                        "state": "starting",
                        "note": "queued",
                        "started_at": time.time(),
                        "updated_at": time.time(),
                        "login_saved": "",
                        "conversation": "",
                        "partner": "",
                        "phase": "",
                        "chats": 0,
                        "sent": 0,
                        "incoming": 0,
                        "skips": 0,
                        "no_reply": 0,
                        # Real line counters (SENT counts lines, user_lines counts
                        # the partner's lines the runner read) and the activity the
                        # runner publishes, so RUNNING is never the only thing shown.
                        "user_lines": 0,
                        "bot_lines": 0,
                        "activity": "",
                        "activity_note": "",
                        "paused": False,
                        "last_activity": "",
                    }
                worker = threading.Thread(
                    target=self._run_one,
                    args=(profile_dir, profile, browser, index, count, entry, event),
                    name="show-mode-{}".format(index),
                    daemon=True,
                )
                worker.start()
                workers.append(worker)
            for worker in workers:
                worker.join()
        except Exception as error:  # noqa: BLE001 - surfaced in the log/UI
            self.report_crash("Show Mode failed:", error)
        finally:
            with self._lock:
                self._status = "idle"
                self._stop = None
                # The finished workers stay in the registry so the Overview can
                # still show what each browser did; their stop events are done.
                self._worker_events.clear()

    def _run_one(
        self,
        profile_dir: Path,
        profile: str,
        browser,
        index: int,
        total: int,
        proxy_entry=None,
        stop: threading.Event | None = None,
    ) -> None:
        prefix = "[{} fresh {}/{}]".format(profile, index, total)
        temp_dir = Path(
            tempfile.mkdtemp(prefix="chitchat-port-{}-{:02d}-".format(profile, index))
        )
        backend = None
        reason = "stopped"  # a `return` inside the try skips the else-branch
        stop = stop if stop is not None else threading.Event()
        try:
            user_data_dir = temp_dir / "user-data"
            user_data_dir.mkdir(parents=True, exist_ok=True)
            backend = PlaywrightDOMBackend(
                user_data_dir=user_data_dir,
                channel=browser.channel,
                headless=browser.headless,
                proxy=proxy_entry,
            )
            self._worker_update(index, state="opening", note="opening a fresh browser")
            self.logs.append(
                "info",
                prefix + " Opening brand-new {} browser. Old profile data is not used.".format(
                    browser.channel
                ),
            )
            if proxy_entry is not None:
                self.logs.append("info", prefix + " Using proxy " + proxy_entry.label())
            backend.start()
            with self._lock:
                self._status = "waiting-login"
            self._worker_update(index, state="waiting-login", note="waiting for the manual login")
            login = self._wait_for_login(backend, profile, index, total, stop)
            if login is None:
                reason = "stop was pressed" if stop.is_set() else "no login was saved"
                self._worker_update(index, state="done", note=reason)
                return
            login["profile"] = profile
            login["user_id"] = current_user_id(login)
            login_path = fresh_login_info_path(profile_dir, login, index)
            save_json(login_path, login)
            with self._lock:
                self._login_saved = str(login_path)
            self._worker_update(index, login_saved=str(login_path), note="login saved")
            self.logs.append(
                "info",
                prefix + " Login saved to {}. Temporary browser data will be removed after close.".format(
                    login_path
                ),
            )
            self._run_live_chat(backend, profile, index, total, stop)
        except PageGoneError:
            reason = "browser window closed by the user or by Chrome"
            self._worker_update(index, state="done", note=reason)
            self.logs.append("info", prefix + " Fresh browser was closed before login was saved.")
        except Exception as error:  # noqa: BLE001 - one browser failing must not kill the batch
            reason = self.report_crash(prefix, error)
            self._worker_update(index, state="error", note=reason[:200])
        else:
            reason = "stop was pressed" if stop.is_set() else "run finished"
            self._worker_update(index, state="done", note=reason)
        finally:
            if backend is not None:
                backend.close()
                # The operator sees the window vanish: always say why.
                self.logs.append("info", prefix + " Browser closed - reason: {}".format(reason))
            self._remove_browser_data(temp_dir, prefix)
            with self._lock:
                if self._status not in ("idle", "stopping"):
                    self._status = "idle"

    def _wait_for_login(
        self,
        backend: PlaywrightDOMBackend,
        profile: str,
        index: int,
        total: int,
        stop: threading.Event | None = None,
    ):
        waiting_token_logged = False
        waiting_session_logged = False
        stop = stop if stop is not None else threading.Event()
        busy_logged_at = 0.0
        while not stop.is_set():
            try:
                login = backend.capture_session()
                token = token_from_login(login)
                authenticated = bool(token) and backend.session_authenticated()
            except PageGoneError:
                raise
            except Exception as error:  # noqa: BLE001 - the page is navigating / logging in
                # Never a crash: a login redirect, a reload or a slow page just
                # means "ask again in 2 s". Say so at most once a minute.
                if time.time() - busy_logged_at > 60:
                    busy_logged_at = time.time()
                    self.logs.append(
                        "info",
                        "[{} fresh {}/{}] Browser is busy ({}); keep logging in, checking again.".format(
                            profile, index, total, str(error).splitlines()[0][:120]
                        ),
                    )
                if stop.wait(2.0):
                    return None
                continue
            if authenticated:
                if not current_user_id(login):
                    raise RuntimeError(
                        "login detected, but current bot user ID could not be read from token or browser storage"
                    )
                return login
            if not token and not waiting_token_logged:
                waiting_token_logged = True
                self.logs.append(
                    "info",
                    "[{} fresh {}/{}] Waiting for login token in the new {} browser window.".format(
                        profile, index, total, backend.channel or backend.requested_channel
                    ),
                )
            if token and not waiting_session_logged:
                waiting_session_logged = True
                self.logs.append(
                    "info",
                    "[{} fresh {}/{}] Token detected, but the session is still unauthorized. "
                    "Finish the full login in the {} browser window.".format(
                        profile, index, total, backend.channel or backend.requested_channel
                    ),
                )
            if stop.wait(2.0):
                return None
        return None

    def _worker_logf(self, index: int) -> Callable[[str, str], None]:
        """Log like before, and keep this worker's Overview row current.

        The runner already writes every state change as an ACTIVITY line, so the
        counters are read from those lines instead of duplicating the logic.
        """

        def logf(level: str, message: str) -> None:
            text = str(message or "")
            updates = {"last_activity": text[:160]}
            worker = self._workers.get(index) or {}
            if "SEND CONFIRMED" in text:
                updates["sent"] = int(worker.get("sent") or 0) + 1
            if "INCOMING |" in text:
                updates["incoming"] = int(worker.get("incoming") or 0) + 1
            if "CHAT STARTED" in text:
                updates["chats"] = int(worker.get("chats") or 0) + 1
            if "SKIP |" in text:
                updates["skips"] = int(worker.get("skips") or 0) + 1
            if "NO REPLY |" in text:
                updates["no_reply"] = int(worker.get("no_reply") or 0) + 1
            # Real line counts: the runner reports running totals in its own log
            # line, so SENT (lines) and the partner's lines can be compared.
            for key, pattern in (("user_lines", r"total_user_lines=(\d+)"), ("bot_lines", r"total_bot_lines=(\d+)")):
                match = re.search(pattern, text)
                if match:
                    updates[key] = int(match.group(1))
            self._worker_update(index, **updates)
            self.logs.append(level, message)

        return logf

    def _run_live_chat(
        self,
        backend: PlaywrightDOMBackend,
        profile: str,
        index: int,
        total: int,
        stop: threading.Event | None = None,
    ) -> None:
        prefix = "[{} show-live {}/{}]".format(profile, index, total)
        general = self.store.load_general_settings()
        feed = self.feed
        run = feed.start("{} · Browser {}/{}".format(profile, index, total))

        def on_snapshot(snapshot) -> None:
            controls = getattr(snapshot, "controls", None)
            if controls is None:
                return
            self._worker_update(
                index,
                conversation=str(getattr(controls, "conversation", "") or ""),
                partner=str(getattr(controls, "partner", "") or ""),
                phase=str(getattr(controls, "phase", "") or ""),
            )

        def on_state(activity: str, note: str) -> None:
            """The runner's real activity, so RUNNING is never the whole story.

            A paused runner (unconfirmed send, missing identity, engine failure)
            keeps its browser row honest instead of looking busy forever.
            """
            text = str(note or "")
            self._worker_update(
                index,
                activity=str(activity or ""),
                activity_note=text[:160],
                paused=(activity == "paused"),
            )

        observed = ObservedDOM(backend, feed, run, on_snapshot=on_snapshot)
        rng = random.Random()
        self._worker_update(index, state="running", note="watching chats")
        with self._lock:
            self._status = "running"
        self.logs.append(
            "activity",
            prefix + " DOM READY | browser-only chat watcher started; close its browser window to stop",
        )
        runner = LiveRunner(
            dom=observed,
            prefix=prefix,
            logf=self._worker_logf(index),
            new_responder=lambda: make_responder(self.store.load_message_settings(), rng),
            first_message_mode=lambda: self.store.load_general_settings().effective_first_message_mode(),
            answer_all_rows=lambda: self.store.load_general_settings().first_message_answer_all,
            on_state=on_state,
            rng=rng,
            reply_delay=max(0.0, general.reply_delay_min_seconds or 0),
            reply_delay_max=max(0.0, general.reply_delay_max_seconds or 0),
            poll_interval=max(0.05, general.poll_interval_ms / 1000.0),
            no_reply_seconds=max(0.0, general.skip_if_no_reply_ms / 1000.0),
            no_reply_max_seconds=max(0.0, general.skip_if_no_reply_max_ms / 1000.0),
            stop=stop if stop is not None else threading.Event(),
        )
        try:
            runner.run()
        finally:
            feed.end(run)
            self._worker_update(index, state="closing", note="closing the browser")
            self.logs.append("activity", prefix + " LIVE CHAT stopped")

    def _remove_browser_data(self, temp_dir: Path, prefix: str) -> None:
        last_error = None
        for attempt in range(5):
            try:
                shutil.rmtree(temp_dir)
                self.logs.append("info", prefix + " Removed temporary browser data from {}".format(temp_dir))
                return
            except FileNotFoundError:
                return
            except OSError as error:
                last_error = error
                time.sleep(0.3 * (attempt + 1))
        if last_error is not None:
            self.logs.append(
                "warn",
                prefix + " Could not remove temporary browser data from {}: {}".format(temp_dir, last_error),
            )
