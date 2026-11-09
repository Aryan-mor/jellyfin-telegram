@echo off
rem Double-click to install or update the nightly build on Windows.
rem The script comes from the default branch. The copy on the stable release
rem is old and ignores JFTG_CHANNEL, so it would install stable instead.
rem Download the script, then run the file. "irm | iex" on the powershell.exe
rem command line is blocked by Defender (Trojan:Win32/Commando.B!ml): Access is denied.
set JFTG_CHANNEL=nightly
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/Aryan-mor/jellyfin-telegram/main/install.ps1 -OutFile $env:TEMP\jellyfin-telegram-install.ps1"
if errorlevel 1 goto fail
powershell -NoProfile -ExecutionPolicy Bypass -File "%TEMP%\jellyfin-telegram-install.ps1"
if errorlevel 1 goto fail
goto end
:fail
echo.
echo Install failed. See the message above.
pause
:end
