@echo off
setlocal
cd /d "%~dp0"
where node >nul 2>nul
if errorlevel 1 (
  echo Node.js pawa jacchena. Age install.bat chalaben.
  pause
  exit /b 1
)
echo Multi-site capture suru hocche... Ek browser khulbe - seta BONDHO korben na.
echo Notun tab (Ctrl+T) diye ek ek kore site kholun, login korun, normally chat korun.
echo Marker: Ctrl+Alt+I incoming, Ctrl+Alt+O outgoing, Ctrl+Alt+S snapshot.
echo Site bodlale capture folder nijei alada hobe: sessions\\^<run^>\\sites\\^<domain^>\\
echo Stop: terminal-e Q chapen (browser bondho hoye jabe) othoba browser window bondho korun.
node tool\main.cjs %*
if errorlevel 1 (
  echo.
  echo [Somossa] Capture suru hoyni. Age install.bat chaliye setup check PASS korun.
)
echo.
echo Capture sesh. Result: sessions\ folder -> RUN-SUMMARY.md
pause
