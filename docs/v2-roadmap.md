# v2 Release Roadmap

Target: close the feature gaps vs Plex/Emby/Jellyfin identified in [comparison-matrix.md](comparison-matrix.md) under "Where OnScreen Trails." Ten items, grouped into six tracks. Rough sizing below is ballpark only — estimates tighten once we spike each item.

---

## Track A — New Media Types

Unlocks the "all-in-one library" story for users coming from Plex/Emby. Each new type is schema + scanner + enricher + API surface + minimal web UI.

| Item | Complexity | Notes |
|---|---|---|
| Audiobooks | L | New `audiobook` item type + `audiobook_chapters` table. Reuse music scanner for tag parsing (m4b / mp3). Chaptered resume state. Agent: Audible has no open API — could try Google Books + local chapter markers. |
| Podcasts | M | RSS feed poller. Episodes as children of podcast series. No transcoding needed — serve raw mp3. Subscription model (URL + refresh interval). |
| Music videos | S | New type; mostly a scanner branch that routes files with music-video tags or `Music Videos/` folder convention to a separate type. Reuse existing video transcode pipeline. |
| Books / comics | L | CBR/CBZ/EPUB parsing (new deps). Separate reader UI (significant frontend work). Likely candidate to **defer to v2.1** — smallest overlap with current user base. |

**Dependency order:** music videos (smallest, shares existing video pipeline) → podcasts → audiobooks → books.

---

## Track B — Music Feature Polish

Music is a stated audiophile pillar; these items close the "sounds serious" gap.

| Item | Complexity | Notes |
|---|---|---|
| Gapless playback | M | Requires lossless-PCM passthrough on direct play, buffered crossfade on HLS. Existing `hls.js` doesn't do gapless trivially — may need preload-next-track via `hlsInstance.loadSource` swap. |
| Cover Art Archive fallback | S | Straight MusicBrainz release-ID → `https://coverartarchive.org/release/{mbid}/front.jpg`. 100 LOC in enricher; can chain after TheAudioDB. **Cheapest win on this list.** |
| Tidal / Qobuz integration | XL | OAuth, library import, streaming passthrough, licensing — this is Plex Pass-tier work. Suggest **scoping down**: "Tidal metadata supplement" (use Tidal as another metadata source, not a playback path) before full integration. |
| Lyrics (LRC / synced) | M | Parse `.lrc` sidecars; scan embedded USLT / SYLT frames in ID3. UI scroll-with-timestamp. Agent: LrcLib.net is free and MIT-licensed (good fit). |

**Dependency order:** Cover Art Archive (trivial, ship first) → Lyrics → Gapless → Tidal (last; scope decision needed).

---

## Track C — Transcoding Extensions

Closes the "my device couldn't play that" cases and broadens GPU hardware support.

| Item | Complexity | Notes |
|---|---|---|
| HEVC hw encode on QSV / VAAPI / AMF | M | Mostly a BuildHLS branch per encoder family — the underlying FFmpeg support exists. Test matrix is the work (one physical box per accelerator). |
| AV1 encode (SVT-AV1 software) | M | libsvtav1 encoder. Runtime cost is significant — AV1 SW encode is slow. Gate behind an admin opt-in or "4K only" policy. |
| AV1 hw encode (NVENC Ada, QSV ARC) | M | Only on RTX 40-series (`av1_nvenc`) and Intel ARC (`av1_qsv`). Detect-and-offer, don't hard-require. |
| Subtitle burn-in | M | `-vf subtitles=path.ext` or `-vf overlay` for image subs. Gated by client capability detection (only burn when client can't render WebVTT). New player-side signal needed. |

**Dependency order:** HEVC QSV/VAAPI/AMF (test-matrix-driven) → Subtitle burn-in (needs capability signal design) → AV1 SW → AV1 HW.

---

## Track D — Live TV / DVR

Finish Phase B of the existing Live TV work.

| Item | Complexity | Notes |
|---|---|---|
| Schedules Direct EPG fetcher | M | Schema already exists; SD has a clean JSON API. Paid service ($35/yr) but widely used. XMLTV stays as the free alternative. |
| DVR scheduler + conflict resolver | M | Cron-like runner reading `epg_programs` with user-set rules (series / single / keyword). Conflict detection when tuner count is exceeded. |
| DVR web UI (schedule list, upcoming, past recordings) | M | New routes under `/tv/recordings`. Schedules Direct content rating integration. |

**Dependency order:** Schedules Direct fetcher → Scheduler → Web UI. Can parallelize UI with scheduler once scheduler's API contract lands.

---

## Track E — Import / Interop

Lowers the switching cost from Kodi / existing libraries.

| Item | Complexity | Notes |
|---|---|---|
| NFO sidecar import | M | Read Kodi-style `movie.nfo` / `tvshow.nfo` / `episodedetails.nfo` / `album.nfo` / `artist.nfo` XML. Parse deterministic fields (title, year, plot, ratings, IDs). Treat NFO as authoritative when present, fall back to agents when missing. Most valuable for users migrating large Kodi libraries. |

---

## Track F — Auth

Enterprise / compliance.

| Item | Complexity | Notes |
|---|---|---|
| SAML | L | Shared libs exist (crewjam/saml). IdP-initiated + SP-initiated flows. Attribute-mapping UI for admin. Less common in self-hosted space but a hard requirement for corporate/edu deployments. Could **defer to v2.1** if v2 timing pressure hits — OIDC already covers most SSO needs. |

---

## Summary Table

| Track | Items | Sizing | Priority |
|---|---|---|---|
| A — New media types | 4 | S+M+L+L = ~XL total | High (user-facing story) |
| B — Music polish | 4 | S+M+M+XL = large if Tidal full-scope, medium if scoped | High (core differentiator) |
| C — Transcoding | 4 | M×4 = ~L total | Medium (device compat) |
| D — Live TV / DVR | 3 | M×3 = ~L total | High (already in flight) |
| E — NFO import | 1 | M | Medium (migration funnel) |
| F — SAML | 1 | L | Low (small audience, OIDC covers 80%) |

---

## Recommended Defer-to-v2.1

If v2 scope needs trimming, these are the safest to push:

1. **Books / comics** — smallest user overlap, largest frontend cost
2. **Tidal / Qobuz full integration** — scope to "metadata supplement" for v2; full streaming in v2.1
3. **SAML** — OIDC covers the common cases; SAML is a corporate checkbox
4. **AV1 hw encode** — RTX 40-series + Intel ARC only; small current install base
5. **Async OCR endpoint** — discovered during 2026-04-27 beta soak. The synchronous `POST /items/{id}/subtitles/ocr` binds tesseract to `r.Context()`, so any reverse proxy with a sub-multi-minute response timeout (Cloudflare Tunnel free tier is 100s) kills the subprocess and produces zero output. Feature-length PGS tracks regularly exceed 100s. Convert to job-queued: POST returns 202 + job_id, `GET /subtitles/ocr/{jobId}` polls, OCR runs in a server-lifetime goroutine via `context.Background()`. Watch-page UI polls instead of awaiting. The scheduler-based `ocr_subtitles` backfill already does this correctly; same pattern exposed per-stream. **Workaround for v2.0 deployments behind reverse proxies:** trigger the scheduler `ocr_subtitles` task instead of clicking the per-stream OCR button in the UI.

---

## Open Questions

- Do we bundle this as **v2.0 big bang** or split into **v2.0 (Tracks A/D) + v2.1 (B/C/E/F)**?
- Is there a v2 deadline driving prioritization?
- Do we need external beta testers before the v2 release?
