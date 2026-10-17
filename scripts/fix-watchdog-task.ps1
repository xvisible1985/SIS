# fix-watchdog-task.ps1 -- run once, from an elevated PowerShell, to switch the
# SIS-Service-Watchdog scheduled task's action from calling powershell.exe directly to
# calling it through run-watchdog-hidden.vbs instead.
#
# Why: powershell.exe is a console-subsystem binary -- Windows briefly allocates (and
# shows) a console window for it at process-creation time, before any "hidden" flag
# takes effect. wscript.exe is a GUI-subsystem binary and never allocates a console at
# all, for itself or for whatever it launches via .Run(..., 0, True) -- this is the
# standard, reliable fix for that exact Windows quirk. Needs an elevated shell because
# this task was originally registered with elevated rights.

$action = New-ScheduledTaskAction -Execute 'wscript.exe' -Argument '"C:\Users\123\Projects\sis\scripts\run-watchdog-hidden.vbs"'
Set-ScheduledTask -TaskName 'SIS-Service-Watchdog' -Action $action

Write-Host '--- Verify ---'
(Get-ScheduledTask -TaskName 'SIS-Service-Watchdog').Actions | Format-List *
