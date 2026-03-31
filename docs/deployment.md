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
| `MEDIA_PATH` | Absolute path to your media library root | `/media` |
| `SECRET_KEY` | AES-256-GCM encryption key (32 bytes). Accepts hex (64 chars), base64 (~44 chars), or a raw string (32+ chars) | `openssl rand -hex 32` |

### Database

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_RO_URL` | Falls back to `DATABASE_URL` | Read-replica connection string. Set this if you run a read replica for query offloading. |

### Cache

| Variable | Default | Description |
|----------|---------|-------------|
| `CACHE_PATH` | `$MEDIA_PATH/.cache/artwork` | Directory for resized artwork cache. Override to put the cache on a faster disk. |

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTEN_ADDR` | `:7070` | Address the HTTP server binds to |
| `METRICS_ADDR` | `:7071` | Address for the Prometheus metrics endpoint |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `RETAIN_MONTHS` | `24` | How many months of watch history to retain |

### Worker

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_ADDR` | (none) | Address the standalone worker listens on, e.g. `:7073` |

### Scanning (hot-reloadable via SIGHUP)

| Variable | Default | Description |
|----------|---------|-------------|
| `SCAN_FILE_CONCURRENCY` | `NumCPU * 2` | Concurrent file scan goroutines (I/O-bound) |
| `SCAN_LIBRARY_CONCURRENCY` | `2` | Concurrent library scans |
| `MISSING_FILE_GRACE_PERIOD` | `15m` | How long to wait before marking a missing file as unavailable |

### Transcoding (hot-reloadable via SIGHUP)

| Variable | Default | Description |
|----------|---------|-------------|
| `TRANSCODE_MAX_SESSIONS` | `max(1, NumCPU/2)` | Maximum concurrent transcode sessions |
| `TRANSCODE_ENCODERS` | auto-detect | Encoder priority, e.g. `nvenc,software` or `software` |
| `TRANSCODE_MAX_BITRATE_KBPS` | `40000` | Max transcode output bitrate in kbps |
| `TRANSCODE_MAX_WIDTH` | `3840` | Max transcode output width |
| `TRANSCODE_MAX_HEIGHT` | `2160` | Max transcode output height |

### Metadata

| Variable | Default | Description |
|----------|---------|-------------|
| `TMDB_API_KEY` | (none) | TMDB API key for cover art, ratings, and genre metadata |
| `TMDB_RATE_LIMIT` | `20` | TMDB API requests per second |
| `TVDB_API_KEY` | (none) | TheTVDB v4 project key; enables episode metadata fallback |

### Worker

| Variable | Default | Description |
|----------|---------|-------------|
| `WORKER_HEALTH_ADDR` | `:7074` | Worker health server listen address (`/health/live`, `/health/ready`) |

### Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none) | OTLP endpoint for distributed tracing. Tracing is disabled if unset. |

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
      MEDIA_PATH: /media
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
      MEDIA_PATH: /media
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
export MEDIA_PATH="/srv/media"
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

---

## Reverse Proxy (nginx)

OnScreen listens on port 7070. In production, put it behind a reverse proxy with TLS termination.

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

- The `MEDIA_PATH` directory must be readable by the onscreen process (and writable if transcoding writes to the same volume).
- In Docker, bind-mount your host media directory. Use `:ro` if you do not need transcoding to write alongside source files.
- File changes are detected during library scans. After adding or removing media, trigger a scan from the web UI or wait for the next scheduled scan.
- The artwork cache (`CACHE_PATH`) can be safely deleted; it will be regenerated on demand.

### Hot-reload configuration

Several settings can be updated without restarting the server by sending `SIGHUP`:

```bash
kill -HUP $(pidof server)
```

Hot-reloadable values: `LOG_LEVEL`, `SCAN_FILE_CONCURRENCY`, `SCAN_LIBRARY_CONCURRENCY`, `TRANSCODE_MAX_SESSIONS`, `TRANSCODE_MAX_BITRATE_KBPS`, `TRANSCODE_MAX_WIDTH`, `TRANSCODE_MAX_HEIGHT`.

Changes to `DATABASE_URL`, `VALKEY_URL`, `SECRET_KEY`, `LISTEN_ADDR`, or `MEDIA_PATH` require a full restart.

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

All four required environment variables must be set: `DATABASE_URL`, `VALKEY_URL`, `MEDIA_PATH`, `SECRET_KEY`. Double-check your `.env` file or systemd `EnvironmentFile`.

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

- Verify `MEDIA_PATH` points to the correct directory and the onscreen process can read it.
- In Docker, confirm the volume mount is correct (`docker compose exec server ls /media`).
- Check logs for scan errors: `docker compose logs server | grep -i scan`

### Transcode sessions failing

- Verify FFmpeg is installed and on `PATH`: `ffmpeg -version`
- In the Docker image, FFmpeg is bundled. For bare metal, install it separately.
- If using hardware encoding (`TRANSCODE_ENCODERS=nvenc`), ensure the GPU drivers and NVIDIA Container Toolkit are installed.

### WebSocket connections failing behind reverse proxy

Make sure your reverse proxy forwards the `Upgrade` and `Connection` headers. See the nginx config example above.

### High memory usage during scans

Lower `SCAN_FILE_CONCURRENCY` (hot-reloadable via SIGHUP). The default is `NumCPU * 2`, which may be aggressive on memory-constrained systems.

### Health check endpoint

The server exposes `GET /health/live` on the main listen address. A `200` response means the server is running. Use this for load balancer health checks.

The metrics endpoint at `METRICS_ADDR` (default `:7071`) exposes Prometheus-compatible metrics. Keep this port firewalled from public access.
