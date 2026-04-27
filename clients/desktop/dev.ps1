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

# Resolve npm to the .cmd shipped alongside node.exe — Start-Process
# can't dispatch the bare "npm" alias on Windows because it's a
# PowerShell wrapper, not an executable. Going through node.exe's
# install dir means we don't depend on whichever npm shim PowerShell
# happens to resolve.
$node = Get-Command node -ErrorAction SilentlyContinue
if (-not $node) {
    Write-Host "==> node not found on PATH. Install Node 24+ first." -ForegroundColor Red
    exit 1
}
$npmCmd = Join-Path (Split-Path $node.Source) "npm.cmd"
if (-not (Test-Path $npmCmd)) {
    Write-Host "==> npm.cmd not found next to node.exe at $npmCmd" -ForegroundColor Red
    exit 1
}

Write-Host "==> Starting Vite dev server in background..." -ForegroundColor Cyan
$vite = Start-Process -FilePath $npmCmd `
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
        # Windows doesn't auto-kill descendants when the parent dies.
        # taskkill /T walks the whole tree (npm.cmd → node.exe → vite
        # → any worker forks) and /F is required because vite ignores
        # WM_CLOSE on the console wrapper.
        & taskkill.exe /F /T /PID $vite.Id 2>$null | Out-Null
    }
}
