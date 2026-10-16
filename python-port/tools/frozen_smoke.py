#!/usr/bin/env python3
"""Drive the real app the way the packaged .exe runs it - without PyInstaller.

Why this exists: a Windows ``.exe`` can only be *built* on Windows (PyInstaller
cannot cross-compile), so a Linux/macOS machine - this sandbox, a CI runner -
can never run ``build-exe.bat``. The part of the app that a bundle changes is
not PyInstaller-specific, though. It is decided by three things only:

    sys.frozen       -> resource files come from the bundle, data/ sits next to
                        the "exe", the window opens by default
    sys._MEIPASS     -> where the bundle was unpacked (ui/, assets/, go-reference/)
    sys.executable   -> the folder the data/ folder is created in

This script recreates exactly that layout in a temporary folder:

    <tmp>/ChitChatBot/ChitChatBot          the "exe" (a small launcher)
    <tmp>/ChitChatBot/_internal/           the unpacked bundle (sys._MEIPASS)
        app.py, chitchatport/, go-reference/, README.md
    <tmp>/ChitChatBot/data/                created on the first run

and then really runs it:

  1. ``--selftest`` inside the bundle: UI assets, DOM asset, data folder,
     server and settings are all checked from the bundled copies. The playwright
     row is printed but not counted: this simulation copies the app, not the
     installed packages (the real .exe bundles playwright and build-exe.bat
     runs this very selftest on the packed app);
  2. ``--diag`` inside the bundle: the CHECK SYSTEM preflight, including the
     DOM-asset identity check against the bundled Go reference;
  3. a live fetch: the app starts its server on a real port and this script
     downloads ``/`` from it - proving index.html and its per-run token came out
     of the *bundled* ui folder, not from the source tree;
  4. the data folder: it must be ``<bundle>/data``, a settings save sent to the
     frozen server must land in ``<bundle>/data/state/general.json`` with the
     seven Python settings (and none of the removed Go ones), and the log file
     must appear there too - never in a temp path that disappears;
  5. the single-instance lock inside the bundle: a second start is refused while
     the first one runs, and a stale lock (a killed process) is taken over.

Every line is PASS/FAIL and the exit code is non-zero on any FAIL, so
``tools/verify.py`` can gate on it.
"""

from __future__ import annotations

import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]          # python-port/
GO_TREE = ROOT.parent / "root-project"

results: list[tuple[str, str, str]] = []


def record(name: str, ok: bool, detail: str = "") -> None:
    results.append((name, "PASS" if ok else "FAIL", detail))
    print("[{}] {}{}".format("PASS" if ok else "FAIL", name, " — " + detail if detail else ""))


LAUNCHER = '''"""The simulated .exe: pretend to be PyInstaller's bootloader."""
import runpy
import sys
from pathlib import Path

HERE = Path(__file__).resolve()
INTERNAL = HERE.parent / "_internal"

sys.frozen = True
sys._MEIPASS = str(INTERNAL)
sys.executable = str(HERE)          # what Bootstrap sets before running the app
sys.path.insert(0, str(INTERNAL))

runpy.run_path(str(INTERNAL / "app.py"), run_name="__main__")
'''


def build_bundle(base: Path) -> Path:
    """Lay out a PyInstaller-shaped onefolder bundle and return the launcher."""
    app_dir = base / "ChitChatBot"
    internal = app_dir / "_internal"
    internal.mkdir(parents=True)

    shutil.copytree(ROOT / "chitchatport", internal / "chitchatport")
    shutil.copy2(ROOT / "app.py", internal / "app.py")
    shutil.copy2(ROOT / "README.md", internal / "README.md")

    reference = internal / "go-reference"
    (reference / "internal" / "browser").mkdir(parents=True)
    (reference / "reference-ui").mkdir(parents=True)
    shutil.copy2(
        GO_TREE / "internal" / "browser" / "showmode_dom.js",
        reference / "internal" / "browser" / "showmode_dom.js",
    )
    shutil.copy2(GO_TREE / "reference-ui" / "styles.css", reference / "reference-ui" / "styles.css")
    dist_styles = GO_TREE / "cmd" / "chitchatui" / "frontend" / "dist" / "styles.css"
    if dist_styles.is_file():
        (reference / "cmd" / "chitchatui" / "frontend" / "dist").mkdir(parents=True)
        shutil.copy2(dist_styles, reference / "cmd" / "chitchatui" / "frontend" / "dist" / "styles.css")

    launcher = app_dir / "ChitChatBot"
    launcher.write_text(LAUNCHER, encoding="utf-8")
    return launcher


