# Build a portable Windows zip for OnScreen.
#
# Output: dist/onscreen-windows-amd64-<version>.zip
#
# Contents:
#   - server.exe / worker.exe             (Go binaries; devtoken is a dev tool, not shipped)
#   - WinSW.exe + onscreen.xml         (Windows Service wrapper)
#   - ffmpeg.exe + libraries           (Gyan.FFmpeg full build, ships with QSV/NVENC/AMF)
#   - start.ps1                        (foreground launch)
#   - install-service.ps1              (registers Windows Service)
#   - uninstall-service.ps1            (removes service)
#   - start-deps.ps1                   (docker compose up Postgres + Valkey)
#   - docker-compose.deps.yml          (deps-only stack)
#   - .env.example                     (config template)
#   - README.md                        (quickstart)
#
# Prereqs on the build host:
#   - Go 1.22+ on PATH
#   - Node.js 20+ + npm
#   - PowerShell 5.1+ (or pwsh 7+)
#   - Internet access (downloads WinSW + ffmpeg first time, caches in installer/windows/.cache/)

[CmdletBinding()]
param(
    [string]$Version = "",
    [switch]$SkipFrontend = $false,
    [switch]$NoFfmpeg = $false   # produce a smaller zip without bundled ffmpeg
)

$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot\..\.."
Set-Location $root

# Windows PowerShell 5.1 negotiates TLS 1.0/1.1 by default and GitHub
# rejects those — force TLS 1.2 so Invoke-WebRequest can reach
# github.com / objects.githubusercontent.com without the bare
# "connection was closed unexpectedly" error.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.ServicePointManager]::SecurityProtocol

# ── Pinned third-party downloads ─────────────────────────────────────────────
# Bundled third-party binaries run as SYSTEM under the OnScreen service. Each
# download and each CACHED copy (on every build) is checked against a pinned
# SHA-256; a mismatch deletes the file and fails the build. Same helper and
# pins as installer/windows-msi/build.ps1 — keep them in sync when bumping.
function Get-PinnedDownload {
    param([string]$Url, [string]$OutFile, [string]$Sha256)
    if (-not (Test-Path $OutFile)) {
        Write-Host "==> Downloading $Url" -ForegroundColor Cyan
        $partial = "$OutFile.partial"
        Invoke-WebRequest -Uri $Url -OutFile $partial -UseBasicParsing
        Move-Item -Force $partial $OutFile
    }
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $OutFile).Hash
    if (-not $Sha256) {
        Write-Warning "UNPINNED download $(Split-Path -Leaf $OutFile): sha256=$actual - verify it and pin the hash."
        return
    }
    if ($actual -ne $Sha256.ToUpperInvariant()) {
        Remove-Item -Force $OutFile
        throw "SHA-256 mismatch for $(Split-Path -Leaf $OutFile): expected $Sha256, got $actual. The file was deleted; re-verify the source before changing the pin."
    }
}
# Provenance: vendors publish no checksum/signature for these; hashes taken on
# 2026-09-25 from the maintainer's existing caches (two independent downloads
# agreed; sizes match GitHub's release asset metadata). See the MSI build.ps1.
$Pins = @{
    WinSW  = "05B82D46AD331CC16BDC00DE5C6332C1EF818DF8CEEFCD49C726553209B3A0DA"  # WinSW-x64.exe v2.12.0
    Ffmpeg = "D760E1B3574402ED18B4865851F87D87E73965A982E6453212DF8621FED1C508"  # Gyan ffmpeg-7.1.1-full_build.zip
}

