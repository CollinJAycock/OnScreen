# OnScreen — Manual Test Plan

What `go test` and `vitest` can't catch: actual pixels rendered, actual frames decoded, actual third-party services responding, actual humans clicking. This doc is the gap-filler.

Three tiers. Tag each release ticket with which tier ran and what failed.

- **Smoke** (~15 min) — gates every release tag. Run on the production-like staging box.
- **Full sweep** (~2-3h) — gates major releases (v1.x → v1.y, v1 → v2). Requires staging with realistic library and a phone/tablet handy.
- **Deep dive** — on a cadence (monthly endurance, quarterly audiophile, ad-hoc on hardware/auth changes).

---

## Tier 1 — Smoke (~15 min, every release)

Goal: prove the build boots and the golden path works. Stop at first failure.

### Boot
- [ ] `docker compose up -d` (or systemd start) returns no errors in 30s
- [ ] `curl -fsS http://localhost:7070/health/live` → `{"status":"ok"}`
- [ ] Server logs show no `ERROR` lines on cold start (warnings OK)
- [ ] Web UI loads at `http://localhost:7070` — no console errors in DevTools

### Auth
- [ ] Log in as admin via username/password — lands on home page
- [ ] Log out — redirected to login, refresh-token cookie cleared

### Library + scan
- [ ] Existing library shows items in hub (Continue Watching, Recently Added)
- [ ] Trigger a scan on one library — completes without error in logs

### Playback (the highest-value smoke)
- [ ] Pick one movie. Click play. Video starts in <5s, audio is in sync
- [ ] Seek forward 10 min — playback resumes within 3s
- [ ] Pause for 30s, resume — no audio dropout
- [ ] Close tab, reopen, click resume — playback restarts at the correct position

### Music (if music library present)
- [ ] Play a song. Play next song. **Listen for a gap** between tracks of a gapless album — there should be none

### Admin
- [ ] Settings page loads, shows current TMDB key (masked)
- [ ] No 4xx/5xx responses in DevTools Network tab during the above

---

## Tier 2 — Full pre-release sweep (~2-3h, major releases)

Run on staging with a library of ≥500 items, ideally a mix of movies, TV, music, photos. Need a second user account, a phone, and a tablet (or browser DevTools mobile emulation as a worse fallback).

### Player matrix

For each row, hit play and confirm: video starts <5s, audio sync, seek works, no console errors.

| Source | Codec / container | Decoder path | Pass? |
|---|---|---|---|
| Movie | H.264 / MP4 | Direct play | ☐ |
| Movie | HEVC / MKV | Direct play | ☐ |
| Movie | HEVC 10-bit HDR | Tonemap (transcode) | ☐ |
| Movie | AV1 / WebM | Direct play (Chrome) | ☐ |
| TV episode | H.264 / TS | Direct play | ☐ |
| Movie with embedded subs | PGS / VobSub | OCR'd VTT | ☐ |
| Movie with sidecar `.srt` | — | External subtitle | ☐ |
| Music album (FLAC) | — | Direct stream | ☐ |
| Music album (MP3) | — | Direct stream | ☐ |
| Music — gapless album | FLAC | Listen for gaps between every track | ☐ |
| Photo (JPEG) | — | Resize via /image endpoint | ☐ |
| Photo (HEIC) | — | ffmpeg HEIC→JPEG | ☐ |

### Subtitle correctness
- [ ] Cue text with `<i>`/`<b>` tags renders as italic/bold (not raw markup, not stripped)
- [ ] CJK / Cyrillic / RTL cue renders correctly (no mojibake)
- [ ] Subtitle search (`/items/{id}/subtitles/search`) returns OpenSubtitles results, picking one downloads + auto-selects
- [ ] Forced-subtitles-only preference: enable in user prefs → English audio + Spanish-forced subs on multi-track movie shows only the forced track

### Multi-user / multi-device
- [ ] Two separate browser profiles (different users) can play *different* items concurrently — no session collision
- [ ] User A starts an item. User A on a phone resumes — server stops the desktop session, phone resumes at the right position
- [ ] Admin demotes user B → user B's next request returns 401 within ~1 round trip (not 1h later)
- [ ] Admin resets user B's password → user B's existing tabs return 401 on next request
- [ ] User B logs in, plays an item, then on a *third* device the admin force-logs-out — both old sessions stop accepting requests

### Library scan
- [ ] Add a folder, trigger scan — items appear with metadata + posters
- [ ] Modify a file (rename), rescan — old item marked unavailable after grace period
- [ ] NFO sidecar (`.nfo`) takes precedence over TMDB title — confirm a deliberately-misleading NFO overrides
- [ ] Music album with embedded ReplayGain — `replaygain_track_gain` shows in track JSON
- [ ] Music album without art — Cover Art Archive fallback succeeds when MusicBrainz ID is present

