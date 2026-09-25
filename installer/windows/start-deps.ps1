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

# Postgres password. docker-compose.deps.yml has no default any more (the old
# one, "onscreen", is public and the role is a superuser reachable by every
# local process on the loopback port). Compose reads DB_PASS from .env in this
# directory; generate a random one on first run and point DATABASE_URL at it.
$envPath = Join-Path $PSScriptRoot ".env"
if (-not (Test-Path $envPath)) {
    Write-Host "ERROR: .env not found. Copy .env.example to .env (and set SECRET_KEY) first." -ForegroundColor Red
    exit 1
}
$envText = Get-Content $envPath -Raw
if ($envText -notmatch '(?m)^\s*(?:export\s+)?DB_PASS="?[^"\s]') {
    $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
    docker volume inspect onscreen-deps_postgres_data 2>$null | Out-Null
    $volumeExists = ($LASTEXITCODE -eq 0)
    $ErrorActionPreference = $prevEAP
    if ($volumeExists) {
        # Existing volume: it keeps the password it was initialised with,
        # almost certainly the old default. Keep it working; say how to rotate.
        Add-Content -Path $envPath -Value 'DB_PASS="onscreen"'
        Write-Host "WARNING: existing Postgres volume found; set DB_PASS=`"onscreen`" (the old public default) in .env so it keeps working." -ForegroundColor Yellow
        Write-Host "         Rotate: docker compose -f docker-compose.deps.yml exec postgres psql -U onscreen -c `"ALTER ROLE onscreen PASSWORD '<new>'`"" -ForegroundColor Yellow
        Write-Host "         then update DB_PASS and the password in DATABASE_URL in .env." -ForegroundColor Yellow
    } else {
        $bytes = New-Object byte[] 24
        [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
        $newPass = -join ($bytes | ForEach-Object { $_.ToString('x2') })
        $envText = $envText -replace 'postgres://onscreen:(?:onscreen|CHANGE_ME_DB_PASS)@', "postgres://onscreen:$newPass@"
        if (-not $envText.EndsWith("`n")) { $envText += "`r`n" }
        $envText += "DB_PASS=`"$newPass`"`r`n"
        # UTF-8 without BOM (PS 5.1's -Encoding utf8 adds one, which the
        # compose .env parser and the service installer's parser don't expect).
        [System.IO.File]::WriteAllText($envPath, $envText, (New-Object System.Text.UTF8Encoding($false)))
        Write-Host "==> Generated a random DB_PASS in .env (DATABASE_URL updated to match)." -ForegroundColor Cyan
    }
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
Write-Host "    Postgres: 127.0.0.1:5432  (user=onscreen db=onscreen; password = DB_PASS in .env)"
Write-Host "    Valkey:   localhost:6379"
Write-Host
Write-Host "    Stop with: docker compose -f docker-compose.deps.yml down"
