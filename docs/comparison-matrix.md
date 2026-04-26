# Feature Matrix: OnScreen vs Plex / Emby / Jellyfin (Server)

**Scope:** server-side features only. Client apps (Plex apps, Emby apps, Jellyfin apps, OnScreen web/Android TV/webOS) are **not** compared here — that's a separate axis and would dwarf the server comparison if combined.

**Legend**
- ✅ Supported out of the box
- 💎 Supported but behind a paid tier (Plex Pass / Emby Premiere)
- 🧩 Supported via an official plugin in the vendor's plugin catalog
- ⚠️ Partial — some aspect works but not parity with peers
- ❌ Not supported
- ❓ Unverified / depends on configuration

**Snapshot date:** 2026-04-26. Plex / Emby / Jellyfin rows reflect widely-documented upstream behavior as of that date; premium tiering (Plex Pass / Emby Premiere) and plugin availability change over time.

> **v2 in flight.** Cells marked ✅ in the last few weeks include items shipped during the v2 push: music videos, audiobooks (flat MVP), podcasts (local-files MVP), Cover Art Archive fallback, Kodi NFO import, lyrics end-to-end, DVR retention purge, subtitle burn-in, AV1 encode, HEVC on QSV/VAAPI/AMF. See [v2-roadmap.md](v2-roadmap.md) for what's still open.

---

## 1. Media Types

| Feature                    | OnScreen | Plex | Emby | Jellyfin | Notes |
|----------------------------|:--:|:--:|:--:|:--:|---|
| Movies                     | ✅ | ✅ | ✅ | ✅ | All four scan filename + metadata agent |
| TV shows (episodes)        | ✅ | ✅ | ✅ | ✅ | |
| Music (artists/albums/tracks) | ✅ | ✅ | ✅ | ✅ | |
| Photos                     | ✅ | ✅ | ✅ | ✅ | OnScreen: EXIF + map + timeline |
| Live TV                    | ✅ | 💎 | 💎 | ✅ | Plex/Emby gate behind paid tier |
| DVR (scheduled recording)  | ✅ | 💎 | 💎 | ✅ | OnScreen: matcher + capture + retention purge wired (commit `246027b`) |
| Audiobooks                 | ⚠️ | ✅ | ✅ | ✅ | OnScreen: flat one-file-per-book MVP (commit `933c1f0`); author/series hierarchy is v2.1 |
| Books / comics             | ❌ | ❌ | ⚠️ | ⚠️ | Jellyfin + Emby: basic comic/book scanning |
| Podcasts                   | ⚠️ | ⚠️ | ❌ | 🧩 | OnScreen: local files (commit `a8812ad`); RSS subscriptions are v2.1 |
| Music videos               | ✅ | ✅ | ✅ | ✅ | OnScreen: artist children w/ 16:9 thumbs (commit `3319bd6`) |
| Home videos (separate type)| ❌ | ✅ | ✅ | ✅ | OnScreen ingests as untyped movies |

---

