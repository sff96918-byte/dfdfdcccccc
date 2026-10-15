@echo off
rem ChitChat Bot - Python Show Mode : one-shot installer.
rem Creates .venv (when possible), installs the requirements and runs the
rem built-in checks. Safe to run again at any time.
setlocal enableextensions
title ChitChat Bot (Python Show Mode) - install
pushd "%~dp0"

echo ============================================================
echo  ChitChat Bot - Python Show Mode : install
echo  Project folder: %CD%
echo ============================================================
echo.

rem --- step 1: locate a working Python (full path, so quoting is safe) ---
set "PY="
for /f "delims=" %%P in ('py -3 -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
if not defined PY (
  for /f "delims=" %%P in ('python -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  for /f "delims=" %%P in ('python3 -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  echo [FAIL] Python was not found.
  echo        Install Python 3.10+ from https://www.python.org/downloads/windows/
  echo        and tick "Add python.exe to PATH" during setup, then run this file again.
  goto :fail
)
"%PY%" -c "import sys; raise SystemExit(0 if sys.version_info >= (3,10) else 1)" >nul 2>nul
if errorlevel 1 (
  echo [FAIL] Python 3.10 or newer is required. Found:
  "%PY%" -c "import sys;print('  ' + sys.version)"
  goto :fail
)
echo [ok] Python found: %PY%
"%PY%" -c "import sys;print('     version ' + sys.version.split()[0])"

rem --- step 2: prefer a project-local virtual environment ---------
if exist "%CD%\.venv\Scripts\python.exe" (
  echo [ok] Existing .venv found; using it.
) else (
  echo [..] Creating .venv ^(keeps this project's packages separate^) ...
  "%PY%" -m venv .venv >nul 2>nul
  if exist "%CD%\.venv\Scripts\python.exe" (
    echo [ok] .venv created.
  ) else (
    echo [warn] Could not create .venv; the system Python will be used instead.
  )
)
if exist "%CD%\.venv\Scripts\python.exe" set "PY=%CD%\.venv\Scripts\python.exe"

rem --- step 3: dependencies ----------------------------------------
"%PY%" -c "import playwright, webview" >nul 2>nul
if not errorlevel 1 (
  echo [ok] Dependencies already installed.
  goto :verify
)
echo [..] Installing dependencies ^(first run downloads roughly 10-60 MB^) ...
"%PY%" -m pip install --upgrade pip --disable-pip-version-check >nul 2>nul
"%PY%" -m pip install -r requirements.txt --disable-pip-version-check
if errorlevel 1 (
  echo [warn] First attempt failed; retrying without the pip cache ...
  "%PY%" -m pip install --no-cache-dir -r requirements.txt --disable-pip-version-check
)
"%PY%" -c "import playwright, webview" >nul 2>nul
if errorlevel 1 (
  echo [FAIL] Dependencies are still missing.
  echo        Check the internet connection or proxy, then run install.bat again.
  goto :fail
)
echo [ok] Dependencies installed.
echo     Note: no Chromium download is needed - Show Mode uses your system Chrome or Edge.

:verify
echo.
echo [..] Running the built-in checks ^(44 tests^) ...
"%PY%" tools\verify.py

echo.
echo ============================================================
echo  Install finished. Start the dashboard with:  run.bat
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
