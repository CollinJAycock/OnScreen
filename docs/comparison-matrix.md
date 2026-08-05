# OnScreen vs Plex / Emby / Jellyfin

**Snapshot:** 2026-06-18 against v2.4.0 (unreleased on `feat/v2.4`; server lock lifted after v2.3.0; the on-demand adaptive-bitrate ladder now carries H.264 / HEVC / AV1 rungs, is capped at the client-requested height, and is multi-instance-safe; a multi-node transcode fleet landed — storage-less workers pull the source from the primary over HTTP, and opt-in Intel QSV hardware HEVC decode was validated end-to-end on an i9-13900HX worker decoding 4K HDR; the fleet dispatcher is now cost-weighted (a 4K stream counts ~4× a 1080p one) and capability-aware (HDR jobs route to a GPU-tonemap node, AV1 output to an AV1-capable node), with per-node load % and a live-tunable embedded session cap in the UI; the admin UI now also owns the cluster-wide System knobs that used to be env-only (server name, retention, TMDB rate, public asset cache, static-ABR pre-encode, scanner concurrency, missing-file grace, LAN discovery) and — on the Transcode page, where the rest of the encoder tuning lives — the global Transcode output ceilings plus the adaptive-bitrate ladder + caps, with env vars kept as the initial fallback; HTTPS can be managed entirely from the UI — paste a cert + key in Settings ▸ Security and the server serves TLS from an in-memory keypair with no cert file on disk; node- and site-specific config (bind addresses, paths, SITE_ID, QSV decode, embedded-worker role) moved to a per-node `node_settings` store editable in Settings ▸ Nodes, keyed by NODE_ID and merging joined fleet workers into the picker so a freshly-joined worker is selectable before it has a config row; the Prometheus surface was fully instrumented — HTTP requests/latency with chi **route-template** path labels (no per-ID cardinality blow-up), DB query duration labeled by SQL verb (the `query` label, bounded to a handful of values — never raw SQL text) via a pgx tracer that wraps the existing OTel one, transcode active gauge + jobs by status, scanner files per library, watch events by type, webhook delivery failures, and hub-cache refresh duration; the Windows MSI installer's worker-only mode now registers an onlogon interactive scheduled task instead of a Windows service so NVENC/QSV can actually reach the GPU; TOTP two-factor auth + a purpose-scoped asset token shipped server-side and across the client fleet; v2.2 anime track landed 2026-05-04; Android TV client on Play Store closed-testing track as of 2026-05-13; Fire TV client live on Amazon Appstore test channel same day, **promoted to production 2026-06-18** (ASIN B0GX2XH9P2); Android phone client submitted to Play Store, in first-review queue; Tizen client hardware-verified on a Samsung QN75Q80B 2022 panel between 2026-05-11 and 2026-05-12, Samsung Apps Store submission package prepared 2026-05-17; Intel Arc QSV + native VAAPI transcode paths both end-to-end-validated 2026-05-21 on an A770 against the full coverage set, including HDR tonemap on every encoder family; `av1_vaapi` and `av1_amf` planner support added the same day, with `av1_vaapi` hardware-validated on both Intel Arc (iHD) and AMD RDNA4 (radeonsi) — `av1_amf` still pending a Windows AMD host; the full-VRAM VAAPI path (`TRANSCODE_VAAPI_VRAM`, `scale_vaapi` in GPU memory) was hardware-confirmed on the A770 (iHD) 2026-06-04 — ~360 MB VRAM/session with the Video engine ~99 % under a 2-session 4K-HEVC load — with the AMD `radeonsi` full-VRAM path still pending; per-user ListenBrainz scrobbling is now wired across the full first-party client fleet — web, Android phone, Android TV, Fire TV, webOS, and Tizen — and the Android TV / Fire TV client's background-audio handoff (a foreground MediaSessionService that keeps music playing when the app is sent to the background, e.g. HOME) was device-verified on a Fire TV Stick 4K and a Hisense Google TV, with a detail-screen resume-label refresh confirmed on the Fire TV; **2026-06-05:** encoder fail-over landed — a worker whose hardware encoder can't acquire the GPU (notably a GeForce NVENC worker hitting the driver's measured 12-session cap) automatically spills the job to the next encode provider on the box (NVIDIA → Intel QSV → software; AMD → AMF) instead of failing the stream, so a saturated GPU degrades to the iGPU; full-VRAM encode is now the **default** (`TRANSCODE_QSV_VRAM` / `TRANSCODE_VAAPI_VRAM` flipped on, probe-gated, per-job software fallback) and the all-VRAM VAAPI HDR path (`tonemap_vaapi`) was hardware-confirmed on the A770 — HDR→SDR now runs on the GPU's VEBOX/VECS engine instead of CPU zscale, dropping host CPU from a saturated load-116 to ~25 at equal load; a 3-node homelab fleet (RTX 5080 + RTX 4080 laptop + Arc A770) was load-tested to **~82 concurrent 1080p transcodes** (every GPU under 50%, coordination/CPU-bound) and a solo Arc A770 sustained **~28 mixed-1080p / ~18–20 4K-HDR-tonemap** streams entirely on the GPU; **2026-06-09:** native RTMP "go live" ingest landed — an OBS/ffmpeg push to `rtmp://host:1935/live/<key>` is authenticated by a per-broadcast stream key and surfaces as a Live TV channel through the existing HLS proxy + DVR, with codec-agnostic FLV pass-through (legacy H.264 + enhanced-RTMP HEVC/AV1) and multiple concurrent broadcasters; and a whole-server security review shipped and was remediated — subtitle path-traversal, static-ABR content-rating enforcement, transcode-worker segment-server auth, metrics/pprof bound to loopback, NAT64/6to4 + CGNAT SSRF blocking, image-decode bombs, and SMTP STARTTLS-strip — documented in [security.md](security.md); **2026-06-16:** a subtitle-subsystem hardening pass — embedded **text**-subtitle extraction is now cached, single-flighted, and detached so large 4K-UHD-remux subtitles load instantly instead of taking 25–60 s and 524-ing behind a reverse proxy (the demux-the-whole-file cost is paid once, then served from disk), an image-based stream requested as text returns `415` pointing at OCR instead of a silently-empty `200`, the OCR pass batches a whole stream into one Tesseract invocation (model loaded once, not per cue) with a per-cue fallback, and SDH/forced dispositions are captured on **embedded** streams to parity with external subs and surfaced across the client fleet; a subtitle/OCR cache-dir path bug — the cache root was computed a level **above** the writable volume mount, so on the locked-down container every OCR job and OpenSubtitle download had been failing at `mkdir … permission denied` — was fixed, and OpenSubtitles search responses are now memoized (including the no-match case) behind the existing per-IP rate limit + 401/429 circuit breaker; per-user **home-hub customization** landed — each user reorders and shows/hides every hub row (the Continue-Watching splits, Trending, each per-library Recently Added strip, and the Libraries grid, pinnable to the top), persisted server-side so the layout follows the account across devices; the **admin analytics dashboard** was overhauled — UTC-correct timestamps + viewer-timezone day bucketing, video-only resolution/codec breakdowns, a 7/30/90-day range selector, a per-user leaderboard, client/platform and plays-by-hour breakdowns, a completion-rate card, and a direct-vs-transcode stream-type split backed by a new persisted playback-decision column on `watch_events`; and scanner/metadata fixes — date-based daily/talk-show episode filenames now build a proper show→season-by-year→episode hierarchy, shows resolve by on-disk folder after a Fix Match rename (ending a loop that re-created a duplicate unmatched row on every import), Fix Match gained a release-year filter, intro-detection library passes are scoped to seasons that still have unmarked episodes (it had been re-fingerprinting the entire library on every scan), and the TMDB client gained a disk response cache + `append_to_response` request batching after a personal key got rate-limited).