## 2. Transcoding

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| H.264 encode (software)        | ✅ | ✅ | ✅ | ✅ | |
| H.264 encode (NVENC)           | ✅ | 💎 | 💎 | ✅ | Plex/Emby HW transcoding is paid |
| H.264 encode (QSV)             | ✅ | 💎 | 💎 | ✅ | |
| H.264 encode (VAAPI)           | ✅ | 💎 | 💎 | ✅ | |
| H.264 encode (AMF)             | ✅ | 💎 | 💎 | ✅ | |
| H.264 encode (VideoToolbox)    | ❌ | 💎 | 💎 | ✅ | macOS/Apple Silicon only |
| HEVC encode (NVENC)            | ✅ | 💎 | 💎 | ✅ | |
| HEVC encode (software)         | ✅ | 💎 | 💎 | ✅ | libx265 |
| HEVC encode (QSV/VAAPI/AMF)    | ⚠️ | 💎 | 💎 | ✅ | OnScreen: encoder paths added (commit `652b87e`); awaiting hardware validation on the beta |
| AV1 encode                     | ⚠️ | 💎 | 💎 | ⚠️ | OnScreen: SVT-AV1 SW + AV1 NVENC + AV1 QSV paths (commit `652b87e`); SVT-AV1 preset 8 for live |
| HDR → SDR tone mapping (GPU)   | ✅ | 💎 | 💎 | ✅ | OnScreen: tonemap_cuda → tonemap_opencl → zscale fallback ladder |
| 10-bit HEVC source handling    | ✅ | ✅ | ✅ | ✅ | |
| Subtitle burn-in                | ✅ | ✅ | ✅ | ✅ | OnScreen: software-encode only (commit `652b87e`); HW path skipped to preserve GPU throughput |
| Remux (stream-copy video)       | ✅ | ✅ | ✅ | ✅ | |
| Direct play decision engine     | ✅ | ✅ | ✅ | ✅ | |
| Multi-audio track selection     | ✅ | ✅ | ✅ | ✅ | |
| Audio downmix (5.1 → 2.0)       | ✅ | ✅ | ✅ | ✅ | |
| Per-user quality throttle       | ⚠️ | ✅ | ✅ | ✅ | OnScreen: global server cap only |
| Multi-worker transcode fleet    | ✅ | ❌ | ❌ | ❌ | OnScreen ships standalone worker binary that joins a fleet; competitors are single-process |

---

## 3. Metadata Agents

| Feature                         | OnScreen | Plex | Emby | Jellyfin | Notes |
|---------------------------------|:--:|:--:|:--:|:--:|---|
| TMDB (movies / TV)              | ✅ | ✅ | ✅ | ✅ | Plex uses its own "Plex agent" built on TMDB + TVDB |
| TheTVDB                         | ✅ | ✅ | ✅ | ✅ | |
| Fanart.tv                       | ✅ | ✅ | ✅ | ✅ | |
| MusicBrainz                     | ✅ | ✅ | ✅ | ✅ | OnScreen: IDs from tags only, no live API |
| TheAudioDB                      | ✅ | ⚠️ | ✅ | ✅ | Plex uses its own music agent (Gracenote-derived) |
| Cover Art Archive               | ✅ | ❌ | ✅ | ✅ | OnScreen: chains after TheAudioDB via MusicBrainz IDs (commit `43017e2`) |
| OpenSubtitles (metadata hashing)| ❌ | ✅ | ✅ | ✅ | |
| Local NFO file import           | ✅ | ✅ | ✅ | ✅ | OnScreen: movie/tvshow/episodedetails — overrides TMDB on the final write (commit `21738b3`) |
| Disk cover art (cover.jpg etc.) | ✅ | ✅ | ✅ | ✅ | OnScreen shipped 2026-04-24 (commit bc0e9c7) |
| Embedded tag art (ID3/FLAC/MP4) | ✅ | ✅ | ✅ | ✅ | |

---

## 4. Music (Audiophile Detail)

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| FLAC / ALAC / WAV scanning     | ✅ | ✅ | ✅ | ✅ | |
| Bit depth / sample rate exposure| ✅ | ✅ | ✅ | ✅ | |
| Lossless flag                  | ✅ | ⚠️ | ⚠️ | ✅ | |
| Hi-res badge (>44.1kHz/16-bit) | ✅ | ⚠️ | ❌ | ⚠️ | OnScreen: explicit UI badge |
| ReplayGain track + album       | ✅ | ⚠️ | ✅ | ✅ | Plex uses its own loudness normalization |
| MusicBrainz ID exposure        | ✅ | ❌ | ⚠️ | ✅ | OnScreen: all 5 MB ID types surfaced |
| Bit-perfect playback           | ❌ | ❌ | ❌ | ❌ | None do this today; all pipe through transcode path |
| Gapless playback               | ✅ | ✅ | ✅ | ✅ | OnScreen: dual-`<audio>` preload rotation (commit `55612c8`); Chrome/Firefox sub-frame, Safari per-its-usual |
| DSD (DoP) support              | ❌ | ❌ | ❌ | ❌ | |
| Release type (Album/EP/Single) | ✅ | ⚠️ | ✅ | ✅ | |
| Original release year          | ✅ | ✅ | ✅ | ✅ | |
| Compilation flag               | ✅ | ✅ | ✅ | ✅ | |
| Collab / featured artists      | ⚠️ | ✅ | ✅ | ✅ | OnScreen: two-sided match but no dedicated collab entity |
| Lyrics (synced/unsynced)       | ✅ | ✅ | ✅ | ✅ | OnScreen: embedded USLT + .lrc sidecar + LRCLIB fallback (commits `333a55e`, `67524d0`) |
| Tidal / Qobuz integration      | ❌ | ✅ | ❌ | ❌ | Plex-exclusive via Plex Pass |
| SoundCloud / YouTube Music     | ❌ | ❌ | ❌ | ❌ | |

