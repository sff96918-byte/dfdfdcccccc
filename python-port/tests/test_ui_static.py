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
SERVICE = (PACKAGE / "service.py").read_text(encoding="utf-8")


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
            self.assertIn(path, JS, "the UI calls " + path)
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

    def test_everything_read_off_the_window_is_published_on_it(self):
        # A top-level `const Flow = ...` lives in the global lexical scope, NOT on
        # the window object, so `if (window.Flow)` stays false in a real browser:
        # flow.js never started and the blank home hid the whole dashboard. Any
        # window.<Name> the UI reads must be assigned by its script.
        for name in sorted(set(re.findall(r"window\.([A-Z][A-Za-z0-9_]*)", JS))):
            self.assertIn(
                "window.{} =".format(name),
                JS,
                "window.{} is read but never assigned - publish it from its script".format(name),
            )

    def test_a_missing_flow_script_can_never_leave_a_blank_page(self):
        # Fail-open: if the flow cannot start, app.js must show the console.
        self.assertIn("function startFlow()", APP_JS)
        self.assertIn("function showConsole(", APP_JS)
        self.assertIn("stickyBanner", APP_JS)
        self.assertIn('$("flow").hidden = true', APP_JS)
        self.assertIn("#flow[hidden]", CSS)

    def test_the_unlocked_dashboard_is_not_cluttered(self):
        # The operator asked for a calm console: no post-unlock flow HUD, no
        # keyboard crib in the status bar, compact labels, no "> " prefixes.
        self.assertNotIn("Flow finished", FLOW_JS)
        self.assertNotIn("flow-reset", HTML)
        self.assertNotIn("keys: 1-6 tabs", HTML)
        self.assertNotIn("&gt; ", HTML)
        self.assertIn('id="flow-restart"', HTML)  # one small way back into the flow
        self.assertIn("body[data-flow=\"console\"] #flow { display: none; }", CSS)
        self.assertIn('$("flow").hidden = true', FLOW_JS)

    def test_the_username_requirement_is_shown_before_the_run(self):
        self.assertIn('id="username-warning"', HTML)
        self.assertIn('id="usernames-field"', HTML)
        self.assertIn(".field.needs", CSS)
        self.assertIn('"usernames-field"', APP_JS)
        self.assertIn("needs_usernames", APP_JS)
        self.assertIn("needs_usernames", SERVICE)
        self.assertIn("sequence_wants_a_username", SERVICE)

    def test_a_missing_username_is_alerted_after_the_flow_is_gone(self):
        # The rail hint disappears with the flow layer, so the unlocked console
        # needs its own alert: one short topbar chip plus a refused RUN.
        self.assertIn('id="username-alert"', HTML)
        self.assertGreater(HTML.index('id="username-alert"'), HTML.index('id="console"'))
        label = re.search(r'id="username-alert"[^>]*>([^<]+)<', HTML).group(1).strip()
        self.assertLessEqual(len(label), 20, label)  # short, not a sentence
        self.assertNotIn(".", label)
        self.assertIn(".chip.warn", CSS)
        self.assertIn("state.needsUsernames", APP_JS)
        self.assertIn('$("username-alert").addEventListener("click", revealUsernames)', APP_JS)
        self.assertIn("function revealUsernames()", APP_JS)
        # the guard must run before the start request can be sent
        guard = APP_JS.index("if (state.needsUsernames) {")
        request = APP_JS.index('post("/api/start"')
        self.assertLess(guard, request, "the RUN guard no longer runs before /api/start")

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

    def test_the_timing_fields_are_seconds_and_the_dead_options_are_gone(self):
        # The operator reads seconds; Go's milliseconds stay in the settings file.
        for element_id in ("reply-delay-s", "poll-interval-s", "skip-no-reply-s"):
            self.assertIn('id="{}"'.format(element_id), HTML)
            self.assertIn('"{}"'.format(element_id), APP_JS)
        self.assertIn("msToSeconds", APP_JS)
        self.assertIn("secondsToMs", APP_JS)
        self.assertIn("skip_if_no_reply_ms", APP_JS)
        # Dropped from the dashboard: they drive the Go auto engine, not Show Mode.
        for gone in ("reply-delay-ms", "poll-interval-ms", "poll-min", "poll-max", "action-min", "action-max"):
            self.assertNotIn('id="{}"'.format(gone), HTML)
            self.assertNotIn('"{}"'.format(gone), APP_JS)
        self.assertNotIn("(ms)", HTML)

    def test_the_silence_option_reaches_the_runner(self):
        self.assertIn("skip_if_no_reply_ms", SERVICE)
        self.assertIn("no_reply_seconds", SERVICE)
        self.assertIn("no_reply_seconds", (PACKAGE / "runner.py").read_text(encoding="utf-8"))
        self.assertIn("NO REPLY | partner silent for", (PACKAGE / "runner.py").read_text(encoding="utf-8"))

    def test_theme_is_not_the_old_go_stylesheet(self):
        self.assertNotIn(".browser-choice", CSS)
        self.assertIn("matrix", (UI / "app.js").read_text(encoding="utf-8").lower() + CSS.lower())


if __name__ == "__main__":
    unittest.main()
