# Bring up the deps stack (Postgres + Valkey) on the test box.
#
# Requires Docker Desktop installed and running. Sample DATABASE_URL /
# VALKEY_URL in .env.example point at this stack's exposed ports
# (5432 / 6379 on localhost) so OnScreen on the host can reach them.

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$compose = Join-Path $PSScriptRoot "docker-compose.deps.yml"
if (-not (Test-Path $compose)) { throw "docker-compose.deps.yml missing." }

# Sanity check: Docker reachable.
docker version --format '{{.Server.Version}}' 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Docker isn't reachable. Install Docker Desktop and start it before running this script." -ForegroundColor Red
    exit 1
}

Write-Host "==> Starting Postgres + Valkey..." -ForegroundColor Cyan
docker compose -f $compose up -d
if ($LASTEXITCODE -ne 0) { throw "docker compose up failed (exit $LASTEXITCODE)" }

Write-Host "==> Waiting for Postgres ready (up to 60s)..." -ForegroundColor Cyan
$deadline = (Get-Date).AddSeconds(60)
while ((Get-Date) -lt $deadline) {
    docker compose -f $compose exec -T postgres pg_isready -U onscreen 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { break }
    Start-Sleep -Seconds 1
}
if ($LASTEXITCODE -ne 0) {
    Write-Host "WARN: Postgres didn't report ready in 60s; continuing anyway. Check `docker compose logs`." -ForegroundColor Yellow
}

Write-Host "==> Done." -ForegroundColor Green
Write-Host "    Postgres: localhost:5432  (user=onscreen pass=onscreen db=onscreen)"
Write-Host "    Valkey:   localhost:6379"
Write-Host
Write-Host "    Stop with: docker compose -f docker-compose.deps.yml down"
