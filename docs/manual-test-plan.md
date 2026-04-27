# OnScreen — Manual Test Plan

What `go test` and `vitest` can't catch: actual pixels rendered, actual frames decoded, actual third-party services responding, actual humans clicking. This doc is the gap-filler.

Three tiers. Tag each release ticket with which tier ran and what failed.

- **Smoke** (~10 min) — gates every release tag. Run on the production-like staging box.
- **Full sweep** (~90 min) — gates major releases (v1.x → v1.y, v1 → v2). Requires staging with realistic library and a phone/tablet handy.
- **Deep dive** — on a cadence (monthly endurance, quarterly audiophile, ad-hoc on hardware/auth changes).

The sections below are smaller than they used to be. As the automated suite grew (UAT 39 → 87, integration 40 → 73, handler unit tests across api/v1, arr, settings, observability, valkey, streaming, middleware, plus parser fuzz tests), the manual plan rebalanced toward what genuinely needs human eyes — visual UI, real codecs, real hardware, real third-party services. The "Reference: what's now automated" section at the bottom maps each shrunken area to the test that took its place, so when something breaks you know where to look first.

---

## Tier 1 — Smoke (~10 min, every release)

Goal: prove the build boots and the golden path works. Stop at first failure.

### Boot
- [ ] `docker compose up -d` (or systemd start) returns no errors in 30s
- [ ] `curl -fsS http://localhost:7070/health/live` → `{"status":"ok"}`
- [ ] `curl -fsS http://localhost:7070/health/ready` → 200 (DB + Valkey + migrations all healthy)
- [ ] Server logs show no `ERROR` lines on cold start (warnings OK)
- [ ] Web UI loads at `http://localhost:7070` — no console errors in DevTools

### Auth
- [ ] Log in as admin via username/password — lands on home page
- [ ] Log out — redirected to login, refresh-token cookie cleared

### Library + scan
- [ ] Existing library shows items in hub (Continue Watching, Recently Added)
- [ ] Trigger a scan on one library — completes without error in logs

### Playback (the highest-value smoke — automation can't see frames)
- [ ] Pick one movie. Click play. Video starts in <5s, audio is in sync
- [ ] Seek forward 10 min — playback resumes within 3s
- [ ] Pause for 30s, resume — no audio dropout
- [ ] Close tab, reopen, click resume — playback restarts at the correct position

### Music (if music library present)
- [ ] Play a song. Play next song. **Listen for a gap** between tracks of a gapless album — there should be none

### Admin smoke
- [ ] Settings page loads, shows current TMDB key (masked)
- [ ] No 4xx/5xx responses in DevTools Network tab during the above

---

## Tier 2 — Full pre-release sweep (~90 min, major releases)

Run on staging with a library of ≥500 items, ideally a mix of movies, TV, music, photos. Need a second user account, a phone, and a tablet (or browser DevTools mobile emulation as a worse fallback).

### Player matrix — the irreducible core

For each row, hit play and confirm: video starts <5s, audio sync, seek works, no console errors. *Only thing that catches "the codec actually decoded right."*

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

### Subtitle correctness — visual rendering only
- [ ] Cue text with `<i>`/`<b>` tags renders as italic/bold (not raw markup, not stripped)
- [ ] CJK / Cyrillic / RTL cue renders correctly (no mojibake)
- [ ] Subtitle search (`/items/{id}/subtitles/search`) returns OpenSubtitles results, picking one downloads + auto-selects
- [ ] Forced-subtitles-only preference: enable in user prefs → English audio + Spanish-forced subs on multi-track movie shows only the forced track

### Multi-user / multi-device — real device behavior
*Auth-side revocation is unit-tested ([TestSessionEpoch_DemotionRevokesTargetToken](test/uat/uat_test.go), [TestPasswordReset_SuccessRevokesEverySession](test/uat/uat_extras_test.go)) — these cases test that the **client** actually reacts.*

