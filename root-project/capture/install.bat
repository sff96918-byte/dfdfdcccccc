@echo off
setlocal
cd /d "%~dp0"
echo Capture tool setup suru hocche...
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install.ps1"
echo.
pause
