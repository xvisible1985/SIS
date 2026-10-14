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
#
# Restart counters per service are also kept in watchdog-stats.json next to
# this script, read by api-gateway's GET /admin/watchdog-stats and shown in
# the "Go-сервисы" admin tab (frontend/src/features/admin-services) -- that's
# the whole point of keeping it structured instead of just the text log.

$ErrorActionPreference = 'Stop'
$logFile = Join-Path $PSScriptRoot 'watchdog.log'
$statsFile = Join-Path $PSScriptRoot 'watchdog-stats.json'

function Log($msg) {
    $ts = Get-Date -Format 'yyyy-MM-dd HH:mm:ss'
    Add-Content -Path $logFile -Value "[$ts] $msg"
}

function Format-Rfc3339($dt) {
    # Go's time.Time JSON (un)marshaling expects RFC3339 -- plain -Format 'o' or
    # the default ToString() don't match it closely enough to round-trip cleanly.
    return $dt.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
}

function Update-WatchdogStat($name, [bool]$restarted, $status) {
    $stats = @{}
    if (Test-Path $statsFile) {
        try {
            $raw = Get-Content -Path $statsFile -Raw | ConvertFrom-Json
            # ConvertFrom-Json gives a PSCustomObject -- copy into a hashtable we can
            # add/overwrite keys on freely (PSCustomObject properties are awkward to
            # upsert dynamically by a variable key name).
            foreach ($prop in $raw.PSObject.Properties) { $stats[$prop.Name] = $prop.Value }
        } catch {
            Log "watchdog-stats.json unreadable, starting fresh: $_"
        }
    }

    $existing = $stats[$name]
    $restarts = if ($existing) { $existing.restarts } else { 0 }
    $lastRestart = if ($existing) { $existing.last_restart } else { $null }
    if ($restarted) {
        $restarts += 1
        $lastRestart = Format-Rfc3339 (Get-Date)
    }

    $stats[$name] = @{
        restarts     = $restarts
        last_restart = $lastRestart
        last_check   = Format-Rfc3339 (Get-Date)
        status       = $status
    }

    ($stats | ConvertTo-Json -Depth 5) | Set-Content -Path $statsFile -Encoding UTF8
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
    Update-WatchdogStat 'docker' $false 'ok'
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
        Update-WatchdogStat 'docker' $true 'ok'
    } else {
        Log 'Docker still down after 90s -- giving up this cycle, will retry next run'
        Update-WatchdogStat 'docker' $true 'down'
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
    foreach ($name in $knownApps) { Update-WatchdogStat $name $true 'pm2 down' }
    & $pm2 resurrect *> $null
} else {
    foreach ($name in $knownApps) {
        $row = $list | Select-String -SimpleMatch $name
        if (-not $row) {
            Log "pm2 app '$name' not found in pm2 list -- attempting resurrect"
            Update-WatchdogStat $name $true 'not found'
            & $pm2 resurrect *> $null
        } elseif ($row -notmatch 'online') {
            Log "pm2 app '$name' is not online -- restarting"
            Update-WatchdogStat $name $true 'restarted'
            & $pm2 restart $name *> $null
        } else {
            Update-WatchdogStat $name $false 'online'
        }
    }
}
