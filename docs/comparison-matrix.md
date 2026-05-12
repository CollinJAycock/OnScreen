# OnScreen vs Plex / Emby / Jellyfin

**Snapshot:** 2026-05-12 against v2.2.0 on `main` (server lock active; v2.2 anime track landed 2026-05-04; Play Store internal-testing track active for the Android TV client; Tizen client hardware-verified on a Samsung QN75Q80B 2022 panel between 2026-05-11 and 2026-05-12).

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
| Photo geo map view (server `/photos/map` API, bbox-paginated) | ✅ | ⚠   |  ⚠   |    ❌    |
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
| Embedded USLT lyrics + LRCLIB / `.lrc` synced fallback          |    ✅    |  💎  |  🧩  |    🧩    |
| Tidal / Qobuz integration                                       |    ❌    |  💎  |  ❌  |    ❌    |

OnScreen's native desktop client decodes through symphonia 0.5 and writes raw `IAudioClient` in `AUDCLNT_SHAREMODE_EXCLUSIVE` — OS mixer bypassed. Plex Pass ships an exclusive-mode pipeline in Plexamp; the rest layer Roon / Audirvana on top.

Lyrics: scanner extracts ID3 USLT and Vorbis-comment LYRICS frames during the music walk and persists them to `lyrics`. Fallback chain hits LRCLIB for community-sourced synced `.lrc` content when the file ships no embedded text. Plexamp shows lyrics behind the Plex Pass tier; Emby and Jellyfin lean on community plugins.

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
| Watch-status mirror (Plan/Watching/Done/Hold/Drop) |    ✅    |  ❌  |  ❌  |    ❌    |
| Watchlist (persistent, all types)                  |    ✅    |  ⚠   |  ✅  |    ✅    |
| Full-text search w/ library ACL + rating ceiling   |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Sonarr / Radarr request fan-out (admin-approved)   |    ✅    |  🧩  |  ❌  |    🧩    |
| Intro / credits auto-detection (AcoustID-FP)       |    ✅    |  💎  |  🧩  |    🧩    |
| In-app TMDB discover + request                     |    ✅    |  ❌  |  ❌  |    ❌    |
| "Because you watched X" / personalised row         |    ❌    |  ✅  |  ✅  |    ❌    |

OnScreen's home hub serves the request flow inline — no Overseerr / Ombi / Jellyseerr companion needed. Approved requests fan out to configured Sonarr / Radarr instances (per-instance quality profile + root-folder mapping) and the resulting download links back to the originating request when the file lands. The personalised row was scaffolded (item-to-item collaborative filtering) but pulled before release because it didn't earn the home-hub real estate; trending stays. Intro / credits detection runs `fpcalc` (AcoustID) over a 600 s leading window to find the shared intro fingerprint across episodes of a season, plus `ffmpeg blackdetect` over the trailing 360 s for the credits boundary; both are stored as chapter rows and exposed via `GET /items/{id}` so clients can render skip buttons. Plex Pass ships this as "Intro & Credit Markers"; Emby + Jellyfin lean on the community Intro Skipper plugin.

The watch-status mirror (Plan to Watch / Watching / Completed / On Hold / Dropped) shipped with the v2.2 anime track but applies generically to every library type — `(user_id, item_id)` keyed, distinct from playback progress, exposed as filter-able rails on the home hub. None of the three competitors carries the equivalent in core. Search is Postgres `websearch_to_tsquery` over `media_items_fts` with library-ACL pre-filter and per-user content-rating ceiling enforced inside the query — no result-set leakage to filtering layers above.

---

## 6. Playback & client UX

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Skip intro / skip credits button on player         |    ✅    |  💎  |  🧩  |    🧩    |
| OpenSubtitles search + download (in-player)        |    ✅    |  ❌  |  💎  |    🧩    |
| Cross-device "play on…" transfer (own ecosystem)   |    ✅    |  ✅  |  ✅  |    ❌    |
| Sleep timer                                        |    ✅    |  ✅  |  ✅  |    ✅    |
| On-screen subtitle styling (size/color/background/outline) |  ✅ |  ✅  |  ✅  |    ✅    |
| Chromecast / Google Cast                           |    ❌    |  ✅  |  ✅  |    ✅    |
| AirPlay                                            |    ❌    |  ✅  |  ✅  |    ⚠    |
| DLNA / UPnP server                                 |    ❌    |  ✅  |  ✅  |    ✅    |
| Web + desktop file download (server-wide admin toggle, default off) | ✅ | ⚠   |  ⚠   |    ⚠    |
| Mobile offline downloads                           |    ⚠    |  💎  |  💎  |    ✅    |
| Sync watch / watch parties                         |    ❌    |  ❌  |  ❌  |    ✅    |
| Last.fm / ListenBrainz scrobbling                  |    ❌    |  ⚠   |  🧩  |    🧩    |
| Chapter markers + skip targets                     |    ✅    |  ✅  |  ✅  |    ✅    |

