param([string]$Output = 'bin/papergo.exe')
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $taskRoot
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
$taskOutputDirectory = Split-Path -Parent $Output
if ($taskOutputDirectory) { New-Item -ItemType Directory -Force -Path $taskOutputDirectory | Out-Null }
$env:CGO_ENABLED = '0'
& $taskGo build -trimpath '-ldflags=-s -w' -o $Output ./cmd/api
exit $LASTEXITCODE
