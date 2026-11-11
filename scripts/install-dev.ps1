# Replace the installed program with this folder's build.
# Settings, data and library in %LOCALAPPDATA%\jellyfin-telegram stay.
#   pnpm run install:dev

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$dir = Join-Path $env:LOCALAPPDATA 'jellyfin-telegram'
$installed = Join-Path $dir 'jellyfin-telegram.exe'
$built = Join-Path $root 'bin\jellyfin-telegram.exe'
New-Item -ItemType Directory -Force (Split-Path $built) | Out-Null
New-Item -ItemType Directory -Force $dir | Out-Null

$ldflags = @()
$local = Join-Path $root '.build.local'
if (Test-Path $local) {
    $vars = @{}
    foreach ($line in Get-Content $local) {
        if ($line -match '^\s*#' -or $line -notmatch '=') { continue }
        $key, $value = $line.Split('=', 2)
        $vars[$key.Trim()] = $value.Trim()
    }
    $pkg = 'github.com/Aryan-mor/jellyfin-telegram/internal/config'
    if ($vars.DefaultAPIID -and $vars.DefaultAPIHash) {
        $ldflags = "-X ${pkg}.DefaultAPIID=$($vars.DefaultAPIID) -X ${pkg}.DefaultAPIHash=$($vars.DefaultAPIHash)"
    }
}

Write-Host "building $built"
if ($ldflags) {
    & go build -ldflags $ldflags -o $built ./cmd/jellyfin-telegram
} else {
    & go build -o $built ./cmd/jellyfin-telegram
}
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "stopping the installed copy"
Get-Process -Name 'jellyfin-telegram' -ErrorAction SilentlyContinue | Stop-Process -Force
$deadline = (Get-Date).AddSeconds(20)
do {
    $busy = Get-NetTCPConnection -LocalPort 8097 -State Listen -ErrorAction SilentlyContinue
    if (-not $busy) { break }
    Start-Sleep -Milliseconds 400
} while ((Get-Date) -lt $deadline)
if (Get-NetTCPConnection -LocalPort 8097 -State Listen -ErrorAction SilentlyContinue) {
    throw 'Port 8097 is still in use. Stop whatever is listening there, then run this again.'
}

Copy-Item -Force $built $installed

$task = 'jellyfin-telegram'
$action = New-ScheduledTaskAction -Execute $installed -Argument 'serve' -WorkingDirectory $dir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
$settings.Hidden = $true
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null

Write-Host "starting $installed"
Start-Process -FilePath $installed -ArgumentList 'serve' -WorkingDirectory $dir -WindowStyle Hidden
Write-Host 'Installed. Settings were kept.'
Write-Host 'Panel: http://127.0.0.1:8097/panel'
