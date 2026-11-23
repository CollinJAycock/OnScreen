<#
.SYNOPSIS
  Builds the store packages for OnScreen's clients into clients\packages\, each
  file named for the store it is uploaded to.

.DESCRIPTION
  Every package is named

      OnScreen-<Store>-<Platform>-<version>[-<build>].<ext>

  e.g. OnScreen-GooglePlay-AndroidTV-1.6.0-29.aab, and gets two files beside it:
  <name>.sha256 (sha256sum format) and <name>.json (store, version, commit,
  whether the tree was clean, signer, build time).

  Targets (-List prints them):
    googleplay-tv      Google Play, Android TV / Google TV   .aab  clients\android (googletv)
    googleplay-phone   Google Play, phone + tablet           .aab  clients\android_native (mobile)
    amazon-firetv      Amazon Appstore, Fire TV              .apk  clients\android (firetv)
    meta-quest         Meta Horizon Store, Quest             .apk  clients\android_native (quest)
    roku               Roku Channel Store                    .pkg  clients\roku (.zip without -RokuSign)
    lg-webos           LG Content Store, webOS               .ipk  clients\webos
    samsung-tizen      Samsung TV Apps (Seller Office)       .wgt  clients\tizen (OnScreenProd profile)
    microsoft-xbox     Microsoft Store, Xbox                 .msixupload  clients\xbox\shell

  A store build comes from committed code: the script stops if the client's
  folder has uncommitted changes, unless -AllowDirty (the .json then says so).
  Signing material stays where each client already keeps it (gitignored); the
  script only checks it is there, and checks the Android packages carry the
  upload key.

.EXAMPLE
  .\build-packages.ps1 -Target googleplay-tv,googleplay-phone
.EXAMPLE
  .\build-packages.ps1 -Target all
.EXAMPLE
  .\build-packages.ps1 -Target roku -RokuSign -RokuHost 10.0.0.86
