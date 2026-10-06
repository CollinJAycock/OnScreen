# OnScreen

A modern, open-source media server. PostgreSQL-native. Single binary. Native clients across web, desktop, TV, and phone.

> **Status:** v2.5.0 is the current release; [CHANGELOG.md](CHANGELOG.md) has the full list and the upgrade notes from v2.4.x. Since v2.4, a breaking API change lands only when every first-party client moves with it (see [docs/server-lock.md](docs/server-lock.md)). A private beta is running. Headline v2.5: **request depth** (per-user quotas and auto-approve, season-level TV requests, live Radarr/Sonarr download status, an Upcoming calendar); **watch marks**, Continue Watching controls and **Next Up**; **Trakt and Last.fm scrobbling**; **report a problem** plus an admin **Library health** page; **TMDB franchise collections**; **notification agents** (Discord, Telegram, ntfy, Gotify, email); **music browse**; audiobook **speed and bookmarks**; a **photo map and albums** in the web app; the **TV web app at `/tvapp/`**, the front end of the Xbox app (phase 0); and an opt-in **file integrity probe**. Per-platform store-submission state in [docs/comparison-matrix.md](docs/comparison-matrix.md); the v2.5 plan in [docs/v2.5-roadmap.md](docs/v2.5-roadmap.md).

## Why another media server?

Plex, Jellyfin, and Emby are all great — OnScreen exists because we wanted something that:

