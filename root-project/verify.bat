@echo off
cd /d "%~dp0"
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify.ps1
exit /b %ERRORLEVEL%
