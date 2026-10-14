# service-watchdog.ps1 -- external health check + recovery for local dev infra.
#
# Runs on a schedule (see scripts/register-watchdog-task.ps1) and checks:
#   1. Docker Desktop -- not a plain child process, so pm2 can't supervise it;
#      needs an active `docker ps` health check and the same clean-restart
#      sequence that's been used by hand throughout this project (kill the
#      Docker Desktop processes, `wsl --shutdown` to release the VM cleanly,
#      relaunch). Covers both "process died" and "process alive but the WSL
#      engine won't start" (e.g. a transient Windows WSL-service hiccup).
#   2. pm2 itself and its managed apps (sis-frontend, sis-api-gateway) -- a
#      safety net in case pm2's daemon died or an app is stuck in a
#      non-'online' state despite pm2's own restart budget.
#
# Every check/action is timestamped into watchdog.log next to this script --
# tail that file to see what the watchdog has been doing.

$ErrorActionPreference = 'Stop'
$logFile = Join-Path $PSScriptRoot 'watchdog.log'

function Log($msg) {
    $ts = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
    Add-Content -Path $logFile -Value "[$ts] $msg"
}

function Test-DockerUp {
    try {
        docker ps *> $null
        return $LASTEXITCODE -eq 0
    } catch {
        return $false
    }
}

# -- 1. Docker ------------------------------------------------------------
if (Test-DockerUp) {
    Log 'Docker OK'
} else {
    Log 'Docker not responding -- restarting (kill processes, wsl --shutdown, relaunch)'
    Get-Process -Name 'Docker Desktop','com.docker.backend','com.docker.build','docker-sandbox' `
        -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    wsl --shutdown *> $null
    Start-Sleep -Seconds 3
    Start-Process 'C:\Program Files\Docker\Docker\Docker Desktop.exe'

    $waited = 0
    $recovered = $false
    while ($waited -lt 90) {
        Start-Sleep -Seconds 5
        $waited += 5
        if (Test-DockerUp) { $recovered = $true; break }
    }
    if ($recovered) {
        Log "Docker recovered after $waited s"
    } else {
        Log 'Docker still down after 90s -- giving up this cycle, will retry next run'
    }
}

# -- 2. pm2 + its managed apps ---------------------------------------------
# `pm2 jlist`'s JSON dumps each app's full inherited environment, which on
# Windows includes both-case duplicates (e.g. "username" and "USERNAME") --
# PowerShell 5.1's ConvertFrom-Json builds a case-insensitive object and
# refuses to parse that. Plain `pm2 list` + text matching sidesteps it.
$pm2 = 'C:\Users\123\AppData\Roaming\npm\pm2.cmd'
$knownApps = @('sis-frontend', 'sis-api-gateway')
$list = & $pm2 list --no-color 2>$null

if (-not $list) {
    Log 'pm2 not responding -- attempting resurrect'
    & $pm2 resurrect *> $null
} else {
    foreach ($name in $knownApps) {
        $row = $list | Select-String -SimpleMatch $name
        if (-not $row) {
            Log "pm2 app '$name' not found in pm2 list -- attempting resurrect"
            & $pm2 resurrect *> $null
        } elseif ($row -notmatch 'online') {
            Log "pm2 app '$name' is not online -- restarting"
            & $pm2 restart $name *> $null
        }
    }
}
