# Build the all-in-one Windows installer (.exe via Inno Setup 6).
#
# Output: dist/OnScreen-Setup-<version>.exe
#
# What's bundled:
#   - server.exe / worker.exe / devtoken.exe (cross-compiled here)
#   - WinSW.exe (downloaded, cached)
#   - ffmpeg.exe + ffprobe.exe (Gyan.dev full build, downloaded, cached)
#   - PostgreSQL 17 Windows binaries (downloaded from EnterpriseDB, cached)
#   - tporadowski/redis Windows zip (downloaded, cached)
#
# Prereqs on the build host:
#   - Go 1.22+ on PATH
#   - Node.js 20+ + npm
#   - PowerShell 5.1+
#   - Inno Setup 6.x at "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
#     (auto-installed via choco / direct download if missing — see below)
#   - Internet on first run (subsequent runs reuse ../.cache/)

[CmdletBinding()]
param(
    [string]$Version = "",
    [switch]$SkipFrontend = $false
)

$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot\..\.."
$cacheDir = "$PSScriptRoot\..\.cache"
$stageDir = "$PSScriptRoot\stage"
$distDir  = "$root\dist"
Set-Location $root

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.ServicePointManager]::SecurityProtocol

if (-not $Version) {
    # Source of truth: the VERSION file at the repo root. `git describe`
    # is unreliable here because v2.0.0 was tagged on a sidetrack
    # commit that isn't in main's history; describe falls back to
    # v1.1.2 and stamps installers with the wrong major.
    $verFile = Join-Path $root "VERSION"
    if (Test-Path $verFile) {
        $base = (Get-Content $verFile -Raw).Trim()
        $sha = ""
        try { $sha = (git rev-parse --short HEAD 2>$null).Trim() } catch { }
        $dirty = ""
        try {
            $dirtyOut = (git status --porcelain 2>$null)
            if ($dirtyOut) { $dirty = "-dirty" }
        } catch { }
        if ($sha) { $Version = "$base-$sha$dirty" } else { $Version = $base }
    } else {
        try { $Version = (git describe --tags --always --dirty 2>$null).Trim() } catch { }
        if (-not $Version) { $Version = "dev" }
    }
}

# Inno Setup version-string rules: must be only digits, dots, and at
# most a trailing build tag. `git describe` output like
# `v1.1.2-297-gc285541-dirty` is rejected. Strip down to a clean
# semver-shaped string for the SetupVersion field.
$cleanVersion = $Version -replace '^v',''
if ($cleanVersion -match '^([0-9]+\.[0-9]+\.[0-9]+)') {
    $cleanVersion = $Matches[1]
} else {
    $cleanVersion = "0.0.0"
}

$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-X main.version=$Version -X main.buildTime=$buildTime"

New-Item -ItemType Directory -Path $cacheDir, $distDir -Force | Out-Null
if (Test-Path $stageDir) { Remove-Item -Recurse -Force $stageDir }
New-Item -ItemType Directory -Path $stageDir | Out-Null

# ── Locate ISCC.exe ──────────────────────────────────────────────────────────
$iscc = "C:\Program Files (x86)\Inno Setup 6\ISCC.exe"
if (-not (Test-Path $iscc)) {
    $iscc = "C:\Program Files\Inno Setup 6\ISCC.exe"
}
if (-not (Test-Path $iscc)) {
    Write-Host "Inno Setup 6 not found. Install it first:" -ForegroundColor Red
    Write-Host "  https://jrsoftware.org/isdl.php" -ForegroundColor Red
    Write-Host "  or: choco install innosetup -y" -ForegroundColor Red
    exit 1
}
Write-Host "Inno Setup at: $iscc" -ForegroundColor Gray

# ── Frontend ─────────────────────────────────────────────────────────────────
if (-not $SkipFrontend) {
    Write-Host "==> Building frontend..." -ForegroundColor Cyan
    Push-Location web
    npm install --silent
    if ($LASTEXITCODE -ne 0) { throw "npm install failed" }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "npm run build failed" }
    Pop-Location
    if (Test-Path internal\webui\dist) { Remove-Item -Recurse -Force internal\webui\dist }
    Copy-Item -Recurse web\dist internal\webui\dist
} else {
    Write-Host "==> Skipping frontend build (-SkipFrontend)" -ForegroundColor Yellow
}

# ── Go binaries ──────────────────────────────────────────────────────────────
Write-Host "==> Building Go binaries (windows/amd64)..." -ForegroundColor Cyan
$env:GOOS = "windows"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"

foreach ($b in @(
    @{ Pkg = "./cmd/server";   Out = "server.exe" }
    @{ Pkg = "./cmd/worker";   Out = "worker.exe" }
    @{ Pkg = "./cmd/devtoken"; Out = "devtoken.exe" }
)) {
    Write-Host "    -> $($b.Out)" -ForegroundColor Gray
    go build -ldflags "$ldflags" -o (Join-Path $stageDir $b.Out) $b.Pkg
    if ($LASTEXITCODE -ne 0) { throw "go build $($b.Pkg) failed" }
}

