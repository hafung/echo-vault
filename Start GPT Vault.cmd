@echo off
cd /d "%SystemRoot%"
cls
title GPT Vault
echo Starting GPT Vault...
echo Existing service: open the browser, then close this launcher.
echo New service: keep this window open; you can minimize it.
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-gpt-vault.ps1"
if errorlevel 1 (
    echo.
    echo GPT Vault could not start. Review the error above.
    pause
)
