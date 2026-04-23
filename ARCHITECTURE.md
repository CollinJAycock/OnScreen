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

| Layer | Choice | Rationale |
|---|---|---|
| Language | Go 1.23+ | Single binary, maintainable, fast ramp |
| Router | Chi v5 | Lightweight, idiomatic Go, middleware-friendly |
| Database | PostgreSQL 16+ | MVCC, materialized views, pgvector, FTS |
| DB driver | pgx/v5 | Native Postgres; better than `database/sql` |
| Query gen | sqlc | Type-safe queries from raw SQL |
| Migrations | goose v3 | SQL-first, simple |
| Cache / queue | Valkey (Redis OSS fork) | Sessions, transcode job queue, rate-limit buckets |
| Auth | Paseto v4 local | No algorithm confusion attacks; symmetric |
| Config | env vars | 12-factor; SIGHUP hot-reload for runtime knobs |
| Transcoding | FFmpeg via os/exec | Industry standard; NVENC + software fallback |
| Frontend | SvelteKit + TypeScript | Small bundle; no React overhead |
| Logging | `log/slog` | Stdlib structured logging |
| Metrics | Prometheus | Scrape-based; standard |
| Tracing | OpenTelemetry | Vendor-neutral; optional |

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
│   │   ├── main.go     Entry point; wires all dependencies
│   │   └── adapter.go  DB adapter layer (sqlc ↔ domain interfaces)
│   └── worker/         Standalone transcode + maintenance worker
├── internal/
│   ├── api/
│   │   ├── middleware/ Auth, logging, rate-limit, recovery, request IDs
│   │   ├── respond/    Standard response envelope helpers
│   │   ├── router.go   Chi router assembly
│   │   └── v1/         All API handlers (auth, libraries, items, sessions,
│   │                   transcode, webhooks, settings, analytics, fs)
│   ├── artwork/        Poster/fanart download + resize cache (ADR-006)
│   ├── auth/           Paseto v4 token issuance + validation (ADR-013)
│   ├── config/         Env-based config; hot-reload via SIGHUP (ADR-027)
│   ├── db/
│   │   ├── db.go       pgxpool setup (4× CPU connections)
│   │   ├── gen/        sqlc-generated query wrappers
│   │   └── migrations/ 6 goose migrations (schema, partitions, settings,
│   │                   dedup, cleanup, drop-plex-columns)
│   ├── domain/
│   │   ├── library/    Library CRUD + scan scheduling
│   │   ├── media/      Item + File domain models + service
│   │   ├── settings/   Server settings (TMDB key)
│   │   └── watchevent/ Immutable event recording + watch state derivation
│   ├── gdm/            (removed)
│   ├── metadata/
│   │   ├── agent.go    Metadata provider interface
│   │   └── tmdb/       TMDB API v3 client (rate-limited, configurable)
│   ├── observability/  slog, Prometheus metrics, health checks
│   ├── scanner/
│   │   ├── scanner.go  Recursive dir walk; hash + probe + DB upsert
│   │   ├── hash.go     SHA-256 partial hash with mtime/size cache (ADR-011)
│   │   ├── probe.go    ffprobe subprocess; 50 MB read cap; 30s timeout
│   │   ├── enricher.go TMDB enrichment + artwork download; title cleaning
│   │   └── watcher.go  fsnotify watcher; 500ms debounce per directory
│   ├── streaming/      HTTP byte-range tracker (direct-play "Now Playing")
│   ├── transcode/
│   │   ├── ffmpeg.go   FFmpeg args builder; NVENC/VAAPI/SW encoder paths
│   │   ├── session.go  Valkey session store (4h TTL, heartbeat, job queue)
│   │   └── worker.go   Job dequeue + ffmpeg exec + HLS output
│   ├── valkey/         go-redis/v9 wrapper
│   └── worker/         Periodic maintenance tasks (partition cleanup,
│                       missing-file promotion, hub refresh)
└── web/
    ├── src/
    │   ├── lib/
    │   │   ├── api.ts            TypeScript API client (fetch + auto-refresh)
    │   │   └── components/
    │   │       └── Logo.svelte   Brand logo (favicon.svg + wordmark)
    │   └── routes/
    │       ├── +layout.svelte    Shell: auth guard, sidebar nav, Logo
    │       ├── +page.svelte      Home: library grid
    │       ├── login/            Login form
    │       ├── setup/            First-user registration wizard
    │       ├── libraries/
    │       │   ├── new/          Create library
    │       │   └── [id]/         Library grid (poster cards, scan, sort/filter)
    │       │       └── settings/ Library settings
    │       ├── watch/[id]/       Full video player (HLS + direct play)
    │       ├── analytics/        Watch stats dashboard
    │       └── settings/         Server settings (TMDB key)
    └── static/                   Favicon set (SVG, ICO, PNG, webmanifest)
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

### Required

| Var | Description |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN (`postgres://user:pass@host:5432/db?sslmode=disable`) |
| `VALKEY_URL` | Valkey/Redis URL (`redis://host:6379`) |
| `MEDIA_PATH` | Root directory where media files live |
| `SECRET_KEY` | 32-byte key for Paseto tokens (hex, base64, or raw) |

### Optional