**Addendum 2026-08-05 (v2.4.0 cut):** two rows corrected against the tree — Chromecast moves ❌→⚠ (a direct-play-only web sender against the Default Media Receiver shipped in `web/src/lib/cast.ts`; transcoded-HLS casting via a custom receiver is a v2.5 roadmap item), and cross-device "play on…" transfer moves ✅→⚠ (server endpoints + receive side are complete in every client, but no client ships a *sender* UI yet). The July–August playback-hardening campaign (server-authoritative capability profiles with runtime codec demotion, dead-chroma detection, bounded stall recovery, TLS-origin pairing fix) post-dates the snapshot paragraph above; a full re-snapshot is scheduled with the v2.5 cycle ([v2.5-roadmap.md](v2.5-roadmap.md)).

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
| Encoder fail-over (saturated GPU → next provider)  |    ✅    |  ❌  |  ❌  |    ❌    |
| Full-VRAM pipeline default (decode→scale→tonemap→encode) | ✅ |  💎  |  💎  |    ⚠     |
| Per-session supersede (one stream per user / item) |    ✅    |  ✅  |  ⚠   |    ⚠     |

Every hardware encoder family — NVENC, QSV, native VAAPI, AMF, plus their HEVC and AV1 variants — is hardware-validated end-to-end against the nine-movie coverage set (H.264 1080p, HEVC 1080p 10-bit, HEVC 4K HDR10, AV1 4K). Four matrix runs across four planner branches and **two independent VAAPI driver stacks** (77/77 transcode-session tests pass total), all 2026-05-21 except where noted: AV1 NVENC on RTX 5080 (2026-04-30); the full QSV family — `h264_qsv` / `hevc_qsv` / `av1_qsv` — and the native `*_vaapi` family — `h264_vaapi` / `hevc_vaapi` / `av1_vaapi` — on an Intel Arc A770 box (iHD driver); the same native `*_vaapi` family again on an **AMD RDNA4 box** (Mesa `radeonsi` driver), confirming `av1_vaapi` on a second, unrelated VAAPI implementation; and AMF (`h264_amf` / `hevc_amf`) forced on the local Windows box. The HDR-to-SDR tonemap chain (zscale-linear → tonemap=hable → zscale bt709 → format=yuv420p) was confirmed firing on every path with `color_transfer=bt709` in the output segments — a fix landed 2026-05-21 to insert the chain BEFORE the VAAPI hwupload, which was previously skipped on the VAAPI branch.

