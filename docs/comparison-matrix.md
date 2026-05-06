# OnScreen vs Plex / Emby / Jellyfin

**Snapshot:** 2026-05-05 against v2.1.1-dev on `main` (server v2.1.0 tagged; v2.2 anime track landed 2026-05-04 ahead of the v2.2 cut; Play Store internal-testing track active for the Android TV client).

**Legend** — ✅ in core · 💎 paid tier · 🧩 official plugin · ⚠ partial · ❌ not supported

**Scope** — server-side features and first-party clients. Plex / Emby / Jellyfin rows reflect widely-documented upstream behaviour as of the snapshot date; tiering and plugin availability change over time. Cells where all four are ✅ have been moved to [Non-differentiators](#non-differentiators) at the bottom rather than padding every table.

---

## 1. Media types

| Feature                                     | OnScreen | Plex | Emby | Jellyfin |
| ------------------------------------------- | :------: | :--: | :--: | :------: |
| Live TV                                     |    ✅    |  💎  |  💎  |    ✅    |
| DVR                                         |    ✅    |  💎  |  💎  |    ✅    |
| Anime library type (AniList primary)        |    ✅    |  ❌  |  🧩  |    🧩    |
| Books / comics (CBZ + CBR + EPUB)           |    ✅    |  ❌  |  ⚠   |    ⚠    |
| Audiobook author / series hierarchy         |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Music videos (typed)                        |    ✅    |  ✅  |  ✅  |    ✅    |
| Home videos (separate type, on-disk edits)  |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Podcasts (local files)                      |    ⚠    |  ⚠   |  ❌  |    🧩    |
| Podcasts (RSS subscription)                 |    ❌    |  ✅  |  ❌  |    🧩    |

OnScreen native books reader handles all three formats with one in-browser UI (image-page mode for CBZ/CBR; epub.js for EPUB with reflowable pagination). Audiobooks: full `book_author → book_series → audiobook → audiobook_chapter` schema with multi-file resume snapping to chapter boundary, embedded cover serving, and author/series detail pages on every native client.

Anime is a first-class library type — AniList runs primary instead of fallback, with a per-season franchise walk that maps "Show / Season 2 / Season 3" on disk onto the distinct AniList Media rows (the cours that AniList tracks separately). Episode metadata falls through TMDB → TVDB → AniList streamingEpisodes so unmainstream / unlicensed shows still land titles + thumbnails, and a watching-status mirror (Plan to Watch / Watching / Completed / On Hold / Dropped) ships with the track. Plex has no anime-aware path; Emby and Jellyfin rely on community plugins (Shoko, jellyfin-plugin-anime) that vary in upstream-source coverage.

---

## 2. Transcoding

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Hardware encode (NVENC)                            |    ✅    |  💎  |  💎  |    ✅    |
| Hardware encode (QSV)                              |    ✅    |  💎  |  💎  |    ✅    |
| Hardware encode (AMF)                              |    ✅    |  💎  |  💎  |    ✅    |
| Hardware encode (VAAPI)                            |    ⚠    |  💎  |  💎  |    ✅    |
| AV1 encode (NVENC)                                 |    ✅    |  💎  |  💎  |    ⚠    |
| AV1 encode (QSV, Arc / Xe2)                        |    ✅    |  💎  |  ❌  |    ⚠    |
| HDR → SDR tonemap                                  |    ✅    |  💎  |  💎  |    ✅    |
| Subtitle burn-in (PGS / VOBSUB)                    |    ✅    |  ✅  |  ✅  |    ✅    |
| Subtitle OCR (PGS / VOBSUB → text WebVTT)          |    ✅    |  ❌  |  ❌  |    ⚠    |
| Trickplay sprite sheets (BIF-shape)                |    ✅    |  💎  |  💎  |    ✅    |
| fMP4 HLS for HEVC + AV1 (vs MPEG-TS)               |    ✅    |  ✅  |  ✅  |    ✅    |
| Adaptive bitrate ladder (multi-rendition HLS)      |    ❌    |  ✅  |  ✅  |    ✅    |
| Multi-worker fleet (separate worker binary)        |    ✅    |  ❌  |  ❌  |    ❌    |
| Per-session supersede (one stream per user / item) |    ✅    |  ✅  |  ⚠   |    ⚠    |

VAAPI is the last encoder family pending hardware validation — TrueNAS GPU box is NVIDIA-only, an Intel Arc test rig is in the v2.1 backlog. AV1 NVENC was end-to-end-validated 2026-04-30 on RTX 5080. AMD AV1 encode requires an RDNA3 dGPU (Ryzen 9900X iGPU's RDNA2 VCN3 doesn't have an AV1 encoder block).

OnScreen runs Tesseract on PGS / VOBSUB / DVB / XSUB streams and persists the results as `external_subtitles` rows so every client gets text-based playback (smaller bandwidth, restyleable, searchable) rather than burning the bitmap into the video stream. Plex and Emby only do burn-in; Jellyfin has community-plugin OCR. Trickplay generates 10-per-row sprite sheets at 10 s intervals with WebVTT `xywh` cues — same shape Plex Pass / Emby Premiere ship paid; OnScreen ships in core.

---

## 3. Music — audiophile detail

| Feature                                                         | OnScreen | Plex | Emby | Jellyfin |
| --------------------------------------------------------------- | :------: | :--: | :--: | :------: |
| FLAC / ALAC / DSD passthrough                                   |    ✅    |  ✅  |  ✅  |    ✅    |
| WASAPI exclusive (Windows, bit-perfect)                         |    ✅    |  💎  |  ❌  |    ❌    |
| DSD-via-DoP playback                                            |    ✅    |  💎  |  ❌  |    ❌    |
| ReplayGain (track + album, with preamp)                         |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Bit depth / sample rate / channel layout API surface            |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| MusicBrainz ID set (recording / release / artist / album-artist)|    ✅    |  ⚠   |  ⚠   |    ✅    |
| Cover Art Archive fallback                                      |    ✅    |  ❌  |  ✅  |    ✅    |
| Hi-Res / Lossless badging in the player                         |    ✅    |  💎  |  ⚠   |    ❌    |
| Gapless playback (web client)                                   |    ✅    |  ✅  |  ✅  |    ⚠    |
| Tidal / Qobuz integration                                       |    ❌    |  💎  |  ❌  |    ❌    |

OnScreen's native desktop client decodes through symphonia 0.5 and writes raw `IAudioClient` in `AUDCLNT_SHAREMODE_EXCLUSIVE` — OS mixer bypassed. Plex Pass ships an exclusive-mode pipeline in Plexamp; the rest layer Roon / Audirvana on top.

---

## 4. Live TV / DVR

| Feature                                                       | OnScreen | Plex | Emby | Jellyfin |
| ------------------------------------------------------------- | :------: | :--: | :--: | :------: |
| HDHomeRun tuner                                               |    ✅    |  💎  |  💎  |    ✅    |
| M3U / IPTV tuner                                              |    ✅    |  ⚠   |  💎  |    ✅    |
| Schedules Direct EPG                                          |    ✅    |  💎  |  💎  |    ✅    |
| Recording rules (once / series / channel-block)               |    ✅    |  💎  |  💎  |    ✅    |
| Series new-only filter                                        |    ✅    |  💎  |  💎  |    ⚠    |
| Pre / post padding per recording                              |    ✅    |  💎  |  💎  |    ✅    |
| Retention purge (auto-delete after N days)                    |    ✅    |  💎  |  💎  |    ✅    |
| Stream-copy capture (zero CPU)                                |    ✅    |  ✅  |  ✅  |    ✅    |
| Refcounted shared sessions (multiple viewers, one tuner slot) |    ✅    |  ⚠   |  ⚠   |    ⚠    |

Plex and Emby gate the entire Live TV / DVR feature set behind paid tiers (Plex Pass / Emby Premiere). OnScreen and Jellyfin are core.

---

## 5. Discovery & recommendations

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Continue Watching (split TV / Movies / Other)      |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Recently Added per library                         |    ✅    |  ✅  |  ✅  |    ✅    |
| Trending row (rolling watch_events aggregate)      |    ✅    |  ✅  |  ✅  |    ❌    |
| Smart playlists (rule-based, query-time eval)      |    ✅    |  ⚠   |  ✅  |    ⚠    |
| Auto-genre collections (rule-based)                |    ✅    |  ✅  |  ✅  |    ⚠    |
| Intro / credits auto-detection (AcoustID-FP)       |    ✅    |  💎  |  🧩  |    🧩    |
| In-app TMDB discover + request                     |    ✅    |  ❌  |  ❌  |    ❌    |
| "Because you watched X" / personalised row         |    ❌    |  ✅  |  ✅  |    ❌    |

OnScreen's home hub serves the request flow inline — no Overseerr / Ombi / Jellyseerr companion needed. The personalised row was scaffolded (item-to-item collaborative filtering) but pulled before release because it didn't earn the home-hub real estate; trending stays. Intro / credits detection runs `fpcalc` (AcoustID) over a 600 s leading window to find the shared intro fingerprint across episodes of a season, plus `ffmpeg blackdetect` over the trailing 360 s for the credits boundary; both are stored as chapter rows and exposed via `GET /items/{id}` so clients can render skip buttons. Plex Pass ships this as "Intro & Credit Markers"; Emby + Jellyfin lean on the community Intro Skipper plugin.

---

## 6. Playback & client UX

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Skip intro / skip credits button on player         |    ⚠    |  💎  |  🧩  |    🧩    |
| Sleep timer                                        |    ❌    |  ✅  |  ✅  |    ✅    |
| On-screen subtitle styling (font/size/color)       |    ❌    |  ✅  |  ✅  |    ✅    |
| Chromecast / Google Cast                           |    ❌    |  ✅  |  ✅  |    ✅    |
| AirPlay                                            |    ❌    |  ✅  |  ✅  |    ⚠    |
| DLNA / UPnP server                                 |    ❌    |  ✅  |  ✅  |    ✅    |
| Mobile offline downloads                           |    ❌    |  💎  |  💎  |    ✅    |
| Sync watch / watch parties                         |    ❌    |  ❌  |  ❌  |    ✅    |
| Last.fm / ListenBrainz scrobbling                  |    ❌    |  ⚠   |  🧩  |    🧩    |
| Chapter markers + skip targets                     |    ✅    |  ✅  |  ✅  |    ✅    |

Detection-side intro / credits is shipped (see section 5) but the player-side "Skip intro" button isn't wired into the web client yet — clients consume the chapter rows server-side, the UX surface is the open item. Most of the rest in this section are real trails: ABR ladder, Cast, AirPlay, DLNA, mobile downloads, and SyncPlay-style watch parties are all areas competitors are ahead. None are scheduled for v2.2.

---

## 7. User management & auth

| Feature                                                       | OnScreen | Plex | Emby | Jellyfin |
| ------------------------------------------------------------- | :------: | :--: | :--: | :------: |
| Multi-user with managed profiles                              |    ✅    |  ✅  |  ✅  |    ✅    |
| Parental rating ceiling per profile                           |    ✅    |  ✅  |  ✅  |    ✅    |
| Library-level visibility (`is_private`)                       |    ✅    |  ⚠   |  ✅  |    ✅    |
| Auto-grant template for new users                             |    ✅    |  ❌  |  ⚠   |    ❌    |
| Admin "view as" (test policy as a target user)                |    ✅    |  ❌  |  ❌  |    ❌    |
| OIDC                                                          |    ✅    |  ❌  |  ❌  |    🧩    |
| OAuth (Google / GitHub / Discord)                             |    ✅    |  ❌  |  ❌  |    ❌    |
| SAML 2.0 SP-initiated SSO                                     |    ✅    |  ❌  |  💎  |    ❌    |
| LDAP (incl. group sync)                                       |    ✅    |  ❌  |  💎  |    🧩    |
| PASETO tokens (over JWT)                                      |    ✅    |  ❌  |  ❌  |    ❌    |
| Per-file streaming token (24h, file_id-bound, purpose-scoped) |    ✅    |  ❌  |  ❌  |    ❌    |

OIDC + OAuth + SAML + LDAP are all core, no plugin install. The per-file stream token closes the long-tail "ExoPlayer dies at 1 h on a 90-minute movie" failure — natively-played streams need a longer-lived token than the API access token, and that token must not be repurposable as a Bearer or for a different file.

---

## 8. Native clients

Per-platform status. ✅ here means "shipped to a real distribution channel and exercised on hardware"; ⚠ means code-complete but not yet hardware-verified or in soak.

| Platform                       | OnScreen | Plex | Emby | Jellyfin |
| ------------------------------ | :------: | :--: | :--: | :------: |
| Web (browser)                  |    ✅    |  ✅  |  ✅  |    ✅    |
| Desktop (Windows/macOS/Linux)  |    ✅    |  ✅  |  ✅  |    ✅    |
| Android phone                  |    ⚠    |  ✅  |  ✅  |    ✅    |
| Android TV / Google TV         |    ⚠    |  ✅  |  ✅  |    ✅    |
| Fire TV                        |    ⚠    |  ✅  |  ✅  |    ✅    |
| LG webOS                       |    ⚠    |  ✅  |  ✅  |    🧩    |
| Samsung Tizen                  |    ⚠    |  ✅  |  ✅  |    🧩    |
| Roku                           |    ⚠    |  ✅  |  ✅  |    🧩    |
| iOS / iPadOS                   |    ❌    |  ✅  |  ✅  |    ✅    |
| Apple TV                       |    ❌    |  ✅  |  ✅  |    ✅    |

OnScreen's Android TV / Fire TV client is on Play Store internal testing as of 2026-05-04 (graduates to closed → open → production over a 14-day Play-mandated soak). Desktop ships via Tauri 2 with a native Rust audio engine outside the webview. webOS / Tizen / Roku are feature-complete in code; real-hardware soak is the open item. iOS + Apple TV are out of scope until a Swift skill ramp + App Store review budget land.

---

## 9. Admin & observability

| Feature                                          | OnScreen | Plex | Emby | Jellyfin |
| ------------------------------------------------ | :------: | :--: | :--: | :------: |
| OpenTelemetry tracing (OTLP/gRPC)                |    ✅    |  ❌  |  ❌  |    ❌    |
| Prometheus metrics endpoint                      |    ✅    |  ❌  |  ❌  |    ⚠    |
| Structured JSON logs with trace IDs              |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Audit log of admin / playback / auth events     |    ✅    |  ❌  |  ⚠   |    ⚠    |
| Admin logs API (in-process ring buffer)          |    ✅    |  ❌  |  ❌  |    ❌    |
| Schema-version-gated `/health/ready`             |    ✅    |  ❌  |  ❌  |    ❌    |
| Backup + restore round-trip (schema-aware)       |    ✅    |  ❌  |  ✅  |    ✅    |
| Admin Settings UI (no XML / JSON config files)   |    ✅    |  ⚠   |  ✅  |    ✅    |

OnScreen ships an OTel + Prometheus + audit-log stack as core; competitors either omit telemetry, gate behind a paid tier, or expect operators to layer it themselves.

---

## 10. Security & privacy

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Secret encryption at rest (AES-256-GCM)            |    ✅    |  ❌  |  ❌  |    ❌    |
| Built-in HTTPS (operator-provided PEM)             |    ✅    |  ❌  |  ❌  |    ✅    |
| Path-traversal hardening on every asset route      |    ✅    |  ✅  |  ✅  |    ✅    |
| Rate limiting (per-route, env-overridable)         |    ✅    |  ❌  |  ⚠   |    ⚠    |
| No third-party telemetry / analytics in clients    |    ✅    |  ❌  |  ⚠   |    ✅    |
| Self-hosted account system (no vendor cloud)       |    ✅    |  ❌  |  ✅  |    ✅    |

Plex requires a plex.tv account for sign-in even on a self-hosted server. OnScreen and Jellyfin are fully self-hosted; Emby is mostly self-hosted with optional cloud features.

---

## 11. Storage & infrastructure

| Feature                                            | OnScreen   | Plex   | Emby   | Jellyfin |
| -------------------------------------------------- | :--------: | :----: | :----: | :------: |
| Database                                           | PostgreSQL | SQLite | SQLite |  SQLite  |
| Stateless API tier (horizontally scalable)         |     ✅     |   ❌   |   ❌   |    ❌    |
| Event-sourced watch state (immutable log)          |     ✅     |   ❌   |   ❌   |    ❌    |
| Materialized hub cache                             |     ✅     |   ❌   |   ❌   |    ❌    |
| Single-binary deployment                           |     ✅     |   ✅   |   ✅   |    ✅    |
| Docker / Compose first-class                       |     ✅     |   ✅   |   ✅   |    ✅    |
| Direct cloud storage (S3 / GCS)                    |     ❌     |   ❌   |   ❌   |    ❌    |

PostgreSQL-native is the foundational architecture choice — partitioned `watch_events` tables, tsvector full-text search, materialized views for the home hub, no SQLite write-contention pain at scale. None of the four ship native S3/GCS libraries; all four expect the operator to mount with rclone or similar.

---

## 12. Plugins & extensibility

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Webhooks (HMAC-signed, retryable)                  |    ✅    |  ❌  |  ✅  |    ✅    |
| MCP-compatible plugin host (outbound)              |    ✅    |  ❌  |  ❌  |    ❌    |
| In-process plugin host                             |    ❌    |  ❌  |  ✅  |    ✅    |
| Tautulli / Overseerr-shape integration             |    ✅    |  ✅  |  ⚠   |    ⚠    |

OnScreen plugins are MCP servers OnScreen calls out to (outbound MCP). Inbound MCP was rejected as a security stance; the plugin attack surface stays one-way. Webhooks are HMAC-SHA256-signed, retried with exponential backoff, and shaped to drop into existing Overseerr / Tautulli receivers.

---

## 13. License

| Feature                                | OnScreen   | Plex        | Emby                          | Jellyfin |
| -------------------------------------- | :--------: | :---------: | :---------------------------: | :------: |
| Open source                            | ✅ AGPLv3  |     ❌      | ⚠ GPLv2 + proprietary server  | ✅ GPLv2 |
| All features in core (no paid tier)    |     ✅     |     ❌      |              ❌               |    ✅    |

---

## Where OnScreen leads

- **PostgreSQL-native** — partitioned event tables, materialized hub views, tsvector FTS, no SQLite write-contention failure modes under heavy users.
- **Live TV + DVR + hardware transcoding all included** — no Plex Pass / Emby Premiere gate.
- **Modern auth out of the box** — OIDC, OAuth, SAML, LDAP, PASETO; competitors require plugins or paid tiers for most of these.
- **Native bit-perfect audio engine on Windows** — WASAPI exclusive + DSD-via-DoP + ReplayGain enforcement, shipped today. Plex Pass has it in Plexamp; Emby and Jellyfin don't ship a bit-perfect path.
- **All three book formats native** — CBZ + CBR + EPUB, one reader UI, no plugin install.
- **Anime as a typed library** — AniList primary metadata, per-season franchise walk that maps on-disk seasons onto distinct AniList cours, TMDB → TVDB → AniList episode-fallback chain, watching-status mirror. No competitor ships this in core; Plex has nothing, Emby and Jellyfin rely on community plugins.
- **Subtitle OCR in core** — bitmap subtitle streams (PGS / VOBSUB / DVB / XSUB) get Tesseract'd to text WebVTT and persisted; every client gets restyleable, smaller, searchable subs without re-encoding the video. Plex and Emby only do burn-in; Jellyfin needs a community plugin.
- **Trickplay sprite sheets in core** — BIF-shape `xywh`-cued WebVTT thumbnails out of the box, no Plex Pass / Emby Premiere gate.
- **Intro / credits auto-detection in core** — AcoustID fingerprinting + blackdetect, exposed as chapter rows. Plex Pass ships this paid; Emby and Jellyfin lean on community plugins.
- **First-class observability** — OTel tracing, Prometheus, audit log, structured logs with trace IDs, schema-gated readiness probe — without a premium tier.
- **In-app discover + request** — TMDB discover and request workflow ship in the search page; competitors require Overseerr / Ombi / Jellyseerr.
- **User-owned home-video metadata** — edits rename the file on disk and stamp the mtime, so user-supplied titles travel across tools instead of being locked into one app's database.

---

## Where OnScreen trails

Specific competitor named per row. "Nobody has it" doesn't count as a trail.

- **iOS + Apple TV apps** *(vs Plex / Emby / Jellyfin)*. Out of scope until a Swift ramp + App Store review budget land.
- **Tidal / Qobuz integration** *(vs Plex Pass)*. Sized XL — OAuth bind, library import, streaming passthrough, ReplayGain alignment with the local FLAC pipeline, licensing legwork. Track C of the v2.1 roadmap; re-scope decision pending.
- **ML-driven personalised recommendations** *(vs Plex / Emby)*. Item-to-item collaborative filtering shipped and was pulled — the row didn't earn its space; trending row stays. Pgvector embedding pipeline never landed.
- **TV-client hardware soak** *(vs all three)*. Code-complete on every platform; Android TV / Fire TV is hardware-verified. webOS / Tizen / Roku / Android phone need real-device soak before Plex-class confidence.
- **VAAPI hardware encode validation** *(vs Plex / Emby paid tiers; Jellyfin core)*. Three of four encoder families validated on real hardware. VAAPI needs a Linux + non-NVIDIA GPU rig the project doesn't yet have.
- **Adaptive bitrate HLS ladder** *(vs all three)*. OnScreen transcodes a single rendition per session and lets the operator-side bandwidth profile pick. Multi-rendition variant playlists with bandwidth-aware client switching are absent.
- **Cast / AirPlay / DLNA out** *(vs all three)*. None of the three protocols is wired — Cast and AirPlay receivers don't see OnScreen, and there's no UPnP/DLNA server for legacy renderers. Browser-only "play here" today.
- **Mobile offline downloads** *(vs Plex Pass / Emby Premiere / Jellyfin)*. No download-for-offline flow on any client.
- **Sync-watch / watch parties** *(vs Jellyfin SyncPlay)*. Plex retired Watch Together; Emby has nothing native. Jellyfin's SyncPlay is the only differentiated one and OnScreen doesn't match it.
- **Subtitle styling controls** *(vs all three)*. OCR-derived WebVTT is restyleable in principle, but the player UI doesn't expose font / size / colour / outline pickers yet.
- **Sleep timer** *(vs all three)*. Trivial UX feature, easy to add when the player UI gets a polish pass.
- **Last.fm / ListenBrainz scrobbling** *(vs community plugins on the others)*. Listen events live in `watch_events`; a one-way scrobble exporter would close this without much work.

---

## Non-differentiators

Movies / TV / music / photo scanning and metadata enrichment, embedded + disk artwork, TMDB + TVDB + MusicBrainz agents, HLS streaming, direct play, resume position, multi-user, parental content ratings, chapter markers, audit-safe session management, S3 / GCS native libraries (none of the four ship one — all four rely on local or NFS mounts).

---

## See also

- [v2.1-roadmap.md](v2.1-roadmap.md) — full v2.1 track list and current status
- [API.md](../API.md) — REST surface
- [ARCHITECTURE.md](../ARCHITECTURE.md) — design notes
- [CHANGELOG.md](../CHANGELOG.md) — what shipped when
