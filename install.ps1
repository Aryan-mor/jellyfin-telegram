# One-step installer for Windows.
#   irm https://github.com/Aryan-mor/jellyfin-telegram/releases/latest/download/install.ps1 -OutFile $env:TEMP\jellyfin-telegram-install.ps1; & $env:TEMP\jellyfin-telegram-install.ps1
# It downloads the latest build into %LOCALAPPDATA%\jellyfin-telegram, makes it start at logon,
# and opens the setup page. Run it again to update; your settings and library are kept.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# Save-Url downloads one file and draws a progress bar. Invoke-WebRequest's own bar
# makes the download very slow, so this reads the stream itself.
function Save-Url([string]$url, [string]$dest) {
  $req = [System.Net.HttpWebRequest]::Create($url)
  $req.UserAgent = 'jellyfin-telegram'
  $req.AllowAutoRedirect = $true
  $res = $req.GetResponse()
  try {
    $total = $res.ContentLength
    $stream = $res.GetResponseStream()
    $output = [System.IO.File]::Create($dest)
    try {
      $buf = New-Object byte[] 65536
      $done = [int64]0
      $shown = -1
      while (($n = $stream.Read($buf, 0, $buf.Length)) -gt 0) {
        $output.Write($buf, 0, $n)
        $done += $n
        if ($total -gt 0) {
          $pct = [int](100 * $done / $total)
          if ($pct -ne $shown -and ($pct % 5 -eq 0 -or $pct -eq 100)) {
            $shown = $pct
            $filled = [int](28 * $pct / 100)
            $bar = ('#' * $filled).PadRight(28, '-')
            Write-Host -NoNewline ("`r[{0}] {1,3}%  Downloading" -f $bar, $pct)
          }
        }
      }
    } finally {
      $output.Close()
      $stream.Close()
    }
  } finally {
    $res.Close()
  }
}

$repo = 'Aryan-mor/jellyfin-telegram'
$dir = Join-Path $env:LOCALAPPDATA 'jellyfin-telegram'
$exe = Join-Path $dir 'jellyfin-telegram.exe'
New-Item -ItemType Directory -Force $dir | Out-Null
# An existing settings file means this run is an update. A first install has none,
# stays in this window, and opens the setup page itself.
$updating = Test-Path (Join-Path $dir '.env')

# A later run with no channel set stays on the channel already installed.
$channelFile = Join-Path $dir 'channel'
$channel = $env:JFTG_CHANNEL
if (-not $channel -and (Test-Path $channelFile)) { $channel = (Get-Content $channelFile -Raw).Trim() }
if ($channel -ne 'nightly') { $channel = 'stable' }
Set-Content -Path $channelFile -Value $channel -Encoding ascii

Write-Host "Downloading jellyfin-telegram ($channel)..."
Get-Process -Name 'jellyfin-telegram' -ErrorAction SilentlyContinue | Stop-Process -Force
$zip = Join-Path $env:TEMP 'jellyfin-telegram.zip'
if ($channel -eq 'nightly') {
  # The releases page, not the API. The API rate limit made this installer fail.
  $atom = Invoke-WebRequest -Headers @{ 'User-Agent' = 'jellyfin-telegram' } -Uri "https://github.com/$repo/releases.atom" -UseBasicParsing
  $found = [regex]::Match($atom.Content, 'v\d+\.\d+\.\d+-nightly\.\d+')
  if (-not $found.Success) { throw 'No nightly release yet.' }
  $tag = $found.Value
  $url = "https://github.com/$repo/releases/download/$tag/jellyfin-telegram-windows-amd64.zip"
} else {
  $url = "https://github.com/$repo/releases/latest/download/jellyfin-telegram-windows-amd64.zip"
}
Save-Url $url $zip
Write-Host ''
Expand-Archive $zip -DestinationPath $dir -Force
Remove-Item $zip

# Start at logon, from the install folder (the program keeps .env, data and library there).
$task = 'jellyfin-telegram'
$action = New-ScheduledTaskAction -Execute $exe -Argument 'serve' -WorkingDirectory $dir
$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
$settings.Hidden = $true
Register-ScheduledTask -TaskName $task -Action $action -Trigger $trigger -Settings $settings -Force | Out-Null

# A shortcut on the Desktop and in the Start menu that opens the panel in the browser.
$url = "[InternetShortcut]`r`nURL=http://127.0.0.1:8097/panel`r`nIconFile=$exe`r`nIconIndex=0`r`n"
foreach ($folder in @([Environment]::GetFolderPath('Desktop'), [Environment]::GetFolderPath('Programs'))) {
  if ($folder) { Set-Content -Path (Join-Path $folder 'jellyfin-telegram panel.url') -Value $url -Encoding ASCII }
}

Write-Host ''
if ($updating) {
  Write-Host 'Update installed. Opening the panel...'
} else {
  Write-Host 'Leave this window open. Setup runs here and shows its progress.'
  Write-Host 'The setup page also opens in your browser; sign in with your phone number there.'
}
# Same window, so the log stays here. A scheduled task would open another console.
# An update detaches and this call returns at once. A first install stays here.
Push-Location $dir
try {
  & $exe serve
  if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
} finally {
  Pop-Location
}
if ($updating) {
  $panel = 'http://127.0.0.1:8097/panel?notice=updated'
  $up = $false
  foreach ($i in 1..40) {
    try {
      $res = Invoke-WebRequest -Uri 'http://127.0.0.1:8097/panel' -UseBasicParsing -TimeoutSec 2
      if ($res.StatusCode -eq 200) { $up = $true; break }
    } catch {}
    Start-Sleep -Milliseconds 500
  }
  if (-not $up) { throw 'Updated, but the panel did not start.' }
  Start-Process $panel
}

