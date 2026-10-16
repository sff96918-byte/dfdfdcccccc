"""Entry point for the ChitChat Python Show Mode dashboard.

Two ways to run the very same dashboard:

* **the .exe** (``ChitChatBot.exe``, built with tools/build-exe.bat) shows it in
  its own window - pywebview with the system WebView2, which is the same engine
  the Go/Wails build used - and keeps its ``data/`` folder next to the exe;
* **from source** (``python app.py``) serves it on a free port and opens your
  browser, which is the mode that works on every machine with no WebView2.

Flags:

    --window         force the native window (the .exe default)
    --browser        force the browser (the source default)
    --no-open        only print the URL
    --port 8300      fixed port instead of a free one
    --data DIR       data folder (default: next to the exe / project folder)
    --selftest       check the bundled app (assets, data folder, server, window
                     support) and exit - safe to run anywhere
    --diag           print the environment diagnostics and exit
    --debug          echo every log line into this console too
    --allow-multiple run even when another instance holds the data folder lock
"""

from __future__ import annotations

import argparse
import os
import platform
import sys
import time
import webbrowser
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
if str(BASE_DIR) not in sys.path:
    sys.path.insert(0, str(BASE_DIR))

from chitchatport import __version__  # noqa: E402
from chitchatport import runtime  # noqa: E402
from chitchatport.server import DashboardServer  # noqa: E402
from chitchatport.service import ShowModeService  # noqa: E402

WINDOW_TITLE = "ChitChat Bot"
WINDOW_WIDTH, WINDOW_HEIGHT = 1280, 820
WINDOW_MIN_WIDTH, WINDOW_MIN_HEIGHT = 1080, 700
WINDOW_BACKGROUND = "#161716"  # the same dark background the Go window used

BANNER = r"""
   ______  __    _  ______ __              __   ____        __
  / ____/ / /_  (_)/ ____// /_  ____  ____/ /  / __ )____  / /_
 / /     / __ \/ // /    / __ \/ __ \/ __  /  / __  / __ \/ __/
/ /___  / / / / // /___ / / / / /_/ / /_/ /  / /_/ / /_/ / /_
\____/ /_/ /_//_/\____//_/ /_/\____/\__,_/  /_____/\____/\__/
                       P Y T H O N   ·   S H O W   M O D E
"""


def _ensure_stdio() -> None:
    """A windowed .exe has no console: stdout/stderr are None, print() would crash."""
    for name in ("stdout", "stderr"):
        if getattr(sys, name, None) is None:
            try:
                setattr(sys, name, open(os.devnull, "w", encoding="utf-8"))
            except OSError:  # pragma: no cover - nothing left to do then
                pass


