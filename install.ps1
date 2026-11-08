# One-step installer for Windows.
#   irm https://github.com/Aryan-mor/jellyfin-telegram/releases/latest/download/install.ps1 | iex
# It downloads the latest build into %LOCALAPPDATA%\jellyfin-telegram, makes it start at logon,
# and opens the setup page. Run it again to update; your settings and library are kept.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo = 'Aryan-mor/jellyfin-telegram'
$dir = Join-Path $env:LOCALAPPDATA 'jellyfin-telegram'
$exe = Join-Path $dir 'jellyfin-telegram.exe'
New-Item -ItemType Directory -Force $dir | Out-Null

Write-Host 'Downloading jellyfin-telegram...'
Get-Process -Name 'jellyfin-telegram' -ErrorAction SilentlyContinue | Stop-Process -Force
$zip = Join-Path $env:TEMP 'jellyfin-telegram.zip'
Invoke-WebRequest "https://github.com/$repo/releases/latest/download/jellyfin-telegram-windows-amd64.zip" -OutFile $zip
Expand-Archive $zip -DestinationPath $dir -Force
Remove-Item $zip

# Start at logon, from the install folder (the program keeps .env, data and library there).
$task = 'jellyfin-telegram'
$action = New-ScheduledTaskAction -Execute $exe -Argument 'serve' -WorkingDirectory $dir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null

# A shortcut on the Desktop and in the Start menu that opens the panel in the browser.
$url = "[InternetShortcut]`r`nURL=http://127.0.0.1:8097/panel`r`nIconFile=$exe`r`nIconIndex=0`r`n"
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
  if ($folder) { Set-Content -Path (Join-Path $folder 'jellyfin-telegram panel.url') -Value $url -Encoding ASCII }
}

Write-Host 'Starting. The setup page opens in your browser; sign in with your phone number there.'
Start-Process -FilePath $exe -WorkingDirectory $dir
