#!/usr/bin/env python3
"""Local verification for the Python port (the small sibling of verify.cjs).

Runs, in order:
  1. byte-compile every Python file (syntax gate),
  2. the unittest suite,
  3. the guided-flow dashboard served on a throwaway port,
  4. the DOM-asset identity check against the Go tree when it is available.

Exit code is non-zero when any check FAILs; a missing Go tree is reported as
BLOCKED (not PASS), exactly like the Go verification policy.
"""

from __future__ import annotations

import compileall
import hashlib
import json
import subprocess
import tempfile
import urllib.request
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GO_ASSET = ROOT.parent / "root-project" / "internal" / "browser" / "showmode_dom.js"
OUR_ASSET = ROOT / "chitchatport" / "assets" / "showmode_dom.js"
UI_STYLES_GO = ROOT.parent / "root-project" / "cmd" / "chitchatui" / "frontend" / "dist" / "styles.css"
UI_STYLES_OURS = ROOT / "chitchatport" / "ui" / "styles.css"

results: list[tuple[str, str, str]] = []


def record(name: str, status: str, detail: str = "") -> None:
    results.append((name, status, detail))
    print("[{}] {}{}".format(status, name, " — " + detail if detail else ""))


def check_compile() -> None:
    ok = compileall.compile_file(str(ROOT / "app.py"), quiet=1, force=True)
    for folder in ("chitchatport", "tests", "tools"):
        ok = compileall.compile_dir(str(ROOT / folder), quiet=1, force=True) and ok
    record(
        "python-syntax",
        "PASS" if ok else "FAIL",
        "byte-compiled every module" if ok else "run 'python -m compileall -q .' for details",
    )


def check_tests() -> None:
    loader = unittest.TestLoader()
    suite = loader.discover(start_dir=str(ROOT / "tests"), top_level_dir=str(ROOT))
    runner = unittest.TextTestRunner(verbosity=2, stream=sys.stdout)
    result = runner.run(suite)
    detail = "{} test(s), {} failure(s), {} error(s)".format(
        result.testsRun, len(result.failures), len(result.errors)
    )
    record("unittest", "PASS" if result.wasSuccessful() else "FAIL", detail)


def check_asset_identity() -> None:
    if not GO_ASSET.exists():
        record("dom-asset-identity", "BLOCKED", "Go source tree not found: " + str(GO_ASSET))
        return
    ours = hashlib.sha256(OUR_ASSET.read_bytes()).hexdigest()
    theirs = hashlib.sha256(GO_ASSET.read_bytes()).hexdigest()
    status = "PASS" if ours == theirs else "FAIL"
    record("dom-asset-identity", status, "sha256 {}".format(ours[:16]))

    if UI_STYLES_GO.exists():
        ours = hashlib.sha256(UI_STYLES_OURS.read_bytes()).hexdigest()
        theirs = hashlib.sha256(UI_STYLES_GO.read_bytes()).hexdigest()
        record(
            "ui-styles-identity",
            "PASS" if ours == theirs else "FAIL",
            "sha256 {}".format(ours[:16]),
        )


def check_import_without_gui() -> None:
    """The engine must import without Playwright/pywebview installed."""
    code = (
        "import sys; sys.path.insert(0, r'{}');"
        "import chitchatport.service, chitchatport.runner, chitchatport.responders,"
        " chitchatport.settings, chitchatport.domchat, chitchatport.homefeed,"
        " chitchatport.logging_util, chitchatport.domscript, chitchatport.browser;"
        "print('ok')"
    ).format(ROOT)
    proc = subprocess.run([sys.executable, "-c", code], capture_output=True, text=True)
    ok = proc.returncode == 0 and proc.stdout.strip().endswith("ok")
    detail = "engine imports cleanly" if ok else (proc.stderr or proc.stdout).strip().splitlines()[-1:]
    record("engine-imports", "PASS" if ok else "FAIL", str(detail))


def check_dashboard_flow() -> None:
    """Serve the dashboard on a throwaway port and walk the guided flow.

    Proves the flow layer is really shipped: the blank home, the rail markup,
    the step state from /api/state and the unlock after the first chat row.
    """
    sys.path.insert(0, str(ROOT))
    from chitchatport.domchat import DOMControls, DOMMessage, DOMSnapshot
    from chitchatport.server import DashboardServer
    from chitchatport.service import ShowModeService

    with tempfile.TemporaryDirectory() as temp:
        service = ShowModeService(Path(temp))
        server = DashboardServer(service)
        port = server.start(0)
        base = "http://127.0.0.1:{}".format(port)

        def get(path: str) -> str:
            request = urllib.request.Request(base + path)
            request.add_header("X-API-Token", server.token)
            with urllib.request.urlopen(request, timeout=10) as response:
                return response.read().decode("utf-8")

        try:
            index = get("/")
            script = get("/flow.js")
            app = get("/app.js")
            state = json.loads(get("/api/state"))
            flow = state.get("flow") or {}
            problems = []
            if 'id="flow-home"' not in index or 'id="rail"' not in index:
                problems.append("index.html has no flow layer")
            if '<div id="console" hidden>' not in index:
                problems.append("the console is not hidden at start")
            if "const Flow = " not in script or "Flow.init" not in app or "flow.js" not in index:
                problems.append("flow.js is not wired")
            if flow.get("stage") != "message" or flow.get("unlocked"):
                problems.append("a fresh install must start at the message step")

            # the first chat message (here: the bot's own row) unlocks the console
            run = service.feed.start("verify")
            service.feed.observe(
                run,
                DOMSnapshot(
                    controls=DOMControls(
                        conversation="verify",
                        phase="chatting",
                        own_name="self",
                        partner="partner",
                        can_send=True,
                    ),
                    messages=[DOMMessage(id="own", author="SELF", text="hi", order=1)],
                ),
                False,
            )
            after = (json.loads(get("/api/state")).get("flow") or {})
            if not after.get("unlocked") or not after.get("complete", {}).get("live"):
                problems.append("the first chat row did not unlock the console")
            record(
                "dashboard-flow",
                "FAIL" if problems else "PASS",
                "; ".join(problems) if problems else "blank home, rail, stage and unlock all served",
            )
        except Exception as error:  # noqa: BLE001 - reported as a FAIL, never a crash
            record("dashboard-flow", "FAIL", "{}: {}".format(type(error).__name__, error))
        finally:
            server.stop()


def main() -> int:
    print("Python Show Mode port — local verification")
    check_compile()
    check_import_without_gui()
    check_tests()
    check_dashboard_flow()
    check_asset_identity()

    failed = [name for name, status, _ in results if status == "FAIL"]
    blocked = [name for name, status, _ in results if status == "BLOCKED"]
    overall = "FAIL" if failed else ("BLOCKED" if blocked else "PASS")
    print()
    print("Overall: {} (PASS {}, FAIL {}, BLOCKED {})".format(
        overall,
        sum(1 for _, status, _ in results if status == "PASS"),
        len(failed),
        len(blocked),
    ))
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
