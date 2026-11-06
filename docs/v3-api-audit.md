# v3 API audit: dead endpoints and fields in `/api/v1`

2026-11-05, `main` @ `bef35c94`. The first step of [v3-plan.md](v3-plan.md)
item 11: the owner decided to keep `/api/v1` and remove what no first-party
client uses, with every client moving in the same release.

**Status 2026-11-05:** the owner decided the endpoints: **E1–E7 removed**
(branch `claude/v3-api-removals`), **E8 kept** as an operator tool.

**Status 2026-11-06:** **F1–F7 removed** (branch `claude/v3-remove-f1-f7`),
with `docs/api/openapi.yaml`. **F8 (`audio_passthrough`) kept and shown**:
the web Now Playing card has an "Audio passthrough" chip next to the play
decision (branch `claude/now-playing-passthrough`). F9 (`video_profile`,
`video_level`) stays until the Kodi add-on exists. Two removals went a step
further than the field:

- **F1:** library create no longer takes an interval at all. New libraries
  scan daily and refresh metadata weekly, as before when the field was absent;
  the scan interval is changed afterwards in minutes.
- **F6:** the server-wide PIN-switch toggle is gone, not just its settings
  field. It defaulted to on, nothing could switch it off without a raw API
  call, and PIN switching only ever reaches the caller's own household
  (`VerifyPIN`), so turning it off never protected one account from another.

## How it was checked

1. **Every route the server registers.** `chi.Walk` over the real
   `NewRouter`, with every handler field filled so no conditional route is
   skipped (`internal/api/routes_dump_test.go`, run with `ROUTES_OUT=…`):
   **434 routes**, 422 without the OPTIONS / HEAD preflights.
2. **Every client's source**, tests excluded: web, Android TV / Fire TV,
   phone + Quest, webOS, Tizen, Xbox (app and shell), Roku, desktop. Plus the
   server itself (the transcode worker, playlists and feeds it writes) and the
   docs, as separate columns. The matcher accepts each client's way of
   building a path: Retrofit `{id}`, Kotlin `$id`, TypeScript `${id}` behind
   an `/api/v1` prefix, BrightScript `"…/" + id + "/…"`
   (`scripts/api-audit/endpoints.py`).
3. **Every candidate re-checked by its distinctive word** in every client.
   This caught one false positive: Roku builds `trickplay/index.bif` in
   pieces, so it *is* used.
4. **Every JSON field name** declared in `internal/api/v1` (857 names) was
   searched for as a whole word in every client, snake_case and camelCase
   (`scripts/api-audit/fields.py`). A name used anywhere counts as used, so
   this finds the clearly dead fields, not every unread one.

## Endpoints: 422 checked, 387 used by a client

The other 35 break down as follows.

### In use without a client calling them (keep: 27)

| Routes | Who uses them |
|---|---|
| `/transcode/sessions/{sid}/playlist.m3u8`, `…/seg/{name}`, `…/abr/{rung}/index.m3u8`, `…/abr/{rung}/seg/{name}` | Players follow the URLs inside the playlists the server writes. |
| `/transcode/static/{fileID}/master.m3u8`, `…/{rung}/index.m3u8`, `…/{rung}/seg/{name}` | Static ABR, same: URLs in the server's own playlists. |
| `/tv/channels/{id}/segments/{name}` | Live TV segments listed in the channel playlist. |
| `/items/{id}/trickplay/index.bif` | Roku (path built in pieces). |
| `/opds/libraries/{id}`, `/opds/recent`, `/opds/search`, `/opds/opensearch.xml`, `/opds/files/{fileId}/cover` | E-reader apps (KOReader and the like), via the feed's own links ([opds.md](opds.md)). |
| `POST /auth/saml/acs` | The SAML identity provider posts the login here. |
| `POST /arr/webhook` | Sonarr / Radarr / Lidarr post here. Its camelCase payload fields (`eventType`, `movieFile`, …) are theirs. |
| `GET` and `POST /digest/unsubscribe` | The link in the digest email. |
| `/admin/debug/pprof/*` (7), `/admin/debug/runtime`, `/admin/debug/explain/{name}` | Operator tools (`go tool pprof`, curl), admin-only. |

### No client, no external caller: candidates (8)

| # | Route | What it is | Recommendation |
|---|---|---|---|
| E1 | `DELETE /optimize/versions/{id}` | An alias: same handler and same id space as `DELETE /optimize/jobs/{id}`, which the web app calls. | **Remove.** |
| E2 | `GET /photos/search` | EXIF search (camera, lens, aperture, ISO, focal length, dates, GPS). No client has UI for it. | **Remove**, unless a photo-search UI is wanted in v3. |
| E3 | `POST /maintenance/refresh-missing-art` | Manual trigger for what the scheduled `refresh_missing_art` task already does (Settings › Tasks › Run now). | **Remove.** |
| E4 | `POST /maintenance/dedupe-shows` | One-off repair. Full scans already repair duplicates (since migration 00033). | **Remove.** |
| E5 | `POST /maintenance/dedupe-movies` | Same as E4, for movies. | **Remove.** |
| E6 | `POST /maintenance/purge-deleted-library` | Purges a deleted library's rows. No UI. | **Decide:** remove, or add a button to Settings › Maintenance. |
| E7 | `POST /admin/items/re-enrich-unmatched` | Bulk re-match of unmatched items. The Unmatched page works per item. | **Decide:** remove, or add a "Re-match all" button to Settings › Unmatched. |
| E8 | `GET /admin/logs` | The in-memory log ring buffer. Server comments point operators to it; no UI reads it. | **Keep** as an operator tool (like `/admin/debug`), or add a Logs page. |

