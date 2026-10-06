# OnScreen API error codes

All error responses use the same envelope:

```json
{ "error": { "code": "...", "message": "...", "request_id": "..." } }
```

Clients should branch on `code`, not HTTP status — the same HTTP status may
carry multiple codes, and `message` is human text that may change across
releases.

## Generic (any endpoint may emit)

| Code | HTTP | Meaning |
|---|---|---|
| `BAD_REQUEST` | 400 | Malformed input. `message` carries specifics. |
| `VALIDATION` | 422 | Parsed OK, failed domain validation. |
| `UNAUTHORIZED` | 401 | No or invalid credentials. |
| `FORBIDDEN` | 403 | Authenticated but not permitted. |
| `NOT_FOUND` | 404 | Resource doesn't exist (or obfuscated: not owned). |
| `RATE_LIMITED` | 429 | Exceeded per-key rate limit. `Retry-After` header set. |
| `INTERNAL` | 500 | Unexpected server-side failure. |
| `RATE_LIMITER_UNAVAILABLE` | 503 | Rate limiter backend (Valkey) unreachable. |

## Setup + CSRF

| Code | HTTP | Meaning |
|---|---|---|
| `SETUP_LOCAL_HOST_ONLY` | 403 | `POST /auth/register` on a server with no users yet, from a local client but through a `Host` that isn't an IP address, `localhost`, a single-label name or a local-only suffix (`.local`, `.localhost`, `.home.arpa`, `.internal`, `.lan`, `.home`). Guards first-run setup against DNS rebinding. Open setup via the server's IP once, or set `ALLOW_PUBLIC_SETUP=true`. |
| `CSRF_BLOCKED` | 403 | A state-changing request authenticated by the browser cookie (no `Authorization: Bearer`) came from another origin (`Sec-Fetch-Site: same-site` / `cross-site`, or a foreign `Origin` not in the CORS allow-list). Also returned for a cross-origin, non-JSON body posted to a sign-in path (login, LDAP login, TOTP verify, register, invite accept). Written by middleware before the handler runs, so this envelope has no `request_id`. |

## Live TV / DVR

| Code | HTTP | Meaning |
|---|---|---|
| `LIVE_TV_NOT_CONFIGURED` | 503 | Server has Live TV subsystem disabled. |
| `ALL_TUNERS_BUSY` | 503 | No tune slots free on any configured tuner. |
| `STREAM_NOT_READY` | 504 | Tuner didn't produce a playlist in time. |
| `EPG_REFRESH_FAILED` | 502 | Upstream XMLTV/Schedules Direct fetch or parse failed. |
| `DVR_NOT_CONFIGURED` | 503 | DVR service not wired or disabled. |

## Photos + media

| Code | HTTP | Meaning |
|---|---|---|
| `IMAGE_SERVER_UNAVAILABLE` | 503 | On-demand image resizer not configured. |

## Backup / restore

| Code | HTTP | Meaning |
|---|---|---|
| `DUMP_NEWER_THAN_SERVER` | 409 | Dump file's schema is newer than the running binary. Client can retry with `?force=true`. |

## Transcode

| Code | HTTP | Meaning |
|---|---|---|
| `SOURCE_MISSING` | 422 | The source file's path doesn't resolve on disk. Typical causes: media drive unmounted, file deleted/moved since the last scan, network share offline. Re-scan the library to clear stale rows. |
| `SOURCE_UNREADABLE` | 422 | ffprobe pre-flight failed within the 5 s budget — the container is corrupt, the file is zero-length, or the header references streams the demuxer can't parse. Re-encode or replace the file. Surfaces a friendly error in the player instead of the historical 60 s "spinner forever" while ffmpeg hung on the bad input. |
| `TOO_MANY_SESSIONS` | 429 | Per-user concurrent session cap reached (default 5). Stop one of the user's existing sessions before starting another. |
| `FILE_DAMAGED` | 422 | `POST /items/{id}/transcode` on a file the opt-in `integrity_probe` task marked damaged (its stream fails software decode partway through even though the header parses). The playback-decision endpoint gives the same file a "damaged" verdict. Replace the file and re-scan. |
| `UNSUPPORTED_CONTAINER` | 415 | The file isn't a playable media container — typically an HLS/M3U playlist or other reference file in place of real media. Returned by `POST /items/{id}/transcode` and `POST /items/{id}/playback-decision`, before ffprobe or ffmpeg ever opens the file. |

