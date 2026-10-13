@echo off
cd /d "%SystemRoot%"
cls
title Echo Vault
echo Starting Echo Vault...
echo Existing service: open the browser, then close this launcher.
echo New service: keep this window open; you can minimize it.
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-echo-vault.ps1"
if errorlevel 1 (
    echo.
    echo Echo Vault could not start. Review the error above.
    pause
)
