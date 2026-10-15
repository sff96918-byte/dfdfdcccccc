"""Pure helpers of the browser layer (no Playwright needed)."""

from __future__ import annotations

import base64
import json
import re
import unittest
from pathlib import Path

from chitchatport.browser import (
    current_user_id,
    current_user_id_from_token,
    fresh_login_info_path,
    sanitize_fresh_file_segment,
    token_from_login,
)
from chitchatport.service import safe_profile_name


def make_jwt(claims: dict) -> str:
    payload = base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip("=")
    return "header." + payload + ".signature"


class TokenTests(unittest.TestCase):
    def test_token_comes_from_the_token_cookie(self):
        login = {"cookies": [{"name": "other", "value": "x"}, {"name": "token", "value": "abc"}]}
        self.assertEqual(token_from_login(login), "abc")
        self.assertEqual(token_from_login({"cookies": []}), "")

    def test_jwt_claims_priority_matches_go(self):
        self.assertEqual(current_user_id_from_token(make_jwt({"userId": "u1", "sub": "u2"})), "u1")
        self.assertEqual(current_user_id_from_token(make_jwt({"sub": "u2", "id": "u3"})), "u2")
        self.assertEqual(current_user_id_from_token(make_jwt({"id": "u3"})), "u3")
        self.assertEqual(current_user_id_from_token(make_jwt({"other": "x"})), "")
        self.assertEqual(current_user_id_from_token("not-a-token"), "")
        self.assertEqual(current_user_id_from_token("a.!!!.c"), "")

    def test_current_user_id_fallbacks(self):
        with_field = {"user_id": "explicit", "cookies": [{"name": "token", "value": make_jwt({"userId": "jwt"})}]}
        self.assertEqual(current_user_id(with_field), "explicit")
        from_jwt = {"cookies": [{"name": "token", "value": make_jwt({"userId": "jwt"})}]}
        self.assertEqual(current_user_id(from_jwt), "jwt")
        from_storage = {"cookies": [], "local_storage": {"cachedUserId": "cached"}}
        self.assertEqual(current_user_id(from_storage), "cached")
        from_session = {"cookies": [], "session_storage": {"cachedUserId": "session-cached"}}
        self.assertEqual(current_user_id(from_session), "session-cached")
        self.assertEqual(current_user_id({"cookies": []}), "")


class LoginPathTests(unittest.TestCase):
    def test_file_name_pattern_matches_the_go_app(self):
        login = {
            "user_id": "user-1",
            "saved_at": "2026-10-15T08:09:10.123456Z",
            "cookies": [],
        }
        path = fresh_login_info_path(Path("profiles/default"), login, 2)
        self.assertEqual(path.parent, Path("profiles/default"))
        self.assertRegex(
            path.name,
            r"^login_info_user-1_\d{8}-\d{6}-\d{9}_02\.json$",
        )
        self.assertIn("20261015-080910-123456000", path.name)

    def test_missing_user_id_uses_the_index(self):
        path = fresh_login_info_path(Path("p"), {"cookies": [], "saved_at": "2026-10-15T00:00:00Z"}, 3)
        self.assertTrue(path.name.startswith("login_info_browser-03_"))

    def test_sanitize_segment_is_path_safe(self):
        self.assertEqual(sanitize_fresh_file_segment(" ../a/b:c*? "), "a_b_c")
        self.assertEqual(sanitize_fresh_file_segment(""), "")


class ProfileNameTests(unittest.TestCase):
    def test_safe_profile_name_matches_the_go_table(self):
        cases = {
            ".": "default",
            " ": "default",
            "": "default",
            "normal": "normal",
            "CON": "_CON",
            "con.txt": "_con.txt",
            "LPT1": "_LPT1",
            "COM9.json": "_COM9.json",
            "COM10": "COM10",
            "account. ": "account",
            "../unsafe/name": "__unsafe_name",
        }
        for value, expected in cases.items():
            self.assertEqual(safe_profile_name(value), expected, value)


if __name__ == "__main__":
    unittest.main()


