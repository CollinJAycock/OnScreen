# Uninstall the OnScreen Windows Service.
#
# Stops the service if running, then unregisters it. Files in this
# directory (binaries, logs, .env) are left intact — re-running
# install-service.ps1 will pick them up again.

[CmdletBinding()]
param()

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$identity  = [System.Security.Principal.WindowsIdentity]::GetCurrent()
$principal = New-Object System.Security.Principal.WindowsPrincipal($identity)
if (-not $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "==> Re-launching elevated (UAC prompt)..." -ForegroundColor Yellow
    $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $MyInvocation.MyCommand.Path)
    Start-Process -FilePath "powershell.exe" -ArgumentList $argList -Verb RunAs -Wait
    return
}

$xmlPath = Join-Path $PSScriptRoot "onscreen.xml"
$winsw   = Join-Path $PSScriptRoot "WinSW.exe"
if (-not (Test-Path $winsw)) { throw "WinSW.exe missing." }
if (-not (Test-Path $xmlPath)) { throw "onscreen.xml missing." }

# Stop first; ignore errors if it wasn't running.
Write-Host "==> Stopping service (best-effort)..." -ForegroundColor Cyan
& $winsw stop $xmlPath 2>&1 | Out-Host

Write-Host "==> Unregistering service..." -ForegroundColor Cyan
& $winsw uninstall $xmlPath
if ($LASTEXITCODE -ne 0) { throw "WinSW uninstall failed (exit $LASTEXITCODE)" }

Write-Host
Write-Host "==> Done." -ForegroundColor Green
