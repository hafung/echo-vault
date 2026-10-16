@echo off
cd /d "%SystemRoot%"
cls
title ChatGPT Desktop - GPT Vault enhanced sync
echo This launcher restarts ChatGPT Desktop once to enable enhanced sync.
echo Subsequent launches reuse the running enhanced Desktop.
echo.
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\start-chatgpt-enhanced.ps1" -Restart
if errorlevel 1 (
    echo.
    echo Enhanced sync could not be enabled. Review the error above.
    pause
)
