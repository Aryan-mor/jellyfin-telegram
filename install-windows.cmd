@echo off
rem Double-click to install or update jellyfin-telegram on Windows.
rem Download the script, then run the file. "irm | iex" on the powershell.exe
rem command line is blocked by Defender (Trojan:Win32/Commando.B!ml): Access is denied.
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://github.com/Aryan-mor/jellyfin-telegram/releases/latest/download/install.ps1 -OutFile $env:TEMP\jellyfin-telegram-install.ps1"
if errorlevel 1 goto fail
powershell -NoProfile -ExecutionPolicy Bypass -File "%TEMP%\jellyfin-telegram-install.ps1"
if errorlevel 1 goto fail
goto end
:fail
echo.
echo Install failed. See the message above.
pause
:end
