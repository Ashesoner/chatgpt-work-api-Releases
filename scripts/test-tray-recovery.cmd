@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0test-tray-recovery.ps1"
set "EXITCODE=%ERRORLEVEL%"
echo.
if not "%EXITCODE%"=="0" echo Tray recovery test failed.
if "%EXITCODE%"=="0" echo Tray recovery test passed.
pause
exit /b %EXITCODE%