#>
[CmdletBinding()]
param(
    [string[]]$Target,
    [switch]$List,
    # Build even if the client's folder has uncommitted changes.
    [switch]$AllowDirty,
    # roku: sideload the zip to a dev-mode Roku, sign it there and download the
    # .pkg. Passwords come from ROKU_DEV_PASSWORD / ROKU_SIGN_PASSWORD or a prompt.
    [switch]$RokuSign,
    [string]$RokuHost = $env:ROKU_HOST,
    # samsung-tizen: the Tizen certificate profile for the store build.
    [string]$TizenProfile = 'OnScreenProd',
    [string]$OutDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$Clients = $PSScriptRoot
$Repo = Split-Path $Clients -Parent
if (-not $OutDir) { $OutDir = Join-Path $Clients 'packages' }

# SHA-256 of the Android upload key (both apps). Play and the Horizon Store
# re-sign or check against it; a package signed with anything else is refused.
$AndroidUploadCert = '579BD03770F0B45EF2A0E56C5A1B43B94FBC1F6960BE0337AB96C476FA3CAC75'

# ---------------------------------------------------------------- helpers

function Write-Step([string]$text) { Write-Host "==> $text" -ForegroundColor Cyan }

function Invoke-Checked {
    # Runs a native command in a folder and throws on a non-zero exit.
    param([string]$Dir, [string]$Exe, [string[]]$ArgList)
    Push-Location $Dir
    try {
        # Out-Host: a native command's output would otherwise become part of the
        # calling function's return value.
        & $Exe @ArgList | Out-Host
        if ($LASTEXITCODE -ne 0) { throw "$Exe $($ArgList -join ' ') failed ($LASTEXITCODE) in $Dir" }
    } finally { Pop-Location }
}

function Get-GitState([string]$RelDir) {
    $commit = (& git -C $Repo rev-parse --short=10 HEAD).Trim()
    $branch = (& git -C $Repo rev-parse --abbrev-ref HEAD).Trim()
    $changes = @(& git -C $Repo status --porcelain -- $RelDir)
    [pscustomobject]@{ Commit = $commit; Branch = $branch; Changes = $changes }
}

function Assert-File([string]$Path, [string]$What) {
    if (-not (Test-Path -LiteralPath $Path)) { throw "$What not found: $Path" }
}

function Find-JavaHome {
    if ($env:JAVA_HOME -and (Test-Path (Join-Path $env:JAVA_HOME 'bin\java.exe'))) { return $env:JAVA_HOME }
    $candidates = @(
        (Get-ChildItem 'C:\Program Files\Java' -Directory -Filter 'jdk-*' -ErrorAction SilentlyContinue |
            Sort-Object Name -Descending | ForEach-Object { $_.FullName }),
        'C:\Program Files\Android\Android Studio\jbr'
    ) | Where-Object { $_ }
    foreach ($c in $candidates) { if (Test-Path (Join-Path $c 'bin\java.exe')) { return $c } }
    throw 'No JDK found: set JAVA_HOME.'
}

function Find-AndroidSdk {
    foreach ($c in @($env:ANDROID_HOME, $env:ANDROID_SDK_ROOT, (Join-Path $env:LOCALAPPDATA 'Android\Sdk'))) {
        if ($c -and (Test-Path (Join-Path $c 'build-tools'))) { return $c }
    }
    throw 'Android SDK not found: set ANDROID_HOME.'
}

function Find-Aapt2 {
    $bt = Join-Path (Find-AndroidSdk) 'build-tools'
    $dir = Get-ChildItem $bt -Directory | Where-Object { Test-Path (Join-Path $_.FullName 'aapt2.exe') } |
        Sort-Object { [version]($_.Name -replace '[^0-9.].*$', '') } -Descending | Select-Object -First 1
    if (-not $dir) { throw "aapt2.exe not found under $bt" }
    Join-Path $dir.FullName 'aapt2.exe'
}

function Get-SignerSha256([string]$Path) {
    # keytool reads the v1 (JAR) signature of an .apk or .aab.
    $keytool = Join-Path (Find-JavaHome) 'bin\keytool.exe'
    $out = & $keytool -printcert -jarfile $Path | Out-String
    $m = [regex]::Match($out, 'SHA256:\s*([0-9A-F:]{95})')
    if (-not $m.Success) { throw "No signer certificate in $Path (unsigned?)" }
    $m.Groups[1].Value -replace ':', ''
}

function Get-GradleVersion([string]$GradleFile) {
    $text = Get-Content -LiteralPath $GradleFile -Raw
    $code = [regex]::Match($text, '(?m)^\s*versionCode\s*=\s*(\d+)')
    $name = [regex]::Match($text, '(?m)^\s*versionName\s*=\s*"([^"]+)"')
    if (-not ($code.Success -and $name.Success)) { throw "versionCode/versionName not found in $GradleFile" }
    [pscustomobject]@{ Version = $name.Groups[1].Value; Build = $code.Groups[1].Value }
}

function Invoke-Gradle([string]$ProjectDir, [string]$Task) {
    $saved = $env:JAVA_HOME
    try {
        $env:JAVA_HOME = Find-JavaHome
        Invoke-Checked $ProjectDir (Join-Path $ProjectDir 'gradlew.bat') @('--no-daemon', $Task)
    } finally { $env:JAVA_HOME = $saved }
}

function Assert-AndroidPackage {
    param([string]$Path, [string]$Package, [string]$Build, [switch]$Apk)
    $signer = Get-SignerSha256 $Path
    if ($signer -ne $AndroidUploadCert) {
        throw "$Path is signed by $signer, not the upload key $AndroidUploadCert"
    }
    if ($Apk) {
        $badging = & (Find-Aapt2) dump badging $Path | Select-Object -First 1
        $m = [regex]::Match($badging, "package: name='([^']+)' versionCode='(\d+)'")
        if (-not $m.Success) { throw "aapt2 couldn't read $Path" }
        if ($m.Groups[1].Value -ne $Package -or $m.Groups[2].Value -ne $Build) {
            throw "$Path is $($m.Groups[1].Value) $($m.Groups[2].Value), expected $Package $Build"
        }
    }
    $signer
}

function Assert-Npm([string]$Dir) {
    if (-not (Test-Path (Join-Path $Dir 'node_modules'))) {
        Write-Step "npm ci in $Dir"
        Invoke-Checked $Dir 'npm.cmd' @('ci')
    }
}

function Get-Newest([string]$Dir, [string]$Filter, [datetime]$After) {
    $f = Get-ChildItem -LiteralPath $Dir -Filter $Filter -Recurse -File -ErrorAction SilentlyContinue |
        Where-Object { $_.LastWriteTime -ge $After } | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if (-not $f) { throw "No $Filter newer than $After under $Dir" }
    $f.FullName
}

# ---------------------------------------------------------------- targets
# Each target: store / platform for the name, the client folder (for the clean
# check), Version (returns Version + optional Build) and Build (builds, checks,
# returns @{ Path; Ext; Signer }). Build gets the start time to find fresh output.

$Targets = [ordered]@{}

$Targets['googleplay-tv'] = @{
    Store = 'GooglePlay'; Platform = 'AndroidTV'; Dir = 'clients/android'
    Version = { Get-GradleVersion (Join-Path $Clients 'android\app\build.gradle.kts') }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'android'
        Assert-File (Join-Path $proj 'local.properties') 'TV signing config (release.keystore in local.properties)'
        Invoke-Gradle $proj ':app:bundleGoogletvRelease'
        $out = Join-Path $proj 'app\build\outputs\bundle\googletvRelease\app-googletv-release.aab'
        $null = Get-Newest (Split-Path $out) '*.aab' $start
        @{ Path = $out; Ext = 'aab'; Signer = (Assert-AndroidPackage $out 'tv.onscreen.android' $v.Build) }
    }
}

