# Post-install. Two modes:
#   full   (default) — initialise local Postgres + Redis, create DB, migrate,
#                      register Postgres/Redis/OnScreen services. Single-box
#                      all-in-one server.
#   worker           — no local DB; write a .env pointing at a remote primary's
#                      shared DATABASE_URL + VALKEY_URL and register only the
#                      transcode worker service. Joins the primary's fleet.
#
# Called from Inno Setup's [Run] section. Must succeed in one pass — failures
# roll back the whole install.

[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$InstallDir,
    [string]$Mode = "full",
    [string]$WorkerConfig = ""
)

$ErrorActionPreference = "Stop"
$dataRoot = "$env:ProgramData\OnScreen"
$pgdata   = "$dataRoot\pgdata"
$logs     = "$dataRoot\logs"
$envFile  = "$InstallDir\.env"

New-Item -ItemType Directory -Path $dataRoot, $logs -Force | Out-Null

function Write-Log {
    param([string]$Msg)
    Add-Content -Path "$logs\install.log" -Value "$(Get-Date -Format o)  $Msg"
    Write-Host $Msg
}

# Random password / secret. PostgreSQL needs an ASCII-printable password
# without URL-special chars (we put it in DATABASE_URL); SECRET_KEY needs
# 32+ bytes of entropy. Use base64-of-random-bytes for both, stripped of
# +/= to keep the URL shape simple.
function New-Secret {
    param([int]$Bytes = 24)
    $b = New-Object byte[] $Bytes
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
    [Convert]::ToBase64String($b) -replace '[+/=]',''
}

# Rewrite a WinSW service XML's <env/> block from the .env file (plus a PATH
# entry that puts the bundled ffmpeg first), then expand {app}/{userpf}
# placeholders. Shared by the OnScreen server and the worker service.
function Set-ServiceEnv {
    param([string]$XmlName)
    $xmlPath = "$InstallDir\$XmlName"
    $xml = Get-Content $xmlPath -Raw
    $envLines = @('  <env name="PATH" value="{app}\ffmpeg;%PATH%"/>'.Replace('{app}', $InstallDir))
    Get-Content $envFile | ForEach-Object {
        if ($_ -match '^\s*(?:export\s+)?(\w+)\s*=\s*["'']?(.*?)["'']?\s*$') {
            $key = $Matches[1]; $val = $Matches[2]
            if ($key -and $key -notmatch '^\s*#' -and $key -ne "PATH") {
                $val = $val -replace '&','&amp;' -replace '<','&lt;' -replace '>','&gt;' -replace '"','&quot;'
                $envLines += "  <env name=`"$key`" value=`"$val`"/>"
            }
        }
    }
    $xml = $xml -replace '(?ms)\s*<env\s[^/]*/>', ''
    $envBlock = ($envLines -join "`n")
    $xml = $xml -replace '</service>', "`n$envBlock`n</service>"
    $xml = $xml -replace '\{app\}', $InstallDir
    $xml = $xml -replace '\{userpf\}', $env:ProgramData
    Set-Content -Path $xmlPath -Value $xml -Encoding utf8
}

function Register-WinswService {
    param([string]$XmlName)
    # WinSW locates its config by matching the running exe's own base name
    # (service-worker.exe -> service-worker.xml in the same dir), NOT by a path
    # argument. So copy WinSW.exe to a per-service name and invoke that — passing
    # the XML path fails with "Unable to locate WinSW.[xml|yml]".
    $base = [System.IO.Path]::GetFileNameWithoutExtension($XmlName)
    $svcExe = "$InstallDir\$base.exe"
    Copy-Item "$InstallDir\WinSW.exe" $svcExe -Force
    # Tear down any prior registration first so a reinstall/upgrade re-applies
    # cleanly — WinSW `install` errors if the service already exists, and the
    # MSI doesn't run preuninstall on an in-place upgrade. Best-effort: a fresh
    # install has nothing to stop/uninstall, and uninstall reads the service id
    # from the (already env-injected) XML so it targets the right service even
    # if a prior run registered it via a different exe name. Relax the Stop
    # preference here: on a fresh install these write to stderr, which under
    # ErrorActionPreference=Stop would otherwise throw and abort the install.
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    & $svcExe stop      2>&1 | ForEach-Object { Write-Log "  $_" }
    & $svcExe uninstall 2>&1 | ForEach-Object { Write-Log "  $_" }
    $ErrorActionPreference = $prevEAP
    Write-Log "Registering service from $XmlName (via $base.exe)"
    & $svcExe install 2>&1 | ForEach-Object { Write-Log "  $_" }
    if ($LASTEXITCODE -ne 0) { throw "$XmlName install failed (exit $LASTEXITCODE)" }
    & $svcExe start 2>&1 | ForEach-Object { Write-Log "  $_" }
    if ($LASTEXITCODE -ne 0) { throw "$XmlName start failed (exit $LASTEXITCODE)" }
}

