# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Live TV (Phase A): pluggable tuner abstraction with HDHomeRun and M3U/IPTV backends. HDHomeRun discovers via `/discover.json` + `/lineup.json`, filters DRM=1 channels, maps `503` to a stable `ALL_TUNERS_BUSY` error code. M3U accepts an HTTP URL or local file, parses Extended M3U with `tvg-id`/`tvg-chno`/`tvg-name`/`tvg-logo`, optional `User-Agent` passthrough for IPTV providers. Migration `00042_live_tv.sql` adds `tuner_devices`, `channels`, `epg_sources`, `epg_programs` tables with cascading deletes. Endpoints: `GET /api/v1/tv/channels` (with optional `?enabled=false` for the admin UI), `GET /api/v1/tv/channels/now-next` (LATERAL join surfaces current+next program per channel), admin-gated tuner CRUD under `/api/v1/tv/tuners` plus `POST /tuners/{id}/rescan`. Live HLS proxy at `GET /api/v1/tv/channels/{id}/stream.m3u8` + `/segments/{name}` runs ffmpeg `-c copy` (stream-copy keeps CPU near zero) with refcounted shared sessions — multiple viewers on the same channel share one tuner slot, with a 30s grace window after the last viewer disconnects so navigation churn doesn't burn re-tunes. Web UI: `/tv` channels page with now/next + progress bars; `/tv/{id}` player with hls.js, channel info banner, P/N keyboard channel switching. Phase B (Schedules Direct EPG + DVR scheduler) is the next milestone — the schema is in place
- Photo EXIF search `GET /api/v1/photos/search?library_id=&camera_make=&camera_model=&lens_model=&aperture_min=&aperture_max=&iso_min=&iso_max=&focal_min=&focal_max=&from=&to=&has_gps=&limit=&offset=`. Text fields use case-insensitive substring match (`ILIKE %term%`) so "sony" matches "SONY"; numeric ranges are inclusive on both ends; `has_gps` is tri-state (`true`/`false`/absent). All filters AND-combined and optional. INNER JOIN on `photo_metadata` so EXIF-less photos (screenshots, PNGs) never match — `/photos` is the right endpoint for those. Response carries the EXIF fields the search filtered on (lens, aperture, ISO, focal length, GPS) so the result UI can render "matched on f/2.8, ISO 6400" hints inline without a per-row `/items/{id}/exif` round-trip
- Photo map view `GET /api/v1/photos/map?library_id=&min_lat=&max_lat=&min_lon=&max_lon=&limit=` returns geotagged photos (`id`, `lat`, `lon`, `taken_at`, `poster_path`) for client-side rendering with MapLibre/Leaflet + supercluster. Bbox edges are independently optional — pass none to get the whole library, capped at `limit` (default 5000, hard ceiling 25000). Lat/lon range-validated server-side to reject `lat=200`-style garbage before it hits the SQL filter. Response envelope's `total` is the library-wide geotagged-photo count (ignoring bbox) so the UI can show "showing N of M — zoom in to see more" when truncated. Antimeridian crossings (e.g. `min_lon=170, max_lon=-170`) must be issued as two requests client-side; SQL filter is a straight BETWEEN
- Photo albums: user-curated groupings of photos under `/api/v1/photo-albums` (CRUD + items: `GET/POST` `/photo-albums`, `PATCH/DELETE/GET /photo-albums/{id}`, `GET/POST/DELETE /photo-albums/{id}/items[/{itemId}]`). Albums live in the existing `collections` table (migration `00041_photo_albums.sql` widens the `type` CHECK to allow `'photo_album'`) so they reuse the cascading deletes and uniqueness constraints already in place. Item-list join with `photo_metadata` returns `taken_at`, dimensions, camera make/model in one round-trip; ordered by `COALESCE(taken_at, created_at) DESC` since photo albums are reviewed chronologically. Cover image surfaces the most-recently-taken photo. Adds reject non-photo media items (movies/episodes) at the AddItem boundary with 400 rather than silently filtering. All endpoints are owner-scoped — foreign-owned and non-album rows return 404 to avoid leaking existence
- Photo server: on-demand resize endpoint `GET /api/v1/items/{id}/image?w=&h=&fit=&q=` with disk cache (SHA-256 keyed by source path + options, 2-byte directory sharding). Width/height clamp to 4096; quality clamps to 95. `fit=contain` (default) preserves aspect ratio inside the box; `fit=cover` fills it. HEIC/HEIF sources decode through ffmpeg (`-f mjpeg pipe:1`); JPEG/PNG decode in-process. EXIF orientation tag (1–8) is read and the image is rotated/mirrored before resize so phone-portrait photos land upright. Cache-Control set to `private, max-age=3600, immutable` since the cache key embeds dimensions. 503 when image server isn't configured; 404 for non-photo items
- Photo browse list `GET /api/v1/photos?library_id=&from=&to=&limit=&offset=` with optional RFC3339 from/to date filter (inclusive); orders by `COALESCE(taken_at, created_at) DESC`. Returns camera make/model, dimensions, and orientation alongside the standard envelope so the grid can render width/height-aware tiles without a second EXIF round-trip
- Photo timeline `GET /api/v1/photos/timeline?library_id=` returns `(year, month, count)` buckets so the client can render a sticky-section sidebar
- Transcode Start now supersedes any prior session for the same `(user_id, item_id)` pair before creating the new one — last-writer-wins semantics matching Plex/Jellyfin. Starting playback on a phone implicitly stops the in-progress session on the TV instead of holding two GPU slots and orphan playlists per user. Old sessions are killed (embedded worker via `KillSession`, remote workers via Valkey delete + heartbeat-loop notice), their segment tokens revoked, their on-disk session dirs reaped. Each supersede emits an `audit.ActionTranscodeStop` row with `reason: "superseded"` so operators can see why a player went dark
- Migration round-trip integration test (`go test -tags=integration ./internal/db/migrations`) runs `goose up → down-to 0 → up` against a real Postgres testcontainer and verifies the final applied version matches the highest embedded migration. Catches the most common Down-block bug class — references to columns that no longer exist or files that don't compile — without per-migration data fixtures
- Backup/restore round-trip is gated on schema version. Download now stamps the dump filename with `-vN.dump` and emits an `X-OnScreen-Schema-Version` response header. Restore peeks at the dump's `goose_db_version` table (via `pg_restore --data-only --table=goose_db_version --file=-`) before running the destructive restore. If the dump is from a newer schema than the running binary expects, the restore is refused with `409 DUMP_NEWER_THAN_SERVER` and the operator can retry with `?force=true`. If the dump is from an older schema, `pg_restore --clean --if-exists` runs and the handler then runs `goose up` against the restored database so the schema ends up matching the running build — no follow-up step required. Restore response now includes `dump_version`, `server_version`, `migrated`, and `migrate_error` so the UI can surface what happened. Integration round-trip test (`go test -tags=integration ./internal/api/v1 -run TestBackup_RoundTrip_Integration`) seeds a sentinel row, dumps, deletes, restores, and verifies the row returns
- Public URL, log level and CORS allow-list moved from env vars to admin Settings → Server. Stored under settings key `general_config`; bootstrap-read via a one-shot `pgx.Conn` at process startup so they're available before the logger is built and before HTTP handlers are constructed. Restart required after changes (the UI surfaces this notice). Removed `BASE_URL`, `LOG_LEVEL` and `CORS_ALLOWED_ORIGINS` env vars; SIGHUP no longer hot-reloads log level. `.env.example` restructured to "Bind addresses & infrastructure" since the only remaining optional env vars are bootstrap class — things needed to bind sockets or open the read replica before the UI exists
- Email/SMTP configuration moved from env vars to admin Settings → Email. Sender resolves credentials from `server_settings` on every send so admins can flip enabled/disabled and rotate passwords without restarting. New tab includes a "send test email" button. Removed `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, and `Config.SMTPEnabled()` — config is now stored under settings key `smtp_config`
- OpenTelemetry tracing configuration moved from env vars to admin Settings → Observability. Endpoint, sample ratio, deployment-env tag and an enable toggle are stored under settings key `otel_config`. The tracer provider is built once at startup, so a server/worker restart is required after a change (the UI surfaces this notice). A bootstrap one-shot `pgx.Conn` reads the config before the instrumented pool is built — necessary because `otelpgx` captures the global TracerProvider at `pgxpool.NewWithConfig` time. Removed `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_TRACES_SAMPLER_ARG`, and `DEPLOYMENT_ENV` env vars (also dropped from docker-compose)
- Audiophile music metadata: scanner reads bit depth, sample rate, channel layout, lossless flag, ReplayGain (track + album, gain + peak), and full MusicBrainz ID set (recording, release, release group, artist, album-artist) plus disc/track totals, original year, compilation flag, and release type
- Web album page surfaces audio quality: per-track Hi-Res / Lossless badge plus a track-detail modal showing container, codec, bit depth, sample rate, channels, bitrate, ReplayGain values, and clickable MusicBrainz links
- Subtitle OCR: image-based subtitle streams (PGS, VOBSUB, DVB, XSUB) are converted to WebVTT via ffmpeg + tesseract and persisted as `external_subtitles` rows so any client gets text-based playback. On-demand endpoint plus `ocr_subtitles` scheduled task that walks libraries with `skip_existing` defaulting to true. 12 language packs mapped; `Available()` check at startup degrades gracefully when binaries are missing
- External subtitles: OpenSubtitles search and download wired into the player picker; OCR'd and downloaded subs flow through the same `external_subtitles` table
- `bit_depth`, `sample_rate`, `channel_layout`, `lossless`, `replaygain_track_gain/peak`, `replaygain_album_gain/peak` on `ItemFileResponse`; `musicbrainz_*`, `disc_total`, `track_total`, `original_year`, `compilation`, `release_type` on `ItemDetailResponse`
- Audio-only safeguard in transcode decision logic so music files with empty video codec aren't forced to transcode
- OpenTelemetry distributed tracing (OTLP/gRPC) — disabled when `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, so instrumentation is free. Auto-instruments HTTP (otelchi, spans named by chi route template) and pgx (otelpgx). Custom spans on `scanner.library` and `transcode.run_job`. Slog records carry `trace_id`/`span_id` when a span is active. Server and worker register as separate services. Config: `OTEL_TRACES_SAMPLER_ARG` (sample ratio), `DEPLOYMENT_ENV` (resource attribute)
- Jaeger all-in-one in `docker/docker-compose.yml` under the `tracing` profile (`docker compose --profile tracing up`) — UI at `:16686`, OTLP gRPC at `:4317`

