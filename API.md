# OnScreen API Reference

All endpoints are under `/api/v1/` unless otherwise noted. Responses use a
standard envelope:

```json
{ "data": { ... } }                 // single resource
{ "data": [ ... ], "meta": { ... }} // list
{ "error": { "code": "...", "message": "...", "request_id": "..." } } // error
```

Authentication uses httpOnly cookies set by `/auth/login` or `/auth/refresh`.
Admin-only endpoints are marked with **[admin]**.

---

## Setup & Status

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/setup/status` | none | Check if initial admin account exists |
| GET | `/email/enabled` | none | Whether SMTP is configured |
| GET | `/auth/forgot-password/enabled` | none | Whether password reset is available |
| GET | `/auth/google/enabled` | none | Whether Google SSO is configured |
| GET | `/auth/github/enabled` | none | Whether GitHub SSO is configured |
| GET | `/auth/discord/enabled` | none | Whether Discord SSO is configured |

---

## Authentication

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/auth/login` | none | Login with username + password. Sets httpOnly cookie. |
| POST | `/auth/refresh` | cookie | Rotate access token using refresh token cookie. |
| POST | `/auth/logout` | cookie | Revoke session and clear cookies. |
| POST | `/auth/register` | none* | Register a new user. First user becomes admin. Subsequent registrations require admin cookie. |
| POST | `/auth/forgot-password` | none | Send password reset email (if SMTP configured). |
| POST | `/auth/reset-password` | none | Complete password reset with token from email. |
| POST | `/auth/pin-switch` | cookie | Switch to a managed profile using PIN. |
| PUT | `/users/me/pin` | cookie | Set or update current user's PIN. |
| DELETE | `/users/me/pin` | cookie | Clear current user's PIN. |

**Rate limited**: All auth endpoints share a 10 req/min per IP limit.

### OAuth / SSO

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/auth/google` | none | Redirect to Google OAuth consent screen |
| GET | `/auth/google/callback` | none | Google OAuth callback (sets cookies, redirects to `/`) |
| GET | `/auth/github` | none | Redirect to GitHub OAuth consent screen |
| GET | `/auth/github/callback` | none | GitHub OAuth callback |
| GET | `/auth/discord` | none | Redirect to Discord OAuth consent screen |
| GET | `/auth/discord/callback` | none | Discord OAuth callback |

---

## Libraries

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/libraries` | user | List all libraries. |
| GET | `/libraries/{id}` | user | Get library details. |
| POST | `/libraries` | **[admin]** | Create a new library. |
| PATCH | `/libraries/{id}` | **[admin]** | Update library settings. |
| DELETE | `/libraries/{id}` | **[admin]** | Delete a library and all its items. |
| POST | `/libraries/{id}/scan` | **[admin]** | Trigger a library scan. |
| GET | `/libraries/{id}/items` | user | List items in a library. Supports `limit`, `offset`, `type`, `genre`, `sort`, `sort_dir` query params. |
| GET | `/libraries/{id}/genres` | user | List distinct genres in a library. |

---

## Items

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/items/{id}` | user | Get full item detail (includes files, artwork, metadata). |
| GET | `/items/{id}/children` | user | List child items (seasons of a show, episodes of a season, tracks of an album). |
| PUT | `/items/{id}/progress` | user | Save playback progress. Body: `{ "view_offset_ms", "duration_ms", "state" }` |
| POST | `/items/{id}/enrich` | **[admin]** | Trigger on-demand metadata enrichment. |
| GET | `/items/{id}/match/search` | **[admin]** | Search TMDB for match candidates. Query param: `q`. |
| POST | `/items/{id}/match` | **[admin]** | Apply a TMDB match. Body: `{ "tmdb_id" }` |

---

## Transcoding (HLS)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/items/{id}/transcode` | user | Start a transcode/remux session. Returns `session_id`, `playlist_url`, `token`. |
| DELETE | `/transcode/sessions/{sid}` | user | Stop a transcode session (kills FFmpeg). |
| GET | `/transcode/sessions/{sid}/playlist.m3u8` | token | Get HLS playlist. Query param: `token`. |
| GET | `/transcode/sessions/{sid}/seg/{name}` | token | Get HLS segment. Query param: `token`. |
| GET | `/sessions` | user | List active transcode sessions. |

### Transcode Start Request

```json
{
  "file_id": "optional-uuid",   // specific file; defaults to best quality
  "height": 1080,               // 0 = source resolution; max: TRANSCODE_MAX_HEIGHT
  "position_ms": 30000,         // start offset
  "video_copy": false           // true = remux (copy video, transcode audio only)
}
```

---

