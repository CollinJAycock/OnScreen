# OnScreen Production Deployment Guide

## Prerequisites

| Dependency | Version | Purpose |
|------------|---------|---------|
| PostgreSQL | 16+ with `pgvector` extension | Primary data store |
| Valkey or Redis | 7+ | Sessions, job queue, rate limiting |
| FFmpeg | Latest stable | Transcoding, `ffprobe` media analysis |
| Go | 1.25+ | Building from source (bare metal only) |
| Node.js | 22+ | Building frontend from source (bare metal only) |
| goose | v3 | Running database migrations |

---

## Environment Variables

### Required

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgres://onscreen:secret@localhost:5432/onscreen?sslmode=disable` |
| `VALKEY_URL` | Valkey/Redis connection string | `redis://localhost:6379` |
| `SECRET_KEY` | AES-256-GCM encryption key (32 bytes). Accepts hex (64 chars), base64 (~44 chars), or a raw string (32+ chars) | `openssl rand -hex 32` |

### Database

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_RO_URL` | Falls back to `DATABASE_URL` | Read-replica connection string. Set this if you run a read replica for query offloading. |

### Cache

| Variable | Default | Description |
|----------|---------|-------------|
| `CACHE_PATH` | `~/.onscreen/cache/artwork` | Directory for resized artwork cache. Override to put the cache on a faster disk. |

### Server

Most of these are now the *initial default* for a value editable in the admin UI.
`LISTEN_ADDR`, `METRICS_ADDR`, and `TLS` are per-node (Settings ▸ Nodes); a saved
per-node value wins over the env var. `RETAIN_MONTHS` is cluster-wide (Settings ▸
System). `NODE_ID`, `IGNORE_NODE_DB_CONFIG`, and the connection strings stay
env-only — they're needed before the settings tables are reachable.

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_ID` | host name | Stable identity used to key this node's row in `node_settings` (Settings ▸ Nodes). |
| `IGNORE_NODE_DB_CONFIG` | `false` | Break-glass: boot from env/defaults only, ignoring the `node_settings` row. Recovers a node locked out by a bad bind address. |
| `LISTEN_ADDR` | `:7070` | Address the HTTP server binds to (per-node UI override). |
| `METRICS_ADDR` | `:7071` | Address for the Prometheus metrics endpoint (per-node UI override). |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `RETAIN_MONTHS` | `24` | How many months of watch history to retain |
| `TLS_CERT_FILE` | (none) | PEM-encoded certificate chain. When set with `TLS_KEY_FILE`, serves HTTPS from files (these win over an uploaded cert). See [Built-in HTTPS](#built-in-https). |
| `TLS_KEY_FILE` | (none) | PEM-encoded private key. Must be paired with `TLS_CERT_FILE`; setting only one is a startup error. |

### Worker

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_ADDR` | (none) | Address the standalone worker listens on, e.g. `:7073` |

### Scanning

These are the initial defaults; the effective values are editable in the admin UI
under **Settings ▸ System** (restart-required, a saved value wins over the env var).

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAN_FILE_CONCURRENCY` | `NumCPU * 2` | Concurrent file scan goroutines (I/O-bound) |
| `SCAN_LIBRARY_CONCURRENCY` | `2` | Concurrent library scans |
| `MISSING_FILE_GRACE_PERIOD` | `15m` | How long to wait before marking a missing file as unavailable |

### Transcoding

`TRANSCODE_MAX_SESSIONS` and the NVENC tuning are hot-reloadable via SIGHUP. The
output ceilings (`TRANSCODE_MAX_BITRATE_KBPS` / `_WIDTH` / `_HEIGHT`) and the
adaptive-bitrate ladder (`TRANSCODE_ABR`, `TRANSCODE_ABR_MAX_HEIGHT`,
`TRANSCODE_ABR_AUTO_MAX_HEIGHT`) are the initial defaults — edit the effective
values in the admin UI under **Settings ▸ Transcode** ▸ *Output Limits* /
*Adaptive Bitrate* (restart-required; a saved value wins).

| Variable | Default | Description |
|----------|---------|-------------|
| `TRANSCODE_MAX_SESSIONS` | `max(1, NumCPU/2)` | Maximum concurrent transcode sessions |
| `TRANSCODE_ENCODERS` | auto-detect | Encoder priority, e.g. `nvenc,software` or `software` |
| `TRANSCODE_MAX_BITRATE_KBPS` | `40000` | Max transcode output bitrate in kbps |
| `TRANSCODE_MAX_WIDTH` | `3840` | Max transcode output width |
| `TRANSCODE_MAX_HEIGHT` | `2160` | Max transcode output height |
| `TRANSCODE_NVENC_PRESET` | `p4` | NVENC preset: `p1` (fastest) through `p7` (best quality). Lower presets reduce GPU load at the cost of quality. |
| `TRANSCODE_NVENC_TUNE` | `hq` | NVENC tuning mode: `hq` (high quality, recommended for VOD), `ll` (low latency), `ull` (ultra-low latency) |
| `TRANSCODE_NVENC_RC` | `vbr` | NVENC rate control: `vbr` (variable bitrate, best quality per bit), `cbr` (constant bitrate), `constqp` (constant quantizer) |
| `TRANSCODE_MAXRATE_RATIO` | `1.5` | Peak-to-average bitrate ratio for all encoders. `maxrate = bitrate × ratio`. Use `1.0` to cap bandwidth tightly, `2.0` for high-bandwidth LANs. |

### Metadata

| Variable | Default | Description |
|----------|---------|-------------|
| `TMDB_API_KEY` | (none) | TMDB API key for cover art, ratings, and genre metadata |
| `TMDB_RATE_LIMIT` | `5` | TMDB API requests per second |
| `TVDB_API_KEY` | (none) | TheTVDB v4 project key; enables episode metadata fallback |

### Worker

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_HEALTH_ADDR` | `:7074` | Worker health server listen address (`/health/live`, `/health/ready`) |

### Observability

OpenTelemetry tracing (OTLP/gRPC) is configured from the admin Settings UI
under **Settings → Observability** rather than environment variables. The
tracer provider is built once at process startup, so a server/worker restart
is required after changing the endpoint, sample ratio, or deployment env tag.

### OAuth / SSO (optional)

| Variable | Default | Description |
|----------|---------|-------------|
| `BASE_URL` | `http://localhost:$LISTEN_ADDR` | Public URL of the server (e.g. `https://media.example.com`). Required for OAuth redirect URIs. |
| `GOOGLE_CLIENT_ID` | (none) | Google OAuth2 client ID |
| `GOOGLE_CLIENT_SECRET` | (none) | Google OAuth2 client secret |
| `GITHUB_CLIENT_ID` | (none) | GitHub OAuth2 client ID |
| `GITHUB_CLIENT_SECRET` | (none) | GitHub OAuth2 client secret |
| `DISCORD_CLIENT_ID` | (none) | Discord OAuth2 client ID |
| `DISCORD_CLIENT_SECRET` | (none) | Discord OAuth2 client secret |

### Development (ignored in production)

| Variable | Default | Description |
|----------|---------|-------------|
| `DEV_FRONTEND_URL` | (none) | Proxies non-API requests to Vite dev server (dev builds only) |

---

## Docker Compose Deployment (Recommended)

This is the simplest way to run OnScreen in production.

### 1. Create a project directory

```bash
mkdir onscreen && cd onscreen
```

### 2. Create a `.env` file

```bash
# Generate a secret key
SECRET_KEY=$(openssl rand -hex 32)

cat > .env <<EOF
DB_PASS=change-me-to-a-strong-password
SECRET_KEY=${SECRET_KEY}
TMDB_API_KEY=your-tmdb-api-key
LOG_LEVEL=info
EOF
```

### 3. Create `docker-compose.yml`

```yaml
name: onscreen

services:
  postgres:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_USER: onscreen
      POSTGRES_PASSWORD: ${DB_PASS}
      POSTGRES_DB: onscreen
    ports:
      - "127.0.0.1:5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U onscreen"]
      interval: 5s
      timeout: 3s
      retries: 10

  valkey:
    image: valkey/valkey:8-alpine
    ports:
      - "127.0.0.1:6379:6379"
    healthcheck:
      test: ["CMD", "valkey-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  migrate:
    image: ghcr.io/pressly/goose:v3.24.1
    depends_on:
      postgres:
        condition: service_healthy
    command: >
      -dir /migrations postgres
      "postgres://onscreen:${DB_PASS}@postgres:5432/onscreen?sslmode=disable" up
    volumes:
      - ./migrations:/migrations:ro
    restart: "no"

  server:
    image: ghcr.io/your-org/onscreen:latest
    depends_on:
      migrate:
        condition: service_completed_successfully
      valkey:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://onscreen:${DB_PASS}@postgres:5432/onscreen?sslmode=disable
      VALKEY_URL: redis://valkey:6379
      SECRET_KEY: ${SECRET_KEY}
      TMDB_API_KEY: ${TMDB_API_KEY:-}
      LOG_LEVEL: ${LOG_LEVEL:-info}
    restart: unless-stopped
    ports:
      - "7070:7070"
      - "127.0.0.1:7071:7071"
    volumes:
      - /path/to/your/media:/media:ro
    healthcheck:
      test: ["CMD-SHELL", "wget -qO- http://localhost:7070/health/live || exit 1"]
      interval: 10s
      timeout: 3s
      retries: 10

  worker:
    image: ghcr.io/your-org/onscreen:latest
    entrypoint: ["/usr/local/bin/worker"]
    depends_on:
      migrate:
        condition: service_completed_successfully
      valkey:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://onscreen:${DB_PASS}@postgres:5432/onscreen?sslmode=disable
      VALKEY_URL: redis://valkey:6379
      SECRET_KEY: ${SECRET_KEY}
      TMDB_API_KEY: ${TMDB_API_KEY:-}
      LOG_LEVEL: ${LOG_LEVEL:-info}
      WORKER_ADDR: ":7073"
    restart: unless-stopped
    volumes:
      - /path/to/your/media:/media:ro

volumes:
  postgres_data:
```

Replace `/path/to/your/media` with the actual path to your media library. Remove `:ro` if transcoding writes output alongside source files.

### 4. Start everything

```bash
docker compose up -d
```

The `migrate` service runs once, applies pending migrations, then exits. The server starts after migrations complete.

### 5. Verify

```bash
# Check health
curl http://localhost:7070/health/live

# View logs
docker compose logs -f server
```

Open `http://your-server:7070` in a browser and create your admin account.

---

## GPU-Accelerated Deployment (NVIDIA)

For hardware-accelerated transcoding with NVENC/NVDEC, use the GPU Docker images. This requires the [NVIDIA Container Toolkit](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/latest/install-guide.html) on the host.

### Driver and CUDA requirements

The FFmpeg image is built on **CUDA 12.8** (`ARG CUDA_VERSION` in `docker/Dockerfile.ffmpeg`) — deliberately, not CUDA 13 — for broad hardware compatibility:

- **CUDA 12.8** needs NVIDIA driver **≥ 570** and supports GPUs from **Maxwell through Blackwell**.
- **CUDA 13** would require driver **≥ 580** (only released mid-2025) and drops every GPU older than Turing (compute < 7.5) — breaking GPU acceleration on the drivers most distros/NAS appliances actually ship (535/550/560/570) and on common cards like the GTX 10-series and Quadro P2000.

Check the host with `nvidia-smi`: the driver must be **≥ 570** (the table's "CUDA Version" shows the max the driver supports — needs ≥ 12.8). NVENC/NVDEC themselves work on older drivers via the codec SDK, but the CUDA-runtime scaler (`scale_cuda`) needs the matched driver. The `nvidia/cuda` base is pinned in `.github/dependabot.yml` so it isn't auto-bumped back to 13; only raise `CUDA_VERSION` once a 580+ driver is broadly deployed.

### 1. Build the FFmpeg base image (one-time)

```bash
docker build -f docker/Dockerfile.ffmpeg -t onscreen-ffmpeg:latest .
```

This builds a custom FFmpeg with NVENC, NVDEC, CUDA hwaccel (`scale_cuda`), and the libplacebo Vulkan tonemap. It rarely changes, so the layer cache makes subsequent rebuilds fast — **except** when `CUDA_VERSION` changes, which busts the cache for a full (~15–20 min) rebuild.

### 2. Build the GPU application image

```bash
docker build -f docker/Dockerfile.gpu -t onscreen:gpu .
```

### 3. Run with GPU access

```yaml
# docker-compose.gpu.yml (worker service override)
worker:
  image: onscreen:gpu
  entrypoint: ["/usr/local/bin/worker"]
  deploy:
    resources:
      reservations:
        devices:
          - driver: nvidia
            count: 1
            capabilities: [gpu, compute, video, graphics, utility]
  environment:
    NVIDIA_VISIBLE_DEVICES: all
    NVIDIA_DRIVER_CAPABILITIES: all
    TRANSCODE_ENCODERS: nvenc,software
  volumes:
    - onscreen_cache:/var/cache/onscreen   # MUST persist — see below
```

Or run directly:

```bash
docker run --gpus all -e TRANSCODE_ENCODERS=nvenc,software \
  -v onscreen_cache:/var/cache/onscreen onscreen:gpu
```

Two settings here are easy to miss and each silently disables a GPU path:

- **`graphics` capability / `NVIDIA_DRIVER_CAPABILITIES` must include `graphics`** (or `all`). `nvidia-container-toolkit` only injects the NVIDIA Vulkan ICD when `graphics` is requested, and the libplacebo HDR tonemap (which also does the GPU downscale) needs it. Without it the worker logs `libplacebo=false` and falls back to a software scale/tonemap. The image bakes `NVIDIA_DRIVER_CAPABILITIES=all` in, but **orchestrators can override it** — notably TrueNAS apps, where you must set it explicitly in the app's environment.
- **`/var/cache/onscreen` must be a persistent volume.** It holds the CUDA JIT cache (`CUDA_CACHE_PATH`). FFmpeg ships `scale_cuda` as PTX that the driver JIT-compiles to your GPU's SASS on first use — CPU-bound, up to ~90s cold on older arches. With a persistent volume that cost is paid **once**; on an ephemeral volume it recurs every deploy (non-blocking, but each fresh container re-warms for ~90s before `cuda_scale` turns on).

### Verifying GPU acceleration

On startup the worker probes the hardware and logs a `transcode worker ready` line. Check it:

```bash
docker logs <container> 2>&1 | grep "worker ready"
```

The flags that matter:

| Flag | Meaning | Needs |
|------|---------|-------|
| `nvdec_hevc` | NVDEC HEVC decode to system memory (offloads the decode) | NVENC GPU + driver |
| `cuda_scale` | Full-VRAM `cuvid → scale_cuda → NVENC` (GPU downscale, 4K SDR) | matched CUDA/driver + warm JIT cache |
| `libplacebo` | Vulkan GPU HDR→SDR tonemap **and** scale (4K HDR) | `graphics` capability |

`cuda_scale` is probed **in the background** and reads `false` in the initial `worker ready` line; on a cold JIT cache it flips on ~90s later (logged as `cuda_scale enabled (full-VRAM scale_cuda path)`), and within ~2s on warm boots. So a healthy GPU node ends up with all three `true`. If any stays `false`, see [Troubleshooting](#troubleshooting-gpu-acceleration).

### NVENC tuning

Configure NVENC quality/performance via environment variables (all hot-reloadable):

- `TRANSCODE_NVENC_PRESET`: `p1` (fastest) to `p7` (best quality), default `p4`
- `TRANSCODE_NVENC_TUNE`: `hq` (recommended), `ll`, or `ull`
- `TRANSCODE_NVENC_RC`: `vbr` (default), `cbr`, or `constqp`
- `TRANSCODE_MAXRATE_RATIO`: Peak-to-average bitrate ratio, default `1.5`

### HDR tonemapping

HDR content is automatically tonemapped to SDR for clients that don't support HDR. The worker picks the best available filter at runtime, in priority order:

1. **libplacebo (Vulkan)** — GPU tonemap **and** scale in one pass; best quality and the preferred path. Requires the `graphics` capability (see above). This is what our mainline FFmpeg build uses.
2. **tonemap_opencl** — OpenCL tonemap on the GPU; fallback when libplacebo's Vulkan device is unavailable.
3. **zscale + tonemap** — CPU software fallback (requires libzimg).

> Note: `tonemap_cuda` is **not** in our build — it's a jellyfin-ffmpeg downstream patch, not upstream FFmpeg. We build mainline FFmpeg and use libplacebo for the GPU HDR path instead. (SDR downscales go through `scale_cuda`, which *is* mainline.)

The player displays a notice when tonemapping is active, recommending users enable HDR on their display.

### Troubleshooting GPU acceleration

Symptoms are read off the `worker ready` log line (see [Verifying GPU acceleration](#verifying-gpu-acceleration)).

**`libplacebo=false` (4K HDR slow / software tonemap):** the container isn't getting the Vulkan ICD. Ensure `NVIDIA_DRIVER_CAPABILITIES` includes `graphics` (or `all`) **as actually applied to the container** — on TrueNAS/orchestrators the app layer can override the image default, so set it in the app env. Confirm inside the container: `ffmpeg -init_hw_device vulkan -f lavfi -i color=c=black:s=64x64 -frames:v 1 -f null -` should succeed.

**`cuda_scale=false` (4K SDR slow / software scale):** almost always one of:
- *CUDA/driver mismatch* — the image's CUDA toolkit is newer than the host driver supports. Check `nvidia-smi` driver ≥ 570 and that the container reports `/usr/local/cuda-12.8` (`docker exec <c> ls -d /usr/local/cuda-*`). NVENC/NVDEC will still work (they don't use the CUDA runtime), which is why `nvdec_hevc=true` but `cuda_scale=false`.
- *Cold/non-persistent JIT cache* — if `/var/cache/onscreen` isn't a persistent, writable volume, the `scale_cuda` PTX re-JITs (~90s) on every process and the probe times out. Verify `CUDA_CACHE_PATH=/var/cache/onscreen/nv` resolves to a writable volume; after the first warm-up the dir should contain an `index` file. A quick reproducer in the container — run twice, expect cold (~90s) then warm (~2s):
  ```bash
  ffmpeg -hide_banner -loglevel error -y -f lavfi -i testsrc2=s=640x480:r=10:d=1 -c:v hevc_nvenc -frames:v 10 /tmp/t.hevc
  time ffmpeg -hide_banner -loglevel error -hwaccel cuda -hwaccel_output_format cuda -c:v hevc_cuvid -i /tmp/t.hevc -vf scale_cuda=320:240 -c:v hevc_nvenc -frames:v 5 -f null -
  ```

**First transcode after a fresh deploy is slow, then fine:** expected — that's the one-time cold `scale_cuda` JIT warming the cache. Persist `/var/cache/onscreen` so it doesn't recur.

### Local GPU testing on Docker Desktop (Windows/WSL2)

For contributors validating the GPU image on Windows: Docker Desktop's `--gpus` passthrough injects CUDA compute (`libcuda`) but **not** the NVENC/NVDEC codec libraries, so `hevc_nvenc`/`hevc_cuvid` fail with "Cannot load libnvidia-encode.so.1". Mount them from the WSL VM:

```bash
MSYS_NO_PATHCONV=1 docker run --rm --gpus all \
  -v /usr/lib/wsl/lib:/wsllib:ro -e LD_LIBRARY_PATH=/wsllib \
  onscreen-ffmpeg:latest bash -c 'ffmpeg ...'
```

`MSYS_NO_PATHCONV=1` is only needed in Git Bash (it stops the `/usr/lib/wsl/lib` path being mangled to a Windows path). For the full stack, put the same `volumes`/`environment` in a local-only `docker/docker-compose.local-gpu.yml` (gitignored) layered as an extra `-f`. Note: libplacebo/Vulkan still won't initialize in WSL2 containers — that path can only be validated on real Linux.

---

## Bare Metal Deployment

### 1. Install dependencies

```bash
# Debian/Ubuntu
sudo apt install postgresql-16 postgresql-16-pgvector valkey ffmpeg

# Arch
sudo pacman -S postgresql valkey ffmpeg
```

### 2. Build from source

```bash
git clone https://github.com/your-org/onscreen.git
cd onscreen

# Build frontend
cd web && npm ci && npm run build && cd ..

# Build Go binaries
CGO_ENABLED=0 go build -o bin/server ./cmd/server
CGO_ENABLED=0 go build -o bin/worker ./cmd/worker
```

### 3. Configure

```bash
export DATABASE_URL="postgres://onscreen:secret@localhost:5432/onscreen?sslmode=disable"
export VALKEY_URL="redis://localhost:6379"
export SECRET_KEY="$(openssl rand -hex 32)"
export TMDB_API_KEY="your-key"
```

Or place these in an environment file and load with your init system (see systemd example below).

### 4. Run migrations

```bash
make migrate DATABASE_URL="$DATABASE_URL"
# or directly:
goose -dir internal/db/migrations postgres "$DATABASE_URL" up
```

### 5. Start

```bash
./bin/server &
./bin/worker &
```

### systemd service example

```ini
# /etc/systemd/system/onscreen-server.service
[Unit]
Description=OnScreen Server
After=network.target postgresql.service valkey.service

[Service]
Type=simple
User=onscreen
Group=onscreen
EnvironmentFile=/etc/onscreen/env
ExecStart=/usr/local/bin/server
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```ini
# /etc/systemd/system/onscreen-worker.service
[Unit]
Description=OnScreen Worker
After=network.target postgresql.service valkey.service

[Service]
Type=simple
User=onscreen
Group=onscreen
EnvironmentFile=/etc/onscreen/env
ExecStart=/usr/local/bin/worker
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now onscreen-server onscreen-worker
```

---

## Database Setup

OnScreen uses PostgreSQL 16+ with the `pgvector` extension. Migrations are managed by [goose](https://github.com/pressly/goose).

### Create the database

```sql
CREATE USER onscreen WITH PASSWORD 'your-password';
CREATE DATABASE onscreen OWNER onscreen;
\c onscreen
CREATE EXTENSION IF NOT EXISTS vector;
```

### Run migrations

```bash
# Using Make
make migrate DATABASE_URL="postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable"

# Using goose directly
goose -dir internal/db/migrations postgres \
  "postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable" up

# Using Docker (no local goose needed)
docker run --rm --network host \
  -v ./internal/db/migrations:/migrations:ro \
  ghcr.io/pressly/goose:v3.24.1 \
  -dir /migrations postgres \
  "postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable" up
```

### Check migration status

```bash
make migrate-status DATABASE_URL="$DATABASE_URL"
# or
goose -dir internal/db/migrations postgres "$DATABASE_URL" status
```

Current migrations:

| File | Purpose |
|------|---------|
| `00001_init.sql` | Initial schema |
| `00002_watch_event_partitions.sql` | Watch history partitioning |
| `00003_server_settings.sql` | Server settings table |
| `00004_dedup_orphaned_items.sql` | Deduplicate orphaned items |
| `00005_cleanup_stale_file_paths.sql` | Clean up stale file paths |
| `00006_drop_plex_columns.sql` | Remove legacy Plex columns |
| `00007_fk_cascades.sql` | Add foreign key cascades |
| `00008_dedup_hierarchy_items.sql` | Deduplicate hierarchy items |
| `00009_google_oauth.sql` | Google OAuth support |
| `00010_github_discord_oauth.sql` | GitHub and Discord OAuth support |
| `00011_password_reset_tokens.sql` | Password reset token table |
| `00012_invite_tokens.sql` | Invite code system |
| `00013_audit_log.sql` | Admin audit logging |
| `00014_add_missing_indexes.sql` | Performance indexes |
| `00015_dedup_top_level_items.sql` | Deduplicate top-level items |
| `00016_clear_artwork_for_relocation.sql` | Clear artwork for path relocation |
| `00017_file_duration.sql` | Per-file duration from ffprobe |
| `00018_library_filter_indexes.sql` | Library filtering indexes |
| `00019_collections.sql` | Collections (playlists) |
| `00020_managed_profiles.sql` | Managed user profiles |
| `00021_photo_type.sql` | Photo library support |
| `00022_user_language_preferences.sql` | Preferred audio/subtitle language |
| `00023_content_rating_filter.sql` | Parental content rating filter |
| `00024_notifications.sql` | In-app notification system |
| `00025_fix_content_rating_null.sql` | Fix NULL content rating handling |

---

## Built-in HTTPS

For deployments that don't want a reverse proxy in front (single-host installs, intranets, dev/test, or hosts where you already manage certs another way), the server can terminate TLS itself.

Set both `TLS_CERT_FILE` and `TLS_KEY_FILE` to PEM-encoded files readable by the server process:

```bash
TLS_CERT_FILE=/etc/onscreen/tls/fullchain.pem
TLS_KEY_FILE=/etc/onscreen/tls/privkey.pem
```

When both are set the server uses `ListenAndServeTLS` on `LISTEN_ADDR` (commonly retargeted to `:443`). Setting only one is a startup error so you don't accidentally deploy thinking HTTPS is on.

**Or upload a cert in the UI.** If you'd rather not put cert files on disk, leave the env vars unset and paste the certificate + key under **Settings ▸ Security ▸ HTTPS / TLS** instead. They're stored encrypted in the database and loaded into memory at startup (restart-required). The env file paths, when set, take precedence. This is cluster-wide — the common single-host / wildcard-cert case; for per-host certs across a cluster, use the env path on each node or a reverse proxy. The same renewal caveat applies: the server doesn't auto-renew, so re-upload and restart on renewal.

Where the certs come from is your call:

- **mkcert** — quick local CA for LAN deployments and dev.
- **Corporate / private CA** — drop the issued chain + key on the host.
- **Let's Encrypt** — run [certbot](https://certbot.eff.org/) on the host (e.g. `certbot certonly --standalone` or DNS-01) and point the env vars at the issued files. The server does **not** auto-renew or reload — restart on cert renewal, or run a reverse proxy (next section) which handles renewal natively.

If you need automated renewal, ACME, or want to share TLS termination with other services, use a reverse proxy (Caddy is the simplest path).

---

## Reverse Proxy (nginx)

OnScreen listens on port 7070. In production, put it behind a reverse proxy with TLS termination — or use [built-in HTTPS](#built-in-https) for simpler deployments.

```nginx
upstream onscreen {
    server 127.0.0.1:7070;
}

server {
    listen 80;
    server_name media.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name media.example.com;

    ssl_certificate     /etc/letsencrypt/live/media.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/media.example.com/privkey.pem;

    # Modern TLS
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384;
    ssl_prefer_server_ciphers off;

    # Large media files and long-running transcode streams
    client_max_body_size 0;
    proxy_buffering off;
    proxy_request_buffering off;

    location / {
        proxy_pass http://onscreen;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket support (used by HLS live progress)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # Long timeouts for transcoding streams
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # Metrics endpoint should not be public
    location /metrics {
        deny all;
    }
}
```

If you use Caddy instead:

```
media.example.com {
    reverse_proxy localhost:7070 {
        flush_interval -1
    }
}
```

---

## OAuth / SSO Setup

OnScreen supports **Google**, **GitHub**, and **Discord** as OAuth login providers. Each provider is enabled by setting its client ID and secret. Set `BASE_URL` to your public server URL so redirect URIs are correct.

### Google

1. Go to [Google Cloud Console](https://console.cloud.google.com/) > **APIs & Services > Credentials**.
2. Create an **OAuth client ID** (Web application).
3. Authorized redirect URI: `https://media.example.com/api/v1/auth/google/callback`
4. Set `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`.

### GitHub

1. Go to [GitHub Developer Settings](https://github.com/settings/developers) > **OAuth Apps > New OAuth App**.
2. Authorization callback URL: `https://media.example.com/api/v1/auth/github/callback`
3. Set `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`.

### Discord

1. Go to [Discord Developer Portal](https://discord.com/developers/applications) > **New Application > OAuth2**.
2. Add redirect: `https://media.example.com/api/v1/auth/discord/callback`
3. Set `DISCORD_CLIENT_ID` and `DISCORD_CLIENT_SECRET`.

### Configuration

```bash
BASE_URL=https://media.example.com
GOOGLE_CLIENT_ID=123456789.apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=GOCSPX-xxxxxxxxxxxxxxxx
GITHUB_CLIENT_ID=Iv1.xxxxxxxx
GITHUB_CLIENT_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
DISCORD_CLIENT_ID=123456789012345678
DISCORD_CLIENT_SECRET=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

### User flow

Users click "Sign in with Google/GitHub/Discord" on the login page. On first login, a local OnScreen account is created and linked. If an existing user has the same email, the accounts are automatically linked. The first user registered becomes admin.

---

## Backup and Maintenance

### Database backups

```bash
# Full backup
pg_dump -U onscreen -Fc onscreen > onscreen_$(date +%Y%m%d).dump

# Restore
pg_restore -U onscreen -d onscreen --clean onscreen_20260328.dump
```

For automated backups, use a cron job or a tool like [pgBackRest](https://pgbackrest.org/).

```bash
# Example cron entry (daily at 3 AM)
0 3 * * * pg_dump -U onscreen -Fc onscreen > /backups/onscreen_$(date +\%Y\%m\%d).dump
```

### Media path considerations

- Each library's directory must be readable by the onscreen process (and writable if transcoding writes alongside source files). Library paths are configured per-library in the admin UI, not via a global env var.
- In Docker, bind-mount your host media directory and point libraries at the container path. Use `:ro` if you do not need transcoding to write alongside source files.
- File changes are detected during library scans. After adding or removing media, trigger a scan from the web UI or wait for the next scheduled scan.
- The artwork cache (`CACHE_PATH`) can be safely deleted; it will be regenerated on demand.

### Configuration changes require a restart

**Nothing is hot-reloadable.** `SIGHUP` is still handled, but it applies no
setting — it logs a warning saying so. Earlier revisions of this page listed
`LOG_LEVEL`, `TRANSCODE_MAX_SESSIONS` and the NVENC knobs as reloadable; none of
them were still wired to a live reader, so an operator sending `SIGHUP` was told
"config reloaded" while the running values never changed.

Settings now flow one of two ways, and both take effect on the next restart:

- **Admin UI** (Settings ▸ System / ▸ Transcode / ▸ Nodes) — stored in the
  database; the matching env var is only the initial default.
- **Environment** — the bootstrap set that must be readable before the settings
  tables are (`DATABASE_URL`, `VALKEY_URL`, `SECRET_KEY`, `NODE_ID`), plus bind
  addresses and paths.

Restart the process after any change.

### Watch history retention

The `RETAIN_MONTHS` variable (default: 24) controls how long watch history is kept. Older records are purged automatically.

---

## Troubleshooting

### Server won't start: "SECRET_KEY must be at least 32 bytes"

Your `SECRET_KEY` is too short. Generate a valid one:

```bash
openssl rand -hex 32
```

This produces a 64-character hex string encoding 32 bytes.

### Server won't start: "DATABASE_URL is required"

All three required environment variables must be set: `DATABASE_URL`, `VALKEY_URL`, `SECRET_KEY`. Double-check your `.env` file or systemd `EnvironmentFile`.

### Database connection refused

- Verify PostgreSQL is running: `pg_isready -h localhost -p 5432`
- Check the connection string includes `?sslmode=disable` for local connections.
- In Docker Compose, services connect via container names (`postgres`, `valkey`), not `localhost`.

### Migrations fail

- Ensure the `pgvector` extension is installed: `CREATE EXTENSION IF NOT EXISTS vector;`
- Check that the database user has schema creation privileges.
- Run `goose status` to see which migrations have been applied.

### No metadata (missing cover art, genres)

- Set `TMDB_API_KEY` to a valid TMDB API v3 key. Get one at [themoviedb.org/settings/api](https://www.themoviedb.org/settings/api).
- Check logs for TMDB rate limit errors. Lower `TMDB_RATE_LIMIT` if needed.

### Media files not appearing after scan

- Verify each library's path points to the correct directory and the onscreen process can read it.
- In Docker, confirm the volume mount is correct (`docker compose exec server ls /media`).
- Check logs for scan errors: `docker compose logs server | grep -i scan`

### Transcode sessions failing

- Verify FFmpeg is installed and on `PATH`: `ffmpeg -version`
- In the Docker image, FFmpeg is bundled. For bare metal, install it separately.
- If using hardware encoding (`TRANSCODE_ENCODERS=nvenc`), ensure the GPU drivers and NVIDIA Container Toolkit are installed.

### WebSocket connections failing behind reverse proxy

Make sure your reverse proxy forwards the `Upgrade` and `Connection` headers. See the nginx config example above.

### PostgreSQL connection exhaustion

If you see "remaining connection slots are reserved for non-replication superuser connections", the server has leaked connections. This was fixed in v1.1.1 — the connection pool is now capped at 20 with health checks and idle timeouts. To recover:

```sql
-- Check active connections
SELECT count(*) FROM pg_stat_activity WHERE datname = 'onscreen';

-- Terminate stale connections (safe — the pool will reconnect)
SELECT pg_terminate_backend(pid) FROM pg_stat_activity
WHERE datname = 'onscreen' AND state = 'idle' AND query_start < now() - interval '5 minutes';
```

Ensure Docker containers use `STOPSIGNAL SIGTERM` and a stop grace period of at least 35s so the server can drain connections on shutdown.

### High memory usage during scans

Lower `SCAN_FILE_CONCURRENCY` (hot-reloadable via SIGHUP). The default is `NumCPU * 2`, which may be aggressive on memory-constrained systems.

### Health check endpoint

The server exposes `GET /health/live` on the main listen address. A `200` response means the server is running. Use this for load balancer health checks. `GET /health/ready` additionally verifies DB + Valkey reachability and that no migrations are pending.

### Metrics endpoint

The metrics endpoint at `METRICS_ADDR` (default `:7071`) exposes Prometheus
exposition format (`text/plain; version=0.0.4`). It's a dedicated mux — only
`/metrics` is served, no app routes leak onto it. **Keep this port firewalled
from public access.**

Beyond the standard `go_*` runtime and `process_*` collectors, the server
exports an `onscreen_*` set:

| Metric | Labels | What it tracks |
|---|---|---|
| `onscreen_http_requests_total` | `method`, `path`, `status` | Per-request count. `path` is the chi **route template** (e.g. `/api/v1/items/{id}`), so per-ID URLs collapse to one series — no cardinality blow-up |
| `onscreen_http_request_duration_seconds` | `method`, `path` | Request-duration histogram, same path-template label |
| `onscreen_db_query_duration_seconds` | `query` | Query duration histogram, `query` = SQL verb (`SELECT`, `INSERT`, …, or `other`). pgx tracer wraps the existing OTel one |
| `onscreen_transcode_sessions_active` | — | Gauge of active transcode sessions, set from the live Valkey session index (multi-instance / TTL-correct, not a drifting local inc/dec) |
| `onscreen_transcode_jobs_total` | `status` | Job dispatch counter; `status` is `dispatched`, `queued`, or `error` |
| `onscreen_scanner_files_scanned_total` | `library_id` | Files scanned per library, incremented at scan completion |
| `onscreen_watch_events_total` | `event_type` | Watch events ingested (`play` / `pause` / `stop` / `scrobble` / …) |
| `onscreen_webhook_failures_total` | `url` | Webhook delivery failures after all retries are exhausted |
| `onscreen_hub_cache_refresh_duration_seconds` | `hub` | Hub materialized-view refresh duration |
| `onscreen_ratelimit_failopen_total` | — | Counter of requests allowed through because the Valkey rate-limiter was unavailable |

Label-bearing counters/histograms only emit a series once they've been observed
at least once — a freshly-restarted server may show only the runtime + gauge
metrics until traffic / a scan / a transcode session / a watch event fires.

---

## High Availability, Object Storage & Multi-Site

Every HA/scale feature is **opt-in and off by default** — the deployment above is a standard single node. To go further:

- **Object storage** (remove the local-disk SPOF; enable CDN offload): configure S3 / MinIO / Backblaze B2 / Wasabi / Cloudflare R2 from **Settings ▸ Integrations ▸ Storage** (no env var — it hot-swaps live). Every read and write path then routes through it.
- **Valkey Sentinel** (HA lock/session/cache): deploy `docker/docker-compose.valkey-ha.yml`, set `VALKEY_SENTINEL_ADDRS`.
- **PostgreSQL failover**: deploy `docker/docker-compose.postgres-ha.yml`, point `DATABASE_URL` at a multi-host failover DSN; add a promotion orchestrator (managed Multi-AZ / Patroni).
- **CDN offload**: object-storage bytes offload via signed URLs automatically once a CDN base is set in Storage settings; for local-disk artwork set `PUBLIC_ASSET_CACHE=true`.
- **Static-ABR** (`STATIC_ABR_ENABLED=true`): pre-encodes popular titles' ABR ladders to the store so hot-title playback serves from the CDN, not the live fleet.
- **Multi-site DR**: set `SITE_ID`, monitor `GET /health/cluster` (`{site_id, role, replication_lag_seconds}`), and follow the failover/fail-back procedures.

### Split segment access (bypass a CDN/tunnel for video)

Some remote-access setups can't put bulk video through the main hostname — e.g. a
**Cloudflare Tunnel**, whose terms restrict proxying self-hosted video through the
CDN and which can throttle sustained streams. Set `PUBLIC_SEGMENT_BASE_URL` to a
host that reaches **this same server** by a *direct* path (a DNS-only / "grey-cloud"
record, a `stream.` subdomain port-forwarded to the origin, a Tailscale name, etc.):

```bash
PUBLIC_SEGMENT_BASE_URL=https://stream.example.com
```

The HLS **manifests** (small, re-polled) keep coming from the main host, but every
**segment / rung-playlist URL** inside them is rewritten to
`https://stream.example.com/api/v1/transcode/...` — so the bandwidth-heavy bytes
ride the direct host while the UI/API stay behind the tunnel. Notes:

- `stream.example.com` must terminate TLS and reach the **same** OnScreen origin —
  it's a different *network path*, not a different backend (segment tokens and
  session state are shared). Leave unset for a normal single-host deploy.
- Covers single-stream, ABR (master + rungs), and static-ABR playlists. Direct-play
  (`/media/stream/...`), artwork, and subtitles are small and stay on the main host.

---

Full operational procedures — enabling each tier, monitoring, and failover/fail-back: **[dr-runbook.md](dr-runbook.md)**. Design and rationale: **[ha-roadmap.md](ha-roadmap.md)**.