### Changed

- `media_files` schema gained 8 audio-quality columns (migration `00040_music_audiophile.sql`); `media_items` gained 10 music-context columns

## [1.1.2] - 2026-04-19

### Added

- Android TV (Leanback) client with library browse, episode picker, and direct-play streaming
- User favorites with hub-page surfacing
- Chapter navigation in the video player (jump-to-chapter, next/prev chapter buttons)
- TVDB show-level metadata fallback when TMDB has no match
- Admin maintenance endpoints: `POST /api/v1/maintenance/dedupe-shows`, `POST /api/v1/maintenance/dedupe-movies`, art backfill
- Two-pass dedupe: identical-normalized-title pass plus prefix-extension pass for unenriched folder-name dupes (e.g. "Adventure Time With Finn And Jake" → enriched "Adventure Time")

### Fixed

- Scanner creating duplicate shows when episode rows crowded the title-search result set
- Scanner soft-deleting all shows and seasons on every scan (cascade walk treated parents as orphans)
- Docker build silently swallowing `go build` failures via background-job `wait` masking exit codes; serialized builds with explicit `test -x` gate
- Dedupe normalization now handles bare trailing year, apostrophes, colons/hyphens, `&` vs `and`, and HTML-escaped `&amp;`
- Year-conflict guard prevents remakes (e.g. "Heroes" 2006 vs "Heroes" 2024) from merging