Generic codes (`INTERNAL` / `NOT_FOUND` / `FORBIDDEN`) still cover the rest of
the transcode surface; when adding new specific failure modes (codec rejection,
encoder unavailable, supersede chain), introduce a new stable code rather than
reusing a generic one.

## Now Playing (admin stop)

| Code | HTTP | Meaning |
|---|---|---|
| `PLAYBACK_STOPPED` | 403 | An admin stopped this stream from Now Playing. For 2 minutes the same user, item and client IP get this on direct-play byte requests (`/media/stream/{id}`, `/media/download/{id}`), `POST /items/{id}/transcode` and `playing` progress reports. `message` carries the admin's note when one was given. The user's other devices and other titles aren't affected. |
| `STOP_UNSUPPORTED` | 409 | `POST /sessions/{id}/stop` on a Now Playing card the server can't tie to one viewer (`can_stop: false`: a direct play recorded without a user or item). The card drops out of Now Playing within a minute. |

## Problem reports + re-grab

| Code | HTTP | Meaning |
|---|---|---|
| `ALREADY_REPORTED` | 409 | `POST /items/{id}/issues`: the caller already has an open report of this kind for this item. |
| `TOO_MANY_OPEN_ISSUES` | 429 | `POST /items/{id}/issues`: a non-admin caller already has 10 open reports. |
| `NOT_OPEN` | 409 | `PATCH /admin/issues/{id}` closing a report that is already closed. |
| `NOT_MANAGED` | 409 | `POST /admin/items/{id}/regrab`: no enabled Radarr / Sonarr instance is configured, or none of them manages this title. |
| `ARR_UNAVAILABLE` | 502 | `POST /admin/items/{id}/regrab`: the instance that manages the title failed the re-grab, or an instance couldn't be reached while looking for the title. Details go to the server log. |

## Requests

| Code | HTTP | Meaning |
|---|---|---|
| `REQUESTS_DISABLED` | 403 | An admin turned off requesting for this account (`can_request = false`). Returned by `POST /requests` and `GET /discover/search`; never for an admin. |
| `TOO_MANY_PENDING_REQUESTS` | 429 | `POST /requests`: the caller already has 25 pending requests. Separate from the per-user quota. |

## Collections

| Code | HTTP | Meaning |
|---|---|---|
| `MANAGED_COLLECTION` | 409 | Update, delete, add-item or remove-item on a franchise collection. Its name and members are rebuilt from TMDB on every sync, so edits are refused. |

## Scrobbling

| Code | HTTP | Meaning |
|---|---|---|
| `SCROBBLE_NOT_CONFIGURED` | 409 | Last.fm / Trakt link attempted, but the admin hasn't entered that service's API credentials. |
| `SCROBBLE_APP_REJECTED` | 502 | Last.fm / Trakt turned down the server's API credentials (wrong key or secret, unknown client); an admin fixes them in Settings. |
| `SCROBBLE_UPSTREAM` | 502 | Last.fm / Trakt couldn't be reached, or answered unexpectedly, during a link. |

## Audiobooks

| Code | HTTP | Meaning |
|---|---|---|
| `BOOKMARK_LIMIT` | 409 | The caller already has 1000 bookmarks in this book. |

## Contract guarantees

1. **Codes are stable.** Once shipped, a code never changes meaning. Renames
   require a parallel introduction period with both codes returned.
2. **Codes are uppercase snake_case.** Never kebab-case, never spaces.
3. **`message` is mutable.** It's aimed at humans debugging; don't parse it.
4. **`request_id` is present on every error.** Copy it into bug reports —
   it maps to a server log line via the `request_id` slog field.

## Adding a new code

When introducing a new stable error code:

1. Use `respond.Error(w, r, STATUS, "YOUR_CODE", message)` at the emission site.
2. Add a row to this doc in the appropriate section.
3. Don't reuse a generic code — be specific even for low-volume paths.
