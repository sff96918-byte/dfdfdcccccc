"""The frozen-app helpers: where data lives, the lock, the window support.

These are the parts of a packaged .exe that break silently - a data folder
pointing into a temp directory that is deleted on exit, two instances fighting
over one log file, or a bundled WebView2 that is ignored. Each one is a pure
function here, so they are tested without a display and without PyInstaller.
"""

from __future__ import annotations

import json
import os
import sys
import tempfile
import unittest
from dataclasses import asdict
from pathlib import Path
from unittest import mock

from chitchatport import runtime
from chitchatport.settings import SettingsStore


class FrozenPathTests(unittest.TestCase):
    def test_from_source_the_project_folder_is_the_bundle(self):
        self.assertFalse(runtime.is_frozen())
        self.assertEqual(runtime.bundle_dir(), Path(runtime.__file__).resolve().parents[1])
        self.assertTrue(runtime.resource_path("chitchatport", "ui", "index.html").is_file())
        self.assertEqual(runtime.executable_dir(), runtime.bundle_dir())

    def test_a_frozen_bundle_reads_its_resources_from_meipass(self):
        with tempfile.TemporaryDirectory() as temp:
            meipass = Path(temp) / "_MEI123"
            (meipass / "chitchatport" / "ui").mkdir(parents=True)
            (meipass / "chitchatport" / "ui" / "index.html").write_text("ui", encoding="utf-8")
            with mock.patch.object(sys, "frozen", True, create=True), mock.patch.object(
                sys, "_MEIPASS", str(meipass), create=True
            ):
                self.assertTrue(runtime.is_frozen())
                self.assertEqual(runtime.bundle_dir(), meipass.resolve())
                self.assertEqual(
                    runtime.resource_path("chitchatport", "ui", "index.html").read_text(encoding="utf-8"),
                    "ui",
                )

    def test_the_go_reference_folder_follows_the_layout(self):
        # from source: the real Go tree next to the port
        reference = runtime.go_reference_dir()
        self.assertEqual(reference.name, "root-project")
        self.assertTrue((reference / "internal" / "browser" / "showmode_dom.js").is_file())
        # frozen: the build-time copy inside the bundle. The bundle path comes
        # from tempfile, not a hardcoded "/tmp/...": bundle_dir() resolves it,
        # and on Windows a leading-slash path resolves to <drive>:\tmp, which
        # would make this assertion about the OS, not about the code.
        with tempfile.TemporaryDirectory() as bundle:
            with mock.patch.object(sys, "frozen", True, create=True), mock.patch.object(
                sys, "_MEIPASS", bundle, create=True
            ):
                self.assertEqual(runtime.go_reference_dir(), Path(bundle).resolve() / "go-reference")