---

## [1.1.1] - 2026-04-03

### Added

- JavaScript-based subtitle renderer replacing native `<track>` elements for reliable HLS/MSE playback
- Subtitle sync/delay adjustment (±0.5s) in both desktop and mobile player menus
- PTS offset detection for accurate subtitle timing on container-shifted sources
- Touch-interactive seek bar with swipe-to-seek, double-tap skip, and swipe-down dismiss
- Mobile-optimized bottom sheet menus for subtitles, quality, and audio track selection
- Orientation lock to landscape on fullscreen for mobile devices
- Safe area insets for notched/dynamic island phones
- Subtitle font size control (small/medium/large) with localStorage persistence
- HDR tonemapping notice: "Enable HDR on your display for best performance"

### Fixed

- HLS seek sending video back to the beginning — `videoEl.duration` is `Infinity` for live HLS streams; now falls back to `seekable`/`buffered` ranges
- Seek within remux sessions now preserves remux mode instead of restarting as full transcode
- Artwork `srcset` parsing errors for titles with spaces (e.g., "GoodFellas") — URL-encode all artwork paths with `encodeURI()`
- PostgreSQL connection pool exhaustion (97 stale connections) — cap pool at 20, add health checks, 3-min idle timeout
- Graceful shutdown on Windows (`deploy.ps1`) and Docker (`STOPSIGNAL SIGTERM`, 35s grace period)

### Changed

- HLS.js buffer strategy: cap `maxMaxBufferLength` = `maxBufferLength` = 30s (Jellyfin pattern) to prevent live-edge stalls
- Connection pool sizing: `cpus * 2` capped at 20, with 15-min max lifetime and 30s health checks

---

## [1.1.0] - 2026-03-31

### Added