### Live TV + DVR (if HDHomeRun or M3U source available)
- [ ] Channels list renders, includes EPG data
- [ ] Click a live channel — playback starts <8s
- [ ] Schedule a recording 5 minutes in the future — record starts on time, file appears in `dvr` library
- [ ] EPG ID auto-match: import a Schedules Direct lineup, channels with matching callsigns auto-link
- [ ] Series rule: schedule "new only" on a daily show — only new episodes record, reruns skipped

### Auth providers (test the ones that are configured)
- [ ] Google OAuth: sign in from incognito, account auto-created (or linked to matching email)
- [ ] GitHub OAuth: same
- [ ] Discord OAuth: same
- [ ] OIDC (generic): redirect roundtrip, nonce + state validate, claims map to user
- [ ] SAML: SP-initiated flow with real IdP (Authentik / Keycloak) — login lands on home, refresh works after 1h
- [ ] LDAP: bind with valid creds → login; invalid → "invalid credentials"; ambiguous filter → invalid creds (not enumeration)
- [ ] Forgot password: enter known email, receive email, click link, set new password — old sessions kicked out
- [ ] First-user setup: nuke the DB, register first user, confirm they're admin

### Admin operations
- [ ] Backup download — `pg_dump` succeeds, file ≥1 KB, version header present
- [ ] Backup restore (on a *separate* test instance!) — round-trips, schema version preserved, login still works after
- [ ] Settings → Email → Send Test → email arrives at admin inbox
- [ ] Plugin: register an MCP plugin (echo or filesystem MCP), trigger a tool call, confirm round-trip in logs
- [ ] Webhook: register a webhook pointing at `webhook.site` URL, trigger a `media.play`, payload arrives + signature header validates

### Settings UX
- [ ] Change TMDB API key in UI → save → reload → value masked, scan picks up new key on next refresh
- [ ] Toggle SMTP → save → "Send Test Email" appears
- [ ] Add a CORS origin → save → cross-origin XHR from that origin succeeds
- [ ] Set parental rating ceiling on a managed profile → that profile can't see TV-MA items in hub or search

### Reverse-proxy + TLS
- [ ] Behind nginx with TLS — login + playback work, WebSocket / SSE for notifications stays open
- [ ] Built-in HTTPS (set `TLS_CERT_FILE` + `TLS_KEY_FILE`) — server starts on https, refusing http
- [ ] Half-set TLS (only one of cert/key) — server refuses to start with config error

### Cross-browser
- [ ] Chrome (latest) — golden path
- [ ] Firefox (latest) — golden path
- [ ] Safari (latest, macOS or iPad) — playback works, gapless music transitions cleanly
- [ ] Mobile Safari (iPhone) — playback fits viewport, controls reachable with thumb
- [ ] Mobile Chrome (Android) — same

