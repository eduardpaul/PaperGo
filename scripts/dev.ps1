param([string]$Token, [string]$Subject = 'local-admin')
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $taskRoot
$taskGo = Join-Path $taskRoot '.tools/go/bin/go.exe'
if (Test-Path -LiteralPath $taskGo) {
    $env:PATH = (Split-Path -Parent $taskGo) + ';' + $env:PATH
    $env:GOCACHE = Join-Path $taskRoot '.cache/build'
    $env:GOMODCACHE = Join-Path $taskRoot '.cache/mod'
    $env:GOPATH = Join-Path $taskRoot '.cache/gopath'
} else {
    $taskGo = (Get-Command go -ErrorAction Stop).Source
}
if (-not (Test-Path -LiteralPath (Join-Path $taskRoot 'web/node_modules'))) {
    & npm ci --prefix web
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}
& npm --prefix web run build
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
if ([string]::IsNullOrWhiteSpace($Token)) {
    $Token = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLowerInvariant()
    Write-Host ('Development bearer token: ' + $Token)
}
$env:APP_ENV = 'development'
$env:AUTH_MODE = 'development'
$env:DEV_TOKEN = $Token
$env:DEV_SUBJECT = $Subject
& $taskGo run ./cmd/api
exit $LASTEXITCODE