- Theme toggle (light/dark mode) with system preference detection and FOUC prevention
- Subtitle and audio language preference settings per user
- Audio track picker in the video player
- Parental content filtering with configurable max rating per user
- In-app notification system with SSE real-time delivery
- Notification bell with unread badge and mark-read support
- Image proxy/thumbnailer with `?w=` resize, responsive `srcset`, and CDN-friendly cache headers
- Mobile player gestures: horizontal swipe to seek, vertical swipe for volume/brightness
- Double-tap left/right to skip ±10s with ripple animation
- Transcode fleet management UI with worker status, encoder info, and session monitoring
- Configurable encoder tuning (NVENC options, max bitrate) via Transcode settings tab
- GPU-preferred worker dispatch for multi-worker deployments
- Multi-worker Docker Compose deployment support
- HEVC (H.265) direct play support for Safari and other HEVC-capable browsers
- 4K transcoding defaults: 4K sources default to 4K output quality
- HDR tonemapping: CUDA, OpenCL, and software (zscale) fallback chain
- OpenCL HDR tonemapping support with comprehensive test coverage
- Infinite scroll in library grid (replaces load-more button)
- Responsive `srcset` on all content images for bandwidth-efficient loading
- GPU Docker image (`Dockerfile.gpu`) with CUDA/NVENC FFmpeg
- `libzimg` in FFmpeg Docker image for software tonemapping via zscale filter
- API and transcode load test tooling

### Fixed

- HLS playback stalls: buffer before play, disable live sync, HEVC fMP4 segments
- HEVC/TS segment format mismatch: worker now stamps actual encoder output format
- NVENC 4K transcode failures
- NVENC 10-bit encode failure: force `yuv420p` for H.264 output
- NVENC HEVC hang: use explicit cuvid decoder (Jellyfin pattern)
- NVENC software decode + GPU encode path (CUDA hwdec hangs on HEVC)
- Transcode timeout for Blu-ray rips with many PGS subtitle tracks
- Mass-missing files during container restarts
- Infinite scroll binding issue: use Svelte action instead of `bind:this`
- Docker Compose file fix

### Changed

- HDR transcode startup speed: reduced probe time, increased playlist deadline
- Docker build speed improvements for GPU images

---

## [1.0.0] - 2026-03-30

### Added

- Library management for movies, TV shows, and music with recursive folder scanning
- TMDB and TVDB metadata enrichment: posters, fanart, ratings, genres, and summaries
- TheAudioDB enrichment for artist and album artwork in music libraries
- Web-based video player with HLS adaptive bitrate transcoding
- Direct play with HTTP byte-range support for native client playback
- Continue Watching and Recently Added hubs on the home page
- Full-text search across all libraries
- Watch history tracking via immutable event-sourced playback events
- Analytics dashboard with play counts, bandwidth, codec distribution, and top played items
- User management with admin panel and role-based access (admin/user)
- PIN-based user switching for family/shared setups
- OAuth SSO with Google, GitHub, and Discord
- Email-based password reset with SMTP support
- Invite system for controlled user registration
- Webhook notifications for media events and library scans (HMAC-SHA256 signed, 3-attempt retry)
- HLS adaptive bitrate streaming with hardware encoder auto-detection (NVENC, VAAPI, software)
- Bounded HLS disk usage with automatic segment deletion
- Server settings management (TMDB/TVDB API keys) via UI and API
- TV show scanning with S##E## filename parsing and show/season/episode hierarchy
- Music library scanning with ID3/FLAC/Vorbis tag reading and artist/album/track hierarchy
- Manual metadata matching when auto-match gets it wrong
- Collections (playlists) with full CRUD and auto-genre queries
- Sonarr, Radarr, and Lidarr webhook receiver with path mapping
- Audit logging for admin actions with configurable retention
- Multi-user authentication with Paseto v4 tokens and refresh token rotation
- Rate limiting: 10 req/min per IP on auth, 1000 req/min per session on authenticated endpoints
- Security headers: CSP, X-Frame-Options, X-Content-Type-Options, Permissions-Policy
- Webhook SSRF protection via SafeTransport (rejects private/loopback IPs)
- Worker health endpoint (`/health/live`, `/health/ready`) for Docker/load balancer health checks
- Prometheus metrics: HTTP requests, transcode sessions, scanner progress, watch events
- OpenTelemetry tracing support (optional)
- Structured JSON logging with request-scoped context (request ID, user ID)
- Hot-reloadable configuration via SIGHUP (log level, scan/transcode concurrency)
- Graceful shutdown with SIGTERM/SIGINT handling
- Read-write database separation with optional read-only replica support
- Docker deployment support (multi-stage Dockerfile, docker-compose, HA example with pgBouncer + Nginx)
- Cross-platform release builds (Linux amd64/arm64, macOS amd64/arm64, Windows amd64)
- GitHub Actions CI/CD pipeline (build, lint, test, Docker build, release with checksums)
- Comprehensive documentation: ARCHITECTURE.md, API.md, CONTRIBUTING.md, deployment guide
- AGPLv3 license
