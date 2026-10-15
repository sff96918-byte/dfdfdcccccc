#!/usr/bin/env python3
"""Local verification for the Python port (the small sibling of verify.cjs).

Runs, in order:
  1. byte-compile every Python file (syntax gate),
  2. the unittest suite,
  3. the DOM-asset identity check against the Go tree when it is available.

Exit code is non-zero when any check FAILs; a missing Go tree is reported as
BLOCKED (not PASS), exactly like the Go verification policy.
"""

from __future__ import annotations

import compileall
import hashlib

import subprocess
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


def main() -> int:
    print("Python Show Mode port — local verification")
    check_compile()
    check_import_without_gui()
    check_tests()
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
