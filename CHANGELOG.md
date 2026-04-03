# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
