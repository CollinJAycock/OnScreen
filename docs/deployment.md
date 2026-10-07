# OnScreen Production Deployment Guide

## Prerequisites

| Dependency | Version | Purpose |
|------------|---------|---------|
| PostgreSQL | 16+ | Primary data store. Uses the `pg_trgm`, `pgcrypto` and `unaccent` extensions, which ship with PostgreSQL. |
| Valkey or Redis | 7+ | Sessions, job queue, rate limiting |
| FFmpeg | Latest stable | Transcoding, `ffprobe` media analysis |
| Go | 1.26+ | Building from source (bare metal only) |
| Node.js | 24+ | Building frontend from source (bare metal only) |
| goose | v3 | Running database migrations (bundled in the Docker image) |

---

## Ports

| Port (default) | Env | Purpose | Exposure guidance |
|---|---|---|---|
| `:7070` | `LISTEN_ADDR` | HTTP/S API + web UI | Public (front with TLS / reverse proxy) |
| `127.0.0.1:7071` | `METRICS_ADDR` | Prometheus `/metrics` **and unauthenticated `/debug/pprof`** | Loopback by default — keep private. pprof leaks heap/goroutine memory (live tokens), cmdline, and webhook URLs. Only widen behind a firewall. |
| `:1935` | `RTMP_LISTEN_ADDR` (if `RTMP_ENABLED`) | RTMP "go live" ingest | Reachable by broadcasters; publishes require a valid stream key. |
| `:7073` | `WORKER_ADDR` (standalone `cmd/worker`) | Transcode-fleet HLS segment server | Requires a `SECRET_KEY`-derived bearer, so every fleet node needs the same `SECRET_KEY`. The embedded worker binds loopback. |
| `:7368/udp` | `DISCOVERY_PORT` | LAN auto-discovery | Unauthenticated; answers only loopback / private / link-local senders with server name / machine-id / version. Set `DISCOVERY_ENABLED=false` on untrusted L2 segments. |

The standalone worker also serves `/health/live` and `/health/ready` on
`WORKER_HEALTH_ADDR` (default `:7074`). See [security.md](security.md) for the
rest of the network posture.

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

Many of these are the *initial default* for a value editable in the admin UI; a
saved value wins over the env var, and a change takes effect on the next restart.

- **Per-node** (Settings ▸ Nodes): `LISTEN_ADDR`, `METRICS_ADDR`, `CACHE_PATH`,
  `WORKER_HEALTH_ADDR`, `STATIC_ABR_ROOT`, `SITE_ID`, `TRANSCODE_QSV_DECODE`,
  `DISABLE_EMBEDDED_WORKER`.
- **Cluster-wide** (Settings ▸ System): `RETAIN_MONTHS`, `SERVER_NAME`,
  `DISCOVERY_ENABLED`, `DISCOVERY_PORT`, `TMDB_RATE_LIMIT`, `PUBLIC_ASSET_CACHE`,
  `STATIC_ABR_ENABLED`, and the scan settings below.
