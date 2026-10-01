# Linux desktop installers (.deb, .rpm, .AppImage) from a Windows box, built
# in Docker (Docker Desktop, Linux containers). Nothing is installed on the
# host or in WSL. Output: <repo>\dist\desktop-linux\ plus SHA256SUMS.
#
#   cd clients\desktop\linux-build
#   .\build.ps1                   # builds web\dist, then the installers
#   .\build.ps1 -SkipWeb          # reuse the existing web\dist as-is
#   .\build.ps1 -Version 2.5.1    # stamp X.Y.Z instead of tauri.conf.json's
#                                 # version, as CI does from a v* tag
#
# The first run builds the image (~5 min: apt + rustup + tauri-cli) and a
# cold cargo build; the onscreen-desktop-target / onscreen-desktop-cargo
# volumes keep later runs incremental. `docker volume rm` them to start over.
param(
    [string]$Out,
    [switch]$SkipWeb,
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version
)

# No $ErrorActionPreference = "Stop": Windows PowerShell 5.1 turns a native
# command's stderr (docker and cargo log progress there) into a terminating
# error once the output is redirected. Each native call checks
# $LASTEXITCODE instead.
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..\..") -ErrorAction Stop).Path
if (-not $Out) { $Out = Join-Path $repoRoot "dist\desktop-linux" }
New-Item -ItemType Directory -Force $Out -ErrorAction Stop | Out-Null
$Out = (Resolve-Path $Out -ErrorAction Stop).Path

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "==> docker not found. Install Docker Desktop (Linux containers) first." -ForegroundColor Red
    exit 1
}

if (-not $SkipWeb) {
    Write-Host "==> Building SvelteKit frontend..." -ForegroundColor Cyan
    Push-Location (Join-Path $repoRoot "web")
    try {
        npm ci --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    } finally { Pop-Location }
}

Write-Host "==> Building the onscreen-desktop-linux image..." -ForegroundColor Cyan
docker build -t onscreen-desktop-linux $PSScriptRoot
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

Write-Host "==> Building the installers in the container..." -ForegroundColor Cyan
$runArgs = @(
    "run", "--rm",
    "-v", "${repoRoot}:/src:ro",
    "-v", "${Out}:/out",
    "-v", "onscreen-desktop-target:/target",
    "-v", "onscreen-desktop-cargo:/usr/local/cargo/registry"
)
if ($Version) { $runArgs += @("-e", "DESKTOP_VERSION=$Version") }
docker @runArgs onscreen-desktop-linux
if ($LASTEXITCODE -ne 0) { throw "container build failed" }

Write-Host ""
Write-Host "==> Done. Installers under:" -ForegroundColor Green
Write-Host "    $Out"
