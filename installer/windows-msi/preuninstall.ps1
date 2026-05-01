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
    $xmlFull = "$InstallDir\$XmlName"
    if (-not (Test-Path $xmlFull)) { return }
    Write-Host "Stopping $XmlName..."
    & "$InstallDir\WinSW.exe" stop $xmlFull 2>&1 | Out-Host
    Write-Host "Unregistering $XmlName..."
    & "$InstallDir\WinSW.exe" uninstall $xmlFull 2>&1 | Out-Host
}

# Tear down in reverse-dependency order: OnScreen first (depends on
# the others), then Redis + Postgres.
Stop-And-Unregister "service-onscreen.xml"
Stop-And-Unregister "service-redis.xml"
Stop-And-Unregister "service-postgres.xml"

# Brief wait so SCM finalizes deregistration before Inno Setup tries
# to delete the files. Without this, file-lock errors are common.
Start-Sleep -Seconds 2