- **Settings ▸ Transcode:** the output ceilings and ABR ladder (see
  [Transcoding](#transcoding)).
- **Env-only:** `NODE_ID`, `IGNORE_NODE_DB_CONFIG` and the connection strings
  (needed before the settings tables are reachable), and every other variable
  on this page not listed above.

The public URL and the log level have no env var: set them in **Settings ▸
General ▸ Server** (restart-required).

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_ID` | host name | Stable identity used to key this node's row in `node_settings` (Settings ▸ Nodes). |
| `IGNORE_NODE_DB_CONFIG` | `false` | Break-glass: boot from env/defaults only, ignoring the `node_settings` row. Recovers a node locked out by a bad bind address. |
| `LISTEN_ADDR` | `:7070` | Address the HTTP server binds to (per-node UI override). |
| `METRICS_ADDR` | `127.0.0.1:7071` | Address for the Prometheus metrics endpoint and `/debug/pprof` (per-node UI override). In a container, set `0.0.0.0:7071` and keep the published port loopback-only. |
| `RETAIN_MONTHS` | `24` | How many months of watch history to retain |
| `TLS_CERT_FILE` | (none) | PEM-encoded certificate chain. When set with `TLS_KEY_FILE`, serves HTTPS from files (these win over an uploaded cert). See [Built-in HTTPS](#built-in-https). |
| `TLS_KEY_FILE` | (none) | PEM-encoded private key. Must be paired with `TLS_CERT_FILE`; setting only one is a startup error. |
| `TRUSTED_PROXIES` | (none) | Comma-separated CIDRs or IPs allowed to set `X-Forwarded-*`, e.g. `127.0.0.1,172.18.0.0/16`. Unset means any loopback or private-network peer, which lets a LAN client forge its address for rate limits and the audit log. Set it to your reverse proxy's address. |
| `PUBLIC_SEGMENT_BASE_URL` | (none) | Host that HLS segments are fetched from instead of the main host. See [Split segment access](#split-segment-access-bypass-a-cdntunnel-for-video). |
| `SERVER_NAME` | `OnScreen` | Name shown in LAN discovery and capability responses. |
| `DISCOVERY_ENABLED` | `true` | Answer LAN auto-discovery broadcasts. |
| `DISCOVERY_PORT` | `7368` | UDP port for LAN discovery. |
| `AUTO_MIGRATE` | `false` | Apply pending database migrations at startup, before serving. Use it when there is no separate migrate step (single-container installs). |
| `ALLOW_PUBLIC_SETUP` | `false` | First-run setup (creating the first admin) is accepted only from the local network or a local host name. Set `true` only if you must finish setup over the internet, and finish it promptly. |
| `PUBLIC_ASSET_CACHE` | `false` | Send resized artwork with `Cache-Control: public` so a CDN in front can cache it. Only set it with a CDN deployed. |
| `STATIC_ABR_ENABLED` | `false` | Pre-encode the ABR ladder for the most-played titles to the media store. Worthwhile mainly with object storage and a CDN. |
| `STATIC_ABR_ROOT` | (none) | Key prefix for static-ABR output. Leave empty for object storage; set a directory for a local static root. |
| `SITE_ID` | (none) | This deployment's site name for multi-site DR, reported on `/health/cluster`. |
| `VALKEY_SENTINEL_ADDRS` | (none) | Comma-separated Sentinel addresses. When set, Valkey is reached through Sentinel; `VALKEY_URL` still supplies auth, db and TLS (its host is ignored). |
| `VALKEY_SENTINEL_MASTER` | `onscreen` | Sentinel master name. |
| `VALKEY_SENTINEL_PASSWORD` | (none) | Password for the Sentinels themselves. |

### Rate limits

| Variable | Default | Description |
|----------|---------|-------------|
| `OS_AUTH_RATE_LIMIT_PER_MIN` | `10` | Requests per minute to the credential endpoints (sign-in, PIN switch, pairing claims). |
| `OS_AUTH_RATE_LIMIT_FAIL_CLOSED` | `false` | When Valkey is unreachable, refuse those requests with `503` instead of letting them through unlimited. |
| `OS_TRANSCODE_START_RATE_LIMIT_PER_MIN` | `10` | Transcode session starts per minute. |

An empty, zero or unparseable value falls back to the default.

### RTMP ingest

| Variable | Default | Description |
|----------|---------|-------------|
| `RTMP_ENABLED` | `true` | Run the embedded RTMP server that accepts OBS/ffmpeg pushes as Live TV channels. A bind failure is logged and the rest of the server still starts. |
| `RTMP_LISTEN_ADDR` | `:1935` | RTMP listen address. Must be reachable by broadcasters. |
| `RTMP_PUBLIC_HOST` | (none) | Host name shown in the ingest URL in the broadcast admin UI. Empty uses the request's host. |

### Worker

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_ADDR` | `:7073` | Address the standalone worker's segment server binds **and** registers for the API to proxy HLS to, so it must be routable from the server, e.g. `worker:7073`. A bare `:7073` only works when the API runs on the same host. |
| `WORKER_HEALTH_ADDR` | `:7074` | Standalone worker health server (`/health/live`, `/health/ready`). |
| `DISABLE_EMBEDDED_WORKER` | `false` | Server only: skip the in-process transcode worker and use standalone workers only. |
| `TRANSCODE_QSV_DECODE` | `false` | Standalone worker: Intel Quick Sync HEVC decode into system memory. Opt in once the worker's QSV stack is known good. |
| `TRANSCODE_QSV_VRAM` | `true` | Standalone worker: keep decode, scale and encode in GPU memory when a QSV encoder is selected (SDR only). Falls back to software decode per job. Set `false` to force the software path. |
| `TRANSCODE_VAAPI_VRAM` | `true` | Standalone worker: the VAAPI equivalent of `TRANSCODE_QSV_VRAM`. |
| `TRANSCODE_ENCODER_FAILOVER` | `true` | When the hardware encoder can't get the GPU (for example GeForce NVENC's session cap), retry the job on the next encoder the box has (QSV, then software). No effect on single-GPU boxes. |

### Scanning

These are the initial defaults; the effective values are editable in the admin UI
under **Settings ▸ System** (restart-required, a saved value wins over the env var).

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAN_FILE_CONCURRENCY` | `NumCPU * 2` | Concurrent file scan goroutines (I/O-bound) |
| `SCAN_LIBRARY_CONCURRENCY` | `2` | Concurrent library scans |
| `MISSING_FILE_GRACE_PERIOD` | `15m` | How long to wait before marking a missing file as unavailable |

### Transcoding

`TRANSCODE_MAX_SESSIONS`, `TRANSCODE_ENCODERS` and the NVENC tuning are read at
startup, so change them and restart (per-worker session caps and encoders can
also be set in the fleet section of **Settings ▸ Transcode**). The
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
| `TRANSCODE_ABR` | `false` | Serve an adaptive-bitrate ladder (multi-rendition master playlist) instead of a single rendition. |
| `TRANSCODE_ABR_MAX_HEIGHT` | `0` | Hard cap on the ladder's top rung. `0` = up to the source resolution. |
| `TRANSCODE_ABR_AUTO_MAX_HEIGHT` | `1080` | Top rung when the client hasn't picked a quality. An explicit pick overrides it; `TRANSCODE_ABR_MAX_HEIGHT` still applies. `0` = up to the source resolution. |
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

### Observability

OpenTelemetry tracing (OTLP/gRPC) is configured from the admin Settings UI
under **Settings → Observability** rather than environment variables. The
tracer provider is built once at process startup, so a server/worker restart
is required after changing the endpoint, sample ratio, or deployment env tag.

### Single sign-on

OIDC, SAML and LDAP have no env vars; configure them in the admin UI. See
[Single sign-on (OIDC, SAML, LDAP)](#single-sign-on-oidc-saml-ldap).

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
DB_PASS=$(openssl rand -hex 24)
SECRET_KEY=${SECRET_KEY}
TMDB_API_KEY=your-tmdb-api-key
EOF
```

`DB_PASS` is required (compose refuses to start without it) and `SECRET_KEY`
must be a real random key: the server refuses to boot with a placeholder value.

### 3. Create `docker-compose.yml`

```yaml
name: onscreen

services:
  postgres:
    image: pgvector/pgvector:pg16   # same as docker/docker-compose.yml; any PostgreSQL 16+ image works
    environment:
      POSTGRES_USER: onscreen
      POSTGRES_PASSWORD: ${DB_PASS:?DB_PASS is required}
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

  # Runs the goose binary and migrations bundled in the OnScreen image, so
  # the schema always matches the image version.
  migrate:
    image: ghcr.io/collinjaycock/onscreen:2.5
    entrypoint: ["/usr/local/bin/goose"]
    depends_on:
      postgres:
        condition: service_healthy
    command: >
      -dir /migrations postgres
      "postgres://onscreen:${DB_PASS:?DB_PASS is required}@postgres:5432/onscreen?sslmode=disable" up
    restart: "no"

  server:
    image: ghcr.io/collinjaycock/onscreen:2.5
    depends_on:
      migrate:
        condition: service_completed_successfully
      valkey:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://onscreen:${DB_PASS:?DB_PASS is required}@postgres:5432/onscreen?sslmode=disable
      VALKEY_URL: redis://valkey:6379
      SECRET_KEY: ${SECRET_KEY:?SECRET_KEY required}
      TMDB_API_KEY: ${TMDB_API_KEY:-}
      # Container loopback is unreachable through a port publish; the host
      # side stays loopback-only.
      METRICS_ADDR: "0.0.0.0:7071"
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
    image: ghcr.io/collinjaycock/onscreen:2.5
    entrypoint: ["/usr/local/bin/worker"]
    depends_on:
      migrate:
        condition: service_completed_successfully
      valkey:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://onscreen:${DB_PASS:?DB_PASS is required}@postgres:5432/onscreen?sslmode=disable
      VALKEY_URL: redis://valkey:6379
      SECRET_KEY: ${SECRET_KEY:?SECRET_KEY required}
      TMDB_API_KEY: ${TMDB_API_KEY:-}
      # Bind + advertised address: must be routable from the server container.
      WORKER_ADDR: "worker:7073"
    restart: unless-stopped
    # The image's built-in health check probes the server's :7070, which the
    # worker doesn't serve; point it at the worker's own health port.
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:7074/health/ready"]
      interval: 10s
      timeout: 3s
      retries: 5
    volumes:
      - /path/to/your/media:/media:ro

volumes:
  postgres_data:
```

Replace `/path/to/your/media` with the actual path to your media library. Remove `:ro` if transcoding writes output alongside source files.

Release images are published as `ghcr.io/collinjaycock/onscreen:<version>`,
with `:<major>.<minor>` (used above) and `:<major>` tags that follow the latest
patch release. Pin a full version such as `:2.5.0` if you want upgrades only
when you change the tag.

If you'd rather not run a separate `migrate` service, drop it (and the
`depends_on: migrate` entries) and set `AUTO_MIGRATE: "true"` on the server: it
then applies pending migrations at startup, before serving.

