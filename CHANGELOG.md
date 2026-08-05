# Changelog

OnScreen tracks server + first-party-client changes. Versions follow
[semantic versioning](https://semver.org/) — minor releases add
endpoints / features without breaking existing API contracts; major
releases (none yet past v2) reserve the right to break things.

The server API is frozen as of **v2.2.0** — see [docs/server-lock.md](docs/server-lock.md)
for the lock posture and what's expected to land in v2.3+ minor
releases without breaking v2.2 clients.

## [v2.4.0] — unreleased

The server lock was lifted after v2.3.0 (see [docs/server-lock.md](docs/server-lock.md)).
v2.4 adds new auth surface and a multi-node transcode fleet alongside an
on-demand adaptive-bitrate pipeline; first-party clients move in lockstep for
the asset-token migration.

### Added — server

- **Two-factor authentication (TOTP)** for local accounts — enrolment + QR
  provisioning and verify-on-login, honored across the web app and the native
  client fleet (including an Android-phone enable flow).
- **Purpose-scoped asset token + nonce-based CSP** — a `purpose=asset` token
  authenticates read-only cross-origin `?token=` URLs (artwork, trickplay,
  external subtitles, SSE) without being usable as a general API credential,
  and `script-src` moves to a per-response nonce. All first-party clients
  (web, desktop, Android, Roku, webOS, Tizen) send the asset token for
  cross-origin asset URLs.
- **On-demand adaptive-bitrate HLS (Jellyfin model)** — the parent session
  runs no ffmpeg; a master playlist lists the ladder and each rung is
  transcoded on demand with at most one rung active at a time. H.264 (MPEG-TS)
  plus HEVC and AV1 (fMP4) ladders; the ladder is capped at the
  client-requested height; multi-instance-safe; `selected_rendition` surfaced
  on `/api/v1/sessions`.
- **Multi-node transcode fleet** — worker nodes join via the primary's shared
  DATABASE_URL + VALKEY_URL. Workers with no shared storage pull the source
  from the primary over HTTP with a per-file stream token, so the fleet works
  without shared mounts. Settings → Transcode shows worker setup info, with
  DATABASE_URL + SECRET_KEY revealed only behind step-up reauth.
- **Intel QSV hardware HEVC decode** (opt-in via `TRANSCODE_QSV_DECODE`) —
  offloads the 4K HEVC decode from the CPU on workers with a known-good QSV
  stack; auto-falls-back to software decode if a source fails to decode on QSV.
- **Maintenance: re-probe metadata** — admin endpoint + a Settings →
  Maintenance action that clears file hashes so the next scan re-probes and
  backfills missing source metadata.
- **Pluggable media storage (`MediaStore`)** — local disk by default, or
  S3-compatible object storage (S3 / MinIO / Backblaze B2 / Wasabi / Cloudflare
  R2) configured live from Settings → Integrations → Storage. Every read path
  (direct play, download, transcode source, artwork, scanner discovery + music/
  photo reads) and write path (cover/folder-art extraction, external-art
  downloads) routes through it; `SignedURL` 302-offloads cacheable bytes to a
  CDN. Default local behaviour is byte-for-byte unchanged.
- **High availability (opt-in, off by default)** — Valkey Sentinel client
  (`VALKEY_SENTINEL_ADDRS`); multi-host Postgres failover DSN that re-homes to
  the read-write node, over a streaming-replication substrate
  (`docker/docker-compose.{valkey,postgres}-ha.yml`).
- **Static-ABR pre-encode** (`STATIC_ABR_ENABLED`) — pre-encodes popular titles'
  ABR ladders (top-played from `watch_plays`) to the media store; playback serves
  the static master + signed segment URLs instead of a live session, so the
  fleet handles only the cold tail.
- **Multi-site DR surface** — per-site content addressing (`Local.Remap` / S3
  `MediaRoot`, multi-site path mappings in Settings), `SITE_ID`, and
  `GET /health/cluster` reporting `{site_id, role, replication_lag_seconds}`; a
  write to a read-only standby degrades to a 503. See
  [docs/dr-runbook.md](docs/dr-runbook.md).
- **CDN-cacheable artwork** (`PUBLIC_ASSET_CACHE`) — immutable resized artwork
  emits `Cache-Control: public` so a CDN fronting the app can cache it.
- **Settings ▸ System** — cluster-wide startup toggles that were env-only move
  into the admin UI: server name, watch-history retention, TMDB rate limit,
  adaptive-bitrate (on/off + max heights), public asset cache, static-ABR, plus
  scanner concurrency and the missing-file grace period. The matching env vars
  become the initial default (a saved override wins), so existing installs are
  unaffected. Node/site-specific config (connection strings, `SECRET_KEY`, bind
  addresses, paths, `SITE_ID`, per-worker hardware) stays env-only because
  `server_settings` replicates across sites. A standalone worker now applies the
  same System overrides (retention + grace period) as the server.
- **Settings ▸ Transcode — Output Limits** — the global transcode ceilings (max
  bitrate / width / height), previously env-only, are editable in the admin UI
  (restart-required; the env vars remain the initial default).
- **LAN discovery in the UI** — `DISCOVERY_ENABLED` / `DISCOVERY_PORT` move to
  Settings ▸ System (restart-required).
- **UI-managed HTTPS** — upload a TLS certificate + key in Settings ▸ System and
  the server serves HTTPS from it (stored encrypted, loaded in-memory, no cert
  file on disk). `TLS_CERT_FILE` / `TLS_KEY_FILE` still take precedence when set.
- **Settings ▸ Nodes — per-node configuration** — a new `node_settings` table,
  keyed by node identity (`NODE_ID`, default hostname), lets node/site-specific
  config that must NOT be shared fleet-wide be managed from the UI per node: bind
  addresses, filesystem paths, `SITE_ID`, Intel QSV decode, and the embedded-
  worker role. The per-node value wins over env; `IGNORE_NODE_DB_CONFIG=true` is
  a break-glass to boot a locked-out node from env only. The bootstrap set
  (`DATABASE_URL`, `SECRET_KEY`, `NODE_ID`) necessarily stays in the environment.
- **`grandparent_id` on item detail** — episode detail resolves the show id
  (episode → season → show) so the player's back button returns to the show
  page instead of the previously-played episode; the web client uses it for
  `goBack()` and falls back to browser history when it's unset.
- **Self-service account deletion** — `DELETE /api/v1/users/me` lets a signed-in
  user delete their own account (refused for the last admin); the access cookie
  is cleared and the token dies immediately via the session-epoch check.
- **`rotate-key` tool** — `cmd/rotate-key` re-encrypts every at-rest secret
  (encrypted settings, webhook secrets, TOTP secrets) from an old `SECRET_KEY`
  to a new one, so rotating the key is no longer destructive. Dry-run by
  default. See [docs/key-rotation.md](docs/key-rotation.md).
- **ListenBrainz scrobbling (opt-in, per-user)** — link a ListenBrainz token
  from **Settings → Scrobbling** (`PUT /api/v1/users/me/scrobble/listenbrainz`;
  stored AES-256-GCM-encrypted) and OnScreen submits a listen when you finish a
  music track. One-way and best-effort — it fires on the `stop` watch-event once
  the play crosses the listen threshold (≥50% of the track, or ≥4 minutes) and
  never blocks playback — routed through the SSRF-guarded outbound client.
  Last.fm is the planned follow-up.
- **Capability profiles + server-authoritative playback decision** — clients
  declare what they can decode in a declarative `X-Client-Capabilities` header
  (codecs, containers, resolution, audio channels, bit depth, HDR), and
  `POST /items/{id}/playback-decision` returns the server's verdict
  (directPlay / directStream / transcode / unsupported). Every first-party
  client sends the header; web, desktop, Android (TV + phone), Tizen and webOS
  consume the verdict with a local-heuristic fallback. The per-request
  `supports_hevc`/`supports_av1` booleans became **tri-state**: absent defers
  to the header, an explicit value overrides it in both directions — which is
  what lets a client demote a codec its own probe lied about (Windows Chrome
  claims HEVC without the platform extensions installed, then rejects the
  append; the web player now proves the failure, demotes the claim, and the
  retry really does come back H.264). See
  [docs/capability-profiles.md](docs/capability-profiles.md).
- **Dolby Vision is refused, not broken** — DV sources return a clear
  `unsupported` verdict + a 415 on transcode-start, and every client shows
  "Dolby Vision is not supported" instead of a green/purple tonemap. Decision
  record: [docs/dolby-vision.md](docs/dolby-vision.md).
- **RTMP live ingest ("go live")** — OBS / ffmpeg push to
  `rtmp://host:1935/live/<key>` surfaces as a Live TV channel through the
  existing HLS proxy + DVR: per-key authentication, codec-agnostic FLV
  pass-through (H.264 + enhanced-RTMP HEVC/AV1), multiple concurrent
  broadcasters with multi-viewer fan-out.
- **Encoder fail-over + full-VRAM by default** — a hardware encoder that can't
  acquire the GPU (e.g. GeForce NVENC session cap) spills the job to the next
  provider on the box (NVENC → QSV → software; AMF likewise) instead of
  failing the stream. The full-VRAM Intel paths (`TRANSCODE_QSV_VRAM`,
  `TRANSCODE_VAAPI_VRAM`) default on, probe-gated with per-job software
  fallback; NVIDIA gained an all-VRAM HDR→SDR path via a curated
  `tonemap_cuda` on an own-built ffmpeg 8.1.1.
- **Per-user star ratings + community average**, an **admin analytics
  overhaul** (timezone-correct buckets, 7/30/90 ranges, user leaderboard,
  client/hour breakdowns, completion rate, direct-vs-transcode split backed by
  a persisted playback-decision column on `watch_events`), **admin per-user
  streaming caps** (concurrent streams / bitrate / height), an in-player
  **multi-version picker** for items with several files, and **per-user hub
  customization** (row visibility + ordering, Libraries grid pinnable).
- **Subtitle-subsystem hardening** — embedded text-subtitle extraction is
  cached, single-flighted and detached (4K-remux subs load instantly instead
  of timing out), OCR batches a whole stream into one Tesseract invocation,
  SDH/forced dispositions are captured and surfaced fleet-wide with
  preferred-language/forced-only auto-select, OpenSubtitles downloads gate on
  the daily quota, and bundled application keys ship for TMDB + OpenSubtitles.
- **Scanner/metadata wave** — date-based (daily/talk-show) episode filenames
  build a proper show→season-by-year→episode hierarchy; shows resolve by
  on-disk folder after a Fix Match rename; Fix Match gained a release-year
  filter; the TMDB client gained a disk response cache +
  `append_to_response` batching; a `cartoons` library type joined the picker.
- **Split segment serving** (`PUBLIC_SEGMENT_BASE_URL`) — optional distinct
  base URL for HLS segment fetches, so a CDN or separate ingress can carry the
  segment bandwidth.

### Changed — server

- **Removed `MEDIA_PATH`** — it was a vestige of the old artwork-storage scheme
  (artwork now lives next to the media file) and only fed the `CACHE_PATH`
  default. Library paths are configured per-library in the admin UI, not via a
  global root. `CACHE_PATH` now defaults to `~/.onscreen/cache/artwork`. The env
  var is simply ignored if still set — the required set is now `DATABASE_URL`,
  `VALKEY_URL`, `SECRET_KEY`.
- **Scale before tonemap on the software HDR path** — the zscale HDR→SDR chain
  now runs at output resolution instead of source resolution, dramatically
  cutting CPU on 4K→1080p HDR transcodes.
- **DirectStream redefined** — video-copy + audio-transcode (matching what
  clients actually do), instead of "copy both streams". Without this, the
  server verdict would have full-transcoded most h264-in-mkv libraries.
- **Transcoded AAC capped at 5.1** — browsers can't decode 7.1 AAC via MSE;
  the capability header declares `maxAudioChannels=6` and the transcode path
  never emits 8-channel AAC.
- **Per-user session cap counts only live sessions**, and ABR rung children
  are excluded from the cap and from `/api/v1/sessions`.
- **Go 1.26** — toolchain bumped for patched stdlib (three govulncheck CVEs);
  the release workflow now builds with the same version CI tests.

### Fixed — server

- **Transcode "spinner forever" on corrupt source files** — a 5 s pre-flight
  ffprobe (`scanner.VerifySource`) gates the transcode-start handler before a
  worker is dispatched. A missing path returns `422 SOURCE_MISSING`; a corrupt
  container returns `422 SOURCE_UNREADABLE` with a friendly message ("This file
  appears to be corrupt — the server couldn't read its container. Re-encode or
  replace the file."), surfacing in the player's error overlay instead of the
  historical 60 s spinner while ffmpeg hung trying to demux the bad input.
  Healthy files clear in ~200–500 ms.
- **Playlist endpoint's 60 s deadline could be defeated by a hung worker** —
  each `workerReady` HEAD inside the wait loop now uses a 3 s sub-context, so
  the deadline check fires on the next 100 ms tick after expiry instead of
  being pushed out to 60+30 s. The client gets a clean `503 playlist not
  ready` if seg 0 never lands.
- **CSP blocked the Google Cast sender SDK** — `cast_sender.js` (loaded
  dynamically by the watch screen) is now allowed on `script-src`, and the
  Cast picker iframe on `frame-src`. The Cast button + Chromecast device
  discovery work again on the web client. `frame-src` is set explicitly
  because `frame-ancestors 'none'` only governs inbound framing, not what we
  embed; without it `default-src 'self'` would block the picker.
- **Prometheus metrics were exported but never recorded** — 9 of the 10
  `onscreen_*` metrics were defined and registered yet had zero instrumentation,
  so `/metrics` only ever showed runtime/process stats. Wired them all up: HTTP
  request count + latency (chi route-template labels to bound cardinality), DB
  query duration (by SQL verb, via a pgx tracer that wraps the existing OTel
  one), transcode active-sessions gauge + jobs-by-status, scanner files per
  library, watch events by type, webhook delivery failures, and hub-cache
  refresh duration.
- **Settings ▸ System values weren't read back** — `GET /settings/system`
  returned its body un-enveloped while the web client unwraps `{"data": …}`, so
  saved values never repopulated the form. It now uses the standard envelope
  like every other settings endpoint.
- **`DEV_FRONTEND_URL` now actually proxies** — in a `-tags dev` build the
  server reverse-proxies non-API requests to the Vite dev server, so the whole
  app (UI + HMR) is reachable on the single API port. The flag was documented
  but previously a no-op; production builds always serve the embedded SPA.
- **Persist float Numeric columns** (frame_rate, item rating, ReplayGain) —
  pgx can't scan a float64 into a `Numeric`, so these were silently nulled;
  scan the decimal string form instead.
- **Index the FK cascade columns** the schema squash missed.
- **The July–August playback-hardening campaign** — a multi-week audit swept
  the entire playback path end-to-end; the headline fixes:
  - *Codec demotion actually reaches the server* — the web player's
    `X-Client-Capabilities` header is rebuilt (and persisted for the session)
    when a decode failure disproves the browser's own probe, the cached
    playback verdict is invalidated, and the tri-state body override stops the
    server from re-encoding the fallback straight back to the codec that just
    failed. A fatal `bufferAppendError` escalates to a full transcode on the
    first occurrence instead of looping hls.js recovery forever.
  - *Dead-chroma ("green frame") detection* — the worker signalstats-checks
    the first segment whenever a hardware decode/scale stage is active; zeroed
    chroma kills the run and retries with software decode, and the full-VRAM
    startup probe now validates output **pixels** at both bit depths instead
    of trusting the exit code. Damaged/fake source files (the failure that
    motivated this) now fail visibly instead of playing green — and the
    player's stall-restart loop is bounded (3 per title) with a "file may be
    damaged" error instead of endless session churn.
  - *ABR restart machinery* — rung restarts are incarnation-scoped so a dying
    run can't reap its successor's ffmpeg or wipe its segments; session
    lifetime is idle-based; segments become visible only when complete.
  - *DVR/Live TV lifecycle* — recordings end by encoder EOF instead of a
    SIGKILL at the scheduled boundary (no more truncated tails), failed
    recordings retry, RTMP fan-out survives subscriber churn, and double-tune
    joins the existing session.
  - *Access-gate closure* — artwork, live TV, usage accrual and image serving
    all enforce the same per-library ACL + content-rating + watch-limit gates
    as the primary playback paths.
  - *Decision/arg-builder correctness* — codec-aware bit-depth caps, audio-only
    and audio-less stream shapes, even-dimension scaling, 10-bit profile
    handling, lazy re-probe of NULL metadata rows, and one shared resolver for
    the segment container (`videooutput.go`) so the API, worker and playlist
    handler can never disagree about `.ts` vs `.m4s` again.
  - *Web player recovery bounds* — media-error recovery is budgeted and only
    refills after quiet playback; PTS offset is latched once; resume math and
    subtitle timing survive quality switches.
- **Two full-server audit sweeps** (June and July) closed ~50 batches of
  findings across session lifecycle, SSO admin sync, invite/PIN races,
  scanner atomicity, repository correctness and client UX — each fix landing
  with a regression test.

### Security — server

A defensive audit pass hardened the auth, SSRF, and content-handling surfaces:

- **PIN user-switching is brute-force resistant** — `POST /auth/pin-switch`
  applies a per-target failure lockout (5 attempts / 15 min) plus a per-session
  rate limit, and `/auth/pair/claim` is rate-limited too, so a 4-digit switch
  PIN can no longer be guessed into a token carrying the target user's
  privileges.
- **Now-playing is per-user** — `GET /api/v1/sessions` returns only the
  caller's own sessions for non-admins; admins still get the full dashboard.
- **Favorites enforce access on write** — adding a favorite applies the same
  per-library ACL + content-rating ceiling as the read path, so an item in a
  hidden library can't be favorited (404).
- **Stricter CSP** — added `base-uri 'self'` and `object-src 'none'`.
- **Tighter outbound SSRF policy** — the Radarr/Sonarr client no longer allows
  link-local, the S3/MediaStore client dials through the shared SSRF guard, and
  the guard now also blocks RFC 6598 CGNAT (100.64.0.0/10) unless a caller opts
  into private ranges — keeping a misconfigured/abused URL away from cloud
  metadata and internal hosts.
- **Webhooks fail closed on a bad secret** — if a configured signing secret
  can't be decrypted (e.g. after a `SECRET_KEY` rotation), delivery is refused
  rather than sent unsigned.
- **Subtitle cues are sanitized** — `<script>`, inline event handlers, and
  `javascript:` URIs are stripped from external/OCR subtitles before caching,
  as defense-in-depth for any client that renders cue text as HTML.
- **Opt-in fail-closed rate limiting** — `OS_AUTH_RATE_LIMIT_FAIL_CLOSED=true`
  makes the credential-path limiter reject (503) instead of failing open when
  Valkey is unreachable, so an outage can't silently disable brute-force
  protection. Default stays fail-open (ADR-015).
- **Per-library access wiring asserted at startup** — the server fails fast if a
  content handler is constructed without its library-ACL checker, since a nil
  checker would otherwise fail open (serve every library).
- **June security wave** — content-rating-ceiling bypasses closed on five
  side-paths (external subtitles, artwork, static ABR among them), an
  authenticated path traversal in OCR subtitle-language handling fixed,
  subtitle-extraction concurrency bounded + OCR start rate-limited (DoS),
  bundled API keys injected via BuildKit secret mounts instead of build ARGs,
  and the full 12-finding security/code-smell audit remediated.
- **July audit tiers** — static-ABR stream-token parameter mismatch; watch
  limits enforced on direct play, download AND static ABR; five handlers
  brought under `ValidateLibraryAccess`; blanket RFC1918 proxy trust replaced
  with explicit configuration; ABR sessions brought under the per-user cap;
  UI-managed TLS no longer falls back silently; settings ciphertexts bound to
  their key via AES-GCM associated data; audit-log IPs no longer forgeable.

### Security — clients

- **TV client: platform-delegated TLS trust** — the Android TV client replaced
  its hand-rolled PKIX trust manager (custom cert validation, plus an AIA
  incomplete-chain bug that broke Cloudflare-fronted servers) with the same
  system `X509TrustManager` delegation the phone client uses. Not trust-all;
  hostname verification intact.
- **Phone client: EPUB reader WebView sandboxed** — the in-app EPUB WebView now
  blocks off-origin `http(s)` requests, so a malicious book can't beacon out,
  exfiltrate, or navigate the reader off-origin. Bundled resources still render.
- **Pairing survives a stale cleartext origin** — a TV whose stored server URL
  was `http://` against a TLS-only server had its pairing POST silently
  rewritten to GET by the redirect follower (OkHttp and fetch both do this on
  a 301), so the PIN never appeared. Android now upgrades the origin before
  the unauthenticated POSTs; Tizen/webOS persist the origin the setup probe
  actually answered on and self-heal stale installs by replaying the request
  once against the adopted TLS origin.

### Clients

- **Fire TV is in production** — live on the Amazon Appstore since 2026-06-18
  (the first OnScreen client in a production store); Android TV is on the Play
  closed-testing track and the Android phone build is in Play review, all
  under one listing with separate versionCode lanes.
- **Client sweeps** — three Android TV UX/audit waves (focus/scroll
  preservation, pagination, playback re-emit restarts, Up Next double-load,
  hub-layout parity), a phone-client security + UI pass, TV-fleet lifecycle/
  error-recovery fixes across Android TV/webOS/Tizen/Roku, episode-card art
  fallbacks, and background-audio handoff device-verified on Fire TV and
  Google TV hardware.
- **Settings UI unification** — one token/component pass across all 24
  settings pages; per-row actions collapsed into overflow menus.

### Added — Windows installer

- **Worker-only install mode** — a node joins an existing primary's fleet with
  no local database, and opens its segment port inbound.
- Applies database migrations on install (fixes a won't-start on a fresh DB);
  registers WinSW services by per-service exe name; idempotent re-register on
  upgrade; opens the API/UI port to the LAN; and resolves the bundled ffmpeg
  even when a binary is run outside its service.

### Fixed — Windows installer

- **Worker-only mode no longer registers a Windows service** — it now installs
  as an onlogon interactive scheduled task (`OnScreenWorker`). A service runs
  in session 0 with no GPU access, so the worker would crash immediately during
  NVENC/QSV probing (Event 7023 on `OnScreenWorker`); the only reliable run was
  a manual `worker.exe` from a cmd prompt. The task runs in the install user's
  interactive session (same GPU path as the manual run), auto-restarts on
  failure, and the post-install also tears down the legacy WinSW worker service
  on upgrade.

## [v2.3.0] — 2026-05-22

Additive release under the server lock — no breaking API changes. A
sustained transcode-hardening pass (validated across NVENC, Intel QSV,
Intel + AMD VAAPI, and AMF on real hardware), two new AV1 encoder
families, SSRF hardening on the SSO paths, and a scanner fix for
filesystem-trash directories.

### Added — server

- **AV1 encode via VAAPI and AMF** — `av1_vaapi` (Intel Arc + AMD
  RDNA4 / radeonsi) and `av1_amf` (Windows AMD RDNA3+) join the
  existing `av1_nvenc` / `av1_qsv` paths. Probe-gated: an encoder only
  appears if a 1-second test encode succeeds on the host, so cards
  without an AV1 block are silently skipped.
- **Scanner skips filesystem-trash directories** — `.recycle`,
  `#recycle`, `$RECYCLE.BIN`, `.Trash*`, `@eaDir`, `.stversions`,
  `.AppleDouble`, `lost+found` are no longer walked. Samba's
  `vfs_recycle` (which moves SMB-deleted content into `.recycle/` with
  the folder tree intact) was causing the scanner to ingest deleted
  shows as zombie duplicate rows.

### Changed — server

- **VAAPI preferred over QSV in auto-detect** on Intel hardware where
  both are present — the native `*_vaapi` path is 2–5× faster than
  QSV/libmfx on HDR sources on Arc. Operators can pin QSV via
  `TRANSCODE_ENCODERS=h264_qsv,…`.
- **`supports_hevc` honored for HEVC sources** — an HEVC source played
  by an HEVC-capable client now stays HEVC instead of round-tripping
  to H.264 (mirrors the existing AV1 source-preservation rule).

### Fixed — transcode

- **HDR tonemap on the VAAPI path** — the zscale tonemap chain now runs
  before the VAAPI hwupload. HDR sources transcoded via `*_vaapi` were
  producing 8-bit output still tagged `smpte2084`/`bt2020` (washed-out,
  banded); they now correctly emit `bt709`.
- **Playlist serves on the first segment** instead of waiting for two,
  and the long-poll stamps session activity — slow-first-segment cases
  (HDR, large remux, flaky media) no longer get reaped by the worker's
  idle timer mid-startup. First-segment latency roughly halved on light
  sources.
- **Lazy re-probe at transcode start** — a file row left without
  codec/dimensions/HDR metadata (transient I/O blip during scan, or an
  oversized source) is re-probed before planning, so the planner picks
  the right scale + tonemap chain instead of mis-sizing the canvas.
- **VAAPI probed independently of QSV** — both Intel encoder families
  are now detected when present rather than QSV short-circuiting VAAPI.

### Fixed — security / auth

- **OIDC discovery / token / JWKS routed through `safehttp`** — closes
  an SSRF vector where an admin-set (or hijacked) issuer URL could
  pivot the discovery fetch onto a cloud-metadata service. SAML already
  did this.
- **OIDC + SAML allow LAN / loopback IdPs** — the SSRF hardening uses
  the same policy as LDAP (`AllowPrivate` + `AllowLoopback`, link-local
  still denied), so a Keycloak / Authentik / Authelia on the LAN, in a
  Docker network, or on localhost works while the cloud-metadata range
  stays blocked. Validated end-to-end against Keycloak.
- **First-admin creation serialized** with a Postgres advisory lock —
  concurrent `/auth/register` calls during the bootstrap window could
  each pass the `WHERE NOT EXISTS` guard and create multiple admins.
- **Artwork download redirect chain capped** at 3 hops with logging —
  bounds a malicious metadata source from spinning the fetcher through
  an unbounded redirect chain (Cover Art Archive's single hop to
  archive.org still resolves).

### Fixed — tests / build

- **Integration suite unblocked** — `make test-int` now passes the
  `integration` build tag (17 testcontainer-gated files were silently
  never compiling in), a stale settings stub gained its missing
  methods, and the destructive migration round-trip test is skipped
  with a documented reason. Running the suite this way is what surfaced
  the first-admin race above.

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