Skip Intro / Skip Credits is wired in the web player: a button slides in over the bottom-right corner whenever the playback head is inside an intro / credits region (server-detected, see section 5), `S` is the keyboard shortcut, and a per-browser "Always skip intros" toggle sits right under the button so users discover it the first time it appears. Auto-skip is intro-only — auto-skipping credits would yank the user out of the episode prematurely; that path is handled by the existing auto-next-episode flow with the sleep-timer "end of episode" gate. ABR ladder + AirPlay are real trails. Cast / DLNA / SyncPlay show ❌ in the matrix but are not chasing tasks — see "Deferred" near the bottom for why (Android apps + cross-device transfer cover Cast's main use case; DLNA and SyncPlay are permanently scoped out).

OpenSubtitles search + download is built in: the player UI calls `/items/{id}/subtitles/search` against the OpenSubtitles v1 API, and downloaded `.srt` files are persisted to disk and registered as `external_subtitles` rows so subsequent playback gets text-based subs without re-querying. Per-session rate limit (10 searches/minute, 5 downloads/minute) prevents player retries from blowing the OpenSubtitles per-IP quota. Cross-device transfer (`POST /playback/transfer`) hands a playback state to a named target client by device label — same shape as Plex's "Play on" / Emby's "Remote Control".

Web + desktop file download has a server-wide admin toggle in Settings → General → Sharing, **default off**. Posture is "stream only" out of the box — operators on shared servers don't want guests pulling raw files unintentionally; personal-server operators flip it on. Plex / Emby / Jellyfin all expose this only as a per-user permission (no global kill-switch). Under Tauri the click flows through a `download_to_file` Rust command that opens a native save-as dialog and streams the response with ureq — webview `<a download>` doesn't fire the OS save flow without that bridge.

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
| Admin-issued invite links (no plex.tv account required)       |    ✅    |  ⚠   |  ✅  |    ❌    |
| PIN-based native client device pairing                        |    ✅    |  ✅  |  ✅  |    ❌    |
| Password reset (email link, expiring token)                   |    ✅    |  ✅  |  ✅  |    ❌    |

OIDC + OAuth + SAML + LDAP are all core, no plugin install. The per-file stream token closes the long-tail "ExoPlayer dies at 1 h on a 90-minute movie" failure — natively-played streams need a longer-lived token than the API access token, and that token must not be repurposable as a Bearer or for a different file.

---

## 8. Native clients

Per-platform status. ✅ here means "shipped to a real distribution channel and exercised on hardware"; ⚠ means code-complete but not yet hardware-verified or in soak.

| Platform                       | OnScreen | Plex | Emby | Jellyfin |
| ------------------------------ | :------: | :--: | :--: | :------: |
| Web (browser)                  |    ✅    |  ✅  |  ✅  |    ✅    |
| Desktop (Windows/macOS/Linux)  |    ✅    |  ✅  |  ✅  |    ✅    |
| Android phone                  |    ⚠     |  ✅  |  ✅  |    ✅    |
| Android TV / Google TV         |    ⚠     |  ✅  |  ✅  |    ✅    |
| Fire TV                        |    ⚠     |  ✅  |  ✅  |    ✅    |
| LG webOS                       |    ⚠     |  ✅  |  ✅  |    🧩    |
| Samsung Tizen                  |    ⚠     |  ✅  |  ✅  |    🧩    |
| Roku                           |    ⚠     |  ✅  |  ✅  |    🧩    |
| iOS / iPadOS                   |    ❌    |  ✅  |  ✅  |    ✅    |
| Apple TV                       |    ❌    |  ✅  |  ✅  |    ✅    |

OnScreen's Android TV / Fire TV client is on Play Store internal testing as of 2026-05-04 (graduates to closed → open → production over a 14-day Play-mandated soak). Desktop ships via Tauri 2 with a native Rust audio engine outside the webview. Tizen got its first end-to-end hardware run on 2026-05-11 against a Samsung QN75Q80B (2022) — sideloaded via Samsung partner cert against the bound DUID, with the full surface exercised on the panel (navigation, video / audio / music / photo playback, watch state, library hygiene). The webOS scaffold sits at near-parity in code; real LG hardware soak is the open item. Roku is feature-complete in code; real-device soak likewise pending. Samsung Apps Store + LG Content Store submission paperwork is the gate between ⚠ and ✅ in the table above. iOS + Apple TV are out of scope until a Swift skill ramp + App Store review budget land.

---

## 9. Admin & observability

| Feature                                          | OnScreen | Plex | Emby | Jellyfin  |
| ------------------------------------------------ | :------: | :--: | :--: | :------:  |
| OpenTelemetry tracing (OTLP/gRPC)                |    ✅    |  ❌  |  ❌  |    ❌   |
| Prometheus metrics endpoint                      |    ✅    |  ❌  |  ❌  |    ⚠    |
| Structured JSON logs with trace IDs              |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Audit log of admin / playback / auth events      |    ✅    |  ❌  |  ⚠   |    ⚠    |
| Admin logs API (in-process ring buffer)          |    ✅    |  ❌  |  ❌  |    ❌   |
| `/debug/pprof` (CPU/heap/goroutine/block/mutex)  |    ✅    |  ❌  |  ❌  |    ❌   |
| Scheduled task framework w/ run history + UI     |    ✅    |  ⚠   |  ✅  |    ✅   |
| Background jobs status feed (scans + missing-art)|    ✅    |  ⚠   |  ⚠   |    ⚠    |
| In-app real-time notifications (SSE stream)      |    ✅    |  ✅  |  ✅  |    ⚠    |
| Schema-version-gated `/health/ready`             |    ✅    |  ❌  |  ❌  |    ❌   |
| Backup + restore round-trip (schema-aware)       |    ✅    |  ❌  |  ✅  |    ✅   |
| Admin Settings UI (no XML / JSON config files)   |    ✅    |  ⚠   |  ✅  |    ✅   |

OnScreen ships an OTel + Prometheus + audit-log stack as core; competitors either omit telemetry, gate behind a paid tier, or expect operators to layer it themselves. The scheduler runs cron-driven admin tasks (scan, EPG refresh, DVR retention, OCR pass, intro detection, refresh missing artwork, dedupe shows / movies, backup) — every task records `last_run_at`, last status, and last error so the admin UI can surface failures without grepping logs. The jobs feed (`GET /jobs`) gives a 30 s-poll snapshot of in-flight scans + missing-art and unmatched-item counts so the home banner can show "scanning…" / "12 items need a poster" without hammering item endpoints. `/debug/pprof` is admin-gated.

---

## 10. Security & privacy

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Secret encryption at rest (AES-256-GCM)            |    ✅    |  ❌  |  ❌  |    ❌    |
| Built-in HTTPS (operator-provided PEM)             |    ✅    |  ❌  |  ❌  |    ✅    |
| Path-traversal hardening on every asset route      |    ✅    |  ✅  |  ✅  |    ✅    |
| Strict CSP + HSTS + X-Frame-DENY + Permissions-Policy | ✅    |  ⚠   |  ⚠   |    ⚠    |
| SSRF-hardened outbound HTTP (loopback / RFC1918 / link-local denied) | ✅ | ❌ | ❌ |  ❌    |
| Rate limiting (per-route, env-overridable)         |    ✅    |  ❌  |  ⚠   |    ⚠    |
| No third-party telemetry / analytics in clients    |    ✅    |  ❌  |  ⚠   |    ✅    |
| Self-hosted account system (no vendor cloud)       |    ✅    |  ❌  |  ✅  |    ✅    |

Plex requires a plex.tv account for sign-in even on a self-hosted server. OnScreen and Jellyfin are fully self-hosted; Emby is mostly self-hosted with optional cloud features.

Outbound metadata + artwork fetches go through a `safehttp` dial policy that rejects loopback / RFC1918 / link-local destinations *post-resolution*, so a malicious or compromised metadata source can't return a URL that pivots the fetch into the operator's internal network. The webview CSP allows only self + inline styles + Cloudflare Insights for the beacon; `script-src` excludes `unsafe-eval` and external CDNs. Most competitors set `X-Content-Type-Options` and `X-Frame-Options` but ship without a strict `Content-Security-Policy` or `Permissions-Policy`.

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

## 12. Library hygiene & admin tooling

Operator-facing tools for fixing the metadata that auto-enrichment couldn't get right.

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| NFO sidecar override (`<uniqueid type="tmdb">`)    |    ✅    |  🧩  |  ✅  |    ✅    |
| Per-item Fix Match (search TMDB → apply specific ID) |  ✅    |  ✅  |  ✅  |    ✅    |
| Bulk Fix Match tray (every unmatched row, paged)   |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Set Poster tray (TMDB variants + paste-URL fallback) |  ✅    |  ⚠   |  ⚠   |    ⚠    |
| Refresh-missing-art scheduled sweep                |    ✅    |  ❌  |  ⚠   |    ⚠    |
| `[ReleaseGroup]` prefix auto-strip during scan     |    ✅    |  ⚠   |  ⚠   |    ⚠    |
| Per-item re-enrich (force-reenrich bypass cache)   |    ✅    |  ✅  |  ✅  |    ✅    |
| Title-edit reflects to filesystem (rename + mtime) |    ✅    |  ❌  |  ❌  |    ❌    |

The Fix Match tray (`/admin/items/unmatched`) and Set Poster tray (`/admin/items/missing-art`) are the operator's two main hygiene surfaces: paged lists of every top-level row that auto-enrichment couldn't match (no TMDB / TVDB IDs) or couldn't poster (provider IDs but no `poster_path`). Set Poster supports both TMDB poster-variant picking and paste-a-URL fallback (with direct-image-URL hinting and 4xx surfacing on upstream failures, so operators see "image URL returned HTTP 403" rather than a generic 500). A scheduled `refresh_missing_art` task re-runs every 2 h to catch newly-recovered titles.

---

## 13. Plugins & extensibility

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Webhooks (HMAC-signed, retryable)                  |    ✅    |  ❌  |  ✅  |    ✅    |
| MCP-compatible plugin host (outbound)              |    ✅    |  ❌  |  ❌  |    ❌    |
| In-process plugin host                             |    ❌    |  ❌  |  ✅  |    ✅    |
| Tautulli / Overseerr-shape integration             |    ✅    |  ✅  |  ⚠   |    ⚠    |

OnScreen plugins are MCP servers OnScreen calls out to (outbound MCP). Inbound MCP was rejected as a security stance; the plugin attack surface stays one-way. Webhooks are HMAC-SHA256-signed, retried with exponential backoff, and shaped to drop into existing Overseerr / Tautulli receivers.

---

## 14. License

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
- **First-class observability** — OTel tracing, Prometheus, audit log, structured logs with trace IDs, schema-gated readiness probe, `/debug/pprof` — without a premium tier.
- **In-app discover + request with arr fan-out** — TMDB discover, request, admin approval, and Sonarr / Radarr dispatch all ship in core; competitors require Overseerr / Ombi / Jellyseerr.
- **User-owned home-video metadata** — edits rename the file on disk and stamp the mtime, so user-supplied titles travel across tools instead of being locked into one app's database.
- **Watch-status mirror across every type** — Plan to Watch / Watching / Completed / On Hold / Dropped is a generic feature, not anime-only. None of Plex / Emby / Jellyfin carries the equivalent.
- **OpenSubtitles search + download in core** — the player itself drives subtitle search + download with rate-limited per-session quotas, and downloaded files persist as `external_subtitles` rows. Plex sunset its plugin; Emby gates this behind Premiere.
- **Library hygiene trays** — Fix Match (every unmatched row, paged) and Set Poster (TMDB variants + paste-URL fallback with proper 4xx surfacing) are first-class admin pages. Competitors expose per-item match/poster pickers but not bulk-tray surfaces.
- **Embedded lyrics + LRCLIB synced fallback** — USLT / Vorbis lyrics extracted at scan, LRCLIB filled in afterwards; Plexamp gates this behind Plex Pass and the rest are plugin-only.
- **Strict CSP + SSRF-hardened outbound HTTP** — `safehttp` denies post-resolution loopback / RFC1918 / link-local destinations on every metadata fetch; CSP, HSTS, X-Frame-DENY, Permissions-Policy all set out of the box.

---

## Where OnScreen trails

Specific competitor named per row. "Nobody has it" doesn't count as a trail.

- **iOS + Apple TV apps** *(vs Plex / Emby / Jellyfin)*. Out of scope until a Swift ramp + App Store review budget land.
- **Tidal / Qobuz integration** *(vs Plex Pass)*. Sized XL — OAuth bind, library import, streaming passthrough, ReplayGain absent on the source side; not a near-term track.
- **ML-driven personalised recommendations** *(vs Plex / Emby)*. Item-to-item collaborative filtering shipped and was pulled — the row didn't earn its space; trending row stays. Pgvector embedding pipeline never landed.
- **TV-client hardware soak** *(vs all three)*. Code-complete on every platform; Android TV / Fire TV is hardware-verified, Tizen is hardware-verified on a 2022 Q80B panel as of 2026-05-12 (sideloaded; store submission pending). webOS / Roku / Android phone still need real-device soak before Plex-class confidence.
- **VAAPI hardware encode validation** *(vs Plex / Emby paid tiers; Jellyfin core)*. Three of four encoder families validated on real hardware. VAAPI needs a Linux + non-NVIDIA GPU rig the project doesn't yet have.
- **Adaptive bitrate HLS ladder** *(vs all three)*. OnScreen transcodes a single rendition per session and lets the operator-side bandwidth profile pick. Multi-rendition variant playlists with bandwidth-aware client switching are absent.
- **AirPlay out** *(vs Plex / Emby; partial on Jellyfin)*. No path from web or desktop into the Apple TV / HomePod ecosystem. Cast and DLNA are tracked separately under "Deferred" below — see that section for why those two don't sit here.
- **iOS offline downloads** *(vs Plex Pass / Emby Premiere / Jellyfin)*. The Android phone client (`android_native`) ships a WorkManager-backed download flow with on-device manifest + queueing; iOS is the only portable surface still uncovered (TV / set-top platforms aren't candidates — they sit on the network and never go offline). Web + desktop now ship a "save the original file" download (admin-toggleable, default off), so the laptop-on-a-flight use case is already covered through the browser path.
- **Last.fm / ListenBrainz scrobbling** *(vs community plugins on the others)*. Listen events live in `watch_events`; a one-way scrobble exporter would close this without much work.

---

## Deferred — workaround in place

Features competitors ship that OnScreen doesn't, but where the gap is mitigated by something else we already have. Lower priority than the trails above — they show up as missing rows in the matrix but don't block the use case for users on the OnScreen-native client stack.

- **Chromecast / Google Cast** *(vs all three)*. The phone-to-TV use case Cast was invented for is covered by the OnScreen Android phone + Android TV / Fire TV apps and the cross-device "play on…" transfer (`POST /playback/transfer`). Remaining value: laptop / web → TV without switching devices, older Chromecast dongles plugged into TVs that can't run the Android TV app, and guests with no app installed. Real but narrow — sized at ~2 weeks of focused work for sender SDK + auth-token URL handover + real-hardware soak. Will land when a polish track has the room; the matrix-parity row stays ❌ until then.
- **DLNA / UPnP server** *(vs all three)*. **Permanently scoped out.** DLNA wants progressive MP4 / MPEG-TS streams with DLNA-PN profile tags — a separate muxer path from our HLS pipeline — plus a SOAP ContentDirectory service and per-renderer compatibility quirks (Sony Bravia, Samsung Tizen, Kodi, VLC). The audience is mostly legacy TVs that have already been replaced by Cast / AirPlay / app-store TVs in the modern install base. Sized at ~3 weeks for a working implementation; ROI doesn't justify the surface.
- **Sync-watch / watch parties** *(vs Jellyfin SyncPlay)*. **Permanently scoped out.** Plex retired Watch Together; Emby has nothing native; Jellyfin's SyncPlay exists but the audience is small (mostly anime club / friend-group remote-watch sessions, which Discord screenshare or Watch2Gether already cover). The synchronisation engine, chat overlay, and per-room ACL surface are real engineering with no demand signal from OnScreen users.

---

## Non-differentiators

Movies / TV / music / photo scanning and metadata enrichment, embedded + disk artwork, TMDB + TVDB + MusicBrainz agents, HLS streaming, direct play, resume position, multi-user, parental content ratings, chapter markers, audit-safe session management, S3 / GCS native libraries (none of the four ship one — all four rely on local or NFS mounts).

---

## See also

- [v2.1-roadmap.md](v2.1-roadmap.md) — full v2.1 track list and current status
- [API.md](../API.md) — REST surface
- [ARCHITECTURE.md](../ARCHITECTURE.md) — design notes
- [CHANGELOG.md](../CHANGELOG.md) — what shipped when
