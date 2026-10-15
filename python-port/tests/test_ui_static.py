"""Static sanity checks for the dashboard UI (no browser required).

These catch the classic wiring mistakes without running a browser: a script
referencing an element id that index.html does not define, an API path the
server does not implement, or a missing/renamed asset.
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

PACKAGE = Path(__file__).resolve().parents[1] / "chitchatport"
UI = PACKAGE / "ui"
HTML = (UI / "index.html").read_text(encoding="utf-8")
JS = (UI / "app.js").read_text(encoding="utf-8")
CSS = (UI / "theme.css").read_text(encoding="utf-8")
SERVER = (PACKAGE / "server.py").read_text(encoding="utf-8")


def html_ids() -> set[str]:
    return set(re.findall(r'id="([A-Za-z0-9_-]+)"', HTML))


def js_referenced_ids() -> set[str]:
    found = set(re.findall(r'\$\("([A-Za-z0-9_-]+)"\)', JS))
    found |= set(re.findall(r'getElementById\("([A-Za-z0-9_-]+)"\)', JS))
    found |= set(re.findall(r'querySelector\("#([A-Za-z0-9_-]+)"\)', JS))
    return found


class DashboardUiTests(unittest.TestCase):
    def test_assets_exist_and_are_linked(self):
        for name in ("index.html", "app.js", "theme.css"):
            self.assertTrue((UI / name).is_file(), name)
        self.assertIn('href="theme.css"', HTML)
        self.assertIn('src="app.js"', HTML)
        self.assertNotIn("styles.css", HTML)  # the old copied Go theme is gone

    def test_every_referenced_element_id_exists(self):
        defined = html_ids()
        referenced = js_referenced_ids()
        self.assertTrue(referenced)
        missing = sorted(referenced - defined)
        self.assertEqual(missing, [], "app.js references undefined element ids: %s" % missing)

    def test_no_duplicate_element_ids(self):
        ids = re.findall(r'id="([A-Za-z0-9_-]+)"', HTML)
        duplicates = sorted({value for value in ids if ids.count(value) > 1})
        self.assertEqual(duplicates, [], "duplicate ids in index.html: %s" % duplicates)

    def test_api_paths_used_by_the_ui_exist_in_the_server(self):
        used = set(re.findall(r'"(/api/[a-z/]+)"', JS))
        used |= set(re.findall(r"`(/api/[a-z]+)", JS))
        self.assertTrue(used)
        for path in sorted(used):
            self.assertIn('"{}"'.format(path), SERVER, "server has no route for %s" % path)

    def test_token_placeholder_and_theme_tokens(self):
        self.assertIn("__API_TOKEN__", HTML)
        self.assertIn("api-token", HTML)
        for token in ("--accent", "--bg", "--red"):
            self.assertIn(token, CSS, token)

    def test_theme_is_not_the_old_go_stylesheet(self):
        self.assertNotIn(".browser-choice", CSS)
        self.assertIn("matrix", (UI / "app.js").read_text(encoding="utf-8").lower() + CSS.lower())


if __name__ == "__main__":
    unittest.main()
