$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

Get-Content .env.dev | ForEach-Object {
    if ($_ -match '^\s*export\s+(\w+)=["'']?(.+?)["'']?\s*$') {
        [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2], "Process")
    }
}

# Prepend TOOLS_PATH (from .env.dev, semicolon-separated) so the server can
# find fpcalc, ffmpeg, etc. Inherited by Start-Process below.
if ($env:TOOLS_PATH) {
    $env:Path = "$env:TOOLS_PATH;$env:Path"
}

Write-Host "==> Building frontend..." -ForegroundColor Cyan
Push-Location web
npm install
npm run build
Pop-Location
if (Test-Path internal\webui\dist) { Remove-Item -Recurse -Force internal\webui\dist }
Copy-Item -Recurse web\dist internal\webui\dist

Write-Host "==> Building server..." -ForegroundColor Cyan
try { $version = git describe --tags --always --dirty 2>$null } catch { }
if (-not $version) { $version = "dev" }
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
go build -ldflags "-X main.version=$version -X main.buildTime=$buildTime" -o bin\server.exe .\cmd\server
if ($LASTEXITCODE -ne 0) { throw "Go build failed" }

Write-Host "==> Running migrations..." -ForegroundColor Cyan
goose -dir internal\db\migrations postgres $env:DATABASE_URL up
if ($LASTEXITCODE -ne 0) { throw "Migration failed" }

Write-Host "==> Stopping old server..." -ForegroundColor Cyan
$oldProcs = Get-Process -Name server -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "*OnScreen*" }
if ($oldProcs) {
    # Graceful: send Ctrl+C so the Go signal handler runs pool.Close() / server.Shutdown().
    # GenerateConsoleCtrlEvent only works within the same console group, so we use
    # taskkill without /F which sends WM_CLOSE — Go maps this to os.Interrupt on Windows.
    foreach ($p in $oldProcs) {
        taskkill /PID $p.Id 2>$null
    }
    # Wait up to 10 s for the process to exit gracefully.
    $deadline = (Get-Date).AddSeconds(10)
    while ((Get-Date) -lt $deadline) {
        $still = Get-Process -Name server -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "*OnScreen*" }
        if (-not $still) { break }
        Start-Sleep -Milliseconds 500
    }
    # Force-kill if it didn't exit in time.
    Get-Process -Name server -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "*OnScreen*" } | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 500
}

Write-Host "==> Starting server..." -ForegroundColor Cyan
$logPath = Join-Path $PSScriptRoot "bin\server.log"
$proc = Start-Process -FilePath .\bin\server.exe -PassThru -WindowStyle Hidden `
    -RedirectStandardOutput $logPath -RedirectStandardError "$logPath.err"
Start-Sleep -Seconds 3

if (-not $proc.HasExited) {
    Write-Host "==> Server running (PID $($proc.Id)) on :7070" -ForegroundColor Green
    Write-Host "==> Logs: Get-Content $logPath -Wait" -ForegroundColor DarkGray
} else {
    Write-Host "==> Server failed to start!" -ForegroundColor Red
    if (Test-Path $logPath)      { Write-Host "--- stdout ---"; Get-Content $logPath -Tail 40 }
    if (Test-Path "$logPath.err"){ Write-Host "--- stderr ---"; Get-Content "$logPath.err" -Tail 40 }
    exit 1
}
