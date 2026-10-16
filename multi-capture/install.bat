@echo off
setlocal
cd /d "%~dp0"
where node >nul 2>nul
if errorlevel 1 (
  echo Node.js pawa jacchena. https://nodejs.org theke Node 22 LTS install korun.
  pause
  exit /b 1
)
node tool\doctor.cjs
if errorlevel 1 (
  echo.
  echo [Somossa] Setup check FAIL hoyeche - upore dekha jaoa line gulo thik korun.
  pause
  exit /b 1
)
echo.
echo Setup PASS. Ekhon run.bat chalale browser khulbe; sekhane ek ek kore site kholun.
pause
