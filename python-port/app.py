"""Entry point for the ChitChat Python Show Mode dashboard.

Default mode starts the local dashboard server and opens it in the user's own
browser (no WebView needed - this is the method that works on every Windows
setup). ``--window`` shows the same URL inside a pywebview window instead, and
``--no-open`` just prints the URL.

    python app.py                 # browser dashboard (recommended)
    python app.py --window        # native pywebview window
    python app.py --port 8300     # fixed port instead of a free one
    python app.py --diag          # print environment diagnostics and exit
    python app.py --debug         # echo every log line to this console too
"""

from __future__ import annotations

import argparse
import platform
import sys
import time
import webbrowser
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
if str(BASE_DIR) not in sys.path:
    sys.path.insert(0, str(BASE_DIR))

from chitchatport import __version__  # noqa: E402
from chitchatport.server import DashboardServer  # noqa: E402
from chitchatport.service import ShowModeService  # noqa: E402

BANNER = r"""
   ______  __    _  ______ __              __   ____        __
  / ____/ / /_  (_)/ ____// /_  ____  ____/ /  / __ )____  / /_
 / /     / __ \/ // /    / __ \/ __ \/ __  /  / __  / __ \/ __/
/ /___  / / / / // /___ / / / / /_/ / /_/ /  / /_/ / /_/ / /_
\____/ /_/ /_//_/\____//_/ /_/\____/\__,_/  /_____/\____/\__/
                       P Y T H O N   ·   S H O W   M O D E
"""


def parse_args(argv=None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="ChitChat Bot - Python Show Mode dashboard",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    parser.add_argument("--window", action="store_true", help="show the dashboard in a pywebview window")
    parser.add_argument("--no-open", action="store_true", help="do not open a browser automatically")
    parser.add_argument("--port", type=int, default=0, help="fixed dashboard port (default: a free one)")
    parser.add_argument(
        "--host",
        default="127.0.0.1",
        help="interface to bind (default: 127.0.0.1 - local only; use 0.0.0.0 only in a container)",
    )
    parser.add_argument("--diag", action="store_true", help="print environment diagnostics and exit")
    parser.add_argument(
        "--debug",
        action="store_true",
        help="also print every log line in this console window (same lines as the Logs tab)",
    )
    return parser.parse_args(argv)


def diagnostics() -> int:
    print("ChitChat Bot - Python Show Mode :: diagnostics")
    print("  app version :", __version__)
    print("  python      :", sys.version.split()[0], "|", platform.platform())
    print("  executable  :", sys.executable)
    print("  project dir :", BASE_DIR)
    for module in ("playwright", "webview"):
        try:
            __import__(module)
            print(f"  {module:<11} : available")
        except Exception as error:  # noqa: BLE001 - diagnostics only
            print(f"  {module:<11} : MISSING ({error})")
    try:
        from playwright.sync_api import sync_playwright

        with sync_playwright() as playwright:
            for channel in ("chrome", "msedge"):
                try:
                    browser = playwright.chromium.launch(channel=channel, headless=True)
                    print(f"  browser {channel:<5}: launched OK, closing")
                    browser.close()
                except Exception as error:  # noqa: BLE001
                    print(f"  browser {channel:<5}: NOT available ({str(error).splitlines()[0][:90]})")
    except Exception as error:  # noqa: BLE001
        print("  browser check skipped:", error)
    log_dir = BASE_DIR / "data" / "logs"
    crashes = sorted(log_dir.glob("crash-*.txt")) if log_dir.exists() else []
    print("  log file    :", log_dir / "port.log", "(exists)" if (log_dir / "port.log").exists() else "(not yet)")
    print("  crash files :", len(crashes), "-", crashes[-1].name if crashes else "none")
    return 0


def run_window(url: str, service: ShowModeService) -> bool:
    """Try the native window; False means "fall back to the browser"."""
    try:
        import webview
    except Exception as error:  # noqa: BLE001 - optional dependency
        print("[warn] pywebview is not available ({}); falling back to the browser.".format(error))
        return False
    try:
        webview.create_window("ChitChat Bot // Show Mode", url=url, width=1280, height=840, min_size=(980, 640))
        webview.start()
        service.shutdown()
        return True
    except Exception as error:  # noqa: BLE001
        print("[warn] the native window could not start ({}); falling back to the browser.".format(error))
        return False


def main(argv=None) -> int:
    args = parse_args(argv)
    if args.diag:
        return diagnostics()

    print(BANNER)
    service = ShowModeService(BASE_DIR / "data", echo_logs=args.debug)
    server = DashboardServer(service, host=args.host)
    port = server.start(args.port)
    url = server.url
    print("  dashboard : {}".format(url))
    print("  data dir  : {}".format(service.data_dir))
    print("  log file  : {}".format(service.logs.path))
    if args.debug:
        print("  debug     : on - every log line is echoed below; crash tracebacks go to data/logs/crash-*.txt")
    print()

    try:
        if args.window and run_window(url, service):
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
        print("  stopped. The browser tab can be closed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