The VA-API row is split because OnScreen carries two distinct paths: the `*_qsv` encoder family (libmfx → VA-API → kernel `i915` → `/dev/dri/renderD128`, Intel-only), and the `*_vaapi` encoder family (ffmpeg's native VAAPI wrappers with `scale_vaapi` filters in a separate planner branch in [`internal/transcode/ffmpeg.go`](../internal/transcode/ffmpeg.go)). Native `*_vaapi` is now validated on both the Intel iHD driver (Arc A770) and the AMD Mesa `radeonsi` driver (RDNA4) — the same planner branch drives both vendors, since on Linux AMD cards expose VAAPI rather than AMF. On Arc, native VAAPI is consistently faster than QSV/libmfx (Dune HDR 1080p first-segment 2.6 s vs 12.9 s, Interstellar 6.2 s vs 15.5 s, GoodFellas 4K HDR 23.0 s vs 34.2 s); RDNA4 is faster still on the heavy 4K-HDR case (GoodFellas 4K HDR ~10 s h264 / ~12 s hevc).

The AMF AV1 branch (`av1_amf`) is still ⚠: AMF is a **Windows-only** AMD encoder API, so a Linux RDNA4 box exercises `av1_vaapi` (radeonsi), not `av1_amf`. Closing the AMF AV1 cell needs an RDNA3+ AMD GPU in a **Windows** host — the planner enum, probe, and filter routing are all wired and the probe correctly excludes it on hardware/OSes that don't expose it (verified on a Ryzen 9900X RDNA2 iGPU, which returned `AMF error 30 — CreateComponent(AMFVideoEncoderHW_AV1) failed`).

The adaptive-bitrate ladder is now ✅ — implemented via the **on-demand model** (Jellyfin-style), not the simultaneous-N-encode `var_stream_map` approach. The parent session runs no ffmpeg: `playlist.m3u8` returns a master listing the ladder, each rung's media playlist is server-predicted from the source duration so all rungs share one segment timeline, and a segment request transcodes that rung on demand — at most one rung encoding at a time, which is what makes it viable on a single GPU. Segment boundaries are frame-quantized to `ceil(i·SegmentDuration·fps)/fps` (exactly where ffmpeg forces a keyframe), so the advertised playlist and the encoded segments stay aligned to <1 frame and seeks land frame-accurately. The ladder now carries **H.264 (`.ts`), HEVC, and AV1 (`.m4s` fMP4)** rungs, is **capped at the client-requested height** (picking 1080p never offers a 2160p rung — which on a heavy 4K HDR source over a fleet link prevents the player from oscillating up to a rung it can't sustain), and the per-rung reachability check is **multi-instance-safe** (worker-side `/seghead` query, not local-disk only). Validated end-to-end on `h264_nvenc` (predicted EXTINF matching the encoder, sub-second forward/backward seeks via prompt rung restart) and exercised on the fleet against a 4K HDR HEVC source decoding on a remote QSV worker. Landing the original fix also surfaced and fixed a latent bug where `-force_key_frames` produced non-IDR frames on NVENC unless `-forced-idr` was set — segments were silently falling back to the `-g` GOP boundary (5 s on a 23.976 fps source instead of 4 s).

The **multi-node transcode fleet** is OnScreen-only (Plex/Emby/Jellyfin transcode in-process on the server). Worker nodes run a separate `worker` binary and join the primary's queue via the shared DATABASE_URL + VALKEY_URL. A worker with **no shared storage** pulls the source from the primary over HTTP — the job carries a `/media/stream/{file_id}` URL with a per-file stream token, and ffmpeg reads it with reconnect + Range support — so the fleet works without an NFS/SMB mount on every node. Each worker resolves the session dir locally and serves its own segments back to the primary's segment proxy. **Intel QSV hardware HEVC decode** is opt-in per worker (`TRANSCODE_QSV_DECODE`): it offloads the 4K HEVC decode to the Intel iGPU while the chosen encoder (NVENC/AMF/software) still does the encode — no cross-GPU surface sharing, since decoded frames download to system memory. It auto-falls-back to software decode if a source fails to init on QSV, so a bad source can't hard-fail playback. Validated 2026-05-23 on an i9-13900HX worker decoding 4K HDR HEVC (Goodfellas) cleanly — the exact source class that hit `-22 EINVAL` on the retired NVDEC/CUDA pipeline, so QSV decode is reliable where NVDEC-HEVC wasn't on mainline ffmpeg.

Beyond that system-memory decode offload, a **full-VRAM Intel QSV path** (opt-in `TRANSCODE_QSV_VRAM`) keeps the *entire* pipeline on the GPU: QSV decodes into VA surfaces (`-hwaccel qsv -hwaccel_output_format qsv`), `vpp_qsv` scales in GPU memory, and the QSV encoder reads those surfaces directly — zero system-memory round-trips, the Intel analogue of the NVIDIA NVDEC → `scale_cuda` → NVENC path. **Validated 2026-06-03 on the i9-13900HX's Intel UHD iGPU** (ffmpeg 8.0.1, oneVPL 2.15): the `-v verbose` filtergraph shows decode, `vpp_qsv`, and encode all reporting *"video memory surface"* with **no `hwdownload`/`hwupload`/`auto_scale`**, and it needs no explicit `-init_hw_device` (oneVPL auto-selects the D3D11VA child device on Windows). SDR-only (HDR keeps the libplacebo/zscale tonemap, which needs CPU frames) and single-GPU (decode + encode on the same Intel device), exactly like the CUDA full-VRAM path; it auto-falls-back to the software-decode path on failure. The VAAPI analogue (`TRANSCODE_VAAPI_VRAM`, `scale_vaapi`) is now **hardware-confirmed on Intel Arc (A770, iHD) 2026-06-04**: with `scale_vaapi` keeping frames in GPU memory, a 2-session 4K-HEVC load held ~360 MB VRAM per session with the Video engine ~99 % busy (scale/VECS engine ~58 %), and native VAAPI decode (`-hwaccel_output_format vaapi`) feeds the encoder with no system-memory round-trip — the Linux/Intel analogue of the QSV and CUDA full-VRAM paths. The AMD `radeonsi` full-VRAM VAAPI path is still pending hardware validation. On **Windows AMD the equivalent full-VRAM AMF path was evaluated 2026-06-03 and declined**: ffmpeg does carry the AMD GPU scalers (`vpp_amf` / `scale_d3d11`) and `d3d11va` decode → `hevc_amf` encode keeps frames on the GPU, but on a multi-GPU host `-hwaccel d3d11va` binds the wrong adapter (it grabbed the NVIDIA dGPU and AMF refused to init), so it needs brittle per-host AMD-adapter pinning; the small RDNA2 iGPU that is the common Windows-AMF target lacks the AMF VPP converter (`vpp_amf` → `AMFConverter-Init error 10`, `scale_d3d11` → unsupported format), so the on-GPU scale a transcode needs won't init there; and the case that *would* benefit — a discrete AMD GPU — is already served cleanly by the native VAAPI full-VRAM path on Linux. So Windows AMF stays software-decode + CPU-scale + AMF-encode.