---

## 5. Content Discovery

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Full-text search               | ✅ | ✅ | ✅ | ✅ | OnScreen: Postgres tsvector FTS |
| Recently added                 | ✅ | ✅ | ✅ | ✅ | |
| Continue watching / On Deck    | ✅ | ✅ | ✅ | ✅ | |
| Genre browse                   | ✅ | ✅ | ✅ | ✅ | |
| Collections                    | ✅ | ✅ | ✅ | ✅ | OnScreen: auto-genre + playlist types |
| Smart playlists (rule-based)   | ❌ | ✅ | ✅ | ✅ | |
| Recommendations                | ⚠️ | ✅ | ✅ | ✅ | OnScreen: pgvector similarity in-progress (Phase 5) |
| Trending                       | ❌ | ✅ | ✅ | ✅ | |
| "Because you watched X"        | ❌ | ✅ | ✅ | ⚠️ | |
| TMDB discover in search        | ✅ | ❌ | ❌ | ❌ | OnScreen: Overseerr-style request inline |
| Requests (self-service)        | ✅ | ❌ | ❌ | ❌ | Competitors need Overseerr/Ombi/Jellyseerr |
| Plex Discover (external titles)| ❌ | ✅ | ❌ | ❌ | Plex-exclusive |

---

## 6. User Management & Authentication

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Local auth                     | ✅ | ⚠️ | ✅ | ✅ | Plex forces Plex.tv SSO for most features |
| LDAP                           | ✅ | ❌ | 💎 | 🧩 | Jellyfin LDAP is a plugin |
| OAuth (Google/GitHub/Discord)  | ✅ | ❌ | ❌ | 🧩 | |
| OIDC (generic)                 | ✅ | ❌ | 💎 | 🧩 | |
| SAML                           | ✅ | ❌ | 💎 | 🧩 | OnScreen: SP-initiated flow + JIT provisioning + admin-group sync (commit `af96edb`) |
| Plex.tv SSO (accept Plex tokens)| ❌ | ✅ | ❌ | ❌ | |
| Multi-user                     | ✅ | ✅ | ✅ | ✅ | |
| Managed user profiles (PIN)    | ✅ | 💎 | ✅ | ❌ | OnScreen: up to 6 profiles per user with PIN |
| Parental controls / rating cap | ✅ | 💎 | ✅ | ✅ | OnScreen: rank function filters at query layer |
| Per-user library visibility    | ⚠️ | ✅ | ✅ | ✅ | OnScreen has LibraryAccessChecker hook but policy still global |
| Password + PIN (separate)      | ✅ | ❌ | ⚠️ | ❌ | |
| Refresh tokens w/ rotation     | ✅ | ✅ | ✅ | ✅ | |
| Session revocation / kill switch| ✅ | ✅ | ✅ | ✅ | |
| Audit log                      | ✅ | ❌ | 💎 | ❌ | |
| Session supersede (new device kills old) | ✅ | ✅ | ✅ | ✅ | |

---