### 4. Start everything

```bash
docker compose up -d
```

The `migrate` service runs once, applies pending migrations, then exits. The server starts after migrations complete.

On a fresh install the users table is empty, and whoever completes first-run
setup becomes the admin. That is accepted only from the local network or a
local host name unless you set `ALLOW_PUBLIC_SETUP=true`.

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

This builds a custom FFmpeg with NVENC, NVDEC, CUDA hwaccel (`scale_cuda`), the patched-in `tonemap_cuda` filter, and the libplacebo Vulkan tonemap. It rarely changes, so the layer cache makes subsequent rebuilds fast — **except** when `CUDA_VERSION` changes, which busts the cache for a full (~15–20 min) rebuild.

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
  healthcheck:   # the image's default probes the server's :7070
    test: ["CMD", "wget", "--spider", "-q", "http://localhost:7074/health/ready"]
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
| `tonemap_cuda` | All-VRAM `cuvid → scale_cuda → tonemap_cuda → NVENC` HDR→SDR (4K HDR, no Vulkan) | matched CUDA/driver + warm JIT cache |
| `libplacebo` | Vulkan GPU HDR→SDR tonemap **and** scale (4K HDR) | `graphics` capability |

`cuda_scale` and `tonemap_cuda` are probed **in the background** and read `false` in the initial `worker ready` line; on a cold JIT cache they flip on ~90s later (logged as `cuda_scale enabled (full-VRAM scale_cuda path)` and `tonemap_cuda enabled (all-VRAM CUDA HDR→SDR path)`), and within ~2s on warm boots. So a healthy GPU node ends up with all four `true`. `libplacebo` can stay `false` where Vulkan won't initialize (TrueNAS, WSL2); HDR then uses `tonemap_cuda`. If another flag stays `false`, see [Troubleshooting](#troubleshooting-gpu-acceleration).