# Register the worker as an onlogon INTERACTIVE scheduled task — NOT a Windows
# service. A service runs in session 0 with no access to the GPU, so NVENC/QSV
# probing fails and the worker crashes immediately (the historical "Event 7023"
# on the OnScreenWorker service). Running in the install user's interactive
# session keeps the same GPU path the manual `worker.exe` cmd run uses, which
# is the only thing that ever actually worked on Windows.
function Register-WorkerTask {
    $launcher = "$InstallDir\run-worker.ps1"
    # Launcher: load .env into the process env (values stay in-process — never
    # echoed to a command line or a service config), then exec worker.exe with
    # output appended to the same console log the old WinSW setup wrote to.
    # Single-quoted here-string keeps the script literal; placeholders substituted below.
    $launcherTpl = @'
Set-Location '__INSTALLDIR__'
foreach ($line in Get-Content '__ENVFILE__') {
  $t = $line.Trim()
  if (-not $t -or $t.StartsWith('#')) { continue }
  if ($t.StartsWith('export ')) { $t = $t.Substring(7).Trim() }
  $i = $t.IndexOf('='); if ($i -lt 1) { continue }
  [Environment]::SetEnvironmentVariable($t.Substring(0,$i).Trim(), $t.Substring($i+1).Trim().Trim('"'), 'Process')
}
& '__INSTALLDIR__\worker.exe' *>> '__INSTALLDIR__\worker-console.log'
'@
    ($launcherTpl -replace '__INSTALLDIR__', $InstallDir -replace '__ENVFILE__', $envFile) |
        Set-Content -Encoding utf8 $launcher

    # If an earlier (broken) install left a WinSW worker service, tear it down so
    # it doesn't fight the new task for the same WORKER_ADDR port on next boot.
    $svcExe = "$InstallDir\service-worker.exe"
    if (Test-Path $svcExe) {
        $prevEAP = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
        & $svcExe stop      2>&1 | ForEach-Object { Write-Log "  legacy-svc: $_" }
        & $svcExe uninstall 2>&1 | ForEach-Object { Write-Log "  legacy-svc: $_" }
        $ErrorActionPreference = $prevEAP
    }

    $user     = "$env:USERDOMAIN\$env:USERNAME"
    $taskName = 'OnScreenWorker'
    # Replace any prior task so reinstalls re-apply cleanly.
    Unregister-ScheduledTask -TaskName $taskName -Confirm:$false -ErrorAction SilentlyContinue

    # Avoid backtick line-continuations (a trailing space silently breaks them).
    # The Argument is the powershell launcher invocation with the quoted -File path.
    $arg       = '-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $launcher + '"'
    $action    = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument $arg
    $trigger   = New-ScheduledTaskTrigger -AtLogOn -User $user
    # Interactive + Highest: runs in the user's logged-on session (GPU access)
    # with admin elevation (binds the segment port, writes to Program Files).
    $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Highest
    # Auto-restart so a transient primary outage doesn't strand the worker until
    # the next logon. ExecutionTimeLimit 0 = no kill on long uptime (it's a daemon).
    $settings  = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 99 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit (New-TimeSpan -Seconds 0)
    Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Start-ScheduledTask -TaskName $taskName
    Write-Log "Worker registered as scheduled task '$taskName' (onlogon, interactive, as $user) and started."
}