class DataDirTests(unittest.TestCase):
    def test_an_explicit_folder_wins(self):
        with tempfile.TemporaryDirectory() as temp:
            choice = runtime.choose_data_dir(temp)
            # Compare the *resolved* folder: on Windows tempfile hands back the
            # short (8.3) form, e.g. C:\Users\RUNNER~1\AppData\Local\Temp\tmp1,
            # while the app reports the long form of the same folder. The folder
            # is the same folder - comparing the raw strings would be a test of
            # the file system's naming, not of the app.
            resolved = Path(temp).resolve()
            self.assertEqual(choice.path, resolved)
            self.assertEqual(choice.source, "explicit")
            self.assertIn(str(resolved), choice.describe())

    def test_next_to_the_exe_is_used_when_it_is_writable(self):
        with tempfile.TemporaryDirectory() as temp:
            with mock.patch.object(runtime, "executable_dir", return_value=Path(temp)), mock.patch.object(
                runtime, "is_frozen", return_value=True
            ):
                choice = runtime.choose_data_dir()
            self.assertEqual(choice.path, (Path(temp) / "data").resolve())
            self.assertEqual(choice.source, "next-to-exe")
            self.assertTrue((Path(temp) / "data").is_dir())

    def test_from_source_the_project_folder_is_named_as_such(self):
        with tempfile.TemporaryDirectory() as temp:
            with mock.patch.object(runtime, "executable_dir", return_value=Path(temp)):
                choice = runtime.choose_data_dir()
            self.assertEqual(choice.path, (Path(temp) / "data").resolve())
            self.assertEqual(choice.source, "project")

    def test_a_read_only_install_falls_back_to_the_user_profile(self):
        """A Program Files install or a read-only stick must still work."""
        with tempfile.TemporaryDirectory() as temp:
            fallback = Path(temp) / "local" / "ChitChatBot" / "data"
            with mock.patch.object(runtime, "executable_dir", return_value=Path(temp)), mock.patch.object(
                runtime, "is_frozen", return_value=True
            ), mock.patch.object(runtime, "is_writable", return_value=False), mock.patch.object(
                runtime, "_fallback_data_dir", return_value=fallback
            ):
                choice = runtime.choose_data_dir()
            self.assertEqual(choice.source, "localappdata")
            self.assertEqual(choice.path, fallback.resolve())

    def test_the_fallback_path_is_per_user(self):
        path = runtime._fallback_data_dir()
        self.assertEqual(path.name, "data")
        self.assertEqual(path.parent.name, "ChitChatBot")

    def test_the_chosen_folder_can_hold_real_settings(self):
        with tempfile.TemporaryDirectory() as temp:
            choice = runtime.choose_data_dir(temp)
            store = SettingsStore(choice.path)
            store.save_general_settings(store.load_general_settings())
            saved = json.loads((choice.path / "state" / "general.json").read_text(encoding="utf-8"))
            self.assertEqual(
                sorted(saved),
                [
                    "first_message_answer_all",
                    "first_message_mode",
                    "poll_interval_ms",
                    "reply_delay_max_seconds",
                    "reply_delay_min_seconds",
                    "skip_if_no_reply_max_ms",
                    "skip_if_no_reply_ms",
                ],
            )


class InstanceLockTests(unittest.TestCase):
    def test_the_second_instance_is_refused_with_the_holder(self):
        with tempfile.TemporaryDirectory() as temp:
            taken, why = runtime.acquire_instance_lock(Path(temp))
            self.assertTrue(taken, why)
            self.assertEqual(why, "")
            again, reason = runtime.acquire_instance_lock(Path(temp))
            self.assertFalse(again)
            self.assertIn("already running", reason)
            self.assertIn("pid {}".format(os.getpid()), reason)

    def test_a_stale_lock_from_a_dead_process_is_taken_over(self):
        """A crashed run must never block the next start."""
        with tempfile.TemporaryDirectory() as temp:
            runtime.instance_lock_path(Path(temp)).write_text("pid=999999\n", encoding="utf-8")
            taken, why = runtime.acquire_instance_lock(Path(temp))
            self.assertTrue(taken, why)

    def test_releasing_only_removes_our_own_lock(self):
        with tempfile.TemporaryDirectory() as temp:
            runtime.acquire_instance_lock(Path(temp))
            runtime.release_instance_lock(Path(temp))
            self.assertFalse(runtime.instance_lock_path(Path(temp)).exists())
            # someone else's lock survives our release
            runtime.instance_lock_path(Path(temp)).write_text("pid=4242\n", encoding="utf-8")
            runtime.release_instance_lock(Path(temp))
            self.assertTrue(runtime.instance_lock_path(Path(temp)).exists())

    def test_a_lock_we_cannot_write_never_stops_the_app(self):
        with tempfile.TemporaryDirectory() as temp:
            with mock.patch("pathlib.Path.write_text", side_effect=OSError("read-only")):
                taken, why = runtime.acquire_instance_lock(Path(temp))
            self.assertTrue(taken, "a read-only data folder must still start")
            self.assertIn("could not write the lock", why)