### NVENC tuning

Configure NVENC quality/performance via environment variables (read at startup; restart after a change):

- `TRANSCODE_NVENC_PRESET`: `p1` (fastest) to `p7` (best quality), default `p4`
- `TRANSCODE_NVENC_TUNE`: `hq` (recommended), `ll`, or `ull`
- `TRANSCODE_NVENC_RC`: `vbr` (default), `cbr`, or `constqp`
- `TRANSCODE_MAXRATE_RATIO`: Peak-to-average bitrate ratio, default `1.5`

### HDR tonemapping

HDR content is automatically tonemapped to SDR for clients that don't support HDR. The worker picks the best available filter at runtime, in priority order:

1. **tonemap_cuda** (NVIDIA) — `scale_cuda` + `tonemap_cuda` entirely in VRAM, straight to NVENC. Plain CUDA, no Vulkan, so it also works where libplacebo can't start (TrueNAS). Used once the `tonemap_cuda` probe passes, for HEVC/H.264 sources on an NVENC encoder.
2. **libplacebo (Vulkan)** — GPU tonemap **and** scale in one pass, any vendor. Requires the `graphics` capability (see above).
3. **scale_cuda + zscale** (NVIDIA) — GPU downscale, then the CPU tonemap on the smaller frame. Used when neither of the above is available.
4. **tonemap_vaapi** (VAAPI encoders, which skip libplacebo) — scale and tonemap on VA surfaces; enabled only where the startup probe passes.
5. **zscale + tonemap** — CPU software fallback (requires libzimg).

