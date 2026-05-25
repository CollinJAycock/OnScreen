# OnScreen

A modern, open-source media server. PostgreSQL-native. Single binary. Native clients across web, desktop, TV, and phone.

![OnScreen hub page](screenshots/hero.png)

> **Status:** v2.4 in active development (v2.3.0 was the last tagged release; the [server-lock posture](docs/server-lock.md) was lifted after v2.3.0, so v2.4 can land coordinated breaking changes across the client fleet). A private beta is running. Headline v2.4 work: a **multi-node transcode fleet** with cost-weighted, capability-aware dispatch (storage-less workers pull the source over HTTP); an **on-demand adaptive-bitrate HLS ladder** (H.264 / HEVC / AV1 rungs); **GPU HDR→SDR tonemap via libplacebo/Vulkan**; **TOTP two-factor auth** and purpose-scoped asset tokens. Per-platform store-submission state in [docs/comparison-matrix.md](docs/comparison-matrix.md). Breaking changes are called out in [CHANGELOG.md](CHANGELOG.md).

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
| Multi-node transcode fleet | ✅ core | ❌ | ❌ | ❌ |

## Features

**Library**
- Movies, TV shows, **anime** (typed library with AniList primary metadata), music, photos, **audiobooks**, **books / comics** (CBZ + CBR + EPUB), **music videos**, **home videos**, podcasts (local files); all scanned with ffprobe / EXIF / tag readers
- TMDB + TVDB + AniList + MusicBrainz metadata enrichment with Cover Art Archive fallback
- Watching status (Plan to Watch / Watching / On Hold / Completed / Dropped) — generic, not anime-only — synced across every client
- Audiophile-grade music: ID3/Vorbis/MP4 tag reading, MusicBrainz IDs, ReplayGain (track + album), bit depth, sample rate, channel layout, lossless detection
- Audiobook hierarchy: `book_author → book_series → audiobook → audiobook_chapter` with multi-file resume snapping to chapter boundary
- Photo libraries with EXIF (camera, lens, GPS, capture time), date-grouped browsing, EXIF search, map view, user-curated photo albums
- Home videos as a distinct type with on-disk metadata edits (rename file + stamp mtime so user titles travel across tools)
- Two-pass admin dedupe for shows/movies (handles `"Title"` vs `"Title YYYY"`, apostrophes, `&` vs `and`, HTML entities, prefix-extension folder names)

**Playback**
- Native SvelteKit web player with direct play, remux, and full transcode fallback
- HLS transcoding via FFmpeg with hardware encoder auto-detection (NVENC, QuickSync, AMF, VAAPI), AV1 encode on supported hardware
- On-demand adaptive-bitrate HLS ladder (Jellyfin-style — one ffmpeg per rung, transcoded on demand) with H.264 / HEVC / AV1 rungs, capped at the client-requested height
- HDR → SDR tonemapping on the GPU via libplacebo/Vulkan (vendor-agnostic), with OpenCL and software zscale fallbacks
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
- **Android TV / Google TV** (Leanback + Media3) — on the Play Store **closed-testing track** as of 2026-05-13. Same APK serves Fire TV via Amazon Appstore (review queue, pending appeal).
- **Android phone** (Compose + Material 3) — book/comic reader (CBZ/CBR/EPUB), Chromecast support, picture-in-picture, WorkManager-backed offline downloads, pair-PIN SSO bridge. Submitted to Play Store 2026-05-13.
- **Samsung Tizen** (SvelteKit + tizen-package) — hardware-verified on a Samsung QN75Q80B 2022 panel; AVPlay HEVC + HDR10 + audio passthrough confirmed. Samsung Apps Store submission prepped under [`clients/tizen/SAMSUNG_APP_DESCRIPTION.md`](clients/tizen/SAMSUNG_APP_DESCRIPTION.md).
- **LG webOS** (SvelteKit + ares-package) — code-complete; real LG-hardware soak still pending.
- **Roku** (BrightScript + SceneGraph) — code-complete; real-device soak pending.
- See [docs/comparison-matrix.md](docs/comparison-matrix.md) for current per-platform store-submission state, and [docs/store-assets/](docs/store-assets/) for all the screenshots / icons / banners uploaded to each console.

**Multi-user & policy**
- OIDC, OAuth (Google / GitHub / Discord), SAML 2.0 SP-initiated SSO with JIT provisioning, LDAP with group sync — all core, no plugin install
- TOTP two-factor auth for self-hosted local accounts (enrolment + QR provisioning, verify-on-login across web and every native client), no vendor-cloud account required
- PASETO v4 local tokens, refresh rotation, a per-file streaming token (24h, file_id-bound) and a purpose-scoped asset token for cross-origin `?token=` URLs (artwork, SSE, players) — neither honoured as a general API Bearer, so native players don't drop streams at access-token expiry and a leaked asset URL can't become a general credential
- Managed profiles (up to 6 per account) with per-profile watch state, favorites, language prefs
- Library `is_private` flag with public/private union semantics; auto-grant template for new users; admin "view as" middleware
- Parental content-rating ceiling per profile, enforced in hub queries, search, and items
- User favorites, in-app SSE notifications