$Targets['googleplay-phone'] = @{
    Store = 'GooglePlay'; Platform = 'Phone'; Dir = 'clients/android_native'
    Version = { Get-GradleVersion (Join-Path $Clients 'android_native\app\build.gradle.kts') }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'android_native'
        Assert-File (Join-Path $proj 'keystore.properties') 'Phone signing config'
        Invoke-Gradle $proj ':app:bundleMobileRelease'
        $out = Join-Path $proj 'app\build\outputs\bundle\mobileRelease\app-mobile-release.aab'
        $null = Get-Newest (Split-Path $out) '*.aab' $start
        @{ Path = $out; Ext = 'aab'; Signer = (Assert-AndroidPackage $out 'tv.onscreen.android' $v.Build) }
    }
}

$Targets['amazon-firetv'] = @{
    Store = 'AmazonAppstore'; Platform = 'FireTV'; Dir = 'clients/android'
    Version = { Get-GradleVersion (Join-Path $Clients 'android\app\build.gradle.kts') }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'android'
        Assert-File (Join-Path $proj 'local.properties') 'TV signing config (release.keystore in local.properties)'
        Invoke-Gradle $proj ':app:assembleFiretvRelease'
        $out = Join-Path $proj 'app\build\outputs\apk\firetv\release\app-firetv-release.apk'
        $null = Get-Newest (Split-Path $out) '*.apk' $start
        @{ Path = $out; Ext = 'apk'; Signer = (Assert-AndroidPackage $out 'tv.onscreen.android' $v.Build -Apk) }
    }
}

$Targets['meta-quest'] = @{
    Store = 'MetaHorizonStore'; Platform = 'Quest'; Dir = 'clients/android_native'
    Version = { Get-GradleVersion (Join-Path $Clients 'android_native\app\build.gradle.kts') }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'android_native'
        Assert-File (Join-Path $proj 'keystore.properties') 'Phone/Quest signing config'
        Invoke-Gradle $proj ':app:assembleQuestRelease'
        $out = Join-Path $proj 'app\build\outputs\apk\quest\release\app-quest-release.apk'
        $null = Get-Newest (Split-Path $out) '*.apk' $start
        @{ Path = $out; Ext = 'apk'; Signer = (Assert-AndroidPackage $out 'tv.onscreen.quest' $v.Build -Apk) }
    }
}