**Full-VRAM is now the default, and HDR tonemap joined it on the GPU (2026-06-05).** The QSV and VAAPI full-VRAM paths were flipped to **on by default** (`TRANSCODE_QSV_VRAM` / `TRANSCODE_VAAPI_VRAM`), each gated so it only engages when its encoder family is actually selected, with a per-job software-decode fallback if the VRAM pipeline can't init a given source — so a worker keeps decode→scale→encode in GPU memory whenever the hardware supports it, and degrades silently when it doesn't. The last CPU holdout, HDR→SDR tonemapping, moved onto the GPU too: an all-VRAM VAAPI path (`scale_vaapi` → `tonemap_vaapi`, all on VA surfaces) was **hardware-confirmed on the Arc A770 (iHD) 2026-06-05**, running the tonemap on the GPU's VEBOX/VECS engine — which sat at ~0 % in every prior run because HDR was tonemapping on the CPU. Measured impact on the Arc at equal load: host CPU dropped from a saturated **load-116** (software decode + CPU zscale tonemap) to **~25** (everything in VRAM), while the GPU video engine held ~50 % with headroom to spare. It's probe-gated because `tonemap_vaapi` refuses input without SMPTE-2086 mastering-display metadata — real HDR10 sources carry it; HLG / metadata-less sources fall back to the CPU zscale path per-job. Jellyfin runs GPU tonemap in its full-hwaccel pipelines too, but it isn't the default posture and there's no spill-over when a path can't init; Plex / Emby gate HW transcoding behind paid tiers.

**Encoder fail-over** makes a saturated GPU degrade gracefully instead of failing the stream — and it's OnScreen-only, because none of the others has a second encoder (or node) to fall over to. GeForce cards cap NVENC at a fixed number of concurrent sessions: measured **12** on *both* an RTX 4080 laptop and an RTX 5080 (driver 595.71) — higher than the long-quoted 8, but still a hard wall where the next session fails at init with no output. When a worker's hardware encoder can't acquire the GPU, OnScreen retries the job on the next provider the box has — same output codec family, different vendor (`h264_nvenc` → `h264_qsv` → `libx264`; on the AMD/Windows box NVENC → AMF) — turning a hard failure into a spill onto the iGPU. Under a fleet load test this roughly **doubled each NVIDIA node's live-transcode capacity** (12 NVENC + ~15 spilled to QSV/AMF ≈ 27/node, observed live on both GeForce nodes simultaneously). The 3-node homelab fleet sustained **~82 concurrent 1080p transcodes**, coordination/CPU-bound with every GPU under 50 %; a single **Arc A770 solo** sustained **~28 mixed-1080p** or **~18–20 4K-HDR-tonemap** streams entirely GPU-side (host-CPU-gated, GPU video engine ~50 %), and the 24-core coordinator driving it sat **under 22 %** — so one coordinator scales to many worker nodes. The practical takeaway for a budget transcode farm: pair each cheap Arc card with enough host CPU cores and the GPU is the only ceiling left.

On the software HDR path the HDR→SDR tonemap chain now runs **after** the downscale (at output resolution, not source), which for a 4K→1080p HDR transcode cuts the dominant CPU stage by ~4× — an i9-13900HX dropped from ~90 % to single-digit CPU on a 1080p rung of a 4K HDR source.

The fleet dispatcher is **cost-weighted and capability-aware**, which is where OnScreen pulls ahead of the very idea of distributed transcoding (Plex/Emby/Jellyfin have no fleet to schedule). Plain round-robin would treat a 4K HDR tonemap and a 480p remux as equal "one session" load; instead each job is scored in centi-units of a 1080p transcode (`JobCostCenti`: 1080p ≈ 100, 4K ≈ 400, remux ≈ 25) and each worker tracks in-flight cost, so the dispatcher routes by **proportional headroom** — a 16-slot node grinding a 4K stream yields the next job to an idle 12-slot node, while two idle nodes still favour the larger. On top of that, workers advertise their GPU HDR→SDR tonemap path (libplacebo/Vulkan) and encoder families, and the dispatcher ranks in strict tiers — GPU availability, then **capability fit for the specific job** (an HDR job prefers a node that can GPU-tonemap rather than fall back to software zscale; an AV1-output job prefers a node with an AV1 encoder), then load — as a soft preference that never strands a job on a node that can't serve it. The fleet settings page surfaces a cost-weighted **load %** per node, an **HDR-tonemap** capability chip, and an **admin-settable Max Sessions** for the embedded worker that applies live (no restart) and persists across restarts. A concurrency load test (12 k dispatch+ack/s, no counter leak under the Incr/Decr race) backs the accounting.

OnScreen runs Tesseract on PGS / VOBSUB / DVB / XSUB streams and persists the results as `external_subtitles` rows so every client gets text-based playback (smaller bandwidth, restyleable, searchable) rather than burning the bitmap into the video stream. Plex and Emby only do burn-in; Jellyfin has community-plugin OCR. The OCR pass renders the stream's cue frames once (ffmpeg overlay + `mpdecimate` dedupe) and OCRs the whole set in a **single Tesseract invocation** via a list file — the language model loads once per stream instead of once per cue, which on a feature-length film is the difference between minutes and tens of minutes; per-cue OCR remains the automatic fallback if a Tesseract version mis-aligns the batch. SDH (`hearing_impaired`) and forced dispositions are captured on **embedded** streams to parity with external subs, so clients label/filter SDH and forced tracks regardless of source. Embedded **text** subtitle extraction is itself cached (one `ffmpeg` demux per file+stream, single-flighted and detached so a slow 4K-UHD-remux extraction completes and caches even if a reverse proxy times the first request out), turning a 25–60 s per-request cost into an instant repeat; an image-based stream requested as text returns `415` pointing at OCR rather than a silently-empty track. Trickplay generates 10-per-row sprite sheets at 10 s intervals with WebVTT `xywh` cues — same shape Plex Pass / Emby Premiere ship paid; OnScreen ships in core.

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
| Live broadcast ingest (RTMP push / "go live")                 |    ✅    |  ❌  |  ❌  |    ❌    |
| Schedules Direct EPG                                          |    ✅    |  💎  |  💎  |    ✅    |
| Recording rules (once / series / channel-block)               |    ✅    |  💎  |  💎  |    ✅    |
| Series new-only filter                                        |    ✅    |  💎  |  💎  |    ⚠    |
| Pre / post padding per recording                              |    ✅    |  💎  |  💎  |    ✅    |
| Retention purge (auto-delete after N days)                    |    ✅    |  💎  |  💎  |    ✅    |
| Stream-copy capture (zero CPU)                                |    ✅    |  ✅  |  ✅  |    ✅    |
| Refcounted shared sessions (multiple viewers, one tuner slot) |    ✅    |  ⚠   |  ⚠   |    ⚠    |

