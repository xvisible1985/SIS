param([Parameter(ValueFromRemainingArguments = $true)][string[]] $GraphArguments)

$ErrorActionPreference = 'Stop'
$base = Join-Path $env:LOCALAPPDATA 'SIS\Graphify'
$env:PYTHONPATH = Join-Path $base 'packages-0.9.113'
$env:PYTHONUTF8 = '1'
$env:GRAPHIFY_BACKEND = 'none'
$env:GRAPHIFY_QUERY_LOG_DISABLE = '1'
$runtime = Join-Path $env:USERPROFILE '.cache\codex-runtimes\codex-primary-runtime\dependencies'
$python = Join-Path $runtime 'python\python.exe'
$git = Get-Command git.exe -ErrorAction SilentlyContinue
if ($git) { $gitExe = $git.Source }
else { $gitExe = Join-Path $runtime 'native\git\cmd\git.exe' }
if (!(Test-Path (Join-Path $env:PYTHONPATH 'graphify\__main__.py'))) {
    throw "Graphify packages missing from $env:PYTHONPATH."
}

$root = Split-Path $PSScriptRoot
$commit = & $gitExe -C $root rev-parse HEAD
if ($LASTEXITCODE -ne 0 -or $commit -notmatch '^[a-f0-9]{40}$') { throw 'Could not resolve SIS HEAD.' }
$folders = @('pkg', 'services', 'migrations', 'frontend/src', 'cmd')
$files = & $gitExe -C $root ls-tree -r --name-only $commit -- $folders
if ($LASTEXITCODE -ne 0) { throw 'Could not inspect SIS snapshot.' }
if ($files -match '(?i)(^|/)(\.env[^/]*|[^/]+\.(log|dump|backup|pem|key|pfx|sqlite|sql\.gz))$') {
    throw 'A sensitive file type is present in the selected source directories.'
}
if ($GraphArguments -and $GraphArguments[0] -notin @('query', 'path', 'explain')) {
    throw 'Supported graph commands: query, path, explain.'
}

$cache = Join-Path $base "sis-$commit"
$graph = Join-Path $cache 'graphify-out\graph.json'
$report = Join-Path $cache 'graphify-out\GRAPH_REPORT.md'
$map = Join-Path $cache 'graphify-out\graph.html'
$mutex = [System.Threading.Mutex]::new($false, "Local\SISGraphify-$commit")
$locked = $false
try {
    try { $locked = $mutex.WaitOne([TimeSpan]::FromMinutes(3)) }
    catch [System.Threading.AbandonedMutexException] { $locked = $true }
    if (!$locked) { throw 'Timed out waiting for the SIS graph.' }
    if (!(Test-Path $graph) -or !(Test-Path $report) -or !(Test-Path $map)) {
        New-Item -ItemType Directory -Force $cache | Out-Null
        $archive = Join-Path $base "sis-$commit.tar"
        & $gitExe -C $root archive --format=tar "--output=$archive" $commit -- $folders
        if ($LASTEXITCODE -ne 0) { throw 'Could not archive SIS source.' }
        & tar.exe -xf $archive -C $cache
        if ($LASTEXITCODE -ne 0) { throw 'Could not extract SIS source.' }
        Remove-Item -LiteralPath $archive
        Push-Location $cache
        try {
            & $python -m graphify extract . --code-only
            if ($LASTEXITCODE -ne 0) { throw 'Graphify extraction failed.' }
            & $python -m graphify cluster-only .
            if ($LASTEXITCODE -ne 0) { throw 'Graphify report generation failed.' }
        } finally { Pop-Location }
    }
} finally {
    if ($locked) { $mutex.ReleaseMutex() }
    $mutex.Dispose()
}

if (!$GraphArguments) {
    "Commit: $commit"
    "Graph: $graph"
    "Report: $report"
    "Map: $map"
    exit 0
}
Push-Location $cache
try {
    & $python -m graphify @GraphArguments
    exit $LASTEXITCODE
} finally { Pop-Location }
