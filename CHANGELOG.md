# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
