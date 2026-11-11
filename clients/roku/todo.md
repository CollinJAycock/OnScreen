# Roku client — TODO

Working list of feature parity items vs. [android_native](../android_native/) and the source-of-truth docs:
- [docs/comparison-matrix.md](../../docs/comparison-matrix.md) — claimed feature set
- [docs/api/openapi.yaml](../../docs/api/openapi.yaml) — server API surface

Audit snapshot: 2026-05-05 (after android_native picked up subtitle styling + Cast). Latest revision 2026-05-06: P0 #1 shipped, #2 confirmed already shipped, #3 partially shipped (data side; rendering deferred — see notes).

---

## First run on a device

The channel has never run on a Roku. When one is in Developer Mode:

1. `ROKU_HOST=<ip> ROKU_DEV_PASSWORD=<pw> npm run package && npm run sideload`.
2. Sign in with a PIN pair (Setup → server URL → pair code).
3. Capability header: compare what the server logs for the device with
   Client_DeviceCaps (4K output, HEVC Main 10, the TV's HDR10 / HLG).
   CanDecodeVideo's "main 10" profile string and GetDisplayProperties'
   Hdr10 / HLG fields are from Roku's docs and unconfirmed here.
4. Playback, reading the server log's real decision (direct / remux /
   transcode) for each: H.264 1080p, HEVC 4K SDR, HDR10 (TV's HDR badge),
   HLG, an HDR file on an SDR TV (tone-mapped), E-AC-3 / AC-3 audio, image
   subtitles (burn-in), seeking past the transcoded range, the BIF seek
   thumbnails, Up Next and auto-advance, resume.
5. Music, audiobooks, photos, search, Watchlist, Favorites, libraries.
6. Back on every screen, and Home / relaunch keeping the session.
7. Token refresh: let the access token expire with several screens loading.
   If two refreshes race and the loser's 401 arrives before the winner has
   stored the new pair, Client_RefreshSync clears the session; watch for an
   unexpected sign-out.
8. Certification hooks (Roku's Static Analysis / Channel Behavior Analysis
   tools in the developer dashboard, and the debug console on 8085):
   - AppLaunchComplete once Home's rows are up; AppDialogInitiate /
     AppDialogComplete around Setup / Sign in at launch; nothing twice.
   - Deep links, launch and while running:
     `curl -d '' "http://<ip>:8060/launch/dev?contentId=<item id>&mediaType=movie"`
     and `.../input?contentId=…&mediaType=episode`: movie / episode play,
     series / season open the page, Back goes player → page → Home → exit.
     A deep link while signed out opens after sign-in. A bad contentId
     (not a UUID, or an item that's gone) lands on Home or the page's
     error, never a crash.
9. Dolby Vision: not claimed, by choice (2026-11-04). The Stick 4K decodes it
   and the C1 takes it, but the server keeps DV only in fMP4 segments and
   Dolby audio in fMP4 HLS is silent on a Roku (declared or not), while the
   Roku needs TS for its Dolby audio. DV titles play from the HDR10 base.
   See docs/dolby-vision.md.
10. Spanish (v3 translations, source/util/I18n.brs): with the Roku's menu
    language set to Español, Setup / Sign in / Pair come up in Spanish
    before sign-in. Settings › Language cycles Device language → English →
    Español; each press redraws Settings at once with the focus on the
    row, and the account gets it (`PUT /users/me/ui-language`; a pre-v3
    server answers 404 and the row's note says so). An account set to
    Spanish on another device turns Home Spanish on its next visit; one
    set to Chinese changes nothing here. Check the fixed-width spots in
    Spanish: the Skip Intro / Credits pill and the slideshow pill (both
    grow to fit a wider label), the title page's button row, the Settings
    rows (title and value share 744 px), and the Library header buttons.
    The server's error messages (wrong password, PLAYBACK_STOPPED) should
    come in Spanish too (`Accept-Language`).
    **Checked 2026-11-11** (Stick 4K, menu language Español = `es_MX`, QA):
    Setup / Sign in / Pair in Spanish, wrong password message, Home, top
    bar, title page row (5 pills fit), Library header buttons, slideshow
    pills, Settings in both languages; an account set to English kept Home
    English on a Spanish Roku; the row cycles and saves (no failure note).
    Fixed on the way: Sign out stayed violet beside the focused Language row
    after a change (setFocus before attach), and "Idioma del dispositivo
    (Español)" ran into the row's title (now "Del dispositivo (Español)").
    Later the same day: "Abajo: saltar intro" and the Up Next card ("A
    CONTINUACIÓN … Abajo para reproducir ya") fit. Playing them showed a
    bug that isn't about language: on a remux the session playlist grows,
    so the Video node's duration was only what had been written; after a
    Skip Intro the playhead sat within Up Next's 25 s lead (the card came
    up 90 s in) and the progress reports carried that short duration, so
    the server's 90 % rule marked the episode watched after ~2 minutes.
    Up Next, progress and the end-of-stream report now use the item's
    length (itemDurationMs: item, then file, then stream); checked on the
    next episode (no card, not marked watched).
    Still open: the Skip Credits pill (same pill, other text), and the
    server's own Spanish messages, which need QA on the server-text release
    (QA still answered "invalid credentials" in English).

## P0 — Closeable gaps that ship on android_native and matrix promises

### 1. Browse library by type (`LibraryScene`) — ✅ shipped 2026-05-06

[LibraryListScene.xml](components/browse/LibraryListScene.xml) lists the user's libraries; [LibraryScene.xml](components/browse/LibraryScene.xml) renders the items as a poster grid backed by `GET /libraries/{id}/items?limit=200`. Reachable from a "Libraries" button on HomeScene's top nav. Click routing matches FavoritesScene: containers → DetailScene, photos → PhotoScene, leaves → PlayerScene. Filter chips per library type are deferred — single-type libraries are the common case and a chip strip is visual noise without the multi-type case to disambiguate.

---

### 2. Auto-advance to next episode on EOS — ✅ already shipped (rediscovered 2026-05-06)

The original audit missed that [PlayerScene.brs](components/playback/PlayerScene.brs) `onVideoState` already auto-advances on the firmware `state="finished"` edge. The Up Next overlay is the ahead-of-EOS UI for early-accept / dismiss; if the user neither accepts nor dismisses, EOS triggers `goToNext(m.nextSibling)`. Behaviour matches android_native: types with a meaningful next-sibling (episode / podcast_episode / track / audiobook_chapter) auto-advance; movies bail to home. Stale comment in `onVideoState` updated 2026-05-06 to match the actual code path.

---

### 3. Trickplay thumbnails on the seekbar — ✅ done (v2.7)

The server builds a BIF from the trickplay sprites (`GET /trickplay/{id}/index.bif`, cached next to the sprites after the first request) and [PlayerScene.brs](components/playback/PlayerScene.brs) points the content node's `HDBifUrl` / `SDBifUrl` / `FHDBifUrl` at it, so the firmware's own trick-play bar renders the thumbnails — no custom UI. [Trickplay.brs](source/playback/Trickplay.brs) holds the URL builder and the rule for when the thumbnails apply (the version holding the item's first file, a clock starting at 0, not audio). The earlier client-side VTT parser was removed; nothing rendered from it. Needs a check on a real Roku.

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
