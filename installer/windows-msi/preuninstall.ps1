# Pre-uninstall: stop and unregister the three OnScreen Windows
# Services. Called from Inno Setup before files are deleted — if we
# leave services running, the uninstaller can't delete locked binaries.
#
# Data preservation is handled separately (Inno Setup wizard asks the
# user; we don't touch %ProgramData%\OnScreen here).

[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$InstallDir
)

$ErrorActionPreference = "Continue"

function Stop-And-Unregister {
    param([string]$XmlName)
    # Match the per-service exe naming Register-WinswService uses
    # (service-worker.exe <-> service-worker.xml); WinSW finds its config by the
    # exe's own base name, so stop/uninstall must go through that same exe.
    $base = [System.IO.Path]::GetFileNameWithoutExtension($XmlName)
    $svcExe = "$InstallDir\$base.exe"
    if (-not (Test-Path $svcExe)) { return }
    Write-Host "Stopping $XmlName..."
    & $svcExe stop 2>&1 | Out-Host
    Write-Host "Unregistering $XmlName..."
    & $svcExe uninstall 2>&1 | Out-Host
}

# Tear down in reverse-dependency order: OnScreen first (depends on
# the others), then Redis + Postgres.
Stop-And-Unregister "service-onscreen.xml"
Stop-And-Unregister "service-worker.xml"
Stop-And-Unregister "service-redis.xml"
Stop-And-Unregister "service-postgres.xml"

# Remove the firewall rules our installer added (no-op if absent):
#   'OnScreen Worker (segments)' — worker-only installs (the WORKER_ADDR port)
#   'OnScreen Server (LAN)'      — full installs (the API/UI port 7070)
foreach ($rule in @('OnScreen Worker (segments)', 'OnScreen Server (LAN)')) {
    Get-NetFirewallRule -DisplayName $rule -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule -ErrorAction SilentlyContinue
}

# Brief wait so SCM finalizes deregistration before Inno Setup tries
# to delete the files. Without this, file-lock errors are common.
Start-Sleep -Seconds 2
