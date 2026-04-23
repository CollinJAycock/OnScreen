# OnScreen

A modern, open-source media server. PostgreSQL-native. Single binary. Native web + Android TV clients.

![OnScreen hub page](screenshots/hero.png)

> **Beta:** actively deployed and in use. Public API is stable; breaking changes will be called out in [CHANGELOG.md](CHANGELOG.md).

## Why another media server?

Plex, Jellyfin, and Emby are all great — OnScreen exists because we wanted something that:

- Runs on **PostgreSQL** instead of SQLite, so it scales past a single machine and plays well with existing database tooling.
- Ships as a **single Go binary** plus a static SvelteKit bundle — no plugin host, no runtime metadata server, no Python.
- Treats **watch state as an event log** (immutable play/pause/seek/stop), not a mutable `last_viewed_at` column. Rewind a week with `DELETE FROM watch_events WHERE ts > ...`.
- Has a **native web player** built around HLS with hardware-accelerated transcoding (NVENC, QSV, VAAPI), HDR tonemapping, and proper mobile gestures — not a third-party web-only skin over Plex's stream URLs.
- Is **AGPLv3**. Forks and self-hosters get the same freedom the code was written with.

| | OnScreen | Plex | Jellyfin | Emby |
|--|--|--|--|--|
| Database | PostgreSQL | SQLite | SQLite | SQLite |
| License | AGPLv3 | Proprietary | GPLv2 | GPLv2 + proprietary server |
| Account system | Self-hosted | plex.tv cloud | Self-hosted | Self-hosted + paid tier |
| Native clients | Web, Android TV | Many | Many (community) | Many |
| Hardware transcode | NVENC, QSV, VAAPI | Yes (paid Pass) | Yes | Yes (paid Premiere) |
| HDR tonemap | CUDA / OpenCL / zscale | Paid | Yes | Paid |
| API stability | Plain REST, documented in [API.md](API.md) | Semi-public | Public | Public |

## Features

**Library**
- Scan movies and TV shows from local directories; ffprobe extracts duration, streams, HDR type, and chapter markers
- TMDB + TVDB metadata enrichment (posters, fanart, ratings, genres, summaries, content ratings)
- Audiophile-grade music: ID3/Vorbis/MP4 tag reading via dhowden/tag, MusicBrainz cross-reference IDs, ReplayGain (track + album), bit depth, sample rate, channel layout, and lossless detection — exposed on the API as `bit_depth`, `sample_rate`, `lossless`, `replaygain_*`, and `musicbrainz_*` fields
- Photo libraries with EXIF extraction (camera, lens, GPS, capture time)
- Flexible matching with manual override when TMDB gets it wrong
- Admin dedupe endpoints for duplicate shows/movies (two-pass normalization — handles `"Title"` vs `"Title YYYY"`, apostrophes, `&` vs `and`, HTML entities, prefix-extension folder names)

**Playback**
- Native SvelteKit web player with direct play, remux, and full transcode fallback
- HLS transcoding via FFmpeg with hardware encoder auto-detection (NVENC, QuickSync, VAAPI)
- HDR → SDR tonemapping (CUDA, OpenCL, or software zscale fallback)
- HEVC direct play on Safari and other HEVC-capable browsers
- JavaScript subtitle renderer with PTS offset detection and ±0.5s sync adjust
- Subtitle OCR for image-based formats (PGS, VOBSUB, DVB) — ffmpeg + tesseract converts cues to WebVTT so any client can render them. Runs on-demand or as a nightly `ocr_subtitles` task
- OpenSubtitles search and download from the player; OCR'd and downloaded subs share one `external_subtitles` table
- Web audio player with album browsing, Hi-Res / Lossless quality badges, and a track-detail panel showing codec, bit depth, sample rate, ReplayGain, and MusicBrainz links
- Trickplay seek-bar thumbnails generated from the source file
- Intro / credits markers (auto + manual) with per-episode skip prompts
- Chapter navigation (jump-to-chapter, next/prev buttons) from ffprobe-parsed chapter markers
- Per-user default audio/subtitle language preferences — auto-selects matching tracks on load
- Continue Watching + Recently Added hubs backed by materialized views
- Event-sourced watch state (immutable `watch_events` partitioned by month)

**Mobile & clients**
- Touch-optimized player: horizontal swipe to seek, vertical swipe for volume/brightness, double-tap ±10s, swipe-down to dismiss
- Bottom-sheet menus for subtitle/quality/audio on mobile
- Orientation lock to landscape on fullscreen
- Safe-area insets for notched displays
- Android TV (Leanback) client with browse rows, episode picker, and direct-play streaming

