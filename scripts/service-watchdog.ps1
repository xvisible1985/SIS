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

    # Set-Content -Encoding UTF8 writes a BOM on Windows PowerShell 5.1, which Go's
    # encoding/json does not skip -- json.Unmarshal would silently fail on it and the
    # API would keep returning an empty map. Write via .NET directly with no BOM.
    $json = $stats | ConvertTo-Json -Depth 5
    [System.IO.File]::WriteAllText($statsFile, $json, (New-Object System.Text.UTF8Encoding $false))
}

function Invoke-HiddenProcess($exe, $arguments) {
    # Plain PowerShell `& $exe args` (or Start-Process -WindowStyle Hidden) does NOT
    # reliably suppress the console flash for a .cmd target like pm2.cmd: pm2.cmd is
    # itself a cmd.exe batch wrapper, and this task's own process has no console at all
    # (S4U logon, non-interactive) -- when it spawns that nested cmd.exe, Windows
    # allocates a BRAND NEW, briefly-visible console for it, because SW_HIDE (what
    # -WindowStyle Hidden sets) only hides a window AFTER creation, it doesn't stop one
    # from being allocated. CreateNoWindow (only reachable via System.Diagnostics.Process
    # directly, not Start-Process) sets the real CREATE_NO_WINDOW flag, which prevents
    # the console from being allocated in the first place -- this is the actual fix for
    # the flash observed live (2026-10-16) even after the task's own LogonType was
    # already S4U and its own Settings.Hidden was already True.
    # Returns an object with both Output and ExitCode -- $LASTEXITCODE (PowerShell's
    # usual way to check a native command's exit code) is only ever set by PowerShell's
    # OWN invocation machinery (`&`, direct calls), never by a process started directly
    # via System.Diagnostics.Process, so callers must use the returned ExitCode instead.
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = $arguments
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $proc = [System.Diagnostics.Process]::Start($psi)
    $stdout = $proc.StandardOutput.ReadToEnd()
    $proc.WaitForExit()
    return [PSCustomObject]@{ Output = $stdout; ExitCode = $proc.ExitCode }
}

function Test-DockerUp {
    try {
        # Plain `docker ps` has the exact same console-flash problem Invoke-HiddenProcess
        # exists for (see its own comment) -- docker.exe is a console app and this run's
        # own host process has no console, so Windows allocates a fresh, briefly-visible
        # one for it every single cycle (this check runs unconditionally every 5 min,
        # unlike the pm2 restart/resurrect calls which only fire on an actual problem).
        $result = Invoke-HiddenProcess 'docker' 'ps'
        return $result.ExitCode -eq 0
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
$list = (Invoke-HiddenProcess $pm2 'list --no-color').Output

if (-not $list) {
    Log 'pm2 not responding -- attempting resurrect'
    foreach ($name in $knownApps) { Update-WatchdogStat $name $true 'pm2 down' }
    Invoke-HiddenProcess $pm2 'resurrect' | Out-Null
} else {
    foreach ($name in $knownApps) {
        $row = $list -split "`n" | Select-String -SimpleMatch $name
        if (-not $row) {
            Log "pm2 app '$name' not found in pm2 list -- attempting resurrect"
            Update-WatchdogStat $name $true 'not found'
            Invoke-HiddenProcess $pm2 'resurrect' | Out-Null
        } elseif ($row -notmatch 'online') {
            Log "pm2 app '$name' is not online -- restarting"
            Update-WatchdogStat $name $true 'restarted'
            Invoke-HiddenProcess $pm2 "restart $name" | Out-Null
        } else {
            Update-WatchdogStat $name $false 'online'
        }
    }
}
