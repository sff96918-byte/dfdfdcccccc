@echo off
rem ChitChat Bot - Python Show Mode : start the dashboard in your browser.
rem Uses .venv when present, otherwise the system Python; installs the
rem requirements first when they are missing.
setlocal enableextensions
title ChitChat Bot (Python Show Mode)
pushd "%~dp0"

set "PY="
if exist "%CD%\.venv\Scripts\python.exe" set "PY=%CD%\.venv\Scripts\python.exe"
if not defined PY (
  for /f "delims=" %%P in ('py -3 -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  for /f "delims=" %%P in ('python -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  for /f "delims=" %%P in ('python3 -c "import sys;print(sys.executable)" 2^>nul') do set "PY=%%P"
)
if not defined PY (
  echo [FAIL] Python was not found.
  echo        Run install.bat first, or install Python 3.10+ and try again.
  echo.
  pause
  popd
  exit /b 1
)

"%PY%" -c "import playwright" >nul 2>nul
if errorlevel 1 (
  echo [..] Dependencies are missing; running install.bat first ...
  echo.
  set "CCB_NO_PAUSE=1"
  call "%~dp0install.bat"
  set "CCB_NO_PAUSE="
  if exist "%CD%\.venv\Scripts\python.exe" set "PY=%CD%\.venv\Scripts\python.exe"
  "%PY%" -c "import playwright" >nul 2>nul
  if errorlevel 1 (
    echo.
    echo [FAIL] Dependencies are still missing. Read the install messages above.
    echo.
    pause
    popd
    exit /b 1
  )
)

echo Starting ChitChat Bot - Python Show Mode ...
echo Your browser opens the dashboard automatically (hacker-dark theme).
echo ^(Keep this window open while the bot runs; press Ctrl+C here to stop.^)
echo.
"%PY%" app.py
set "CODE=%ERRORLEVEL%"
if not "%CODE%"=="0" (
  echo.
  echo [FAIL] The dashboard exited with code %CODE%.
  echo        Copy the messages above when asking for help.
  echo.
  pause
)
popd
exit /b %CODE%