Plex and Emby gate the entire Live TV / DVR feature set behind paid tiers (Plex Pass / Emby Premiere). OnScreen and Jellyfin are core.

**Live broadcast ingest ("go live") is OnScreen-only.** An embedded RTMP server accepts a push from OBS / ffmpeg / any RTMP encoder at `rtmp://host:1935/live/<stream-key>`; the stream is authenticated by a per-broadcast key (constant-time compare) and surfaces as a Live TV channel through the same Driver → HLS proxy → player path tuners use — so it's watchable in every client and **recordable by the DVR** with no extra plumbing. Ingest is codec-agnostic: the server frames the raw RTMP payloads into FLV and lets ffmpeg transcode whatever came in (legacy H.264 **and** enhanced-RTMP HEVC / AV1) to browser-safe H.264, caching the sequence headers + last keyframe so a viewer can join mid-broadcast. Multiple keys = multiple simultaneous broadcasters, each its own channel. Plex, Emby, and Jellyfin consume *pull* sources (tuners, IPTV playlists) only — none accepts a push/restream, so this replaces a standalone Owncast install rather than competing with the tuner rows above.

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
| Cross-device "play on…" transfer (own ecosystem)   |    ⚠    |  ✅  |  ✅  |    ❌    |
| Sleep timer                                        |    ✅    |  ✅  |  ✅  |    ✅    |
| On-screen subtitle styling (size/color/background/outline) |  ✅ |  ✅  |  ✅  |    ✅    |
| Chromecast / Google Cast                           |    ⚠    |  ✅  |  ✅  |    ✅    |
| AirPlay                                            |    ❌    |  ✅  |  ✅  |    ⚠    |
| DLNA / UPnP server                                 |    ❌    |  ✅  |  ✅  |    ✅    |
| Web + desktop file download (server-wide admin toggle, default off) | ✅ | ⚠   |  ⚠   |    ⚠    |
| Mobile offline downloads                           |    ⚠    |  💎  |  💎  |    ✅    |
| Sync watch / watch parties                         |    ❌    |  ❌  |  ❌  |    ✅    |
| ListenBrainz scrobbling (one-way, per-user, opt-in)|    ✅    |  ⚠   |  🧩  |    🧩    |
| Last.fm scrobbling                                 |    ❌    |  ⚠   |  🧩  |    🧩    |
| Chapter markers + skip targets                     |    ✅    |  ✅  |  ✅  |    ✅    |

Skip Intro / Skip Credits is wired in the web player: a button slides in over the bottom-right corner whenever the playback head is inside an intro / credits region (server-detected, see section 5), `S` is the keyboard shortcut, and a per-browser "Always skip intros" toggle sits right under the button so users discover it the first time it appears. Auto-skip is intro-only — auto-skipping credits would yank the user out of the episode prematurely; that path is handled by the existing auto-next-episode flow with the sleep-timer "end of episode" gate. AirPlay is the remaining real trail here (the ABR ladder landed on `feat/v2.4` — see section 2). Cast / DLNA / SyncPlay show ❌ in the matrix but are not chasing tasks — see "Deferred" near the bottom for why (Android apps + cross-device transfer cover Cast's main use case; DLNA and SyncPlay are permanently scoped out).

OpenSubtitles search + download is built in: the player UI calls `/items/{id}/subtitles/search` against the OpenSubtitles v1 API, and downloaded files are normalized to WebVTT, persisted to disk, and registered as `external_subtitles` rows so subsequent playback gets text-based subs without re-querying. Per-session rate limit (10 searches/minute, 5 downloads/minute) prevents player retries from blowing the OpenSubtitles per-IP quota, search responses (including the no-match case) are memoized for 10 minutes so a re-opened picker doesn't re-spend the tight daily allowance, and a circuit breaker pauses all calls for an hour on a 401/429 (banned/throttled key) instead of hammering into a deeper hole — the same posture as the TMDB client. Cross-device transfer (`POST /playback/transfer`) hands a playback state to a named target client by device label — same shape as Plex's "Play on" / Emby's "Remote Control".

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
| Fire TV                        |    ✅    |  ✅  |  ✅  |    ✅    |
| LG webOS                       |    ⚠     |  ✅  |  ✅  |    🧩    |
| Samsung Tizen                  |    ⚠     |  ✅  |  ✅  |    🧩    |
| Roku                           |    ⚠     |  ✅  |  ✅  |    🧩    |
| iOS / iPadOS                   |    ❌    |  ✅  |  ✅  |    ✅    |
| Apple TV                       |    ❌    |  ✅  |  ✅  |    ✅    |

OnScreen's Android TV / Google TV client graduated to the Play Store **closed-testing track on 2026-05-13** (a dedicated TV release lane under the same `tv.onscreen.android` listing the Android phone build uses; Play's `android.software.leanback` feature filter routes the TV-only AAB to TV devices and the Compose phone AAB to phones). The Android phone client itself was submitted the same day and is in the first-review queue. Desktop ships via Tauri 2 with a native Rust audio engine outside the webview. Tizen got its first end-to-end hardware run on 2026-05-11 against a Samsung QN75Q80B (2022) — sideloaded via Samsung partner cert against the bound DUID, with the full surface exercised on the panel (navigation, video / audio / music / photo playback, watch state, library hygiene). The webOS scaffold sits at near-parity in code — the per-user ListenBrainz scrobble link landed on both the webOS and Tizen clients this cycle, matching the web / phone / Android TV surface — and real LG hardware soak is the open item. The Android TV / Fire TV client's background-audio handoff (a foreground `MediaSessionService` that keeps music playing when the app is backgrounded) was device-verified this cycle on a Fire TV Stick 4K and a Hisense Google TV, with the detail-screen resume-label refresh confirmed on the Fire TV. Roku is feature-complete in code; real-device soak likewise pending. Fire TV ships a dedicated `firetv` product-flavor build of the same Leanback codebase — the flavor drops the TV-provider EPG permissions (`WRITE_EPG_DATA` / `READ_EPG_DATA`) so Amazon's device-compatibility filter doesn't exclude most Fire TV models, while the `googletv` flavor keeps them for the Watch Next row — and it is now **live on the Amazon Appstore (production) as of 2026-06-18** ([ASIN B0GX2XH9P2](https://www.amazon.com/dp/B0GX2XH9P2)), the first OnScreen native client to reach a production store listing. Samsung Apps Store + LG Content Store submission paperwork is the gate between ⚠ and ✅ in the table above. iOS + Apple TV are out of scope until a Swift skill ramp + App Store review budget land.

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

