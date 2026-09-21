# Local verification helper: frontend typecheck + lint + build, then Go full-chain checks.
# Usage: powershell -File scripts\verify.ps1 [-FrontendOnly]
param([switch]$FrontendOnly)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

Write-Host "==> frontend typecheck"
Set-Location (Join-Path $root "cmd\streamguard-gui\frontend")
node ".\node_modules\typescript\bin\tsc" --noEmit -p .
if ($LASTEXITCODE -ne 0) { throw "typecheck failed" }

Write-Host "==> frontend lint"
node ".\node_modules\eslint\bin\eslint.js" src/
if ($LASTEXITCODE -ne 0) { throw "lint failed" }

Write-Host "==> frontend build (vite)"
node ".\node_modules\vite\bin\vite.js" build
if ($LASTEXITCODE -ne 0) { throw "vite build failed" }

if ($FrontendOnly) { Set-Location $root; Write-Host "OK (frontend only)"; exit 0 }

Write-Host "==> go test"
Set-Location $root
go test ./... -count=1
if ($LASTEXITCODE -ne 0) { throw "go test failed" }

Write-Host "==> go vet"
go vet ./...
if ($LASTEXITCODE -ne 0) { throw "go vet failed" }

Write-Host "==> gofmt"
$bad = gofmt -l .
if ($bad) { throw "gofmt dirty: $bad" }

Write-Host "ALL OK"
