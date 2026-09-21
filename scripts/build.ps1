# StreamGuard build script
#
# Usage:
#   .\scripts\build.ps1                    # Build CLI + GUI (current OS)
#   .\scripts\build.ps1 -Version "1.0.0"   # Specify version
#   .\scripts\build.ps1 -Target cli        # Build CLI only
#   .\scripts\build.ps1 -Target gui        # Build GUI only
#   .\scripts\build.ps1 -OS darwin         # Cross-compile CLI for macOS
#   .\scripts\build.ps1 -OS linux          # Cross-compile CLI for Linux
#   .\scripts\build.ps1 -SkipTest          # Skip tests (fast build)
#
# Notes:
#   - GUI (Wails) can only be built for the CURRENT OS (Wails limitation).
#   - CLI can be cross-compiled to any GOOS via -OS (windows/darwin/linux).
#
# Artifact convention:
#   All executables are output to the REPO ROOT for convenience:
#     .\streamguard.exe          (CLI, windows)
#     .\streamguard              (CLI, darwin/linux)
#     .\streamguard-gui.exe      (GUI, windows)
#   They are git-ignored via .gitignore -- never commit binaries.

param(
    [string]$Version = "dev",
    [ValidateSet("all", "cli", "gui")]
    [string]$Target = "all",
    [ValidateSet("windows", "darwin", "linux", "")]
    [string]$OS = "",
    [switch]$SkipTest
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

# Resolve git commit hash for version stamping (best-effort; empty if unavailable).
$Commit = "unknown"
try {
    $c = git rev-parse --short HEAD 2>$null
    if ($LASTEXITCODE -eq 0 -and $c) { $Commit = $c.Trim() }
} catch { }

Write-Host "=== StreamGuard Build ===" -ForegroundColor Cyan
Write-Host "Version: $Version"
Write-Host "Commit : $Commit"
Write-Host "Target : $Target"
if ($OS) { Write-Host "OS     : $OS (CLI cross-compile)" }
Write-Host "Output : $Root (repo root)"

# 1. Run tests
if (-not $SkipTest) {
    Write-Host "`n[1/4] Running tests..." -ForegroundColor Yellow
    go test ./... -count=1
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Tests failed, aborting build" -ForegroundColor Red
        exit 1
    }

    Write-Host "`n[2/4] Running go vet..." -ForegroundColor Yellow
    go vet ./...
    if ($LASTEXITCODE -ne 0) {
        Write-Host "go vet failed, aborting build" -ForegroundColor Red
        exit 1
    }
} else {
    Write-Host "`n[1/4] Skipping tests" -ForegroundColor DarkGray
    Write-Host "[2/4] Skipping go vet" -ForegroundColor DarkGray
}

# 3. Build CLI
if ($Target -eq "all" -or $Target -eq "cli") {
    Write-Host "`n[3/4] Building CLI..." -ForegroundColor Yellow

    # Target OS: -OS param wins; otherwise current OS (default windows).
    # GOOS/GOARCH are set per-invocation via `go env -w`-free scoped env:
    # we restore them right after the build so later steps (tests) are unaffected.
    $goos = $OS
    if (-not $goos) { $goos = "windows" }
    $prevGOOS = $env:GOOS
    $prevGOARCH = $env:GOARCH
    $env:GOOS = $goos
    $env:GOARCH = "amd64"

    $cliName = if ($goos -eq "windows") { "streamguard.exe" } else { "streamguard" }
    $cliOut = Join-Path $Root $cliName
    go build -trimpath -ldflags "-s -w -X main.version=$Version -X main.commit=$Commit" -o $cliOut ./cmd/streamguard
    $buildExit = $LASTEXITCODE
    # Restore env immediately so subsequent go test/vet target the host platform.
    $env:GOOS = $prevGOOS
    $env:GOARCH = $prevGOARCH
    if ($buildExit -ne 0) {
        Write-Host "CLI build failed" -ForegroundColor Red
        exit 1
    }
    $size = [math]::Round((Get-Item $cliOut).Length / 1MB, 2)
    Write-Host "  -> $cliName ($size MB)" -ForegroundColor Green
} else {
    Write-Host "`n[3/4] Skipping CLI" -ForegroundColor DarkGray
}

# 4. Build GUI (Wails)
if ($Target -eq "all" -or $Target -eq "gui") {
    if ($OS -and $OS -ne "windows") {
        # Wails GUI can only target the current OS; skip with a hint.
        Write-Host "`n[4/4] Skipping GUI (GUI build only supports current OS; use -Target cli for cross-compile)" -ForegroundColor DarkGray
    } else {
        Write-Host "`n[4/4] Building GUI (Wails)..." -ForegroundColor Yellow

        $guiDir = Join-Path $Root "cmd\streamguard-gui"
        Push-Location $guiDir
        try {
            # Wails outputs to build/bin by default; copy to repo root afterwards.
            wails build -platform windows/amd64 -skipbindings -ldflags "-X main.version=$Version -X main.commit=$Commit"
            if ($LASTEXITCODE -ne 0) {
                Write-Host "GUI build failed" -ForegroundColor Red
                exit 1
            }

            $guiSrc = Join-Path $guiDir "build\bin\streamguard-gui.exe"
            $guiOut = Join-Path $Root "streamguard-gui.exe"
            Copy-Item $guiSrc $guiOut -Force
            $size = [math]::Round((Get-Item $guiOut).Length / 1MB, 2)
            Write-Host "  -> streamguard-gui.exe ($size MB)" -ForegroundColor Green
        } finally {
            Pop-Location
        }
    }
} else {
    Write-Host "`n[4/4] Skipping GUI" -ForegroundColor DarkGray
}

Write-Host "`n=== Build Complete ===" -ForegroundColor Cyan
Get-ChildItem $Root -Filter *.exe | Select-Object Name, @{N='Size(MB)';E={ [math]::Round($_.Length/1MB, 2) }} | Format-Table -AutoSize

Write-Host "Run CLI: .\streamguard.exe -config config.local.json" -ForegroundColor Green
Write-Host "Run GUI: .\streamguard-gui.exe" -ForegroundColor Green
