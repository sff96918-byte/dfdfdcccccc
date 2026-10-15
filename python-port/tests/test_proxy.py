"""Proxy list parsing, rotation and diagnostics - mirrors the Go unit tests.

The accepted formats, the line numbering, the round-robin order and the
credential-free error text must stay identical to Go's ``internal/proxy``.
"""

from __future__ import annotations

import unittest

from chitchatport.proxy import (
    MAX_TEST_ENTRIES,
    Entry,
    Rotator,
    TunnelError,
    dial_tunnel,
    hint_for,
    parse_line,
    parse_list,
    sanitize_provider_text,
    test_list,
)

VALID_LINES = (
    "host.example:8080",
    "http://host.example:8080",
    "host.example:8080:u:p",
    "http://host.example:8080:u:p",
    "http://u:p@host.example:8080",
    "u:p@host.example:8080",
    "socks5://u:p@host.example:8080",
    "socks5://host.example:8080:u:p",
    "socks5h://host.example:8080",
    "sock5://host.example:8080",
    "[::1]:8080",
    "socks5://u:p@[::1]:8080",
)

INVALID_LINES = (
    "",
    "host",
    "host:0",
    "host:65536",
    "host:no",
    "http://secret:password@host:abc",
    "http://u:p@host:80/path",
    "http://u:p@host:80?q=x",
    "ftp://host:80",
    "socks4://host:80",
    "socks5://host:80:user:",
    "socks5://host:80:" + "x" * 256 + ":p",
)


class ParseFormatTests(unittest.TestCase):
    def test_every_go_format_is_accepted(self):
        for line in VALID_LINES:
            with self.subTest(line=line):
                entry = parse_line(line)
                self.assertEqual(entry.port, "8080")
                self.assertNotIn("u:p", entry.label(), "label exposes credentials")

    def test_url_credentials_are_decoded(self):
        entry = parse_line("socks5://u%40name:p%3Aa%40ss@host.example:8080")
        self.assertEqual(entry.username, "u@name")
        self.assertEqual(entry.password, "p:a@ss")

    def test_provider_passwords_keep_colons_and_slashes(self):
        entry = parse_line("host.example:8080:user:pass:with:colons")
        self.assertEqual(entry.password, "pass:with:colons")
        entry = parse_line("socks5://host:8080:user:p@ss:with/slash/")
        self.assertEqual(entry.password, "p@ss:with/slash/")
        entry = parse_line("host:80:user:p://secret")
        self.assertEqual(entry.password, "p://secret")

    def test_ipv6_endpoints(self):
        self.assertEqual(parse_line("http://[2001:db8::1]:8080").address(), "[2001:db8::1]:8080")
        self.assertEqual(parse_line("[::1]:8080").host, "::1")

    def test_defaults_match_go(self):
        entry = parse_line("1.2.3.4:8080")
        self.assertEqual(entry.scheme, "http")
        self.assertFalse(entry.has_auth())
        self.assertEqual(entry.label(), "http://1.2.3.4:8080")
        self.assertEqual(entry.browser_url(), "http://1.2.3.4:8080")
        self.assertFalse(entry.supports_udp())
        self.assertTrue(parse_line("socks5://1.2.3.4:1080").supports_udp())

    def test_bare_scheme_names_normalise(self):
        for name in ("socks5", "SOCKS5", "socks5h", "sock5"):
            self.assertEqual(parse_line("{}://1.2.3.4:1080".format(name)).scheme, "socks5")
        self.assertEqual(parse_line("HTTP://1.2.3.4:8080").scheme, "http")

    def test_trailing_slash_without_credentials(self):
        self.assertEqual(parse_line("1.2.3.4:8080/").port, "8080")


class InvalidFormatTests(unittest.TestCase):
    def test_every_go_invalid_format_is_rejected(self):
        for line in INVALID_LINES:
            with self.subTest(line=line):
                with self.assertRaises(ValueError):
                    parse_line(line)

    def test_errors_never_leak_credentials(self):
        for line in ("http://secret:password@host:abc", "http://secret:password@host:80/x"):
            with self.assertRaises(ValueError) as caught:
                parse_line(line)
            message = str(caught.exception)
            self.assertNotIn("secret", message)
            self.assertNotIn("password@", message)


class ParseListTests(unittest.TestCase):
    def test_blank_lines_are_skipped_and_the_line_number_is_reported(self):
        with self.assertRaises(ValueError) as caught:
            parse_list("\r\n\ninvalid\n")
        self.assertIn("line 3", str(caught.exception))

    def test_list_keeps_order(self):
        entries = parse_list("host:80:u:p\r\nsocks5://host2:1080\n")
        self.assertEqual([entry.host for entry in entries], ["host", "host2"])
        self.assertEqual(entries[0].username, "u")
        self.assertEqual(entries[1].scheme, "socks5")

    def test_empty_list_is_empty(self):
        self.assertEqual(parse_list("\n \r\n"), [])