## Collections

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/collections` | user | List all collections (playlists + auto-genre). |
| POST | `/collections` | user | Create a playlist. Body: `{ "name" }` |
| GET | `/collections/{id}` | user | Get collection details. |
| PATCH | `/collections/{id}` | user | Update collection name. |
| DELETE | `/collections/{id}` | user | Delete a collection. |
| GET | `/collections/{id}/items` | user | List items in a collection. |
| POST | `/collections/{id}/items` | user | Add an item. Body: `{ "item_id" }` |
| DELETE | `/collections/{id}/items/{itemId}` | user | Remove an item from collection. |

---

## Users & Profiles

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/users` | **[admin]** | List all users. |
| GET | `/users/switchable` | user | List profiles available for switching. |
| PATCH | `/users/{id}` | **[admin]** | Set user admin status. |
| PUT | `/users/{id}/password` | **[admin]** | Reset a user's password. |
| DELETE | `/users/{id}` | **[admin]** | Delete a user. |
| GET | `/profiles` | **[admin]** | List managed profiles. |
| POST | `/profiles` | **[admin]** | Create a managed profile. |
| PATCH | `/profiles/{id}` | **[admin]** | Update profile name. |
| DELETE | `/profiles/{id}` | **[admin]** | Delete a managed profile. |

---

## Invites

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/invites` | **[admin]** | Create an invite token. |
| DELETE | `/invites/{id}` | **[admin]** | Revoke an invite. |
| POST | `/invites/accept` | none | Accept an invite and create a user account. |

---

## Webhooks

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/webhooks` | **[admin]** | List webhook endpoints. |
| POST | `/webhooks` | **[admin]** | Create a webhook endpoint. Body: `{ "url", "secret", "events" }` |
| PATCH | `/webhooks/{id}` | **[admin]** | Update a webhook endpoint. |
| DELETE | `/webhooks/{id}` | **[admin]** | Delete a webhook endpoint. |
| POST | `/webhooks/{id}/test` | **[admin]** | Send a test event to a webhook. |

Webhook payloads are signed with HMAC-SHA256 if a secret is configured.
The signature is in the `X-OnScreen-Signature` header as `sha256=<hex>`.

### Webhook Events

- `media.play`, `media.pause`, `media.resume`, `media.stop`, `media.scrobble`
- `library.scan.complete`

---

## Hub & Search

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/hub` | user | Home screen data: continue watching + recently added per library. |
| GET | `/search` | user | Full-text search across all libraries. Query param: `q`, optional `limit` (max 100). |
| GET | `/history` | user | Watch history. Supports `limit`, `offset`. |

---

## Settings

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/settings` | **[admin]** | Get server settings. |
| PATCH | `/settings` | **[admin]** | Update server settings. |

---

## Email

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/email/test` | **[admin]** | Send a test email to verify SMTP configuration. |

---

## Audit Log

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/audit` | **[admin]** | List audit log entries. Supports `limit` (default 50, max 200), `offset`. |

---

## Filesystem

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/fs/browse` | **[admin]** | Browse server filesystem for library path selection. Query param: `path`. |

---

## Media Streaming (non-API paths)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/media/stream/{id}` | cookie | Stream a media file directly (direct play). |
| GET | `/media/subtitles/{fileId}/{streamIndex}` | cookie | Extract and serve a subtitle track as WebVTT. |
| GET | `/media/files/*` | cookie | Serve raw media files (legacy Plex-compatible path). |
| GET | `/artwork/*` | none | Serve artwork images (posters, fanart, thumbnails). |

---

## Health

These endpoints are outside `/api/v1/`:

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/health/live` | none | Liveness probe (always 200). |
| GET | `/health/ready` | none | Readiness probe (checks DB + Valkey connectivity). |

---

## Arr Integration

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/arr/webhook` | API key | Receive Sonarr/Radarr webhook events. Authenticated via `X-Arr-ApiKey` header. |

---

## Error Codes

| HTTP Status | Code | Description |
|-------------|------|-------------|
| 400 | `BAD_REQUEST` | Invalid request body or parameters |
| 401 | `UNAUTHORIZED` | Missing or invalid authentication |
| 403 | `FORBIDDEN` | Authenticated but insufficient permissions |
| 404 | `NOT_FOUND` | Resource not found |
| 429 | `RATE_LIMITED` | Rate limit exceeded (see `X-RateLimit-*` headers) |
| 500 | `INTERNAL` | Server error |

Rate limit headers on all authenticated endpoints:
- `X-RateLimit-Limit` — requests allowed per window
- `X-RateLimit-Remaining` — requests remaining
- `X-RateLimit-Reset` — Unix timestamp when the window resets
