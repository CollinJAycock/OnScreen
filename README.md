# OnScreen

A modern, open-source media server. PostgreSQL-native. Single binary. Native clients across web, desktop, TV, and phone.

![OnScreen hub page](screenshots/hero.png)

> **Status:** v2.1.0 tagged 2026-05-04 (beta running at https://onscreen.wolverscreen.com). Public API is stable; breaking changes are called out in [CHANGELOG.md](CHANGELOG.md).

## Why another media server?

Plex, Jellyfin, and Emby are all great — OnScreen exists because we wanted something that:

- Runs on **PostgreSQL** instead of SQLite, so it scales past a single machine and plays well with existing database tooling.
- Ships as a **single Go binary** plus a static SvelteKit bundle — no plugin host, no runtime metadata server, no Python.
- Treats **watch state as an event log** (immutable play/pause/seek/stop), not a mutable `last_viewed_at` column. Rewind a week with `DELETE FROM watch_events WHERE ts > ...`.
- Has a **native web player** built around HLS with hardware-accelerated transcoding (NVENC, QSV, VAAPI), HDR tonemapping, and proper mobile gestures — not a third-party web-only skin over Plex's stream URLs.
- **Live TV, DVR, and hardware transcoding ship in core**, no Plex Pass / Emby Premiere gate.
- **OIDC, OAuth, SAML, and LDAP** are first-class auth providers, no plugin install.
- A **native bit-perfect audio engine** (Windows WASAPI exclusive + DSD-via-DoP + ReplayGain enforcement) ships in the desktop client today.
- Is **AGPLv3**. Forks and self-hosters get the same freedom the code was written with.

For the full feature comparison vs Plex / Emby / Jellyfin (12 sections, plus "Where OnScreen leads / trails"), see [docs/comparison-matrix.md](docs/comparison-matrix.md). Highlights:

| | OnScreen | Plex | Jellyfin | Emby |
|--|--|--|--|--|
| Database | PostgreSQL | SQLite | SQLite | SQLite |
| License | AGPLv3 | Proprietary | GPLv2 | GPLv2 + proprietary server |
| Live TV / DVR | ✅ core | 💎 paid | ✅ core | 💎 paid |
| OIDC / SAML / LDAP | ✅ core | ❌ | 🧩 plugin | 💎 paid |
| Hardware transcode | ✅ core | 💎 paid | ✅ core | 💎 paid |
| Bit-perfect WASAPI | ✅ core | 💎 Plexamp | ❌ | ❌ |
| All books (CBZ + CBR + EPUB) | ✅ core | ❌ | ⚠ partial | ⚠ partial |

## Features

**Library**
- Movies, TV shows, music, photos, **audiobooks**, **books / comics** (CBZ + CBR + EPUB), **music videos**, **home videos**, podcasts (local files); all scanned with ffprobe / EXIF / tag readers
- TMDB + TVDB + MusicBrainz metadata enrichment with Cover Art Archive fallback
- Audiophile-grade music: ID3/Vorbis/MP4 tag reading, MusicBrainz IDs, ReplayGain (track + album), bit depth, sample rate, channel layout, lossless detection
- Audiobook hierarchy: `book_author → book_series → audiobook → audiobook_chapter` with multi-file resume snapping to chapter boundary
- Photo libraries with EXIF (camera, lens, GPS, capture time), date-grouped browsing, EXIF search, map view, user-curated photo albums
- Home videos as a distinct type with on-disk metadata edits (rename file + stamp mtime so user titles travel across tools)
- Two-pass admin dedupe for shows/movies (handles `"Title"` vs `"Title YYYY"`, apostrophes, `&` vs `and`, HTML entities, prefix-extension folder names)

**Playback**
- Native SvelteKit web player with direct play, remux, and full transcode fallback
- HLS transcoding via FFmpeg with hardware encoder auto-detection (NVENC, QuickSync, AMF, VAAPI), AV1 encode on supported hardware
- HDR → SDR tonemapping (CUDA, OpenCL, or software zscale fallback)
- HEVC direct play on Safari and other HEVC-capable browsers
- JavaScript subtitle renderer with PTS offset detection and ±0.5s sync adjust
- Subtitle OCR for image-based formats (PGS, VOBSUB, DVB) — ffmpeg + tesseract converts cues to WebVTT
- OpenSubtitles search and download from inside the player; OCR'd and downloaded subs share one `external_subtitles` table
- Per-session supersede — one stream per user/item; opening the same item on a phone stops the in-progress TV session
- Trickplay seek-bar thumbnails generated from the source file
- Intro / credits markers (auto + manual) with per-episode skip prompts
- Chapter navigation (jump-to-chapter, next/prev buttons)
- Continue Watching split into TV / Movies / Other rows; Recently Added per library; Trending row; smart playlists (rule-based, query-time eval)
- Event-sourced watch state (immutable `watch_events` partitioned by month)

**Native clients**
- **Web** (SvelteKit) — touch-optimised player, bottom-sheet menus, orientation lock, safe-area insets
- **Desktop** (Tauri 2 on Windows / macOS / Linux) — reuses the SvelteKit bundle in a system webview; native Rust audio engine outside the webview decodes through symphonia 0.5 and writes raw `IAudioClient` in `AUDCLNT_SHAREMODE_EXCLUSIVE` (bit-perfect, OS mixer bypassed); DSD-via-DoP; ReplayGain enforcement; OS now-playing widget; OS media keys; system tray
- **Android TV / Google TV / Fire TV** (Leanback + Media3) — browse rows, episode picker, direct-play + transcode, OpenSubtitles in player, Watch Next launcher integration, MediaSessionService for background music, D-pad seek
- **Android phone** (Compose + Material 3) — pairing PIN sign-in, picture-in-picture for video, offline downloads
- **LG webOS / Samsung Tizen / Roku** — feature-complete (pairing → hub → search → playback → audio/subtitle pickers → cross-device resume)
- See [docs/comparison-matrix.md](docs/comparison-matrix.md) for current per-platform validation status.

**Multi-user & policy**
- OIDC, OAuth (Google / GitHub / Discord), SAML 2.0 SP-initiated SSO with JIT provisioning, LDAP with group sync — all core, no plugin install
- PASETO v4 local tokens, refresh rotation, per-file streaming token (24h, file_id-bound, purpose-scoped) so native players don't drop streams at access-token expiry
- Managed profiles (up to 6 per account) with per-profile watch state, favorites, language prefs
- Library `is_private` flag with public/private union semantics; auto-grant template for new users; admin "view as" middleware
- Parental content-rating ceiling per profile, enforced in hub queries, search, and items
- User favorites, in-app SSE notifications

**Operations**
- Theme toggle (light/dark) with system-preference detection and FOUC prevention
- Image proxy / thumbnailer with `?w=` resize, responsive `srcset`, CDN-friendly cache headers
- Transcode fleet management UI: worker status, encoder info, live session monitoring
- Multi-worker Docker Compose deployment support
- Webhooks with HMAC-SHA256 signing and retry (compatible with Overseerr/Tautulli receivers)
- TMDB discover + request workflow inline in search — no Overseerr / Ombi / Jellyseerr companion needed
- `/health/ready` gated on schema-vs-code parity — container stays unhealthy until `goose up` has run
- Backup/restore round-trip with schema-version gating (`409 DUMP_NEWER_THAN_SERVER` on a too-new dump; `pg_restore --clean --if-exists` + `goose up` on an older one)
- Prometheus metrics on a separate port
- OpenTelemetry tracing (OTLP/gRPC) — auto-instruments HTTP + Postgres; logs carry trace IDs
- Admin logs API (in-process 2000-entry slog ring buffer) for environments without SSH/kubectl access
- Audit log of admin / playback / auth events
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

Bootstrap-class settings — needed before the admin Settings UI exists — live in env vars:

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | ✓ | PostgreSQL connection string |
| `VALKEY_URL` | ✓ | Valkey/Redis connection string |
| `SECRET_KEY` | ✓ | 32+ byte secret for token encryption |
| `MEDIA_PATH` | ✓ | Root path to media files |
| `DATABASE_RO_URL` | | Read replica DSN (falls back to `DATABASE_URL`) |
| `LISTEN_ADDR` | | API server bind address (default `:7070`) |
| `METRICS_ADDR` | | Prometheus metrics bind (default `:7071`) |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | | Built-in HTTPS (operator-provided PEM) |
| `TMDB_API_KEY` | | TMDB v3 key — seeded into Settings on first run |
| `TVDB_API_KEY` | | TVDB v4 key — seeded into Settings on first run |

Everything else — public URL, log level, CORS allow-list, OIDC / OAuth / SAML / LDAP, SMTP, OpenTelemetry endpoint, transcode tuning — is configured from the admin Settings UI, stored in `server_settings`, and bootstrap-read at startup. Restart required after changes; the UI surfaces this notice.

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