$Targets['roku'] = @{
    Store = 'RokuChannelStore'; Platform = 'Roku'; Dir = 'clients/roku'
    Version = {
        # The store shows major.minor (1.2.0); build_version is the build.
        $m = Get-Content -LiteralPath (Join-Path $Clients 'roku\manifest') -Raw
        $major = [regex]::Match($m, '(?m)^major_version=(\d+)').Groups[1].Value
        $minor = [regex]::Match($m, '(?m)^minor_version=(\d+)').Groups[1].Value
        $build = [regex]::Match($m, '(?m)^build_version=(\d+)').Groups[1].Value
        [pscustomobject]@{ Version = "$major.$minor.0"; Build = [string][int]$build }
    }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'roku'
        Assert-Npm $proj
        Invoke-Checked $proj 'npm.cmd' @('run', 'package')
        $zip = Get-Newest (Join-Path $proj 'dist') 'onscreen-roku-*.zip' $start
        if (-not $RokuSign) { return @{ Path = $zip; Ext = 'zip'; Signer = $null } }

        if (-not $RokuHost) { throw '-RokuSign needs -RokuHost (or ROKU_HOST).' }
        $pkg = Join-Path $env:TEMP ("onscreen-roku-{0}.pkg" -f [guid]::NewGuid().ToString('N'))
        $saved = @{ ROKU_HOST = $env:ROKU_HOST; ROKU_DEV_PASSWORD = $env:ROKU_DEV_PASSWORD; ROKU_SIGN_PASSWORD = $env:ROKU_SIGN_PASSWORD }
        try {
            $env:ROKU_HOST = $RokuHost
            foreach ($n in @('ROKU_DEV_PASSWORD', 'ROKU_SIGN_PASSWORD')) {
                if (-not (Get-Item "env:$n" -ErrorAction SilentlyContinue)) {
                    $label = if ($n -eq 'ROKU_DEV_PASSWORD') { "Roku developer password (user rokudev) for $RokuHost" } else { 'Roku signing (genkey) password' }
                    $sec = Read-Host -AsSecureString $label
                    Set-Item "env:$n" ([Runtime.InteropServices.Marshal]::PtrToStringAuto(
                        [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)))
                }
            }
            Invoke-Checked $proj 'node' @('scripts/sign.mjs', $zip, $pkg)
        } finally {
            foreach ($k in $saved.Keys) {
                if ($null -eq $saved[$k]) { Remove-Item "env:$k" -ErrorAction SilentlyContinue } else { Set-Item "env:$k" $saved[$k] }
            }
        }
        @{ Path = $pkg; Ext = 'pkg'; Signer = 'Roku Packager on ' + $RokuHost; Temp = $true }
    }
}

$Targets['lg-webos'] = @{
    Store = 'LGContentStore'; Platform = 'webOS'; Dir = 'clients/webos'
    Version = {
        $info = Get-Content -LiteralPath (Join-Path $Clients 'webos\appinfo.json') -Raw | ConvertFrom-Json
        [pscustomobject]@{ Version = $info.version; Build = $null }
    }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'webos'
        Assert-Npm $proj
        Invoke-Checked $proj 'npm.cmd' @('run', 'package')
        $ipk = Join-Path $proj ("tv.onscreen.webos_{0}_all.ipk" -f $v.Version)
        $null = Get-Newest $proj '*.ipk' $start
        Assert-File $ipk 'webOS package'
        @{ Path = $ipk; Ext = 'ipk'; Signer = $null }
    }
}

