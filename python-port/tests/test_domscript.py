"""The shared DOM script must stay byte-identical to the Go asset."""

from __future__ import annotations

import hashlib
import unittest
from pathlib import Path

from chitchatport import domscript

GO_ASSET = (
    Path(__file__).resolve().parents[2]
    / "root-project"
    / "internal"
    / "browser"
    / "showmode_dom.js"
)


class DomScriptTests(unittest.TestCase):
    def test_asset_is_present_and_complete(self):
        text = domscript.dom_script()
        for marker in (
            "__chitchatShowModeDOMv2",
            "data-chat-composer",
            'data-message-viewport="true"',
            "snapshot()",
            "click(labels)",
            "probe()",
            "dispose()",
        ):
            self.assertIn(marker, text, "DOM asset is missing " + marker)
        self.assertEqual(domscript.ASSET_PATH.suffix, ".js")

    def test_expressions_match_the_go_wrapping(self):
        snapshot = domscript.snapshot_expression()
        self.assertTrue(snapshot.startswith("() => { "))
        self.assertIn("const __sm =", snapshot)
        self.assertTrue(snapshot.endswith("return __sm.snapshot(); }"))
        self.assertTrue(domscript.probe_expression().endswith("return __sm.probe(); }"))
        click = domscript.click_expression()
        self.assertTrue(click.startswith("(labels) => { "))
        self.assertTrue(click.endswith("return __sm.click(labels); }"))

    @unittest.skipUnless(GO_ASSET.exists(), "Go source tree not present next to python-port")
    def test_asset_is_byte_identical_to_the_go_copy(self):
        ours = hashlib.sha256(domscript.ASSET_PATH.read_bytes()).hexdigest()
        theirs = hashlib.sha256(GO_ASSET.read_bytes()).hexdigest()
        self.assertEqual(ours, theirs, "the DOM observer script drifted from the Go asset")


if __name__ == "__main__":
    unittest.main()
