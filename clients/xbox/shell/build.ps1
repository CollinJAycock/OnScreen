# Build the Xbox shell with Visual Studio 2022's msbuild (the UWP workload).
#   .\build.ps1                 Release (.NET Native), x64: the signed .msix in
#                               AppPackages\ for Xbox Dev Mode (Device Portal),
#                               with its dependency packages beside it
#   .\build.ps1 -Register       also register the build on this PC (Windows
#                               Developer Mode), replacing an earlier one
#   .\build.ps1 -DevTools       a local test build whose web view opens the
#                               DevTools protocol on 127.0.0.1:9229; never
#                               install that one on a console
#   .\build.ps1 -Debug          Debug instead of Release
param(
    [switch]$Register,
    [switch]$DevTools,
    [switch]$Debug
)
$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$vswhere = "${env:ProgramFiles(x86)}\Microsoft Visual Studio\Installer\vswhere.exe"
$vs = & $vswhere -latest -version '[17.0,18.0)' -requires Microsoft.VisualStudio.Workload.Universal -property installationPath
if (-not $vs) { throw "Visual Studio 2022 with the 'Universal Windows Platform development' workload is required." }
$msbuild = Join-Path $vs 'MSBuild\Current\Bin\amd64\MSBuild.exe'

if (-not (Test-Path (Join-Path $here 'OnScreen.Xbox_TemporaryKey.pfx'))) { & (Join-Path $here 'make-cert.ps1') }

$config = if ($Debug) { 'Debug' } else { 'Release' }
$props = @("/p:Configuration=$config", '/p:Platform=x64')
if ($DevTools) {
    $defines = if ($Debug) { 'DEBUG;TRACE;NETFX_CORE;WINDOWS_UWP;DEVTOOLS' } else { 'TRACE;NETFX_CORE;WINDOWS_UWP;DEVTOOLS' }
    # msbuild splits properties on ";", so escape it.
    $props += "/p:DefineConstants=$($defines.Replace(';', '%3B'))"
}
& $msbuild (Join-Path $here 'OnScreen.Xbox.csproj') /restore /m /nologo /v:minimal @props
if ($LASTEXITCODE -ne 0) { throw "msbuild failed ($LASTEXITCODE)" }

if ($Register) {
    # Release's runnable layout is the .NET Native one (ilc\).
    $layout = if ($Debug) { "bin\x64\Debug" } else { "bin\x64\Release\ilc" }
    $old = Get-AppxPackage -Name OnScreen.Xbox
    # Same version, other folder: Windows keeps the old registration unless
    # it is removed first.
    if ($old) { Remove-AppxPackage -Package $old.PackageFullName }
    Add-AppxPackage -Register (Join-Path $here "$layout\AppxManifest.xml")
    Write-Host "registered: start 'OnScreen' from the Start menu"
}
Get-ChildItem (Join-Path $here 'AppPackages') -Recurse -Include *.msix | Where-Object FullName -match "_x64$(if ($Debug) { '_Debug' })_Test" | ForEach-Object { $_.FullName }
