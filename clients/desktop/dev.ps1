# Desktop client dev launcher (Windows). Mirrors `make client-dev`
# for boxes without GNU Make installed — runs Vite in the background
# at :5173 and starts `cargo tauri dev` so the webview points at it.
# Tauri's tauri.conf.json already declares devUrl = http://localhost:5173,
# so once Vite is up the webview hot-reloads on every Svelte change.
#
# First run: ensure Tauri CLI is installed (`cargo install tauri-cli
# --locked --version "^2.0"`) and that web/node_modules is populated
# (`npm --prefix ..\..\web install`). After that, just:
#
#   cd clients\desktop
#   .\dev.ps1
#
# Ctrl+C in the terminal stops Tauri; the trap cleans up the Vite
# child process so the dev server doesn't linger past exit.

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

Write-Host "==> Starting Vite dev server in background..." -ForegroundColor Cyan
$vite = Start-Process -FilePath "npm" `
    -ArgumentList "run", "dev" `
    -WorkingDirectory $webDir `
    -PassThru -NoNewWindow

# Trap exit so we don't leave Vite orphaned when the user Ctrl+Cs
# tauri or it crashes mid-build. Catch every exit path including
# script-level exceptions.
try {
    Write-Host "==> Waiting for Vite to come up at http://localhost:5173..." -ForegroundColor Cyan
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-Date) -lt $deadline) {
        try {
            $r = Invoke-WebRequest -Uri "http://localhost:5173" -TimeoutSec 1 -UseBasicParsing
            if ($r.StatusCode -eq 200) { break }
        } catch { Start-Sleep -Milliseconds 500 }
    }

    Write-Host "==> Launching Tauri dev shell..." -ForegroundColor Cyan
    Set-Location $tauriDir
    cargo tauri dev
} finally {
    if ($vite -and -not $vite.HasExited) {
        Write-Host "==> Stopping Vite (PID $($vite.Id))..." -ForegroundColor DarkGray
        Stop-Process -Id $vite.Id -Force -ErrorAction SilentlyContinue
        # Vite forks a node child — sweep any node processes spawned
        # by our PID so the port doesn't stay bound.
        Get-CimInstance Win32_Process -Filter "ParentProcessId = $($vite.Id)" `
            -ErrorAction SilentlyContinue | ForEach-Object {
                Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
            }
    }
}
