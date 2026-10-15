@echo off
REM apitest.bat — build + run the standalone API chat tester (separate from the bot code)
REM   apitest.bat          chat with the AI persona
REM   apitest.bat ping     one raw request (is the API / key / model OK?)
REM   apitest.bat scenario horny|warm|cold|asl|tricky|all   scripted stranger (scenarios.txt)
REM   apitest.bat compare warm       one scenario on every COMPARE_MODELS model -> table
setlocal
cd /d "%~dp0"
set "PATH=%ProgramFiles%\Go\bin;%LOCALAPPDATA%\Programs\Go\bin;%USERPROFILE%\go\bin;%PATH%"

if not exist "apitest.config.txt" (
    copy /y "apitest.config.example.txt" "apitest.config.txt" >nul
    echo.
    echo   apitest.config.txt created. Open it in Notepad and paste your API_KEY
    echo   ^(BASE_URL / MODEL are preset for Featherless - change if needed^).
    echo.
    start notepad "apitest.config.txt"
    pause
)

where go >nul 2>&1
if errorlevel 1 (
    echo   [ERROR] Go not found. Run the main project's install.bat first ^(it installs Go^).
    pause
    exit /b 1
)

echo   [..] building apitest.exe ...
set "CGO_ENABLED=0"
go build -o apitest.exe . 
if errorlevel 1 (
    echo   [ERROR] build failed - see the compiler output above.
    pause
    exit /b 1
)

apitest.exe %*
echo.
pause
endlocal