> `tonemap_cuda` is not in mainline FFmpeg. `docker/Dockerfile.ffmpeg` applies jellyfin-ffmpeg's CUDA tonemap patches (`docker/patches/`) on top of mainline and builds the CUDA kernels with clang. `tonemap_opencl` is no longer used; it shows in the `worker ready` line for diagnostics only.

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

**`tonemap_cuda=false` (4K HDR on NVIDIA uses libplacebo or the CPU tonemap):** same causes as `cuda_scale=false` above. The worker logs `tonemap_cuda unavailable; HDR falls back to …` when its probe fails, which can also happen on a GPU generation the patched kernels don't run on; HDR then falls back down the list in [HDR tonemapping](#hdr-tonemapping).

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
sudo apt install postgresql-16 valkey ffmpeg

# Arch
sudo pacman -S postgresql valkey ffmpeg
```

### 2. Build from source

```bash
git clone https://github.com/CollinJAycock/OnScreen.git
cd OnScreen

# Builds the web UI and the TV app, copies them into the Go embed
# directories, then builds bin/server and bin/worker
make build
```

Use `make build` rather than a bare `go build`: the server embeds the web UI
from `internal/webui/dist`, and `make build` refreshes that copy from
`web/dist` (a plain `go build` ships whatever stale copy is there).

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

OnScreen uses PostgreSQL 16+. Migrations are managed by [goose](https://github.com/pressly/goose).

### Create the database

```sql
CREATE USER onscreen WITH PASSWORD 'your-password';
CREATE DATABASE onscreen OWNER onscreen;
```

The first migration creates the `pg_trgm`, `pgcrypto` and `unaccent`
extensions. They ship with PostgreSQL and are trusted extensions, so the
database owner can create them. If your role can't, create them once as a
superuser:

```sql
\c onscreen
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS unaccent;
```

### Run migrations

```bash
# Using Make
make migrate DATABASE_URL="postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable"

