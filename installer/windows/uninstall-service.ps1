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

# WinSW pairs with onscreen.exe (a renamed copy made at install time), not the
# bare WinSW.exe + path. Fall back to WinSW.exe if the renamed copy is absent.
$svcExe = Join-Path $PSScriptRoot "onscreen.exe"
if (-not (Test-Path $svcExe)) { $svcExe = Join-Path $PSScriptRoot "WinSW.exe" }
if (-not (Test-Path $svcExe)) { throw "WinSW.exe missing." }

# Stop first; ignore errors if it wasn't running.
Write-Host "==> Stopping service (best-effort)..." -ForegroundColor Cyan
& $svcExe stop 2>&1 | Out-Host

Write-Host "==> Unregistering service..." -ForegroundColor Cyan
& $svcExe uninstall
if ($LASTEXITCODE -ne 0) { throw "WinSW uninstall failed (exit $LASTEXITCODE)" }

# Remove the LAN firewall rule install-service.ps1 added (no-op if absent).
Get-NetFirewallRule -DisplayName 'OnScreen Server (LAN)' -ErrorAction SilentlyContinue |
    Remove-NetFirewallRule -ErrorAction SilentlyContinue

Write-Host
Write-Host "==> Done." -ForegroundColor Green