## Fields: 857 names checked, 36 in no client

Not client-facing (keep):

- **The incoming *arr webhook payload** (`eventType`, `movieFile`, `episodeFile`, `trackFile`, `folderPath`, `relativePath`, `artistName`): those services send it.
- **The admin runtime snapshot** (`go_version`, `goroutines`, `heap_*`, `num_gc`, …): operator tooling.
- **`request_ip`** on the device-pairing record: stored in Valkey, never returned.
- **The `re-enrich-unmatched` response** (`enrich_queued`, `enrichment_queued`, `old_title`, `new_title`, `title_cleaned`, `titles_cleaned`): goes with E7.

Candidates:

| # | Field | Where | Notes | Recommendation |
|---|---|---|---|---|
| F1 | `scan_interval_ns`, `metadata_refresh_interval_ns` | Library create / update **request** bodies | The web app sends `scan_interval_minutes`. Nanosecond durations are an old form. | **Remove** (accept minutes only). |
| F2 | `max_audio_channels` | Transcode start **request** | Superseded by `maxAudioChannels` in the `X-Client-Capabilities` header, which every client sends. | **Remove.** |
| F3 | `burned_subtitle_stream_index` | Transcode start response | No client reads it. Android TV tracks the burned-in index from the play decision. | **Remove.** |
| F4 | `last_client_name` | Item detail response | No client shows it. | **Remove.** |
| F5 | `owner_id`, `owner_username` | Managed-profile response | No client reads them. | **Remove.** |
| F6 | `pin_switch_enabled` | Settings response (and the Trakt settings DTO) | No client reads it. The PIN switch works without it. | **Remove.** |
| F7 | `abr_ladder` | `/system/capabilities` features | No client reads it. | **Remove.** |
| F8 | `audio_passthrough` | Now Playing session stream format | No client shows it. | **Decide:** remove, or show it in the web Now Playing panel next to the play decision. |
| F9 | `video_profile`, `video_level` | Item file response | Added in v2.7 for the capability profile, which the server uses internally. No client reads them. The planned Kodi add-on might. | **Keep until Kodi exists**, then decide. |

## What this doesn't cover

- **Fields read by name in some client but not others.** 361 names appear in
  exactly one client. They're used, so they stay.
- **A struct field whose name another struct also uses.** For example, an
  unread `title` on one response counts as used, because `title` appears
  everywhere. Finding those needs a per-response check and isn't worth it for
  removals this small.
- **Query parameters.** Only paths and JSON bodies were checked.

## What the endpoint removal changed (E1–E7)

- **Routes:** 434 → 427, exactly the seven (checked by re-running
  `TestDumpRoutes`). Re-running `endpoints.py` finds no new unreferenced
  route.
- **Handlers:** `MaintenanceHandler` keeps only reprobe-metadata, and its
  constructor takes only the library service, the reprober and the logger.
  `ItemBulkAdminHandler` keeps the Unmatched and Missing-art lists, and lost
  its enricher, audit logger and title-update query.
- **The photo EXIF search, at every layer:** the v1 handler and its four
  query parsers, the domain service methods and types, the server and worker
  adapters, and the `SearchPhotosByExif` / `CountPhotosByExif` SQL (sqlc
  regenerated).
- **`library.Service.PurgeDeleted`:** removed. It was a public wrapper over
  `purgeInBatches`, which library delete still runs; its tests now call
  `purgeInBatches` directly. `scanner.StripReleaseGroupPrefix` was only used
  by the bulk re-enrich and is gone too.
- **Kept on purpose:** `DedupeTopLevelItems` (the scanner repairs duplicates
  with it), the `refresh_missing_art` scheduled task, and
  `UpdateMediaItemTitle` (the music scanner's tag titles).
- **Docs and tests:** `docs/api/openapi.yaml` lost the five documented paths
  (photo search and purge were never in it). The UAT admin-gate test now
  covers reprobe-metadata. Stale comments that named the removed routes are
  reworded; CHANGELOG history is left as it was.

No client changed: none called these routes.

## Next step

F9 is the only open field: decide it once the Kodi add-on exists (keep it if
Kodi reads it, else remove it with `docs/api/openapi.yaml` and re-run both
scripts). F1–F7 were removed on 2026-11-06 and F8 is now used by the web app. Each removal
is server-side only: no client calls these routes, and no client reads or
even declares these fields (zero hits in every client), so no client has to
change. Re-run both scripts after the removals to confirm.