# Using goose directly
goose -dir internal/db/migrations postgres \
  "postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable" up

# Using the OnScreen image's bundled goose and migrations (no local goose needed)
docker run --rm --network host --entrypoint /usr/local/bin/goose \
  ghcr.io/collinjaycock/onscreen:2.5 \
  -dir /migrations postgres \
  "postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable" up

# Using the server binary (it embeds the migrations and reads DATABASE_URL)
DATABASE_URL="postgres://onscreen:pass@localhost:5432/onscreen?sslmode=disable" ./bin/server migrate
```

### Check migration status

```bash
make migrate-status DATABASE_URL="$DATABASE_URL"
# or
goose -dir internal/db/migrations postgres "$DATABASE_URL" status
```

Current migrations (the pre-v2.2 set was squashed into `00001_init.sql`
at the v2.2.0 cut — the table below matches `internal/db/migrations/`):

| File | Purpose |
|------|---------|
| `00001_init.sql` | Initial schema (squashed v2.2.0 baseline) |
| `00002_fk_cascade_indexes.sql` | Index the FK cascade columns the squash missed |
| `00003_totp_2fa.sql` | TOTP two-factor authentication (secrets, recovery codes) |
| `00004_watch_plays_matview.sql` | `watch_plays` materialized view (popularity for static ABR) |
| `00005_node_settings.sql` | Per-node configuration table (Settings ▸ Nodes) |
| `00006_create_first_admin_atomic.sql` | Atomic first-admin creation (setup race fix) |
| `00007_user_scrobble.sql` | Per-user scrobbling credentials (ListenBrainz) |
| `00008_parental_watch_limits.sql` | Daily-minutes + allowed-hours watch limits |
| `00009_video_bit_depth.sql` | Per-file video bit depth from ffprobe |
| `00010_library_type_cartoons.sql` | `cartoons` library type |
| `00011_user_ratings.sql` | Per-user star ratings + community average |
| `00012_user_stream_caps.sql` | Admin per-user streaming caps (bitrate/height/streams) |
| `00013_tuner_type_rtmp.sql` | RTMP live-ingest tuner type |
| `00014_watch_events_decision.sql` | Playback decision recorded on watch events |
| `00015_user_hub_layout.sql` | Per-user hub row visibility + ordering |
| `00016_content_rating_rank_anime.sql` | Anime content-rating rank mapping |
| `00017_collections_type_photo_album.sql` | `photo_album` collections type |
| `00018_sessions_prev_token_hash.sql` | Refresh-token reuse detection (previous token hash) |
| `00019_media_file_integrity.sql` | Integrity-probe verdict per media file |
| `00020_request_auto_approve.sql` | Per-user request auto-approve (movies / TV) |
| `00021_notification_types.sql` | Notification type format check (request notifications were rejected) |
| `00022_request_quotas.sql` | Per-user request quotas + can-request switch |
| `00023_watch_marks.sql` | `watch_progress` rollup, manual watched marks, Continue Watching dismissals; drops the `watch_state` matview |
| `00024_library_trickplay.sql` | Per-library automatic seek-bar thumbnails (on for existing video libraries) |
| `00025_request_download_status.sql` | Live Radarr/Sonarr download status on requests |
| `00026_media_issues.sql` | User problem reports |
| `00027_franchise_collections.sql` | TMDB franchise collections |
| `00028_request_seasons.sql` | Season-level TV requests |
| `00029_notification_agents.sql` | Notification agents (Discord / Telegram / ntfy / Gotify / email) |
| `00030_track_disc_number.sql` | A track's disc number; album tracks keyed and ordered by (disc, track) |
| `00031_scrobble_lastfm_trakt.sql` | Per-user Last.fm and Trakt scrobbling credentials |
| `00032_audiobook_listening.sql` | Per-user audiobook listening speed and bookmarks |
| `00033_audiobook_merge_repair.sql` | Cutoff for repairing audiobooks the old dedupe merged (the next full audiobook scan re-imports the affected authors' files) |
| `00034_renumber_zero_led_chapters.sql` | Clears the chapter numbers of audiobooks with a zero-led chapter file ("00 - Prologue"), so the next scan renumbers them with the prologue first |
| `00035_watch_progress_keep_duration.sql` | A watch event without a duration keeps the stored duration in `watch_progress` instead of clearing it (completion is still judged only against the event's own duration) |

`00023` backfills `watch_progress` from the `watch_events` history still
inside the retention window; on a large install it takes longer than the
others, and new watch events wait on its lock until it commits. Set `AUTO_MIGRATE=true` to apply pending migrations on startup, or run
the migrate step above before starting the new binary; `/health/ready` stays
unready until they're applied.

---

## Upgrading

Back up the database first (see [Database backups](#database-backups)).

### v2.5.0 → v2.5.1

Pull the new image (`ghcr.io/collinjaycock/onscreen:2.5`, or `:2.5.1`).
Migration 00036 only adds a column (`media_files.dv_profile`). Then **run a
scan of your movie and show libraries**. Files an earlier scan tagged Dolby
Vision are re-read once and play from their HDR10, HLG or SDR base layer. Only
profile 5 stays refused. Until that scan, the clients keep refusing them.

### v2.4.x → v2.5.0

1. **Pull the new image** (`ghcr.io/collinjaycock/onscreen:2.5`) or build the
   new binaries, and **re-copy `docker/nginx.conf`** if you use it: headers
   moved to server level, and `X-Forwarded-For` is now replaced rather than
   appended. Set `TRUSTED_PROXIES` to your proxy's address.
2. **Make sure `DB_PASS` is set** for compose. There's no `onscreen` default any
   more, so compose refuses to start without it. Use the password your Postgres
   volume was created with.
3. **Check `SECRET_KEY`.** A placeholder value is now refused at boot; rotate it
   with `cmd/rotate-key` (see [Troubleshooting](#server-wont-start-secret_key-must-be-at-least-32-bytes)).
4. **Apply migrations 00018–00035**, either with the compose `migrate` service
   / `make migrate` before starting the new server, or with
   `AUTO_MIGRATE=true`. `/health/ready` stays unready until they're applied.
   - `00023` rebuilds watch state from `watch_events` and holds new watch
     events back while it runs, so it takes longest on a long history. It drops
     the `watch_state` materialized view; anything outside OnScreen that
     queried it must move to the `user_watch_state` view.
   - `00024` turns seek-bar thumbnails (trickplay) on for existing video
     libraries, so the nightly backfill adds CPU load and the cache disk grows.
     Turn it off in a library's settings if you don't want that.
5. **Run a music scan and an audiobook scan** afterwards: multi-disc albums an
   older scan folded together split again, and audiobooks a scan merged into
   one are separated.
6. **Rotate your Radarr/Sonarr API key** if anyone pressed Save on the *arr
   settings under v2.4.x. That save could store the webhook key as `****`,
   which then authenticated anyone.

Behaviour that changes without any action from you:

- Sign-ins now last at most 90 days. Sessions that exist at upgrade get 90 days
  from the upgrade, so nobody is signed out by it; after that each device signs
  in again (TVs re-pair) every 90 days.
- First-run setup works only from the local network or a local host name
  unless `ALLOW_PUBLIC_SETUP=true`. This only matters for a fresh install.

The full list, including client changes, is under "Upgrade notes (from
v2.4.x)" in [CHANGELOG.md](../CHANGELOG.md).

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
        # nginx is the edge here, so REPLACE any client-sent X-Forwarded-For
        # rather than appending to it ($proxy_add_x_forwarded_for): a forged
        # entry could otherwise pick the IP OnScreen rate-limits and audits.
        # Behind a CDN, use real_ip_header/set_real_ip_from instead.
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;

        proxy_http_version 1.1;

        # Long timeouts for transcoding streams and the notification
        # event stream (SSE), which stays open as long as a tab is open
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }

    # Metrics endpoint should not be public
    location /metrics {
        deny all;
    }
}
```

