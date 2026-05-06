# Roku client — TODO

Working list of feature parity items vs. [android_native](../android_native/) and the source-of-truth docs:
- [docs/comparison-matrix.md](../../docs/comparison-matrix.md) — claimed feature set
- [docs/api/openapi.yaml](../../docs/api/openapi.yaml) — server API surface

Audit snapshot: 2026-05-05 (after android_native picked up subtitle styling + Cast). Latest revision 2026-05-06: P0 #1 shipped, #2 confirmed already shipped, #3 partially shipped (data side; rendering deferred — see notes).

---

## P0 — Closeable gaps that ship on android_native and matrix promises

### 1. Browse library by type (`LibraryScene`) — ✅ shipped 2026-05-06

[LibraryListScene.xml](components/browse/LibraryListScene.xml) lists the user's libraries; [LibraryScene.xml](components/browse/LibraryScene.xml) renders the items as a poster grid backed by `GET /libraries/{id}/items?limit=200`. Reachable from a "Libraries" button on HomeScene's top nav. Click routing matches FavoritesScene: containers → DetailScene, photos → PhotoScene, leaves → PlayerScene. Filter chips per library type are deferred — single-type libraries are the common case and a chip strip is visual noise without the multi-type case to disambiguate.

---

### 2. Auto-advance to next episode on EOS — ✅ already shipped (rediscovered 2026-05-06)

The original audit missed that [PlayerScene.brs](components/playback/PlayerScene.brs) `onVideoState` already auto-advances on the firmware `state="finished"` edge. The Up Next overlay is the ahead-of-EOS UI for early-accept / dismiss; if the user neither accepts nor dismisses, EOS triggers `goToNext(m.nextSibling)`. Behaviour matches android_native: types with a meaningful next-sibling (episode / podcast_episode / track / audiobook_chapter) auto-advance; movies bail to home. Stale comment in `onVideoState` updated 2026-05-06 to match the actual code path.

---

### 3. Trickplay thumbnails on the seekbar — ⚠ partial: data side shipped, rendering deferred

**What shipped 2026-05-06:** [Trickplay.brs](source/playback/Trickplay.brs) — a VTT parser that converts the WebVTT index into an array of `{start_ms, end_ms, sprite_path, x, y, w, h}` cues, plus `Trickplay_FindCue(cues, posMs)` for per-frame lookup. 18 unit tests cover timestamp parsing, cue parsing (with / without nested paths, with / without xywh fragment), full VTT parsing, CRLF tolerance, cue-lookup boundary cases.

**What's deferred:** rendering the cropped sprite above the seekbar during scrub. The original scope assumed Roku's `Poster` had an `imageRegion` field — it doesn't. Two paths for the visual layer:

   1. **Server-side BIF endpoint** (preferred — Roku firmware natively renders trickplay from a BIF binary on the Video node's built-in scrub bar; no custom UI). Add `/api/v1/items/{id}/trickplay/index.bif` that assembles per-frame JPEGs from the existing sprite chunks at scan time. Roku then sets `Content.bifUrl` and gets thumbnails for free. Benefits any future Apple TV / webOS / Tizen client too.
   2. **SceneGraph clipping with offset Poster** (no server change, device-tier validation needed). Wrap a full-sprite Poster in a Group sized to (w, h) with the Poster offset by (-x, -y); only the cue region renders. Scrub detection by observing `Video.position` jumps faster than playback rate.

Path 1 is cleaner. Path 2 reaches deeper into BrightScript than is comfortable to ship without on-device testing.

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
