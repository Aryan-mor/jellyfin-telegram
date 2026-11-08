@echo off
rem Double-click to install or update the nightly build on Windows.
set JFTG_CHANNEL=nightly
powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://github.com/Aryan-mor/jellyfin-telegram/releases/latest/download/install.ps1 | iex"
if errorlevel 1 (
  echo.
  echo Install failed. See the message above.
  pause
)
