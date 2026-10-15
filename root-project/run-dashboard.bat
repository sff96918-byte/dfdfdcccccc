@echo off
REM Starts the built ChitChat Bot dashboard. The data\ folder (accounts,
REM settings, data\eva_chat TXT files) is created next to this file.
cd /d "%~dp0"
if not exist "build\bin\chitchatui.exe" (
    echo build\bin\chitchatui.exe not found. Run install.bat first.
    pause
    exit /b 1
)
start "" "%~dp0build\bin\chitchatui.exe"
