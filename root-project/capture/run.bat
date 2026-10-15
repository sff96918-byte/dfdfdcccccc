@echo off
setlocal
cd /d "%~dp0"
where node >nul 2>nul
if errorlevel 1 (
  echo Node.js pawa jacchena. Age install.bat chalaben.
  pause
  exit /b 1
)
echo Starting capture tool... A normal browser window will open.
echo Login kore normally chat korun. Markers: Ctrl+Alt+I incoming, Ctrl+Alt+O outgoing.
echo Stop korte terminal-e Q chapen othoba browser bondho kore din.
node tool\main.cjs %*
if errorlevel 1 (
  echo.
  echo [Somossa] Capture suru hoyni. Prothome install.bat chaliye doctor test
  echo PASS hoy kina dekhun, tarpor abar run.bat chalan.
)
echo.
echo Capture finished. Session folder is under capture-sessions\
pause
