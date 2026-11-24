# Upstream failures behind Cloudflare

> **Status: proposal, 2026-11-24.** Nothing has changed yet. This needs a
> decision on the status code (§4) before any code is written.

## 1. The problem

When OnScreen can't reach an outside service (TMDB, a Plex or Jellyfin
server being imported, a podcast feed, Last.fm, an \*arr instance, an LDAP
server), it answers **502** or **504** with its usual JSON error: a code the
apps act on and a message in the caller's language.

Cloudflare replaces the body of an origin **502 or 504** with its own HTML
error page. This was seen on QA on 2026-10-13: `POST /admin/imports/connect`
answered a 231-byte JSON 502 (`IMPORT_SOURCE_UNAVAILABLE`, "the server could
not be reached: …"), the browser got Cloudflare's page, and the web app
could only show "HTTP 502". QA and the review server both sit behind
Cloudflare, and so does any self-hoster using a Cloudflare proxy or tunnel.

Cloudflare's documentation says its Error Pages **do not apply to 500, 501,
503 or 505** ("to avoid issues with specific API endpoints"), and 4xx
responses pass through untouched. Keeping an origin's 502/504 body is
Enterprise-only ("Origin Error Pages"). Custom Error Rules (paid plans)
replace the body with content you define, so they can't bring the server's
message back. So the fix has to come from the server.

### The case that costs more than a message

`GET /media/subtitles/{fileId}/{streamIndex}` answers **504
`SUBTITLE_PREPARING`** when an embedded track's extraction has *failed*.
The TV web apps (Tizen, webOS, Xbox: `classifySubtitleFailure`) read that
code and make it an ordinary counted retry. Behind Cloudflare the code is
gone. They see a bare 504, which they rightly treat as a proxy timing out
on a slow extraction, and **keep polling for up to 8 minutes**. After a
failure, each of those requests re-runs the whole-file demux on the server:
25 to 60 seconds of disk and CPU for a 4K remux.

## 2. Every 502/504 the API sends

| Where | Today | Code(s) | Proposal |
|---|---|---|---|
| `POST /auth/login` (LDAP server unreachable) | 502 | `error.ldap.upstream` | **503** |
| `GET /discover/tv/{tmdb_id}/seasons` (TMDB lookup) | 502 | `TMDB_LOOKUP_FAILED` | **503** |
| `/admin/imports/*` (Plex, Jellyfin or Emby unreachable), 4 sites | 502 | `IMPORT_SOURCE_UNAVAILABLE` | **503** |
| `POST /admin/items/{id}/regrab` (\*arr), 2 sites | 502 | `ARR_UNAVAILABLE` | **503** |
| `POST /tv/epg-sources/{id}/refresh` | 502 | `EPG_REFRESH_FAILED` | **503** |
| Podcast subscribe and refresh, 2 sites | 502 | `FEED_UNAVAILABLE` | **503** |
| Last.fm / Trakt linking, 2 sites | 502 | `SCROBBLE_APP_REJECTED`, `SCROBBLE_UPSTREAM` | **503** |
| Subtitle search and download (OpenSubtitles), 2 sites | 502 | `SUBTITLE_PROVIDER_ERROR` | **503** |
| `GET /media/subtitles/…` (extraction failed) | 504 | `SUBTITLE_PREPARING` | **500**, code unchanged |
| `GET /tv/channels/{id}/stream.m3u8` (tuner not ready) | 504 | `STREAM_NOT_READY` | keep |
| Transcode segment relayed from a worker (`proxyWorkerFile`), plain text, 2 sites | 502 | none | keep |

The last two stay as they are. Their clients act on the status alone: the
TV apps map a live-TV 504 to "the tuner didn't start in time", and hls.js
and Media3 retry a 5xx segment. Nobody reads those bodies, and moving a
segment to 4xx could make a player drop the rendition instead of retrying.

**Why 500 for the subtitle case:** the failure is the server's own (ffmpeg
couldn't extract the track), not an outside service. 500 passes through
Cloudflare, so the apps read `SUBTITLE_PREPARING` again. Even an app that
never reads codes doesn't count 500 among its "still preparing" statuses
(502, 503, 504, 520, 524), so the 8-minute poll can't happen. The code
keeps its misleading name, because the TV apps match on it.

## 3. Client check: nothing needs to change

Every place a first-party client branches on 502, 503 or 504 was checked
(web, Android TV, phone and Quest, Tizen, webOS, Xbox, Roku, Kodi,
desktop):

- **503 branches always check the code too:** `BURN_IN_UNAVAILABLE`
  (Android TV, phone, Roku, TV web apps) and `SUBTITLES_NOT_CONFIGURED`
  (web player). The new 503s carry other codes, so they fall through to
  the generic error, which shows the server's message.
- **Live-TV 503/504 mapping** (`liveTune.ts`) applies only to the stream
  playlist, which isn't changing.
- **The subtitle classifier** checks `SUBTITLE_PREPARING` before the
  status, so 504 → 500 keeps its behaviour. Media3's
  `DefaultLoadErrorHandlingPolicy` (phone) retries 500 and 504 alike.
- **The auth paths' "503 = couldn't check, don't sign out"** rule applies to
  token refresh, which isn't changing. An LDAP-unreachable login already
  failed, and now says why.

So this is a server-only change, like v3's removals. The API contract
changes (OpenAPI lists ten of these 502 responses), so it gets an upgrade
note, but no OnScreen app is affected.

## 4. Choosing the status

**Recommended: 503.** It is the one 5xx Cloudflare documents as left alone
for APIs. Upstream outages still count as server-side failures, so the
monitoring pack's `OnScreenHigh5xxRate` alert keeps seeing them (these
routes are user-triggered and low-volume, so they won't trip it on their
own). And no OnScreen client treats a bare 503 on these routes specially.
It is also no worse than 502 on meaning: OnScreen isn't really a gateway
for TMDB either.

**Alternative: 424 Failed Dependency** (or 422, which the import already
uses for `IMPORT_SOURCE_UNAUTHORIZED`). A 4xx passes every proxy, including
ones set to intercept 5xx, and keeps upstream outages out of the 5xx
alert. But 4xx says the caller did something wrong. Generic HTTP tooling
won't retry it, and an operator's dashboard would show TMDB being down as
client errors.

**Not recommended:** asking every operator to configure their proxy. On
Cloudflare that needs an Enterprise plan, and the default affects everyone
else.

## 5. If approved: the change

One server PR:

1. The 16 sites in §2 move to 503, or 500 for the subtitle case. Each keeps
   its code and message.
2. Handler tests: the 14 references to 502/504 in 7 test files move with them.
3. `docs/api/openapi.yaml`: the ten 502 entries become 503 and the
   subtitle route documents its 500. CHANGELOG `[Unreleased]` gets an entry
   plus an upgrade note: "upstream failures answer 503 instead of 502 and
   504, so the message survives Cloudflare".
4. **Check before merging, on QA through Cloudflare:** one 503 (e.g.
   `/admin/imports/connect` to an unreachable address) and one 500 should
   arrive with their JSON bodies. The documentation is clear about 503,
   but one community report describes a 500 turned into a 520 on a Pro
   plan, so test it rather than assume it.

Size: a small PR. No client release, and no migration.

## Sources

- Cloudflare, Custom Errors: <https://developers.cloudflare.com/rules/custom-errors/>
- Cloudflare, Error 502 or 504: <https://developers.cloudflare.com/support/troubleshooting/http-status-codes/cloudflare-5xx-errors/error-502-504/>
- Cloudflare, error responses: <https://developers.cloudflare.com/fundamentals/reference/error-responses/>