**Social & multi-user**
- Multi-user auth: PASETO v4 local tokens, refresh rotation, admin/user roles, optional PIN lock
- Managed profiles (up to 6 per account) with per-profile watch state, favorites, and language prefs
- Parental content filtering with configurable max rating per profile
- User favorites with hub-page surfacing
- In-app notifications (SSE real-time) with unread badge and mark-read

**Operations**
- Theme toggle (light/dark) with system-preference detection and FOUC prevention
- Image proxy / thumbnailer with `?w=` resize, responsive `srcset`, CDN-friendly cache headers
- Transcode fleet management UI: worker status, encoder info, live session monitoring, configurable NVENC tuning
- Multi-worker Docker Compose deployment support
- Webhooks with HMAC-SHA256 signing and retry (compatible with Overseerr/Tautulli receivers)
- `/health/ready` gated on schema-vs-code parity — container stays unhealthy until `goose up` has run
- Prometheus metrics on a separate port
- OpenTelemetry tracing (OTLP/gRPC) — auto-instruments HTTP + Postgres; logs carry trace IDs for pivoting from log → trace
- Analytics dashboard (play counts, bandwidth, codec distribution, top played)

## Screenshots

| Desktop | Mobile | Android TV |
|---|---|---|
| ![](screenshots/watch-desktop.png) | ![](screenshots/watch-mobile.png) | ![](screenshots/android-tv.png) |

![Library grid](screenshots/library.png)

## Quick Start

### Prerequisites

- Go 1.25+ and Node.js 24+ (only for building from source)
- PostgreSQL 16+
- Valkey (or Redis) 7+
- FFmpeg for transcoding — `onscreen-ffmpeg` image if you want NVENC/HDR

### Docker (recommended)

```bash
docker build -f docker/Dockerfile -t onscreen .

docker run -p 7070:7070 -p 7071:7071 \
  -e DATABASE_URL="postgres://onscreen:onscreen@postgres:5432/onscreen?sslmode=disable" \
  -e VALKEY_URL="redis://valkey:6379" \
  -e SECRET_KEY="$(openssl rand -hex 32)" \
  -e MEDIA_PATH="/media" \
  -v /your/media:/media:ro \
  onscreen

# Migrations are bundled; run once per release with:
docker exec <container> sh -c 'goose -dir /migrations postgres "$DATABASE_URL" up'
```

For GPU transcoding, see [docker/Dockerfile.gpu](docker/Dockerfile.gpu) and the multi-worker example in [docs/deployment.md](docs/deployment.md).

### Dev setup

```bash
# 1. Start dependencies
docker compose -f docker/docker-compose.yml up -d postgres valkey

# 2. Run migrations
make migrate DATABASE_URL="postgres://onscreen:onscreen@localhost:5432/onscreen?sslmode=disable"

# 3. Run in dev mode (Go API on :7070, Vite on :5173)
make dev MEDIA_PATH=/path/to/your/media
```

Navigate to `http://localhost:5173`, create your admin account, add a library, and scan.

## Configuration

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | ✓ | PostgreSQL connection string |
| `VALKEY_URL` | ✓ | Valkey/Redis connection string |
| `SECRET_KEY` | ✓ | 32+ byte secret for token encryption |
| `MEDIA_PATH` | ✓ | Root path to media files |
| `TMDB_API_KEY` | | TMDB v3 API key for metadata enrichment |
| `TVDB_API_KEY` | | TVDB v4 API key for show-level fallback |
| `API_PORT` | | API server listen port (default `7070`) |
| `METRICS_PORT` | | Prometheus metrics port (default `7071`) |

OpenTelemetry tracing (OTLP/gRPC), SMTP, OIDC, LDAP and other integrations are
now configured from the admin Settings UI rather than env vars. Tracing
config is read at process startup, so a server/worker restart is required for
changes to take effect.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full configuration reference and design notes.

## Architecture

1. **PostgreSQL-native** — not a SQLite port. Uses partitioned tables, materialized views, and `tsvector` full-text search.
2. **Stateless API tier** — horizontally scalable behind a load balancer; session state lives in Valkey.
3. **Event-sourced watch state** — every play/pause/seek/stop is an immutable row in `watch_events`; current state is derived.
4. **Single binary** — `go build ./cmd/server` produces one executable with the frontend embedded.
5. **Plain SQL** — queries authored as `.sql` files and compiled to type-safe Go via [sqlc](https://sqlc.dev).

Full design: [ARCHITECTURE.md](ARCHITECTURE.md). REST reference: [API.md](API.md).

## Development

```bash
make help          # show all targets
make build         # build frontend + server + worker
make test-unit     # fast unit tests (<10s)
make test-int      # integration tests (requires Docker)
make lint          # golangci-lint
make generate      # regenerate sqlc code
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full dev setup, code style, and PR workflow.

## License

AGPLv3. See [LICENSE](LICENSE).

By contributing, you agree your work will be licensed under the same terms.
