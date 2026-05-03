# Build a portable Linux tarball for OnScreen.
#
# Output: dist/onscreen-linux-amd64-<version>.tar.gz
#
# Contents:
#   - server / worker / devtoken      (Go binaries, linux/amd64, static)
#   - ffmpeg/ffmpeg + ffprobe         (John Van Sickle static build, ships
#                                      with NVENC + VAAPI + libsvtav1 + libdav1d)
#   - start.sh                        (foreground launch)
#   - install-service.sh              (registers systemd unit)
#   - uninstall-service.sh            (removes the unit)
#   - start-deps.sh                   (docker compose up Postgres + Valkey)
#   - onscreen.service                (systemd unit template)
#   - docker-compose.deps.yml         (deps-only stack, mirrors Windows)
#   - .env.example                    (config template, Linux paths)
#   - README.md                       (quickstart)
#
# Cross-builds from Windows or runs natively on Linux. Go's CGO_ENABLED=0
# static build for linux/amd64 works identically from either host. The
# bundled ffmpeg is a static x86_64 build so it runs on glibc 2.17+ and
# musl-based distros without any system deps.
#
# Prereqs on the build host:
#   - Go 1.22+ on PATH
#   - Node.js 20+ + npm
#   - PowerShell 5.1+ (or pwsh 7+) — same shell the Windows installer uses;
#     keeps the build entrypoint identical across the two platforms.
#   - tar (Windows 10+ ships bsdtar at C:\Windows\System32\tar.exe — works
#     out of the box; on Linux it's universal).

[CmdletBinding()]
param(
    [string]$Version = "",
    [switch]$SkipFrontend = $false,
    [switch]$NoFfmpeg = $false   # smaller tarball without bundled ffmpeg
)

$ErrorActionPreference = "Stop"
$root = Resolve-Path "$PSScriptRoot\..\.."
Set-Location $root

# Force TLS 1.2 — Windows PowerShell 5.1 negotiates 1.0/1.1 by default and
# johnvansickle.com / github.com both reject those.
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12 -bor [Net.ServicePointManager]::SecurityProtocol

