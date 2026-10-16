@echo off
rem ===========================================================================
rem  ChitChat Bot - Python Show Mode : build ChitChatBot.exe
rem
rem  Double-click this file on Windows. It
rem    1. finds Python (the project .venv first),
rem    2. installs the build requirements (playwright, pywebview, pyinstaller),
rem    3. runs the local verification gate  (python tools\verify.py),
rem    4. packs dist\ChitChatBot\ChitChatBot.exe with build\ChitChatBot.spec,
rem    5. runs the packed app's own self test and prints the result.
rem
rem  The result is a FOLDER, not a single file - the folder IS the app:
rem    dist\ChitChatBot\ChitChatBot.exe      double-click this
rem    dist\ChitChatBot\_internal\...        what it needs (do not move files out)
rem    dist\ChitChatBot\data\...             created on first run (settings, logs)
rem
rem  Why a folder: the Show Mode engine (Playwright) brings its own node.exe and
rem  the window brings WebView2, so a single-file exe would unpack ~200 MB to a
rem  temp folder on EVERY start (10-30 seconds of nothing). The folder starts in
rem  about a second. To share it, zip the whole folder.
rem
rem  Optional: drop a fixed-version WebView2 runtime folder next to
rem  ChitChatBot.exe (e.g. WebView2\msedgewebview2.exe) for machines without
rem  WebView2 - the app picks it up automatically, exactly like the Go build.
rem ===========================================================================
setlocal enableextensions
title ChitChat Bot (Python Show Mode) - build exe
pushd "%~dp0"

echo ============================================================
echo  ChitChat Bot - Python Show Mode : build ChitChatBot.exe
echo  Project folder: %CD%
echo ============================================================
echo.

rem --- step 1: a working Python (project .venv first) ------------------------
set "PY="
if exist "%CD%\.venv\Scripts\python.exe" set "PY=%CD%\.venv\Scripts\python.exe"
if not defined PY (
  for /f "delims=" %%P in ('py -3 -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  for /f "delims=" %%P in ('python -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  echo [FAIL] Python was not found. Run install.bat first, or install Python 3.10+.
  goto :fail
)
echo [ok] Python: %PY%

rem --- step 2: build requirements -------------------------------------------
echo [..] Installing build requirements ^(playwright, pywebview, pyinstaller^) ...
"%PY%" -m pip install -r requirements-build.txt --disable-pip-version-check
if errorlevel 1 (
  echo [warn] First attempt failed; retrying without the pip cache ...
  "%PY%" -m pip install --no-cache-dir -r requirements-build.txt --disable-pip-version-check
)
"%PY%" -c "import PyInstaller, playwright" >nul 2>nul
if errorlevel 1 (
  echo [FAIL] Build packages are missing. Read the pip messages above, then run this file again.
  goto :fail
)
echo [ok] Build requirements installed.

rem --- step 3: the verification gate ---------------------------------------
echo.
echo [..] Running the local verification gate ...
"%PY%" tools\verify.py
if errorlevel 1 (
  echo.
  echo [FAIL] Verification failed - the build is stopped on purpose.
  echo        Fix what the FAIL lines say and run this file again.
  goto :fail
)

rem --- step 4: pack ---------------------------------------------------------
echo.
echo [..] Packing dist\ChitChatBot ^(a few minutes; the output is ~150-200 MB^) ...
"%PY%" -m PyInstaller build\ChitChatBot.spec --noconfirm --clean --distpath "%CD%\dist" --workpath "%CD%\build\pyi"
if errorlevel 1 (
  echo.
  echo [FAIL] PyInstaller failed. Scroll up for its message.
  goto :fail
)
if not exist "%CD%\dist\ChitChatBot\ChitChatBot.exe" (
  echo [FAIL] The build finished but dist\ChitChatBot\ChitChatBot.exe is missing.
  goto :fail
)
echo [ok] Built dist\ChitChatBot\ChitChatBot.exe

rem --- step 5: the packed app checks itself ---------------------------------
echo.
echo [..] Running the packed app's self test ...
dist\ChitChatBot\ChitChatBot.exe --selftest
set "SELFTEST=%ERRORLEVEL%"
echo.
if not "%SELFTEST%"=="0" (
  echo [warn] The self test reported a FAIL line above. The exe still opens, but
  echo        check the line before sharing it.
) else (
  echo [ok] The packed app passed its own self test.
)

echo.
echo ============================================================
echo  Done.  Start the app with:  dist\ChitChatBot\ChitChatBot.exe
echo  Double-click it, or make a desktop shortcut to it.
echo  Settings + logs live in:     dist\ChitChatBot\data\
echo ============================================================
echo.
if not "%CCB_NO_PAUSE%"=="1" pause
popd
exit /b 0

:fail
echo.
if not "%CCB_NO_PAUSE%"=="1" pause
popd
exit /b 1