OnScreen ships an OTel + Prometheus + audit-log stack as core; competitors either omit telemetry, gate behind a paid tier, or expect operators to layer it themselves. The Prometheus surface isn't just "there" — it's actually instrumented: HTTP request count + latency with the **chi route template** as the path label (per-ID URLs collapse to one series — no cardinality blow-up), DB query duration labeled by SQL verb via a pgx tracer that wraps the existing OTel one (the `query` label takes a bounded set — `SELECT`, `INSERT`, `BEGIN`, `COMMIT`, …, plus an `other` catch-all — never raw SQL text, so it can't blow up cardinality from parameter values), transcode sessions active (set from the live Valkey index so it's multi-instance- and TTL-correct), transcode jobs by status, scanner files per library, watch events by type, webhook delivery failures, hub-cache refresh duration, and the rate-limiter fail-open counter. The OTLP/gRPC tracing pipe was hardware-validated end-to-end this cycle against a local Jaeger backend (spans visible in the Jaeger UI; chi route-template HTTP spans nest pgx pool/query children, exactly the shape the instrumentation promises). The scheduler runs cron-driven admin tasks (scan, EPG refresh, DVR retention, OCR pass, intro detection, refresh missing artwork, dedupe shows / movies, backup) — every task records `last_run_at`, last status, and last error so the admin UI can surface failures without grepping logs. The jobs feed (`GET /jobs`) gives a 30 s-poll snapshot of in-flight scans + missing-art and unmatched-item counts so the home banner can show "scanning…" / "12 items need a poster" without hammering item endpoints. `/debug/pprof` is admin-gated.