## 7. Playback Features

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Resume position                | ✅ | ✅ | ✅ | ✅ | |
| Embedded subtitle extraction   | ✅ | ✅ | ✅ | ✅ | |
| External subtitle download (OpenSubtitles) | ✅ | 💎 | ✅ | ✅ | |
| Image subtitle OCR (PGS → VTT) | ✅ | 💎 | ✅ | ✅ | OnScreen: tesseract; 12 language packs |
| Subtitle language preference   | ✅ | ✅ | ✅ | ✅ | |
| Audio language preference      | ✅ | ✅ | ✅ | ✅ | |
| Forced-only subtitles flag     | ✅ | ✅ | ✅ | ✅ | |
| Chapter markers                | ✅ | ✅ | ✅ | ✅ | |
| Intro/credits detection        | ✅ | 💎 | ✅ | 🧩 | Jellyfin: Intro Skipper plugin |
| Intro/credits skip UI          | ✅ | 💎 | ✅ | 🧩 | |
| Trickplay (seekbar thumbnails) | ✅ | 💎 | 💎 | ✅ | |
| Keyframe-snap Resume           | ✅ | ⚠️ | ⚠️ | ⚠️ | OnScreen: explicit snap-back + scrubber truth (commit 5bad942) |
| Watched threshold              | ✅ | ✅ | ✅ | ✅ | |
| Cross-device sync              | ✅ | ✅ | ✅ | ✅ | |
| Picture-in-picture (server signal)| ❌ | ✅ | ✅ | ✅ | |

---

## 8. Live TV / DVR

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| HDHomeRun tuner                | ✅ | 💎 | 💎 | ✅ | OnScreen: auto-discovery via UDP broadcast |
| M3U / IPTV                     | ✅ | 💎 | 💎 | ✅ | |
| USB DVB tuners (DVB-T/S/C)     | ❌ | 💎 | 💎 | ✅ | |
| XMLTV guide                    | ✅ | 💎 | 💎 | ✅ | |
| Schedules Direct guide         | ✅ | 💎 | 💎 | ✅ | OnScreen: full client + auto-match by callsign (commit `16908c8`) |
| Live HLS stream-copy           | ✅ | 💎 | 💎 | ✅ | |
| Channel guide grid UI (server-driven) | ✅ | 💎 | 💎 | ✅ | |
| Scheduled recording            | ✅ | 💎 | 💎 | ✅ | OnScreen: matcher fires on cron, capture worker spawns ffmpeg, retention purge daily |
| Series recording rules         | ✅ | 💎 | 💎 | ✅ | OnScreen: `series` schedule type with title_match + new_only |
| Commercial detection/skip      | ❌ | 💎 | 💎 | 🧩 | |
| Recording conflicts UI         | ⚠️ | 💎 | 💎 | ✅ | OnScreen: backend conflict detection logs + flags; UI surface is minimal |

---

## 9. Networking & Streaming

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| HLS streaming                  | ✅ | ✅ | ✅ | ✅ | |
| DASH streaming                 | ❌ | ✅ | ✅ | ✅ | |
| Raw file serving + byte-range  | ✅ | ✅ | ✅ | ✅ | |
| Signed segment URLs            | ✅ | ✅ | ✅ | ✅ | OnScreen: JWT query-param tokens |
| Range requests (HTTP 206)      | ✅ | ✅ | ✅ | ✅ | |
| Direct play without auth header| ✅ | ✅ | ✅ | ✅ | Artwork + capability tokens |
| CDN / remote-access relay      | ❌ | ✅ | 💎 | ❌ | Plex Relay is free bandwidth through Plex |
| IPv6                           | ✅ | ✅ | ✅ | ✅ | |
| HTTPS termination (built-in)   | ❌ | ✅ | ✅ | ✅ | OnScreen expects reverse-proxy (Caddy/nginx) in front |

---

