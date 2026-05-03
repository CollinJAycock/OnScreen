# Feature Matrix: OnScreen vs Plex / Emby / Jellyfin

**Scope:** sections 1–14 cover server-side features. Section 15 covers first-party clients — desktop (Tauri shell on OnScreen, Plexamp/Plex HTPC, Emby Theater, Jellyfin Media Player), plus the OnScreen TV apps (Android TV / Fire TV / LG webOS / Roku / Samsung Tizen) and the Android phone app against the corresponding incumbents. iOS phone + Apple TV stay ❌ — no scaffold yet.

**Legend**
- ✅ Supported out of the box
- 💎 Supported but behind a paid tier (Plex Pass / Emby Premiere)
- 🧩 Supported via an official plugin in the vendor's plugin catalog
- ⚠️ Partial — some aspect works but not parity with peers
- ❌ Not supported
- ❓ Unverified / depends on configuration

**Snapshot date:** 2026-05-02. Plex / Emby / Jellyfin rows reflect widely-documented upstream behavior as of that date; premium tiering (Plex Pass / Emby Premiere) and plugin availability change over time.

> **v2.0 shipped, v2.1 in flight.** Cells flipped during v2.0 (music videos, audiobooks, podcasts, CAA fallback, NFO import, lyrics end-to-end, DVR purge, subtitle burn-in, AV1, HEVC on QSV/VAAPI/AMF, SAML, built-in HTTPS) are captured in the **v2 Closed** section below. v2.1 work in progress on `main`: home-video library with auto event-folder collections + ffmpeg frame-extracted posters + on-disk-rename metadata editor, **all three book formats (CBZ + CBR + EPUB)** with epub.js reflowable rendering, smart playlists, trending row, library is_private + auto-grant + per-profile visibility (Track G complete), admin logs API, audiobook embedded-cover serving, **audiobook author/series hierarchy with typed shelf + cross-client author+series detail pages + multi-file resume from saved chapter position + cross-parent dedup + release-group folder-name cleanup**, **in-player audio + subtitle pickers across every client (Android TV, phone, web, webOS, Tizen, Roku) with HLS re-issue for transcode-session audio switching**, per-file streaming token (24 h, file_id-bound, purpose-scoped), three TV clients at usable parity (Android TV / Fire TV verified, LG webOS feature-complete, Roku at flow parity). See [v2.1-roadmap.md](v2.1-roadmap.md) for the full track list.

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
| Audiobooks                 | ✅ | ✅ | ✅ | ✅ | OnScreen: full `book_author → book_series → audiobook → audiobook_chapter` hierarchy with multi-file scan, server-side chapter-boundary resume snap, embedded cover art via `/items/{id}/image` (ffmpeg-extracted, cached). Library top-level renders authors as a typed shelf (mirrors music's artist row). All six clients (web, Android phone + TV, webOS, Tizen, Roku) ship author + series detail pages. |
| Books / comics             | ✅ | ❌ | ⚠️ | ⚠️ | OnScreen: CBZ + CBR + EPUB all shipped in v2.1. CBZ uses stdlib zip; CBR uses pure-Go `nwaples/rardecode/v2`; EPUB renders via epub.js with real reflowable pagination, font sizing, and page-flip. One reader UI dispatches by file extension (cbz/cbr → image-page mode, epub → epub.js). |
| Podcasts                   | ⚠️ | ⚠️ | ❌ | 🧩 | OnScreen: local files + episode UI (commit `a8812ad`, v2.1 polish); RSS subscriptions still deferred |
| Music videos               | ✅ | ✅ | ✅ | ✅ | OnScreen: artist children w/ 16:9 thumbs (commit `3319bd6`) |
| Home videos (separate type)| ✅ | ✅ | ✅ | ✅ | OnScreen: dedicated `home_video` library + date-grouped library page; ffmpeg-extracted frame poster per clip (seek 30s in / midpoint / first-frame ladder by duration); auto event_folder collections (`<root>/<EventName>/<files>` → "Yellowstone 2024" tile under an "Events" shelf); admin pencil-overlay metadata editor that renames the file on disk + sets mtime, so user edits travel with the file across tools |

---

## 2. Transcoding

| Feature                        | OnScreen | Plex | Emby | Jellyfin | Notes |
|--------------------------------|:--:|:--:|:--:|:--:|---|
| H.264 encode (software)        | ✅ | ✅ | ✅ | ✅ | |
| H.264 encode (NVENC)           | ✅ | 💎 | 💎 | ✅ | Plex/Emby HW transcoding is paid. **2026-05-01 v2.1 pipeline change:** dropped `-hwaccel cuda` / `scale_cuda` / `tonemap_cuda` / `tonemap_opencl` after the cuda-frame chain proved fragile on mainline ffmpeg 8.x + recent NVIDIA drivers (Avengers `No decoder surfaces left`, GoodFellas 10-bit Main10 `-22 EINVAL`). Now uses the same uniform software-decode + GPU-encode shape as AMF/QSV. Re-validated by the 11-row live matrix on RTX 5080 — 11/11 ✅ |
| H.264 encode (QSV)             | ✅ | 💎 | 💎 | ✅ | Validated 2026-04-30 (spot-check) and **2026-05-01 (11-row live matrix complete, 11/11 ✅)** on Raptor Lake-HX UHD Graphics (i9-13900HX iGPU). Encoder distribution: 8× h264_qsv, 1× hevc_qsv, 2× copy. Notable: row 5 Dune HDR/DV → 1080p SDR through h264_qsv + zscale (`color_transfer=bt709` from a BT.2020/SMPTE2084/DV source — no `tonemap_qsv` exists in mainline ffmpeg, zscale is the only path); row 7 Chainsaw AV1 video_copy confirms the v2.1 AV1-source-remux fMP4 fix on QSV; row 10 GoodFellas 4K HEVC HDR10 → 4K HEVC SDR was the live confirmation of the `BestHEVCEncoder`/`HasHEVCEncoder` selector fix in commit `6a29fc3` |
| H.264 encode (VAAPI)           | ⚠️ | 💎 | 💎 | ✅ | OnScreen: encoder path shipped + auto-detected; awaiting hardware validation on a Linux/VAAPI host |
| H.264 encode (AMF)             | ✅ | 💎 | 💎 | ✅ | Validated 2026-04-30 on Ryzen 9900X iGPU (RDNA 2, VCN 3) end-to-end including HDR HEVC source → zscale tonemap → AMF encode. **Re-validated 2026-05-01** via the 11-row live matrix — 11/11 ✅, encoder distribution: 7× h264_amf, 1× hevc_amf, 3× copy. Notable: case 5 Dune HDR/DV → 1080p SDR via h264_amf + zscale chain, 3.7s playlist; case 10 GoodFellas 4K HEVC AMF needed 20.8s for first segment (real iGPU 4K-HEVC startup cost) |
| H.264 encode (VideoToolbox)    | ❌ | 💎 | 💎 | ✅ | macOS/Apple Silicon only |
| HEVC encode (NVENC)            | ✅ | 💎 | 💎 | ✅ | Validated 2026-04-30 on RTX 5080 (Windows dev box) and on RTX 5000 in production via TrueNAS Docker deploy (Linux). **2026-05-01:** the source that previously failed `-22 EINVAL` on the cuda-frame chain (GoodFellas, 4K HEVC HDR10, 10-bit Main10) now produces valid 4K HEVC SDR fMP4/hvc1 segments under the uniform software-decode pipeline. |
| HEVC encode (software)         | ✅ | 💎 | 💎 | ✅ | libx265. **2026-05-01 v2.1 fix:** dropped `-level-idc 150` (an `hevc_nvenc`-specific option name); libx265 returned `Unrecognized option 'level-idc'` and refused to start. Pre-fix, anyone setting `TRANSCODE_ENCODERS=libx265` hit immediate failure |
| HEVC encode (QSV)              | ✅ | 💎 | 💎 | ✅ | Validated end-to-end through the OnScreen worker on Raptor Lake-HX UHD Graphics (i9-13900HX iGPU): GoodFellas 4K HEVC HDR10 (Main10) → 4K HEVC SDR via zscale tonemap → hevc_qsv with fMP4 + `hvc1` tag (row 10 of the 2026-05-01 11-row live matrix). Required a one-line fix to `BestHEVCEncoder`/`HasHEVCEncoder` (commit `6a29fc3`) so they recognise non-NVENC HEVC variants — pre-fix, the worker fell back to h264_qsv on the 4K-prefer-HEVC path |
| HEVC encode (VAAPI)            | ⚠️ | 💎 | 💎 | ✅ | OnScreen: encoder path shipped + auto-detected (commit `652b87e`); awaiting hardware validation on a Linux host. Last encoder family still pending |
| HEVC encode (AMF)              | ✅ | 💎 | 💎 | ✅ | Validated 2026-04-30 on Ryzen 9900X iGPU (auto-selected via embedded-worker device picker), re-validated in the 2026-05-01 11-row live matrix on row 10: GoodFellas 4K HEVC HDR10 → 4K HEVC SDR via zscale tonemap → hevc_amf, fMP4 + `hvc1` tag, decoded segment is hevc Main 3840×2160 yuv420p with `color_transfer=bt709`. Playlist responded in 20.8s on the iGPU (real 4K-HEVC startup cost, not a correctness issue) |
| AV1 encode                     | ✅ | 💎 | 💎 | ⚠️ | OnScreen: SVT-AV1 SW + AV1 NVENC + AV1 QSV paths (commit `652b87e`); SVT-AV1 preset 8 for live. AV1 NVENC validated 2026-04-30 on RTX 5080 against a real 4K AV1 anamorphic source — hardware decode (NVDEC AV1) → tonemap → AV1 encode round-trips end-to-end. **2026-05-01 v2.1 fixes:** (a) AV1 source remux (`-c:v copy` from an AV1 file) was crashing the mpegts muxer (`Could not find tag for codec av1`) — now switches the HLS muxer to fMP4 with `-tag:v av01` and stamps `AV1Output=true` on the session. Validated end-to-end on Chainsaw Man 4K AV1 anamorphic (yuv420p10le) — playlist responds in 485ms, init.mp4 + .m4s segments decode clean. (b) libsvtav1 was rejecting `-maxrate` outside CRF mode (`Max Bitrate only supported with CRF mode`) — skip the maxrate clamp for the SW AV1 path; pre-fix, anyone setting `TRANSCODE_ENCODERS=libsvtav1` hit immediate failure. **AV1 QSV hardware-gated on Intel Arc / Xe2** (Alchemist/Battlemage discrete or Lunar/Meteor/Arrow Lake iGPU); pre-Arc Intel iGPUs have AV1 *decode* but no encode block — confirmed 2026-04-30 on Raptor Lake-HX UHD Graphics where `av1_qsv` runtime returns `MFX -40 "Current codec type is unsupported"` even though the encoder is compiled into the ffmpeg build. DetectEncoders correctly excludes it from the active list when the probe fails |
| HDR → SDR tone mapping (GPU)   | ✅ | 💎 | 💎 | ✅ | OnScreen: **uniform zscale chain across NVENC, AMF, QSV, software** as of v2.1 (`zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p`). Earlier `tonemap_cuda → tonemap_opencl → zscale` ladder retired 2026-05-01 — the cuda-frame pipeline that fed those filters proved fragile across mainline ffmpeg 8.x + driver versions. Validated on Dune (HDR/DV) → 1080p SDR through both NVENC and AMF, plus GoodFellas (HDR10) → 4K HEVC SDR through hevc_nvenc and hevc_amf |
| 10-bit HEVC source handling    | ✅ | ✅ | ✅ | ✅ | **2026-05-01 v2.1 fix:** libx264 was inheriting 10-bit `pix_fmt` from AV1 / 10-bit-anime sources and emitting High 10 profile H.264, which Chromium can't decode. Extended the existing `format=yuv420p` strip from AMF/QSV/NVENC to libx264; libx265 / libsvtav1 still allow 10-bit (Main10 / AV1 10-bit are valid for HEVC/AV1 clients) |
| Subtitle burn-in                | ✅ | ✅ | ✅ | ✅ | OnScreen: software-encode only (commit `652b87e`); HW path skipped to preserve GPU throughput. **2026-05-01 v2.1 fix:** `subtitleBurnFilter` was crashing on Windows paths — the filter parser stripped backslashes as escape introducers and treated the drive-letter colon (`C:`) as a key=value separator, producing `Unable to parse 'original_size' option value 'moviesGoodFellas (1990)…'`. Fix: convert backslashes to forward slashes + escape every colon. ffmpeg's `subtitles` filter only accepts text-based streams (subrip/ass); bitmap PGS burn-in still requires a separate `overlay` filter chain (not implemented) |
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
| Bit-perfect playback           | ⚠️ | ✅ | ✅ | ⚠️ | OnScreen: shipped on Windows via the Tauri native engine (raw `wasapi` IAudioClient in `AUDCLNT_SHAREMODE_EXCLUSIVE` — OS mixer bypassed, samples reach DAC at file's native bit-depth + rate). macOS HOG mode + Linux ALSA `hw:` deferred. Browser path stays mixer-resampled (Web Audio constraint); the native client is the bit-perfect path. |
| Gapless playback               | ✅ | ✅ | ✅ | ✅ | OnScreen: native engine promotes a preloaded ringbuf consumer into the new active stream (sub-frame transition); web client uses dual-`<audio>` rotation (commit `55612c8`) |
| DSD (DoP) support              | ✅ | ❌ | ❌ | ❌ | OnScreen: DSF parser + DoP packer in the native engine (DSD64/128/256 → 16 DSD samples per channel pack into a 24-bit PCM frame at sr/16). Compatible DACs see the DoP marker bytes and decode the original DSD; non-DoP DACs play the carrier as harmless low-level noise. Routes through the same WASAPI exclusive path. |
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
| DASH streaming                 | ❌ | ✅ | ✅ | ✅ | OnScreen is HLS-only by design — same posture as Jellyfin (which tried DASH and reverted) and Plex (HLS-only for browser playback). Single-rendition transcode on demand maps cleanly onto hls.js's growing-playlist semantics; HEVC ships in fMP4 segments via HLS. Server-side DASH was built during v2.1-flight (Track H) and ripped out 2026-04-30 once the cost/benefit math was honest. |
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
| Native audio engine (out-of-webview) | ✅ | ✅ | ✅ | ⚠️ | OnScreen: symphonia decoder + raw `wasapi` output over a lock-free SPSC ringbuf. cpal stays as the macOS/Linux fallback until per-platform exclusive backends land. JMP defers to mpv. |
| Bit-perfect / WASAPI exclusive (Windows) | ✅ | ✅ | ✅ | ✅ | OnScreen: raw `wasapi` IAudioClient in `AUDCLNT_SHAREMODE_EXCLUSIVE`, event-driven on a dedicated thread. Shared-mode path uses `AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM` so any file rate plays even when device mix-format differs (cpal's WASAPI shared rejects non-mix rates outright; the autoconvert flag is what unblocks 24/192 on a 48 kHz-default device). Settings surface a "Currently: WASAPI exclusive · bit-perfect" badge so users know which path engaged. |
| CoreAudio HOG mode (macOS)      | ❌ | ✅ | ⚠️ | ✅ | Deferred — Windows shipped first. macOS = `kAudioDevicePropertyHogMode`, ~250 LOC mirroring `windows_exclusive.rs`. |
| ALSA `hw:` device selection (Linux) | ❌ | ✅ | ⚠️ | ✅ | Deferred. Linux ALSA `hw:` device + tuned period_size, same shape as the macOS lift. |
| Gapless playback                | ✅ | ✅ | ✅ | ✅ | OnScreen native: preload slot promotes the decoder's ringbuf consumer into the next active stream (sub-frame transition validated against pre-cut tracks); web client uses dual-`<audio>` rotation |
| Native FLAC decode              | ✅ | ✅ | ✅ | ✅ | OnScreen: symphonia 0.5 (FLAC bundle). Migrated from claxon for SEEKTABLE-driven seek — see scrub row. |
| Native ALAC / WAV / AIFF decode | ✅ | ✅ | ✅ | ✅ | OnScreen: symphonia handles the entire lossless catalog through one pipeline (FLAC/ALAC-in-MP4/WAV/AIFF). |
| DSD (DoP) playback              | ✅ | ⚠️ | ❌ | ⚠️ | OnScreen: native DSF parser + DoP packer (16 DSD samples per channel → one 24-bit PCM frame at sr/16). Compatible DACs decode the DoP marker bytes back to DSD; routes through the same WASAPI exclusive path. Plexamp does DoP for compatible DACs; JMP via mpv. |
| ReplayGain enforced client-side | ✅ | ✅ | ✅ | ✅ | OnScreen: native engine reads `REPLAYGAIN_TRACK_GAIN/PEAK` + `REPLAYGAIN_ALBUM_GAIN/PEAK` from symphonia metadata, applies the gain factor in the decoder thread (track / album / off mode + ±15 dB preamp via `/native/audio` settings page) |
| Per-device output picker        | ✅ | ✅ | ✅ | ✅ | OnScreen: cpal device enum + diagnostic test-tone page |
| Hi-res / sample-rate switching  | ✅ | ✅ | ✅ | ✅ | OnScreen exclusive mode opens IAudioClient at the file's exact rate (192/96/48 kHz, 24/16-bit) — DAC sees the source rate. Shared-mode path uses AUTOCONVERTPCM so the OS engine's SRC handles cross-rate adaptation without rejecting the format. |
| Mid-track scrubbing on hi-res   | ✅ | ✅ | ✅ | ✅ | OnScreen: symphonia's FLAC demuxer binary-searches the SEEKTABLE block, then the HTTP body is satisfied via `Range: bytes=N-` against an `HttpSeekableSource` that implements `Read + Seek + MediaSource`. Sub-200 ms even on 24/192 content; the prior claxon path took ~6 s for a 60 s seek (decode-bound). |
| Network resilience (mid-track)  | ✅ | ✅ | ✅ | ✅ | OnScreen: `HttpSeekableSource` recovers a dropped socket by reopening with `Range: bytes={offset}-` from the byte where the read died — survives proxy idle closes, Cloudflare Tunnel keepalive lapses, NAT timeouts. The decoder thread never sees the discontinuity. |

### 15c. Cross-device + power-user

| Feature                         | OnScreen | Plex (Plexamp/HTPC) | Emby Theater | Jellyfin (JMP) | Notes |
|---------------------------------|:--:|:--:|:--:|:--:|---|
| OS media keys (Play/Pause/Next/Prev) | ✅ | ✅ | ✅ | ✅ | OnScreen: `tauri-plugin-global-shortcut`, system-wide |
| System tray (background play)   | ✅ | ✅ | ✅ | ⚠️ | OnScreen: tray menu for Show/Transport/Quit |
| Native OS notifications         | ✅ | ✅ | ✅ | ⚠️ | OnScreen: now-playing on track change, gated on window blur |
| OS now-playing widget (SMTC/MPRIS/MediaPlayer) | ✅ | ✅ | ✅ | ⚠️ | OnScreen: `souvlaki` 0.8 abstracts SMTC (Windows) / MPRIS (Linux) / MediaPlayer (macOS); track metadata + cover art + transport buttons sync from the player on every state change. |
| Secure credential storage       | ✅ | ✅ | ✅ | ⚠️ | OnScreen: Windows Credential Manager / macOS Keychain / Linux Secret Service via `keyring 3.x` |
| Cross-device resume sync (push) | ✅ | ✅ | ✅ | ⚠️ | OnScreen: SSE `progress.updated` broadcast + watch-page consumer; Jellyfin polls |
| "Play on this device" remote control | ✅ | ✅ | ✅ | ⚠️ | OnScreen: pick another logged-in device from the now-playing transfer menu; the target picks up the queue + position via the existing SSE channel and starts playback. Native client surfaces a friendly device label ("Desktop — Chrome on Windows" / "Web — Safari on macOS") so the picker isn't a list of UUIDs. |
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
| Native Android phone app        | ⚠️ | ✅ | ✅ | ✅ | OnScreen: [`clients/android_native/`](../clients/android_native/) — Kotlin + Jetpack Compose + Material 3 + Hilt + Retrofit, package `tv.onscreen.mobile`. Reuses the TV client's data layer verbatim (Retrofit/Moshi API, AuthInterceptor + TokenAuthenticator, ServerPrefs DataStore, all repos). UI: pairing-PIN sign-in (password fallback), hub with poster strips + library list, library grid, item detail, search with debounce, favorites + history + collections drill-in + photo viewer + downloads screen, search with type-filter chips, audiobook chapter list, **author + series detail screens** (book_author / book_series routed via dedicated AuthorScreen + SeriesScreen with the bucketed series-then-books grid). Player: Media3 ExoPlayer with direct/remux/transcode negotiation (port of TV `PlaybackHelper`), per-file 24h stream token, audio + subtitle pickers (HLS audio re-issues the session with a new audio_stream_index), in-player OpenSubtitles search + download, 10s progress reporting, skip-intro/credits overlay, Up Next + music auto-advance (cross-album fall-through via `NextSiblingResolver`), **offline-first short-circuit to a local copy when a completed download exists**, **picture-in-picture for video**, **background audio via `OnScreenMediaSessionService`** so backing out of the player doesn't kill the music, **cross-device resume sync** consuming `progress.updated` SSE events. Favorite-toggle in the detail page TopAppBar with optimistic flip. WorkManager + Hilt-injected worker + JSON manifest store on disk. 46 unit tests cover the playback decision matrix, pairing flow, transcode session lifecycle, progress reporting, search-filter logic, OpenSubtitles search/download, cross-device SSE resume + same-device echo dedupe, and `NextSiblingResolver`'s in-container/cross-container/last-of-last paths. Outstanding: real-hardware validation. |
| Download for offline playback (mobile) | ✅ | 💎 | 💎 | ⚠️ | OnScreen: WorkManager-driven downloads on the Android phone client; player short-circuits to the local file. Free, not paywalled. Jellyfin: Finamp does music-only |
| CarPlay / Android Auto          | ❌ | ✅ | ❌ | ❌ | Plexamp only |

### 15e. TV-app architecture (OnScreen scaffolds)

| Decision                        | Android TV scaffold | Android phone scaffold | webOS scaffold | Tizen scaffold | Roku scaffold | Rationale |
|---------------------------------|--------------------|------------------------|---------------|----------------|---------------|-----------|
| Language / framework            | Kotlin + AndroidX Leanback | Kotlin + Jetpack Compose + Material 3 | SvelteKit SPA | SvelteKit SPA | BrightScript + SceneGraph | Phone client deliberately picks Compose over the TV's Leanback so touch + gesture + insets aren't fighting a remote-first framework; data layer is shared verbatim across the two Kotlin modules |
| Video player                    | Media3 ExoPlayer (HLS) | Media3 ExoPlayer (HLS) | HTML5 `<video>` + hls.js | Tizen AVPlay JS API (HW HLS/MP4 + HEVC/AV1) | Firmware Video node (HLS + MP4) | The native option on each platform; AVPlay is the audiophile-pillar equivalent for video on Samsung — firmware decoders for HEVC/AV1, native 4K + HDR pipeline. OnScreen is HLS-only — DASH support in these frameworks is unused. |
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
| Author + series detail pages     | ✅ | ❌ | ❓ | ❓ | OnScreen: book_author / book_series shelf via DetailFragment — series alphabetical, standalone books year-desc, both surfaces drill into a books grid |
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
| Search type-filter chips         | ✅ | ❌ | ❌ | ❌ | Movies / TV Shows / Episodes / Music chips, persisted via DataStore — same shape as the web/TV/webOS/Roku/Tizen filter |
| Item detail page                 | ✅ | ✅ | ✅ | ✅ | OnScreen: title + year + summary + Play + Download + favorite-toggle |
| Favorites                        | ✅ | ✅ | ✅ | ✅ | OnScreen: list view + heart-icon toggle in detail TopAppBar (optimistic flip) |
| History                          | ✅ | ✅ | ✅ | ✅ | |
| Collections drill-in             | ✅ | ✅ | ✅ | ✅ | |
| Photo viewer                     | ✅ | ✅ | ✅ | ✅ | OnScreen: HorizontalPager with sibling resolution (parent-album-first → paginated library scan fallback), `/items/{id}/image?w=1920&h=1080` |
| Audiobook chapter list           | ✅ | ❌ | ⚠️ | ⚠️ | Renders embedded chapter table on the audiobook detail page |
| Author + series detail pages     | ✅ | ❌ | ❓ | ❓ | Dedicated AuthorScreen + SeriesScreen on their own /author/{id} + /series/{id} routes; ItemDetailScreen redirects book_author / book_series types so library cards land on the right page |
| Direct play                      | ✅ | ✅ | ✅ | ✅ | |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ✅ | Port of TV client's `PlaybackHelper.decide()` matrix |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker (HLS re-issue)| ✅ | ❓ | ❓ | ❓ | |
| Subtitle picker                  | ✅ | ✅ | ✅ | ✅ | |
| In-player OpenSubtitles search   | ✅ | ❌ | ⚠️ | 🧩 | Search by lang + optional title; download attaches to the active file's media_files row |
| Skip-intro / skip-credits        | ✅ | 💎 | ✅ | 🧩 | |
| Up Next overlay                  | ✅ | ✅ | ✅ | ✅ | |
| Music auto-advance               | ✅ | ✅ | ✅ | ✅ | Cross-album fall-through via `NextSiblingResolver` |
| Picture-in-picture (video)       | ✅ | ✅ | ❓ | ❓ | Standard `enterPictureInPictureMode` at 16:9; gated on hasVideo so audio-only items don't surface a useless button |
| Cross-device resume sync         | ✅ | ✅ | ✅ | ⚠️ | SSE `progress.updated` consumer in PlayerViewModel; same-device echoes within 3 s of the last local report are dropped to avoid self-fight |
| Background audio + lock-screen controls | ✅ | ✅ | ✅ | ✅ | Media3 `MediaSessionService` parks the audio player on screen exit; progress reporter + auto-advance keep running. System media-session controls (Bluetooth, lock screen, Now Playing) come for free |
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
| Author + series detail pages     | ✅ | ❌ | ❓ | ❓ | /item/{id} renders book_author / book_series via the same SvelteKit detail page; books grid sorted client-side (series alphabetical, books year-desc) |
| Direct play                      | ✅ | ✅ | ✅ | ⚠️ | |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ⚠️ | hls.js + HTML5 `<video>` |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker (HLS re-issue)| ✅ | ✅ | ✅ | ⚠️ | Yellow remote key opens picker; selection re-issues the transcode session at the current position with the new `audio_stream_index` |
| Subtitle picker                  | ✅ | ✅ | ✅ | ⚠️ | Blue remote key; toggles via `video.textTracks[i].mode` against the WebVTT lanes hls.js exposes |
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
| Author + series detail pages     | ✅ | ❌ | ❓ | ❓ | Bulk-shared with the webOS /item/{id} detail page (Tizen is a port from webOS); same client-side sort for the author bucket |
| Direct play                      | ✅ | ✅ | ✅ | ⚠️ | AVPlay JS API HW path |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ⚠️ | AVPlay HLS + HTML5 `<video>` fallback |
| HW HEVC / AV1 decode             | ✅ | ✅ | ✅ | ⚠️ | Firmware decoders via AVPlay. NVDEC HEVC + AV1 validated 2026-04-30 on RTX 5080 (Goodfellas 4K HDR HEVC + Chainsaw Man 4K AV1 anamorphic) end-to-end through the transcode pipeline. AMD VCN (iGPU) decode not yet exercised — software AV1 decode worked on the AMF path. |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker (HLS re-issue)| ✅ | ✅ | ✅ | ⚠️ | Yellow remote key; AVPlay + HTML5 fallback both rebuild the player on a fresh transcode session at the current position |
| Subtitle picker                  | ✅ | ✅ | ✅ | ⚠️ | Blue remote key; AVPlay path uses `webapis.avplay.setSelectTrack('TEXT', i)`, dev fallback uses `video.textTracks` |
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
| Audiobook (chapter list + author / series detail) | ✅ | ❌ | ⚠️ | ⚠️ | Full hierarchy: book_author + book_series routed through DetailScene from all 5 source scenes (HomeScene, CollectionScene, FavoritesScene, HistoryScene, SearchScene). DetailScene branches onChildSelected to drill into nested parents (audiobook under series, series under author) instead of mis-playing them. roArray.sortBy + manual reverse for the year-desc author bucket since BrightScript has no closure comparator |
| Direct play                      | ✅ | ✅ | ✅ | ⚠️ | Firmware Video node |
| HLS transcode negotiation        | ✅ | ✅ | ✅ | ⚠️ | `Playback_Decide` three-mode split, 13 brs unit tests |
| Per-file 24h stream token        | ✅ | ❌ | ❌ | ❌ | |
| Audio track picker (HLS re-issue)| ✅ | ✅ | ✅ | ⚠️ | * (options) toggles a unified picker through audio → subtitle → closed; LabelList navigation. TranscodeStartTask gains `audioStreamIndex` + `positionMs` interface fields; selection fires a new task that rebuilds the Video node's content with the fresh playlist URL |
| Subtitle picker                  | ✅ | ✅ | ✅ | ⚠️ | Same * picker; selection maps the picked index to `availableSubtitleTracks[i].TrackName` and assigns `video.subtitleTrack`. Off row clears it |
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
| Native audio engine              | ✅ | ✅ | ✅ | ⚠️ | OnScreen: symphonia + raw `wasapi` over a lock-free SPSC ringbuf; JMP: mpv |
| WASAPI exclusive (Win)           | ✅ | ✅ | ✅ | ✅ | OnScreen: raw `wasapi` IAudioClient in `AUDCLNT_SHAREMODE_EXCLUSIVE`; shared-mode path uses `AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM` for non-mix rates |
| CoreAudio HOG (macOS)            | ❌ | ✅ | ⚠️ | ✅ | Deferred — Windows shipped first |
| ALSA `hw:` (Linux)               | ❌ | ✅ | ⚠️ | ✅ | Deferred |
| Gapless playback                 | ✅ | ✅ | ✅ | ✅ | |
| Native FLAC                      | ✅ | ✅ | ✅ | ✅ | symphonia (migrated from claxon for SEEKTABLE-driven seek) |
| Native ALAC / WAV / AIFF         | ✅ | ✅ | ✅ | ✅ | symphonia handles the entire lossless catalog through one pipeline |
| DSD (DoP)                        | ✅ | ⚠️ | ❌ | ⚠️ | OnScreen: native DSF parser + DoP packer routed through WASAPI exclusive |
| ReplayGain enforcement           | ✅ | ✅ | ✅ | ✅ | OnScreen: track / album / off mode + ±15 dB preamp via `/native/audio` |
| Mid-track scrub on hi-res        | ✅ | ✅ | ✅ | ✅ | symphonia SEEKTABLE binary search + HTTP Range; sub-200 ms on 24/192 |
| OS media keys                    | ✅ | ✅ | ✅ | ✅ | |
| System tray                      | ✅ | ✅ | ✅ | ⚠️ | |
| Native OS notifications          | ✅ | ✅ | ✅ | ⚠️ | |
| OS now-playing widget (SMTC/MPRIS)| ✅ | ✅ | ✅ | ⚠️ | `souvlaki` 0.8 — SMTC / MPRIS / MediaPlayer |
| Secure credential storage        | ✅ | ✅ | ✅ | ⚠️ | Keychain / Cred Mgr / Secret Service |
| Cross-device resume sync (SSE)   | ✅ | ✅ | ✅ | ⚠️ | |
| "Play on this device" remote     | ✅ | ✅ | ✅ | ⚠️ | OnScreen: now-playing transfer menu picks another logged-in device, target picks up via SSE |
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
| Author + series detail pages     | ✅ | ❌ | ❓ | ❓ | Dedicated `/authors/{id}` + `/series/{id}` SvelteKit routes; library page renders authors as round portraits at the top level (typed shelf, mirrors the music artist row) |
| Music: Lossless + hi-res badges  | ✅ | ⚠️ | ❌ | ⚠️ | |
| Music: Synced lyrics (USLT/.lrc/LRCLIB) | ✅ | ✅ | ✅ | ✅ | |
| Music: Gapless via dual-`<audio>`| ✅ | ✅ | ✅ | ✅ | |
| Direct play (raw file + range)   | ✅ | ✅ | ✅ | ✅ | |
| HLS transcode                    | ✅ | ✅ | ✅ | ✅ | HLS-only by design (see DASH row in §3) |
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
- **Native bit-perfect audio engine** (Windows): WASAPI exclusive (raw `IAudioClient` in `AUDCLNT_SHAREMODE_EXCLUSIVE`, OS mixer bypassed) + DSD-via-DoP + ReplayGain enforcement + symphonia-backed FLAC/ALAC/WAV/AIFF with SEEKTABLE-driven mid-track scrub. Plex/Emby/Jellyfin don't ship a bit-perfect path on any OS — audiophile users layer Roon/Audirvana on top. macOS HOG + Linux ALSA `hw:` are roadmap'd to mirror this; Windows is shipped today.
- **All three book formats native** (CBZ + CBR + EPUB): Plex doesn't do books at all; Emby/Jellyfin handle some via plugin (Komga / similar) with mixed format coverage. OnScreen ships native scanners for all three plus a single browser reader that dispatches by extension (image-page mode for cbz/cbr, epub.js with reflowable pagination + font sizing + page-flip for epub) — no plugin install or external app required.
- **User-owned home-video metadata** (rename file + set mtime, not just DB): home_video has no external metadata source, so the user owns title / summary / date outright. The pencil-overlay editor renames the file on disk to match the new title and stamps the mtime — edits travel with the file across tools (Finder, Photos.app, Plex/Jellyfin scanners) instead of being locked into the OnScreen DB. Plex/Emby/Jellyfin keep edits in their own metadata stores; if you migrate, you lose them.

## Where OnScreen Trails (as of 2026-04-30)

Each entry names the specific competitor we're behind. "We don't have feature X but neither does anyone else" isn't a trail — those moved to Non-Differentiators.

- **iOS + Apple TV apps** *(vs Plex, Emby, Jellyfin — all three ship native apps)*. We don't yet. Closes the "every couch-platform" parity story, but blocked on a Swift skill ramp + App Store review the project hasn't budgeted.
- **Tidal / Qobuz integration** *(vs Plex Pass — Plex is the only one of the four that has this)*. Track C of the v2.1 roadmap; sized XL (OAuth bind, library import, streaming passthrough, ReplayGain alignment with the local FLAC pipeline, licensing legwork). Re-scope decision still pending.
- **ML-driven recommendations** *(vs Plex + Emby — both ship "Because you watched X"-style rows; Jellyfin doesn't)*. The v2.1 pgvector embedding pipeline never landed; a watch-cooccurrence implementation was built and pulled because the home-hub row didn't earn its space. Trending + smart playlists shipped (Track F, validated); the personalised tier didn't.
- **TV-client hardware validation** *(vs all three competitors — they ship polished, soaked apps for these surfaces)*. Code-complete:
  - **Android TV / Fire TV** (Leanback + Media3): ✅ hardware-verified, no gap.
  - **LG webOS, Samsung Tizen, Roku, Android phone**: full feature parity in code (pairing → hub → search → playback → audio/subtitle pickers → cross-device resume → audiobook/photo features). Real-hardware soak still outstanding; web frontend is the fallback.
- **Hardware encoder validation on Linux VAAPI** *(vs Plex/Emby paid tiers, which exercise this continuously)*. Three of four vendors now validated against the same 11-row live-API path matrix (real ffmpeg, real fixtures, ffprobe-on-decoded-bitstream, not probe-only). Run procedure documented in `docs/v2.1-release-test-plan.md` so a fourth host can repeat the matrix mechanically:
  - NVENC (RTX 5080 + Windows/ffmpeg 8.1 mainline): ✅ 11/11 2026-05-01. Surfaced the cuda-frame fragility (`No decoder surfaces left`, 10-bit Main10 `-22 EINVAL`) — pipeline replaced with uniform software-decode + GPU-encode shape.
  - AMF (Ryzen 9900X iGPU, RDNA 2 / VCN 3): ✅ 11/11 2026-05-01. Encoder distribution: 7× h264_amf, 1× hevc_amf, 3× copy. Notable: 4K HEVC AMF startup is iGPU-bound (20.8s playlist response on case 10, segments decode clean).
  - libx264 (software fallback, no GPU): ✅ 9/11 2026-05-01 — case 1 hit a curl-side timeout on a 30 GB cold-cache POST (environmental), case 10 (4K H.264 software encode) couldn't deliver seg00001 within the 30s playlist deadline (real perf signal — software 4K is too slow for live). Surfaced + fixed libx264 inheriting 10-bit pix_fmt from AV1 sources (was emitting browser-unplayable High 10 H.264).
  - QSV (Raptor Lake-HX UHD Graphics, i9-13900HX): ✅ 11/11 2026-05-01. Encoder distribution: 8× h264_qsv, 1× hevc_qsv, 2× copy. Row 10 was the live confirmation of the `BestHEVCEncoder`/`HasHEVCEncoder` selector fix from commit `6a29fc3` — pre-fix the worker fell back to h264_qsv on the prefer_hevc path; post-fix hevc_qsv is correctly selected and emits fMP4 + `hvc1` tag.
  - VAAPI (Linux Intel iGPU or AMD discrete): ⚠️ **last encoder family still pending** — needs Linux + non-NVIDIA GPU; TrueNAS box is NVIDIA-only.
- **AV1 encode hardware coverage** *(vs Plex Pass — Plex has limited AV1 encode on Arc; Emby/Jellyfin paid tiers similar)*. AV1 NVENC validated 2026-04-30 on RTX 5080. AV1 QSV is hardware-gated on Intel Arc / Xe2 (Alchemist/Battlemage discrete or Lunar/Meteor/Arrow Lake iGPU); pre-Arc Intel iGPUs have AV1 *decode* but no encode block — confirmed 2026-05-01 on Raptor Lake-HX where `av1_qsv` returns `MFX -40 "Current codec type is unsupported"` despite being compiled into the ffmpeg build. AMD AV1 encode requires RDNA3 dGPU (not the 9900X iGPU's RDNA2 VCN3). DetectEncoders correctly excludes unsupported variants from the active list.
- **Hardware decode coverage on AMD VCN + Intel QSV** *(vs Plex/Emby, same reasoning)*. NVDEC HEVC + AV1 validated 2026-04-30 on RTX 5080. AMD VCN3 (Ryzen iGPU) and Intel QSV decode paths haven't been driven through real content yet — both the AMF and QSV encode tests ran on software input decode (the dual-adapter handoff kept VCN/QSV out of the loop, and we don't currently set `-hwaccel qsv` / `-hwaccel d3d11va` on those encoder paths). Encode validation isn't decode validation; the decode block needs an explicit pipeline change to exercise.
- **Picture-in-picture server signal** *(competitor parity: marginal — Plex has limited cross-device PiP awareness, Emby/Jellyfin don't)*. The handler/store has no PiP-mode flag, so cross-device "is this user in PiP?" awareness can't be derived. PiP itself works on Android TV + Android phone; the server just doesn't know.
- **Picture-in-picture in the Tauri desktop shell** *(vs Plex desktop — has PiP; Emby/Jellyfin desktop are web-wrapper apps with similar limitations to ours)*. Tauri 2 webview doesn't expose `requestPictureInPicture` on Windows. Lands when the upstream PR or a workaround merges; tracked as outstanding on Track E.

## v2 Closed (since the prior snapshot)

- ✅ Music videos as a distinct type (artist children, 16:9 thumbnails)
- ✅ Audiobooks as a library type (flat MVP)
- ✅ Podcasts as a library type (local-files MVP; RSS subscriptions deferred to v2.1)
- ✅ Lyrics end-to-end (USLT + .lrc + LRCLIB)
- ✅ Kodi NFO sidecar import (movie / tvshow / episodedetails)
- ✅ Cover Art Archive fallback for album art
- ✅ DVR retention purge (closes the matcher → capture → cleanup loop)
- ✅ Subtitle burn-in (software-encode path)
- ✅ AV1 encode (SVT-AV1 SW + AV1 NVENC + AV1 QSV constants — **AV1 NVENC validated 2026-04-30** on RTX 5080 against real 4K AV1 source; QSV vendor pending)
- ✅ HEVC encode on QSV / VAAPI / AMF (**HEVC NVENC validated 2026-04-30** on RTX 5080; **AMF validated same day** on Ryzen 9900X iGPU with HDR HEVC source → zscale tonemap → AMF encode; **QSV validated 2026-04-30** on Raptor Lake-HX UHD Graphics — both `h264_qsv` (4K AV1 SDR + 4K HEVC HDR10 → 1080p) and `hevc_qsv` (4K HEVC HDR10 → 4K HEVC SDR via zscale tonemap, fMP4 + hvc1 tag, prefer_hevc selector path) end-to-end through the OnScreen worker; surfaced and fixed a `BestHEVCEncoder`/`HasHEVCEncoder` selector bug at `internal/transcode/hardware.go:212-233` that was hiding non-NVENC HEVC variants from the 4K-HEVC-prefer path. VAAPI still pending hardware tests)
- ✅ Schedules Direct as a second EPG source (token auth, batched fetch, callsign auto-match)
- ✅ Gapless music playback (dual `<audio>` preload rotation)
- ✅ SAML 2.0 SP-initiated SSO (JIT provisioning, admin-group sync, SP keypair auto-generate)
- ✅ Built-in HTTPS (operator-provided PEM via `TLS_CERT_FILE`/`TLS_KEY_FILE`)

## v2.1 Closed (in flight on `main`)

- ✅ **Track A — Bug-shape fixes** (3/3): job-queued OCR endpoint (POST returns 202 + job_id, GET polls — unblocks Cloudflare Tunnel free-tier users hitting 100 s timeouts); Vitest SMTP fixture cleanup; Valkey-backed SAML request tracker (HA-ready — AuthnRequest minted on instance A is validatable by ACS callback on instance B)
- ✅ **Track B — Media types**: home_video library + date-grouped page; CBZ books with paginated reader; **audiobook hierarchy complete** — `book_author → book_series → audiobook → audiobook_chapter` schema (migration 00069 with backfill from the v2.0 flat-grid `original_title` stash, no rescan needed for visibility); scanner detects 4 folder layouts (loose at root, author-only, multi-file book, author/series/book/file); `rootItemType("audiobook")` returns book_author so the library top-level renders authors as a typed shelf (mirrors music's artist row); server-side chapter-boundary resume snap on `GET /items/{id}` so every native client picks up snap-resume without per-client code; author + series detail pages on **all six clients** (web, Android phone + TV, webOS, Tizen, Roku); audiobook embedded covers + chapter scrubber UI in playback. Migration 00068 fixed a latent CHECK-constraint bug on `audiobook_chapter` that would have blocked any fresh DB scanning a multi-file book. Podcast show + episode detail UI also closed.
- ✅ **Track F — Discovery**: smart playlists (rule JSONB, query-time evaluation); trending row (rolling watch_events aggregate). Watch-cooccurrence recommendations + "Because you watched X" were built (item-to-item collaborative filtering, replaced the planned pgvector pipeline) but removed from the home hub before release — the row didn't earn its space; trending stays. Cooccurrence table + sql kept dormant in case the row earns a comeback
- ✅ **Track G — Per-user policy** (5/5): library `is_private` flag with public/private union semantics; `auto_grant_new_users` template wired into invite + OIDC + SAML + LDAP user-creation paths; per-profile inherit-or-override library access; content-rating gates closed in `ListCollectionItems`, `ListItemsByGenre`, `ListWatchHistory`; admin "view as" middleware (read-only, GET-only, IDOR-gated)
- ❌ **Track H — Streaming format (cut)**: server-side DASH `manifest.mpd` endpoint shipped over the existing fMP4 ladder, then ripped out 2026-04-30 along with `shaka-player`, the `Dash` source variants in every native client, and the `media3-exoplayer-dash` Android dep. Cost/benefit didn't pencil out — single-rendition transcode-on-the-fly fights shaka's static-MPD seek model, and Plex/Jellyfin both chose HLS-only for the same reasons. HEVC continues to ship via HLS-fMP4.
- ✅ **Track D — Quality + dev workflow** (3/3): `auth-providers.spec.ts` Playwright spec covering OIDC PKCE shape, SAML signed-AuthnRequest (locks the four-layer SAML signing fix behind a regression guard), LDAP end-to-end + negative path; gh CLI added to CONTRIBUTING.md prereqs (cuts release form to one command); 10-PR Dependabot triage doc grouping the v2.0-tag queue by risk with paste-ready merge commands
- ✅ **Track E — Native desktop client** (most of the list): Tauri 2 shell for Windows/macOS/Linux reusing the SvelteKit bundle in a system webview; native audio engine outside the webview decoding FLAC/ALAC/WAV/AIFF through symphonia 0.5 (migrated from claxon for SEEKTABLE-driven seek; sub-200 ms scrubbing on 24/192) and DSD via a native DSF parser + DoP packer; output through raw `wasapi` IAudioClient in `AUDCLNT_SHAREMODE_EXCLUSIVE` (bit-perfect — OS mixer bypassed) with a shared-mode fallback that uses `AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM` so any file rate plays even when device mix-format differs; ReplayGain enforcement (track / album / off + ±15 dB preamp); OS now-playing widget via `souvlaki` (SMTC / MPRIS / MediaPlayer); "play on this device" cross-device remote control over the existing SSE channel; OS media keys via `tauri-plugin-global-shortcut`; system tray with transport menu; OS notifications on track change; refresh + access tokens in the OS keychain (Windows Credential Manager / macOS Keychain / Linux Secret Service) with one-shot store-to-keychain migration; SSE `progress.updated` broadcast + watch-page consumer for cross-device resume; HTTP body wrapped in an `HttpSeekableSource` that satisfies symphonia's seek via `Range: bytes=N-` and recovers a dropped socket transparently (Cloudflare Tunnel idle close, NAT timeout, server `WriteTimeout`). **Outstanding:** macOS HOG mode + Linux ALSA `hw:` exclusive backends, picture-in-picture for video.
- ✅ **Track E — TV clients**:
  - **Android TV / Fire TV** (hardware-verified): full Leanback + Media3 ExoPlayer client; device-pairing sign-in covers every auth provider via web browser PIN handoff; photo viewer with D-pad sibling navigation (auto-resolves siblings from parent album or library); music auto-advance through albums (silent EOS chain, no Up Next overlay); audiobook speed picker (0.75–2x); collections drill from search/hub; HLS retry policy + 60s read timeout for cold-start transcodes over Cloudflare Tunnel; screen-on flag during active playback.
  - **LG webOS** (SvelteKit + ares-package): setup → login → hub → library → item → search → watch + pairing flow + search type-filter chips + photo viewer + audiobook chapter list + collections + favorites + history + skip-intro/credits + Up Next + music auto-advance + SSE cross-device resume. Hardware validation on a real LG TV outstanding.
  - **Roku** (BrightScript + SceneGraph): setup → login → hub → DetailScene → search with type-filter chips + photo viewer with D-pad sibling nav + favorites + history + collections + transcode negotiation (direct/remux/transcode) + markers + Up Next + music auto-advance + cross-device sync via 5 s polling + per-file stream-token consumption. `Playback_Decide` covered by 13 brs unit tests; channel zip builds clean.
  - **Samsung Tizen** (SvelteKit + tizen-package): bulk-ported from webOS — pairing flow + hub + search with type-filter chips + photos + audiobook chapter list + collections + favorites + history + skip-intro/credits + Up Next + music auto-advance + cross-device SSE sync. AVPlay JS dual-path with HTML5 `<video>` fallback for HW HEVC/AV1. Hardware validation on a real Samsung TV outstanding.
  - **Android phone** (Compose + Material 3): new module at `clients/android_native/`, package `tv.onscreen.mobile`, distinct from the TV client at `clients/android/`. Reuses the TV client's data layer verbatim (Retrofit + Moshi + Hilt + DataStore + AuthInterceptor + TokenAuthenticator). UI: pairing-PIN sign-in (password fallback), hub with poster strips, library grid, item detail, search with debounce, favorites + history + collections drill-in. Player: Media3 ExoPlayer with the full direct/remux/transcode negotiation port from `PlaybackHelper.decide()`, per-file 24h stream token, audio + subtitle pickers (HLS audio re-issues the session with a new audio_stream_index), 10 s progress reporting, skip-intro/credits overlay, Up Next + music auto-advance. Real-hardware validation, photo viewer, audiobook chapter list, cross-device SSE resume, search type-filter chips, favorite-toggle on detail page outstanding.
- ✅ **Android TV subtitle + audio pickers**: pickers showed only ExoPlayer-side tracks, so transcode/remux sessions (which the server emits with one audio stream) couldn't switch language. `PlaybackViewModel.switchAudioStream` now re-issues the HLS session with a new `audio_stream_index` while preserving position; direct-play still uses ExoPlayer's language selector. Subtitle picker gets the same single-choice UX with active-row detection.
- ✅ **Smart-TV trio audio + subtitle pickers**: closes the same gap on webOS, Tizen, and Roku that the Android TV picker work closed earlier. webOS + Tizen wire yellow (audio) and blue (subtitle) remote keys to floating SvelteKit overlays; audio selection re-issues the transcode session at the current position, subtitle selection toggles `video.textTracks[i].mode` (or `webapis.avplay.setSelectTrack('TEXT', i)` on the Tizen AVPlay HW path). Roku ships a unified LabelList picker the * (options) button cycles through audio → subtitle → closed; `TranscodeStartTask` gains `audioStreamIndex` + `positionMs` interface fields so the re-issue lands on the same Roku Video node without a scene rebuild. Subtitle selection maps the picked index to the firmware Video node's `availableSubtitleTracks[i].TrackName`. All three were the last cells flagged ⚠️ / ❌ on the picker rows in sections 18–20.
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
- ✅ **Android phone full feature parity wave**: closes the headline gaps from the prior snapshot. Browse: search type-filter chips (Movies / TV Shows / Episodes / Music, persisted via DataStore — same shape as the TV client; album/artist/season piggyback on existing chips), favorite-toggle in the item-detail TopAppBar with optimistic flip, photo viewer on a dedicated `/photo/{id}` route using `HorizontalPager` with parent-album-first sibling resolution, audiobook chapter list rendered on the detail page. Player: picture-in-picture (16:9 ratio, gated on `hasVideo` so audio-only items don't surface a useless button), in-player OpenSubtitles search reusing the `/items/{id}/subtitles/{search,download}` server proxy, cross-device resume sync consuming `progress.updated` SSE events with same-device echo dedupe (3 s window absorbs 10 s ticker jitter), background audio via `OnScreenMediaSessionService` mirroring the TV-client lifecycle (`AudioHandoff` slot + `NextSiblingResolver` for service-side auto-advance + 10 s progress reporter; re-entry rebuilds fresh rather than threading a bound-service binder through the Compose tree). 17 new unit tests across `SearchViewModel`, `NextSiblingResolver`, and `PlayerViewModel`'s SSE + OpenSubtitles paths. **Real-hardware validation is now the only outstanding item.**
- ✅ **Android phone offline downloads**: WorkManager + Hilt-injected worker + on-disk JSON manifest. Player short-circuits to the local file before even running the direct/transcode decision when a completed download exists. The TV client deliberately stays online-only (couches near a network); this is the phone-only differentiator that flips the matrix cell from `❌` to `✅` against Plex/Emby's premium-tier downloads.
- ✅ **Native desktop audio engine — phase 2**: gapless preload (the next track's ringbuf primes during the current track's tail; promotion is sub-frame on EOS), position polling with auto-advance on engine-side STATE_ENDED, asset URLs honour the configured server base, query-token carrier for asset routes (so the cpal engine works through the same `?token=` middleware path that ExoPlayer uses on TV).
- ✅ **Native desktop audio engine — phase 3**: bit-perfect WASAPI exclusive mode on Windows (raw `wasapi` IAudioClient in `AUDCLNT_SHAREMODE_EXCLUSIVE`, event-driven on a dedicated thread; settings UI surfaces a "Currently: WASAPI exclusive · bit-perfect" badge), shared-mode fallback that passes `AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM` so any file rate plays even when the device mix-format differs (cpal's WASAPI shared rejected non-mix rates outright — the autoconvert flag is what unblocks 24/192 on a 48 kHz-default device); FLAC migrated from claxon to symphonia (one decoder pipeline now covers FLAC + ALAC + WAV + AIFF), enabling SEEKTABLE-backed mid-track scrub via `HttpSeekableSource` (HTTP Range over `Read + Seek + MediaSource`) — sub-200 ms on 24/192, where the prior claxon path took ~6 s decode-bound; ReplayGain enforcement in the engine (track / album / off + ±15 dB preamp) reading symphonia metadata; DSD playback via DSF parser + DoP packer (16 DSD samples per channel → 24-bit PCM frame at sr/16, routed through the same exclusive path); volume slider plumbs into the WASAPI write loops via an atomic so slider movements take effect within ~20 ms (skipped at unity for true bit-perfect output); OS now-playing widget shipped (`souvlaki` 0.8 — SMTC / MPRIS / MediaPlayer); "play on this device" cross-device remote control closes the last cell on the desktop matrix; HTTP body resilience (Range-resume on socket close — survives Cloudflare Tunnel idle close, NAT timeout, server `WriteTimeout`); server-side fix: API server's 60 s `WriteTimeout` was killing media bodies mid-track for clients that read at decode rate (browsers dodged it by buffering the whole body in <1 s on LAN); media-stream + direct-play handlers now clear the response write deadline before `http.ServeFile`. **Outstanding:** macOS HOG mode + Linux ALSA `hw:` exclusive backends, picture-in-picture for video.
- ✅ **Test infrastructure restoration**: server settings test moved behind `//go:build integration` (testcontainers panicked locally without Docker), `lostcancel` fix in `worker/master.go`, race-detector CI job for concurrent packages. Android TV unit suite restored (HomeViewModel rewrite + `supervisorScope` so a hub-fetch failure doesn't cancel sibling fetches; SearchViewModel + PlaybackViewModel ctor patches; orphaned NotificationsViewModelTest deleted). Web `AudioPlayer.test.ts` mock extended with `getApiBase` + `getBearerToken`. Android phone client seeded with 29 unit tests across PlaybackHelper / HubViewModel / PairViewModel / PlayerViewModel — closes the "no test sources" gap.
- ✅ **Track D — Tier-1 transcode integration matrix** (closes 6 of 8 path-matrix gaps from the v2.1 release-test-plan): seven new Go integration tests under `internal/transcode/playback_branches_integration_test.go` drive real ffmpeg against real fixtures and ffprobe the output — WebVTT extraction, subtitle burn-in, audio-stream selection (Forrest Gump commentary track 2), software libx264 / libx265 / libsvtav1 fallback, AV1 encode from a non-AV1 source via av1_nvenc. Plus `TestStreamFile_Success_FullBody` + `TestStreamFile_Range` for the direct-play happy path (the existing tests covered only NotFound + InactiveFile). Writing the suite surfaced **five real ffmpeg-arg bugs no unit test had caught**: WebVTT extraction emitted its `-c:s webvtt` output before the HLS positional and accumulated video options onto the .vtt context (ffmpeg rejected with "webvtt muxer does not support any stream of type video"); subtitle burn-in crashed on Windows paths (backslashes stripped as escape introducers, drive-letter colon parsed as a key=value separator); libx265 was emitting `-level-idc` (an `hevc_nvenc`-specific option name); libsvtav1 was emitting `-maxrate` outside CRF mode (SVT-AV1 rejects); and (caught later in the libx264 live matrix run) libx264 was inheriting 10-bit `pix_fmt` from AV1 sources, emitting browser-undecodable High 10 H.264. All five fixed and locked behind regression tests.
- ✅ **Per-file streaming token + auth hardening**: native players (ExoPlayer, Roku Video node, Tizen AVPlay, mpv) bypass the OkHttp / fetch token-refresh paths, so a 1 h access token expired mid-stream and surfaced as `ERROR_CODE_IO_BAD_HTTP_STATUS` on the next range request. `auth.IssueStreamToken(claims, fileID)` mints a 24 h PASETO with two new claims: `purpose="stream"` (rejected on the Bearer / cookie path so a leaked stream URL can't grant general API access) and `file_id=<uuid>` (asset middleware enforces the chi `{id}` URL param matches, so the leaked URL can't be repurposed across files). `ItemHandler.Get` returns one stream token per file in the response. `authService.Logout` now bumps `session_epoch` after deleting the session — closes a pre-existing weakness where outstanding access tokens kept working until natural TTL after logout. Android, webOS, and Roku all consume the token; older clients ignore the field and fall back to the access token.
- ✅ **Audiobook resume from saved chapter position** (multi-file): `audio.play(queue, idx, startMS=0)` extension threads a resume offset through the audio store; AudioPlayer captures `get(audio).positionMS` before the load reactive resets it, sets `currentTime` optimistically, re-applies in `onLoadedMeta` as a safety net for browsers that clamp pre-metadata seeks. `/audiobooks/[id]` scans chapter details back-to-front for the latest with `view_offset_ms > 0` and the Play button becomes "Resume · ch N · M:SS" with a separate "Start over" pill. Single-file books continue to resume via the watch page's embedded chapter-marker snap.
- ✅ **Audiobook scanner durability**: cross-parent dedup (collapses dupes split across two `book_author` rows by inconsistent AlbumArtist tags — the Graphic Audio LLC. case); phantom prune (audiobook rows with no files and no chapter children); empty-author cleanup (book_author rows whose children all reparented during the cross-parent merge); release-group folder-name cleanup (`A.Court.of.Silver.Flames.1-2.by.Sarah.J.Maas` → author "Sarah J Maas", book "A Court of Silver Flames"). Walk-paths-vs-parsing-roots split fixes a watcher race where fsnotify-triggered scans reparsed every file as if its parent dir were the library root, racing to attach files to fresh audiobook rows under a different author.
- ✅ **Home-video polish**: ffmpeg-extracted frame poster per clip (seek 30s in for 60s+ clips, midpoint for 10–60s, first-frame for shorter / unknown duration — duration-aware so `-ss` never runs past EOF on short clips); auto event_folder collections (`<root>/<EventName>/<files>` → one collection per non-root subfolder, multi-level nesting collapses to top-level so `Yellowstone 2024/Day 1/` stays one collection; renders as an "Events" shelf above the date-bucketed grid); admin pencil-overlay metadata editor on tiles + photo detail page that renames the file on disk to match the new title (sanitised, " (n)" on collision, Windows-reserved-name-aware) and sets the file mtime to taken_at — user edits travel with the file across tools (Finder, Photos.app, Plex/Jellyfin scanners), not locked into OnScreen's DB.
- ✅ **Books format expansion (CBR + EPUB)**: closes Stage 2 of the v2.1 books track. CBR uses pure-Go `nwaples/rardecode/v2` (no cgo); same image-page reader UX as CBZ, dispatched by extension. EPUB parses META-INF/container.xml → OPF for spine length (page count) + cover image bytes (poster); the whole `.epub` is streamed to the browser via `/media/stream` and rendered with epub.js for true reflowable pagination, font sizing, and page-flips inside an iframe. CSP relaxed to allow `blob:` on `img-src` / `style-src` / `font-src` so epub.js's archive-extracted resources actually load (resource references inside chapters generate Blob URLs at the page origin; the bare CSP blocked all of them); rendition flag `allowScriptedContent: true` puts `allow-same-origin` on the iframe sandbox so those blob URLs are loadable from inside it; `linkClicked` handler routes external `http(s)` links to a new tab.
- ✅ **Scanner directory-scope orphan-detection bug**: ScanDirectory walks one folder for fsnotify events but `markOrphanedFiles` was comparing the walked file list against ALL active files in the library and deleting any that didn't match. The under-50%-of-active safeguard fails on small libraries: with 2 files (one EPUB, one CBZ in different folders), walking the EPUB's folder finds 1 file, `1 ≥ 2/2 = 1` so the safeguard passes and the CBZ in the other folder gets marked orphan + hard-deleted. Surfaced as "I read the EPUB and the comic disappeared." Orphan detection now runs only on full library scans; directory-scoped scans can't reasonably know about files outside the walked dir.
- ✅ **Scanner mtime+size fast-skip for periodic re-scans**: surfaced as a real production incident on the TrueNAS QA box — periodic music scans of a 19,013-file FLAC library were running for 8+ minutes and saturating the postgres + valkey connection pools to the point that readiness-probe health checks started failing with `context deadline exceeded`. Root cause: `processFile` excluded music / audiobook / book / home_video from the existing hash-based fast path, so every periodic tick re-hashed and re-ffprobed every track even when nothing on disk had changed. Added an mtime+size short-circuit at the top of `processFile` that runs before `HashFile` — if the on-disk size matches the stored row and the file's mtime predates `ScannedAt`, skip hash + ffprobe + DB upsert entirely (just `MarkFileActive` if the row had been flagged missing). Lifted the `GetFileByPath` lookup so the slow path now does one DB query instead of two. Validated locally: per-file scan cost dropped from ~10s of ms (hash + ffprobe + DB) to ~0.3 ms (one stat + one DB lookup); periodic music re-scans drop from minutes to seconds. Locked behind two unit tests in `internal/scanner/processfile_fastskip_test.go` (fast-skip fires when unchanged, slow path runs when size differs).
- ✅ **Path-traversal fall-through catch-alls**: two encoded-slash regressions found by the new Playwright security spec — `/trickplay/..%2F..%2Fetc%2Fpasswd` and `/media/stream/..%2F..%2Fetc%2Fpasswd` both produced a single path segment after URL decoding that didn't match the registered chi routes (`{id}/{file}` and `{id}` respectively). Unmatched paths fell through to the SvelteKit catch-all and returned 200 with the SPA shell — not a content-disclosure bug, but the wrong shape for a security probe and confusing in logs. Added catch-all guards `r.Get("/trickplay/*", …)` and `r.Get("/media/stream/*", …)` that return 400 for any path under those prefixes that doesn't satisfy the proper segment pattern.
- ✅ **Track D — Playwright suite expansion**: extends `auth-providers.spec.ts` (already shipped) with four more spec files that lift end-to-end coverage from "smoke + auth" to "smoke + auth + security + transcode + playback + policy + gapless" — full suite is now 105 cases × chromium/firefox/webkit (73 passing, 32 env-gated, 0 failing in 56 s end-to-end against local dev). New + extended files: `security.spec.ts` (rate limiting opt-in via `E2E_RATE_LIMIT`, XSS reflection via `<script>`-in-search probe + addInitScript sentinel, path-traversal in transcode segment URLs, admin endpoint authorization for unauthenticated + non-admin); `transcode.spec.ts` (DASH-removal contract — no `manifest.mpd` API path AND no `manifest_url` in start response AND `playlist_url` IS present; HLS pipeline smoke from login → library → transcode → M3U8 → first segment 200; three concurrent sessions all return `session_id`; AV1 fMP4 with `video_copy:true` produces `#EXT-X-MAP` + `.m4s` segments — gated on `E2E_AV1_MOVIE_ID`); `playback.spec.ts` chromium-only (Play/Resume button click on `/watch/{id}`, `<video>` mounts, `readyState ≥ 2`, `currentTime > 0`, seek lands within tolerance, zero console errors); `policy.spec.ts` (admin marks library `is_private:true`, freshly-registered non-admin user's `/api/v1/libraries` omits it, library is restored to public + test user deleted in `finally`; home hub contains zero matches for `/because you watched/i`); plus `gapless.spec.ts` fixes that activate it on chromium + firefox via `E2E_GAPLESS_ALBUM`. Side fixes shipped to make the suite robust: rate-limit env overrides (`OS_AUTH_RATE_LIMIT_PER_MIN`, `OS_TRANSCODE_START_RATE_LIMIT_PER_MIN`) so the cumulative auth load of a full Playwright run doesn't trip the prod-default 10/min limiter in dev (defaults still tight in prod). One known leftover surfaced and noted: PATCH `/api/v1/libraries/{id}` is documented as partial-update (`*bool` for `is_private`) but the handler requires the full body — sending only `is_private` returns 500 because empty `name` fails validation. Test works around by GET→mutate→PATCH; the handler fix is a follow-up.

## Non-Differentiators (All Four Roughly Equal)

Movies / TV / music / photo scanning, embedded + disk art, TMDB+TVDB+MusicBrainz metadata, HLS streaming, direct play, resume position, multi-user, parental content ratings, chapter markers, audit-safe session management, **direct cloud-storage integration** (none of the four ship S3/GCS-native libraries — all four rely on local or NFS mounts; users layer rclone or similar themselves).
