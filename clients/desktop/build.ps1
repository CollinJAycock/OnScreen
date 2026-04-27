# Desktop client release builder (Windows). Mirrors `make client-build`
# for boxes without GNU Make installed. Produces the .msi + .exe
# installers in src-tauri\target\release\bundle\.
#
# First run after a clean checkout downloads ~300+ crates and takes
# 5-10 minutes; subsequent builds with a warm cache land in 30-90s.
#
#   cd clients\desktop
#   .\build.ps1

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$repoRoot = Resolve-Path "..\.."
$webDir = Join-Path $repoRoot "web"
$tauriDir = Join-Path $PSScriptRoot "src-tauri"

if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) {
    Write-Host "==> cargo not found. Install Rust from https://rustup.rs first." -ForegroundColor Red
    exit 1
}
if (-not (cargo tauri --version 2>$null)) {
    Write-Host "==> Tauri CLI not installed. Run: cargo install tauri-cli --locked --version `"^2.0`"" -ForegroundColor Yellow
    exit 1
}

Write-Host "==> Building SvelteKit frontend..." -ForegroundColor Cyan
Push-Location $webDir
try {
    npm install
    npm run build
} finally { Pop-Location }

Write-Host "==> Building Tauri bundle (this is the slow part)..." -ForegroundColor Cyan
Set-Location $tauriDir
cargo tauri build

Write-Host ""
Write-Host "==> Done. Installers under:" -ForegroundColor Green
Write-Host "    $tauriDir\target\release\bundle\msi\"
Write-Host "    $tauriDir\target\release\bundle\nsis\"