## 10. Admin & Observability

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Web-based settings UI          | ✅ | ✅ | ✅ | ✅ | |
| On-demand library scan         | ✅ | ✅ | ✅ | ✅ | |
| Filesystem-watcher incremental scan | ✅ | ✅ | ✅ | ✅ | |
| Scheduled scan (cron-like)     | ⚠️ | ✅ | ✅ | ✅ | OnScreen: per-library interval only, no cron syntax |
| Structured JSON logs           | ✅ | ❌ | ❌ | ❌ | OnScreen: log/slog JSON on stdout |
| OpenTelemetry tracing          | ✅ | ❌ | ❌ | ❌ | OTLP/gRPC, otelchi + otelpgx |
| Prometheus metrics             | ✅ | ❌ | ❌ | ❌ | `/metrics` endpoint |
| Analytics dashboard            | ✅ | ✅ | ✅ | ✅ | |
| Audit log for admin actions    | ✅ | ❌ | 💎 | ❌ | |
| HMAC-signed webhooks           | ✅ | ✅ | ✅ | ✅ | OnScreen: AES-256-GCM secret at rest |
| Webhook retry + DLQ            | ✅ | ⚠️ | ⚠️ | ⚠️ | |
| DB migrations embedded         | ✅ | n/a | n/a | n/a | Competitors use SQLite auto-upgrade |
| Live session monitoring        | ✅ | ✅ | ✅ | ✅ | |
| Hot-config reload (SIGHUP)     | ✅ | ❌ | ❌ | ❌ | |

---

## 11. Security

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Rate limiting                  | ✅ | ✅ | ✅ | ✅ | |
| SSRF hardening on URL fetches  | ✅ | ❓ | ❓ | ✅ | |
| CSP / security headers         | ✅ | ✅ | ✅ | ✅ | |
| Bcrypt password hashing        | ✅ | n/a | ✅ | ✅ | Plex delegates to Plex.tv |
| Path traversal protection      | ✅ | ✅ | ✅ | ✅ | |
| SQL-injection-safe (parameterized) | ✅ | ✅ | ✅ | ✅ | |
| PASETO tokens                  | ✅ | ❌ | ❌ | ❌ | Competitors use JWT / opaque tokens |
| Secret-at-rest encryption (webhook/plugin creds) | ✅ | ❓ | ❓ | ⚠️ | |
| Session revocation             | ✅ | ✅ | ✅ | ✅ | |

---

## 12. Storage & Infrastructure

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Database                       | PostgreSQL | SQLite | SQLite | SQLite | OnScreen is the only PG-native of the four |
| External DB possible           | required | ❌ | ⚠️ | ⚠️ | Emby/Jellyfin experiment with Postgres/MariaDB but SQLite is primary |
| Partitioned event tables       | ✅ | ❌ | ❌ | ❌ | `watch_events` monthly partitions |
| Materialized hub views         | ✅ | ❌ | ❌ | ❌ | |
| Redis / Valkey queue           | ✅ | ❌ | ❌ | ❌ | OnScreen: transcode dispatch + rate limiter |
| Single binary deployment       | ✅ | ✅ | ✅ | ✅ | OnScreen: Go binary + SvelteKit embedded |
| Docker images (official)       | ✅ | ✅ | ✅ | ✅ | OnScreen: CPU + GPU variants |
| NAS support (Synology/QNAP/Unraid) | ✅ | ✅ | ✅ | ✅ | OnScreen: runs as Docker or directly |
| TrueNAS deployment doc         | ✅ | ✅ | ✅ | ✅ | OnScreen: dedicated deploy guide |
| Cloud storage (S3/GCS direct)  | ❌ | ❌ | ❌ | ❌ | All rely on local/NFS mounts |
| Config: env vars (12-factor)   | ✅ | ⚠️ | ⚠️ | ⚠️ | Competitors use XML/JSON config files |
| Built-in backup/restore        | ❌ | ❌ | 💎 | ⚠️ | Jellyfin has user-data export |
| Horizontal scaling (workers)   | ✅ | ❌ | ❌ | ❌ | |

---

## 13. Plugins & Extensibility

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Plugin system                  | ✅ | ❌ | ✅ | ✅ | Plex deprecated channels in 2019 |
| Plugin language / transport    | MCP (outbound) | n/a | C# (in-proc) | C# (in-proc) | OnScreen plugins are external MCP servers — no in-proc dll/dll hosting |
| Third-party metadata agents    | ⚠️ | ❌ | ✅ | ✅ | OnScreen: planned via MCP |
| REST API (stable)              | ✅ | ✅ | ✅ | ✅ | |
| GraphQL API                    | ❌ | ❌ | ❌ | ❌ | |
| Webhook events                 | ✅ | ✅ | ✅ | ✅ | |
| Import from Plex/Emby/Jellyfin | ❌ | ❌ | ❌ | ⚠️ | Jellyfin has partial Emby migration |
| Plex API compatibility shim    | ⚠️ | — | ❌ | ❌ | OnScreen: direct play endpoint compat only |

