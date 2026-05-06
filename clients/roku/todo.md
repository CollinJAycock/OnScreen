# Roku client — TODO

Working list of feature parity items vs. [android_native](../android_native/) and the source-of-truth docs:
- [docs/comparison-matrix.md](../../docs/comparison-matrix.md) — claimed feature set
- [docs/api/openapi.yaml](../../docs/api/openapi.yaml) — server API surface

Audit snapshot: 2026-05-05 (after android_native picked up subtitle styling + Cast).

---

## P0 — Closeable gaps that ship on android_native and matrix promises

### 1. Browse library by type (new `LibraryScene`)
**Why:** Every Roku user currently has to use Search to find their movies. android_native ships a dedicated library grid; matrix claims library browse as ✅. Big UX win.

**Scope:**
- New `components/browse/LibraryScene.xml` + `.brs` with a poster grid backed by `GET /libraries/{id}/items?type=movie&sort=title&order=asc`.
- Paging via the existing `Endpoints.brs` envelope (`data` + `meta.total` + `meta.cursor`).
- Library picker on the home hub: tile per library_id from `GET /libraries`.
- Filter chips along the top row mirroring [SearchScene.brs](components/search/SearchScene.brs)'s pattern — pre-applied based on the library's `type` (movie library defaults to `type=movie` chip on, etc.).
- Resume / Play actions identical to the home hub's continue-watching tiles.

**Estimate:** medium. ~1 new scene, ~1 new task fetcher, ~1 envelope mapper. No server changes.

---

### 2. Auto-advance to next episode on EOS
**Why:** Up Next overlay exists at [PlayerScene.brs:67](components/playback/PlayerScene.brs#L67) but tapping the button is the only path. android_native auto-advances at end-of-stream and pre-buffers via the MediaSession service. Stops users sitting through credits to manually click "next."

**Scope:**
- In `PlayerScene.brs`, hook the existing `position` observer for `position == duration - epsilon`.
- Resolve the next sibling using the parent_id + child-index pattern android_native's [NextSiblingResolver.kt](../android_native/app/src/main/java/tv/onscreen/mobile/playback/NextSiblingResolver.kt) already establishes (album → next track, season → next episode, audiobook → next chapter).
- Mirror android_native's behaviour: only auto-advance for episodes/tracks/chapters; movies / standalone items stay on the post-roll overlay.
- Respect the user dismissing the Up Next overlay (`dismissed` flag persisted in the existing skip-marker dismissal store).

**Estimate:** low. ~50 lines of `.brs`, no new scene.

---

### 3. Trickplay thumbnails on the seekbar
**Why:** [Endpoints.brs:59](source/api/Endpoints.brs#L59) already fetches the trickplay sprite-sheet status, but the seekbar doesn't render thumbnails. Matrix claims trickplay sprite sheets as ✅ (a v2.1 differentiator vs Plex Pass / Emby Premiere). Currently a stubbed feature on Roku.

**Scope:**
- Pull the VTT + sprite URLs from `GET /api/v1/items/{id}/trickplay/index.vtt` and the sprite chunks under `/api/v1/items/{id}/trickplay/sprite_{n}.jpg`.
- Parse the VTT cues in BrightScript (existing helper-style: `Endpoints.brs` parses HLS, similar pattern). Each cue carries an `xywh` fragment naming the position inside the sprite sheet.
- During seek-bar scrub, render the cue at the scrubbed position above the bar. Roku's `Poster` node supports `imageRegion` for cropped sprite display.
- Preload sprite_0 at playback start so the first scrub feels instant.

**Estimate:** medium. The VTT parser + sprite cropping is the main work; everything else slots into the existing PlayerScene.

---

## P1 — Useful, not matrix-promised on phone parity

### 4. TMDB discover + in-app requests
- API endpoints (`/discover/*`, `/requests`) exist server-side; no Roku UI.
- Matrix claims ✅ on server, ❌ on Roku.
- Effort: 1 new scene + a request-submit helper. Depends on having a typing surface — Roku's on-screen keyboard is the existing search pattern.

### 5. SSO login (OIDC / SAML / LDAP)
- Currently only local login + PIN pair on [LoginScene.brs](components/setup/LoginScene.brs).
- Matrix lists OIDC / OAuth / SAML / LDAP as ✅ on the server. Roku users on LDAP-only servers must PIN-pair from another device.
- Effort: read `GET /capabilities` and surface a "Sign in via web" deeplink for whichever providers the server reports — the existing pair flow is functionally the same UX.

### 6. Playlist CRUD
- Read-only today; matrix's Playlists tag in openapi promises full CRUD.
- Effort: medium — needs an editing UX which is fiddly on TV with a remote.

### 7. Smart playlists
- Server ships rule-based playlists (matrix ✅). Roku has no UI to construct them. Defer until 6 lands.

---

## P2 — Skip (platform-incompatible or out of scope)

These were considered and intentionally rejected:

| Feature | Why skip |
|---|---|
| **Subtitle styling** (size / colour / background / outline) | Roku `Video` node doesn't expose programmatic font / colour / outline control. System-level Captions Settings are OS-owned, not app-invocable. android_native ships this; Roku platform doesn't allow it. |
| **Cast sender** | Roku is itself a Cast-style receiver. Sending Cast from the Roku app to another receiver isn't a real use case. |
| **Mobile offline downloads** | No persistent filesystem UI on Roku. The matrix lists ❌ on the row anyway. |
| **AirPlay** | Apple ecosystem only — Roku TVs don't receive AirPlay. |
| **DLNA / UPnP server** | Roku consumes DLNA but doesn't run a server. |
| **Sync-watch / watch parties** | Niche; nobody in the matrix ships it except Jellyfin. |
| **Last.fm / ListenBrainz scrobbling** | Better placed on the server side or web — surfacing track-level events to the Roku UI doesn't help the user. |

---

## Order of attack

If picking one at a time: **1 → 2 → 3** (LibraryScene → auto-advance → trickplay). Each touches a different scene so they don't conflict. After P0 lands the matrix's Roku column is honestly ⚠ → near-✅ on the rows that matter.

P1 items 4-7 stay on this list but are each their own multi-day chunk; tackle after P0.