OnScreen uses no WebSockets. The one long-lived connection is the
server-sent events stream at `/api/v1/notifications/stream`, which needs
`proxy_buffering off` (set above; the server also sends
`X-Accel-Buffering: no`) and a read timeout well above a few minutes.

For a fuller nginx setup, start from `docker/nginx.conf` in the repository. It
keeps every `proxy_set_header` at server level: nginx drops inherited
`proxy_set_header` lines for any location that sets one of its own, which loses
`X-Forwarded-For` and `X-Forwarded-Proto` for that location. Set
`TRUSTED_PROXIES` to the proxy's address either way.

If you use Caddy instead:

```
media.example.com {
    reverse_proxy localhost:7070 {
        flush_interval -1
    }
}
```

---

## Single sign-on (OIDC, SAML, LDAP)

OnScreen supports OpenID Connect, SAML 2.0 and LDAP sign-in. There are no env
vars for them: configure each one under **Settings ▸ Users ▸ SSO**, where the
page also shows the URLs to register with your identity provider:

- **OIDC** (Authentik, Keycloak, Auth0, Azure AD, ...): redirect URI
  `https://media.example.com/api/v1/auth/oidc/callback`.
- **SAML**: SP metadata at `https://media.example.com/api/v1/auth/saml/metadata`;
  point OnScreen at your IdP's metadata URL.