---

## 14. Open-Source / Licensing

| Feature                        | OnScreen | Plex | Emby | Jellyfin |
|--------------------------------|:--:|:--:|:--:|:--:|
| Open source                    | ✅ | ❌ | ❌ | ✅ |
| Paid tier for core features    | ❌ | ✅ (Plex Pass) | ✅ (Premiere) | ❌ |
| Self-hostable, no phone-home   | ✅ | ⚠️ | ✅ | ✅ |
| Works offline / LAN-only       | ✅ | ⚠️ | ✅ | ✅ |

---

## Where OnScreen Leads

- **PostgreSQL-native**: partitioned event tables, tsvector FTS, materialized hub cache, no SQLite race conditions under heavy write load.
- **Multi-worker transcode fleet**: a standalone worker binary joins the dispatcher and picks up jobs; the others are single-process.
- **Live TV / DVR / HW transcoding included for free**: no Plex Pass / Emby Premiere gate.
- **Modern auth out of the box**: OIDC, OAuth (Google/GitHub/Discord), LDAP without plugins; PASETO over JWT.
- **First-class observability**: OTel tracing, Prometheus metrics, structured JSON logs, audit log — without a premium tier.
- **Requests built in**: the search page surfaces TMDB discover + request inline; competitors require Overseerr/Ombi/Jellyseerr.
- **Env-var config (12-factor) + hot reload via SIGHUP**: fits container orchestrators; competitors ship XML/JSON config files.
- **Secret encryption at rest** for webhooks and plugin credentials (AES-256-GCM).
- **NFO + Cover Art Archive fallback chain**: NFO overrides TMDB on the final write; CAA fills MusicBrainz-keyed album art that TheAudioDB doesn't have. Plex doesn't do CAA at all.

## Where OnScreen Trails (as of 2026-04-26)

- **No books / comics** as distinct media types.
- **No podcast RSS subscriptions** — local files work, feed-driven auto-download is v2.1.
- **No Tidal / Qobuz integration** for music streaming.
- **No HEVC / AV1 hardware encode validated on real hardware** yet — code paths shipped, beta validation pending.
- **No in-built HTTPS** — expects a reverse proxy in front.
- **No direct cloud-storage integration** (S3/GCS); all four rely on local or NFS mounts.

## v2 Closed (since the prior snapshot)

- ✅ Music videos as a distinct type (artist children, 16:9 thumbnails)
- ✅ Audiobooks as a library type (flat MVP)
- ✅ Podcasts as a library type (local-files MVP; RSS subscriptions deferred to v2.1)
- ✅ Lyrics end-to-end (USLT + .lrc + LRCLIB)
- ✅ Kodi NFO sidecar import (movie / tvshow / episodedetails)
- ✅ Cover Art Archive fallback for album art
- ✅ DVR retention purge (closes the matcher → capture → cleanup loop)
- ✅ Subtitle burn-in (software-encode path)
- ✅ AV1 encode (SVT-AV1 SW + AV1 NVENC + AV1 QSV constants — beta hardware validation pending)
- ✅ HEVC encode on QSV / VAAPI / AMF (beta hardware validation pending)
- ✅ Schedules Direct as a second EPG source (token auth, batched fetch, callsign auto-match)
- ✅ Gapless music playback (dual `<audio>` preload rotation)
- ✅ SAML 2.0 SP-initiated SSO (JIT provisioning, admin-group sync, SP keypair auto-generate)

## Non-Differentiators (All Four Roughly Equal)

Movies / TV / music / photo scanning, embedded + disk art, TMDB+TVDB+MusicBrainz metadata, HLS streaming, direct play, resume position, multi-user, parental content ratings, chapter markers, audit-safe session management.
