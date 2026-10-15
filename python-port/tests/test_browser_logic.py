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
