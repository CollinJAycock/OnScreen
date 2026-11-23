<#
.SYNOPSIS
  Packages the Windows desktop app as a full-trust MSIX for the Microsoft Store.

.DESCRIPTION
  Builds the release exe (..\build.ps1 -NoBundle, which insists on the
  tauri-cli version CI pins), stages it with AppxManifest.xml and the Xbox
  package's logos, generates resources.pri, packs the .msix and signs it with
  the Xbox shell's local test certificate (subject = the Store publisher), so
  the same file can be installed locally for testing. The Store re-signs it.

    .\build-msix.ps1                    # -> dist\OnScreen-Desktop_<ver>_x64.msix
    .\build-msix.ps1 -SkipBuild         # reuse the last release exe
    .\build-msix.ps1 -Register          # also register the staged layout on this
                                        # PC (Developer Mode), replacing any
                                        # OnScreen.OnScreen package installed here
    .\build-msix.ps1 -Out C:\somewhere  # where the .msix goes

  Used by clients\build-packages.ps1 (target microsoft-desktop).
#>
[CmdletBinding()]
param(
    [switch]$SkipBuild,
    [switch]$Register,
    [string]$Out
)

$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$desktop = Split-Path $here -Parent
$clients = Split-Path $desktop -Parent
$tauriDir = Join-Path $desktop 'src-tauri'
$xboxShell = Join-Path $clients 'xbox\shell'
if (-not $Out) { $Out = Join-Path $here 'dist' }

function Find-SdkTool([string]$Name) {
    $bin = Join-Path ${env:ProgramFiles(x86)} 'Windows Kits\10\bin'
    $dir = Get-ChildItem $bin -Directory -Filter '10.*' -ErrorAction SilentlyContinue |
        Where-Object { Test-Path (Join-Path $_.FullName "x64\$Name") } |
        Sort-Object { [version]$_.Name } -Descending | Select-Object -First 1
    if (-not $dir) { throw "$Name not found: install the Windows 10/11 SDK (it comes with Visual Studio's UWP workload)." }
    Join-Path $dir.FullName "x64\$Name"
}

function Invoke-Tool([string]$Exe, [string[]]$ArgList) {
    & $Exe @ArgList | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "$(Split-Path $Exe -Leaf) failed ($LASTEXITCODE)" }
}

# Version: tauri.conf.json's x.y.z plus ".0" (the Store keeps the 4th part).
$conf = Get-Content -LiteralPath (Join-Path $tauriDir 'tauri.conf.json') -Raw | ConvertFrom-Json
if ($conf.version -notmatch '^\d+\.\d+\.\d+$') { throw "tauri.conf.json version '$($conf.version)' isn't x.y.z" }
$version = "$($conf.version).0"

if (-not $SkipBuild) {
    $global:LASTEXITCODE = 0
    & (Join-Path $desktop 'build.ps1') -NoBundle | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "build.ps1 -NoBundle failed ($LASTEXITCODE)" }
}
$exe = Join-Path $tauriDir 'target\release\onscreen-desktop.exe'
if (-not (Test-Path $exe)) { throw "No release exe at $exe (run without -SkipBuild)." }

# Stage the package layout.
$layout = Join-Path $here 'layout'
if (Test-Path $layout) { Remove-Item $layout -Recurse -Force }
New-Item -ItemType Directory -Force (Join-Path $layout 'Assets') | Out-Null
Copy-Item $exe $layout
Copy-Item (Join-Path $xboxShell 'Assets\*.png') (Join-Path $layout 'Assets')
$manifest = (Get-Content -LiteralPath (Join-Path $here 'AppxManifest.xml') -Raw).Replace('{VERSION}', $version)
[IO.File]::WriteAllText((Join-Path $layout 'AppxManifest.xml'), $manifest)

# resources.pri: lets Windows pick the scale-/targetsize- variants of each logo.
$makepri = Find-SdkTool 'makepri.exe'
$priConfig = Join-Path $env:TEMP ("onscreen-priconfig-" + [guid]::NewGuid().ToString('N') + '.xml')
try {
    Invoke-Tool $makepri @('createconfig', '/cf', $priConfig, '/dq', 'en-US', '/pv', '10.0.0', '/o')
    # One resources.pri: the default config splits scale/language candidates
    # into resources.scale-*.pri for resource packages, which only a bundle
    # installs. This is a single .msix, so keep every candidate in the main PRI.
    $cfg = [xml](Get-Content -LiteralPath $priConfig -Raw)
    $packaging = $cfg.SelectSingleNode('//packaging')
    if ($packaging) { [void]$packaging.ParentNode.RemoveChild($packaging) }
    $cfg.Save($priConfig)
    Invoke-Tool $makepri @('new', '/pr', $layout, '/cf', $priConfig, '/mn', (Join-Path $layout 'AppxManifest.xml'), '/of', (Join-Path $layout 'resources.pri'), '/o')
} finally { Remove-Item $priConfig -Force -ErrorAction SilentlyContinue }

# Pack and sign.
New-Item -ItemType Directory -Force $Out | Out-Null
$msix = Join-Path $Out ("OnScreen-Desktop_{0}_x64.msix" -f $version)
Invoke-Tool (Find-SdkTool 'makeappx.exe') @('pack', '/d', $layout, '/p', $msix, '/o')

$pfx = Join-Path $xboxShell 'OnScreen.Xbox_TemporaryKey.pfx'
if (-not (Test-Path $pfx)) { & (Join-Path $xboxShell 'make-cert.ps1') | Out-Host }
# The test certificate's password is the one in OnScreen.Xbox.csproj
# (PackageCertificatePassword); it only signs local test builds.
Invoke-Tool (Find-SdkTool 'signtool.exe') @('sign', '/fd', 'SHA256', '/f', $pfx, '/p', 'onscreen-dev', $msix)

if ($Register) {
    # Developer Mode registers the loose layout without trusting the certificate.
    Get-AppxPackage -Name 'OnScreen.OnScreen' | ForEach-Object {
        Write-Host "removing $($_.PackageFullName)"
        Remove-AppxPackage -Package $_.PackageFullName
    }
    Add-AppxPackage -Register (Join-Path $layout 'AppxManifest.xml')
    Write-Host "registered $((Get-AppxPackage -Name 'OnScreen.OnScreen').PackageFullName)" -ForegroundColor Green
}

Write-Host "==> $msix" -ForegroundColor Green
$msix
