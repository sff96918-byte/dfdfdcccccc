# -*- mode: python ; coding: utf-8 -*-
"""PyInstaller spec for the ChitChat Bot - Python Show Mode desktop app.

Build it with  build-exe.bat  (Windows) or
    python -m PyInstaller build/ChitChatBot.spec --noconfirm --clean
on any OS - the spec is written to work on Windows, macOS and Linux, and the
result is the same dashboard that ``python app.py`` serves, in its own window.

What it bundles, and why:

* ``chitchatport/ui`` - index.html, app.js, flow.js, theme.css and the
  byte-locked styles.css. Without them the .exe would serve a 404 page.
* ``chitchatport/assets/showmode_dom.js`` - the DOM observer script.
* ``playwright`` - the Show Mode engine (its own PyInstaller hook ships the
  driver, node.exe included). Show Mode drives the *system* Chrome/Edge, so no
  Chromium download is bundled.
* ``webview`` - pywebview plus the WebView2 libs PyInstaller's hook collects:
  that is the window itself (the same engine the Go/Wails build used).
* ``go-reference`` - the two Go files the identity checks read
  (``internal/browser/showmode_dom.js`` and ``reference-ui/styles.css``),
  copied out of ``root-project`` *at build time*. Copied, not retyped: the
  .exe can prove its DOM asset and stylesheet still match Go, and the copy can
  never go stale, because it is taken from the Go tree during this build.

No Go code is executed and no Go setting is read: the Go worker-engine timings
are not part of this project at all.
"""

import sys
from pathlib import Path

from PyInstaller.utils.hooks import collect_data_files, collect_submodules

ROOT = Path(SPECPATH).resolve().parent          # python-port/
BUILD = ROOT / "build"
GO_TREE = ROOT.parent / "root-project"          # the Go reference checkout

# ------------------------------------------------------------------ payload
datas = [
    (str(ROOT / "chitchatport" / "ui"), "chitchatport/ui"),
    (str(ROOT / "chitchatport" / "assets"), "chitchatport/assets"),
    (str(ROOT / "README.md"), "."),
]

# The Go reference files, straight from the tree that is in this repository.
go_reference = [
    (GO_TREE / "internal" / "browser" / "showmode_dom.js", "go-reference/internal/browser"),
    (GO_TREE / "reference-ui" / "styles.css", "go-reference/reference-ui"),
    (
        GO_TREE / "cmd" / "chitchatui" / "frontend" / "dist" / "styles.css",
        "go-reference/cmd/chitchatui/frontend/dist",
    ),
]
found_reference = 0
for source, destination in go_reference:
    if source.is_file():
        datas.append((str(source), destination))
        found_reference += 1
if found_reference < 2:
    raise SystemExit(
        "The Go reference tree is not next to the port (looked in {}) - the DOM "
        "asset / stylesheet identity checks inside the .exe need it. Build the "
        "package the same way the zip is laid out: python-port/ next to "
        "root-project/.".format(GO_TREE)
    )

# playwright ships its driver as package data (node.exe, ~90 MB) and registers
# its own PyInstaller hook through an entry point. Belt and braces: if that hook
# is not discovered on this machine, copy the driver in by hand.
import importlib.util

hiddenimports = ["playwright", "playwright.sync_api"]
playwright_spec = importlib.util.find_spec("playwright")
if playwright_spec and playwright_spec.origin:
    playwright_dir = Path(playwright_spec.origin).parent
    own_hook = playwright_dir / "_impl" / "__pyinstaller" / "hook-playwright.sync_api.py"
    if not own_hook.is_file():
        datas += collect_data_files("playwright")
else:
    raise SystemExit("playwright is not installed - run: pip install -r requirements.txt")

# pywebview: the window. Its hook (from pyinstaller-hooks-contrib) collects the
# WebView2 libraries on Windows; the platforms are imported dynamically by
# pywebview, so name the one this OS needs.
webview_spec = importlib.util.find_spec("webview")
if webview_spec:
    hiddenimports += ["webview"]
    if sys.platform == "win32":
        hiddenimports += ["webview.platforms.winforms", "webview.platforms.edgechromium"]
    hiddenimports += collect_submodules("webview.platforms") if sys.platform == "darwin" else []

# ------------------------------------------------------------------ options
excludes = [
    "tkinter", "PyQt5", "PyQt6", "PySide2", "PySide6", "gtk",  # never used, huge
    "matplotlib", "numpy", "scipy", "pandas", "IPython",
]

a = Analysis(
    [str(ROOT / "app.py")],
    pathex=[str(ROOT)],
    binaries=[],
    datas=datas,
    hiddenimports=hiddenimports,
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=excludes,
    noarchive=False,
    optimize=0,
)
pyz = PYZ(a.pure)

exe = EXE(
    pyz,
    a.scripts,
    [],
    exclude_binaries=True,
    name="ChitChatBot",
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=False,
    console=False,          # the window IS the app - exactly like the Go build
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
    icon=str(BUILD / "appicon.ico") if sys.platform == "win32" else None,
    version=str(BUILD / "version_info.txt") if sys.platform == "win32" else None,
)
coll = COLLECT(
    exe,
    a.binaries,
    a.datas,
    strip=False,
    upx=False,
    upx_exclude=[],
    name="ChitChatBot",
)
