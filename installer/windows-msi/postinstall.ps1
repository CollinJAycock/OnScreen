# Post-install. Two modes:
#   full   (default) - initialise local Postgres + Redis, create DB, migrate,
#                      register Postgres/Redis/OnScreen services. Single-box
#                      all-in-one server.
#   worker           - no local DB; write a .env pointing at a remote primary's
#                      shared DATABASE_URL + VALKEY_URL and register only the
#                      transcode worker service. Joins the primary's fleet.
#
# Called from Inno Setup's [Run] section. Must succeed in one pass - failures
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

# -- ACL hardening ------------------------------------------------------------
# The services (OnScreen, Postgres, Redis) run as LocalSystem, so anything a
# non-admin user can WRITE in their binaries/config/data dirs is a path to
# SYSTEM, and anything they can READ in .env / the service XML hands them
# SECRET_KEY (forge admin tokens, decrypt stored API keys) and the Postgres
# superuser DSN. Well-known SIDs, not names, so this works on non-English
# Windows.
$SidSystem = '*S-1-5-18'      # NT AUTHORITY\SYSTEM
$SidAdmins = '*S-1-5-32-544'  # BUILTIN\Administrators
$SidUsers  = '*S-1-5-32-545'  # BUILTIN\Users

function Invoke-Icacls {
    param([string[]]$IcaclsArgs)
    # Function-local: judge by exit code, not by stderr text (PS 5.1 turns
    # native stderr into terminating errors under EAP=Stop).
    $ErrorActionPreference = 'Continue'
    $out = & icacls.exe @IcaclsArgs 2>&1
    if ($LASTEXITCODE -ne 0) { throw "icacls $($IcaclsArgs -join ' ') failed (exit $LASTEXITCODE): $out" }
}

# Replace a directory tree's ACL with SYSTEM + Administrators full control
# (plus Users read/execute when asked), dropping what it inherited from its
# parent - e.g. "Authenticated Users: Modify" when installed under C:\ - and any
# explicit ACEs on children, such as the "users-modify" grants older installers
# put on %ProgramData%\OnScreen\pgdata and \logs.
function Set-RestrictedTreeAcl {
    param([string]$Path, [switch]$UsersReadExecute)
    $grants = @("${SidSystem}:(OI)(CI)F", "${SidAdmins}:(OI)(CI)F")
    if ($UsersReadExecute) { $grants += "${SidUsers}:(OI)(CI)RX" }
    Invoke-Icacls (@($Path, '/inheritance:r', '/grant:r') + $grants)
    if (Get-ChildItem -LiteralPath $Path -Force -ErrorAction SilentlyContinue | Select-Object -First 1) {
        # Best-effort on children: one locked file must not abort the install
        # (the root ACL above, which is what new files inherit, already held).
        $ErrorActionPreference = 'Continue'
        $out = & icacls.exe "$Path\*" /reset /T /C /Q 2>&1
        if ($LASTEXITCODE -ne 0) { Write-Log "WARN: icacls /reset under $Path reported errors: $out" }
    }
}

# Secret-bearing files: SYSTEM + Administrators only, nothing inherited.
# $ExtraReader optionally gets read (e.g. the worker task's account).
function Protect-SecretFile {
    param([string]$Path, [string]$ExtraReader = "")
    if (-not (Test-Path -LiteralPath $Path)) { return }
    $grants = @("${SidSystem}:F", "${SidAdmins}:F")
    if ($ExtraReader) { $grants += "${ExtraReader}:R" }
    Invoke-Icacls (@($Path, '/inheritance:r', '/grant:r') + $grants)
}

# initdb.exe, and the postmaster that pg_ctl starts, never run with this
# script's admin token: Windows Postgres refuses admin rights, so both drop
# to a RESTRICTED copy of it (BUILTIN\Administrators becomes deny-only; the
# user SID stays). Under the SYSTEM + Administrators ACL that copy can reach
# pgdata / logs only through an ACE for the installing account's own SID, so
# initdb and the bootstrap start (fresh installs only) get one for exactly
# that window, removed again right after. Not "Users": any local account
# could then read the cluster. When this runs AS SYSTEM (silent/managed
# deployment) the permanent SYSTEM ACE already covers the restricted token,
# and granting/revoking "the installer" would strip SYSTEM's own ACE.
$InstallerSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$script:InstallerPgGrant = $false

