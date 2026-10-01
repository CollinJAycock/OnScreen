# Desktop client release builder (Windows). Mirrors `make client-build`
# for boxes without GNU Make installed. Produces the .msi + .exe
# installers in src-tauri\target\release\bundle\.
#
# First run after a clean checkout downloads ~300+ crates and takes
# 5-10 minutes; subsequent builds with a warm cache land in 30-90s.
#
#   cd clients\desktop
#   .\build.ps1
#   .\build.ps1 -AnyTauriCli   # build with whatever tauri-cli is installed
param(
    [switch]$AnyTauriCli
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# The CLI carries the bundler, so it decides what goes into the installers.
# Same pin as .github/workflows/desktop-client.yml (TAURI_CLI_VERSION), the
# Makefile and linux-build/Dockerfile; change them together.
$TauriCliVersion = "2.12.1"
$installCli = "cargo install tauri-cli --locked --version $TauriCliVersion"

$repoRoot = Resolve-Path "..\.."
$webDir = Join-Path $repoRoot "web"
$tauriDir = Join-Path $PSScriptRoot "src-tauri"

if (-not (Get-Command cargo -ErrorAction SilentlyContinue)) {
    Write-Host "==> cargo not found. Install Rust from https://rustup.rs first." -ForegroundColor Red
    exit 1
}
# "tauri-cli X.Y.Z". try/catch: with -ErrorAction Stop, Windows PowerShell
# 5.1 turns cargo's "no such command" (stderr) into a terminating error.
$cliOutput = $null
try { $cliOutput = "$(cargo tauri --version 2>$null)".Trim() } catch { }
if (-not $cliOutput) {
    Write-Host "==> Tauri CLI not installed. Run: $installCli (the version CI pins)" -ForegroundColor Yellow
    exit 1
}
if ($cliOutput -ne "tauri-cli $TauriCliVersion") {
    if ($AnyTauriCli) {
        Write-Host "==> WARNING: building with $cliOutput, not tauri-cli $TauriCliVersion (the version CI pins); these installers won't match CI's." -ForegroundColor Yellow
    } else {
        Write-Host "==> Found $cliOutput, but CI builds the installers with tauri-cli $TauriCliVersion." -ForegroundColor Red
        Write-Host "    The CLI holds the bundler, so a different one builds different installers." -ForegroundColor Red
        Write-Host "    Install the pinned one: $installCli" -ForegroundColor Red
        Write-Host "    or build with this one anyway: .\build.ps1 -AnyTauriCli" -ForegroundColor Red
        exit 1
    }
}

Write-Host "==> Building SvelteKit frontend..." -ForegroundColor Cyan
Push-Location $webDir
try {
    npm install
    npm run build
} finally { Pop-Location }

Write-Host "==> Building Tauri bundle (this is the slow part)..." -ForegroundColor Cyan
Set-Location $tauriDir
cargo tauri build -- --locked   # the committed Cargo.lock, as CI builds it

Write-Host ""
Write-Host "==> Done. Installers under:" -ForegroundColor Green
Write-Host "    $tauriDir\target\release\bundle\msi\"
Write-Host "    $tauriDir\target\release\bundle\nsis\"
