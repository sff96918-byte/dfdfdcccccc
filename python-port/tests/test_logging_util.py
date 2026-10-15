"""The activity log: Go's levels, JSON lines and the applog redaction port."""

from __future__ import annotations

import json
import tempfile
import unittest
from pathlib import Path

from chitchatport.logging_util import ActivityLog, LEVELS, redact


class RedactTests(unittest.TestCase):
    """Ported from internal/applog.Redact — known credential shapes only."""

    def test_headers_are_masked(self):
        # The header name keeps its spelling, exactly like Go's regexp keeps $1.
        line = "GET /users/me Authorization: Bearer abc123.def-456"
        self.assertNotIn("abc123", redact(line))
        self.assertEqual(redact(line), "GET /users/me Authorization: [REDACTED]")
        self.assertNotIn("deadbeef", redact("Cookie=session=deadbeef; other=1"))
        self.assertIn("Cookie: [REDACTED]", redact("Cookie: session=deadbeef; other=1"))

    def test_token_password_and_api_key_fields_are_masked(self):
        for line in (
            '{"token": "eyJhbGciOi.payload.sig"}',
            "password=hunter2",
            "api_key: sk-live-1234567890",
            "secret = s3cret-value",
        ):
            self.assertNotIn("hunter2", redact(line))
            self.assertIn("[REDACTED]", redact(line))
        # The same shapes the Go regexp matches (and does not match).
        self.assertNotIn("sk-live-1234567890", redact("api_key: sk-live-1234567890"))
        self.assertEqual(redact("token_in_name=untouched"), "token_in_name=untouched")

    def test_bearer_values_and_proxy_urls_are_masked(self):
        self.assertEqual(redact("sent Bearer eyJ0.a.b"), "sent Bearer [REDACTED]")
        self.assertEqual(
            redact("Using proxy http://user:pw@1.2.3.4:8080"),
            "Using proxy http://[REDACTED]@1.2.3.4:8080",
        )
        self.assertEqual(
            redact("socks5://u:p@9.9.9.9:1080"), "socks5://[REDACTED]@9.9.9.9:1080"
        )

    def test_ordinary_chat_and_show_mode_lines_are_untouched(self):
        for line in (
            "[default show-live 1/1] INCOMING | partner row accepted; pending=1",
            "[default show-live 1/1] REPLY DELAY | selected=7.25s setting=5-10s",
            "hi there, how are you? my snap is bob",
        ):
            self.assertEqual(redact(line), line)


class ActivityLogTests(unittest.TestCase):
    def test_levels_and_the_file_match_the_go_sink(self):
        with tempfile.TemporaryDirectory() as temp:
            log = ActivityLog(temp)
            log.append("activity", "DOM READY")
            log.append("warn", "SEND UNCONFIRMED | automation paused")
            self.assertEqual(LEVELS, ("ACTIVITY", "INFO", "WARN", "ERROR"))

            lines = [
                json.loads(line)
                for line in (Path(temp) / "port.log").read_text(encoding="utf-8").splitlines()
            ]
            self.assertEqual([line["level"] for line in lines], ["ACTIVITY", "WARN"])
            self.assertEqual(lines[0]["message"], "DOM READY")

    def test_the_file_never_contains_a_credential(self):
        with tempfile.TemporaryDirectory() as temp:
            log = ActivityLog(temp)
            log.append("info", "Using proxy http://operator:topsecret@1.2.3.4:8080")
            log.append("info", 'login {"token":"eyJhbGciOiJIUzI1NiJ9.abc.def"}')
            text = (Path(temp) / "port.log").read_text(encoding="utf-8")
            self.assertNotIn("topsecret", text)
            self.assertNotIn("eyJhbGciOiJIUzI1NiJ9", text)
            # but the operator can still see which proxy line was used
            self.assertIn("http://[REDACTED]@1.2.3.4:8080", text)


if __name__ == "__main__":
    unittest.main()
