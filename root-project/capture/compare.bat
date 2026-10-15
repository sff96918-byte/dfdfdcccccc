@echo off
setlocal
cd /d "%~dp0"
if "%~2"=="" (
  echo Usage: compare.bat ^<old-session-folder^> ^<new-session-folder^>
  echo Example: compare.bat capture-sessions\20261014-120000_chitchat.gg capture-sessions\20261101-093000_chitchat.gg
  pause
  exit /b 1
)
node tool\compare.cjs %*
pause