function Grant-InstallerPgAccess {
    if ($InstallerSid -eq 'S-1-5-18' -or $script:InstallerPgGrant) { return }
    # Set first: if the second grant fails, the finally still revokes the first.
    $script:InstallerPgGrant = $true
    foreach ($p in @($pgdata, $logs)) {
        Invoke-Icacls @($p, '/grant', "*${InstallerSid}:(OI)(CI)M")
    }
    Write-Log "ACLs: temporary Modify on pgdata + logs for the installing account ($InstallerSid) during initdb/bootstrap"
}

# Best-effort (runs from a finally): a failure here leaves only the
# installing ADMIN account with access, never a non-admin, so it warns
# instead of masking the error that got us here.
function Revoke-InstallerPgAccess {
    if (-not $script:InstallerPgGrant) { return }
    try {
        foreach ($p in @($pgdata, $logs)) {
            if (Test-Path -LiteralPath $p) { Invoke-Icacls @($p, '/remove:g', "*$InstallerSid") }
        }
        # Re-apply the tree ACL so everything initdb / the bootstrap postmaster
        # created is back to inherit-only SYSTEM + Administrators.
        Set-RestrictedTreeAcl -Path $dataRoot
        $script:InstallerPgGrant = $false
        Write-Log "ACLs: installing account's temporary access removed; $dataRoot is SYSTEM/Administrators only"
    } catch {
        Write-Log "WARN: could not remove the installing account's temporary access to ${pgdata} / ${logs}: $($_.Exception.Message)"
    }
}

function Write-Log {
    param([string]$Msg)
    Add-Content -Path "$logs\install.log" -Value "$(Get-Date -Format o)  $Msg"
    Write-Host $Msg
}

# Applied on every run, so upgrades of older installs are fixed too.
Set-RestrictedTreeAcl -Path $dataRoot
Set-RestrictedTreeAcl -Path $InstallDir -UsersReadExecute
Protect-SecretFile $envFile
Write-Log "ACLs: $dataRoot and $InstallDir restricted (SYSTEM/Administrators; Users read-only on the install dir)"

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
    # PATH includes the bundled ffmpeg AND pgsql\bin. The latter is what
    # makes the backup / restore endpoints work out-of-box on Windows -
    # dbtools.Find also resolves them via the install layout, but having
    # them on PATH keeps psql / pg_isready usable for ad-hoc maintenance.
    $envLines = @('  <env name="PATH" value="{app}\ffmpeg;{app}\pgsql\bin;%PATH%"/>'.Replace('{app}', $InstallDir))
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
    # argument. So copy WinSW.exe to a per-service name and invoke that - passing
    # the XML path fails with "Unable to locate WinSW.[xml|yml]".
    $base = [System.IO.Path]::GetFileNameWithoutExtension($XmlName)
    $svcExe = "$InstallDir\$base.exe"
    Copy-Item "$InstallDir\WinSW.exe" $svcExe -Force
    # Tear down any prior registration first so a reinstall/upgrade re-applies
    # cleanly - WinSW `install` errors if the service already exists, and the
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

