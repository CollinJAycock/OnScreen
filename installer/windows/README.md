# OnScreen — Windows portable build

Version: `<VERSION>`
Built: `<BUILD_TIME>`

This zip is a **portable test deployment** of OnScreen for Windows x64.
It is not a polished installer — it's the smallest viable bundle that
gets the server running on a fresh box so you can validate transcode
behavior on real hardware (Intel QSV, AMD AMF, NVIDIA NVENC).

## What's in the zip

| File | Purpose |
| ---- | ------- |
| `server.exe` | Main HTTP API + embedded web UI |
| `worker.exe` | Transcode worker (runs in same process as server by default; this binary is for the multi-host fan-out path) |
| `devtoken.exe` | Issues a dev JWT for smoke-testing |
| `WinSW.exe` + `onscreen.xml` | Windows Service wrapper for `server.exe` |
| `ffmpeg/ffmpeg.exe` + `ffprobe.exe` | Gyan.dev full build — has NVENC, **QSV**, AMF, AV1, libdav1d |
| `start.ps1` | Foreground launch (interactive use, Ctrl+C to stop) |
| `install-service.ps1` | Register `OnScreen` as a Windows Service (auto-start on boot) |
| `uninstall-service.ps1` | Remove the service (leaves files in place) |
| `docker-compose.deps.yml` + `start-deps.ps1` | Postgres + Valkey via Docker Desktop |
| `.env.example` | Config template — copy to `.env` and edit |

## Quickstart

### 1. Prereqs

- **Windows 10 21H2+ / Windows 11 / Windows Server 2019+** (x64)
- **Docker Desktop** for the dependencies (Postgres + Valkey).
  *Alternative:* install Postgres 16 + Valkey/Redis natively if you
  prefer — the server only cares about `DATABASE_URL` and `VALKEY_URL`.

### 2. Extract and configure

Unzip somewhere stable — e.g. `C:\OnScreen\`. The location matters for
the Windows Service: WinSW reads files from this directory at every
service start, so don't extract to `%TEMP%`.

```powershell
cd C:\OnScreen
copy .env.example .env
notepad .env       # fill in SECRET_KEY at minimum; MEDIA_PATH if not C:\media
```

### 3. Bring up Postgres + Valkey

```powershell
.\start-deps.ps1
```

This runs `docker compose -f docker-compose.deps.yml up -d`. Postgres
binds 127.0.0.1:5432, Valkey binds 127.0.0.1:6379. Both restart with
Docker Desktop.

If you don't want Docker, install Postgres 16 + Valkey/Redis natively
and update `DATABASE_URL` / `VALKEY_URL` in `.env` to match.

### 4. First-time launch (foreground)

```powershell
.\start.ps1
```

The first start runs schema migrations against the empty database.
Open <http://localhost:7070> in a browser to walk through admin
account setup. Press Ctrl+C to stop.

### 5. Install as a Windows Service (recommended for ongoing use)

```powershell
.\install-service.ps1
```

This:

1. Reads `.env` and translates each line into a service-scoped
   environment variable in `onscreen.xml`. (Without this the service
   runs as `LocalSystem` with an empty env and panics on missing
   config.)
2. Registers the service via `WinSW.exe install onscreen.xml`.
3. Starts the service.

Manage from then on with:

```powershell
Restart-Service OnScreen
Stop-Service    OnScreen
Start-Service   OnScreen
```

…or `services.msc`. Logs roll into `.\logs\onscreen.out.log` /
`onscreen.err.log` (10 MB rotation, 8 generations).

To remove: `.\uninstall-service.ps1`.

## Validating Intel QSV / NVENC / AMF

After login, go to **Settings → Transcoding** and check the encoder
list. The server probes ffmpeg on startup and only lists encoders it
can actually invoke. On an Intel box you should see:

- `h264_qsv`
- `hevc_qsv`
- `av1_qsv` *(11th-gen Core / Arc Alchemist+ only)*
- `libx264` / `libsvtav1` *(software fallbacks always present)*

To force a specific encoder for a session, use the quality picker on
the watch page (any non-Auto resolution forces a full transcode and
the server picks the best encoder for the requested codec). Tail
`logs\onscreen.out.log` and look for the `starting ffmpeg` line — the
`encoder` field reports what was actually invoked.

## Troubleshooting

- **`ERROR: .env not found`** — copy `.env.example` to `.env` first.
- **Service starts but immediately stops** — check
  `logs\onscreen.err.log`. Most often: bad `DATABASE_URL` (server
  can't reach Postgres) or `SECRET_KEY` not 32+ chars.
- **Service won't install** (`Access denied`) — re-run
  `install-service.ps1` from an elevated PowerShell, or accept the
  UAC prompt the script raises automatically.
- **Port 7070 already in use** — set `LISTEN_ADDR=:7080` in `.env`.

## What this build deliberately does NOT include

- **An MSI / signed installer** — this is a test bundle, not a
  shippable artifact. Expect SmartScreen warnings on first run.
- **Auto-update** — re-extract a newer zip on top of the existing
  directory. Migrations run automatically on next start.
- **Postgres / Valkey native installs** — the bundled
  `docker-compose.deps.yml` is the supported path. Native installs
  work but you wire them yourself.