- Runs on **PostgreSQL** instead of SQLite, so it scales past a single machine and plays well with existing database tooling.
- Ships as a **single Go binary** plus a static SvelteKit bundle — no plugin host, no runtime metadata server, no Python.
- Treats **watch state as an event log** (immutable play/pause/seek/stop), not a mutable `last_viewed_at` column. A database trigger rolls the log up into per-user progress, so every client reads the same state, and marking something watched never rewrites the log, so play counts and analytics stay intact.
- Has a **native web player** built around HLS with hardware-accelerated transcoding (NVENC, QSV, VAAPI), HDR tonemapping, and proper mobile gestures — not a third-party web-only skin over Plex's stream URLs.
- **Live TV, DVR, and hardware transcoding ship in core**, no Plex Pass / Emby Premiere gate.
- **OIDC, SAML, and LDAP** are first-class auth providers, no plugin install.
- A **native bit-perfect audio engine** (Windows WASAPI exclusive + DSD-via-DoP + ReplayGain enforcement) ships in the desktop client today.
- Is **AGPLv3**. Forks and self-hosters get the same freedom the code was written with. (The LG webOS client and the Xbox TV app are Apache-2.0; see [License](#license).)

For the full feature comparison vs Plex / Emby / Jellyfin (12 sections, plus "Where OnScreen leads / trails"), see [docs/comparison-matrix.md](docs/comparison-matrix.md). Highlights:

| | OnScreen | Plex | Jellyfin | Emby |
|--|--|--|--|--|
| Database | PostgreSQL | SQLite | SQLite | SQLite |
| License | AGPLv3 (LG webOS + Xbox TV app: Apache-2.0) | Proprietary | GPLv2 | GPLv2 + proprietary server |
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
- TMDB franchise collections, created automatically once two films of a collection are in the library; the collection page lists the missing films with a Request button
- Audiophile-grade music: ID3/Vorbis/MP4 tag reading, MusicBrainz IDs, ReplayGain (track + album, applied by the web player and the desktop engine), bit depth, sample rate, channel layout, lossless detection
- Audiobook hierarchy: `book_author → book_series → audiobook → audiobook_chapter` with multi-file resume snapping to chapter boundary
- Photo libraries with EXIF (camera, lens, GPS, capture time), date-grouped browsing, EXIF search, a map view of geotagged photos (web app and Android phone app), and photo albums in the web app
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
- Trickplay seek-bar thumbnails, generated automatically after scans with a nightly backfill (per-library switch)
- Intro / credits markers (auto + manual) with per-episode skip prompts
- Chapter navigation (jump-to-chapter, next/prev buttons)
- "Play on…" — send what you're watching in the web player to another of your devices
- Home rows: Continue Watching split into TV / Movies / Other (titles can be removed), Next Up, Plan to Watch, Recently Added per library, Trending; smart playlists (rule-based, query-time eval)
- Mark watched / unwatched on items, seasons and shows; watched badges, unwatched counts, a watch filter and "Surprise me" in library grids
- Event-sourced watch state (immutable `watch_events` partitioned by month, rolled up per user and item by a trigger)

**Native clients**
- **Web** (SvelteKit) — touch-optimised player, bottom-sheet menus, orientation lock, safe-area insets
- **Desktop** (Tauri 2; Windows and Linux installers attached to each GitHub Release, not yet code-signed; macOS isn't built, since cpal fails to compile there with E0277 — see [clients/desktop/README.md](clients/desktop/README.md)) — reuses the SvelteKit bundle in a system webview; native Rust audio engine outside the webview decodes through symphonia 0.5 and writes raw `IAudioClient` in `AUDCLNT_SHAREMODE_EXCLUSIVE` (bit-perfect, OS mixer bypassed); DSD-via-DoP; ReplayGain enforcement; OS now-playing widget; OS media keys; system tray
- **Android TV / Google TV** (Leanback + Media3) — 1.4.x in Google Play testing tracks (closed testing since 2026-05-13; not yet in production).
- **Fire TV** (a `firetv` flavor of the Android TV app) — live on the Amazon Appstore since 2026-06-18 with the 1.0.x build; 1.4.2 submitted to Amazon 2026-10-06.
- **Android phone** (Compose + Material 3) — book/comic reader (CBZ/CBR/EPUB), Chromecast support, picture-in-picture, WorkManager-backed offline downloads, pair-PIN SSO bridge. 0.3.0 in a Google Play testing track (not yet in production).
- **Samsung Tizen** (SvelteKit + tizen-package) — hardware-verified on a Samsung QN75Q80B 2022 panel; AVPlay HEVC + HDR10 + audio passthrough confirmed. 1.1.0 submitted to Samsung 2026-10-06, its first store submission.
- **LG webOS** (SvelteKit + ares-package) — first run on LG hardware 2026-10-01; 0.2.1 submitted to the LG Content Store 2026-10-02.
- **Roku** (BrightScript + SceneGraph) — has the v2.5 viewer features in code, but has never run on a device.
- **Xbox** — phase 0: a UWP WebView2 shell over the TV web app the server serves at `/tvapp/`; not yet run on a console.
- **`onscreen-cli`** — a terminal client that browses a server and plays through mpv.
- See [docs/comparison-matrix.md](docs/comparison-matrix.md) for current per-platform store-submission state, and [docs/store-assets/](docs/store-assets/) for all the screenshots / icons / banners uploaded to each console.

**Multi-user & policy**
- OIDC, SAML 2.0 SP-initiated SSO with JIT provisioning, LDAP with group sync — all core, no plugin install
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
- Notification agents — Discord, Telegram, ntfy, Gotify and email — for requests, reported problems, new content, failed tasks/backups and transcode-worker outages
- TMDB discover + request workflow inline in search — no Overseerr / Ombi / Jellyseerr companion needed: per-user auto-approve and quotas, season-level TV requests, an approve dialog (instance / quality profile / root folder), live Radarr/Sonarr download status, and an Upcoming calendar
- "Report a problem" for users, and an admin Library health page (open reports, integrity-probe results, unmatched / missing-art counts) with one-click Radarr/Sonarr re-grab
- `/health/ready` gated on schema-vs-code parity — container stays unhealthy until `goose up` has run; optional `AUTO_MIGRATE=true` applies pending migrations on startup for single-container deploys with no separate migrate step
- Backup/restore round-trip with schema-version gating (`409 DUMP_NEWER_THAN_SERVER` on a too-new dump; `pg_restore --clean --if-exists` + `goose up` on an older one)
- Prometheus metrics on a separate port
- OpenTelemetry tracing (OTLP/gRPC) — auto-instruments HTTP + Postgres; logs carry trace IDs
- Admin logs API (in-process 2000-entry slog ring buffer) for environments without SSH/kubectl access
- Audit log of admin / playback / auth events
- Analytics dashboard (play counts, bandwidth, codec distribution, top played) with Now Playing — user, device, LAN/remote, decision and transcode reasons — and an admin Stop for any stream

## Quick Start

### Prerequisites

- Go 1.26+ and Node.js 24+ (only for building from source)
- PostgreSQL 16+
- Valkey (or Redis) 7+
- FFmpeg for transcoding — `onscreen-ffmpeg` image if you want NVENC/HDR

### Docker (recommended)

Release images are published to `ghcr.io/collinjaycock/onscreen` (tags `2.5.0`, `2.5`, `2`). To build your own instead: `docker build -f docker/Dockerfile -t onscreen .`

```bash
docker run -p 7070:7070 \
  -e DATABASE_URL="postgres://onscreen:${DB_PASS}@postgres:5432/onscreen?sslmode=disable" \
  -e VALKEY_URL="redis://valkey:6379" \
  -e SECRET_KEY="$(openssl rand -hex 32)" \
  -v /your/media:/media:ro \
  ghcr.io/collinjaycock/onscreen:2.5.0

# Prometheus metrics (and /debug/pprof) bind to 127.0.0.1:7071 inside the
# container by default. To scrape them, add
#   -p 127.0.0.1:7071:7071 -e METRICS_ADDR=0.0.0.0:7071
# and keep that port off the public network.

# Migrations are bundled. Either run them once per release:
docker exec <container> sh -c 'goose -dir /migrations postgres "$DATABASE_URL" up'
# (equivalently: docker exec <container> /usr/local/bin/server migrate)
# …or set AUTO_MIGRATE=true on the container to apply them on startup —
# recommended for single-container deploys with no separate migrate step.
```

For GPU transcoding, see [docker/Dockerfile.gpu](docker/Dockerfile.gpu) and the multi-worker example in [docs/deployment.md](docs/deployment.md).

### Dev setup

```bash
# 1. Start dependencies. docker-compose.yml requires a DB password; keep it in
#    docker/.env (gitignored), which both compose and the Makefile read.
echo "DB_PASS=$(openssl rand -hex 24)" >> docker/.env
docker compose -f docker/docker-compose.yml up -d postgres valkey

# 2. Run migrations (DATABASE_URL defaults to the local DB with that password)
make migrate

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
| `METRICS_ADDR` | | Prometheus metrics + pprof bind (default `127.0.0.1:7071`; unauthenticated, so expose only on a private network) |
| `TRUSTED_PROXIES` | | Comma-separated CIDRs / IPs allowed to set `X-Forwarded-*` (unset trusts any loopback or private-network peer; set it to your reverse proxy) |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | | Built-in HTTPS (operator-provided PEM) |
| `PUBLIC_SEGMENT_BASE_URL` | | Host that players fetch HLS playlists / segments from, e.g. a direct connection that bypasses a CDN or tunnel (default same-origin) |
| `RTMP_ENABLED` / `RTMP_LISTEN_ADDR` | | Embedded RTMP ingest for OBS / ffmpeg "go live" broadcasts (default on, `:1935`) |
| `DISCOVERY_ENABLED` / `DISCOVERY_PORT` | | LAN discovery broadcast (default on, UDP `7368`) |
| `ALLOW_PUBLIC_SETUP` | | Allow first-run admin creation from a public address (default off: loopback / private network only) |
| `NODE_ID` | | This node's identity for its per-node settings row (default the host name) |
| `TMDB_API_KEY` | | TMDB v3 key — seeded into Settings on first run |
| `TVDB_API_KEY` | | TVDB v4 key — seeded into Settings on first run |
| `AUTO_MIGRATE` | | Apply pending DB migrations on startup (default off; for single-container deploys) |
| `VALKEY_SENTINEL_ADDRS` | | Sentinel addrs → HA Valkey failover (default off) |
| `PUBLIC_ASSET_CACHE` | | Make app-served artwork CDN-cacheable (default off; or **Settings ▸ System**) |
| `STATIC_ABR_ENABLED` | | Pre-encode popular titles' ABR ladders to the store (default off; or **Settings ▸ System**) |
| `SITE_ID` | | Names this site for multi-site DR; shown at `/health/cluster` |

Everything else — public URL, log level, CORS allow-list, OIDC / SAML / LDAP, SMTP, OpenTelemetry endpoint, transcode tuning, **object storage (S3 / MinIO / Backblaze B2 / Wasabi / R2)**, and cluster-wide toggles (ABR, retention, asset cache, static-ABR) under **Settings ▸ System** — is configured from the admin Settings UI, stored in `server_settings`. Env vars above act as the initial default; a stored override wins. Node/site-specific values (connection strings, secret key, bind addresses, paths, `SITE_ID`, per-worker hardware) stay env, since settings replicate across sites.

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full configuration reference, and [docs/dr-runbook.md](docs/dr-runbook.md) for HA / multi-site operations.

## Architecture

1. **PostgreSQL-native** — not a SQLite port. Uses partitioned tables, materialized views, and `tsvector` full-text search.
2. **Stateless API tier** — horizontally scalable behind a load balancer; session state lives in Valkey.
3. **Event-sourced watch state** — every play/pause/seek/stop is an immutable row in `watch_events`; current state is derived.
4. **Single binary** — `go build ./cmd/server` produces one executable with the web app and the TV app embedded.
5. **Plain SQL** — queries authored as `.sql` files and compiled to type-safe Go via [sqlc](https://sqlc.dev).

Full design: [ARCHITECTURE.md](ARCHITECTURE.md). REST reference: [docs/api/openapi.yaml](docs/api/openapi.yaml) (error codes: [docs/api/ERROR_CODES.md](docs/api/ERROR_CODES.md)).

## Development

```bash
make help          # show all targets
make build         # build frontend + TV app (/tvapp/) + server + worker
make test-unit     # fast unit tests (<10s)
make test-int      # integration tests (requires Docker)
make lint          # golangci-lint
make generate      # regenerate sqlc code
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full dev setup, code style, and PR workflow.

## License

AGPLv3. See [LICENSE](LICENSE).

Two exceptions are licensed under the Apache License 2.0: the LG webOS TV client in [`clients/webos`](clients/webos) (see [clients/webos/LICENSE](clients/webos/LICENSE)) and the Xbox TV app and shell in [`clients/xbox`](clients/xbox) (see [clients/xbox/LICENSE](clients/xbox/LICENSE)). The server embeds the `clients/xbox` build and serves it at `/tvapp/`; that code keeps its Apache-2.0 license inside the AGPLv3 server binary. The server and every other client remain AGPLv3.

By contributing, you agree your work will be licensed under the same terms as the part of the repository it changes: Apache-2.0 under `clients/webos` and `clients/xbox`, AGPLv3 everywhere else.