# Register the worker as an onlogon INTERACTIVE scheduled task - NOT a Windows
# service. A service runs in session 0 with no access to the GPU, so NVENC/QSV
# probing fails and the worker crashes immediately (the historical "Event 7023"
# on the OnScreenWorker service). Running in the install user's interactive
# session keeps the same GPU path the manual `worker.exe` cmd run uses, which
# is the only thing that ever actually worked on Windows.
function Register-WorkerTask {
    $launcher = "$InstallDir\run-worker.ps1"
    # Launcher: load .env into the process env (values stay in-process - never
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

# ------------------------------- WORKER MODE -------------------------------
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

    # Write .env - the worker connects to the PRIMARY's shared DB + Valkey.
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
    # Holds the primary's DB DSN + SECRET_KEY: SYSTEM/Administrators only, plus
    # read for the account the OnScreenWorker task runs as.
    Protect-SecretFile $envFile "$env:USERDOMAIN\$env:USERNAME"
    Write-Log "worker .env written (worker_addr=$($cfg.WORKER_ADDR))"

    # Open the worker's segment port (the WORKER_ADDR port) inbound so the
    # primary can pull transcoded segments from this node - without it the
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

# -------------------------------- FULL MODE --------------------------------

# Steps 1 + 2 run initdb / a pg_ctl-started postmaster, i.e. under the
# restricted token that needs the temporary installer grant (see
# Grant-InstallerPgAccess). The finally takes that grant away again whether
# or not they succeed. Upgrades skip both steps and never grant anything.
try {
    # -- 1. Initialize Postgres cluster (idempotent - skip if a previous install
    #       already initdb'd, so a repair doesn't lose data). --
    $pgInitMarker = "$pgdata\PG_VERSION"
    if (-not (Test-Path $pgInitMarker)) {
        Write-Log "Postgres: initdb $pgdata"
        if (Test-Path $pgdata) { Remove-Item -Recurse -Force $pgdata }
        New-Item -ItemType Directory -Path $pgdata -Force | Out-Null
        # After (re)creating pgdata: the grant must sit on this directory.
        Grant-InstallerPgAccess

        $pgPassword = New-Secret 24
        $pwFile = Join-Path $env:TEMP "onscreen-pg-pw.txt"
        [System.IO.File]::WriteAllText($pwFile, $pgPassword, [System.Text.Encoding]::ASCII)

        $initdb = "$InstallDir\pgsql\bin\initdb.exe"
        # --auth=scram-sha-256: initdb's default method is "trust", which let ANY
        # local process connect as the postgres superuser with no password (and
        # from there run OS commands via COPY ... TO PROGRAM as the service
        # account). Require the generated password for every connection instead.
        & $initdb -D $pgdata -U postgres --pwfile $pwFile --auth=scram-sha-256 -E UTF8 --locale=C 2>&1 |
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

    # -- 1b. Upgrade: clusters created by older installers use pg_hba "trust"
    #        (initdb's default), i.e. no password check for any local process.
    #        Switch those lines to scram-sha-256. The postgres role's password was
    #        set from --pwfile at initdb and stored as SCRAM (PG >= 14 default), so
    #        the DATABASE_URL in .env keeps working. Verified after Postgres starts
    #        (step 6) and rolled back automatically if the password doesn't match.
    $pgHba = "$pgdata\pg_hba.conf"
    $pgHbaBackup = "$pgdata\pg_hba.conf.pre-scram"
    $pgHbaChanged = $false
    if (Test-Path $pgHba) {
        $hbaLines = Get-Content $pgHba
        $trustRe = '^(\s*(?:local|host|hostssl|hostnossl|hostgssenc|hostnogssenc)\s.*\s)trust(\s*(?:#.*)?)$'
        if ($hbaLines | Where-Object { $_ -match $trustRe }) {
            Copy-Item $pgHba $pgHbaBackup -Force
            ($hbaLines | ForEach-Object { $_ -replace $trustRe, '${1}scram-sha-256${2}' }) |
                Set-Content -Encoding ASCII $pgHba
            $pgHbaChanged = $true
            Write-Log "Postgres: pg_hba.conf trust -> scram-sha-256 (backup: $pgHbaBackup)"
        }
    }

    # -- 2. Start Postgres briefly to create the onscreen database (fresh only). --
    if (-not (Test-Path "$dataRoot\.db-bootstrapped")) {
        # The postmaster (and the cmd.exe that opens -l's log file) run under
        # the restricted token: they need pgdata AND logs. No-op if step 1
        # already granted.
        Grant-InstallerPgAccess
        Write-Log "Postgres: starting one-shot for bootstrap"
        $pgctl = "$InstallDir\pgsql\bin\pg_ctl.exe"
        # NOT "& pg_ctl ... start 2>&1 | ForEach-Object": the cmd.exe and
        # postmaster pg_ctl leaves running inherit that output pipe, so the
        # pipeline never sees EOF and the install hangs until Postgres stops.
        # Start pg_ctl with no pipe (it shares this script's hidden console) and
        # wait for pg_ctl alone; the server's own output goes to -l's log.
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = $pgctl
        $psi.Arguments = "-D `"$pgdata`" -l `"$logs\pg-bootstrap.log`" -w start"
        $psi.UseShellExecute = $false
        $pgStart = [System.Diagnostics.Process]::Start($psi)
        $pgStart.WaitForExit()
        if ($pgStart.ExitCode -ne 0) { throw "pg_ctl start failed (exit $($pgStart.ExitCode)); see $logs\pg-bootstrap.log" }
        Write-Log "  pg_ctl start: server started"
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
} finally {
    Revoke-InstallerPgAccess
}

# -- 3. Generate .env (fresh only). --
if (-not (Test-Path $envFile)) {
    $secretKey = New-Secret 32
    $pgUrl = "postgres://postgres:$([uri]::EscapeDataString($pgPassword))@localhost:5432/onscreen?sslmode=disable"
    @"
# Auto-generated by OnScreen installer. Edit if you know what you're doing -
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
Protect-SecretFile $envFile

# -- 4. Inject .env into the OnScreen service XML; expand placeholders in the
#       Postgres + Redis XMLs. --
Set-ServiceEnv "service-onscreen.xml"
# The XML now carries every .env value (SECRET_KEY, DATABASE_URL) in <env/>.
Protect-SecretFile "$InstallDir\service-onscreen.xml"
# A legacy worker XML (pre-scheduled-task installs) may hold them too.
Protect-SecretFile "$InstallDir\service-worker.xml"
foreach ($svc in @("service-postgres.xml", "service-redis.xml")) {
    $p = Join-Path $InstallDir $svc
    (Get-Content $p -Raw) `
        -replace '\{app\}', $InstallDir `
        -replace '\{userpf\}', $env:ProgramData `
        | Set-Content -Path $p -Encoding utf8
}

# -- 5. Register Postgres + Redis (OnScreen depends on them). --
Register-WinswService "service-postgres.xml"
Register-WinswService "service-redis.xml"

# -- 6. Apply database migrations BEFORE starting OnScreen. --
# The server does not auto-migrate - it only gates on schema version - so a
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

# Confirm the .env password actually authenticates now that trust is gone. If
# it doesn't (e.g. the role password was changed by hand without updating
# .env), restore the previous pg_hba.conf rather than leave the server unable
# to reach its database, and say so loudly.
if ($pgHbaChanged) {
    # psql reports an auth failure on stderr; under EAP=Stop (PS 5.1) that would
    # throw before the exit code is checked, so relax it for this probe.
    $prevEAP = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $env:PGPASSWORD = $pgPassword
    & "$InstallDir\pgsql\bin\psql.exe" -h localhost -U postgres -d onscreen -tAc "SELECT 1" 2>&1 | Out-Null
    $authOk = ($LASTEXITCODE -eq 0)
    $env:PGPASSWORD = $null
    if ($authOk) {
        Remove-Item -Force $pgHbaBackup -ErrorAction SilentlyContinue
        Write-Log "Postgres: password authentication verified"
    } else {
        Copy-Item $pgHbaBackup $pgHba -Force
        & "$InstallDir\pgsql\bin\pg_ctl.exe" reload -D $pgdata 2>&1 | ForEach-Object { Write-Log "  pg_ctl reload: $_" }
        Write-Log "WARNING: the password in $envFile does not authenticate as 'postgres'; restored the old pg_hba.conf, which still uses TRUST (any local process can connect as superuser). Fix the password in .env, then change 'trust' to 'scram-sha-256' in $pgHba and restart the OnScreenPostgres service."
    }
    $ErrorActionPreference = $prevEAP
}

Write-Log "Applying database migrations..."
$dbUrl = ((Get-Content $envFile | Where-Object { $_ -match '^DATABASE_URL=' }) -replace '^DATABASE_URL=', '').Trim().Trim('"')
$env:DATABASE_URL = $dbUrl
# goose logs every line (even success) to stderr, which Windows PowerShell 5.1
# turns into a terminating error under EAP=Stop - it aborted the install at the
# first "OK 00001_init.sql". Judge by the exit code instead.
$prevEAP = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
& "$InstallDir\server.exe" migrate 2>&1 | ForEach-Object { Write-Log "  migrate: $_" }
$migrateExit = $LASTEXITCODE
$ErrorActionPreference = $prevEAP
$env:DATABASE_URL = $null
if ($migrateExit -ne 0) { throw "database migration failed (exit $migrateExit)" }

# -- 6b. Open the API/UI port to the LAN. Lets other devices reach the web UI
#       and, in a fleet, lets remote workers pull source media + transcoded
#       segments from this primary over HTTP (TranscodeJob.SourceURL). Scoped
#       to the local subnet; every request is still auth-gated.
#
#       Postgres (5432) and Valkey (6379) stay loopback-only by default -
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
Write-Log "To attach remote workers, also expose Postgres (5432) + Valkey (6379) to the LAN - see Settings -> Transcode on the primary."
