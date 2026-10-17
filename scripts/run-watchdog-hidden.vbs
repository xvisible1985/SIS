' run-watchdog-hidden.vbs -- launches service-watchdog.ps1 with ZERO visible window.
'
' Why this exists: the scheduled task used to call powershell.exe directly (even with
' Hidden=True + S4U logon + -WindowStyle Hidden). That still let a brief console flash
' through -- confirmed live (2026-10-16), timed to the exact minute of a scheduled run,
' even after service-watchdog.ps1 itself was fixed to launch docker/pm2 with
' CreateNoWindow (see Invoke-HiddenProcess in that script). powershell.exe is a CONSOLE
' subsystem binary: Windows briefly allocates a console for it on process creation
' before SW_HIDE takes effect, no matter how it's told to hide afterwards.
'
' wscript.exe is a GUI subsystem binary -- it never allocates a console at all, for
' itself or (via .Run with window style 0) for whatever it launches. This is the
' standard, reliable fix for this exact well-known Windows quirk.
Set objShell = CreateObject("WScript.Shell")
objShell.Run "powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File ""C:\Users\123\Projects\sis\scripts\service-watchdog.ps1""", 0, True