class _FakePage:
    def __init__(self, closed=False, fail=None, result=None):
        self.closed = closed
        self.fail = fail
        self.result = result if result is not None else {}
        self.calls = 0

    def is_closed(self):
        return self.closed

    def evaluate(self, expression, arg=None):
        self.calls += 1
        if self.fail is not None:
            raise self.fail
        return self.result


class _FakeContext:
    def __init__(self, pages):
        self.pages = pages

    def cookies(self, urls):
        return []


class LoginWaitSurvivesNavigationTests(unittest.TestCase):
    """The bug: a login redirect destroyed the JS context, the error escaped the
    login wait, the crash handler closed the browser mid-login."""

    def backend(self, page, extra_pages=()):
        from chitchatport.browser import PlaywrightDOMBackend

        backend = PlaywrightDOMBackend(user_data_dir="/tmp/unused", channel="chrome")
        backend._page = page
        backend._context = _FakeContext([page, *extra_pages])
        return backend

    def test_navigation_error_is_transient_not_a_crash(self):
        from chitchatport.browser import TransientPageError

        page = _FakePage(fail=Exception(
            "Page.evaluate: Execution context was destroyed, most likely because of a navigation"
        ))
        backend = self.backend(page)
        with self.assertRaises(TransientPageError):
            backend.capture_session()
        with self.assertRaises(TransientPageError):
            backend.session_authenticated()

    def test_a_closed_first_tab_is_replaced_by_the_open_one(self):
        first = _FakePage(closed=True)
        second = _FakePage(result={"status": 200})
        backend = self.backend(first, extra_pages=[second])
        self.assertTrue(backend.page_alive())
        self.assertIs(backend._page, second)
        self.assertTrue(backend.session_authenticated())

    def test_no_tab_left_is_really_gone(self):
        from chitchatport.domchat import PageGoneError

        backend = self.backend(_FakePage(closed=True))
        self.assertFalse(backend.page_alive())
        with self.assertRaises(PageGoneError):
            backend.session_authenticated()

    def test_target_closed_with_another_tab_open_is_transient(self):
        from chitchatport.browser import TransientPageError

        first = _FakePage(fail=Exception("Target page, context or browser has been closed"))
        second = _FakePage(result={"status": 200})
        backend = self.backend(first, extra_pages=[second])
        with self.assertRaises(TransientPageError):
            backend.session_authenticated()
        # the next call drives the surviving tab
        self.assertTrue(backend.session_authenticated())


class ServiceLoginWaitTests(unittest.TestCase):
    def test_the_login_wait_retries_through_busy_pages_and_keeps_the_browser(self):
        import tempfile
        import threading
        from pathlib import Path

        from chitchatport.browser import TransientPageError
        from chitchatport.service import ShowModeService

        class Backend:
            channel = "chrome"
            requested_channel = "chrome"

            def __init__(self):
                self.calls = 0

            def capture_session(self):
                self.calls += 1
                if self.calls < 3:
                    raise TransientPageError("Execution context was destroyed, most likely because of a navigation")
                return {
                    "cookies": [{"name": "token", "value": "eyJhbGciOiJIUzI1NiJ9.eyJ1c2VySWQiOiJ1LTEifQ.sig"}],
                    "local_storage": {},
                    "session_storage": {},
                }

            def session_authenticated(self):
                return True

        with tempfile.TemporaryDirectory() as temp:
            service = ShowModeService(Path(temp))
            service._stop = threading.Event()
            original_wait = service._stop.wait
            service._stop.wait = lambda seconds=None: original_wait(0.01)  # fast test
            backend = Backend()
            login = service._wait_for_login(backend, "default", 1, 1)
        self.assertIsNotNone(login, "the login wait gave up on a navigating page")
        self.assertEqual(backend.calls, 3)
        messages = [entry["message"] for entry in service.logs.entries()]
        self.assertTrue(any("Browser is busy" in m for m in messages), messages)
        self.assertFalse(any(entry["level"] == "ERROR" for entry in service.logs.entries()))