$Targets['samsung-tizen'] = @{
    Store = 'SamsungTVApps'; Platform = 'Tizen'; Dir = 'clients/tizen'
    Version = {
        $cfg = [xml](Get-Content -LiteralPath (Join-Path $Clients 'tizen\config.xml') -Raw)
        [pscustomobject]@{ Version = $cfg.widget.version; Build = $null }
    }
    Build = {
        param($v, $start)
        $proj = Join-Path $Clients 'tizen'
        $tizen = (Get-Command tizen.bat -ErrorAction SilentlyContinue | Select-Object -ExpandProperty Source -First 1)
        if (-not $tizen) { $tizen = 'C:\tizen-studio\tools\ide\bin\tizen.bat' }
        Assert-File $tizen 'Tizen CLI'
        Assert-Npm $proj
        Invoke-Checked $proj 'npm.cmd' @('run', 'build')
        # Signed into a scratch folder so the dev (sideload) .wgt in dist\ is left alone.
        $tmp = Join-Path $env:TEMP ("onscreen-tizen-" + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory $tmp | Out-Null
        Invoke-Checked $proj $tizen @('package', '-t', 'wgt', '-s', $TizenProfile, '-o', $tmp, '--', (Join-Path $proj 'build'))
        $wgt = Get-Newest $tmp '*.wgt' $start
        @{ Path = $wgt; Ext = 'wgt'; Signer = "Tizen profile $TizenProfile"; Temp = $true; TempDir = $tmp }
    }
}

$Targets['microsoft-xbox'] = @{
    Store = 'MicrosoftStore'; Platform = 'Xbox'; Dir = 'clients/xbox'
    Version = {
        $mf = [xml](Get-Content -LiteralPath (Join-Path $Clients 'xbox\shell\Package.appxmanifest') -Raw)
        [pscustomobject]@{ Version = $mf.Package.Identity.Version; Build = $null }
    }
    Build = {
        param($v, $start)
        $shell = Join-Path $Clients 'xbox\shell'
        & (Join-Path $shell 'build.ps1') -Store | Out-Host
        $upload = Get-Newest (Join-Path $shell 'AppPackages') '*.msixupload' $start
        @{ Path = $upload; Ext = 'msixupload'; Signer = 'Partner Center re-signs' }
    }
}

# ---------------------------------------------------------------- main

if ($List -or -not $Target) {
    Write-Host 'Targets (-Target <name>[,<name>...] or -Target all):'
    foreach ($k in $Targets.Keys) {
        $t = $Targets[$k]
        Write-Host ('  {0,-17} OnScreen-{1}-{2}-<version>  ({3})' -f $k, $t.Store, $t.Platform, $t.Dir)
    }
    if (-not $List) { exit 1 }
    exit 0
}

# "a,b" arrives as one string through powershell -File; split it either way.
$Target = @($Target | ForEach-Object { $_ -split ',' } | ForEach-Object { $_.Trim().ToLowerInvariant() } | Where-Object { $_ })
$wanted = if ($Target -contains 'all') { @($Targets.Keys) } else { $Target }
$unknown = @($wanted | Where-Object { -not $Targets.Contains($_) })
if ($unknown.Count) { throw "Unknown target(s): $($unknown -join ', '). Run with -List." }

New-Item -ItemType Directory -Force $OutDir | Out-Null
$results = @()

foreach ($key in $wanted) {
    $t = $Targets[$key]
    Write-Step "$key ($($t.Store) / $($t.Platform))"

    $git = Get-GitState $t.Dir
    if ($git.Changes.Count -and -not $AllowDirty) {
        throw "$($t.Dir) has uncommitted changes; store builds come from committed code (or pass -AllowDirty):`n  $($git.Changes -join "`n  ")"
    }

    $v = & $t.Version
    $start = (Get-Date).AddSeconds(-2)
    $built = & $t.Build $v $start

    $name = "OnScreen-$($t.Store)-$($t.Platform)-$($v.Version)"
    if ($v.Build) { $name += "-$($v.Build)" }
    $name += ".$($built.Ext)"
    $dest = Join-Path $OutDir $name
    Copy-Item -LiteralPath $built.Path -Destination $dest -Force
    if ($built.ContainsKey('Temp') -and $built.Temp) {
        Remove-Item -LiteralPath $built.Path -Force -ErrorAction SilentlyContinue
        if ($built.ContainsKey('TempDir')) { Remove-Item -LiteralPath $built.TempDir -Recurse -Force -ErrorAction SilentlyContinue }
    }

    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $dest).Hash.ToLowerInvariant()
    [IO.File]::WriteAllText("$dest.sha256", "$hash *$name`n")
    $info = [ordered]@{
        file      = $name
        target    = $key
        store     = $t.Store
        platform  = $t.Platform
        version   = $v.Version
        build     = $v.Build
        sha256    = $hash
        bytes     = (Get-Item -LiteralPath $dest).Length
        commit    = $git.Commit
        branch    = $git.Branch
        clean     = ($git.Changes.Count -eq 0)
        signer    = $built.Signer
        builtAt   = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    }
    [IO.File]::WriteAllText("$dest.json", ($info | ConvertTo-Json) + "`n")
    $results += [pscustomobject]$info
    Write-Host "    -> $dest" -ForegroundColor Green
}

Write-Host ''
$results | Format-Table file, version, build, commit, clean, @{ n = 'sha256'; e = { $_.sha256.Substring(0, 12) } } -AutoSize
