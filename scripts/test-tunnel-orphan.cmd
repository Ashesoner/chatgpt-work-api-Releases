@echo off
setlocal
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0test-tunnel-orphan.ps1"
set "rc=%errorlevel%"
echo.
if "%rc%"=="0" (
  echo PASS: Tunnel child is reclaimed after abrupt parent termination.
) else (
  echo FAIL: Tunnel orphan regression test returned exit code %rc%.
)
exit /b %rc%