**Operations**
- Theme toggle (light/dark) with system-preference detection and FOUC prevention
- Image proxy / thumbnailer with `?w=` resize, responsive `srcset`, CDN-friendly cache headers
- Multi-node transcode fleet: a separate `worker` binary joins the primary's queue, with **cost-weighted, capability-aware dispatch** — a 4K stream weighs ~4× a 1080p one (not "one session"), HDR jobs route to GPU-tonemap nodes and AV1 output to AV1-capable nodes, with per-node load % and an admin-settable max-sessions cap in the fleet UI
- **Storage-less workers** pull the source from the primary over HTTP (per-file token) — no shared NFS/SMB mount required on every node; opt-in Intel QSV hardware HEVC decode offload per worker
- **Pluggable media storage** — local disk by default, or S3-compatible object storage (S3 / MinIO / Backblaze B2 / Wasabi / Cloudflare R2) set live from the admin UI; every read **and** write path routes through it, with `SignedURL` CDN offload so cacheable bytes skip the app tier
- **Optional HA across every tier** (all off by default): Valkey Sentinel, a multi-host Postgres failover DSN over streaming replication, **static-ABR** pre-encode of popular titles (served straight from the CDN, not the live fleet), and multi-site active/passive DR with per-site content addressing + a `/health/cluster` role/lag surface — see [docs/dr-runbook.md](docs/dr-runbook.md)
- Webhooks with HMAC-SHA256 signing and retry (compatible with Overseerr/Tautulli receivers)
- TMDB discover + request workflow inline in search — no Overseerr / Ombi / Jellyseerr companion needed
- `/health/ready` gated on schema-vs-code parity — container stays unhealthy until `goose up` has run; optional `AUTO_MIGRATE=true` applies pending migrations on startup for single-container deploys with no separate migrate step
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
  -v /your/media:/media:ro \
  onscreen

# Migrations are bundled. Either run them once per release:
docker exec <container> sh -c 'goose -dir /migrations postgres "$DATABASE_URL" up'
# (equivalently: docker exec <container> /usr/local/bin/server migrate)
# …or set AUTO_MIGRATE=true on the container to apply them on startup —
# recommended for single-container deploys with no separate migrate step.
```

For GPU transcoding, see [docker/Dockerfile.gpu](docker/Dockerfile.gpu) and the multi-worker example in [docs/deployment.md](docs/deployment.md).

### Dev setup

```bash
# 1. Start dependencies
docker compose -f docker/docker-compose.yml up -d postgres valkey

# 2. Run migrations
make migrate DATABASE_URL="postgres://onscreen:onscreen@localhost:5432/onscreen?sslmode=disable"

# 3. Run in dev mode (Go API on :7070, Vite on :5173)
make dev
```

Navigate to `http://localhost:5173`, create your admin account, add a library, and scan.

## Configuration

Bootstrap-class settings — needed before the admin Settings UI exists — live in env vars:

| Variable | Required | Description |
|----------|----------|-------------|
| `DATABASE_URL` | ✓ | PostgreSQL DSN. Accepts a multi-host failover DSN (`…@primary,standby/db?target_session_attrs=read-write`) for HA |
| `VALKEY_URL` | ✓ | Valkey/Redis connection string |
| `SECRET_KEY` | ✓ | 32+ byte secret for token encryption |
| `DATABASE_RO_URL` | | Read replica DSN (falls back to `DATABASE_URL`) |
| `LISTEN_ADDR` | | API server bind address (default `:7070`) |
| `METRICS_ADDR` | | Prometheus metrics bind (default `:7071`) |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | | Built-in HTTPS (operator-provided PEM) |
| `TMDB_API_KEY` | | TMDB v3 key — seeded into Settings on first run |
| `TVDB_API_KEY` | | TVDB v4 key — seeded into Settings on first run |
| `AUTO_MIGRATE` | | Apply pending DB migrations on startup (default off; for single-container deploys) |
| `VALKEY_SENTINEL_ADDRS` | | Sentinel addrs → HA Valkey failover (default off) |
| `PUBLIC_ASSET_CACHE` | | Make app-served artwork CDN-cacheable (default off; or **Settings ▸ System**) |
| `STATIC_ABR_ENABLED` | | Pre-encode popular titles' ABR ladders to the store (default off; or **Settings ▸ System**) |
| `SITE_ID` | | Names this site for multi-site DR; shown at `/health/cluster` |

Everything else — public URL, log level, CORS allow-list, OIDC / OAuth / SAML / LDAP, SMTP, OpenTelemetry endpoint, transcode tuning, **object storage (S3 / MinIO / Backblaze B2 / Wasabi / R2)**, and cluster-wide toggles (ABR, retention, asset cache, static-ABR) under **Settings ▸ System** — is configured from the admin Settings UI, stored in `server_settings`. Env vars above act as the initial default; a stored override wins. Node/site-specific values (connection strings, secret key, bind addresses, paths, `SITE_ID`, per-worker hardware) stay env, since settings replicate across sites.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full configuration reference, and [docs/dr-runbook.md](docs/dr-runbook.md) for HA / multi-site operations.

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