def parse_args(argv=None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="ChitChat Bot - Python Show Mode dashboard",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--window", action="store_true", help="show the dashboard in a native window")
    parser.add_argument("--browser", action="store_true", help="show the dashboard in your browser")
    parser.add_argument("--no-open", action="store_true", help="do not open a browser automatically")
    parser.add_argument("--port", type=int, default=0, help="fixed dashboard port (default: a free one)")
    parser.add_argument(
        "--host",
        default="127.0.0.1",
        help="interface to bind (default: 127.0.0.1 - local only; use 0.0.0.0 only in a container)",
    )
    parser.add_argument("--data", default="", help="data folder (default: next to the app)")
    parser.add_argument("--diag", action="store_true", help="print environment diagnostics and exit")
    parser.add_argument("--selftest", action="store_true", help="check the bundled app and exit")
    parser.add_argument("--allow-multiple", action="store_true", help="ignore an existing instance lock")
    parser.add_argument(
        "--debug",
        action="store_true",
        help="also print every log line in this console window (same lines as the Logs tab)",
    )
    return parser.parse_args(argv)


def wants_window(args: argparse.Namespace) -> bool:
    """The .exe opens its window; running from source opens the browser.

    ``--browser`` always wins, so there is an escape hatch when a machine has no
    working WebView2 - the fallback path stays reachable by hand.
    """
    if args.browser:
        return False
    if args.window:
        return True
    if args.no_open:
        return False
    return runtime.is_frozen()


# ------------------------------------------------------------------ selftest
def selftest(data_choice: runtime.DataDirChoice | None = None) -> int:
    """Prove the parts of a packaged app that can break, without starting a run.

    Every line is PASS / FAIL / BLOCKED, the same vocabulary the dashboard's
    CHECK SYSTEM uses. This is what to run when the .exe "does nothing".

    ``data_choice`` is the folder the app already picked (main() computes it
    once) so this report names the very folder a real run would use, and says
    *why* it was picked instead of claiming it was requested by hand.
    """
    rows: list[tuple[str, str, str]] = []

    def record(name: str, status: str, detail: str = "") -> None:
        rows.append((name, status, detail))

    frozen = runtime.is_frozen()
    record(
        "app",
        "PASS",
        "{} · python {} · {}".format(
            __version__, platform.python_version(), "frozen .exe" if frozen else "running from source"
        ),
    )
    record("folder", "PASS", "bundle {} · executable {}".format(runtime.bundle_dir(), runtime.executable_dir()))

    ui = runtime.resource_path("chitchatport", "ui")
    wanted = ("index.html", "app.js", "flow.js", "theme.css", "styles.css")
    missing = [name for name in wanted if not (ui / name).is_file()]
    record(
        "ui-assets",
        "FAIL" if missing else "PASS",
        "missing {}".format(", ".join(missing)) if missing else "{} files in {}".format(len(wanted), ui),
    )
    asset = runtime.resource_path("chitchatport", "assets", "showmode_dom.js")
    record("dom-asset", "PASS" if asset.is_file() else "FAIL", str(asset) if asset.is_file() else "missing " + str(asset))

    choice = data_choice if data_choice is not None else runtime.choose_data_dir()
    record("data-dir", "PASS" if runtime.is_writable(choice.path) else "FAIL", choice.describe())

    # The server must be able to serve the real UI on a free port.
    try:
        service = ShowModeService(choice.path)
        server = DashboardServer(service, host="127.0.0.1")
        port = server.start(0)
        import urllib.request

        body = urllib.request.urlopen("http://127.0.0.1:{}/".format(port), timeout=15).read().decode("utf-8", "replace")
        html_ok = "ChitChat Bot" in body and "api-token" in body
        record(
            "server",
            "PASS" if html_ok else "FAIL",
            "http://127.0.0.1:{} served {} bytes{}".format(
                port, len(body), "" if html_ok else " (no token/title - wrong UI?)"
            ),
        )
        settings = service.store.load_general_settings()
        record(
            "settings",
            "PASS",
            "mode-independent: reply delay={}-{}s skip if no reply={} ms scan={} ms".format(
                settings.reply_delay_min_seconds,
                settings.reply_delay_max_seconds,
                settings.skip_if_no_reply_ms or 0,
                settings.poll_interval_ms,
            ),
        )
        service.shutdown()
        server.stop()
    except Exception as error:  # noqa: BLE001 - a selftest reports, never crashes
        record("server", "FAIL", "{}: {}".format(type(error).__name__, error))

    try:
        import playwright  # noqa: F401

        record("playwright", "PASS", "importable (Show Mode drives system Chrome/Edge)")
    except Exception as error:  # noqa: BLE001
        record("playwright", "FAIL", "not bundled ({})".format(error))

    bundled_runtime = runtime.bundled_webview2_dir()
    try:
        import webview  # noqa: F401

        version = getattr(webview, "__version__", "?")
        record(
            "window",
            "PASS",
            "pywebview {} available{}".format(
                version,
                " · bundled WebView2 {}".format(bundled_runtime) if bundled_runtime else " · system WebView2",
            ),
        )
    except Exception as error:  # noqa: BLE001 - the browser mode still works
        record(
            "window",
            "BLOCKED",
            "pywebview not available ({}); the dashboard still opens in your browser".format(error),
        )

    print("ChitChat Bot - Show Mode :: self test")
    print("  frozen      : {}".format("yes (.exe)" if frozen else "no (source)"))
    print("  data folder : {} ({})".format(choice.path, choice.source))
    print()
    for name, status, detail in rows:
        print("  [{:>7}] {:<14} {}".format(status, name, detail))
    failed = [name for name, status, _ in rows if status == "FAIL"]
    print()
    print("  {} PASS · {} FAIL · {} BLOCKED".format(
        sum(1 for _, status, _ in rows if status == "PASS"),
        len(failed),
        sum(1 for _, status, _ in rows if status == "BLOCKED"),
    ))
    return 1 if failed else 0


def diagnostics(data_dir: Path) -> int:
    """Environment check; the same function the Overview tab's CHECK button runs."""
    from chitchatport.service import run_diagnostics

    report = run_diagnostics(data_dir, deep=True)
    print("ChitChat Bot - Python Show Mode :: diagnostics")
    print("  frozen      : {}".format("yes (.exe)" if runtime.is_frozen() else "no (source)"))
    for check in report["checks"]:
        print("  [{:>7}] {:<32} {}".format(check["status"], check["label"], check["detail"]))
    summary = report["summary"]
    print(
        "\n  {} PASS · {} FAIL · {} WARN · {} BLOCKED".format(
            summary["PASS"], summary["FAIL"], summary["WARN"], summary["BLOCKED"]
        )
    )
    print("  app {} · project dir {}".format(report["system"]["app"], report["system"]["data_dir"]))
    return 0 if report["ok"] else 1


# -------------------------------------------------------------------- window
def run_window(url: str, service: ShowModeService) -> bool:
    """Try the native window; False means "fall back to the browser"."""
    bundled = runtime.use_bundled_webview2()
    if bundled:
        print("  webview2  : bundled runtime {}".format(bundled))
    try:
        import webview
    except Exception as error:  # noqa: BLE001 - optional dependency
        print("[warn] pywebview is not available ({}); falling back to the browser.".format(error))
        print("[warn] install it with:  pip install pywebview")
        return False
    try:
        # The dashboard has DOWNLOAD / EXPORT buttons: without this WebView2
        # refuses the save silently and the button looks broken.
        try:
            webview.settings["ALLOW_DOWNLOADS"] = True
        except Exception:  # noqa: BLE001 - older pywebview versions lack it
            pass
        webview.create_window(
            WINDOW_TITLE,
            url=url,
            width=WINDOW_WIDTH,
            height=WINDOW_HEIGHT,
            min_size=(WINDOW_MIN_WIDTH, WINDOW_MIN_HEIGHT),
            background_color=WINDOW_BACKGROUND,
        )
        webview.start()
        service.shutdown()
        return True
    except Exception as error:  # noqa: BLE001
        print("[warn] the native window could not start ({}); falling back to the browser.".format(error))
        return False


def main(argv=None) -> int:
    _ensure_stdio()
    args = parse_args(argv)
    data_choice = runtime.choose_data_dir(args.data or None)

    if args.selftest:
        return selftest(data_choice)
    if args.diag:
        return diagnostics(data_choice.path)

    held = ""
    if not args.allow_multiple:
        taken, held = runtime.acquire_instance_lock(data_choice.path)
        if not taken:
            print("[stop] {}".format(held))
            print("       Close the other window first, or start this one with --allow-multiple.")
            return 2

    print(BANNER)
    service = ShowModeService(data_choice.path, echo_logs=args.debug)
    server = DashboardServer(service, host=args.host)
    port = server.start(args.port)
    url = server.url
    print("  mode      : {}".format("native window" if wants_window(args) else "browser"))
    print("  dashboard : {}".format(url))
    print("  data dir  : {}".format(data_choice.describe()))
    print("  log file  : {}".format(service.logs.path))
    if held:
        print("  notice    : {}".format(held))
    if args.debug:
        print("  debug     : on - every log line is echoed below; crash tracebacks go to data/logs/crash-*.txt")
    print()

    code = 0
    try:
        if wants_window(args) and run_window(url, service):
            return 0
        if not args.no_open:
            try:
                webbrowser.open(url)
            except Exception as error:  # noqa: BLE001
                print("[warn] could not open a browser automatically ({}).".format(error))
        print("  The dashboard is running at the URL above.")
        print("  Keep this window open while the bot runs. Press Ctrl+C to stop.")
        print()
        while True:
            time.sleep(0.4)
    except KeyboardInterrupt:
        print("\n  stopping ...")
    finally:
        service.shutdown()
        server.stop()
        if not args.allow_multiple:
            runtime.release_instance_lock(data_choice.path)
        print("  stopped. The browser tab can be closed.")
    return code


if __name__ == "__main__":
    raise SystemExit(main())