Backup + restore is an admin API + scheduled task: `GET /api/v1/admin/backup` streams a `pg_dump --format=custom` archive with an `X-OnScreen-Schema-Version` header (so the operator's UI can label the file without re-parsing), and `POST /api/v1/admin/restore` runs `pg_restore --clean --if-exists` then — when the uploaded dump's `goose_db_version` is older than the running binary — follows up with an in-process `goose up` so the schema doesn't strand on missing columns. A `?force=true` override gates the dump-newer-than-server case (default refusal is `409 DUMP_NEWER_THAN_SERVER`). The Windows MSI bundles `pg_dump.exe` / `pg_restore.exe` under `{app}\pgsql\bin`, and the handler resolves them via `<exeDir>/pgsql/bin` first then PATH, so a fresh install hits the Backup button and works — no operator-side PATH tweak. pg_restore's per-partition `cannot drop inherited constraint` noise (a Postgres quirk against partitioned tables, of which `watch_events` is one) is classified out of the response when it's the only error class, so a clean restore reports clean while real failures still surface verbatim. Emby and Jellyfin ship backup too, but against SQLite and without the schema-version round-trip / partitioned-table polish.

---

## 10. Security & privacy

| Feature                                            | OnScreen | Plex | Emby | Jellyfin |
| -------------------------------------------------- | :------: | :--: | :--: | :------: |
| Secret encryption at rest (AES-256-GCM)            |    ✅    |  ❌  |  ❌  |    ❌    |
| Built-in HTTPS (operator-provided PEM)             |    ✅    |  ❌  |  ❌  |    ✅    |
| Admin-uploaded TLS cert via UI (no file on disk)   |    ✅    |  ❌  |  ❌  |    ❌    |
| Path-traversal hardening on every asset route      |    ✅    |  ✅  |  ✅  |    ✅    |
| Strict CSP + HSTS + X-Frame-DENY + Permissions-Policy | ✅    |  ⚠   |  ⚠   |    ⚠    |
| SSRF-hardened outbound HTTP (dial-time; loopback/RFC1918/CGNAT/link-local/NAT64-6to4 denied, redirects re-validated) | ✅ | ❌ | ❌ |  ❌    |
| Rate limiting (per-route, env-overridable)         |    ✅    |  ❌  |  ⚠   |    ⚠    |
| No third-party telemetry / analytics in clients    |    ✅    |  ❌  |  ⚠   |    ✅    |
| Self-hosted account system (no vendor cloud)       |    ✅    |  ❌  |  ✅  |    ✅    |

Plex requires a plex.tv account for sign-in even on a self-hosted server. OnScreen and Jellyfin are fully self-hosted; Emby is mostly self-hosted with optional cloud features.

Outbound metadata + artwork fetches go through a `safehttp` dial policy that rejects loopback / RFC1918 / RFC6598 CGNAT / link-local destinations — plus IPv4 tunneled inside NAT64 (`64:ff9b::/96`) or 6to4 (`2002::/16`) — *at dial time* (it validates the resolved IP, not the hostname, so DNS rebinding can't slip through) and **re-validates every redirect hop**, so a malicious or compromised metadata source can't return a URL — or a 302 — that pivots the fetch into the operator's internal network or a cloud-metadata endpoint. The plugin egress path shares the same denylist. A whole-server security review (subtitle path-traversal, static-ABR rating bypass, transcode-fleet segment-server auth, pprof exposure, image-decode bombs, SMTP STARTTLS-strip) was remediated this cycle — see [security.md](security.md). The webview CSP allows only self + inline styles + Cloudflare Insights for the beacon; `script-src` is **nonce-based** (a per-response nonce, no `unsafe-inline`/`unsafe-eval`, no external CDNs), so an injected `<script>` without the request's nonce won't execute. Most competitors set `X-Content-Type-Options` and `X-Frame-Options` but ship without a strict `Content-Security-Policy` or `Permissions-Policy`.

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
- **Live broadcast ingest ("go live"), RTMP push — OnScreen-only** — OBS / ffmpeg push to `rtmp://host:1935/live/<key>` surfaces as a Live TV channel (watchable in every client, DVR-recordable), codec-agnostic (legacy H.264 + enhanced-RTMP HEVC/AV1), per-key authenticated, multi-broadcaster. Plex / Emby / Jellyfin accept only pull tuners / IPTV — none ingests a push, so this replaces a standalone Owncast.
- **Modern auth out of the box** — OIDC, OAuth, SAML, LDAP, PASETO; competitors require plugins or paid tiers for most of these.
- **Native bit-perfect audio engine on Windows** — WASAPI exclusive + DSD-via-DoP + ReplayGain enforcement, shipped today. Plex Pass has it in Plexamp; Emby and Jellyfin don't ship a bit-perfect path.
- **All three book formats native** — CBZ + CBR + EPUB, one reader UI, no plugin install.
- **Anime as a typed library** — AniList primary metadata, per-season franchise walk that maps on-disk seasons onto distinct AniList cours, TMDB → TVDB → AniList episode-fallback chain, watching-status mirror. No competitor ships this in core; Plex has nothing, Emby and Jellyfin rely on community plugins.
- **Subtitle OCR in core** — bitmap subtitle streams (PGS / VOBSUB / DVB / XSUB) get Tesseract'd to text WebVTT and persisted; every client gets restyleable, smaller, searchable subs without re-encoding the video. Plex and Emby only do burn-in; Jellyfin needs a community plugin.
- **Trickplay sprite sheets in core** — BIF-shape `xywh`-cued WebVTT thumbnails out of the box, no Plex Pass / Emby Premiere gate.
- **Intro / credits auto-detection in core** — AcoustID fingerprinting + blackdetect, exposed as chapter rows. Plex Pass ships this paid; Emby and Jellyfin lean on community plugins.
- **First-class observability** — OTel tracing, Prometheus, audit log, structured logs with trace IDs, schema-gated readiness probe, `/debug/pprof` — without a premium tier.
- **Multi-node transcode fleet with smart dispatch** — a separate `worker` binary joins the primary's queue and can run on a box with **no shared storage**, pulling the source over HTTP with a per-file token. The dispatcher is **cost-weighted** (a 4K stream counts ~4× a 1080p one, not "one session") and **capability-aware** (HDR jobs route to a GPU-tonemap node, AV1 output to an AV1-capable node), with per-node load % and a live-tunable session cap in the UI. Plex / Emby / Jellyfin all transcode in-process on the single server; none distributes transcoding across nodes, let alone schedules it by per-node cost and capability. Load-tested to **~82 concurrent 1080p transcodes** across a 3-node homelab fleet, GPUs under 50 %.
- **Encoder fail-over + everything-in-VRAM by default** — a worker whose hardware encoder can't acquire the GPU (e.g. a GeForce NVENC worker hitting the driver's measured 12-session cap) spills the job to the next provider on the same box (NVIDIA → Intel QSV → software; AMD → AMF), so a saturated GPU degrades to the iGPU instead of failing the stream — roughly **doubling each NVIDIA node's live capacity** in testing. Full-VRAM decode→scale→**tonemap**→encode is the default (probe-gated, software fallback); on an Arc A770 that dropped host CPU from a saturated load-116 to ~25 at equal load, with HDR→SDR tonemapping running on the GPU's VEBOX engine. No competitor has a second encoder to fall over to, and none defaults the whole pipeline (tonemap included) to GPU memory across all three vendors.
- **Self-hosted local-account 2FA** — TOTP enrolment + verify-on-login across web + native clients, with no vendor-cloud account dependency. Plex's 2FA rides the required plex.tv account; Emby and Jellyfin ship none in core.
- **In-app discover + request with arr fan-out** — TMDB discover, request, admin approval, and Sonarr / Radarr dispatch all ship in core; competitors require Overseerr / Ombi / Jellyseerr.
- **User-owned home-video metadata** — edits rename the file on disk and stamp the mtime, so user-supplied titles travel across tools instead of being locked into one app's database.
- **Watch-status mirror across every type** — Plan to Watch / Watching / Completed / On Hold / Dropped is a generic feature, not anime-only. None of Plex / Emby / Jellyfin carries the equivalent.
- **OpenSubtitles search + download in core** — the player itself drives subtitle search + download with rate-limited per-session quotas, and downloaded files persist as `external_subtitles` rows. Plex sunset its plugin; Emby gates this behind Premiere.
- **Library hygiene trays** — Fix Match (every unmatched row, paged) and Set Poster (TMDB variants + paste-URL fallback with proper 4xx surfacing) are first-class admin pages. Competitors expose per-item match/poster pickers but not bulk-tray surfaces.
- **Embedded lyrics + LRCLIB synced fallback** — USLT / Vorbis lyrics extracted at scan, LRCLIB filled in afterwards; Plexamp gates this behind Plex Pass and the rest are plugin-only.
- **Strict CSP + SSRF-hardened outbound HTTP** — `safehttp` denies, *at dial time*, loopback / RFC1918 / CGNAT / link-local and NAT64-6to4-tunneled destinations on every metadata fetch and re-validates redirects (DNS-rebind-safe); CSP is nonce-based, with HSTS, X-Frame-DENY, and Permissions-Policy all set out of the box. A whole-server security review (subtitle traversal, static-ABR rating bypass, transcode-fleet segment-server auth, pprof exposure, decode bombs, SMTP TLS) was remediated — see [security.md](security.md).
- **HA across every tier + multi-site DR** — opt-in (off by default): Valkey Sentinel for the lock/session tier, a multi-host Postgres failover DSN over streaming replication, leader-elected singleton work, and active/passive DR across two sites with per-site content addressing + a `/health/cluster` role/lag surface. This is a *structural* lead — the SQLite-based trio can't stream-replicate the database or run a warm second site without re-architecting. Procedures in [dr-runbook.md](dr-runbook.md).
- **Pluggable object storage with CDN offload** — local disk or S3 / MinIO / Backblaze B2 / Wasabi / Cloudflare R2, set live from the admin UI; reads and writes both route through it and `SignedURL` 302-offloads cacheable bytes to a CDN so they skip the app tier. The other three ship no native object-storage layer (operators rclone-mount).
- **Static-ABR pre-encode** — the most-played titles' ABR ladders are pre-encoded once to object storage and served straight from the CDN, so the live-transcode fleet handles only the cold tail — "scales with cache, not GPUs." No competitor pre-encodes a popularity-driven ladder for CDN serving.
- **UI-managed HTTPS — no cert file on disk** — paste a cert + private key in Settings ▸ Security and the server serves TLS from an in-memory `tls.Config`; the key is stored encrypted, validated against the cert on save, and never echoed back. `TLS_CERT_FILE` / `TLS_KEY_FILE` still take precedence when set, so an env-driven deploy isn't disrupted. The competitors that ship built-in HTTPS (Jellyfin, Emby) accept *file paths* in their UI — none accept the PEM content.
- **Per-node config managed from the admin UI** — bind addresses, filesystem paths, `SITE_ID`, Intel QSV decode, and the embedded-worker role are editable **per node** under Settings ▸ Nodes, keyed by NODE_ID and shown alongside joined fleet workers so a freshly-joined node is configurable before it has a row. The competitors expose either one global config UI (Plex / Emby / Jellyfin) or per-server config via static files — none manage *per-node* config in a fleet from a single admin surface.

---

## Where OnScreen trails

Specific competitor named per row. "Nobody has it" doesn't count as a trail.

- **iOS + Apple TV apps** *(vs Plex / Emby / Jellyfin)*. Out of scope until a Swift ramp + App Store review budget land.
- **Tidal / Qobuz integration** *(vs Plex Pass)*. Sized XL — OAuth bind, library import, streaming passthrough, ReplayGain absent on the source side; not a near-term track.
- **ML-driven personalised recommendations** *(vs Plex / Emby)*. Item-to-item collaborative filtering shipped and was pulled — the row didn't earn its space; trending row stays. Pgvector embedding pipeline never landed.
- **TV-client hardware soak** *(vs all three)*. Code-complete on every platform; Android TV is hardware-verified and shipping via Play Store closed testing as of 2026-05-13. Fire TV (a dedicated `firetv` product flavor of the Android TV codebase) is **live on the Amazon Appstore (production) as of 2026-06-18** (ASIN B0GX2XH9P2) — the first OnScreen client in a production store. Android phone is hardware-verified, submitted to Play and in first-review queue. Tizen is hardware-verified on a 2022 Q80B panel as of 2026-05-12; Samsung Apps Store submission package (manifest + screenshots + listing copy) prepared 2026-05-17 and is the gate to ✅ in the table above. webOS / Roku still need real-device soak before Plex-class confidence.
- **AV1 AMF (Windows) hardware validation** *(vs Plex / Emby paid tiers; Jellyfin partial)*. Planner enum + probe + filter routing all wired for `av1_amf` (commit 2026-05-21); probe correctly excludes the encoder where it isn't available (a Ryzen 9900X RDNA2 iGPU returned `AMF error 30 — CreateComponent(AMFVideoEncoderHW_AV1) failed` on the 1 s probe and the encoder wasn't added). AMF is Windows-only, so the RDNA4 hardware now on hand validates `av1_vaapi` (Linux/radeonsi) but not `av1_amf` — closing this cell needs an RDNA3+ AMD GPU in a Windows host. The Linux/radeonsi AV1 path it would otherwise cover is already ✅.
- **AirPlay out** *(vs Plex / Emby; partial on Jellyfin)*. No path from web or desktop into the Apple TV / HomePod ecosystem. Cast and DLNA are tracked separately under "Deferred" below — see that section for why those two don't sit here.
- **iOS offline downloads** *(vs Plex Pass / Emby Premiere / Jellyfin)*. The Android phone client (`android_native`) ships a WorkManager-backed download flow with on-device manifest + queueing; iOS is the only portable surface still uncovered (TV / set-top platforms aren't candidates — they sit on the network and never go offline). Web + desktop now ship a "save the original file" download (admin-toggleable, default off), so the laptop-on-a-flight use case is already covered through the browser path.
- **Last.fm scrobbling** *(vs community plugins on the others)*. ListenBrainz one-way scrobbling now ships in core (opt-in, per-user token, fires off the completed-play `scrobble` event), with the per-user link UI wired across every first-party client — web, phone, Android TV, Fire TV, webOS, and Tizen. Last.fm is the remaining piece — it needs the operator API-key + secret and a per-user web-auth handshake, vs ListenBrainz's single user token.

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
- [api/openapi.yaml](api/openapi.yaml) — REST surface ([api/ERROR_CODES.md](api/ERROR_CODES.md) for error codes)
- [ARCHITECTURE.md](../ARCHITECTURE.md) — design notes
- [CHANGELOG.md](../CHANGELOG.md) — what shipped when
