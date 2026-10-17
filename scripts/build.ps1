param([string]$OutputDirectory = 'bin')
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $taskRoot
& python scripts/native.py
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
. (Join-Path $taskRoot '.tools/native/env.ps1')
$taskGo = Join-Path $taskRoot '.tools/go/bin/go.exe'
if (Test-Path -LiteralPath $taskGo) {
    $env:PATH = (Split-Path -Parent $taskGo) + ';' + $env:PATH
    $env:GOCACHE = Join-Path $taskRoot '.cache/build'
    $env:GOMODCACHE = Join-Path $taskRoot '.cache/mod'
    $env:GOPATH = Join-Path $taskRoot '.cache/gopath'
} else { $taskGo = (Get-Command go -ErrorAction Stop).Source }
& npm ci --prefix web
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& npm --prefix web run build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& python scripts/build-binaries.py --output-dir $OutputDirectory
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& python scripts/build-assets.py --output-dir (Join-Path $OutputDirectory 'assets')
exit $LASTEXITCODE
