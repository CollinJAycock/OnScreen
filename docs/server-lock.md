# Server lock posture (as of v2.2.0)

The OnScreen server API is **frozen for new breaking changes** as of
v2.2.0 (tagged 2026-05-09). Engineering attention shifts to the
client surfaces — Android phone / Android TV / Fire TV / web /
desktop / iOS / Apple TV / webOS / Tizen / Roku — until the next
major-version reason emerges.

## What "frozen" means

- **The v2 API stays stable.** Endpoints, request shapes, response
  shapes, error codes, capability flags shipped at v2.2.0 are
  contracts. Future v2.3, v2.4, … may *add* endpoints or fields, but
  never *remove* or *rename* them.
- **Settings keys stay stable.** New keys can land in v2.x, but
  existing keys keep their semantics. A client reading
  `web_downloads_enabled` against a v2.2.0 server gets the same
  answer it gets against v2.4.0.
- **Capabilities flags stay honest.** `/system/capabilities` already
  reflects runtime tool availability at boot (ffmpeg / tesseract /
  fpcalc / pg_dump probed in `setRuntimeDetected`). New flags follow
  the same posture — they correspond to working code paths, not
  intent.
- **Database schema stays additive.** Migrations may add tables /
  columns / indexes during v2.x; they may not drop or rename
  anything that's already in place. The squash to `00001_init.sql`
  is the canonical baseline.

## What's allowed in v2.3+

These can ship *during* the lock without breaking it:

- New endpoints under existing namespaces.
- New optional fields on response shapes (clients ignore unknown
  fields per Postel's law).
- New capability flags.
- New scheduled task handlers (handlers + system-task seeds).
- New settings keys with sensible defaults.
- New metadata agents in the agent fallback chain.
- Performance / query / index improvements.
- Bug fixes that preserve the API contract.

## v2.3+ candidates already sized

Tracking these as "ship anytime, don't gate the lock":

- **Adaptive bitrate HLS ladder** — multi-rendition variant
  playlists. Touches the transcode pipeline + playlist generator.
  ~2 weeks. Highest user-facing impact of anything left.
- **2FA / TOTP** — closes Plex / Emby / Jellyfin parity for
  password-based accounts. Drops into the existing PASETO + session
  shape. ~1 week.
- **Last.fm / ListenBrainz scrobble exporter** — listen events
  already live in `watch_events`; one-way exporter. ~1 week.
- **Audio loudnorm filter** — ffmpeg one-pass loudnorm wrapped
  behind a per-session "Normalize" toggle. ~3 days.

None of these break v2.2.0 clients; they're additive and ship as
v2.x minor releases when client work has slack.

## What would force a v3

Only one of:

1. A protocol-level transport change (HTTP/2-only, gRPC migration,
   etc.).
2. An auth-model rework that obsoletes PASETO + the per-file stream
   token shape.
3. A schema change that's not expressible as additive migration
   (renaming a primary domain table, repurposing a foreign-key
   semantic).

Any of these would also need a year-long client-deprecation cycle
across all 9 client platforms. Don't propose v3 work without that
budget.

## Out of scope, permanently

Captured in [comparison-matrix.md](comparison-matrix.md) under
"Deferred — workaround in place":

- **DLNA / UPnP server** — separate muxer path, per-renderer
  quirks, audience already on Cast / app-store TVs.
- **SyncPlay / watch parties** — no demand signal; Discord
  screenshare / Watch2Gether already serve the audience.
- **Trailers / extras** — value-vs-effort doesn't pay back.
- **Tauri desktop picture-in-picture** — webview doesn't expose
  `requestPictureInPicture`; not worth a custom miniplayer.

These don't ship in v2.x and don't justify a v3.

## Audit reference

The lock was preceded by a server-completeness audit that confirmed
the codebase carries no abandoned code, no orphaned endpoints, no
unwired settings keys, and no scheduler tasks without seeds. The
single capabilities-honesty fix (boot-time tool probes) landed in
the v2.2.0 cut.