- [ ] Two separate browser profiles can play *different* items concurrently — no UI session collision
- [ ] User A starts an item. User A on a phone resumes — server stops the desktop session, phone resumes at the right position
- [ ] Admin demotes user B during B's active playback — B's player surfaces a "session ended" message and stops within ~1 segment (don't trust the network panel — watch the screen)
- [ ] Admin resets user B's password during B's playback — same observation

### Library scan — filesystem + metadata
- [ ] Add a folder, trigger scan — items appear with metadata + posters
- [ ] Modify a file (rename), rescan — old item marked unavailable after grace period
- [ ] NFO sidecar (`.nfo`) takes precedence over TMDB title — confirm a deliberately-misleading NFO overrides
- [ ] Music album with embedded ReplayGain — `replaygain_track_gain` shows in track JSON
- [ ] Music album without art — Cover Art Archive fallback succeeds when MusicBrainz ID is present

### Live TV + DVR (if HDHomeRun or M3U source available)
*The DVR query layer is integration-tested ([dvr_integration_test.go](internal/db/gen/dvr_integration_test.go) — full state machine). These cases need a real tuner.*

- [ ] Channels list renders, includes EPG data
- [ ] Click a live channel — playback starts <8s
- [ ] Schedule a recording 5 minutes in the future — record starts on time, file appears in `dvr` library and is playable end-to-end
- [ ] EPG ID auto-match: import a Schedules Direct lineup, channels with matching callsigns auto-link
- [ ] Series rule: schedule "new only" on a daily show — only new episodes record, reruns skipped

### Auth providers — real IdP integration
*Enabled-flag and discovery endpoints are unit-tested ([uat_extras_test.go](test/uat/uat_extras_test.go) — TestOIDCEnabled / TestSAMLEnabled / TestLDAPEnabled / TestSAML_MetadataIsPublic). These cases need real upstream services that automation can't simulate.*

- [ ] Google OAuth: sign in from incognito, account auto-created (or linked to matching email)
- [ ] GitHub OAuth: same
- [ ] Discord OAuth: same
- [ ] OIDC (generic): redirect roundtrip with real Authentik/Keycloak — nonce + state validate, claims map to user
- [ ] SAML: SP-initiated flow with real IdP — login lands on home, refresh works after 1h
- [ ] LDAP: bind with valid creds → login; invalid → "invalid credentials"; ambiguous filter → invalid creds (not enumeration)
- [ ] Forgot password: enter known email, receive email, click link, set new password — old sessions kicked out (this is the integration test of the SMTP delivery + token email + the BumpSessionEpoch path)
- [ ] First-user setup: nuke the DB, register first user, confirm they're admin

### Admin operations — real external integrations
*CRUD and admin gates are UAT-tested. These cases need real external services to verify the round-trip.*

- [ ] Settings → Email → Send Test → email arrives at admin inbox (real SMTP)
- [ ] Plugin: register an MCP plugin (echo or filesystem MCP), trigger a tool call, confirm round-trip in logs (real outbound MCP)
- [ ] Webhook: register a webhook pointing at `webhook.site` URL, trigger a `media.play`, payload arrives + signature header validates (real outbound HTTP + HMAC)
- [ ] Backup → restore round-trip on a *separate* instance — login still works after, all data visible (the dry round-trip is integration-tested at [backup_integration_test.go](internal/api/v1/backup_integration_test.go); this is the "is the actual data there" check)

### Settings UX — that admins can actually find the toggle
- [ ] Change TMDB API key in UI → save → reload → value masked, scan picks up new key on next refresh
- [ ] Toggle SMTP → save → "Send Test Email" appears
- [ ] Add a CORS origin → save → cross-origin XHR from that origin succeeds
- [ ] Set parental rating ceiling on a managed profile → that profile can't see TV-MA items in hub or search

### Reverse-proxy + TLS deployment
*Half-set TLS rejection is unit-tested ([config tests](internal/config/config.go)). These verify the deployed shape.*

- [ ] Behind nginx with TLS — login + playback work, WebSocket / SSE for notifications stays open
- [ ] Built-in HTTPS (set `TLS_CERT_FILE` + `TLS_KEY_FILE`) — server starts on https, refusing http

### Cross-browser — only browsers can test browsers
- [ ] Chrome (latest) — golden path
- [ ] Firefox (latest) — golden path
- [ ] Safari (latest, macOS or iPad) — playback works, gapless music transitions cleanly
- [ ] Mobile Safari (iPhone) — playback fits viewport, controls reachable with thumb
- [ ] Mobile Chrome (Android) — same

### Accessibility quick pass
- [ ] Tab through login form — all controls focus-visible
- [ ] Press Space on a play button (don't click) — playback starts
- [ ] Item card has alt text on poster (inspect DOM)
- [ ] Run Lighthouse accessibility audit on the home page — no critical violations

---

## Tier 3 — Deep dives (cadence-driven)

### Endurance (monthly)
*Goroutine + connection-pool correctness is unit-tested but leaks only show up under load.*

- [ ] Start an 8h playback session (movie marathon or transcoded loop). Watch for:
  - [ ] Memory growth in `docker stats` — server under 1GB, worker under 2GB throughout
  - [ ] No FFmpeg zombies (`ps aux | grep ffmpeg` after session ends shows none)
  - [ ] Postgres connection count stable (`SELECT count(*) FROM pg_stat_activity` — should stay under 20)
  - [ ] Valkey memory stable (`INFO memory`)
  - [ ] No goroutine leak (`/debug/pprof/goroutine?debug=1` count stable across 1h samples)

### Hardware encode validation (when GPU/iGPU changes)
*Encoder selection logic is unit-tested. These cases verify the actual hardware path.*

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
*Most of these now have automated regression guards (referenced inline). The manual probe re-validates with adversarial creativity that the automation can't replicate.*

- [ ] **XSS attempt**: subtitle file with `<script>alert(1)</script>` cue — escaped, no alert *(Svelte autoescape is unit-tested but a crafted cue is the real-world stress test)*
- [ ] **SQL injection**: search with `'; DROP TABLE users; --` — no error, no damage *(sqlc parameterizes; spot-check)*
- [ ] **Path traversal probe**: `/artwork/..%2F..%2Fetc%2Fpasswd`, `/trickplay/..%2F`, `/media/stream/..` → 403 or 404, never 200 *(unit-tested at TestPathTraversal_Segment + the artwork/trickplay handlers, but worth the live re-probe)*
- [ ] **CSRF**: from `attacker.test` HTML, fetch `POST /api/v1/users/me/preferences` with `credentials: 'include'` — request blocked
- [ ] **Open redirect**: try `?redirect=https://evil.com` on every endpoint that takes a redirect param — none should honor cross-origin
- [ ] **Bearer leak**: check nginx access logs after a session — no `?token=` or `?apikey=` or `?device_token=` in any URL *(automated as TestLogger_NeverLogsRawQueryString — but verify nginx isn't logging differently)*

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
*The migration round-trip is integration-tested ([migrations rollback test](internal/db/migrations/rollback_integration_test.go) + [backup_integration_test.go](internal/api/v1/backup_integration_test.go)). These cases verify with **real production data**, not test fixtures.*

- [ ] Backup an N-1 production install, restore into N — schema migrates forward via `goose up`, all data accessible
- [ ] Skip a version: backup N-2, restore into N — same expectation
- [ ] Downgrade attempt: backup N, try to restore into N-1 → refused with 409 unless `?force=true`

---

## Reference: what's now automated

When something fails in production and you wonder "why didn't a test catch this?", check here first. If the area below has a test name, add a regression case to it. If it doesn't, the manual plan above probably has the entry.

| Area | Automated coverage |
|---|---|
| **Trickplay endpoint auth + ACL** (H1) | UAT `TestTrickplay_ServeFile_RequiresAuth`, unit `internal/api/v1/trickplay_test.go` |
| **Password reset session invalidation** (H2/H3) | UAT `TestPasswordReset_SuccessRevokesEverySession`, unit `password_reset_test.go` |
| **Demote / role-change token revocation** | UAT `TestSessionEpoch_DemotionRevokesTargetToken` |
| **Segment-token revocation on credential rotation** (S2) | unit `internal/transcode/segtoken*` + `internal/api/v1/users_test.go` |
| **SAML cookie name + refresh** (S1) | UAT `TestSAML_*`, unit `auth_saml_test.go` |
| **Half-set TLS rejection** | unit `internal/config/config_test.go` |
| **Password policy 12-char floor** | UAT `TestPasswordReset_RejectsShortPassword`, unit `password_policy_test.go` |
| **Arr API key — header only, no query-param** (M2) | unit `internal/api/v1/arr_test.go` |
| **Pair `device_token` — header only** (M3) | unit `internal/api/v1/auth_pair_test.go::TestExtractDeviceToken_IgnoresQueryParam` |
| **Bearer / token leak in logs** | unit `internal/api/middleware/proxy_logging_test.go::TestLogger_NeverLogsRawQueryString` |
| **SSRF on webhook URL validation** | unit `internal/webhook/delivery_test.go`, `internal/safehttp/safehttp_test.go` |
| **SSRF on plugin egress** | unit `internal/plugin/egress_test.go` (host allowlist + IP-pin + no redirects) |
| **LDAP injection** (filter escaping) | unit `internal/api/v1/auth_ldap_test.go` |
| **OIDC state/nonce/PKCE validation** | unit `internal/api/v1/auth_oidc_test.go` |
| **CORS Allow-Credentials never set with `*`** | unit `internal/api/middleware/cors_test.go` |
| **TrustedRealIP / IsSecure spoofing guards** | unit `internal/api/middleware/proxy_logging_test.go` |
| **Recover middleware doesn't leak panic value** | unit `internal/api/middleware/proxy_logging_test.go::TestRecover_PanicReturns500` |
| **Path traversal on `/segments/`, `/artwork/`, `/trickplay/`** | UAT `TestPathTraversal_Segment` + handler regex whitelists are unit-tested |
| **DVR full state machine** (scheduled → recording → completed → expired) | integration `internal/db/gen/dvr_integration_test.go` (needs Docker) |
| **Watch event partition routing + history dedup window** | integration `watch_events_integration_test.go` |
| **Plugin registry CRUD + role/enabled filter** | integration `plugins_integration_test.go` |
| **Notification scoping + read-flag** | integration `notifications_integration_test.go` |
| **Password reset / invite token TTL+used filter** | integration `password_reset_integration_test.go`, `invites_integration_test.go` |
| **Arr services default-flag dance** (one-per-kind invariant) | integration `audit_arr_integration_test.go` |
| **Favorites idempotency + soft-delete hiding** | integration `favorites_integration_test.go` |
| **Library access ACL (the IDOR backstop)** | integration `library_access_integration_test.go` |
| **Refresh-token rotation + expiry filter** | integration `sessions_integration_test.go` |
| **Settings store round-trip for every typed config** | integration `internal/domain/settings/service_integration_test.go` |
| **Auth helpers (PASETO, AES-256-GCM, bcrypt cost)** | unit `internal/auth/*` (84% coverage) |
| **Settings, Plugins, Tasks, ArrServices, Audit, Maintenance, Backup, Invite admin gates** | UAT `uat_extras_test.go` (each has a TestX_RequiresAdmin case) |
| **Lyrics, People, Capabilities, Favorites, Notifications auth gates** | UAT `uat_extras_test.go` |
| **OIDC / SAML / LDAP enabled-flag public endpoints** | UAT `uat_extras_test.go` |
| **NFO / M3U / XMLTV parser panic-free contract** | fuzz `internal/livetv/m3u_fuzz_test.go`, `xmltv_fuzz_test.go`, `internal/metadata/nfo/nfo_fuzz_test.go` (run with `-fuzz=Fuzz... -fuzztime=30s`) |
| **Health endpoints behavior under DB/Valkey/migration failure** | unit `internal/observability/health_test.go` |
| **OTel trace_id/span_id injection into logs** | unit `internal/observability/slog_trace_test.go` |
| **PIN flow** (set/clear/verify, no enumeration) | unit `cmd/server/user_service_test.go` |
| **Valkey set ops** (segment-token revoker primitives) | unit `internal/valkey/client_test.go` |
| **Streaming tracker dedup + Valkey-mode** | unit `internal/streaming/tracker_valkey_test.go` |

If automated coverage drops in any of those areas, *add a test*, don't move it to this plan.

---

## How to run a tier

1. Cut the relevant section into the release ticket as a checklist
2. Each unchecked box at the end → either fix-or-defer decision in the ticket
3. After the release ships, append a one-line "what broke that the plan didn't catch?" — that's the next addition to this doc

The plan is only useful if it grows from real misses, not from imagined ones.
