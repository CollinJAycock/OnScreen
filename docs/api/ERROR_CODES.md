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

Transcode errors surface today as generic `INTERNAL` / `NOT_FOUND` / `FORBIDDEN`.
When adding new transcode-specific failure modes (codec rejection, session
supersede, encoder unavailable), introduce a new stable code rather than
reusing a generic one.

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
