# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - Unreleased

### Added

- Library management for movies, TV shows, and music with recursive folder scanning
- TMDB metadata enrichment: posters, fanart, ratings, genres, and summaries
- Web-based video player with HLS adaptive bitrate transcoding
- Direct play with HTTP byte-range support for native client playback
- Continue Watching and Recently Added hubs on the home page
- Full-text search across all libraries
- Watch history tracking via immutable event-sourced playback events
- Analytics dashboard with play counts, bandwidth, codec distribution, and top played items
- User management with admin panel and role-based access (admin/user)
- PIN-based user switching
- Webhook notifications for media events and library scans (HMAC-SHA256 signed, retry logic)
- HLS adaptive bitrate streaming with hardware encoder auto-detection (NVENC, VAAPI, software)
- Server settings management (TMDB/TVDB API keys) via UI and API
- TV show scanning with S##E## filename parsing and show/season/episode hierarchy
- Music library scanning with ID3/FLAC/Vorbis tag reading and artist/album/track hierarchy
- Manual metadata matching when auto-match gets it wrong
- Multi-user authentication with Paseto v4 tokens and refresh token rotation
- Docker deployment support (Dockerfile, docker-compose with server, worker, Postgres, Valkey)
- GitHub Actions CI pipeline (Go build/test, frontend check/test, Docker build)
- AGPLv3 license
