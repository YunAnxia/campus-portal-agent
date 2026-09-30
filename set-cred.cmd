@echo off
chcp 65001 >nul
cd /d "%~dp0"
echo ==========================================================
echo   Campus portal credential setup
echo   (password is encrypted with Windows DPAPI, never stored in plaintext)
echo ==========================================================
echo.
if not exist portalagent.exe (
    echo [ERROR] portalagent.exe not found in this folder.
    echo.
    pause
    exit /b 1
)
portalagent.exe set-cred
echo.
echo ----------------------------------------------------------
if exist credential.dpapi (
    echo [OK] credential.dpapi created.
) else (
    echo [FAILED] credential.dpapi was NOT created. Check the message above.
)
echo ----------------------------------------------------------
echo.
echo Press any key to close this window.
pause >nul