# ── WinSW ────────────────────────────────────────────────────────────────────
$winswCache = "$cacheDir\WinSW-x64-v2.12.0.exe"
if (-not (Test-Path $winswCache)) {
    Write-Host "==> Downloading WinSW v2.12.0..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri "https://github.com/winsw/winsw/releases/download/v2.12.0/WinSW-x64.exe" `
        -OutFile $winswCache -UseBasicParsing
}
Copy-Item $winswCache "$stageDir\WinSW.exe"

# ── ffmpeg ───────────────────────────────────────────────────────────────────
$ffmpegZip = "$cacheDir\ffmpeg-7.1.1-full_build.zip"
if (-not (Test-Path $ffmpegZip)) {
    Write-Host "==> Downloading ffmpeg 7.1.1 (Gyan full build, ~150 MB)..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri "https://github.com/GyanD/codexffmpeg/releases/download/7.1.1/ffmpeg-7.1.1-full_build.zip" `
        -OutFile $ffmpegZip -UseBasicParsing
}
$ffmpegExtract = "$cacheDir\ffmpeg-extract"
if (Test-Path $ffmpegExtract) { Remove-Item -Recurse -Force $ffmpegExtract }
Expand-Archive -Path $ffmpegZip -DestinationPath $ffmpegExtract -Force
$ffmpegStage = "$stageDir\ffmpeg"
New-Item -ItemType Directory -Path $ffmpegStage -Force | Out-Null
$ffSrc = (Get-ChildItem -Path $ffmpegExtract -Recurse -Filter "ffmpeg.exe" | Select-Object -First 1).DirectoryName
Copy-Item "$ffSrc\ffmpeg.exe"  $ffmpegStage
Copy-Item "$ffSrc\ffprobe.exe" $ffmpegStage

# ── PostgreSQL ───────────────────────────────────────────────────────────────
# EnterpriseDB ships portable Windows binaries as one big zip. We extract
# the pgsql/ subtree (binaries + share data) which is everything initdb
# needs to bootstrap a cluster on the target machine.
$pgVersion = "17.5-1"
$pgZip = "$cacheDir\postgresql-$pgVersion-windows-x64-binaries.zip"
if (-not (Test-Path $pgZip)) {
    Write-Host "==> Downloading PostgreSQL $pgVersion (~320 MB)..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri "https://get.enterprisedb.com/postgresql/postgresql-$pgVersion-windows-x64-binaries.zip" `
        -OutFile $pgZip -UseBasicParsing
}
$pgExtract = "$cacheDir\pg-extract"
if (Test-Path $pgExtract) { Remove-Item -Recurse -Force $pgExtract }
Write-Host "==> Extracting PostgreSQL..." -ForegroundColor Cyan
Expand-Archive -Path $pgZip -DestinationPath $pgExtract -Force
$pgsqlSrc = "$pgExtract\pgsql"
if (-not (Test-Path "$pgsqlSrc\bin\initdb.exe")) {
    throw "Postgres extract layout unexpected: missing $pgsqlSrc\bin\initdb.exe"
}
# Trim non-essentials to keep the installer reasonable. We need bin/,
# share/, lib/. We can drop doc/, symbols/, pgAdmin/ (not bundled
# anyway), and the StackBuilder helper.
$pgStage = "$stageDir\pgsql"
New-Item -ItemType Directory -Path $pgStage -Force | Out-Null
foreach ($sub in @("bin", "share", "lib")) {
    Copy-Item -Recurse "$pgsqlSrc\$sub" "$pgStage\$sub"
}

# ── Redis (tporadowski) ──────────────────────────────────────────────────────
$redisZip = "$cacheDir\Redis-x64-5.0.14.1.zip"
if (-not (Test-Path $redisZip)) {
    Write-Host "==> Downloading Redis-on-Windows 5.0.14.1 (~5 MB)..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri "https://github.com/tporadowski/redis/releases/download/v5.0.14.1/Redis-x64-5.0.14.1.zip" `
        -OutFile $redisZip -UseBasicParsing
}
$redisExtract = "$cacheDir\redis-extract"
if (Test-Path $redisExtract) { Remove-Item -Recurse -Force $redisExtract }
Expand-Archive -Path $redisZip -DestinationPath $redisExtract -Force
$redisStage = "$stageDir\redis"
New-Item -ItemType Directory -Path $redisStage -Force | Out-Null
# Copy just the runtime files (executables + the default conf). Drops
# the docs and dump.rdb sample.
foreach ($f in @("redis-server.exe", "redis-cli.exe", "redis.windows.conf")) {
    $src = Join-Path $redisExtract $f
    if (Test-Path $src) { Copy-Item $src $redisStage }
}

# ── Compile the installer ────────────────────────────────────────────────────
Write-Host "==> Compiling installer with Inno Setup..." -ForegroundColor Cyan
& $iscc "/Q" "/DMyAppVersion=$cleanVersion" "$PSScriptRoot\onscreen.iss"
if ($LASTEXITCODE -ne 0) { throw "ISCC failed (exit $LASTEXITCODE)" }

$outExe = "$distDir\OnScreen-Setup-$cleanVersion.exe"
if (-not (Test-Path $outExe)) { throw "Expected output not found: $outExe" }
$size = "{0:N1} MB" -f ((Get-Item $outExe).Length / 1MB)
Write-Host "==> Done." -ForegroundColor Green
Write-Host "    $outExe  ($size)"
