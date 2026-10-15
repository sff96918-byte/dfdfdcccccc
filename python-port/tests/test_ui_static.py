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
APP_JS = (UI / "app.js").read_text(encoding="utf-8")
FLOW_JS = (UI / "flow.js").read_text(encoding="utf-8")
JS = APP_JS + "\n" + FLOW_JS
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
        for name in ("index.html", "app.js", "flow.js", "theme.css"):
            self.assertTrue((UI / name).is_file(), name)
        self.assertIn('href="theme.css"', HTML)
        self.assertIn('src="flow.js"', HTML)
        self.assertIn('src="app.js"', HTML)
        self.assertLess(HTML.index("flow.js"), HTML.index("app.js"), "flow.js defines Flow before app.js uses it")
        self.assertNotIn("styles.css", HTML)  # the old copied Go theme is gone

    def test_every_referenced_element_id_exists(self):
        defined = html_ids()
        referenced = js_referenced_ids()
        self.assertTrue(referenced)
        missing = sorted(referenced - defined)
        self.assertEqual(missing, [], "app.js references undefined element ids: %s" % missing)

    def test_flow_api_paths_exist_in_the_server(self):
        for path in ('"/api/flow/prefs"', '"/api/flow/reset"'):
            self.assertIn(path, FLOW_JS, "the UI calls " + path)
            self.assertIn(path.strip('"'), SERVER, "the server has no route for " + path)

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

    def test_guided_flow_markup_and_behaviour(self):
        # blank home until the rail slides in
        self.assertIn('<div id="console" hidden>', HTML)
        self.assertIn('id="flow-home"', HTML)
        self.assertIn('id="rail"', HTML)
        # every step points at a real panel
        panels = set(re.findall(r'id="(panel-[\w-]+)"', HTML))
        steps = re.findall(r'panel: "([\w-]+)"', FLOW_JS)
        self.assertEqual(len(steps), 5)
        self.assertEqual(sorted(set(steps) - panels), [])

    def test_step_order_matches_the_server(self):
        from chitchatport.service import ShowModeService

        js_order = re.findall(r'key: "([a-z]+)"', FLOW_JS)
        self.assertEqual(tuple(js_order), tuple(ShowModeService.FLOW_STAGES))

    def test_message_panel_loads_from_the_browser(self):
        # No path box, no mode dropdown, no sequence box: the folder is picked in
        # the browser and loaded through the API (plus the token helper).
        for gone in ('id="folder-path"', 'id="chat-mode"', 'id="message-sequence"'):
            self.assertNotIn(gone, HTML)
        for present in (
            'id="folder-input"',
            "webkitdirectory",
            'id="folder-browse"',
            'id="message-usernames"',
            'id="token-value"',
            'id="token-copy"',
        ):
            self.assertIn(present, HTML)
        self.assertIn('"/api/folder/files"', APP_JS)
        self.assertIn("webkitRelativePath", APP_JS)
        self.assertNotIn('"/api/folder"', APP_JS, "the UI never posts a hand-typed path any more")
        self.assertIn("/api/folder/files", SERVER, "the panel advertises an API the server must answer")

    def test_auto_slide_is_pausable_and_never_interrupts_typing(self):
        # WCAG 2.2.2: moving/auto-advancing content needs a pause/stop control.
        self.assertIn('id="flow-hold"', HTML)
        self.assertIn("aria-pressed", HTML)
        self.assertIn("flow-hold", FLOW_JS)
        self.assertIn("prefers-reduced-motion", FLOW_JS)
        self.assertIn("prefers-reduced-motion", CSS)
        self.assertIn("TEXTAREA", FLOW_JS)
        self.assertIn(":hover", FLOW_JS)
        self.assertIn("state.hold", FLOW_JS)
        # and a keyboard way out of the flow
        self.assertIn('"Escape"', FLOW_JS)
        self.assertIn("SKIP", HTML)

    def test_the_rail_can_move_to_the_other_side(self):
        self.assertIn('data-side="right"', HTML)
        self.assertIn('id="flow-flip"', HTML)
        self.assertIn('data-rail="left"', CSS)
        self.assertIn("rail_side", FLOW_JS)
        self.assertIn('"/api/flow/prefs"', FLOW_JS)

    def test_theme_is_not_the_old_go_stylesheet(self):
        self.assertNotIn(".browser-choice", CSS)
        self.assertIn("matrix", (UI / "app.js").read_text(encoding="utf-8").lower() + CSS.lower())


if __name__ == "__main__":
    unittest.main()
