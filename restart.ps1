# Stop the running server and start it again from this folder.
# Double-click restart.cmd, or:  powershell -File .\restart.ps1
# Add -Build to compile first:   powershell -File .\restart.ps1 -Build

param([switch]$Build)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

$exe = Join-Path $root 'bin\jellyfin-telegram.exe'
if (-not (Test-Path $exe)) {
    $exe = Join-Path $root 'jellyfin-telegram.exe'
}

if ($Build -or -not (Test-Path $exe)) {
    Write-Host "building $exe"
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
    if ($ldflags) {
        & go build -ldflags $ldflags -o $exe ./cmd/jellyfin-telegram
    } else {
        & go build -o $exe ./cmd/jellyfin-telegram
    }
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

Get-CimInstance Win32_Process -Filter "Name = 'jellyfin-telegram.exe'" | ForEach-Object {
    if ($_.CommandLine -match 'serve') {
        Write-Host "stopping $($_.ProcessId)"
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
    }
}

$deadline = (Get-Date).AddSeconds(20)
do {
    $busy = Get-NetTCPConnection -LocalPort 8097 -State Listen -ErrorAction SilentlyContinue
    if (-not $busy) { break }
    Start-Sleep -Milliseconds 400
} while ((Get-Date) -lt $deadline)

Write-Host "starting $exe"
& $exe serve
exit $LASTEXITCODE