- **LDAP**: host, bind credentials, StartTLS or LDAPS.

Set the server's public URL in **Settings ▸ General ▸ Server** first (and
restart) so the redirect URIs OnScreen generates use your public host name.

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

- **Admin UI** (Settings ▸ General / ▸ System / ▸ Transcode / ▸ Nodes) — stored
  in the database; the matching env var, where there is one, is only the
  initial default.
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

The server also refuses a key that looks like a placeholder (one containing
`change-me`, `example`, `your-secret` and similar). If an existing install used
one, rotate it with `cmd/rotate-key` (built from source; a dry run unless you
pass `-apply`) rather than just replacing it, so the secrets encrypted under the
old key are re-encrypted instead of lost.

### Server won't start: "DATABASE_URL is required"

All three required environment variables must be set: `DATABASE_URL`, `VALKEY_URL`, `SECRET_KEY`. Double-check your `.env` file or systemd `EnvironmentFile`.

### Database connection refused

- Verify PostgreSQL is running: `pg_isready -h localhost -p 5432`
- Check the connection string includes `?sslmode=disable` for local connections.
- In Docker Compose, services connect via container names (`postgres`, `valkey`), not `localhost`.

### Migrations fail

- The first migration creates the `pg_trgm`, `pgcrypto` and `unaccent` extensions. If it fails on one of them, create them as a superuser (see [Create the database](#create-the-database)); on distributions that split them out, install the PostgreSQL contrib package.
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

### Notifications don't arrive live behind a reverse proxy

Live notifications use a server-sent events stream (`/api/v1/notifications/stream`), not WebSockets. If they only show up after a page reload, the proxy is buffering the stream or timing it out: set `proxy_buffering off` and a long `proxy_read_timeout` (nginx), or `flush_interval -1` (Caddy). See the reverse-proxy examples above.

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

Lower the file scan concurrency in **Settings ▸ System** (or `SCAN_FILE_CONCURRENCY`, which is only the initial default) and restart. The default is `NumCPU * 2`, which may be aggressive on memory-constrained systems.

### Health check endpoint

The server exposes `GET /health/live` on the main listen address. A `200` response means the server is running. Use this for load balancer health checks. `GET /health/ready` additionally verifies DB + Valkey reachability and that no migrations are pending.

### Metrics endpoint

The metrics endpoint at `METRICS_ADDR` (default `127.0.0.1:7071`) exposes Prometheus
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