class WebView2Tests(unittest.TestCase):
    def test_a_bundled_runtime_next_to_the_exe_is_found_and_used(self):
        with tempfile.TemporaryDirectory() as temp:
            bundled = Path(temp) / "WebView2"
            bundled.mkdir()
            (bundled / "msedgewebview2.exe").write_bytes(b"stub")
            with mock.patch.object(runtime, "executable_dir", return_value=Path(temp)), mock.patch.dict(
                os.environ, {}, clear=False
            ):
                os.environ.pop(runtime.WEBVIEW2_ENV_VAR, None)
                self.assertEqual(runtime.bundled_webview2_dir(), bundled)
                used = runtime.use_bundled_webview2()
                self.assertEqual(used, str(bundled))
                self.assertEqual(os.environ[runtime.WEBVIEW2_ENV_VAR], str(bundled))

    def test_the_fixed_version_folder_name_the_go_build_looked_for(self):
        with tempfile.TemporaryDirectory() as temp:
            child = Path(temp) / "WebView2" / "Microsoft.WebView2.FixedVersionRuntime.120.0.0"
            child.mkdir(parents=True)
            (child / "msedgewebview2.exe").write_bytes(b"stub")
            with mock.patch.object(runtime, "executable_dir", return_value=Path(temp)):
                self.assertEqual(runtime.bundled_webview2_dir(), child)

    def test_no_bundled_runtime_means_the_system_one_is_used(self):
        with tempfile.TemporaryDirectory() as temp:
            with mock.patch.object(runtime, "executable_dir", return_value=Path(temp)), mock.patch.dict(
                os.environ, {}, clear=False
            ):
                os.environ.pop(runtime.WEBVIEW2_ENV_VAR, None)
                self.assertIsNone(runtime.bundled_webview2_dir())
                self.assertEqual(runtime.use_bundled_webview2(), "")
                self.assertNotIn(runtime.WEBVIEW2_ENV_VAR, os.environ)


class PackagingFilesTests(unittest.TestCase):
    """The build files must exist and name the same things as the code."""

    ROOT = Path(__file__).resolve().parents[1]

    def test_the_spec_bundles_what_the_frozen_app_reads(self):
        spec = (self.ROOT / "build" / "ChitChatBot.spec").read_text(encoding="utf-8")
        for needed in (
            '"chitchatport/ui"',
            '"chitchatport/assets"',
            "go-reference/internal/browser",
            "go-reference/reference-ui",
            "hook-playwright.sync_api.py",  # the belt-and-braces check
            "console=False",
        ):
            self.assertIn(needed, spec, needed)
        self.assertIn("ChitChatBot", spec)

    def test_the_icon_is_a_real_multi_size_ico(self):
        data = (self.ROOT / "build" / "appicon.ico").read_bytes()
        self.assertEqual(data[:4], b"\x00\x00\x01\x00", "not an .ico")
        count = int.from_bytes(data[4:6], "little")
        self.assertGreaterEqual(count, 3)
        for index in range(count):
            entry = data[6 + 16 * index : 22 + 16 * index]
            size = int.from_bytes(entry[8:12], "little")
            offset = int.from_bytes(entry[12:16], "little")
            self.assertEqual(data[offset : offset + 4], b"\x89PNG", "each image is a PNG-in-ICO")
            self.assertGreater(size, 200)

    def test_the_windows_version_resource_is_valid_text(self):
        text = (self.ROOT / "build" / "version_info.txt").read_text(encoding="utf-8")
        for key in ("FileVersion", "ProductName", "OriginalFilename", "ChitChatBot.exe"):
            self.assertIn(key, text)

    def test_build_exe_runs_the_gate_before_packing(self):
        bat = (self.ROOT / "build-exe.bat").read_text(encoding="utf-8")
        self.assertIn("tools\\verify.py", bat)
        self.assertIn("ChitChatBot.spec", bat)
        self.assertLess(
            bat.index("tools\\verify.py"),
            bat.index("PyInstaller"),
            "the gate must run before PyInstaller",
        )
        self.assertIn("--selftest", bat, "the packed app checks itself right after the build")

    def test_the_build_requirements_pull_the_window_and_the_packager(self):
        text = (self.ROOT / "requirements-build.txt").read_text(encoding="utf-8")
        self.assertIn("-r requirements.txt", text)
        self.assertIn("pywebview", text)
        self.assertIn("pyinstaller", text)


if __name__ == "__main__":
    unittest.main()
