"""Application service: settings + logs + transcript + the Show Mode run loop.

Mirrors the Go wiring in ``app.go`` / ``live_chat.go``:
fresh temporary browser data, login wait with the same log messages, login JSON
written to ``profiles/<profile>/``, then the DOM runner until the window closes.
"""

from __future__ import annotations

import random
import shutil
import tempfile
import threading
import time
from dataclasses import asdict
from pathlib import Path

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
from .runner import LiveRunner
from .settings import (
    BrowserSettings,
    GeneralSettings,
    MessageSettings,
    SettingsStore,
    save_json,
)

WINDOWS_RESERVED = {"CON", "PRN", "AUX", "NUL"}


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

    def __init__(self, backend: PlaywrightDOMBackend, feed: HomeChatFeed, run: int):
        self.backend = backend
        self.feed = feed
        self.run = run

    def read_dom_snapshot(self):
        snapshot = self.backend.read_dom_snapshot()
        self.feed.observe(self.run, snapshot, False)
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
    def __init__(self, data_dir: str | Path):
        self.data_dir = Path(data_dir)
        self.store = SettingsStore(self.data_dir)
        self.logs = ActivityLog(self.data_dir / "logs")
        self.feed = HomeChatFeed()
        self._lock = threading.Lock()
        self._stop: threading.Event | None = None
        self._thread: threading.Thread | None = None
        self._status = "idle"
        self._login_saved = ""

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
            "browser": asdict(self.store.load_browser_settings()),
            "general": asdict(self.store.load_general_settings()),
            "message": asdict(self.store.load_message_settings()),
            "logs": self.logs.entries(300),
            "chat": self.feed.snapshot(selected),
        }

    def save_browser_settings(self, payload: dict) -> dict:
        settings = BrowserSettings(
            profile=str((payload or {}).get("profile") or ""),
            channel=str((payload or {}).get("channel") or "auto"),
            headless=bool((payload or {}).get("headless")),
        )
        settings = self.store.save_browser_settings(settings)
        self.logs.append(
            "info",
            "Browser settings saved. Channel={} Profile={}".format(settings.channel, settings.profile),
        )
        return asdict(settings)

    def save_general_settings(self, payload: dict) -> dict:
        current = self.store.load_general_settings()
        for key in asdict(current):
            if key in (payload or {}):
                setattr(current, key, payload[key])
        settings = self.store.save_general_settings(current)
        # Same log line as the Go dashboard, including the effective mode.
        self.logs.append(
            "info",
            "General settings saved. Poll={}s-{}s Action={}s-{}s First={}".format(
                settings.poll_min_seconds,
                settings.poll_max_seconds,
                settings.action_min_seconds,
                settings.action_max_seconds,
                settings.effective_first_message_mode(),
            ),
        )
        return asdict(settings)

    def save_message_settings(self, payload: dict) -> dict:
        current = self.store.load_message_settings()
        for key in asdict(current):
            if key in (payload or {}):
                setattr(current, key, payload[key])
        settings = self.store.save_message_settings(current)
        self.logs.append("info", "Message settings saved. Mode={}".format(settings.effective_chat_mode()))
        return asdict(settings)

    def clear_logs(self) -> dict:
        self.logs.clear()
        self.logs.append("info", "Logs cleared.")
        return {"ok": True}

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

        settings = self.store.load_message_settings()
        settings.chat_mode = "folder"
        settings.loaded_sequences = sequences
        settings.use_loaded_sequences = True
        settings = self.store.save_message_settings(settings)
        self.logs.append(
            "info", "Loaded {} sequence file(s) from {}.".format(len(settings.loaded_sequences), folder)
        )
        return {"ok": True, "count": len(settings.loaded_sequences)}

    # -- run control -------------------------------------------------------

    def start_show_mode(self, count: int = 1) -> dict:
        try:
            count = int(count)
        except (TypeError, ValueError):
            count = 1
        count = max(1, min(5, count))
        with self._lock:
            if self._thread and self._thread.is_alive():
                return {"ok": False, "error": "Show Mode is already running"}
            self._stop = threading.Event()
            self._status = "starting"
            self._login_saved = ""
            self._thread = threading.Thread(
                target=self._run_batch, args=(count,), name="show-mode", daemon=True
            )
            self._thread.start()
        return {"ok": True}

    def stop_show_mode(self) -> dict:
        with self._lock:
            stop = self._stop
            self._status = "stopping"
        if stop is not None:
            stop.set()
        self.logs.append("info", "Stopping Show Mode (the browser window is left open for you).")
        return {"ok": True}

    def shutdown(self) -> None:
        self.stop_show_mode()

    # -- internals ---------------------------------------------------------

    def _run_batch(self, count: int) -> None:
        try:
            browser = self.store.load_browser_settings()
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
            workers = []
            for index in range(1, count + 1):
                worker = threading.Thread(
                    target=self._run_one,
                    args=(profile_dir, profile, browser, index, count),
                    name="show-mode-{}".format(index),
                    daemon=True,
                )
                worker.start()
                workers.append(worker)
            for worker in workers:
                worker.join()
        except Exception as error:  # noqa: BLE001 - surfaced in the log/UI
            self.logs.append("error", "Show Mode failed: {}".format(error))
        finally:
            with self._lock:
                self._status = "idle"

    def _run_one(self, profile_dir: Path, profile: str, browser, index: int, total: int) -> None:
        prefix = "[{} fresh {}/{}]".format(profile, index, total)
        temp_dir = Path(
            tempfile.mkdtemp(prefix="chitchat-port-{}-{:02d}-".format(profile, index))
        )
        backend = None
        try:
            user_data_dir = temp_dir / "user-data"
            user_data_dir.mkdir(parents=True, exist_ok=True)
            backend = PlaywrightDOMBackend(
                user_data_dir=user_data_dir,
                channel=browser.channel,
                headless=browser.headless,
            )
            self.logs.append(
                "info",
                prefix + " Opening brand-new {} browser. Old profile data is not used.".format(
                    browser.channel
                ),
            )
            backend.start()
            with self._lock:
                self._status = "waiting-login"
            login = self._wait_for_login(backend, profile, index, total)
            if login is None:
                return
            login["profile"] = profile
            login["user_id"] = current_user_id(login)
            login_path = fresh_login_info_path(profile_dir, login, index)
            save_json(login_path, login)
            with self._lock:
                self._login_saved = str(login_path)
            self.logs.append(
                "info",
                prefix + " Login saved to {}. Temporary browser data will be removed after close.".format(
                    login_path
                ),
            )
            self._run_live_chat(backend, profile, index, total)
        except PageGoneError:
            self.logs.append("info", prefix + " Fresh browser was closed before login was saved.")
        except Exception as error:  # noqa: BLE001 - one browser failing must not kill the batch
            self.logs.append("error", prefix + " {}".format(error))
        finally:
            if backend is not None:
                backend.close()
            self._remove_browser_data(temp_dir, prefix)
            with self._lock:
                if self._status not in ("idle", "stopping"):
                    self._status = "idle"

    def _wait_for_login(self, backend: PlaywrightDOMBackend, profile: str, index: int, total: int):
        waiting_token_logged = False
        waiting_session_logged = False
        stop = self._stop
        while stop is not None and not stop.is_set():
            login = backend.capture_session()
            token = token_from_login(login)
            if token and backend.session_authenticated():
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

    def _run_live_chat(self, backend: PlaywrightDOMBackend, profile: str, index: int, total: int) -> None:
        prefix = "[{} show-live {}/{}]".format(profile, index, total)
        general = self.store.load_general_settings()
        feed = self.feed
        run = feed.start("{} · Browser {}/{}".format(profile, index, total))
        observed = ObservedDOM(backend, feed, run)
        rng = random.Random()
        with self._lock:
            self._status = "running"
        self.logs.append(
            "activity",
            prefix + " DOM READY | browser-only chat watcher started; close its browser window to stop",
        )
        runner = LiveRunner(
            dom=observed,
            prefix=prefix,
            logf=self.logs.append,
            new_responder=lambda: make_responder(self.store.load_message_settings(), rng),
            first_message_mode=lambda: self.store.load_general_settings().effective_first_message_mode(),
            rng=rng,
            reply_delay=max(0.0, general.reply_delay_ms / 1000.0),
            poll_interval=max(0.05, general.poll_interval_ms / 1000.0),
            stop=self._stop,
        )
        try:
            runner.run()
        finally:
            feed.end(run)
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