def free_port() -> int:
    with socket.socket() as probe:
        probe.bind(("127.0.0.1", 0))
        return probe.getsockname()[1]


def run(launcher: Path, *args: str, wait: float = 60) -> tuple[int, str]:
    proc = subprocess.run(
        [sys.executable, str(launcher), *args],
        capture_output=True,
        text=True,
        timeout=wait,
        env=dict(os.environ),
    )
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def main() -> int:
    if not (GO_TREE / "internal" / "browser" / "showmode_dom.js").is_file():
        record("go-reference-available", False, "no Go tree at {}".format(GO_TREE))
        return 1

    with tempfile.TemporaryDirectory(prefix="chitchat-frozen-") as temp:
        base = Path(temp) / "dist"
        base.mkdir()
        launcher = build_bundle(base)

        # 1. --selftest ------------------------------------------------------
        code, output = run(launcher, "--selftest")
        rows = [line for line in output.splitlines() if line.strip().startswith("[")]
        # The playwright row is an environment fact, not a bundle-code fact: this
        # simulation copies the app, not the installed packages, so a machine
        # without playwright reports FAIL there. The real .exe bundles it and
        # build-exe.bat runs the same --selftest on the packed app, so the row is
        # printed but not counted here (it is counted there).
        counted = [line for line in rows if "playwright" not in line]
        fails = [line for line in counted if "[   FAIL]" in line]
        frozen_line = [line for line in output.splitlines() if "frozen" in line]
        record(
            "selftest-inside-the-bundle",
            not fails,
            "{} row(s), {} FAIL{} · {}".format(
                len(counted),
                len(fails),
                "" if not fails else " (" + fails[0].strip()[:60] + ")",
                (frozen_line or [""])[0].strip(),
            ),
        )
        data_line = [line for line in output.splitlines() if "data folder" in line]
        record(
            "selftest-reads-the-bundle",
            "yes (.exe)" in output and "(next-to-exe)" in (data_line or [""])[0],
            "frozen=yes and data sits next to the exe · {}".format((data_line or ["?"])[0].strip()),
        )
        data_dir = base / "ChitChatBot" / "data"
        record(
            "data-folder-next-to-the-exe",
            data_dir.is_dir(),
            str(data_dir.relative_to(base)) + " created by the frozen app",
        )

        # 2. --diag ----------------------------------------------------------
        code, output = run(launcher, "--diag")
        dom_line = [line for line in output.splitlines() if "DOM observer" in line]
        record(
            "diag-checks-the-bundled-go-reference",
            bool(dom_line) and "[   PASS]" in dom_line[0],
            dom_line[0].strip() if dom_line else "no DOM-asset line in --diag",
        )

        # 3. a live fetch out of the bundled UI ------------------------------
        port = free_port()
        proc = subprocess.Popen(
            [sys.executable, str(launcher), "--no-open", "--port", str(port)],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        body, served = "", False
        try:
            deadline = time.time() + 45
            while time.time() < deadline:
                try:
                    with urllib.request.urlopen("http://127.0.0.1:{}/".format(port), timeout=5) as answer:
                        body = answer.read().decode("utf-8", "replace")
                        served = True
                        break
                except (urllib.error.URLError, ConnectionError):
                    if proc.poll() is not None:
                        break
                    time.sleep(0.4)
            record(
                "bundled-ui-is-served",
                served and "ChitChat Bot" in body and 'name="api-token"' in body and "__API_TOKEN__" not in body,
                "{} bytes from the bundle's chitchatport/ui".format(len(body)) if served else "no answer on port {}".format(port),
            )

            # 3b. writing settings through the frozen server -----------------
            token = ""
            marker = 'name="api-token" content="'
            if marker in body:
                token = body.split(marker, 1)[1].split('"', 1)[0]
            payload = json.dumps({"reply_delay_min_seconds": 7, "reply_delay_max_seconds": 21}).encode()
            request = urllib.request.Request(
                "http://127.0.0.1:{}/api/settings/general".format(port),
                data=payload,
                headers={"Content-Type": "application/json", "X-API-Token": token},
                method="POST",
            )
            saved: dict = {}
            try:
                with urllib.request.urlopen(request, timeout=15) as answer:
                    saved = json.loads(answer.read().decode("utf-8"))
            except Exception as error:  # noqa: BLE001 - reported as a check
                record("frozen-settings-write", False, "{}: {}".format(type(error).__name__, error))
            else:
                on_disk = data_dir / "state" / "general.json"
                stored = json.loads(on_disk.read_text(encoding="utf-8")) if on_disk.is_file() else {}
                record(
                    "frozen-settings-write",
                    stored.get("reply_delay_max_seconds") == 21 and stored.get("reply_delay_min_seconds") == 7,
                    "POST /api/settings/general -> {}".format(on_disk.relative_to(base)),
                )
                good_keys = {
                    "first_message_answer_all",
                    "first_message_mode",
                    "poll_interval_ms",
                    "reply_delay_max_seconds",
                    "reply_delay_min_seconds",
                    "skip_if_no_reply_max_ms",
                    "skip_if_no_reply_ms",
                }
                record(
                    "no-go-settings-in-the-bundle",
                    set(stored) == good_keys,
                    "general.json keys: {}".format(", ".join(sorted(stored)) or "none"),
                )
                record(
                    "log-file-next-to-the-exe",
                    (data_dir / "logs" / "port.log").is_file(),
                    "data/logs/port.log written by the frozen app",
                )

            # 4. the single-instance lock, inside the bundle -----------------
            code2, output2 = run(launcher, "--no-open", "--port", str(free_port()), wait=30)
            record(
                "second-instance-is-refused",
                code2 == 2 and "already running" in output2,
                "exit {} · {}".format(code2, (output2.strip().splitlines() or [""])[0][:70]),
            )
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=15)
            except subprocess.TimeoutExpired:  # pragma: no cover - the OS is slow
                proc.kill()

        # 5. the killed process left a stale lock: the next start takes over --
        port2 = free_port()
        again = subprocess.Popen(
            [sys.executable, str(launcher), "--no-open", "--port", str(port2)],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
        )
        took_over = False
        try:
            deadline = time.time() + 45
            while time.time() < deadline:
                try:
                    with urllib.request.urlopen("http://127.0.0.1:{}/".format(port2), timeout=5) as answer:
                        took_over = answer.status == 200
                        break
                except (urllib.error.URLError, ConnectionError):
                    if again.poll() is not None:
                        break
                    time.sleep(0.4)
        finally:
            again.terminate()
            try:
                again.wait(timeout=15)
            except subprocess.TimeoutExpired:  # pragma: no cover
                again.kill()
        record(
            "stale-lock-is-taken-over",
            took_over,
            "a crashed run never blocks the next start",
        )

    failed = [name for name, status, _ in results if status == "FAIL"]
    print()
    print(
        "frozen-layout simulation: {} PASS · {} FAIL".format(
            sum(1 for _, status, _ in results if status == "PASS"), len(failed)
        )
    )
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