# ─────────────────────────────── WORKER MODE ───────────────────────────────
if ($Mode -eq "worker") {
    Write-Log "Worker-only install."
    if (-not (Test-Path $WorkerConfig)) { throw "worker config file not found: $WorkerConfig" }

    # Parse the KEY=VALUE lines the installer wizard wrote.
    $cfg = @{}
    Get-Content $WorkerConfig | ForEach-Object {
        if ($_ -match '^\s*(\w+)\s*=\s*(.*)$') { $cfg[$Matches[1]] = $Matches[2].Trim() }
    }
    foreach ($k in @("DATABASE_URL", "VALKEY_URL", "SECRET_KEY", "WORKER_ADDR")) {
        if (-not $cfg.ContainsKey($k) -or -not $cfg[$k]) { throw "worker config missing $k" }
    }

    # Write .env — the worker connects to the PRIMARY's shared DB + Valkey.
    # No local Postgres/Redis is initialised or started.
    @"
# Auto-generated by OnScreen installer (worker-only mode). Points at the
# primary OnScreen server's shared database + Valkey over the network.
# SECRET_KEY must match the primary's exactly.
DATABASE_URL="$($cfg.DATABASE_URL)"
VALKEY_URL="$($cfg.VALKEY_URL)"
SECRET_KEY="$($cfg.SECRET_KEY)"
WORKER_ADDR="$($cfg.WORKER_ADDR)"
LOG_LEVEL="info"
"@ | Set-Content -Encoding utf8 $envFile
    Write-Log "worker .env written (worker_addr=$($cfg.WORKER_ADDR))"

    # Open the worker's segment port (the WORKER_ADDR port) inbound so the
    # primary can pull transcoded segments from this node — without it the
    # worker registers and looks healthy but playback from it stalls.
    # Scoped to the local subnet (where the primary lives).
    $workerPort = ($cfg.WORKER_ADDR -split ':')[-1]
    if ($workerPort -notmatch '^\d+$') { $workerPort = '7073' }
    $fwName = 'OnScreen Worker (segments)'
    if (Get-NetFirewallRule -DisplayName $fwName -ErrorAction SilentlyContinue) {
        Write-Log "firewall rule '$fwName' already exists"
    } else {
        New-NetFirewallRule -DisplayName $fwName -Direction Inbound -Protocol TCP -LocalPort $workerPort -Action Allow -RemoteAddress LocalSubnet | Out-Null
        Write-Log "firewall: opened inbound tcp/$workerPort (LocalSubnet) for segment fetches"
    }

    Register-WorkerTask
    Write-Log "Worker registered + started. It joins the primary's fleet once it connects; check Settings -> Transcode on the primary."
    return
}

# ──────────────────────────────── FULL MODE ────────────────────────────────

# ── 1. Initialize Postgres cluster (idempotent — skip if a previous install
#       already initdb'd, so a repair doesn't lose data). ──
$pgInitMarker = "$pgdata\PG_VERSION"
if (-not (Test-Path $pgInitMarker)) {
    Write-Log "Postgres: initdb $pgdata"
    if (Test-Path $pgdata) { Remove-Item -Recurse -Force $pgdata }
    New-Item -ItemType Directory -Path $pgdata -Force | Out-Null

    $pgPassword = New-Secret 24
    $pwFile = Join-Path $env:TEMP "onscreen-pg-pw.txt"
    [System.IO.File]::WriteAllText($pwFile, $pgPassword, [System.Text.Encoding]::ASCII)

    $initdb = "$InstallDir\pgsql\bin\initdb.exe"
    & $initdb -D $pgdata -U postgres --pwfile $pwFile -E UTF8 --locale=C 2>&1 |
        ForEach-Object { Write-Log "  initdb: $_" }
    if ($LASTEXITCODE -ne 0) { throw "initdb failed (exit $LASTEXITCODE)" }
    Remove-Item -Force $pwFile

    $confPath = "$pgdata\postgresql.conf"
    (Get-Content $confPath) -replace "^#?\s*port\s*=.*", "port = 5432" |
        Set-Content -Encoding ASCII $confPath
} else {
    Write-Log "Postgres: cluster already initialised; skipping initdb"
    if (Test-Path $envFile) {
        $existing = Get-Content $envFile | Where-Object { $_ -match '^DATABASE_URL=' }
        if ($existing -and $existing -match 'postgres://postgres:([^@]+)@') {
            $pgPassword = [uri]::UnescapeDataString($Matches[1])
        }
    }
    if (-not $pgPassword) {
        throw "Existing Postgres cluster found but no recoverable password in .env; cannot proceed safely"
    }
}

# ── 2. Start Postgres briefly to create the onscreen database (fresh only). ──
if (-not (Test-Path "$dataRoot\.db-bootstrapped")) {
    Write-Log "Postgres: starting one-shot for bootstrap"
    $pgctl = "$InstallDir\pgsql\bin\pg_ctl.exe"
    & $pgctl -D $pgdata -l "$logs\pg-bootstrap.log" -w start 2>&1 |
        ForEach-Object { Write-Log "  pg_ctl start: $_" }
    if ($LASTEXITCODE -ne 0) { throw "pg_ctl start failed (exit $LASTEXITCODE)" }
    try {
        $env:PGPASSWORD = $pgPassword
        $psql = "$InstallDir\pgsql\bin\psql.exe"
        & $psql -h localhost -U postgres -tAc "CREATE DATABASE onscreen;" 2>&1 |
            ForEach-Object { Write-Log "  psql: $_" }
        if ($LASTEXITCODE -ne 0) { throw "CREATE DATABASE failed" }
        New-Item -ItemType File -Path "$dataRoot\.db-bootstrapped" -Force | Out-Null
    } finally {
        & $pgctl -D $pgdata -m fast -w stop 2>&1 |
            ForEach-Object { Write-Log "  pg_ctl stop: $_" }
        $env:PGPASSWORD = $null
    }
}

