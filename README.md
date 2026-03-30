# OnScreen

A modern, open-source media server built for correctness, simplicity, and high availability. PostgreSQL-native. Single binary. Native web player.

## Features

- **Library management** -- scan movies and TV shows from local directories with automatic TMDB metadata enrichment (posters, fanart, ratings, genres, summaries)
- **Native web player** -- built-in SvelteKit player with progress tracking, continue watching, episode navigation, and custom controls
- **HLS transcoding** -- on-the-fly H.264 transcoding via FFmpeg with hardware encoder detection and a configurable quality ladder
- **Direct play** -- serves raw media files with HTTP range request support for clients that can play natively
- **Watch state** -- immutable event-sourced playback tracking (play/pause/stop/seek); current state derived from events, never mutated
- **Continue Watching + Recently Added** -- home page hubs driven by materialized views for instant load
- **Webhooks** -- configurable endpoints with HMAC-SHA256 signing, retry logic, and failure recording (compatible with Overseerr/Tautulli)
- **Multi-user auth** -- Paseto v4 local tokens, refresh token rotation, admin/user roles, optional PIN lock
- **Analytics dashboard** -- play counts, bandwidth, codec distribution, top played items
- **Manual matching** -- search TMDB and apply a specific match when auto-match gets it wrong

## Quick Start

### Prerequisites

- Go 1.25+
- Node.js 22+
- PostgreSQL 16+
- Valkey (or Redis) 7+
- FFmpeg (for transcoding)

### 1. Start dependencies

```bash
docker compose -f docker/docker-compose.yml up -d postgres valkey
```

### 2. Run migrations

```bash
make migrate DATABASE_URL="postgres://onscreen:onscreen@localhost:5432/onscreen?sslmode=disable"
```

### 3. Run in dev mode

```bash
make dev MEDIA_PATH=/path/to/your/media
```

This starts the Go API server on `:7070` and the Vite dev server on `:5173`.

### 4. Open the UI

Navigate to `http://localhost:5173`, create your admin account, add a library, and scan.

## Docker

```bash
docker build -f docker/Dockerfile -t onscreen .
docker run -p 7070:7070 -p 7071:7071 \
  -e DATABASE_URL="postgres://..." \
  -e VALKEY_URL="redis://..." \
  -e SECRET_KEY="your-32-byte-secret-here" \
  -e MEDIA_PATH="/media" \
  -v /your/media:/media:ro \
  onscreen
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASE_URL` | -- | PostgreSQL connection string |
| `VALKEY_URL` | -- | Valkey/Redis connection string |
| `SECRET_KEY` | -- | 32+ character secret for token encryption |
| `MEDIA_PATH` | -- | Root path to media files |
| `TMDB_API_KEY` | -- | TMDB API v3 key for metadata |
| `API_PORT` | `7070` | API server listen port |
| `METRICS_PORT` | `7071` | Prometheus metrics port |

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full configuration reference and system design.

## Architecture

OnScreen is designed around a few core principles:

1. **PostgreSQL-native** -- not a SQLite port
2. **Stateless API tier** -- horizontally scalable behind a load balancer
3. **Event-sourced watch state** -- every play/pause/stop is an immutable event
4. **Single binary** -- `go build` produces one executable
5. **Plain SQL** -- queries compiled to type-safe Go via sqlc

For the full architecture documentation, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Development

```bash
make help          # show all targets
make build         # build frontend + server + worker
make test-unit     # fast unit tests (<10s)
make test-int      # integration tests (requires Docker)
make lint          # golangci-lint
make generate      # regenerate sqlc code
```

## License

AGPLv3. See [LICENSE](LICENSE).
