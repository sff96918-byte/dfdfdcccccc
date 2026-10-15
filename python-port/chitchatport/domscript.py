"""Wrappers around the shared in-page DOM helper.

``assets/showmode_dom.js`` is a verbatim copy of the Go asset; the expressions
below mirror the ones ``internal/browser/domchat.go`` evaluates, including the
``const __sm`` binding the script declares inside the evaluated function.
"""

from __future__ import annotations

from functools import lru_cache
from pathlib import Path

ASSET_PATH = Path(__file__).resolve().parent / "assets" / "showmode_dom.js"


@lru_cache(maxsize=1)
def dom_script() -> str:
    return ASSET_PATH.read_text(encoding="utf-8")


def snapshot_expression() -> str:
    return "() => { " + dom_script() + " return __sm.snapshot(); }"


def probe_expression() -> str:
    return "() => { " + dom_script() + " return __sm.probe(); }"


def click_expression() -> str:
    return "(labels) => { " + dom_script() + " return __sm.click(labels); }"
