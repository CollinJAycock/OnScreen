# OnScreen vs Plex / Emby / Jellyfin

**Snapshot:** 2026-05-25 against v2.4.0 (unreleased on `feat/v2.4`; server lock lifted after v2.3.0; the on-demand adaptive-bitrate ladder now carries H.264 / HEVC / AV1 rungs, is capped at the client-requested height, and is multi-instance-safe; a multi-node transcode fleet landed — storage-less workers pull the source from the primary over HTTP, and opt-in Intel QSV hardware HEVC decode was validated end-to-end on an i9-13900HX worker decoding 4K HDR; the fleet dispatcher is now cost-weighted (a 4K stream counts ~4× a 1080p one) and capability-aware (HDR jobs route to a GPU-tonemap node, AV1 output to an AV1-capable node), with per-node load % and a live-tunable embedded session cap in the UI; the admin UI now also owns the cluster-wide System knobs that used to be env-only (server name, retention, TMDB rate, ABR + caps, public asset cache, static-ABR, scanner concurrency, missing-file grace, LAN discovery) and the global Transcode output ceilings, with env vars kept as the initial fallback; HTTPS can be managed entirely from the UI — paste a cert + key in Settings ▸ System and the server serves TLS from an in-memory keypair with no cert file on disk; node- and site-specific config (bind addresses, paths, SITE_ID, QSV decode, embedded-worker role) moved to a per-node `node_settings` store editable in Settings ▸ Nodes, keyed by NODE_ID and merging joined fleet workers into the picker so a freshly-joined worker is selectable before it has a config row; the Prometheus surface was fully instrumented — HTTP requests/latency with chi **route-template** path labels (no per-ID cardinality blow-up), DB query duration by SQL verb via a pgx tracer that wraps the existing OTel one, transcode active gauge + jobs by status, scanner files per library, watch events by type, webhook delivery failures, and hub-cache refresh duration; the Windows MSI installer's worker-only mode now registers an onlogon interactive scheduled task instead of a Windows service so NVENC/QSV can actually reach the GPU; TOTP two-factor auth + a purpose-scoped asset token shipped server-side and across the client fleet; v2.2 anime track landed 2026-05-04; Android TV client on Play Store closed-testing track as of 2026-05-13; Fire TV client live on Amazon Appstore test channel same day; Android phone client submitted to Play Store, in first-review queue; Tizen client hardware-verified on a Samsung QN75Q80B 2022 panel between 2026-05-11 and 2026-05-12, Samsung Apps Store submission package prepared 2026-05-17; Intel Arc QSV + native VAAPI transcode paths both end-to-end-validated 2026-05-21 on an A770 against the full coverage set, including HDR tonemap on every encoder family; `av1_vaapi` and `av1_amf` planner support added the same day, with `av1_vaapi` hardware-validated on both Intel Arc (iHD) and AMD RDNA4 (radeonsi) — `av1_amf` still pending a Windows AMD host).

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
| Hardware encode (VA-API stack, via QSV/libmfx)     |    ✅    |  💎  |  💎  |    ✅    |
| Hardware encode (native ffmpeg `*_vaapi`)          |    ✅    |  💎  |  💎  |    ✅    |
| AV1 encode (NVENC)                                 |    ✅    |  💎  |  💎  |    ⚠     |
| AV1 encode (QSV, Arc / Xe2)                        |    ✅    |  💎  |  ❌  |    ⚠     |
| AV1 encode (native VAAPI — Intel Arc + AMD RDNA4)  |    ✅    |  💎  |  ❌  |    ⚠     |
| AV1 encode (AMF, Windows AMD RDNA3+ only)          |    ⚠     |  💎  |  ❌  |    ⚠     |
| HDR → SDR tonemap                                  |    ✅    |  💎  |  💎  |    ✅    |
| Subtitle burn-in (PGS / VOBSUB)                    |    ✅    |  ✅  |  ✅  |    ✅    |
| Subtitle OCR (PGS / VOBSUB → text WebVTT)          |    ✅    |  ❌  |  ❌  |    ⚠     |
| Trickplay sprite sheets (BIF-shape)                |    ✅    |  💎  |  💎  |    ✅    |
| fMP4 HLS for HEVC + AV1 (vs MPEG-TS)               |    ✅    |  ✅  |  ✅  |    ✅    |
| Adaptive bitrate ladder (multi-rendition HLS)      |    ✅    |  ✅  |  ✅  |    ✅    |
| Multi-worker fleet (separate worker binary)        |    ✅    |  ❌  |  ❌  |    ❌    |
| Storage-less workers (pull source over HTTP)       |    ✅    |  ❌  |  ❌  |    ❌    |
| Cost-weighted fleet dispatch (load-balanced)       |    ✅    |  ❌  |  ❌  |    ❌    |
| Capability-aware routing (HDR / AV1 → capable node)|    ✅    |  ❌  |  ❌  |    ❌    |
| Per-node load % + admin-tunable session cap (live) |    ✅    |  ❌  |  ❌  |    ❌    |
| Hardware decode offload (QSV HEVC, opt-in)         |    ✅    |  ✅  |  ✅  |    ✅    |
| Per-session supersede (one stream per user / item) |    ✅    |  ✅  |  ⚠   |    ⚠     |

Every hardware encoder family — NVENC, QSV, native VAAPI, AMF, plus their HEVC and AV1 variants — is hardware-validated end-to-end against the nine-movie coverage set (H.264 1080p, HEVC 1080p 10-bit, HEVC 4K HDR10, AV1 4K). Four matrix runs across four planner branches and **two independent VAAPI driver stacks** (77/77 transcode-session tests pass total), all 2026-05-21 except where noted: AV1 NVENC on RTX 5080 (2026-04-30); the full QSV family — `h264_qsv` / `hevc_qsv` / `av1_qsv` — and the native `*_vaapi` family — `h264_vaapi` / `hevc_vaapi` / `av1_vaapi` — on an Intel Arc A770 box (iHD driver); the same native `*_vaapi` family again on an **AMD RDNA4 box** (Mesa `radeonsi` driver), confirming `av1_vaapi` on a second, unrelated VAAPI implementation; and AMF (`h264_amf` / `hevc_amf`) forced on the local Windows box. The HDR-to-SDR tonemap chain (zscale-linear → tonemap=hable → zscale bt709 → format=yuv420p) was confirmed firing on every path with `color_transfer=bt709` in the output segments — a fix landed 2026-05-21 to insert the chain BEFORE the VAAPI hwupload, which was previously skipped on the VAAPI branch.

The VA-API row is split because OnScreen carries two distinct paths: the `*_qsv` encoder family (libmfx → VA-API → kernel `i915` → `/dev/dri/renderD128`, Intel-only), and the `*_vaapi` encoder family (ffmpeg's native VAAPI wrappers with `scale_vaapi` filters in a separate planner branch in [`internal/transcode/ffmpeg.go`](../internal/transcode/ffmpeg.go)). Native `*_vaapi` is now validated on both the Intel iHD driver (Arc A770) and the AMD Mesa `radeonsi` driver (RDNA4) — the same planner branch drives both vendors, since on Linux AMD cards expose VAAPI rather than AMF. On Arc, native VAAPI is consistently faster than QSV/libmfx (Dune HDR 1080p first-segment 2.6 s vs 12.9 s, Interstellar 6.2 s vs 15.5 s, GoodFellas 4K HDR 23.0 s vs 34.2 s); RDNA4 is faster still on the heavy 4K-HDR case (GoodFellas 4K HDR ~10 s h264 / ~12 s hevc).

The AMF AV1 branch (`av1_amf`) is still ⚠: AMF is a **Windows-only** AMD encoder API, so a Linux RDNA4 box exercises `av1_vaapi` (radeonsi), not `av1_amf`. Closing the AMF AV1 cell needs an RDNA3+ AMD GPU in a **Windows** host — the planner enum, probe, and filter routing are all wired and the probe correctly excludes it on hardware/OSes that don't expose it (verified on a Ryzen 9900X RDNA2 iGPU, which returned `AMF error 30 — CreateComponent(AMFVideoEncoderHW_AV1) failed`).

The adaptive-bitrate ladder is now ✅ — implemented via the **on-demand model** (Jellyfin-style), not the simultaneous-N-encode `var_stream_map` approach. The parent session runs no ffmpeg: `playlist.m3u8` returns a master listing the ladder, each rung's media playlist is server-predicted from the source duration so all rungs share one segment timeline, and a segment request transcodes that rung on demand — at most one rung encoding at a time, which is what makes it viable on a single GPU. Segment boundaries are frame-quantized to `ceil(i·SegmentDuration·fps)/fps` (exactly where ffmpeg forces a keyframe), so the advertised playlist and the encoded segments stay aligned to <1 frame and seeks land frame-accurately. The ladder now carries **H.264 (`.ts`), HEVC, and AV1 (`.m4s` fMP4)** rungs, is **capped at the client-requested height** (picking 1080p never offers a 2160p rung — which on a heavy 4K HDR source over a fleet link prevents the player from oscillating up to a rung it can't sustain), and the per-rung reachability check is **multi-instance-safe** (worker-side `/seghead` query, not local-disk only). Validated end-to-end on `h264_nvenc` (predicted EXTINF matching the encoder, sub-second forward/backward seeks via prompt rung restart) and exercised on the fleet against a 4K HDR HEVC source decoding on a remote QSV worker. Landing the original fix also surfaced and fixed a latent bug where `-force_key_frames` produced non-IDR frames on NVENC unless `-forced-idr` was set — segments were silently falling back to the `-g` GOP boundary (5 s on a 23.976 fps source instead of 4 s).

The **multi-node transcode fleet** is OnScreen-only (Plex/Emby/Jellyfin transcode in-process on the server). Worker nodes run a separate `worker` binary and join the primary's queue via the shared DATABASE_URL + VALKEY_URL. A worker with **no shared storage** pulls the source from the primary over HTTP — the job carries a `/media/stream/{file_id}` URL with a per-file stream token, and ffmpeg reads it with reconnect + Range support — so the fleet works without an NFS/SMB mount on every node. Each worker resolves the session dir locally and serves its own segments back to the primary's segment proxy. **Intel QSV hardware HEVC decode** is opt-in per worker (`TRANSCODE_QSV_DECODE`): it offloads the 4K HEVC decode to the Intel iGPU while the chosen encoder (NVENC/AMF/software) still does the encode — no cross-GPU surface sharing, since decoded frames download to system memory. It auto-falls-back to software decode if a source fails to init on QSV, so a bad source can't hard-fail playback. Validated 2026-05-23 on an i9-13900HX worker decoding 4K HDR HEVC (Goodfellas) cleanly — the exact source class that hit `-22 EINVAL` on the retired NVDEC/CUDA pipeline, so QSV decode is reliable where NVDEC-HEVC wasn't on mainline ffmpeg.

On the software HDR path the HDR→SDR tonemap chain now runs **after** the downscale (at output resolution, not source), which for a 4K→1080p HDR transcode cuts the dominant CPU stage by ~4× — an i9-13900HX dropped from ~90 % to single-digit CPU on a 1080p rung of a 4K HDR source.

The fleet dispatcher is **cost-weighted and capability-aware**, which is where OnScreen pulls ahead of the very idea of distributed transcoding (Plex/Emby/Jellyfin have no fleet to schedule). Plain round-robin would treat a 4K HDR tonemap and a 480p remux as equal "one session" load; instead each job is scored in centi-units of a 1080p transcode (`JobCostCenti`: 1080p ≈ 100, 4K ≈ 400, remux ≈ 25) and each worker tracks in-flight cost, so the dispatcher routes by **proportional headroom** — a 16-slot node grinding a 4K stream yields the next job to an idle 12-slot node, while two idle nodes still favour the larger. On top of that, workers advertise their GPU HDR→SDR tonemap path (libplacebo/Vulkan) and encoder families, and the dispatcher ranks in strict tiers — GPU availability, then **capability fit for the specific job** (an HDR job prefers a node that can GPU-tonemap rather than fall back to software zscale; an AV1-output job prefers a node with an AV1 encoder), then load — as a soft preference that never strands a job on a node that can't serve it. The fleet settings page surfaces a cost-weighted **load %** per node, an **HDR-tonemap** capability chip, and an **admin-settable Max Sessions** for the embedded worker that applies live (no restart) and persists across restarts. A concurrency load test (12 k dispatch+ack/s, no counter leak under the Incr/Decr race) backs the accounting.

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

Skip Intro / Skip Credits is wired in the web player: a button slides in over the bottom-right corner whenever the playback head is inside an intro / credits region (server-detected, see section 5), `S` is the keyboard shortcut, and a per-browser "Always skip intros" toggle sits right under the button so users discover it the first time it appears. Auto-skip is intro-only — auto-skipping credits would yank the user out of the episode prematurely; that path is handled by the existing auto-next-episode flow with the sleep-timer "end of episode" gate. AirPlay is the remaining real trail here (the ABR ladder landed on `feat/v2.4` — see section 2). Cast / DLNA / SyncPlay show ❌ in the matrix but are not chasing tasks — see "Deferred" near the bottom for why (Android apps + cross-device transfer cover Cast's main use case; DLNA and SyncPlay are permanently scoped out).

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
| Two-factor auth (TOTP, self-hosted local accounts)            |    ✅    |  ⚠   |  ❌  |    ❌    |
| OIDC                                                          |    ✅    |  ❌  |  ❌  |    🧩    |
| OAuth (Google / GitHub / Discord)                             |    ✅    |  ❌  |  ❌  |    ❌    |
| SAML 2.0 SP-initiated SSO                                     |    ✅    |  ❌  |  💎  |    ❌    |
| LDAP (incl. group sync)                                       |    ✅    |  ❌  |  💎  |    🧩    |
| PASETO tokens (over JWT)                                      |    ✅    |  ❌  |  ❌  |    ❌    |
| Per-file streaming token (24h, file_id-bound, purpose-scoped) |    ✅    |  ❌  |  ❌  |    ❌    |
| Purpose-scoped asset token (cross-origin `?token=`, not Bearer-usable) | ✅ | ❌ | ❌ | ❌ |
| Admin-issued invite links (no plex.tv account required)       |    ✅    |  ⚠   |  ✅  |    ❌    |
| PIN-based native client device pairing                        |    ✅    |  ✅  |  ✅  |    ❌    |
| Password reset (email link, expiring token)                   |    ✅    |  ✅  |  ✅  |    ❌    |

OIDC + OAuth + SAML + LDAP are all core, no plugin install. TOTP two-factor is built into the **self-hosted local-account** system — enrolment + QR provisioning and verify-on-login, honored across the web app and every native client — with no dependency on a vendor cloud account (Plex's 2FA exists but is tied to the required plex.tv account; Emby and Jellyfin have no native 2FA). The per-file stream token closes the long-tail "ExoPlayer dies at 1 h on a 90-minute movie" failure — natively-played streams need a longer-lived token than the API access token, and that token must not be repurposable as a Bearer or for a different file. The purpose-scoped **asset token** extends that posture to read-only cross-origin assets (artwork, trickplay, external subtitles, SSE): it authenticates `?token=` URLs that native players and cross-origin clients can't attach a Bearer header to, yet is rejected on the Bearer/general-API path, so a leaked asset URL can't become a general credential.

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

OnScreen's Android TV / Google TV client graduated to the Play Store **closed-testing track on 2026-05-13** (a dedicated TV release lane under the same `tv.onscreen.android` listing the Android phone build uses; Play's `android.software.leanback` feature filter routes the TV-only AAB to TV devices and the Compose phone AAB to phones). The Android phone client itself was submitted the same day and is in the first-review queue. Desktop ships via Tauri 2 with a native Rust audio engine outside the webview. Tizen got its first end-to-end hardware run on 2026-05-11 against a Samsung QN75Q80B (2022) — sideloaded via Samsung partner cert against the bound DUID, with the full surface exercised on the panel (navigation, video / audio / music / photo playback, watch state, library hygiene). The webOS scaffold sits at near-parity in code; real LG hardware soak is the open item. Roku is feature-complete in code; real-device soak likewise pending. Fire TV ships the same Leanback APK as Android TV but through Amazon's separate channel — live on the Amazon Appstore **test channel as of 2026-05-13** (Amazon's analogue to Play's closed testing; production release pending Amazon review). Samsung Apps Store + LG Content Store submission paperwork is the gate between ⚠ and ✅ in the table above. iOS + Apple TV are out of scope until a Swift skill ramp + App Store review budget land.

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

OnScreen ships an OTel + Prometheus + audit-log stack as core; competitors either omit telemetry, gate behind a paid tier, or expect operators to layer it themselves. The Prometheus surface isn't just "there" — it's actually instrumented: HTTP request count + latency with the **chi route template** as the path label (per-ID URLs collapse to one series — no cardinality blow-up), DB query duration by SQL verb via a pgx tracer that wraps the existing OTel one, transcode sessions active (set from the live Valkey index so it's multi-instance- and TTL-correct), transcode jobs by status, scanner files per library, watch events by type, webhook delivery failures, hub-cache refresh duration, and the rate-limiter fail-open counter. The scheduler runs cron-driven admin tasks (scan, EPG refresh, DVR retention, OCR pass, intro detection, refresh missing artwork, dedupe shows / movies, backup) — every task records `last_run_at`, last status, and last error so the admin UI can surface failures without grepping logs. The jobs feed (`GET /jobs`) gives a 30 s-poll snapshot of in-flight scans + missing-art and unmatched-item counts so the home banner can show "scanning…" / "12 items need a poster" without hammering item endpoints. `/debug/pprof` is admin-gated.

---

## 10. Security & privacy

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Secret encryption at rest (AES-256-GCM)            |    ✅    |  ❌  |  ❌  |    ❌    |
| Built-in HTTPS (operator-provided PEM)             |    ✅    |  ❌  |  ❌  |    ✅    |
| Admin-uploaded TLS cert via UI (no file on disk)   |    ✅    |  ❌  |  ❌  |    ❌    |
| Path-traversal hardening on every asset route      |    ✅    |  ✅  |  ✅  |    ✅    |
| Strict CSP + HSTS + X-Frame-DENY + Permissions-Policy | ✅    |  ⚠   |  ⚠   |    ⚠    |
| SSRF-hardened outbound HTTP (loopback / RFC1918 / link-local denied) | ✅ | ❌ | ❌ |  ❌    |
| Rate limiting (per-route, env-overridable)         |    ✅    |  ❌  |  ⚠   |    ⚠    |
| No third-party telemetry / analytics in clients    |    ✅    |  ❌  |  ⚠   |    ✅    |
| Self-hosted account system (no vendor cloud)       |    ✅    |  ❌  |  ✅  |    ✅    |

Plex requires a plex.tv account for sign-in even on a self-hosted server. OnScreen and Jellyfin are fully self-hosted; Emby is mostly self-hosted with optional cloud features.

Outbound metadata + artwork fetches go through a `safehttp` dial policy that rejects loopback / RFC1918 / link-local destinations *post-resolution*, so a malicious or compromised metadata source can't return a URL that pivots the fetch into the operator's internal network. The webview CSP allows only self + inline styles + Cloudflare Insights for the beacon; `script-src` is **nonce-based** (a per-response nonce, no `unsafe-inline`/`unsafe-eval`, no external CDNs), so an injected `<script>` without the request's nonce won't execute. Most competitors set `X-Content-Type-Options` and `X-Frame-Options` but ship without a strict `Content-Security-Policy` or `Permissions-Policy`.

---

## 11. Storage & infrastructure

| Feature                                              | OnScreen   | Plex   | Emby   | Jellyfin |
| ---------------------------------------------------- | :--------: | :----: | :----: | :------: |
| Database                                             | PostgreSQL | SQLite | SQLite |  SQLite  |
| Stateless API tier (horizontally scalable)           |     ✅     |   ❌   |   ❌   |    ❌    |
| Event-sourced watch state (immutable log)            |     ✅     |   ❌   |   ❌   |    ❌    |
| Materialized hub cache                               |     ✅     |   ❌   |   ❌   |    ❌    |
| Single-binary deployment                             |     ✅     |   ✅   |   ✅   |    ✅    |
| Docker / Compose first-class                         |     ✅     |   ✅   |   ✅   |    ✅    |
| Native object storage (S3 / MinIO / B2 / Wasabi / R2)|     ✅     |   ❌   |   ❌   |    ❌    |
| Object-storage write-back (artwork, covers)          |     ✅     |   ❌   |   ❌   |    ❌    |
| CDN offload via signed URLs                           |     ✅     |   ❌   |   ❌   |    ❌    |
| Leader-elected singleton work (auto-failover)        |     ✅     |   ❌   |   ❌   |    ❌    |
| Postgres streaming replication + failover DSN        |     ✅     |   ❌   |   ❌   |    ❌    |
| Valkey/Redis Sentinel HA (lock/session tier)         |     ✅     |   ❌   |   ❌   |    ❌    |
| Static-ABR pre-encode (popular titles → CDN)         |     ✅     |   ❌   |   ❌   |    ❌    |
| Multi-site active/passive DR                          |     ✅     |   ❌   |   ❌   |    ❌    |
| Per-site content addressing (path remap)             |     ✅     |   ❌   |   ❌   |    ❌    |
| Cluster role / replication-lag endpoint              |     ✅     |   ❌   |   ❌   |    ❌    |
| Per-node config from admin UI (paths / bind addrs / GPU toggles) | ✅ | ❌ | ❌ |    ❌    |

PostgreSQL-native is the foundational choice and the structural moat: partitioned `watch_events` tables, tsvector full-text search, materialized hub views, and — because it's Postgres, not SQLite — **streaming replication, a multi-host failover DSN, and multi-site DR are first-class**, which the SQLite-based trio can't do without re-architecting. Storage is pluggable behind a `MediaStore` abstraction: local disk by default, or S3-compatible object storage configured live from the admin UI, with every read **and** write path routing through it and `SignedURL` CDN offload. The other three ship no native S3/GCS layer — operators mount with rclone or similar, and none can pre-encode popular titles to a CDN, fail over the database, or run a warm second site. All HA/multi-site features are opt-in and off by default. See [dr-runbook.md](dr-runbook.md) and [ha-roadmap.md](ha-roadmap.md).

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
- **Multi-node transcode fleet with smart dispatch** — a separate `worker` binary joins the primary's queue and can run on a box with **no shared storage**, pulling the source over HTTP with a per-file token. The dispatcher is **cost-weighted** (a 4K stream counts ~4× a 1080p one, not "one session") and **capability-aware** (HDR jobs route to a GPU-tonemap node, AV1 output to an AV1-capable node), with per-node load % and a live-tunable session cap in the UI. Plex / Emby / Jellyfin all transcode in-process on the single server; none distributes transcoding across nodes, let alone schedules it by per-node cost and capability.
- **Self-hosted local-account 2FA** — TOTP enrolment + verify-on-login across web + native clients, with no vendor-cloud account dependency. Plex's 2FA rides the required plex.tv account; Emby and Jellyfin ship none in core.
- **In-app discover + request with arr fan-out** — TMDB discover, request, admin approval, and Sonarr / Radarr dispatch all ship in core; competitors require Overseerr / Ombi / Jellyseerr.
- **User-owned home-video metadata** — edits rename the file on disk and stamp the mtime, so user-supplied titles travel across tools instead of being locked into one app's database.
- **Watch-status mirror across every type** — Plan to Watch / Watching / Completed / On Hold / Dropped is a generic feature, not anime-only. None of Plex / Emby / Jellyfin carries the equivalent.
- **OpenSubtitles search + download in core** — the player itself drives subtitle search + download with rate-limited per-session quotas, and downloaded files persist as `external_subtitles` rows. Plex sunset its plugin; Emby gates this behind Premiere.
- **Library hygiene trays** — Fix Match (every unmatched row, paged) and Set Poster (TMDB variants + paste-URL fallback with proper 4xx surfacing) are first-class admin pages. Competitors expose per-item match/poster pickers but not bulk-tray surfaces.
- **Embedded lyrics + LRCLIB synced fallback** — USLT / Vorbis lyrics extracted at scan, LRCLIB filled in afterwards; Plexamp gates this behind Plex Pass and the rest are plugin-only.
- **Strict CSP + SSRF-hardened outbound HTTP** — `safehttp` denies post-resolution loopback / RFC1918 / link-local destinations on every metadata fetch; CSP, HSTS, X-Frame-DENY, Permissions-Policy all set out of the box.
- **HA across every tier + multi-site DR** — opt-in (off by default): Valkey Sentinel for the lock/session tier, a multi-host Postgres failover DSN over streaming replication, leader-elected singleton work, and active/passive DR across two sites with per-site content addressing + a `/health/cluster` role/lag surface. This is a *structural* lead — the SQLite-based trio can't stream-replicate the database or run a warm second site without re-architecting. Procedures in [dr-runbook.md](dr-runbook.md).
- **Pluggable object storage with CDN offload** — local disk or S3 / MinIO / Backblaze B2 / Wasabi / Cloudflare R2, set live from the admin UI; reads and writes both route through it and `SignedURL` 302-offloads cacheable bytes to a CDN so they skip the app tier. The other three ship no native object-storage layer (operators rclone-mount).
- **Static-ABR pre-encode** — the most-played titles' ABR ladders are pre-encoded once to object storage and served straight from the CDN, so the live-transcode fleet handles only the cold tail — "scales with cache, not GPUs." No competitor pre-encodes a popularity-driven ladder for CDN serving.
- **UI-managed HTTPS — no cert file on disk** — paste a cert + private key in Settings ▸ System and the server serves TLS from an in-memory `tls.Config`; the key is stored encrypted, validated against the cert on save, and never echoed back. `TLS_CERT_FILE` / `TLS_KEY_FILE` still take precedence when set, so an env-driven deploy isn't disrupted. The competitors that ship built-in HTTPS (Jellyfin, Emby) accept *file paths* in their UI — none accept the PEM content.
- **Per-node config managed from the admin UI** — bind addresses, filesystem paths, `SITE_ID`, Intel QSV decode, and the embedded-worker role are editable **per node** under Settings ▸ Nodes, keyed by NODE_ID and shown alongside joined fleet workers so a freshly-joined node is configurable before it has a row. The competitors expose either one global config UI (Plex / Emby / Jellyfin) or per-server config via static files — none manage *per-node* config in a fleet from a single admin surface.

---

## Where OnScreen trails

Specific competitor named per row. "Nobody has it" doesn't count as a trail.

- **iOS + Apple TV apps** *(vs Plex / Emby / Jellyfin)*. Out of scope until a Swift ramp + App Store review budget land.
- **Tidal / Qobuz integration** *(vs Plex Pass)*. Sized XL — OAuth bind, library import, streaming passthrough, ReplayGain absent on the source side; not a near-term track.
- **ML-driven personalised recommendations** *(vs Plex / Emby)*. Item-to-item collaborative filtering shipped and was pulled — the row didn't earn its space; trending row stays. Pgvector embedding pipeline never landed.
- **TV-client hardware soak** *(vs all three)*. Code-complete on every platform; Android TV is hardware-verified and shipping via Play Store closed testing as of 2026-05-13. Fire TV (same Leanback APK as Android TV) live on Amazon Appstore test channel the same day. Android phone is hardware-verified, submitted to Play and in first-review queue. Tizen is hardware-verified on a 2022 Q80B panel as of 2026-05-12; Samsung Apps Store submission package (manifest + screenshots + listing copy) prepared 2026-05-17 and is the gate to ✅ in the table above. webOS / Roku still need real-device soak before Plex-class confidence.
- **AV1 AMF (Windows) hardware validation** *(vs Plex / Emby paid tiers; Jellyfin partial)*. Planner enum + probe + filter routing all wired for `av1_amf` (commit 2026-05-21); probe correctly excludes the encoder where it isn't available (a Ryzen 9900X RDNA2 iGPU returned `AMF error 30 — CreateComponent(AMFVideoEncoderHW_AV1) failed` on the 1 s probe and the encoder wasn't added). AMF is Windows-only, so the RDNA4 hardware now on hand validates `av1_vaapi` (Linux/radeonsi) but not `av1_amf` — closing this cell needs an RDNA3+ AMD GPU in a Windows host. The Linux/radeonsi AV1 path it would otherwise cover is already ✅.
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
