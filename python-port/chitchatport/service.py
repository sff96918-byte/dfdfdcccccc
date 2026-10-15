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
        }

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
        return {
            **asdict(settings),
            "needs_usernames": bool(wants and not settings.username_list()),
            "sequence_lines": len(settings.sequence_lines()),
        }

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

        Masks proxy passwords, never includes the API token or login cookies,
        and lists the crash files so the operator knows what to attach.
        """
        general = asdict(self.store.load_general_settings())
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
        return {
            "bundle": "chitchat-python-showmode-debug",
            "generated": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + "Z",
            "system": dict(self.system_info(), executable=sys.executable, playwright_installed=playwright_ok),
            "run": {"running": running, "status": status, "observed_rows": self.feed.observed_rows()},
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
        }

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
        stage = "live"
        for name in self.FLOW_STAGES:
            if not complete[name]:
                stage = name
                break
        return {
            "stage": stage,
            "complete": complete,
            "needs_usernames": needs_usernames,
            "unlocked": bool(flow["console_unlocked"]),
            "hold": bool(flow["hold"]),
            "rail_side": flow["rail_side"],
        }

    def save_flow_prefs(self, payload: dict) -> dict:
        """HOLD, rail side and the SKIP decision; all persisted for the reload.

        ``console_unlocked`` is stored when the operator skips the guided flow,
        so a reload does not lock the console again.
        """
        changes = {}
        for key in ("hold", "rail_side", "console_unlocked"):
            if key in (payload or {}) and payload[key] is not None:
                changes[key] = payload[key]
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
        # The dashboard edits seconds; the log says the same thing in seconds.
        self.logs.append(
            "info",
            "General settings saved. Reply delay={}s Poll={}s Skip if no reply={} First={}".format(
                seconds_label(settings.reply_delay_ms / 1000.0),
                seconds_label(settings.poll_interval_ms / 1000.0),
                seconds_label(settings.skip_if_no_reply_ms / 1000.0) if settings.skip_if_no_reply_ms else "off",
                settings.effective_first_message_mode(),
            ),
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
        """Ported from ``SaveProxySettings``: same validation and the same log lines."""
        current = asdict(self.store.load_proxy_settings())
        current.update({key: value for key, value in (payload or {}).items() if key in current})
        settings = coerce_settings(ProxySettings, current)
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
        self.logs.append("info", "Message settings saved. Mode={}".format(settings.effective_chat_mode()))
        return asdict(settings)

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
                worker = threading.Thread(
                    target=self._run_one,
                    args=(
                        profile_dir,
                        profile,
                        browser,
                        index,
                        count,
                        rotator.next() if rotator is not None else None,
                    ),
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

    def _run_one(self, profile_dir: Path, profile: str, browser, index: int, total: int, proxy_entry=None) -> None:
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
                proxy=proxy_entry,
            )
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
            self.report_crash(prefix, error)
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
            no_reply_seconds=max(0.0, general.skip_if_no_reply_ms / 1000.0),
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
