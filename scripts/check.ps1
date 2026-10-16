$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath (Split-Path -Parent $PSScriptRoot)
& python scripts/check.py @args
exit $LASTEXITCODE
