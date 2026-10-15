# One-command Windows entry point; no app/browser/login is launched.
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path -Parent $PSScriptRoot)
# A failed dependency installation must not leave an old PASS available.
$Report = Join-Path (Get-Location) 'verification-output/report.json'
if (Test-Path -LiteralPath $Report) { Remove-Item -LiteralPath $Report -Force }
if (-not (Get-Command node -ErrorAction SilentlyContinue)) { throw 'Install Node.js 22 LTS first.' }
if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { throw 'npm is required.' }
& npm ci --ignore-scripts --no-audit --no-fund
if ($LASTEXITCODE -ne 0) { throw 'Verification dependencies could not be installed.' }
& node scripts/verify.cjs
exit $LASTEXITCODE
