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


def element_html(element_id: str) -> str:
    """Return the markup of one element, nested tags included.

    Small depth scan instead of a parser: the file is hand-written HTML and the
    test needs the real nesting (a folder box inside the row of the dropdown is
    the whole point of the check below).
    """
    marker = 'id="{}"'.format(element_id)
    index = HTML.index(marker)
    start = HTML.rindex("<", 0, index)
    depth = 0
    position = start
    while position < len(HTML):
        if HTML.startswith("<div", position):
            depth += 1
            position += 4
            continue
        if HTML.startswith("</div", position):
            depth -= 1
            position += 5
            if depth == 0:
                return HTML[start:HTML.index(">", position) + 1]
            continue
        position += 1
    raise AssertionError("unbalanced <div> around " + marker)


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

    def test_message_panel_is_a_complete_editor(self):
        # The panel keeps the browser picker (same as the Go app) AND lets the
        # operator work without one: folder path, mode, one sequence by hand,
        # the loaded-file list and a way to clear the folder again.
        for present in (
            'id="folder-input"',
            "webkitdirectory",
            'id="folder-browse"',
            'id="folder-path"',
            'id="folder-path-load"',
            'id="folder-clear"',
            'id="folder-list"',
            'id="chat-mode"',
            'id="message-sequence"',
            'id="message-usernames"',
            'id="token-value"',
            'id="token-copy"',
        ):
            self.assertIn(present, HTML)
        # both modes exist in the dropdown, in the Go app's own words
        for mode in ('value="single"', 'value="folder"'):
            self.assertIn(mode, HTML)
        # all three folder APIs the panel advertises are real server routes
        self.assertIn("webkitRelativePath", APP_JS)
        self.assertIn('"/api/folder/files"', APP_JS)
        self.assertIn('"/api/folder"', APP_JS, "the path box posts the folder to the server")
        self.assertIn('"/api/folder/clear"', APP_JS)
        for route in ("/api/folder/files", "/api/folder/clear"):
            self.assertIn(route, SERVER, "the panel advertises an API the server must answer")
        # the mode/sequence ride the existing message-settings route
        for key in ("chat_mode", "sequence"):
            self.assertIn(key, APP_JS)
            self.assertIn(key, SERVICE)

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

    def test_the_logs_tab_can_export_a_debug_bundle(self):
        self.assertIn('id="log-debug"', HTML)
        self.assertIn('api("/api/debug")', APP_JS)
        self.assertIn("chitchat-debug-", APP_JS)
        self.assertIn('$("log-debug").addEventListener("click"', APP_JS)
        self.assertIn('"/api/debug"', SERVER)

    def test_every_step_is_reachable_at_any_time_and_there_is_a_back_button(self):
        # The wizard used to refuse a click on a step that was not done yet
        # ("finish the current step first"); now every step is a free move and
        # BACK / NEXT walk the order. The auto-slide must never pull the
        # operator away from a step they chose by hand.
        self.assertNotIn("finish the current step first", FLOW_JS)
        self.assertIn('id="flow-back"', HTML)
        self.assertLess(HTML.index('id="flow-back"'), HTML.index('id="flow-next"'))
        self.assertIn('$("flow-back").addEventListener("click"', FLOW_JS)
        self.assertIn("function goTo(key", FLOW_JS)
        self.assertIn("if (state.manual) return;", FLOW_JS)
        self.assertIn("!wasComplete[wasStage]", FLOW_JS)
        self.assertIn('event.key === "b"', FLOW_JS)
        self.assertIn(".rail-nav", CSS)
        self.assertIn(".rail-step:hover", CSS)

    def test_the_browser_step_exists_only_after_start(self):
        # Message -> Proxy -> Setting -> START -> Browser (RUN). The gate is
        # enforced on the server (start_show_mode) and mirrored in the UI.
        self.assertIn('id="flow-start"', HTML)
        self.assertIn('id="top-start"', HTML)
        self.assertIn('id="top-stop"', HTML)
        self.assertIn('"/api/flow/start"', SERVER)
        self.assertIn("def start_gate_error", SERVICE)
        self.assertIn("Finish these first: ", SERVICE)
        self.assertIn("function visibleStages()", FLOW_JS)
        self.assertIn('post("/api/flow/start"', APP_JS)
        self.assertIn("browserTab.hidden = !started", APP_JS)
        self.assertIn('body[data-started="no"] .tab[data-tab="browser"]', CSS)
        # RUN refused server-side comes before the thread is started
        self.assertLess(SERVICE.index("gate = self.start_gate_error()"), SERVICE.index('name="show-mode", daemon=True'))

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

    def test_the_timing_fields_are_seconds_and_the_go_timings_are_named(self):
        # The operator reads seconds for the two waits Show Mode really uses;
        # Go's milliseconds stay in the settings file but are never typed.
        self.assertIn(".range", CSS)
        for element_id in ("reply-delay-min-s", "reply-delay-max-s", "skip-no-reply-min-s", "skip-no-reply-max-s"):
            self.assertIn('id="{}"'.format(element_id), HTML)
            self.assertIn('"{}"'.format(element_id), APP_JS)
        self.assertIn("msToSeconds", APP_JS)
        self.assertIn("secondsToMs", APP_JS)
        self.assertIn("skip_if_no_reply_ms", APP_JS)
        # The reply delay is the Go field pair (seconds): same names and unit as
        # the Go settings model, so no second copy of the setting can drift.
        for key in ("reply_delay_min_seconds", "reply_delay_max_seconds"):
            self.assertIn(key, APP_JS)
        self.assertNotIn("reply_delay_ms", APP_JS)
        self.assertNotIn('id="reply-delay-ms"', HTML)
        self.assertNotIn('"reply-delay-ms"', APP_JS)
        # The Go worker-engine timings live behind Advanced, with the exact Go
        # field names in seconds, and the panel says who uses them.
        self.assertIn('id="advanced-settings"', HTML)
        self.assertIn("Go worker engine", HTML)
        self.assertIn("worker", HTML)
        for element_id, field in (
            ("poll-min", "poll_min_seconds"),
            ("poll-max", "poll_max_seconds"),
            ("action-min", "action_min_seconds"),
            ("action-max", "action_max_seconds"),
        ):
            self.assertIn('id="{}"'.format(element_id), HTML)
            self.assertIn('"{}"'.format(element_id), APP_JS)
            self.assertIn(field, APP_JS)
        # poll interval is still the backend's own knob, now visible in the same
        # advanced block with its (ms) unit spelled out.
        self.assertIn('id="poll-interval-ms"', HTML)
        self.assertIn("poll_interval_ms", APP_JS)
        self.assertIn("(ms)", HTML)
        # every advanced field is written by the one SAVE of the tab
        self.assertIn("function saveGeneral()", APP_JS)

    def test_the_silence_option_reaches_the_runner(self):
        self.assertIn("skip_if_no_reply_ms", SERVICE)
        self.assertIn("no_reply_seconds", SERVICE)
        self.assertIn("no_reply_seconds", (PACKAGE / "runner.py").read_text(encoding="utf-8"))
        self.assertIn("NO REPLY | partner silent for", (PACKAGE / "runner.py").read_text(encoding="utf-8"))

    def test_the_overview_tab_shows_every_browser(self):
        # WS1: one row per fresh browser, live, plus the counters and the one
        # next action. Overview is the first tab and the landing panel.
        self.assertLess(HTML.index('data-tab="overview"'), HTML.index('data-tab="browser"'))
        self.assertIn('id="panel-overview"', HTML)
        self.assertIn('class="panel active" id="panel-overview"', HTML)
        self.assertIn('id="worker-rows"', HTML)
        self.assertIn('id="worker-empty"', HTML)
        for element_id in ("overview-kpis", "overview-next", "overview-proxy"):
            self.assertIn('id="{}"'.format(element_id), HTML)
            self.assertIn('"{}"'.format(element_id), APP_JS)
        for fn in ("function renderOverview(", "function renderWorkers(", "function workerStateChip("):
            self.assertIn(fn, APP_JS)
        self.assertIn("snapshot.workers", APP_JS)
        self.assertIn('tab: "overview"', APP_JS)  # the console lands here
        # the status strip mirrors the same numbers without a click
        for element_id in ("status-strip", "ss-state", "ss-workers", "ss-sent", "ss-incoming", "ss-proxy", "ss-next"):
            self.assertIn('id="{}"'.format(element_id), HTML)
        self.assertIn("function renderStatusStrip(", APP_JS)
        # a per-browser CLOSE goes through the server (user chose close-one)
        self.assertIn('"/api/workers/close"', APP_JS)
        self.assertIn('"/api/workers/close"', SERVER)
        self.assertIn("def close_worker", SERVICE)
        self.assertIn("data-close=", APP_JS)

    def test_the_overview_can_check_the_system_before_a_run(self):
        # WS3: preflight from the UI - the same checks app.py --diag runs.
        self.assertIn('id="ov-diag"', HTML)
        self.assertIn('id="ov-diag-results"', HTML)
        self.assertIn('id="ov-diag-copy"', HTML)
        self.assertIn("function runDiagnostics(", APP_JS)
        self.assertIn('"/api/diagnostics"', APP_JS)
        self.assertIn('"/api/diagnostics"', SERVER)
        self.assertIn("def run_diagnostics(", SERVICE)
        # ?deep=0 stays available as the fast path (no browser is launched)
        self.assertIn("deep=0", SERVER)
        self.assertIn("def diagnostics", SERVICE)

    def test_destructive_buttons_ask_first_in_the_app(self):
        # WS4: one in-app dialog, not a native popup (the wording matters).
        self.assertNotIn("window.confirm(", APP_JS)
        self.assertNotIn("confirm(", APP_JS.replace("askConfirm(", ""))
        self.assertIn('id="dialog"', HTML)
        self.assertIn('id="dialog-ok"', HTML)
        self.assertIn('id="dialog-cancel"', HTML)
        self.assertIn("function askConfirm(", APP_JS)
        self.assertIn("function closeDialog(", APP_JS)
        # the confirmed actions: STOP, proxy prune, folder clear, logs clear, CLOSE
        for title in (
            'title: "Stop every browser?"',
            'title: "Clear the dashboard log?"',
            'title: "Clear the loaded folder?"',
            'title: `Remove ${failed.size} failed line(s)?`',
            'title: `Close browser ${index}?`',
        ):
            self.assertIn(title, APP_JS)
        self.assertGreaterEqual(APP_JS.count("askConfirm("), 6)
        for fn in ("function stopShowMode(", "function pruneFailedProxies(", "function clearFolder(", "function closeWorker("):
            self.assertIn(fn, APP_JS)
        # Esc closes the dialog first (capture phase), so flow.js cannot fight it
        self.assertIn("}, true);", APP_JS)
        self.assertIn("closeDialog(false)", APP_JS)

    def test_proxy_results_can_be_copied_and_failures_removed(self):
        # WS5: a result table an operator can act on.
        for element_id in ("proxy-entries", "proxy-results", "proxy-summary", "proxy-copy", "proxy-prune", "proxy-validate"):
            self.assertIn('id="{}"'.format(element_id), HTML)
        self.assertIn('"/api/proxy/validate"', APP_JS)
        self.assertIn('"/api/proxy/validate"', SERVER)
        self.assertIn("def validate_proxy_entries", SERVICE)
        self.assertIn("function renderProxyResults(", APP_JS)
        self.assertIn("function validateProxyLive(", APP_JS)
        self.assertIn("default_scheme", APP_JS)
        self.assertIn("default_scheme", SERVICE)

    def test_the_chat_tab_can_search_copy_and_export(self):
        # WS6: the observer window, but usable.
        for element_id in ("chat-search", "chat-follow", "chat-copy", "chat-export"):
            self.assertIn('id="{}"'.format(element_id), HTML)
        self.assertIn("function chatRows(", APP_JS)
        self.assertIn("state.chatSearch", APP_JS)
        self.assertIn("state.chatFollow", APP_JS)
        self.assertIn("function downloadText(", APP_JS)
        self.assertIn("function copyText(", APP_JS)

    def test_the_logs_tab_can_download_the_raw_log_file(self):
        # WS6: the counters tell what is in the buffer, the download gives the
        # operator the file data/logs/port.log itself.
        self.assertIn('id="log-levels"', HTML)
        self.assertIn("function logLevelCounts(", APP_JS)
        self.assertIn('id="log-download"', HTML)
        self.assertIn('"/api/logs/file"', APP_JS)
        self.assertIn('"/api/logs/file"', SERVER)
        self.assertIn("def logs_file_bytes", SERVICE)
        self.assertIn("attachment", SERVER)
        self.assertIn("data-level=", HTML)

    def test_the_theme_can_be_switched_and_respects_reduced_motion(self):
        # WS7: the glow theme stays, PLAIN is one click away and is the default
        # for anyone who asked their system to reduce motion.
        self.assertIn('id="theme-toggle"', HTML)
        self.assertIn("function applyTheme(", APP_JS)
        self.assertIn("chitchat-theme", APP_JS)
        self.assertIn("body.dataset.theme", APP_JS)
        self.assertIn('body[data-theme="plain"]', CSS)
        self.assertIn("prefers-reduced-motion", APP_JS)
        # keyboard + focus visibility (a11y)
        self.assertIn(":focus-visible", CSS)
        self.assertIn('id="help-open"', HTML)
        self.assertIn("function showHelp(", APP_JS)

    def test_the_folder_controls_sit_in_the_mode_dropdown_row(self):
        # The operator asked for the folder loader next to the mode choice: one
        # row answers "how does a chat get its lines, and where do they come
        # from" without a second look around the tab.
        row = element_html("mode-row")
        for element_id in (
            "chat-mode",
            "mode-hint",
            "folder-field",
            "folder-browse",
            "folder-input",
            "folder-summary",
            "folder-path",
            "folder-path-load",
            "folder-clear",
            "folder-list",
            "folder-note",
        ):
            self.assertIn('id="{}"'.format(element_id), row, element_id + " must live in the mode row")
        # and nowhere else (the old separate row above the dropdown is gone)
        self.assertNotIn('id="folder-browse"', HTML[: HTML.index('id="mode-row"')])
        # the folder box is read as part of the mode decision: highlighted while
        # Folder mode has nothing loaded, with the reason written next to it
        self.assertIn('$("folder-field").classList.toggle("needs"', APP_JS)
        self.assertIn('$("folder-note")', APP_JS)
        self.assertIn('$("chat-mode").addEventListener("change"', APP_JS)
        self.assertIn("#folder-field", CSS)
        self.assertIn(".loaded-files", CSS)
        # every loader stays wired to its own route
        self.assertIn('loadFolderPath().catch', APP_JS)
        self.assertIn('loadFolderFromPicker(', APP_JS)
        self.assertIn('"/api/folder"', APP_JS)
        self.assertIn('"/api/folder/files"', APP_JS)
        self.assertIn('"/api/folder/clear"', APP_JS)

    def test_the_theme_is_not_the_old_go_stylesheet(self):
        self.assertNotIn(".browser-choice", CSS)
        self.assertIn("matrix", (UI / "app.js").read_text(encoding="utf-8").lower() + CSS.lower())


if __name__ == "__main__":
    unittest.main()