class RotatorTests(unittest.TestCase):
    def test_round_robin_cycles_in_order(self):
        rotator = Rotator(parse_list("host:80:u:p\r\nsocks5://host2:1080\n"))
        self.assertEqual(len(rotator), 2)
        self.assertEqual([rotator.next().host for _ in range(3)], ["host", "host2", "host"])

    def test_empty_rotator_returns_nothing(self):
        rotator = Rotator([])
        self.assertIsNone(rotator.next())
        self.assertEqual(len(rotator), 0)


class SanitizeTests(unittest.TestCase):
    def test_html_and_credentials_are_removed(self):
        entry = Entry(host="h", port="80", username="alice", password="s3cret")
        raw = "<html><b>402</b> Authentication Required for alice:s3cret</html>"
        cleaned = sanitize_provider_text(raw, entry, 200)
        self.assertNotIn("<", cleaned)
        self.assertNotIn("alice", cleaned)
        self.assertNotIn("s3cret", cleaned)
        self.assertIn("***", cleaned)

    def test_basic_token_is_redacted_too(self):
        entry = Entry(host="h", port="80", username="alice", password="s3cret")
        cleaned = sanitize_provider_text("token " + entry.basic_token(), entry, 200)
        self.assertNotIn(entry.basic_token(), cleaned)

    def test_long_text_is_truncated(self):
        entry = Entry(host="h", port="80")
        cleaned = sanitize_provider_text("x" * 500, entry, 50)
        self.assertLessEqual(len(cleaned), 53)
        self.assertTrue(cleaned.endswith("..."))


class HintTests(unittest.TestCase):
    def test_407_without_credentials_talks_about_user_pass(self):
        hint = hint_for(Entry(host="h", port="80"), 407, "", False)
        self.assertIn("no username:password", hint)

    def test_407_with_credentials_says_re_copy(self):
        hint = hint_for(Entry(host="h", port="80", username="u", password="p"), 407, "", False)
        self.assertIn("rejected the credentials", hint)

    def test_402_points_at_entitlement_when_the_provider_says_so(self):
        entry = Entry(host="h", port="80", username="u", password="p")
        self.assertIn("entitlement", hint_for(entry, 402, "expired plan", False))
        self.assertIn("authentication", hint_for(entry, 402, "Authentication Required", False))

    def test_429_and_5xx_and_network(self):
        entry = Entry(host="h", port="80")
        self.assertIn("rate/concurrency", hint_for(entry, 429, "", False))
        self.assertIn("provider had trouble", hint_for(entry, 503, "", False))
        self.assertIn("No HTTP answer at all", hint_for(entry, 0, "", True))

    def test_unknown_status_has_a_generic_next_step(self):
        self.assertIn("curl", hint_for(Entry(host="h", port="80"), 418, "", False))
        self.assertEqual(hint_for(Entry(host="h", port="80"), 0, "", False), "")


class TunnelInputTests(unittest.TestCase):
    def test_invalid_targets_are_refused_before_connecting(self):
        entry = Entry(host="127.0.0.1", port="9")
        for target in ("", "host", "host:", "host:0", "host:99999", "host:abc"):
            with self.subTest(target=target):
                with self.assertRaises(TunnelError) as caught:
                    dial_tunnel(entry, target, timeout=1)
                self.assertEqual(caught.exception.kind, "input")

    def test_unreachable_proxy_is_a_network_error_without_credentials(self):
        entry = Entry(host="127.0.0.1", port="9", username="alice", password="s3cret")
        with self.assertRaises(TunnelError) as caught:
            dial_tunnel(entry, "example.com:443", timeout=1)
        self.assertEqual(caught.exception.kind, "network")
        self.assertNotIn("s3cret", str(caught.exception))
        hint = hint_for(entry, caught.exception.status, "", True)
        self.assertIn("No HTTP answer at all", hint)


class TestListInputTests(unittest.TestCase):
    def test_empty_input_is_refused(self):
        for raw in ("", "\n\n", "   \r\n"):
            with self.assertRaises(ValueError) as caught:
                test_list(raw)
            self.assertIn("at least one proxy", str(caught.exception))

    def test_more_than_the_cap_is_refused(self):
        raw = "\n".join("1.2.3.{}:8080".format(index) for index in range(MAX_TEST_ENTRIES + 1))
        with self.assertRaises(ValueError) as caught:
            test_list(raw)
        self.assertIn("at most {} proxy lines".format(MAX_TEST_ENTRIES), str(caught.exception))

    def test_oversized_input_is_refused(self):
        with self.assertRaises(ValueError) as caught:
            test_list("1.2.3.4:8080\n" * 6000)
        self.assertIn("too large", str(caught.exception))

    def test_parse_failures_are_reported_without_any_network_use(self):
        results = test_list("1.2.3.4:8080\nnot-a-proxy\n")
        self.assertEqual(len(results), 2)
        self.assertEqual(results[0].line, 1)
        self.assertEqual(results[1].line, 2)
        self.assertTrue(results[1].detail.startswith("expected host:port"))
        self.assertFalse(results[1].ok)

    def test_blank_lines_do_not_hide_the_real_line_numbers(self):
        results = test_list("\n\n1.2.3.4:0\n")
        self.assertEqual([result.line for result in results], [3])


if __name__ == "__main__":
    unittest.main()
