"""Where the app keeps its files - as a script, and as a frozen .exe.

Two very different situations, one rule each:

* run from source (``python app.py``) - the project folder is writable, so
  ``data/`` sits next to ``app.py`` exactly like before;
* frozen (``ChitChatBot.exe``, PyInstaller) - the code lives in a temporary
  unpack folder that is deleted when the app closes, so

  - **read-only resources** (``chitchatport/ui``, ``chitchatport/assets``) are
    read from ``sys._MEIPASS``, the folder PyInstaller unpacked into;
  - **the data folder** is chosen next to the ``.exe`` (same idea as the Go
    build, which used its working directory), and only when that is not
    writable - a Program Files install, a read-only stick - it falls back to
    ``%LOCALAPPDATA%\\ChitChatBot\\data`` (``~/.chitchatbot/data`` elsewhere).

Nothing here imports a GUI library, so every function is unit-tested without a
display, and the same code path runs on Windows, macOS and Linux.
"""

from __future__ import annotations

import os
import sys
from dataclasses import dataclass
from pathlib import Path

APP_NAME = "ChitChatBot"
DATA_FOLDER_NAME = "data"

# PyInstaller >= 6 uses sys._MEIPASS (5.x used it too, but the attribute could
# be missing on some builds); oneplace builds put the folder next to the exe.
MEIPASS_ATTR = "_MEIPASS"

# Bundled WebView2 runtime, exactly the folder names the Go build looked for
# (internal/runtimeassets.BundledWebView2Path): a fixed-version runtime that can
# be shipped next to the exe when the machine has no WebView2 at all.
WEBVIEW2_FOLDER_NAMES = (
    "WebView2",
    "webview2",
    "Microsoft.WebView2.FixedVersionRuntime",
)
WEBVIEW2_ENV_VAR = "WEBVIEW2_BROWSER_EXECUTABLE_FOLDER"


def is_frozen() -> bool:
    """True inside a PyInstaller (or similar) bundle."""
    return bool(getattr(sys, "frozen", False)) or hasattr(sys, MEIPASS_ATTR)


def bundle_dir() -> Path:
    """The folder holding the bundled read-only resources.

    Frozen: ``sys._MEIPASS``. From source: the package's parent, i.e. the
    project folder that contains ``app.py``.
    """
    meipass = getattr(sys, MEIPASS_ATTR, None)
    if meipass:
        return Path(meipass).resolve()
    if getattr(sys, "frozen", False):
        return Path(sys.executable).resolve().parent
    return Path(__file__).resolve().parent.parent


def resource_dir() -> Path:
    """The folder that holds ``ui/`` and ``chitchatport/assets/``."""
    return bundle_dir()


def resource_path(*parts: str) -> Path:
    return resource_dir().joinpath(*parts)


def executable_dir() -> Path:
    """The folder of the running .exe (or the project folder as a script)."""
    if getattr(sys, "frozen", False):
        return Path(sys.executable).resolve().parent
    return Path(__file__).resolve().parent.parent


def _fallback_data_dir() -> Path:
    """A per-user folder that is writable on every OS (Windows: %LOCALAPPDATA%)."""
    local = os.environ.get("LOCALAPPDATA") or os.environ.get("XDG_DATA_HOME")
    if local:
        base = Path(local)
    else:
        base = Path.home() / (Path("AppData") / "Local" if os.name == "nt" else Path(".local") / "share")
    return base / APP_NAME / DATA_FOLDER_NAME


def is_writable(path: Path) -> bool:
    """Can a folder be created (and written to) here? Never raises."""
    probe = Path(path) / ".write-probe"
    try:
        Path(path).mkdir(parents=True, exist_ok=True)
        probe.write_text("ok", encoding="utf-8")
        probe.unlink()
        return True
    except OSError:
        return False


@dataclass
class DataDirChoice:
    """The chosen data folder plus why, so --selftest can explain it."""

    path: Path
    source: str  # "explicit" | "next-to-exe" | "project" | "localappdata"

    def describe(self) -> str:
        return "{} ({})".format(self.path, self.source)


def choose_data_dir(explicit: str | os.PathLike[str] | None = None) -> DataDirChoice:
    """Pick the data folder: explicit flag > next to the exe > user profile.

    An explicitly requested folder is used as-is (the operator asked for it);
    everything else is only accepted when a write probe really succeeds.
    """
    if explicit:
        return DataDirChoice(Path(explicit).expanduser().resolve(), "explicit")

    preferred = executable_dir() / DATA_FOLDER_NAME
    if is_writable(preferred):
        return DataDirChoice(preferred.resolve(), "next-to-exe" if is_frozen() else "project")

    fallback = _fallback_data_dir()
    is_writable(fallback)  # create it now so the first log line can be written
    return DataDirChoice(fallback.resolve(), "localappdata")