| Var | Default | Description |
|---|---|---|
| `DATABASE_RO_URL` | `DATABASE_URL` | Read replica |
| `CACHE_PATH` | `$MEDIA_PATH/.cache/artwork` | Artwork resize cache |
| `LISTEN_ADDR` | `:7070` | API server bind address |
| `METRICS_ADDR` | `:7071` | Prometheus metrics bind |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `RETAIN_MONTHS` | `24` | Watch event partition retention |
| `SCAN_FILE_CONCURRENCY` | `2×CPU` | Per-file goroutines during scan |
| `SCAN_LIBRARY_CONCURRENCY` | `2` | Parallel library scans |
| `MISSING_FILE_GRACE_PERIOD` | `15m` | Before `active→missing` promotion |
| `TRANSCODE_MAX_SESSIONS` | `CPU/2` (SW) / `4` (HW) | Parallel transcode jobs |
| `TRANSCODE_ENCODERS` | auto-detect | Override: `nvenc,software` |
| `TRANSCODE_MAX_BITRATE_KBPS` | `40000` | Quality cap |
| `TRANSCODE_MAX_WIDTH` | `3840` | Max output width |
| `TRANSCODE_MAX_HEIGHT` | `2160` | Max output height |
| `TRANSCODE_NVENC_PRESET` | `p4` | NVENC speed/quality: `p1`–`p7` |
| `TRANSCODE_NVENC_TUNE` | `hq` | NVENC tune: `hq`/`ll`/`ull` |
| `TRANSCODE_NVENC_RC` | `vbr` | NVENC rate control: `vbr`/`cbr`/`constqp` |
| `TRANSCODE_MAXRATE_RATIO` | `1.5` | Peak bitrate = target × ratio |
| `TMDB_API_KEY` | — | Seeded to DB on first run; also configurable via `/settings` |
| `TMDB_RATE_LIMIT` | `5` | TMDB req/s |

### Hot-Reloadable (SIGHUP)

`LOG_LEVEL`, `SCAN_FILE_CONCURRENCY`, `SCAN_LIBRARY_CONCURRENCY`, `TRANSCODE_MAX_SESSIONS`, `TRANSCODE_MAX_BITRATE_KBPS`, `TRANSCODE_MAX_WIDTH`, `TRANSCODE_MAX_HEIGHT`, `TRANSCODE_NVENC_PRESET`, `TRANSCODE_NVENC_TUNE`, `TRANSCODE_NVENC_RC`, `TRANSCODE_MAXRATE_RATIO`

> **Windows**: SIGHUP is a no-op (`internal/config/sighup_windows.go`). Restart the process to reload config.

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
| 1 | Medium | `cmd/worker/main.go` | `stubMissingFilesService` and `stubSessionCleanup` are no-ops in standalone worker; missing-file promotion and session cleanup don't run unless using embedded worker |
| 2 | Medium | `internal/db/gen/` | Some sqlc queries do not filter `WHERE deleted_at IS NULL`; soft-deleted items may leak through in edge cases |
| 3 | Low | `internal/scanner/hash.go` | Hash cached by `(path, mtime, size)` only; file content corruption without mtime change goes undetected until next forced re-scan |
| 4 | Low | `web/src/routes/watch/[id]/+page.svelte` | On-demand enrich (`POST /items/{id}/enrich`) runs silently in background; user has no success/failure feedback beyond the `⟳` spinner stopping |
| ~~5~~ | ~~Low~~ | ~~`internal/worker/webhooks.go`~~ | ~~Resolved: webhook dispatch wired to `PUT /items/{id}/progress` — fires on pause/stop events~~ |
| 6 | Low | `internal/config/config.go` | `MISSING_FILE_GRACE_PERIOD` is not hot-reloadable; requires restart to change |
| 7 | Low | `internal/transcode/session.go` | `SessionStore.List()` uses `KEYS` pattern scan; fine for small deployments but should be replaced with `SCAN` for large session counts |
| 8 | Info | `internal/domain/media/service.go` | `FindOrCreateItem` does a full-text search per file during scan; for TV libraries with thousands of episodes this could become slow — consider a per-scan in-memory cache |

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
| **Phase 1** | ✅ Complete | Core infrastructure: PostgreSQL schema, auth, library CRUD, file scanner, watch events |
| **Phase 2** | ✅ Complete | Transcode pipeline: FFmpeg worker, HLS sessions, quality picker, NVENC support |
| **Phase 3** | ✅ Complete | Native client: SvelteKit web player, progress tracking, Now Playing, analytics, artwork |
| **Phase 4** | ✅ Complete | OSS launch: Docker image, CI/CD, TV show + music scanning, worker wiring |
| **Phase 5** | 🚧 In progress | TVDB ✅, MusicBrainz ✅, intro/credits markers ✅, trickplay ✅, OCR subtitles ✅, OpenSubtitles ✅, audiophile music metadata ✅, photo EXIF ✅, MCP plugin system ✅, OTel tracing ✅, HA guide, pgvector similarity recommendations |

### Phase 4 Completed Items

- [x] Webhook dispatch wired to native progress endpoint (fires on pause/stop)
- [x] Standalone worker stubs replaced with real media.Service and session cleanup
- [x] Docker image + docker-compose with server, worker, postgres, valkey, and migrations
- [x] GitHub Actions CI pipeline (Go build+test, frontend check+test, Docker build)
- [x] TV show scanning: S##E## filename parsing, show→season→episode hierarchy creation
- [x] TV show enrichment: TMDB SearchTV/GetSeason/GetEpisode with poster/fanart download
- [x] Music library scanning: ID3/FLAC/Vorbis tag reading, artist→album→track hierarchy, embedded album art extraction
