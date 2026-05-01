# Foreground launcher for OnScreen on Windows.
#
# Use this for interactive testing and dev / smoke runs. For production
# deployment, register as a Windows Service via install-service.ps1.
#
# Loads `.env` (Bash export-style) into the current process, prepends the
# bundled ffmpeg dir to PATH, and runs server.exe in the foreground.
# Press Ctrl+C to stop — the Go signal handler does a graceful pool/db
# shutdown.

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$envPath = Join-Path $PSScriptRoot ".env"
if (-not (Test-Path $envPath)) {
    Write-Host "ERROR: .env not found. Copy .env.example to .env and fill in DATABASE_URL + VALKEY_URL + SECRET_KEY first." -ForegroundColor Red
    exit 1
}

# Parse Bash export-style lines: `export KEY="value"` or `KEY=value`.
# Survives quoted values with spaces, ignores comments and blank lines.
Get-Content $envPath | ForEach-Object {
    if ($_ -match '^\s*(?:export\s+)?(\w+)\s*=\s*["'']?(.*?)["'']?\s*$') {
        if ($Matches[1] -and $Matches[1] -notmatch '^\s*#') {
            [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2], "Process")
        }
    }
}

# Prepend bundled ffmpeg dir so the server's encoder probe finds it
# without requiring a separate TOOLS_PATH config.
$bundledFfmpeg = Join-Path $PSScriptRoot "ffmpeg"
if (Test-Path $bundledFfmpeg) {
    $env:Path = "$bundledFfmpeg;$env:Path"
}
# Honour TOOLS_PATH from .env (semicolon-separated extras) if set.
if ($env:TOOLS_PATH) {
    $env:Path = "$env:TOOLS_PATH;$env:Path"
}

Write-Host "==> OnScreen server starting on http://localhost:7070" -ForegroundColor Cyan
Write-Host "    Ctrl+C to stop (graceful)." -ForegroundColor Gray
Write-Host

& (Join-Path $PSScriptRoot "server.exe")
