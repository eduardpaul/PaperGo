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
& npm --prefix web run lint
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& npm --prefix web test -- --maxWorkers=2
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $taskGo mod verify
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $taskGo vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
& $taskGo test -count=1 ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
New-Item -ItemType Directory -Force -Path bin | Out-Null
& $taskGo build -trimpath -o bin/papergo.exe ./cmd/api
exit $LASTEXITCODE