if (-not $Version) {
    # See installer/windows/build.ps1 for why we prefer the VERSION
    # file over `git describe` — the v2.0.0 tag isn't in main's history
    # so describe stamps the wrong major number.
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
$bundleName = "onscreen-linux-amd64-$Version"
$tarName  = "$bundleName.tar.gz"
$tarPath  = Join-Path $distDir $tarName

New-Item -ItemType Directory -Path $cacheDir, $distDir -Force | Out-Null
if (Test-Path $stageDir) { Remove-Item -Recurse -Force $stageDir }
$bundleDir = Join-Path $stageDir $bundleName
New-Item -ItemType Directory -Path $bundleDir | Out-Null

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

# ── Go binaries (linux/amd64, static) ────────────────────────────────────────
Write-Host "==> Building Go binaries (linux/amd64)..." -ForegroundColor Cyan
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"   # static — runs on any glibc + musl distro

$binaries = @(
    @{ Pkg = "./cmd/server";   Out = "server" }
    @{ Pkg = "./cmd/worker";   Out = "worker" }
    @{ Pkg = "./cmd/devtoken"; Out = "devtoken" }
)
foreach ($b in $binaries) {
    $outPath = Join-Path $bundleDir $b.Out
    Write-Host "    -> $($b.Out)" -ForegroundColor Gray
    go build -ldflags "$ldflags" -o $outPath $b.Pkg
    if ($LASTEXITCODE -ne 0) { throw "go build $($b.Pkg) failed" }
}

# ── ffmpeg ───────────────────────────────────────────────────────────────────
if (-not $NoFfmpeg) {
    # John Van Sickle's static build — has NVENC, VAAPI, libsvtav1, libdav1d.
    # Single static tarball; we extract just ffmpeg + ffprobe binaries. The
    # "release" channel is preferred over "git" for predictability — release
    # is tied to a numbered upstream version, not a moving HEAD.
    $ffmpegUrl = "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz"
    $ffmpegArchive = Join-Path $cacheDir "ffmpeg-release-amd64-static.tar.xz"
    if (-not (Test-Path $ffmpegArchive)) {
        Write-Host "==> Downloading ffmpeg (johnvansickle static)..." -ForegroundColor Cyan
        Invoke-WebRequest -Uri $ffmpegUrl -OutFile $ffmpegArchive -UseBasicParsing
    }
    $ffmpegStage = Join-Path $cacheDir "ffmpeg-extract"
    if (Test-Path $ffmpegStage) { Remove-Item -Recurse -Force $ffmpegStage }
    New-Item -ItemType Directory -Path $ffmpegStage | Out-Null
    Write-Host "==> Extracting ffmpeg..." -ForegroundColor Cyan
    # bsdtar (shipped with Windows 10+ at System32\tar.exe) handles .tar.xz
    # natively — same on every Linux distro. -C extracts into the target.
    tar -xf $ffmpegArchive -C $ffmpegStage
    if ($LASTEXITCODE -ne 0) { throw "tar extract failed for $ffmpegArchive" }
    $extractedBin = Get-ChildItem -Path $ffmpegStage -Recurse -Filter "ffmpeg" -File | Where-Object { $_.Name -eq "ffmpeg" } | Select-Object -First 1
    if (-not $extractedBin) { throw "ffmpeg binary not found inside johnvansickle tarball" }
    $bundledFfmpegDir = Join-Path $bundleDir "ffmpeg"
    New-Item -ItemType Directory -Path $bundledFfmpegDir -Force | Out-Null
    Copy-Item (Join-Path $extractedBin.DirectoryName "ffmpeg")  $bundledFfmpegDir
    Copy-Item (Join-Path $extractedBin.DirectoryName "ffprobe") $bundledFfmpegDir
} else {
    Write-Host "==> Skipping ffmpeg bundle (-NoFfmpeg)" -ForegroundColor Yellow
}

# ── Static templates ─────────────────────────────────────────────────────────
Write-Host "==> Copying templates..." -ForegroundColor Cyan
$templates = @(
    "onscreen.service",
    "start.sh",
    "install-service.sh",
    "uninstall-service.sh",
    "start-deps.sh",
    "docker-compose.deps.yml",
    ".env.example",
    "README.md"
)
foreach ($t in $templates) {
    $src = Join-Path $PSScriptRoot $t
    if (-not (Test-Path $src)) { throw "missing template: $t" }
    Copy-Item $src $bundleDir
}

# ── Stamp version into README ─────────────────────────────────────────────────
$readmePath = Join-Path $bundleDir "README.md"
(Get-Content $readmePath -Raw) `
    -replace "<VERSION>", $Version `
    -replace "<BUILD_TIME>", $buildTime `
    | Set-Content -Encoding utf8 -NoNewline $readmePath

# ── Tarball ──────────────────────────────────────────────────────────────────
Write-Host "==> Tarballing to $tarPath..." -ForegroundColor Cyan
if (Test-Path $tarPath) { Remove-Item -Force $tarPath }
# Use tar with -C $stageDir so the archive contains the bundle dir as
# its top-level entry (extracts to onscreen-linux-amd64-<ver>/...).
# Pass --owner=0 --group=0 so the archive isn't tagged with the
# build-host's user — keeps install-service.sh's chown step idempotent.
Push-Location $stageDir
tar --owner=0 --group=0 -czf $tarPath $bundleName
Pop-Location
if ($LASTEXITCODE -ne 0) { throw "tar create failed for $tarPath" }

$size = "{0:N1} MB" -f ((Get-Item $tarPath).Length / 1MB)
Write-Host "==> Done." -ForegroundColor Green
Write-Host "    $tarPath  ($size)"