# ── 3. Generate .env (fresh only). ──
if (-not (Test-Path $envFile)) {
    $secretKey = New-Secret 32
    $pgUrl = "postgres://postgres:$([uri]::EscapeDataString($pgPassword))@localhost:5432/onscreen?sslmode=disable"
    @"
# Auto-generated by OnScreen installer. Edit if you know what you're doing —
# DATABASE_URL's password matches the bundled Postgres cluster's `postgres`
# role; changing it here without rotating in the database will break startup.
DATABASE_URL="$pgUrl"
VALKEY_URL="redis://localhost:6379"
SECRET_KEY="$secretKey"
LOG_LEVEL="info"
"@ | Set-Content -Encoding utf8 $envFile
    Write-Log ".env written"
} else {
    Write-Log ".env already present; preserved"
}

# ── 4. Inject .env into the OnScreen service XML; expand placeholders in the
#       Postgres + Redis XMLs. ──
Set-ServiceEnv "service-onscreen.xml"
foreach ($svc in @("service-postgres.xml", "service-redis.xml")) {
    $p = Join-Path $InstallDir $svc
    (Get-Content $p -Raw) `
        -replace '\{app\}', $InstallDir `
        -replace '\{userpf\}', $env:ProgramData `
        | Set-Content -Path $p -Encoding utf8
}

# ── 5. Register Postgres + Redis (OnScreen depends on them). ──
Register-WinswService "service-postgres.xml"
Register-WinswService "service-redis.xml"

# ── 6. Apply database migrations BEFORE starting OnScreen. ──
# The server does not auto-migrate — it only gates on schema version — so a
# fresh cluster has no schema and the server would crash-loop on its first DB
# query. Mirrors the Linux installer's migrate.sh / systemd ExecStartPre.
Write-Log "Waiting for Postgres to accept connections..."
$pgIsReady = "$InstallDir\pgsql\bin\pg_isready.exe"
$pgUp = $false
for ($i = 0; $i -lt 60; $i++) {
    & $pgIsReady -h localhost -p 5432 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) { $pgUp = $true; break }
    Start-Sleep -Milliseconds 500
}
if (-not $pgUp) { throw "Postgres did not become ready within 30 s; cannot migrate" }

Write-Log "Applying database migrations..."
$dbUrl = ((Get-Content $envFile | Where-Object { $_ -match '^DATABASE_URL=' }) -replace '^DATABASE_URL=', '').Trim().Trim('"')
$env:DATABASE_URL = $dbUrl
& "$InstallDir\server.exe" migrate 2>&1 | ForEach-Object { Write-Log "  migrate: $_" }
$migrateExit = $LASTEXITCODE
$env:DATABASE_URL = $null
if ($migrateExit -ne 0) { throw "database migration failed (exit $migrateExit)" }

# ── 6b. Open the API/UI port to the LAN. Lets other devices reach the web UI
#       and, in a fleet, lets remote workers pull source media + transcoded
#       segments from this primary over HTTP (TranscodeJob.SourceURL). Scoped
#       to the local subnet; every request is still auth-gated.
#
#       Postgres (5432) and Valkey (6379) stay loopback-only by default —
#       exposing them to the LAN is what lets a worker JOIN the fleet, but it's
#       a deliberate admin decision (the worker needs the shared DATABASE_URL /
#       VALKEY_URL), not something a single-box install should do silently.
$fwServer = 'OnScreen Server (LAN)'
if (Get-NetFirewallRule -DisplayName $fwServer -ErrorAction SilentlyContinue) {
    Write-Log "firewall rule '$fwServer' already exists"
} else {
    New-NetFirewallRule -DisplayName $fwServer -Direction Inbound -Protocol TCP -LocalPort 7070 -Action Allow -RemoteAddress LocalSubnet | Out-Null
    Write-Log "firewall: opened inbound tcp/7070 (LocalSubnet) for UI + fleet source/segment fetches"
}

Register-WinswService "service-onscreen.xml"

Write-Log "All services up. Open http://localhost:7070 for setup."
Write-Log "To attach remote workers, also expose Postgres (5432) + Valkey (6379) to the LAN — see Settings -> Transcode on the primary."
