# Feature Matrix: OnScreen vs Plex / Emby / Jellyfin

**Scope:** sections 1–14 cover server-side features. Section 15 covers first-party clients — desktop (Tauri shell on OnScreen, Plexamp/Plex HTPC, Emby Theater, Jellyfin Media Player), plus the OnScreen TV apps (Android TV / Fire TV / LG webOS / Roku / Samsung Tizen) and the Android phone app against the corresponding incumbents. iOS phone + Apple TV stay ❌ — no scaffold yet.

**Legend**
- ✅ Supported out of the box
- 💎 Supported but behind a paid tier (Plex Pass / Emby Premiere)
- 🧩 Supported via an official plugin in the vendor's plugin catalog
- ⚠️ Partial — some aspect works but not parity with peers
- ❌ Not supported
- ❓ Unverified / depends on configuration

**Snapshot date:** 2026-04-29. Plex / Emby / Jellyfin rows reflect widely-documented upstream behavior as of that date; premium tiering (Plex Pass / Emby Premiere) and plugin availability change over time.

> **v2.0 shipped, v2.1 in flight.** Cells flipped during v2.0 (music videos, audiobooks, podcasts, CAA fallback, NFO import, lyrics end-to-end, DVR purge, subtitle burn-in, AV1, HEVC on QSV/VAAPI/AMF, SAML, built-in HTTPS) are captured in the **v2 Closed** section below. v2.1 work in progress on `main`: home-video library, CBZ books + reader, smart playlists, trending row, library is_private + auto-grant + per-profile visibility (Track G complete), DASH manifest endpoint (server side), admin logs API, audiobook embedded-cover serving, per-file streaming token (24 h, file_id-bound, purpose-scoped), three TV clients at usable parity (Android TV / Fire TV verified, LG webOS feature-complete, Roku at flow parity). See [v2.1-roadmap.md](v2.1-roadmap.md) for the full track list.

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
| Audiobooks                 | ⚠️ | ✅ | ✅ | ✅ | OnScreen: flat one-file-per-book MVP (commit `933c1f0`); embedded cover art served on demand via `/items/{id}/image` (ffmpeg-extracted from m4b/mp3/flac, cached); author/series hierarchy is v2.1 |
| Books / comics             | ⚠️ | ❌ | ⚠️ | ⚠️ | OnScreen: CBZ scan + paginated reader shipped in v2.1 (Track B Stage 1); EPUB and CBR explicitly deferred to Stage 2 |
| Podcasts                   | ⚠️ | ⚠️ | ❌ | 🧩 | OnScreen: local files + episode UI (commit `a8812ad`, v2.1 polish); RSS subscriptions still deferred |
| Music videos               | ✅ | ✅ | ✅ | ✅ | OnScreen: artist children w/ 16:9 thumbs (commit `3319bd6`) |
| Home videos (separate type)| ✅ | ✅ | ✅ | ✅ | OnScreen: dedicated `home_video` library + date-grouped library page (v2.1 Track B); reuses `originally_available_at` as "date taken" |

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
| Multi-audio track selection     | ✅ | ✅ | ✅ | ✅ | OnScreen Android TV: subtitle + audio pickers re-issue the HLS session with a new `audio_stream_index` when in transcode/remux mode (server emits one audio stream per session, so client-side track switch isn't possible); direct-play falls through to ExoPlayer's language selector |
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
| Bit-perfect playback           | ❌ | ✅ | ✅ | ⚠️ | Browsers force everything through the OS mixer (resampled to system rate); requires a native client with WASAPI-exclusive / CoreAudio hog / ALSA `hw:`. Plexamp and Emby Theater ship this; Jellyfin gets it via 3rd-party clients (Finamp, JMP). OnScreen is web-only today — lands with the native client phase. |
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
| Smart playlists (rule-based)   | ✅ | ✅ | ✅ | ✅ | OnScreen: JSON rules persisted on `collections.rules`, evaluated at query time so newly-imported items appear without rebuild (v2.1 Track F item 1) |
| Recommendations                | ❌ | ✅ | ✅ | ✅ | OnScreen: removed — the cooccurrence-based "Because you watched" row didn't earn its space on the home hub; trending stays |
| Trending                       | ✅ | ✅ | ✅ | ✅ | OnScreen: rolling-window aggregate over `watch_events` (v2.1 Track F item 3) |
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
| Per-user library visibility    | ✅ | ✅ | ✅ | ✅ | OnScreen: per-library `is_private` flag, `auto_grant_new_users` template, per-profile inherit-or-override toggle, content-rating gates closed in playlists/genre/history, admin "view as" tool (v2.1 Track G items 1–5) |
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
| DASH streaming                 | ⚠️ | ✅ | ✅ | ✅ | OnScreen: `manifest.mpd` endpoint over the existing fMP4 ladder for HEVC sessions, plus `manifest_url` surfaced on the session-start response so native clients consume it without URL construction (v2.1 Track H, server side complete); browser shaka-player swap + smart-TV test matrix deferred — real DASH leverage is the smart-TV native-client side (Track E) which goes through the MPD URL directly |
| Raw file serving + byte-range  | ✅ | ✅ | ✅ | ✅ | |
| Signed segment URLs            | ✅ | ✅ | ✅ | ✅ | OnScreen: JWT query-param tokens |
| Range requests (HTTP 206)      | ✅ | ✅ | ✅ | ✅ | |
| Direct play without auth header| ✅ | ✅ | ✅ | ✅ | Artwork + capability tokens |
| CDN / remote-access relay      | ❌ | ✅ | 💎 | ❌ | Plex Relay is free bandwidth through Plex |
| IPv6                           | ✅ | ✅ | ✅ | ✅ | |
| HTTPS termination (built-in)   | ✅ | ✅ | ✅ | ✅ | OnScreen: operator-provided PEM via `TLS_CERT_FILE`/`TLS_KEY_FILE`; reverse proxy still recommended for ACME auto-renew |

---

## 10. Admin & Observability

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| Web-based settings UI          | ✅ | ✅ | ✅ | ✅ | |
| On-demand library scan         | ✅ | ✅ | ✅ | ✅ | |
| Filesystem-watcher incremental scan | ✅ | ✅ | ✅ | ✅ | |
| Scheduled scan (cron-like)     | ⚠️ | ✅ | ✅ | ✅ | OnScreen: per-library interval only, no cron syntax |
| Structured JSON logs           | ✅ | ❌ | ❌ | ❌ | OnScreen: log/slog JSON on stdout |
| Admin log retrieval API        | ✅ | ❌ | ❌ | ❌ | OnScreen: `GET /api/v1/admin/logs?level=…&limit=…` reads from a 2000-entry in-process ring; lets operators pull recent server output without host shell access (TrueNAS Apps, Cloud Run) |
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
| Horizontal scaling (workers)   | ✅ | ❌ | ❌ | ❌ | OnScreen: transcode workers join via Valkey; SAML request tracker is also Valkey-backed (v2.1) so SP-initiated SSO survives load-balanced AuthnRequest → ACS roundtrips across instances |

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

## 15. Native Desktop Clients

Compares OnScreen's Tauri 2 shell against the first-party desktop clients in each ecosystem: Plex's pair (Plexamp for music + Plex HTPC for video), Emby Theater, and the community-maintained Jellyfin Media Player (Jellyfin has no first-party desktop client, so JMP is the de-facto reference).

### 15a. Platform & shell

| Feature                         | OnScreen | Plex (Plexamp/HTPC) | Emby Theater | Jellyfin (JMP) | Notes |
|---------------------------------|:--:|:--:|:--:|:--:|---|
| Windows                         | ✅ | ✅ | ✅ | ✅ | OnScreen: Tauri 2 + WebView2 |
| macOS                           | ✅ | ✅ | ✅ | ✅ | OnScreen: Tauri 2 + WKWebView |
| Linux                           | ✅ | ⚠️ | ⚠️ | ✅ | OnScreen: Tauri 2 + WebKitGTK; Plex/Emby Linux desktops are second-class |
| Single shared codebase with web | ✅ | ⚠️ | ⚠️ | ✅ | OnScreen: same SvelteKit bundle in browser + Tauri webview; Plexamp is its own React-Native code |
| Install size                    | ~10 MB | ~80 MB (Plexamp) | ~150 MB | ~120 MB | OnScreen uses the system webview (no bundled Chromium) |
| Bundled Chromium                | ❌ | ⚠️ | ✅ | ✅ | Tauri trades install size for system-webview variance |

### 15b. Audiophile audio path

| Feature                         | OnScreen | Plex (Plexamp/HTPC) | Emby Theater | Jellyfin (JMP) | Notes |
|---------------------------------|:--:|:--:|:--:|:--:|---|
| Native audio engine (out-of-webview) | ✅ | ✅ | ✅ | ⚠️ | OnScreen: cpal + claxon over a lock-free ringbuf; JMP defers to mpv |
| Bit-perfect / WASAPI exclusive (Windows) | ❌ | ✅ | ✅ | ✅ | OnScreen: cpal 0.16 hard-codes shared mode; needs a cpal fork or raw `wasapi` swap |
| CoreAudio HOG mode (macOS)      | ❌ | ✅ | ⚠️ | ✅ | Same cpal limitation |
| ALSA `hw:` device selection (Linux) | ❌ | ✅ | ⚠️ | ✅ | Same cpal limitation |
| Gapless playback                | ✅ | ✅ | ✅ | ✅ | OnScreen native: preload slot promotes ringbuf into the new active stream — sub-frame; web client uses dual-`<audio>` rotation |
| Native FLAC decode              | ✅ | ✅ | ✅ | ✅ | OnScreen: claxon (pure-Rust) |
| Native ALAC / WAV / AIFF decode | ⚠️ | ✅ | ✅ | ✅ | OnScreen native engine is FLAC-only today; other formats fall through to the webview's `<audio>` |
| DSD (DoP) playback              | ❌ | ⚠️ | ❌ | ⚠️ | Plexamp does DoP for compatible DACs; JMP via mpv |
| ReplayGain enforced client-side | ⚠️ | ✅ | ✅ | ✅ | OnScreen: tags surfaced server-side, native engine doesn't apply gain yet |
| Per-device output picker        | ✅ | ✅ | ✅ | ✅ | OnScreen: cpal device enum + diagnostic test-tone page |
| Hi-res / sample-rate switching  | ⚠️ | ✅ | ✅ | ✅ | OnScreen requests the FLAC's native rate from cpal but without exclusive mode the OS mixer may still resample |

### 15c. Cross-device + power-user

| Feature                         | OnScreen | Plex (Plexamp/HTPC) | Emby Theater | Jellyfin (JMP) | Notes |
|---------------------------------|:--:|:--:|:--:|:--:|---|
| OS media keys (Play/Pause/Next/Prev) | ✅ | ✅ | ✅ | ✅ | OnScreen: `tauri-plugin-global-shortcut`, system-wide |
| System tray (background play)   | ✅ | ✅ | ✅ | ⚠️ | OnScreen: tray menu for Show/Transport/Quit |
| Native OS notifications         | ✅ | ✅ | ✅ | ⚠️ | OnScreen: now-playing on track change, gated on window blur |
| OS now-playing widget (SMTC/MPRIS/MediaPlayer) | ❌ | ✅ | ✅ | ⚠️ | Lockscreen/taskbar art + transport; OnScreen punted (`souvlaki` swap) |
| Secure credential storage       | ✅ | ✅ | ✅ | ⚠️ | OnScreen: Windows Credential Manager / macOS Keychain / Linux Secret Service via `keyring 3.x` |
| Cross-device resume sync (push) | ✅ | ✅ | ✅ | ⚠️ | OnScreen: SSE `progress.updated` broadcast + watch-page consumer; Jellyfin polls |
| "Play on this device" remote control | ❌ | ✅ | ✅ | ⚠️ | Pick another logged-in device from a "now playing" list and stream there |
| Picture-in-picture mode         | ❌ | ✅ | ✅ | ⚠️ | |
| Configurable server URL (no Plex.tv lock-in) | ✅ | ⚠️ | ✅ | ✅ | OnScreen: first-run picker + `/native/server` reset |

### 15d. TV & mobile coverage

| Feature                         | OnScreen | Plex (apps) | Emby (apps) | Jellyfin (apps) | Notes |
|---------------------------------|:--:|:--:|:--:|:--:|---|
| Android TV / Google TV          | ✅ | ✅ | ✅ | ✅ | OnScreen: [`clients/android/`](../clients/android/) — AndroidX Leanback + Media3 ExoPlayer + Hilt + Retrofit; full hub + library browse, photos with D-pad nav, music with auto-advance, audiobook speed picker, collections, skip-intro/credits, chapters, trickplay, device-pairing sign-in (covers OIDC/OAuth/SAML/LDAP/local), cross-device resume, screen-on during playback. Verified on real hardware (Fire Stick, Google TV). Outstanding: full TV-app polish + offline downloads |
| LG webOS (smart TV)             | ✅ | ✅ | ✅ | ⚠️ | OnScreen: [`clients/webos/`](../clients/webos/) — SvelteKit SPA packaged via `ares-package` with its own spatial-navigation focus manager. Setup → login → hub → library → item → search → watch all wired; pairing-flow sign-in (covers OIDC/OAuth/SAML/LDAP), search type-filter chips, photo viewer with D-pad sibling nav, audiobook chapter list, collections drill-in, favorites + history, skip-intro/credits + Up Next overlays, music auto-advance, cross-device resume via SSE. Per-file stream token consumed for long sessions. Outstanding: hardware validation on a real LG TV |
| Roku                            | ✅ | ✅ | ✅ | ⚠️ | OnScreen: [`clients/roku/`](../clients/roku/) — BrightScript + SceneGraph. Setup → login → hub → DetailScene → search → player wired; pairing-flow sign-in, search with type-filter chips, type-aware DetailScene, photo viewer with D-pad sibling nav, favorites + history + collections, three-mode playback (direct/remux/transcode via `Playback_Decide`, 13 brs unit tests), markers + Up Next + music auto-advance + cross-device sync via 5 s polling, per-file stream token consumed. Jellyfin: third-party. |
| Amazon Fire TV                  | ✅ | ✅ | ✅ | ⚠️ | OnScreen: shares the [`clients/android/`](../clients/android/) APK; [`clients/firetv/`](../clients/firetv/) is build/sideload + Amazon Appstore docs. Fire OS = Android fork, accepts the same binary; the manifest declares `amazon.hardware.fire_tv` so Amazon's launcher categorises it correctly while remaining a no-op on Google TV devices. **Verified on hardware (Fire Stick)** — streaming, artwork (Coil-via-authed-OkHttp + `/items/{id}/image` fallback for audiobook covers), photo D-pad navigation, music auto-advance, screen-on during playback all confirmed. Jellyfin: third-party Fire TV builds. |
| Samsung Tizen (smart TV)        | ✅ | ✅ | ✅ | ⚠️ | OnScreen: [`clients/tizen/`](../clients/tizen/) — SvelteKit SPA packaged via `tizen package -t wgt`; AVPlay JS API dual-path (HW HEVC/AV1) with HTML5 `<video>` fallback. Bulk-ported from webOS: pairing-flow sign-in, hub, library, search with type-filter chips, photos with D-pad sibling nav, audiobook chapter list, collections, favorites + history, skip-intro/credits + Up Next, music auto-advance, cross-device resume via SSE, per-file stream token. Hardware validation on a real Samsung TV outstanding. |
| Apple TV (tvOS)                 | ❌ | ✅ | ✅ | ⚠️ | Jellyfin: third-party Infuse/SwiftFin |
| Native iOS phone app            | ❌ | ✅ | ✅ | ✅ | OnScreen runs in mobile browser only |
| Native Android phone app        | ⚠️ | ✅ | ✅ | ✅ | OnScreen: [`clients/android_native/`](../clients/android_native/) — Kotlin + Jetpack Compose + Material 3 + Hilt + Retrofit, package `tv.onscreen.mobile`. Reuses the TV client's data layer verbatim (Retrofit/Moshi API, AuthInterceptor + TokenAuthenticator, ServerPrefs DataStore, all repos). UI: pairing-PIN sign-in (password fallback), hub with poster strips + library list, library grid, item detail, search with debounce, favorites + history + collections drill-in, downloads screen. Player: Media3 ExoPlayer with direct/remux/transcode negotiation (port of TV `PlaybackHelper`), per-file 24h stream token, audio + subtitle pickers (HLS audio re-issues the session with a new audio_stream_index), 10s progress reporting, skip-intro/credits overlay, Up Next + music auto-advance, **offline-first short-circuit to a local copy when a completed download exists**. WorkManager + Hilt-injected worker + JSON manifest store on disk. 29 unit tests cover the playback decision matrix, pairing flow, transcode session lifecycle, and progress reporting. Outstanding: real-hardware validation, photo viewer, audiobook chapter list, cross-device SSE resume, search type-filter chips, favorite-toggle on detail page, background audio (Media3 MediaSessionService — TV client shipped, phone port pending). |
| Download for offline playback (mobile) | ✅ | 💎 | 💎 | ⚠️ | OnScreen: WorkManager-driven downloads on the Android phone client; player short-circuits to the local file. Free, not paywalled. Jellyfin: Finamp does music-only |
| CarPlay / Android Auto          | ❌ | ✅ | ❌ | ❌ | Plexamp only |

### 15e. TV-app architecture (OnScreen scaffolds)

| Decision                        | Android TV scaffold | Android phone scaffold | webOS scaffold | Tizen scaffold | Roku scaffold | Rationale |
|---------------------------------|--------------------|------------------------|---------------|----------------|---------------|-----------|
| Language / framework            | Kotlin + AndroidX Leanback | Kotlin + Jetpack Compose + Material 3 | SvelteKit SPA | SvelteKit SPA | BrightScript + SceneGraph | Phone client deliberately picks Compose over the TV's Leanback so touch + gesture + insets aren't fighting a remote-first framework; data layer is shared verbatim across the two Kotlin modules |
| Video player                    | Media3 ExoPlayer (HLS + DASH) | Media3 ExoPlayer (HLS + DASH) | HTML5 `<video>` + hls.js | Tizen AVPlay JS API (HW HLS/DASH/MP4 + HEVC/AV1) | Firmware Video node (HLS + DASH + MP4) | The native option on each platform; AVPlay is the audiophile-pillar equivalent for video on Samsung — firmware decoders for HEVC/AV1, native 4K + HDR pipeline |
| Networking                      | Retrofit + Moshi + OkHttp + okhttp-sse | Retrofit + Moshi + OkHttp + okhttp-sse | reuses `web/src/lib/api.ts` shape | reuses `web/src/lib/api.ts` shape | `roUrlTransfer` + ParseJson | Roku has no SSE primitive — sync via long-poll fallback when wired |
| DI                              | Hilt | Hilt | n/a (Svelte stores) | n/a (Svelte stores) | n/a (file-scoped functions) | BrightScript has no DI ecosystem; Singletons-by-convention is the norm |
| Image loading                   | Coil | Coil (Compose) | browser-native | browser-native | Poster node (firmware) | Async + cached + diskbacked on Roku for free |
| Persistent prefs (server URL etc.)| AndroidX DataStore | AndroidX DataStore | `localStorage` | `localStorage` | `roRegistrySection` | Roku registry isn't encrypted (no Keystore equivalent) — same threat model the Android client documented before its keychain migration |
| Remote-key / touch navigation   | Leanback handles natively | Compose touch + gesture (no D-pad) | custom spatial-nav in `lib/focus/` | custom spatial-nav in `lib/focus/` (Tizen `VK_*` codes) | RowList / Group focus handles natively | Tizen + webOS share the spatial-nav shape; only the keycode integers differ between LG and Samsung remotes |
| Packaging                       | Gradle → APK | Gradle → APK | `ares-package` → IPK | `tizen package` → WGT | npm + archiver → ZIP | Each store dictates the format |
| Min OS                          | Android 5 (API 21) | Android 7 (API 24) | webOS 6 (LG C1 / 2021+) | Tizen 5.5 (Samsung 2019+) | RokuOS 11+ | Phone bumps minSdk past the TV client's 21 so Compose + Material 3 + predictive-back work without compat shims |

---

> **Per-app feature sections (16–22).** Sections 1–14 cover server-side capabilities; sections 16–22 cover what each first-party client app actually exposes today, on the same axes (auth → browse → search → media types → playback → cross-device → offline). Each section compares OnScreen's app to the corresponding vendor app on the *same* platform. Cell legend follows the same scheme as the rest of the doc; ❓ is used when a competitor's app behavior on that exact axis isn't reliably documented and we don't want to guess.

---

## 16. Android TV / Fire TV apps

| Feature                          | OnScreen | Plex Android TV | Emby Android TV | Jellyfin Android TV | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Device-pairing (PIN) sign-in     | ✅ | ✅ | ✅ | ⚠️ | OnScreen pair flow covers OIDC/SAML/LDAP/local — TV never types a password |
| Password sign-in                 | ✅ | ⚠️ | ✅ | ✅ | Plex pushes Plex.tv SSO over local password |
| Hub (Continue / Recently / Trending) | ✅ | ✅ | ✅ | ✅ | |
| Library browse + genre filter    | ✅ | ✅ | ✅ | ✅ | |
| Full-text search                 | ✅ | ✅ | ✅ | ✅ | |
| Search type-filter chips         | ✅ | ❌ | ❌ | ❌ | OnScreen TV: Movies / TV Shows / Episodes / Tracks chips on SearchFragment, persisted via DataStore — matches the web/webOS/Roku/Tizen feature |
| Item detail page                 | ✅ | ✅ | ✅ | ✅ | |
| Favorites                        | ✅ | ✅ | ✅ | ✅ | |
| History                          | ✅ | ✅ | ✅ | ✅ | |
| Collections drill-in             | ✅ | ✅ | ✅ | ✅ | |
| Photo viewer with D-pad nav      | ✅ | ✅ | ❓ | ❓ | OnScreen auto-resolves siblings from parent album or library |
| Audiobook chapter list           | ✅ | ❌ | ❓ | ❓ | OnScreen ships chapter list + 0.75–2× speed picker |
| Direct play                      | ✅ | ✅ | ✅ | ✅ | |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ✅ | |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | Avoids ERROR_CODE_IO_BAD_HTTP_STATUS at the 1h access-token mark |
| Audio track picker (HLS re-issue)| ✅ | ❓ | ❓ | ❓ | OnScreen re-issues the session with a new audio_stream_index; transcoded HLS only carries one audio per session |
| Subtitle picker                  | ✅ | ✅ | ✅ | ✅ | |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | 🧩 | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ✅ | |
| Music auto-advance (silent EOS chain) | ✅ | ✅ | ✅ | ✅ | OnScreen chains across album boundaries (last track of A → first of B) |
| Episode auto-advance across season boundaries | ✅ | ✅ | ✅ | ✅ | OnScreen `NextSiblingResolver` falls through season → series → next season |
| Cross-device resume sync         | ✅ | ✅ | ✅ | ⚠️ | SSE `progress.updated` push; Jellyfin polls |
| Picture-in-picture (video)       | ✅ | ✅ | ❓ | ❓ | OnScreen: standard Android `enterPictureInPictureMode` from PlaybackFragment |
| Background audio + system controls | ✅ | ✅ | ✅ | ✅ | OnScreen: Media3 `MediaSessionService` runs progress reporter + auto-advance independent of the fragment so playback survives backgrounding |
| Album-art backdrop on playback   | ✅ | ✅ | ❓ | ❓ | OnScreen renders the poster as a blurred full-screen backdrop during music playback |
| In-player OpenSubtitles search   | ✅ | ❌ | ⚠️ | 🧩 | Search + download + attach to the active file from inside the player |
| Plexamp-style "Play All" / Shuffle on artist | ✅ | ✅ | ⚠️ | ⚠️ | Builds a queue from every track on the artist's albums |
| Live TV channel grid + Recordings list | ✅ | ✅ | ✅ | ✅ | OnScreen: dedicated Leanback rows backed by `/livetv/channels` + `/livetv/recordings` |
| Offline downloads                | ❌ | 💎 | 💎 | ⚠️ | |
| Hardware verified                | ✅ | ✅ | ✅ | ✅ | OnScreen verified on Fire Stick + Google TV |

---

## 17. Android phone apps

| Feature                          | OnScreen | Plex | Emby | Jellyfin | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Device-pairing (PIN) sign-in     | ✅ | ⚠️ | ❓ | ❓ | OnScreen pair flow is the default path on the phone too; password is a fallback |
| Password sign-in                 | ✅ | ⚠️ | ✅ | ✅ | |
| Hub (Continue / Recently / Trending) | ✅ | ✅ | ✅ | ✅ | |
| Library browse + grid view       | ✅ | ✅ | ✅ | ✅ | |
| Full-text search                 | ✅ | ✅ | ✅ | ✅ | OnScreen: 300ms debounce |
| Search type-filter chips         | ❌ | ❌ | ❌ | ❌ | Outstanding for the phone client |
| Item detail page                 | ✅ | ✅ | ✅ | ✅ | OnScreen: minimal — title + year + summary + Play |
| Favorites                        | ✅ | ✅ | ✅ | ✅ | OnScreen: list view; toggle from detail outstanding |
| History                          | ✅ | ✅ | ✅ | ✅ | |
| Collections drill-in             | ✅ | ✅ | ✅ | ✅ | |
| Photo viewer                     | ❌ | ✅ | ✅ | ✅ | Outstanding for the phone client |
| Audiobook chapter list           | ❌ | ❌ | ⚠️ | ⚠️ | Outstanding |
| Direct play                      | ✅ | ✅ | ✅ | ✅ | |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ✅ | Port of TV client's `PlaybackHelper.decide()` matrix |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker (HLS re-issue)| ✅ | ❓ | ❓ | ❓ | |
| Subtitle picker                  | ✅ | ✅ | ✅ | ✅ | |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | 🧩 | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ✅ | |
| Music auto-advance               | ✅ | ✅ | ✅ | ✅ | |
| Cross-device resume sync         | ❌ | ✅ | ✅ | ⚠️ | Outstanding — SSE consumer not wired in the phone client yet |
| Background audio / lock-screen controls | ❌ | ✅ | ✅ | ✅ | Outstanding — needs a Media3 MediaSessionService (TV client has it; phone port pending) |
| CarPlay / Android Auto           | ❌ | ✅ | ❌ | ❌ | Plexamp only |
| Offline downloads                | ✅ | 💎 | 💎 | ⚠️ | OnScreen: WorkManager + Hilt-injected worker + on-disk manifest; player short-circuits to local file when a completed download exists. Free, not paywalled like Plex/Emby |
| Unit tests for player + auth flows | ✅ | ❓ | ❓ | ❓ | 29 tests across PlaybackHelper / HubViewModel / PairViewModel / PlayerViewModel |
| Hardware verified                | ❌ | ✅ | ✅ | ✅ | Real-device validation outstanding |

---

## 18. LG webOS apps

| Feature                          | OnScreen | Plex | Emby | Jellyfin | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Device-pairing (PIN) sign-in     | ✅ | ✅ | ✅ | ⚠️ | |
| Password sign-in                 | ✅ | ⚠️ | ✅ | ✅ | |
| Hub (Continue / Recently / Trending) | ✅ | ✅ | ✅ | ⚠️ | |
| Library browse                   | ✅ | ✅ | ✅ | ⚠️ | |
| Full-text search                 | ✅ | ✅ | ✅ | ⚠️ | |
| Search type-filter chips         | ✅ | ❌ | ❌ | ❌ | |
| Item detail page                 | ✅ | ✅ | ✅ | ⚠️ | |
| Favorites                        | ✅ | ✅ | ✅ | ⚠️ | |
| History                          | ✅ | ✅ | ✅ | ⚠️ | |
| Collections drill-in             | ✅ | ✅ | ✅ | ⚠️ | |
| Photo viewer (D-pad sibling nav) | ✅ | ✅ | ❓ | ❓ | |
| Audiobook chapter list           | ✅ | ❌ | ❓ | ❓ | |
| Direct play                      | ✅ | ✅ | ✅ | ⚠️ | |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ⚠️ | hls.js + HTML5 `<video>` |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker               | ⚠️ | ✅ | ✅ | ⚠️ | Direct-play only — HLS re-issue path not wired |
| Subtitle picker                  | ✅ | ✅ | ✅ | ⚠️ | |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | 🧩 | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ⚠️ | |
| Music auto-advance               | ✅ | ✅ | ✅ | ⚠️ | |
| Cross-device resume sync (SSE)   | ✅ | ✅ | ✅ | ⚠️ | |
| Hardware verified                | ❌ | ✅ | ✅ | ❓ | Real LG TV validation outstanding |

---

## 19. Samsung Tizen apps

| Feature                          | OnScreen | Plex | Emby | Jellyfin | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Device-pairing (PIN) sign-in     | ✅ | ✅ | ✅ | ⚠️ | |
| Password sign-in                 | ✅ | ⚠️ | ✅ | ⚠️ | |
| Hub                              | ✅ | ✅ | ✅ | ⚠️ | |
| Library browse                   | ✅ | ✅ | ✅ | ⚠️ | |
| Full-text search                 | ✅ | ✅ | ✅ | ⚠️ | |
| Search type-filter chips         | ✅ | ❌ | ❌ | ❌ | |
| Item detail page                 | ✅ | ✅ | ✅ | ⚠️ | |
| Favorites                        | ✅ | ✅ | ✅ | ⚠️ | |
| History                          | ✅ | ✅ | ✅ | ⚠️ | |
| Collections drill-in             | ✅ | ✅ | ✅ | ⚠️ | |
| Photo viewer (D-pad sibling nav) | ✅ | ✅ | ❓ | ❓ | |
| Audiobook chapter list           | ✅ | ❌ | ❓ | ❓ | |
| Direct play                      | ✅ | ✅ | ✅ | ⚠️ | AVPlay JS API HW path |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ⚠️ | AVPlay HLS + HTML5 `<video>` fallback |
| HW HEVC / AV1 decode             | ✅ | ✅ | ✅ | ⚠️ | Firmware decoders via AVPlay |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker               | ⚠️ | ✅ | ✅ | ⚠️ | Direct-play only |
| Subtitle picker                  | ✅ | ✅ | ✅ | ⚠️ | |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | 🧩 | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ⚠️ | |
| Music auto-advance               | ✅ | ✅ | ✅ | ⚠️ | |
| Cross-device resume sync (SSE)   | ✅ | ✅ | ✅ | ⚠️ | |
| Hardware verified                | ❌ | ✅ | ✅ | ❓ | Real Samsung TV validation outstanding |

---

## 20. Roku apps

| Feature                          | OnScreen | Plex | Emby | Jellyfin | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Device-pairing (PIN) sign-in     | ✅ | ✅ | ✅ | ⚠️ | Jellyfin: third-party channel |
| Password sign-in                 | ✅ | ⚠️ | ✅ | ⚠️ | |
| Hub                              | ✅ | ✅ | ✅ | ⚠️ | |
| Library browse                   | ✅ | ✅ | ✅ | ⚠️ | |
| Full-text search                 | ✅ | ✅ | ✅ | ⚠️ | |
| Search type-filter chips         | ✅ | ❌ | ❌ | ❌ | |
| Item detail page                 | ✅ | ✅ | ✅ | ⚠️ | Type-aware DetailScene |
| Favorites                        | ✅ | ✅ | ✅ | ⚠️ | |
| History                          | ✅ | ✅ | ✅ | ⚠️ | |
| Collections drill-in             | ✅ | ✅ | ✅ | ⚠️ | |
| Photo viewer (D-pad sibling nav) | ✅ | ✅ | ❓ | ❓ | |
| Audiobook                        | ⚠️ | ❌ | ⚠️ | ⚠️ | DetailScene + direct play; chapter list outstanding |
| Direct play                      | ✅ | ✅ | ✅ | ⚠️ | Firmware Video node |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ⚠️ | `Playback_Decide` three-mode split, 13 brs unit tests |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker               | ❌ | ✅ | ✅ | ⚠️ | Outstanding |
| Subtitle picker                  | ❌ | ✅ | ✅ | ⚠️ | Outstanding — firmware Video node supports it; not wired |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | ⚠️ | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ⚠️ | |
| Music auto-advance               | ✅ | ✅ | ✅ | ⚠️ | |
| Cross-device resume sync         | ⚠️ | ✅ | ✅ | ⚠️ | 5s polling fallback (Roku has no SSE primitive) |
| Hardware verified                | ❌ | ✅ | ✅ | ❓ | Channel-zip validation outstanding |

---

## 21. Native desktop apps

| Feature                          | OnScreen (Tauri) | Plex (Plexamp / HTPC) | Emby Theater | Jellyfin (JMP) | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Single shared codebase with web  | ✅ | ⚠️ | ⚠️ | ✅ | |
| Native audio engine              | ✅ | ✅ | ✅ | ⚠️ | OnScreen: cpal + claxon; JMP: mpv |
| WASAPI exclusive (Win)           | ❌ | ✅ | ✅ | ✅ | cpal 0.16 limitation |
| CoreAudio HOG (macOS)            | ❌ | ✅ | ⚠️ | ✅ | |
| ALSA `hw:` (Linux)               | ❌ | ✅ | ⚠️ | ✅ | |
| Gapless playback                 | ✅ | ✅ | ✅ | ✅ | |
| Native FLAC                      | ✅ | ✅ | ✅ | ✅ | claxon |
| Native ALAC / WAV / AIFF         | ⚠️ | ✅ | ✅ | ✅ | Webview fallback for non-FLAC |
| OS media keys                    | ✅ | ✅ | ✅ | ✅ | |
| System tray                      | ✅ | ✅ | ✅ | ⚠️ | |
| Native OS notifications          | ✅ | ✅ | ✅ | ⚠️ | |
| OS now-playing widget (SMTC/MPRIS)| ❌ | ✅ | ✅ | ⚠️ | `souvlaki` swap pending |
| Secure credential storage        | ✅ | ✅ | ✅ | ⚠️ | Keychain / Cred Mgr / Secret Service |
| Cross-device resume sync (SSE)   | ✅ | ✅ | ✅ | ⚠️ | |
| "Play on this device" remote     | ❌ | ✅ | ✅ | ⚠️ | |
| Picture-in-picture               | ❌ | ✅ | ✅ | ⚠️ | |
| Configurable server URL          | ✅ | ⚠️ | ✅ | ✅ | No Plex.tv lock-in |

---

## 22. Web client (browser)

The web client is the universal fallback — runs in any modern browser with no install. Compared against Plex Web / Emby Web / Jellyfin Web rather than against the native apps.

| Feature                          | OnScreen | Plex Web | Emby Web | Jellyfin Web | Notes |
|----------------------------------|:--:|:--:|:--:|:--:|---|
| Device-pairing PIN page (`/pair`)| ✅ | ❌ | ❌ | ❌ | Browser is the canonical PIN-claim surface for the TV apps |
| Hub                              | ✅ | ✅ | ✅ | ✅ | |
| Library browse + genre filter    | ✅ | ✅ | ✅ | ✅ | |
| Full-text search                 | ✅ | ✅ | ✅ | ✅ | |
| Search type-filter chips         | ✅ | ❌ | ❌ | ❌ | |
| TMDB discover + Request inline   | ✅ | ❌ | ❌ | ❌ | |
| Item detail page                 | ✅ | ✅ | ✅ | ✅ | |
| Favorites                        | ✅ | ✅ | ✅ | ✅ | |
| History                          | ✅ | ✅ | ✅ | ✅ | |
| Collections (incl. smart playlists) | ✅ | ✅ | ✅ | ✅ | |
| Analytics dashboard              | ✅ | ✅ | ✅ | ✅ | |
| Photo viewer + EXIF + map        | ✅ | ⚠️ | ⚠️ | ⚠️ | |
| Audiobook chapter list           | ✅ | ❌ | ⚠️ | ⚠️ | |
| Music: Lossless + hi-res badges  | ✅ | ⚠️ | ❌ | ⚠️ | |
| Music: Synced lyrics (USLT/.lrc/LRCLIB) | ✅ | ✅ | ✅ | ✅ | |
| Music: Gapless via dual-`<audio>`| ✅ | ✅ | ✅ | ✅ | |
| Direct play (raw file + range)   | ✅ | ✅ | ✅ | ✅ | |
| HLS transcode + DASH             | ✅ | ✅ | ✅ | ✅ | DASH server-side; frontend still uses hls.js |
| Audio + subtitle pickers         | ✅ | ✅ | ✅ | ✅ | |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | 🧩 | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ✅ | |
| Cross-device resume sync (SSE)   | ✅ | ✅ | ✅ | ⚠️ | |
| Admin: settings + scan + users   | ✅ | ✅ | ✅ | ✅ | |
| Admin: log retrieval             | ✅ | ❌ | ❌ | ❌ | `/api/v1/admin/logs` ring buffer |
| Admin: live session monitoring   | ✅ | ✅ | ✅ | ✅ | |

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

## Where OnScreen Trails (as of 2026-04-29)

- **EPUB / CBR books** — CBZ scan + reader shipped in v2.1 Stage 1, but the other two formats still need their parsers and explicitly slipped to Stage 2.
- **No Tidal / Qobuz integration** for music streaming.
- **No HEVC / AV1 hardware encode validated on real hardware** yet — code paths shipped, beta validation pending.
- **No direct cloud-storage integration** (S3/GCS); all four rely on local or NFS mounts.
- **No bit-perfect playback** — the native Tauri shell ships with a cpal+claxon FLAC engine (out of webview), but cpal 0.16 hard-codes WASAPI shared mode so the OS mixer can still resample. Real exclusive output needs either a cpal fork or dropping to raw `wasapi`/`coreaudio`/`alsa` per platform — multi-day work behind the audiophile pillar.
- **TV / mobile coverage is uneven** — OnScreen has a Tauri 2 desktop client (Windows/macOS/Linux), a hardware-verified Android TV / Fire TV client (Leanback + Media3 ExoPlayer), a feature-complete LG webOS app (SvelteKit + ares-package), a Roku app at full flow parity (BrightScript + SceneGraph, including transcode negotiation), a Samsung Tizen app at flow parity (SvelteKit + AVPlay), and an Android phone app at flow parity (Compose + Material 3, scaffold in `clients/android_native/`). webOS, Roku, Tizen, and the Android phone app still need real-hardware validation. iOS and Apple TV apps don't exist. The web frontend works in those browsers as a fallback.
- **No "play on this device" remote control** — cross-device resume sync ships in v2.1 (SSE `progress.updated` broadcast + watch-page consumer), but transferring an active playback session from one device to another isn't wired.
- **DASH on the client side** — `manifest.mpd` ships server-side in v2.1, but the frontend still uses `hls.js`. Smart-TV apps (Tizen, webOS, Roku) that prefer DASH won't see the benefit until the shaka-player swap lands.
- **Picture-in-picture server signal** — handler/store has no PiP-mode flag yet.

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
- ✅ Built-in HTTPS (operator-provided PEM via `TLS_CERT_FILE`/`TLS_KEY_FILE`)

## v2.1 Closed (in flight on `main`)

- ✅ **Track A — Bug-shape fixes** (3/3): job-queued OCR endpoint (POST returns 202 + job_id, GET polls — unblocks Cloudflare Tunnel free-tier users hitting 100 s timeouts); Vitest SMTP fixture cleanup; Valkey-backed SAML request tracker (HA-ready — AuthnRequest minted on instance A is validatable by ACS callback on instance B)
- ✅ **Track B — Media types**: home_video library + date-grouped page; CBZ books with paginated reader; audiobook author display + chapter-boundary resume; podcast show + episode detail UI
- ✅ **Track F — Discovery**: smart playlists (rule JSONB, query-time evaluation); trending row (rolling watch_events aggregate). Watch-cooccurrence recommendations + "Because you watched X" were built (item-to-item collaborative filtering, replaced the planned pgvector pipeline) but removed from the home hub before release — the row didn't earn its space; trending stays. Cooccurrence table + sql kept dormant in case the row earns a comeback
- ✅ **Track G — Per-user policy** (5/5): library `is_private` flag with public/private union semantics; `auto_grant_new_users` template wired into invite + OIDC + SAML + LDAP user-creation paths; per-profile inherit-or-override library access; content-rating gates closed in `ListCollectionItems`, `ListItemsByGenre`, `ListWatchHistory`; admin "view as" middleware (read-only, GET-only, IDOR-gated)
- ✅ **Track H — Streaming format**: server-side DASH `manifest.mpd` endpoint over the existing fMP4 ladder (one segment ladder, two manifests) + `manifest_url` exposed on the session-start response; frontend shaka-player swap intentionally deferred — real DASH leverage is smart-TV native clients (Track E) consuming the URL directly
- ✅ **Track D — Quality + dev workflow** (3/3): `auth-providers.spec.ts` Playwright spec covering OIDC PKCE shape, SAML signed-AuthnRequest (locks the four-layer SAML signing fix behind a regression guard), LDAP end-to-end + negative path; gh CLI added to CONTRIBUTING.md prereqs (cuts release form to one command); 10-PR Dependabot triage doc grouping the v2.0-tag queue by risk with paste-ready merge commands
- ✅ **Track E — Native desktop client** (most of the list): Tauri 2 shell for Windows/macOS/Linux reusing the SvelteKit bundle in a system webview; cpal + claxon native FLAC engine (play/pause/preload/seek/auto-advance) outside the webview; OS media keys via `tauri-plugin-global-shortcut`; system tray with transport menu; OS notifications on track change; refresh + access tokens in the OS keychain (Windows Credential Manager / macOS Keychain / Linux Secret Service) with one-shot store-to-keychain migration; SSE `progress.updated` broadcast + watch-page consumer for cross-device resume. **Outstanding:** real WASAPI exclusive mode (cpal 0.16 limitation, multi-day platform work), OS now-playing widgets (`souvlaki` swap), "play on this device" remote control.
- ✅ **Track E — TV clients**:
  - **Android TV / Fire TV** (hardware-verified): full Leanback + Media3 ExoPlayer client; device-pairing sign-in covers every auth provider via web browser PIN handoff; photo viewer with D-pad sibling navigation (auto-resolves siblings from parent album or library); music auto-advance through albums (silent EOS chain, no Up Next overlay); audiobook speed picker (0.75–2x); collections drill from search/hub; HLS retry policy + 60s read timeout for cold-start transcodes over Cloudflare Tunnel; screen-on flag during active playback.
  - **LG webOS** (SvelteKit + ares-package): setup → login → hub → library → item → search → watch + pairing flow + search type-filter chips + photo viewer + audiobook chapter list + collections + favorites + history + skip-intro/credits + Up Next + music auto-advance + SSE cross-device resume. Hardware validation on a real LG TV outstanding.
  - **Roku** (BrightScript + SceneGraph): setup → login → hub → DetailScene → search with type-filter chips + photo viewer with D-pad sibling nav + favorites + history + collections + transcode negotiation (direct/remux/transcode) + markers + Up Next + music auto-advance + cross-device sync via 5 s polling + per-file stream-token consumption. `Playback_Decide` covered by 13 brs unit tests; channel zip builds clean.
  - **Samsung Tizen** (SvelteKit + tizen-package): bulk-ported from webOS — pairing flow + hub + search with type-filter chips + photos + audiobook chapter list + collections + favorites + history + skip-intro/credits + Up Next + music auto-advance + cross-device SSE sync. AVPlay JS dual-path with HTML5 `<video>` fallback for HW HEVC/AV1. Hardware validation on a real Samsung TV outstanding.
  - **Android phone** (Compose + Material 3): new module at `clients/android_native/`, package `tv.onscreen.mobile`, distinct from the TV client at `clients/android/`. Reuses the TV client's data layer verbatim (Retrofit + Moshi + Hilt + DataStore + AuthInterceptor + TokenAuthenticator). UI: pairing-PIN sign-in (password fallback), hub with poster strips, library grid, item detail, search with debounce, favorites + history + collections drill-in. Player: Media3 ExoPlayer with the full direct/remux/transcode negotiation port from `PlaybackHelper.decide()`, per-file 24h stream token, audio + subtitle pickers (HLS audio re-issues the session with a new audio_stream_index), 10 s progress reporting, skip-intro/credits overlay, Up Next + music auto-advance. Real-hardware validation, photo viewer, audiobook chapter list, cross-device SSE resume, search type-filter chips, favorite-toggle on detail page outstanding.
- ✅ **Android TV subtitle + audio pickers**: pickers showed only ExoPlayer-side tracks, so transcode/remux sessions (which the server emits with one audio stream) couldn't switch language. `PlaybackViewModel.switchAudioStream` now re-issues the HLS session with a new `audio_stream_index` while preserving position; direct-play still uses ExoPlayer's language selector. Subtitle picker gets the same single-choice UX with active-row detection.
- ✅ **Track J — Admin observability**: `/api/v1/admin/logs` endpoint backed by an in-process 2000-entry slog ring buffer — admin-only, level + limit filters, error attrs stringified for diagnostic readability. Lets operators pull recent server output without SSH/kubectl access (TrueNAS Apps, Cloud Run).
- ✅ **Audiobook embedded covers**: `/items/{id}/image` extends to type=audiobook, extracts the first attached picture from the m4b/mp3/flac container via ffmpeg, runs it through the same resize + on-disk cache as photos. First request per book triggers ffmpeg; subsequent requests at the same dimensions hit the cache.
- ✅ **Continue Watching split (server + clients)**: hub now returns three pre-split arrays (`continue_watching_tv`, `continue_watching_movies`, `continue_watching_other`) on top of the legacy combined feed; TV-shows row dedupes to one tile per series (the most recently watched episode). Web + Android TV + phone consume the split when present and fall back to the combined feed for older servers.
- ✅ **Manual poster picker + admin Fix Match**: admin can search TMDB/TVDB for a movie or show and pin the chosen artwork; cast/crew refresh and a fresh background download fire on confirm. Uses the existing enricher pipeline so the image lands in the same on-disk cache the rest of the artwork uses. Includes a "show poster on episode tiles" preference (`episode_use_show_poster`) that substitutes the series poster on browse surfaces — episode stills look terrible at thumbnail size and most users prefer the show key art.
- ✅ **Sonarr/Radarr/TRaSH naming awareness**: scanner now parses `{tmdb-12345}` / `{tvdb-67890}` / `[release-group]` markers in folder names per the Trash Guides convention, dedupes shows that differ only by release-group prefix or country tag (e.g. "The Zoo IE" vs "The Zoo Ireland"), and uses the embedded ID as a high-confidence match anchor before falling back to title lookup.
- ✅ **Hub UX polish**: "Recently Added" tile click now lands the user on the source library sorted newest-first (one per library, scoped landing) rather than a generic "recently added" list. Empty + error states across all browse fragments on the Android TV client.
- ✅ **Android TV background audio + Picture-in-Picture**: Media3 `MediaSessionService` owns the player when the user backs out of `PlaybackFragment` — progress reporter and auto-advance run in the service so backgrounded playback survives, and `PlaybackFragment` re-attaches to the parked session on re-entry instead of restarting playback. Picture-in-picture for video uses the standard Android `enterPictureInPictureMode`.
- ✅ **Android TV music polish**: cross-album auto-advance (last track of album A → first track of album B), Plexamp-style "Play All" + Shuffle on the artist detail page (queue from every track on the artist's albums), album-art backdrop on the playback fragment for music. Episodes auto-advance across season boundaries (S04E12 → S05E01); the resolver uses series → next-season → first-episode fall-through and lives outside the ViewModel so the `MediaSessionService` calls the same logic on STATE_ENDED.
- ✅ **Android TV in-player OpenSubtitles search**: search by language + title from the player overlay, download attaches to the active file's `media_files` row, server-side metadata is carried over from the search result so the server doesn't re-query OpenSubtitles. Same flow on the web side ports here.
- ✅ **Android TV Live TV channel grid + Recordings list**: dedicated Leanback rows backed by `/livetv/channels` + `/livetv/recordings`; pulls Live TV onto the same surface as VOD playback for users who run the EPG.
- ✅ **Android phone offline downloads**: WorkManager + Hilt-injected worker + on-disk JSON manifest. Player short-circuits to the local file before even running the direct/transcode decision when a completed download exists. The TV client deliberately stays online-only (couches near a network); this is the phone-only differentiator that flips the matrix cell from `❌` to `✅` against Plex/Emby's premium-tier downloads.
- ✅ **Native desktop audio engine — phase 2**: gapless preload (the next track's ringbuf primes during the current track's tail; promotion is sub-frame on EOS), position polling with auto-advance on engine-side STATE_ENDED, asset URLs honour the configured server base, query-token carrier for asset routes (so the cpal engine works through the same `?token=` middleware path that ExoPlayer uses on TV).
- ✅ **Test infrastructure restoration**: server settings test moved behind `//go:build integration` (testcontainers panicked locally without Docker), `lostcancel` fix in `worker/master.go`, race-detector CI job for concurrent packages. Android TV unit suite restored (HomeViewModel rewrite + `supervisorScope` so a hub-fetch failure doesn't cancel sibling fetches; SearchViewModel + PlaybackViewModel ctor patches; orphaned NotificationsViewModelTest deleted). Web `AudioPlayer.test.ts` mock extended with `getApiBase` + `getBearerToken`. Android phone client seeded with 29 unit tests across PlaybackHelper / HubViewModel / PairViewModel / PlayerViewModel — closes the "no test sources" gap.
- ✅ **Per-file streaming token + auth hardening**: native players (ExoPlayer, Roku Video node, Tizen AVPlay, mpv) bypass the OkHttp / fetch token-refresh paths, so a 1 h access token expired mid-stream and surfaced as `ERROR_CODE_IO_BAD_HTTP_STATUS` on the next range request. `auth.IssueStreamToken(claims, fileID)` mints a 24 h PASETO with two new claims: `purpose="stream"` (rejected on the Bearer / cookie path so a leaked stream URL can't grant general API access) and `file_id=<uuid>` (asset middleware enforces the chi `{id}` URL param matches, so the leaked URL can't be repurposed across files). `ItemHandler.Get` returns one stream token per file in the response. `authService.Logout` now bumps `session_epoch` after deleting the session — closes a pre-existing weakness where outstanding access tokens kept working until natural TTL after logout. Android, webOS, and Roku all consume the token; older clients ignore the field and fall back to the access token.

## Non-Differentiators (All Four Roughly Equal)

Movies / TV / music / photo scanning, embedded + disk art, TMDB+TVDB+MusicBrainz metadata, HLS streaming, direct play, resume position, multi-user, parental content ratings, chapter markers, audit-safe session management.
