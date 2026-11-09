@echo off
rem Double-click to install or update the nightly build on Windows.
rem The script comes from the default branch. The copy on the stable release
rem is old and ignores JFTG_CHANNEL, so it would install stable instead.
set JFTG_CHANNEL=nightly
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/Aryan-mor/jellyfin-telegram/main/install.ps1 | iex"
if errorlevel 1 (
  echo.
  echo Install failed. See the message above.
  pause
)