if (-not $Version) {
    # Source of truth: the VERSION file at the repo root. `git describe`
    # is unreliable here because v2.0.0 was tagged on a sidetrack
    # commit that isn't in main's history, so describe falls back to
    # the old v1.1.2 tag and stamps installers with the wrong major.
    # The VERSION file is a flat semantic-version string the team
    # bumps explicitly when shipping; the short SHA + dirty marker
    # are appended for traceability without changing the leading
    # major.minor.
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

$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-X main.version=$Version -X main.buildTime=$buildTime"

$cacheDir = Join-Path $PSScriptRoot ".cache"
$stageDir = Join-Path $PSScriptRoot ".stage"
$distDir  = Join-Path $root "dist"
$zipName  = "onscreen-windows-amd64-$Version.zip"
$zipPath  = Join-Path $distDir $zipName

New-Item -ItemType Directory -Path $cacheDir, $distDir -Force | Out-Null
if (Test-Path $stageDir) { Remove-Item -Recurse -Force $stageDir }
New-Item -ItemType Directory -Path $stageDir | Out-Null

# ── Frontend ─────────────────────────────────────────────────────────────────
if (-not $SkipFrontend) {
    Write-Host "==> Building frontend..." -ForegroundColor Cyan
    Push-Location web
    # npm ci: install exactly the lockfile (with integrity checks).
    npm ci --silent
    if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
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
$env:CGO_ENABLED = "0"   # static, no MSYS dependency on the target box

# cmd/devtoken is NOT shipped: it mints admin tokens from SECRET_KEY and is a
# developer tool, not part of the product.
$binaries = @(
    @{ Pkg = "./cmd/server";   Out = "server.exe" }
    @{ Pkg = "./cmd/worker";   Out = "worker.exe" }
)
foreach ($b in $binaries) {
    $outPath = Join-Path $stageDir $b.Out
    Write-Host "    -> $($b.Out)" -ForegroundColor Gray
    go build -ldflags "$ldflags" -o $outPath $b.Pkg
    if ($LASTEXITCODE -ne 0) { throw "go build $($b.Pkg) failed" }
}

# ── WinSW (Windows Service wrapper) ──────────────────────────────────────────
# v2.12.0 ships `WinSW-x64.exe` as a self-contained binary — runs without
# a .NET install on the target box. The v3.x line is alpha-only as of
# 2026 and several of the alpha tags have been retracted.
$winswVersion = "v2.12.0"
$winswCache = Join-Path $cacheDir "WinSW-x64-$winswVersion.exe"
Get-PinnedDownload -Url "https://github.com/winsw/winsw/releases/download/$winswVersion/WinSW-x64.exe" `
    -OutFile $winswCache -Sha256 $Pins.WinSW
Copy-Item $winswCache (Join-Path $stageDir "WinSW.exe")

# ── ffmpeg ───────────────────────────────────────────────────────────────────
if (-not $NoFfmpeg) {
    # Gyan.dev's "full" build — has NVENC, QSV, AMF, libsvtav1, libdav1d, etc.
    # Single static zip; we extract just bin/ffmpeg.exe + bin/ffprobe.exe.
    $ffmpegVersion = "7.1.1"
    $ffmpegZip = Join-Path $cacheDir "ffmpeg-$ffmpegVersion-full_build.zip"
    Get-PinnedDownload -Url "https://github.com/GyanD/codexffmpeg/releases/download/$ffmpegVersion/ffmpeg-$ffmpegVersion-full_build.zip" `
        -OutFile $ffmpegZip -Sha256 $Pins.Ffmpeg
    $ffmpegStage = Join-Path $cacheDir "ffmpeg-extract"
    if (Test-Path $ffmpegStage) { Remove-Item -Recurse -Force $ffmpegStage }
    Expand-Archive -Path $ffmpegZip -DestinationPath $ffmpegStage -Force
    $bundledFfmpegDir = Join-Path $stageDir "ffmpeg"
    New-Item -ItemType Directory -Path $bundledFfmpegDir -Force | Out-Null
    $extractedBin = Get-ChildItem -Path $ffmpegStage -Recurse -Filter "ffmpeg.exe" | Select-Object -First 1
    if (-not $extractedBin) { throw "ffmpeg.exe not found inside downloaded zip" }
    Copy-Item (Join-Path $extractedBin.DirectoryName "ffmpeg.exe")  $bundledFfmpegDir
    Copy-Item (Join-Path $extractedBin.DirectoryName "ffprobe.exe") $bundledFfmpegDir
} else {
    Write-Host "==> Skipping ffmpeg bundle (-NoFfmpeg)" -ForegroundColor Yellow
}

# ── Static templates ─────────────────────────────────────────────────────────
Write-Host "==> Copying templates..." -ForegroundColor Cyan
$templates = @(
    "onscreen.xml",
    "start.ps1",
    "install-service.ps1",
    "uninstall-service.ps1",
    "start-deps.ps1",
    "docker-compose.deps.yml",
    ".env.example",
    "README.md"
)
foreach ($t in $templates) {
    $src = Join-Path $PSScriptRoot $t
    if (-not (Test-Path $src)) { throw "missing template: $t" }
    Copy-Item $src $stageDir
}

# ── Stamp version into README + .env.example ─────────────────────────────────
$readmePath = Join-Path $stageDir "README.md"
(Get-Content $readmePath -Raw) `
    -replace "<VERSION>", $Version `
    -replace "<BUILD_TIME>", $buildTime `
    | Set-Content -Encoding utf8 $readmePath

# ── Zip ──────────────────────────────────────────────────────────────────────
Write-Host "==> Zipping to $zipPath..." -ForegroundColor Cyan
if (Test-Path $zipPath) { Remove-Item -Force $zipPath }
Compress-Archive -Path "$stageDir\*" -DestinationPath $zipPath -CompressionLevel Optimal

$size = "{0:N1} MB" -f ((Get-Item $zipPath).Length / 1MB)
Write-Host "==> Done." -ForegroundColor Green
Write-Host "    $zipPath  ($size)"