def bundled_webview2_dir() -> Path | None:
    """A fixed-version WebView2 runtime shipped next to the exe, if there is one."""
    base = executable_dir()
    for name in WEBVIEW2_FOLDER_NAMES:
        candidate = base / name
        # A fixed-version runtime always carries msedgewebview2.exe.
        if (candidate / "msedgewebview2.exe").is_file():
            return candidate
        for child in sorted(candidate.glob("Microsoft.WebView2.FixedVersionRuntime*")):
            if (child / "msedgewebview2.exe").is_file():
                return child
    return None


def use_bundled_webview2() -> str:
    """Point the WebView2 loader at a bundled runtime, like the Go build did.

    pywebview talks to the same WebView2 loader the Go/Wails build did, and that
    loader honours ``WEBVIEW2_BROWSER_EXECUTABLE_FOLDER``. Returns the folder
    that was set ("" when the machine's own WebView2 is used).
    """
    bundled = bundled_webview2_dir()
    if bundled is None:
        return ""
    os.environ[WEBVIEW2_ENV_VAR] = str(bundled)
    return str(bundled)


def go_reference_dir() -> Path:
    """Where the identity checks read the Go originals from.

    From source: ``root-project`` next to the port - the whole Go reference
    tree. Frozen: the ``go-reference`` subset the build copied into the bundle
    *from that very tree*, so the .exe can still prove its DOM asset and
    stylesheet are the Go bytes instead of reporting BLOCKED.
    """
    if not getattr(sys, "frozen", False):
        repo = Path(__file__).resolve().parents[2] / "root-project"
        if repo.is_dir():
            return repo
        return repo  # keep the source behaviour: the caller reports BLOCKED
    return resource_path("go-reference")


# ---------------------------------------------------------------- one instance
def instance_lock_path(data_dir: Path) -> Path:
    return Path(data_dir) / ".app.lock"


def _process_alive(pid: int) -> bool:
    """Best-effort liveness check that never raises and never a false "alive"."""
    if pid <= 0:
        return False
    if pid == os.getpid():
        return True
    if os.name == "nt":
        try:  # pragma: no cover - Windows-only, exercised on the operator's PC
            import ctypes

            handle = ctypes.windll.kernel32.OpenProcess(0x1000, False, pid)  # PROCESS_QUERY_LIMITED
            if not handle:
                return False
            code = ctypes.c_ulong()
            alive = bool(ctypes.windll.kernel32.GetExitCodeProcess(handle, ctypes.byref(code)))
            ctypes.windll.kernel32.CloseHandle(handle)
            return alive and code.value == 259  # STILL_ACTIVE
        except Exception:  # noqa: BLE001 - if we cannot tell, treat as not running
            return False
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return False


def acquire_instance_lock(data_dir: Path) -> tuple[bool, str]:
    """Take the single-instance lock. Returns (taken, holder_description).

    A stale lock (the process that wrote it is gone) is taken over silently,
    because a crashed run must never block the next start. Two live instances
    would fight over the same data folder and the same log file, so the second
    one is refused with the holder's pid - the operator sees why.
    """
    path = instance_lock_path(data_dir)
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        if path.exists():
            raw = path.read_text(encoding="utf-8", errors="replace").strip()
            holder = 0
            for line in raw.splitlines():
                if line.startswith("pid="):
                    try:
                        holder = int(line.split("=", 1)[1])
                    except ValueError:
                        holder = 0
            if _process_alive(holder):
                return False, "another instance is already running (pid {}, lock {})".format(
                    holder, path
                )
        path.write_text("pid={}\nstarted={}\n".format(os.getpid(), _now()), encoding="utf-8")
        return True, ""
    except OSError as error:
        # A data folder we cannot lock must not stop the app: log and continue.
        return True, "could not write the lock ({}): continuing without it".format(error)


def release_instance_lock(data_dir: Path) -> None:
    path = instance_lock_path(data_dir)
    try:
        if path.exists() and "pid={}".format(os.getpid()) in path.read_text(
            encoding="utf-8", errors="replace"
        ):
            path.unlink()
    except OSError:
        pass


def _now() -> str:
    import time

    return time.strftime("%Y-%m-%dT%H:%M:%S", time.localtime())
