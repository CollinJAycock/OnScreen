# Changelog

OnScreen tracks server + first-party-client changes. Versions follow
[semantic versioning](https://semver.org/) — minor releases add
endpoints / features without breaking existing API contracts; major
releases (none yet past v2) reserve the right to break things.

The server API is frozen as of **v2.2.0** — see [docs/server-lock.md](docs/server-lock.md)
for the lock posture and what's expected to land in v2.3+ minor
releases without breaking v2.2 clients.

## [v2.2.0] — 2026-05-09

First "lock the server" cut. Anime track shipped, Live TV / DVR
hardened, and a sustained admin / library-hygiene pass make this the
release where existing clients should expect API stability for a
stretch.

### Added — server

- **Anime as a typed library** — AniList primary metadata with
  per-season franchise walk that maps disk seasons onto distinct
  AniList cours; episode fallback chain TMDB → TVDB → AniList
  streamingEpisodes. Watching-status mirror (Plan to Watch / Watching
  / Completed / On Hold / Dropped) shipped as a generic feature, not
  anime-only.
- **Library hygiene admin trays** —
  - `/admin/items/unmatched` (bulk Fix Match, paged) with multi-result
    movie search returning every TMDB candidate not just the top one.
  - `/admin/items/missing-art` (Set Poster) with TMDB poster-variant
    picker + paste-URL fallback. 4xx surfacing with the upstream HTTP
    status when the URL is bad ("URL returned HTTP 403…") instead of
    a generic 500.
  - `/jobs` status feed (in-flight scans + missing-art + unmatched
    counts) for the home banner's 30 s-poll surface.
  - Scheduled `refresh_missing_art` task (every 2 h) for self-healing
    partial enrichment.
- **Set poster + Fix Match per-item endpoints** — TMDB image
  variants, manual TMDB-id apply, force-reenrich that bypasses the
  in-process music-attempted cache.
- **Web download admin toggle** — `web_downloads_enabled` setting,
  default false. Server-wide on/off (Plex / Emby / Jellyfin only have
  per-user permissions). `DownloadFile` short-circuits with 403 +
  `DOWNLOADS_DISABLED` before any DB or filesystem work when the
  gate is off; mirrored to `features.web_downloads` in
  `/system/capabilities` so the client UI hides too.
- **NFO sidecar override** — `<uniqueid type="tmdb">` IDs propagated
  through to `UpdateItemMetadataParams`; movies enriched via
  `RefreshMovie(nfoMovie.TMDBID)` when present.
- **Per-item bulk re-enrich** — `/admin/items/re-enrich-unmatched`
  cap raised from 200 to 1000.
- **Capabilities runtime-truthful** — `features.{trickplay,
  subtitles_ocr, intro_markers, backup}` now reflect actual binary
  availability (ffmpeg, tesseract, fpcalc, pg_dump probed once at
  boot). Clients consuming `/system/capabilities` no longer see UI
  for features that 5xx on first invocation.

### Added — clients

- **Web v2.2 parity push** — subtitle styling controls, manga reader,
  SSO bridge, downloads gated on capabilities, OpenSubtitles search +
  download in player.
- **Android phone (`android_native`) v2.2 parity** — 13 new features,
  191 unit tests; downloads with WorkManager-backed offline mode,
  photo viewer, anime navigation, sign-out, About screen, PiP polish,
  cellular / Wi-Fi settings.
- **Android TV / Fire TV / webOS / Tizen / Roku parity gaps closed** —
  settings, discover, Live TV, DVR, online subtitles, trickplay scrub
  previews across the TV-platform fleet.
- **Tauri desktop downloads** — `download_to_file` Rust command opens
  a native save-as dialog and streams via ureq on a worker thread,
  bridging the webview's broken `<a download>`.
- **Sleep timer** — 15m / 30m / 45m / 1h + "End of episode"; pauses
  on duration fire, short-circuits auto-next on episode mode.
- **Skip Intro / Skip Credits finished** — `S` keyboard shortcut,
  per-browser auto-skip-intros toggle, slide-in animation, marker
  coordinate fix (`contentTimeSec` no longer double-adds
  `hlsOffsetSec` under HLS).

### Added — UX / admin

- **Settings nav consolidated** — 15 → 7 top-level tabs with pill
  sub-nav for groups that have multiple children. Existing per-screen
  routes preserved (bookmarks still work).
- **Sharing subsection at the top of General** — Allow web downloads
  toggle moved out of the misleading "Restart required" Server
  subsection.
- **Setup flow swap** — first-run gate asks for TMDB / TVDB API keys
  instead of pushing operators to create libraries before any
  metadata can land.

### Fixed

- **Music artist art reliability** — `extractArtistArt` restricted to
  `artist.jpg/jpeg/png` (was including `folder.jpg` / `poster.jpg`
  which made every artist serve AC/DC's band photo on flat-album
  layouts). Flat-album-aware `artistDirFromTrack` helper handles
  `<root>/<artist>/<track>` layouts that previously resolved to the
  library root.
- **TMDB enrichment hardening** —
  - Pre-flight merge by TMDB id with `mergeIsSafe` similarity gate
    catches stale-canonical-row collisions without false-merging
    similar titles.
  - Pre-flight match-by-title+year picks up NFO-only siblings.
  - `enrichMovie` no longer leaks the resolved TMDB id (it set
    `result.TMDBID` but never assigned to `p.TMDBID`).
  - Auto-enrich title-similarity gate blocks bad TMDB guesses on
    obscure titles.
  - Year-regex tightened: prefers `(YYYY)` over bare 4-digit, handles
    `Title-YYYY` suffix.
- **Scanner correctness** — rescan queues enrichment for items
  missing metadata; music art written per-item not per-directory; per-
  artist UA-aware artwork fetch (Wikimedia rejects Go default UA).
- **DB performance** — query-perf indexes on FK cascade columns, FTS
  + ACL filtering inside the query (no result-set leakage), batched
  library-purge loop, scheduled-tasks `task_type` unique constraint.
- **`/api/v1/hub` cut from ~3.2s to <1s** — admin EXPLAIN ANALYZE
  endpoint added for diagnosis.
- **Background long-running endpoints** — `refresh-missing-art`
  backgrounded so the HTTP request doesn't hit Cloudflare's 30 s
  edge timeout.
- **Force-reenrich bypasses musicAttempted cache** — manual operator
  override after data corruption (NULL'd poster_path, wrong match)
  no longer silently no-ops because the prior scan's entry sat in
  the in-process map.
- **Artwork download 4xx surfacing** — `DownloadHTTPError` typed
  error so the apply-poster handler returns 400 with the upstream
  status, not a generic 500.
- **`pprof` index dispatched** — admin `/debug/pprof` was routed to
  the wrong path.
- **Concurrent resize decodes bounded** to GOMAXPROCS so a TV row
  scroll doesn't spike RSS.

### Changed

- **Migrations squashed** — 83 chained files collapsed into a single
  `00001_init.sql`. Existing DBs migrate cleanly; new installs skip
  the 83-step replay.
- **Delete = hard delete** — `media_items.status='deleted'`
  tombstones dropped; subtree DELETE actually removes rows.
- **HLS-only streaming** — DASH support ripped out (matches Plex /
  Jellyfin posture).
- **Frontend deploy pipeline** — frontend must `web/dist →
  internal/webui/dist` before Go build (use `deploy.ps1` or
  `make deploy`).

### Permanently scoped out

These were considered and rejected; see
[comparison-matrix.md](docs/comparison-matrix.md) "Deferred — workaround
in place" for full rationale.

- **DLNA / UPnP server** — separate progressive-MP4 muxer path,
  per-renderer compatibility quirks, audience is mostly legacy TVs
  already replaced by Cast / app-store TVs.
- **SyncPlay / watch parties** — no demand signal; Discord
  screenshare / Watch2Gether already serve the audience.
- **Trailers / extras** — value-vs-effort doesn't pay back.
- **Tauri desktop picture-in-picture** — webview doesn't expose
  `requestPictureInPicture` on Windows; not worth a custom miniplayer.

---

## [v2.1.x] and earlier

See git history (`git log v2.1.0`) — CHANGELOG.md entries for
versions before v2.2 were not preserved through the v2.2 cut.
