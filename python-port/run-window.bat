@echo off
rem ChitChat Bot - Python Show Mode : optional native window mode.
rem Same dashboard, shown in a pywebview window instead of your browser.
rem Needs the optional package:  .venv\Scripts\python -m pip install pywebview
rem Uses .venv when present, otherwise the system Python; installs the
rem requirements first when they are missing.
setlocal enableextensions
title ChitChat Bot (Python Show Mode - window)
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
echo Opening the dashboard in a native window ...
echo ^(Close the window to stop; if it stays black the app falls back to your browser.^)
echo.
"%PY%" app.py --window
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
