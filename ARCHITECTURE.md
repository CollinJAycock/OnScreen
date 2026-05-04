# OnScreen

> *"On screen."* — Star Trek
>
> A modern, open-source media server built for correctness, simplicity, and high availability.
> PostgreSQL-native. Single binary. Native web player.

---

## Table of Contents

1. [Why OnScreen](#why-onscreen)
2. [Core Design Principles](#core-design-principles)
3. [Technology Stack](#technology-stack)
4. [System Architecture](#system-architecture)
5. [Repository Structure](#repository-structure)
6. [Database Schema](#database-schema)
7. [API Reference](#api-reference)
8. [Data Flows](#data-flows)
9. [Scan Pipeline](#scan-pipeline)
10. [Transcode Pipeline](#transcode-pipeline)
11. [Authentication Flow](#authentication-flow)
12. [Configuration](#configuration)
13. [Observability](#observability)
14. [Security](#security)
15. [Known Issues & Technical Debt](#known-issues--technical-debt)
16. [Architectural Decisions](#architectural-decisions)
17. [Development Phases](#development-phases)

---

## Why OnScreen

Every existing self-hosted media server (Plex, Jellyfin, Emby) shares the same fundamental architectural flaws:

- **SQLite** — write serialization under concurrent load; no horizontal scaling; no HA
- **Mutable watch state** — no audit trail; hard to derive "resume from" correctly
- **Coupled transcode** — transcoding and API share process; one dies, both die
- **No real observability** — no metrics, no structured logs, no distributed tracing

OnScreen fixes all of this from scratch. It is **not** a Plex clone — it ships its own native web player and API, with no dependency on Plex clients or Plex.tv.

---

## Core Design Principles

1. **PostgreSQL-native** — schema designed for Postgres from day one. Not a SQLite port.
2. **Stateless API tier** — any number of API instances run behind a load balancer.
3. **Event-sourced watch state** — every play/pause/stop/seek is an immutable event. Current state is derived, never mutated.
4. **Native client** — ships its own SvelteKit web player. No dependency on Plex, Infuse, or any third-party client.
5. **Single binary** — `go build` produces one executable. Runtime dependencies: PostgreSQL and Valkey only.
6. **Plain SQL** — queries are `.sql` files compiled to type-safe Go via `sqlc`. No ORM, no hidden N+1s.
7. **Explicit over magic** — any request can be traced from router to DB in under 5 minutes.

---

## Technology Stack

### Server

| Layer | Choice | Rationale |
|---|---|---|
| Language | Go 1.25+ | Single binary, maintainable, fast ramp |
| Router | Chi v5 | Lightweight, idiomatic Go, middleware-friendly |
| Database | PostgreSQL 16+ | MVCC, materialized views, pgvector, FTS |
| DB driver | pgx/v5 | Native Postgres; better than `database/sql` |
| Query gen | sqlc | Type-safe queries from raw SQL |
| Migrations | goose v3 | SQL-first, simple |
| Cache / queue | Valkey (Redis OSS fork) | Sessions, transcode job queue, rate-limit buckets, plugin credential cache |
| Auth | Paseto v4 local + 24h per-file stream tokens | No algorithm confusion attacks; symmetric; native players bypass header-auth so streams need their own token |
| Config | env vars (bootstrap) + `server_settings` table | 12-factor for what's needed pre-DB; everything else in DB so admin UI can edit at runtime |
| Transcoding | FFmpeg via os/exec | Industry standard; NVENC / QSV / AMF / VAAPI / SVT-AV1 / libx264 / libx265 fallback |
| Frontend | SvelteKit + TypeScript | Small bundle; no React overhead; SPA mode (`ssr=false, prerender=false`) |
| Logging | `log/slog` | Stdlib structured logging; trace_id / span_id auto-injected when a span is active |
| Metrics | Prometheus | Scrape-based; standard |
| Tracing | OpenTelemetry (OTLP/gRPC) | Vendor-neutral; configured in admin Settings → Observability |
| Plugin host | MCP (outbound only) | OnScreen calls out to plugin processes; inbound MCP rejected as a security stance |

### Clients

| Client | Stack | Notable |
|---|---|---|
| Web | SvelteKit + TypeScript + hls.js | Same bundle reused inside the desktop Tauri shell |
| Desktop | Tauri 2 + Rust audio engine outside the webview | symphonia 0.5 decoder; raw `wasapi` in `AUDCLNT_SHAREMODE_EXCLUSIVE` (bit-perfect); DSF + DoP for DSD; OS keychain for token storage; `souvlaki` for OS now-playing |
| Android TV / Fire TV | Kotlin + AndroidX Leanback + Media3 1.3 | Hilt DI, Retrofit + Moshi, Coil; Watch Next launcher row via `androidx.tvprovider` |
| Android phone | Kotlin + Jetpack Compose | Reuses TV client's data layer verbatim; WorkManager-backed offline downloads |
| LG webOS / Samsung Tizen | SvelteKit + ares-package / tizen-package | Bulk-ported; remote-key overlays |
| Roku | BrightScript + SceneGraph | `Playback_Decide()` covered by 13 brs unit tests |

---

## System Architecture

```
                         ┌─────────────────────────────┐
                         │      Load Balancer           │
                         │   (Nginx / HAProxy / CF)     │
                         └──────────┬──────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              │                     │                     │
   ┌──────────▼──────┐   ┌──────────▼──────┐   ┌─────────▼───────┐
   │   OnScreen API  │   │   OnScreen API  │   │  OnScreen API   │
   │   Instance 1    │   │   Instance 2    │   │  Instance 3     │
   │   (stateless)   │   │   (stateless)   │   │  (stateless)    │
   └──────────┬──────┘   └──────────┬──────┘   └─────────┬───────┘
              │                     │                     │
              └─────────────────────┼─────────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              │                     │                     │
   ┌──────────▼──────┐   ┌──────────▼──────┐   ┌─────────▼───────┐
   │   PostgreSQL    │   │     Valkey       │   │   Transcode     │
   │   Primary       │   │  - sessions      │   │   Workers       │
   │                 │   │  - job queue     │   │  (cmd/worker    │
   │  - Metadata     │   │  - rate limits   │   │   or embedded)  │
   │  - Watch events │   │  - heartbeats    │   └─────────┬───────┘
   │  - Auth tokens  │   └─────────────────┘             │
   │  - Webhooks     │                         ┌─────────▼───────┐
   └─────────────────┘   ┌─────────────────┐   │     FFmpeg      │
                         │   PG Replica    │   │  (HLS output    │
                         │  (read scale)   │   │   → /tmp/…)     │
                         └─────────────────┘   └─────────────────┘
                                    │
                         ┌──────────▼──────────┐
                         │   Shared Storage    │
                         │   (NFS / local)     │
                         │  - media files      │
                         │  - artwork          │
                         │  - HLS segments     │
                         └─────────────────────┘
```

### Single-Node (Default)

In the default deployment, `cmd/server` embeds the transcode worker in-process. The standalone `cmd/worker` binary is available for multi-node setups where the transcode tier scales independently.

---

## Repository Structure

```
OnScreen/
├── cmd/
│   ├── server/         HTTP API server + embedded transcode worker
│   └── worker/         Standalone transcode + maintenance worker (separate binary)
├── internal/           ~50 packages — server-side code
│   ├── api/v1/         All HTTP handlers (auth, items, libraries, transcode, tv,
│   │                   photo, audiobook, books, plugins, requests, settings, …)
│   ├── auth/           Paseto v4 issuance + validation; OIDC, OAuth, SAML, LDAP providers
│   ├── audit/          Append-only audit log of admin / playback / auth events
│   ├── config/         Env-var bootstrap config (only what's needed pre-DB)
│   ├── db/
│   │   ├── gen/        sqlc-generated query wrappers
│   │   └── migrations/ 70+ goose migrations (schema, partitions, audiophile,
│   │                   live_tv, dvr, photo_albums, audiobook hierarchy, etc.)
│   ├── domain/         Library, media, profile, library_access, settings, watchevent
│   ├── livetv/         HDHomeRun / M3U tuners, EPG (Schedules Direct + XMLTV),
│   │                   DVR matcher + recording worker, refcounted HLS proxy
│   ├── metadata/       TMDB / TVDB / MusicBrainz / OpenLibrary / Wikipedia clients
│   ├── notification/   In-app SSE notifications + webhook dispatch (HMAC-SHA256)
│   ├── observability/  slog handlers, Prometheus metrics, OTel setup, health checks
│   ├── photoimage/     On-demand photo resize endpoint with disk cache
│   ├── plugin/         MCP-based outbound plugin host
│   ├── scanner/        Recursive scan, hash + ffprobe, mtime+size fast-skip,
│   │                   per-type modules (audiobook, book_cbz/cbr/epub, home_video)
│   ├── streaming/      Direct-play "Now Playing" tracker; per-file stream tokens
│   ├── subtitles/      OCR (PGS/VOBSUB/DVB → WebVTT) + OpenSubtitles client
│   ├── transcode/      FFmpeg args, encoder auto-detect, session store,
│   │                   per-user supersede, multi-worker dispatcher
│   ├── trickplay/      Seek-bar thumbnail strip generation
│   ├── valkey/         go-redis/v9 wrapper
│   └── worker/         Periodic tasks (partition cleanup, scheduled scans,
│                       OCR, EPG refresh, DVR matcher, hub refresh)
├── web/                SvelteKit SPA (ssr=false, prerender=false)
│   └── src/routes/     /, /login, /setup, /libraries/[id], /watch/[id],
│                       /tv, /tv/[id], /tv/recordings, /photos, /audiobooks/[id],
│                       /books/[id], /search, /settings, /privacy, /account-deletion, …
├── clients/            First-party non-web clients
│   ├── desktop/        Tauri 2 + Rust native audio engine (Windows / macOS / Linux)
│   ├── android/        Android TV / Google TV / Fire TV (Leanback + Media3)
│   ├── android_native/ Android phone (Compose + Material 3)
│   ├── webos/          LG webOS (SvelteKit + ares-package)
│   ├── tizen/          Samsung Tizen (SvelteKit + tizen-package)
│   ├── roku/           Roku (BrightScript + SceneGraph)
│   └── firetv/         Fire-TV-specific assets (same APK as android/, separate listing)
├── docker/             Dockerfile, Dockerfile.gpu, Dockerfile.ffmpeg, compose stacks
├── docs/               Architecture decisions, deployment, manual test plan,
│                       v2 + v2.1 roadmaps, comparison matrix, plugin authoring
└── installer/          Windows MSI + Linux portable tarball builders
```

---

## Database Schema

### Design Principles

- All PKs are UUIDs (`gen_random_uuid()`)
- All timestamps are `TIMESTAMPTZ` (UTC always)
- Soft deletes via `deleted_at` — metadata never hard-deleted
- Enum-like columns use `TEXT + CHECK` — easier to extend than PG enums
- All FK columns are indexed
- Full-text search via generated `TSVECTOR` column (GIN index)

### Tables

| Table | Purpose |
|---|---|
| `libraries` | Media library roots; type, scan paths, scheduling intervals |
| `media_items` | Items (movie/show/season/episode/track/album/artist); hierarchy via `parent_id`. Music rows carry MusicBrainz IDs (recording/release/release-group/artist/album-artist), disc/track totals, original year, compilation flag, release type |
| `media_files` | Physical files; technical metadata (codec, resolution, HDR, streams); 3-state lifecycle. Audio files additionally carry bit depth, sample rate, channel layout, lossless flag, ReplayGain track + album gain/peak |
| `external_subtitles` | Sidecar subtitle tracks (OpenSubtitles downloads + OCR'd PGS/VOBSUB). UNIQUE on `(file_id, source, source_id)` so re-runs upsert in place |
| `intro_markers` | Intro / credits / recap timestamps (auto from chapter heuristics, or admin-set via API) |
| `trickplay_sheets` | Generated sprite-sheet thumbnails for HLS seek-bar previews |
| `users` | Local accounts; bcrypt password + optional PIN |
| `sessions` | Refresh token store; only hash stored, never raw token |
| `watch_events` | Immutable play/pause/stop events; monthly partitions (ADR-002) |
| `watch_state` | Materialized view: per-user per-item status (unwatched/in_progress/watched) |
| `hub_recently_added` | Materialized recently-added cache; refreshed by background worker |
| `webhook_endpoints` | Outbound webhook URLs; AES-256-GCM encrypted secrets |
| `webhook_failures` | Dead-letter for failed webhook deliveries |
| `server_settings` | Runtime config (TMDB API key, etc.) |

### File Lifecycle (ADR-011)

```
active  ──(file disappears)──►  missing  ──(grace period elapsed)──►  deleted
  ▲                                                                        │
  └──────────────────(file reappears / move detected via hash)────────────┘
```

Move detection: if a file disappears from path A but a new file at path B has the same SHA-256 hash, it's treated as a move rather than a delete + add.

---

## API Reference

All endpoints under `/api/v1/` require `Authorization: Bearer <access_token>` except where noted.

### Auth (no auth, IP rate-limited)

| Method | Path | Description |
|---|---|---|
| `GET` | `/setup/status` | `{setup_required: bool}` |
| `POST` | `/auth/register` | Create first user (admin); open only when no users exist |
| `POST` | `/auth/login` | `{username, password}` → `{access_token, refresh_token}` |
| `POST` | `/auth/refresh` | `{refresh_token}` → new `{access_token}` |
| `POST` | `/auth/logout` | Invalidates refresh token |

### Libraries

| Method | Path | Description |
|---|---|---|
| `GET` | `/libraries` | List all libraries |
| `POST` | `/libraries` | Create library |
| `GET` | `/libraries/{id}` | Get library |
| `PATCH` | `/libraries/{id}` | Update (name, paths, agent, language, interval) |
| `DELETE` | `/libraries/{id}` | Soft-delete |
| `POST` | `/libraries/{id}/scan` | Enqueue async full scan → 204 |
| `GET` | `/libraries/{id}/items` | Paginated item list |

### Media Items

| Method | Path | Description |
|---|---|---|
| `GET` | `/items/{id}` | Item detail + files + watch offset |
| `GET` | `/items/{id}/children` | Child items (seasons, episodes) |
| `PUT` | `/items/{id}/progress` | Record watch event (play/pause/stop) |
| `POST` | `/items/{id}/enrich` | Re-run TMDB enrichment in background → 204 |

### Playback (no auth — UUID is the credential)

| Method | Path | Description |
|---|---|---|
| `GET` | `/media/stream/{file_uuid}` | Direct file serve (byte-range) |
| `GET` | `/media/files/*` | Legacy direct-play path |
| `GET` | `/artwork/*` | Serve artwork images from MEDIA_PATH |

### HLS Transcode (segment auth via query token)

| Method | Path | Description |
|---|---|---|
| `POST` | `/items/{id}/transcode` | Start HLS session; returns session_id + playlist URL + token |
| `DELETE` | `/transcode/sessions/{sid}` | Stop and clean up session |
| `GET` | `/transcode/sessions/{sid}/playlist.m3u8?token=…` | HLS playlist (rewritten segment URLs) |
| `GET` | `/transcode/sessions/{sid}/seg/{name}?token=…` | Serve individual .ts segment |

### Other

| Method | Path | Description |
|---|---|---|
| `GET` | `/sessions` | Active sessions (Now Playing) |
| `GET` | `/analytics` | Watch stats |
| `GET/PATCH` | `/settings` | Server settings (TMDB key) |
| `GET/POST/PATCH/DELETE` | `/webhooks` | Webhook CRUD |
| `POST` | `/webhooks/{id}/test` | Send test payload |
| `PUT/DELETE` | `/users/me/pin` | Set / clear 4-digit PIN |
| `GET` | `/fs/browse` | Directory browser (for path picker) |

---

## Data Flows

### Scan Pipeline

```
POST /libraries/{id}/scan
        │
        ▼
scanEnqueuer.EnqueueScan()         (goroutine; context.WithoutCancel)
        │
        ▼
Scanner.ScanLibrary()
  1. filepath.WalkDir  →  collect media file paths
  2. Per-file goroutines (bounded by SCAN_FILE_CONCURRENCY):
       a. os.Stat (mtime + size)
       b. HashFile (SHA-256 partial; cached by mtime+size)
       c. Fast path: if path in DB AND hash matches AND poster exists
            └─ MarkFileActive → return          ← skips ffprobe entirely
       d. Slow path (new or changed):
            ├─ ProbeFile (ffprobe; 50MB cap; 30s timeout)
            ├─ FindOrCreateItem (title+year from filename)
            └─ CreateOrUpdateFile (upsert)
       e. If item has no poster: queue for enrichment
  3. wg.Wait()  ← file I/O phase complete
  4. Enrich goroutines (bounded at 4):
       └─ TMDB search (cleaned title + year)
          ├─ UpdateItemMetadata (title, year, summary, rating, genres)
          └─ DownloadPoster + DownloadFanart → poster_path saved
        │
        ▼
MarkScanCompleted → update scan_last_completed_at
watchLibrary → start fsnotify watcher (500ms debounce)
```

**Title cleaning** (`cleanTitle`): normalises `_` and `.` separators, extracts 4-digit year, strips everything after (resolution tags, source tags, group names). Used by both `parseFilename` (new items) and `enrichMovie` (existing items with garbled stored titles).

**Fast path**: unchanged file (hash cache hit + DB hash match) → only `MarkFileActive` + item poster check. Skips ffprobe and all metadata DB writes. Re-scans of settled libraries complete in milliseconds.

### Transcode Pipeline

```
POST /items/{id}/transcode?height=720
        │
        ▼
NativeTranscodeHandler.Start()
  - Select best media file
  - Calculate output dims (aspect-ratio preserving)
  - Create Session → Valkey (TTL 4h)
  - Create TranscodeJob → Valkey queue (RPush)
  - Issue segment JWT (session-scoped)
  - Return {session_id, playlist_url, token}
        │
        ▼
Worker.jobLoop()  (embedded or cmd/worker)
  - BLPop from Valkey queue
  - Build ffmpeg args:
      NVENC:    -hwaccel cuda + hwupload_cuda,scale_npp
      VAAPI:    scale_vaapi
      Software: libx264 + scale+pad
  - Exec: ffmpeg -i input … -hls_time 6 -hls_list_size 0 session_dir/index.m3u8
  - Heartbeat every 2s → Valkey (TTL 10s)
        │
        ▼
GET /transcode/sessions/{sid}/playlist.m3u8?token=…
  - Validate segment JWT
  - Wait up to 10s for index.m3u8 to appear
  - Rewrite segment URIs: seg0.ts → /api/v1/transcode/sessions/{sid}/seg/seg0.ts?token=…
  - Serve playlist
        │
        ▼
GET /transcode/sessions/{sid}/seg/{name}?token=…
  - Validate JWT
  - http.ServeFile(session_dir/name)
```

**HLS seek**: client requests `/items/{id}/transcode?height=720&start_sec=3600`; server passes `-ss 3600` to ffmpeg and records `hlsOffsetSec` in session. Player adds offset to `videoEl.currentTime` for correct timeline display.

**Now Playing** (`GET /sessions`): merges Valkey transcode sessions (with 2-minute activity timeout via `LastActivityAt`) and HTTP byte-range tracker entries from `streaming.Tracker`.

### Authentication Flow

```
POST /auth/login
  - bcrypt.Compare(password, stored_hash)
  - Issue Paseto v4 access token (15m TTL)
  - Issue opaque refresh token → hash → store in sessions table
  - Return {access_token, refresh_token}

Authenticated request:
  - middleware.Authenticator extracts Bearer token
  - Paseto.Validate → claims{user_id, username, is_admin}
  - Attach to request context

401 → POST /auth/refresh
  - SHA-256 hash refresh token → look up sessions table
  - Check expiry + last_seen
  - Issue new access token; slide refresh token expiry
  - Return {access_token}
```

### Watch Event Flow

```
PUT /items/{id}/progress  {view_offset_ms, duration_ms, state: "playing"|"paused"|"stopped"}
  - watchevent.Record() → INSERT watch_events (immutable)
  - streaming.Tracker.SetItemState() → update in-memory position
  - state == "stopped":
      SessionStore.DeleteByMedia() → remove Valkey session immediately
  - state != "stopped":
      SessionStore.UpdatePositionByMedia() → refresh position + LastActivityAt

watch_state (materialized view):
  - status = "watched"     if position_ms / duration_ms > 0.90
  - status = "in_progress" if position_ms > 0 and not watched
  - status = "unwatched"   otherwise
  - Used by GET /items/{id} to return view_offset_ms for resume
```

---

## Configuration

Two layers: **bootstrap env vars** (required to bind sockets and reach the database before the admin UI exists) and the **`server_settings` table** (everything else, edited via Settings → ... in the web admin and bootstrap-read at startup).

### Bootstrap env vars

| Var | Required | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | ✓ | — | PostgreSQL DSN (`postgres://user:pass@host:5432/db?sslmode=disable`) |
| `VALKEY_URL` | ✓ | — | Valkey/Redis URL (`redis://host:6379`) |
| `MEDIA_PATH` | ✓ | — | Root directory where media files live |
| `SECRET_KEY` | ✓ | — | 32-byte key for Paseto tokens + secret encryption (hex, base64, or raw) |
| `DATABASE_RO_URL` | | `DATABASE_URL` | Read replica DSN |
| `CACHE_PATH` | | `$MEDIA_PATH/.cache/artwork` | Artwork resize cache |
| `LISTEN_ADDR` | | `:7070` | API server bind address |
| `METRICS_ADDR` | | `:7071` | Prometheus metrics bind |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | | — | Built-in HTTPS (operator-provided PEM) |
| `TMDB_API_KEY` | | — | Seeded into Settings on first run |
| `TVDB_API_KEY` | | — | Seeded into Settings on first run |
| `OS_AUTH_RATE_LIMIT_PER_MIN` | | `10` | Auth-route rate-limit override (test/dev) |
| `OS_TRANSCODE_START_RATE_LIMIT_PER_MIN` | | `10` | Transcode-start rate-limit override |

### Settings UI (stored in `server_settings`)

These were env vars in v1.x and earlier; they're now table-stored under typed keys (`general_config`, `smtp_config`, `otel_config`, `oidc_config`, `saml_config`, `ldap_config`, `oauth_*_config`, `transcode_config`, `scan_config`, `tmdb_config`, …):

- Public URL, log level, CORS allow-list (`general_config`)
- SMTP host / port / credentials / from-address (`smtp_config`)
- OTLP endpoint, sample ratio, deployment-environment tag (`otel_config`)
- OIDC issuer / client / scopes (`oidc_config`)
- SAML EntityID / IdP metadata / signing keys (`saml_config`)
- LDAP host / bind-DN / search base / group sync (`ldap_config`)
- OAuth provider credentials (Google / GitHub / Discord)
- Transcode quality cap, max sessions, encoder filters, NVENC preset / tune / rate control
- Scan concurrency (per-file, per-library), missing-file grace period, retention months
- TMDB / TVDB rate limits

A **bootstrap one-shot `pgx.Conn`** reads these at process startup so the logger, OTel tracer provider, and HTTP handlers can be built with the right config before the main pool opens. Restart required after changes; the UI surfaces this notice on each settings tab.

> SIGHUP hot-reload was removed in v2.1 along with the env-var-resident knobs that used it. Settings changes flow through the DB and require a process restart to take effect.

---

## Observability

| Signal | Implementation |
|---|---|
| **Structured logs** | `log/slog` JSON to stdout; request ID on every log line; `trace_id`/`span_id` auto-added when a span is active |
| **Metrics** | Prometheus; exposed at `METRICS_ADDR/metrics` |
| **Tracing** | OpenTelemetry (OTLP/gRPC); configured in Settings → Observability and read once at startup (restart required). Auto-instruments HTTP (otelchi) + pgx (otelpgx). Custom spans on `scanner.library` and `transcode.run_job`. |
| **Health** | `GET /health/live` (always 200); `GET /health/ready` (checks PG + Valkey) |

---

## Security

| Concern | Approach |
|---|---|
| Auth tokens | Paseto v4 symmetric; no algorithm confusion; 15m access TTL |
| Refresh tokens | Opaque random bytes; only SHA-256 hash stored in DB |
| PIN | bcrypt; stored separately from password |
| Webhook secrets | AES-256-GCM encrypted at rest in DB |
| Path traversal | All file-serve handlers clean and validate paths against roots |
| SQL injection | sqlc generates parameterised queries; no raw string interpolation |
| Rate limiting | IP-based for auth endpoints; session-based for API; fails open if Valkey down |
| HLS segments | Per-session signed JWT in query param; HLS.js cannot send arbitrary headers |
| Direct stream | UUID acts as capability token; no auth header required |

---

## Known Issues & Technical Debt

| # | Severity | Location | Description |
|---|---|---|---|
| 1 | Medium | `internal/api/v1/users.go` | No `DELETE /api/v1/users/me` self-service deletion endpoint. Admin-only `DELETE /api/v1/users/{id}` exists; the public `/account-deletion` page documents email-request as the workaround. Surfaced during the Play Console submission audit (2026-05-04) — Play accepts the email-request flow under their User Account Deletion policy, but a self-service button is the right product-side fix. |
| 2 | Low | `internal/scanner/hash.go` | Hash cached by `(path, mtime, size)`; file content corruption without an mtime change goes undetected until a forced re-scan. The mtime+size fast-skip path added in v2.1 (production scanner perf incident) extends this design — a re-evaluation would need to weigh per-file hash cost against the corruption-detection upside. |
| 3 | Low | `internal/domain/media/service.go` | `FindOrCreateItem` runs a full-text search per file during scan; for TV libraries with thousands of episodes this is measurable. A per-scan in-memory cache would amortise the cost. |
| 4 | Low | `web/src/routes/watch/[id]/+page.svelte` | On-demand enrich (`POST /items/{id}/enrich`) runs silently with a page-reload-after-delay rather than success/failure feedback. Toast on completion would close the loop. |
| 5 | Low | `internal/livetv/hls_test.go` | `TestHLSProxy_RefcountAcrossViewers` and `TestHLSProxy_ReleaseAfterCloseIsNoop` hard-code `exec.Command("sh", ...)` without a `runtime.GOOS == "windows"` skip guard. CI on Linux passes; Windows-side `go test` fails with "sh: executable file not found in %PATH%" — purely a developer-environment issue, not a runtime bug. |
| 6 | Low | `web/tests/e2e/sse.spec.ts` | The Go server's `/api/v1/notifications/stream` doesn't flush initial bytes for non-browser clients (curl, Node `http`, Playwright `EventSource` all see zero bytes for 30+ seconds); real browsers work in production. Likely a Windows dev-build flush-ordering issue or a client-header-specific middleware-wrapping bug. Spec is `test.skip(true, ...)`-gated until diagnosed. |
| 7 | Info | release CI gate | `make lint` reports 26 issues (9 unused, 8 staticcheck, 3 exhaustive switch, 3 goimports, 2 ineffassign, 1 noctx). All cosmetic, none affect runtime; v2.1.0 shipped with this debt to avoid blocking on cleanup. Sweep planned for v2.1.1 alongside the Play Store readiness work. |

---

## Architectural Decisions

| ADR | Decision | Rationale |
|---|---|---|
| ADR-002 | Watch event monthly partitions | Analytics queries hit only recent partitions; old data purged by dropping a partition |
| ADR-006 | Artwork stored alongside media | Simplifies backup; avoids separate blob store |
| ADR-011 | File identity = SHA-256 partial hash | Move detection without full-file reads; cached by mtime+size |
| ADR-013 | Paseto v4 for access tokens | No algorithm confusion; symmetric; no public-key infrastructure |
| ADR-021 | Separate RW + RO DB pools | Read replicas supported; falls back to single pool |
| ADR-024 | Bounded scan concurrency | Prevents I/O thrashing; hot-reloadable at runtime |
| ADR-025 | Encoder auto-detect at startup | NVENC → VAAPI → software; overridable via `TRANSCODE_ENCODERS` |
| ADR-027 | Hot-reload via SIGHUP | Runtime tuning without restart; no-op on Windows |
| ADR-031 | Multiple files per media item | Supports multi-version libraries (1080p + 4K editions) |

---

## Development Phases

| Phase | Status | Description |
|---|---|---|
| **Phase 1** | ✅ | Core infrastructure: PostgreSQL schema, auth, library CRUD, file scanner, watch events |
| **Phase 2** | ✅ | Transcode pipeline: FFmpeg worker, HLS sessions, quality picker, NVENC support |
| **Phase 3** | ✅ | Native client: SvelteKit web player, progress tracking, Now Playing, analytics, artwork |
| **Phase 4** | ✅ | OSS launch: Docker image, CI/CD, TV show + music scanning, worker wiring |
| **Phase 5** | ✅ | Metadata depth + hardware transcode breadth: TVDB, MusicBrainz, intro/credits markers, trickplay, OCR subtitles, OpenSubtitles, audiophile music metadata, photo EXIF, MCP plugin system, OTel tracing, NVENC/QSV/AMF/SVT-AV1/AV1-NVENC encoder validation |
| **Phase 6 (v2.0)** | ✅ | Polish + extension: HEVC encode on every encoder family, music videos / audiobooks / podcasts as types, lyrics, NFO sidecar import, Cover Art Archive fallback, DVR retention, subtitle burn-in, SAML SSO, built-in HTTPS, Schedules Direct EPG, gapless playback |
| **Phase 7 (v2.1)** | ✅ | Native client breadth + per-user policy + audiophile pillar: Tauri desktop with bit-perfect WASAPI, Android TV / phone / webOS / Tizen / Roku clients with playback parity, audiobook hierarchy, all three book formats (CBZ + CBR + EPUB), home video type, smart playlists, trending row, library is_private + auto-grant + admin "view as", per-file streaming token, in-player audio/subtitle pickers across every client, Continue Watching split, Live TV (HDHomeRun + M3U + EPG + DVR), photo albums + EXIF search + map view |
| **Phase 8** | 🚧 | Play Store / Amazon Appstore launch (Android TV in internal testing 2026-05-04); webOS / Tizen / Roku hardware soak; iOS + Apple TV scoping; Tidal / Qobuz integration decision; ML-driven personalised recommendations re-scope |

The detailed v2.1 track-by-track status is in [docs/v2.1-roadmap.md](docs/v2.1-roadmap.md). The full feature comparison vs Plex / Emby / Jellyfin is in [docs/comparison-matrix.md](docs/comparison-matrix.md).