### Accessibility quick pass
- [ ] Tab through login form — all controls focus-visible
- [ ] Press Space on a play button (don't click) — playback starts
- [ ] Item card has alt text on poster (inspect DOM)

---

## Tier 3 — Deep dives (cadence-driven)

### Endurance (monthly)
- [ ] Start an 8h playback session (movie marathon or transcoded loop). Watch for:
  - [ ] Memory growth in `docker stats` — server under 1GB, worker under 2GB throughout
  - [ ] No FFmpeg zombies (`ps aux | grep ffmpeg` after session ends shows none)
  - [ ] Postgres connection count stable (`SELECT count(*) FROM pg_stat_activity` — should stay under 20)
  - [ ] Valkey memory stable (`INFO memory`)
  - [ ] No goroutine leak (`/debug/pprof/goroutine?debug=1` count stable across 1h samples)

### Hardware encode validation (when GPU/iGPU changes)
- [ ] Force NVENC: `TRANSCODE_ENCODERS=nvenc` → 4K HDR transcode runs, GPU utilization in `nvidia-smi` shows NVENC engine active
- [ ] Force QSV (Intel iGPU): `TRANSCODE_ENCODERS=qsv` → transcode runs without CPU spike
- [ ] Force VAAPI (AMD/Intel Linux): `TRANSCODE_ENCODERS=vaapi` → transcode runs
- [ ] AV1 encode: `TRANSCODE_ENCODERS=av1_nvenc` (RTX 40+ only) → output plays in Chrome
- [ ] HEVC encode: client requests HEVC, output `.m4s` plays in Safari
- [ ] HDR tonemap: 4K HDR source → SDR client → on-screen "Tonemapping active" notice appears, output looks correct (not blown out, not crushed)
- [ ] Subtitle burn-in: PGS subs on 4K HEVC → forced burn-in path produces correct overlay

### Audiophile listening test (quarterly, with the audiophile friend)
- [ ] FLAC 24/96 album — direct stream confirmed via DevTools Network (no `transcode/sessions` requests)
- [ ] Compare the same FLAC played in OnScreen web vs played in foobar2000 / Audirvana with WASAPI exclusive — friend should be able to tell them apart (browser will lose) and that's *expected* until native clients ship
- [ ] DSD file (DSF) — currently unplayable, should fail gracefully not silently
- [ ] ReplayGain album mode — A/B with normalization off, perceived loudness consistent across tracks
- [ ] Multi-disc classical album — disc + track ordering correct, gapless across disc boundary
- [ ] Album-artist vs track-artist — compilation album lists "Various Artists" as album artist, individual tracks attributed correctly

### Security probes (each release that touches auth or new endpoints)
- [ ] **XSS attempt**: subtitle file with `<script>alert(1)</script>` cue — escaped, no alert
- [ ] **SQL injection**: search with `'; DROP TABLE users; --` — no error, no damage (sqlc parameterizes; verify still true)
- [ ] **Path traversal**: `/artwork/..%2F..%2Fetc%2Fpasswd` → 403 or 404, never 200
- [ ] **CSRF**: from `attacker.test` HTML, fetch `POST /api/v1/users/me/preferences` with `credentials: 'include'` — request blocked (no cookie sent due to SameSite, or rejected at Origin check)
- [ ] **Open redirect**: try `?redirect=https://evil.com` on every endpoint that takes a redirect param — none should honor cross-origin
- [ ] **SSRF on webhook**: register webhook URL pointing at `http://127.0.0.1:7070/admin/...` — rejected at validation, and again at delivery time
- [ ] **SSRF on plugin**: register plugin with endpoint outside its allowlist — egress rejects
- [ ] **Bearer leak**: check nginx access logs after a session — no `?token=` or `?apikey=` or `?device_token=` in any URL
- [ ] **Logout completeness**: log in, copy access + refresh tokens, log out, replay tokens — both rejected
- [ ] **Demote revocation**: admin A demotes admin B during B's active session — B's next request 401, segment-token playback also dies within ~1 segment
- [ ] **Password reset cuts segment tokens**: B is mid-playback, B resets password → segment token revoked, next segment 401

### Multi-instance / failover (when scaling out)
- [ ] Two server instances behind a load balancer, single Postgres + Valkey — login on instance 1, hit instance 2 → token still valid
- [ ] Worker on a separate machine — transcode jobs claimed by remote worker, segments served back through API proxy correctly
- [ ] Restart Postgres mid-playback — playback continues from buffer, recovers when DB returns
- [ ] Restart Valkey mid-playback — segment token validation fails, client re-issues via /transcode/start
- [ ] Kill the worker mid-transcode — the `master` worker re-claims partition, scheduled tasks resume

### Docker GPU deploy (when Dockerfile.gpu changes)
- [ ] `docker build -f docker/Dockerfile.ffmpeg` succeeds, layers cache for next build
- [ ] `docker build -f docker/Dockerfile.gpu` succeeds in <2 min on cache hit
- [ ] `docker run --gpus all` on TrueNAS RTX 5000 → NVENC visible in container, transcode session uses GPU
- [ ] Host networking + Cloudflare Tunnel still work after image rebuild

### Large library (annually, or before claiming "scales to N")
- [ ] Scan a 50k-item library — completes in <2h, memory peak under 4GB
- [ ] Hub renders <500ms with 50k items
- [ ] Search returns first page <500ms
- [ ] `/api/v1/items?limit=200&offset=10000` returns <300ms (pagination not O(N))

### Upgrade / migration
- [ ] Backup an N-1 install, restore into N — schema migrates forward via `goose up`, all data accessible
- [ ] Skip a version: backup N-2, restore into N — same expectation
- [ ] Downgrade attempt: backup N, try to restore into N-1 → refused with 409 unless `?force=true`

---

## What this plan deliberately does NOT cover

These are either (a) covered well by automated tests or (b) too rare to be worth manual time:

- API handler unit behavior — `go test ./internal/api/v1/...` is exhaustive
- SQL correctness — sqlc + integration tests cover this
- Migration up/down — goose handles, integration tests assert schema
- Known-good metadata extraction (ffprobe parsing, tag reading) — unit-tested with golden files
- HTTP middleware (CORS, rate limit, auth) — middleware tests cover
- The TMDB / TVDB / MusicBrainz API clients in isolation — mocked

If automated coverage drops in any of those areas, *add a test*, don't move it to this plan.

---

## How to run a tier

1. Cut the relevant section into the release ticket as a checklist
2. Each unchecked box at the end → either fix-or-defer decision in the ticket
3. After the release ships, append a one-line "what broke that the plan didn't catch?" — that's the next addition to this doc

The plan is only useful if it grows from real misses, not from imagined ones.
