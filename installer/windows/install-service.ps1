# Install OnScreen as a Windows Service via WinSW.
#
# Steps:
#   1. Reads .env in this directory and rewrites onscreen.xml's <env/> block
#      so the service inherits DATABASE_URL, VALKEY_URL, SECRET_KEY, etc.
#      (Without this the service runs with the SYSTEM account's empty env
#      and immediately panics on missing config.)
#   2. Runs `WinSW.exe install onscreen.xml` to register the service.
#   3. Runs `WinSW.exe start onscreen.xml` to bring it up.
#
# Requires elevation (Run as Administrator) — sc.exe needs admin to register
# services. If launched non-elevated, this script self-elevates.

[CmdletBinding()]
param(
    [switch]$NoStart = $false
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# Self-elevate if we're not running as admin.
$identity  = [System.Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object System.Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "==> Re-launching elevated (UAC prompt)..." -ForegroundColor Yellow
    $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $MyInvocation.MyCommand.Path)
    if ($NoStart) { $argList += "-NoStart" }
    Start-Process -FilePath "powershell.exe" -ArgumentList $argList -Verb RunAs -Wait
    return
}

$envPath = Join-Path $PSScriptRoot ".env"
if (-not (Test-Path $envPath)) {
    Write-Host "ERROR: .env not found. Copy .env.example to .env and fill in DATABASE_URL + VALKEY_URL + SECRET_KEY first." -ForegroundColor Red
    exit 1
}

$xmlPath = Join-Path $PSScriptRoot "onscreen.xml"
$winsw   = Join-Path $PSScriptRoot "WinSW.exe"
if (-not (Test-Path $winsw)) { throw "WinSW.exe missing — re-extract the zip." }
if (-not (Test-Path $xmlPath)) { throw "onscreen.xml missing — re-extract the zip." }

# Build <env/> lines from .env so the service sees the same config the
# foreground launcher does. Skip blank lines / comments.
$envLines = @()
$envLines += '  <env name="PATH" value="%BASE%\ffmpeg;%PATH%"/>'
Get-Content $envPath | ForEach-Object {
    if ($_ -match '^\s*(?:export\s+)?(\w+)\s*=\s*["'']?(.*?)["'']?\s*$') {
        $key = $Matches[1]
        $val = $Matches[2]
        if ($key -and $key -notmatch '^\s*#' -and $key -ne "PATH") {
            # Escape XML-significant chars in the value.
            $val = $val -replace '&','&amp;' -replace '<','&lt;' -replace '>','&gt;' -replace '"','&quot;'
            $envLines += "  <env name=`"$key`" value=`"$val`"/>"
        }
    }
}

$xml = Get-Content $xmlPath -Raw
# Replace ALL existing <env .../> tags (and the surrounding whitespace) with
# the freshly-built block. The template ships with one PATH entry; this
# regex eats it plus any whitespace before the closing </service>.
$xml = $xml -replace '(?ms)\s*<env\s[^/]*/>', ''
$envBlock = ($envLines -join "`n")
$xml = $xml -replace '</service>', "`n$envBlock`n</service>"
Set-Content -Path $xmlPath -Value $xml -Encoding utf8

Write-Host "==> Registering OnScreen service..." -ForegroundColor Cyan
& $winsw install $xmlPath
if ($LASTEXITCODE -ne 0) { throw "WinSW install failed (exit $LASTEXITCODE)" }

if (-not $NoStart) {
    Write-Host "==> Starting OnScreen service..." -ForegroundColor Cyan
    & $winsw start $xmlPath
    if ($LASTEXITCODE -ne 0) { throw "WinSW start failed (exit $LASTEXITCODE)" }
}

Write-Host
Write-Host "==> Done." -ForegroundColor Green
Write-Host "    Manage with: services.msc, or `Restart-Service OnScreen` / `Stop-Service OnScreen`."
Write-Host "    Logs:        $PSScriptRoot\logs\"
Write-Host "    Web UI:      http://localhost:7070"
