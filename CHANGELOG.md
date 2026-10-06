# Changelog

OnScreen tracks server + first-party-client changes. Versions follow
[semantic versioning](https://semver.org/) — minor releases add
endpoints / features without breaking existing API contracts; major
releases (none yet past v2) reserve the right to break things.

The server API is frozen as of **v2.2.0** — see [docs/server-lock.md](docs/server-lock.md)
for the lock posture and what's expected to land in v2.3+ minor
releases without breaking v2.2 clients.

## [v2.5.0] — unreleased

Planning: [docs/v2.5-roadmap.md](docs/v2.5-roadmap.md) — the distribution
wave (stores for the existing fleet), the non-Apple platform track
(Chromecast receiver, Android Auto, CLI, Flathub), client-parity honesty,
and product depth (Trakt/Last.fm, collections, music browse, audiobook UX).

### Added

- **`onscreen-cli`** — a terminal client that plays through mpv: login (with
  TOTP), browse/search, server-authoritative playback decision under an mpv
  capability profile, direct play via Authorization header, transcode
  fallback with session teardown, and resume-parity progress via mpv IPC
  (non-Windows). `play --print-url` doubles as a playback-matrix QA probe.
- **Admin stream termination** — an admin can stop any user's stream from
  the Now Playing cards (`POST /api/v1/sessions/{id}/stop`, audit-logged
  with both parties), in every playback mode, with an optional message (up
  to 200 characters) shown to the viewer. A transcode is torn down; for
  direct play, direct stream and remux the server also refuses that user +
  item + client IP for 2 minutes (`403 PLAYBACK_STOPPED` on media bytes,
  transcode start and progress beacons), so a client that ignores the stop
  event can't simply restart. Owner-only remains the rule for non-admins.
- **Now Playing detail** — each card shows the user and household profile,
  device, LAN or remote, the playback decision, source → output format and
  the reasons for a transcode.
- **Runtime codec demotion on the TV fleet** — webOS ports the web player's
  full auto-escalation (bufferAppendError → demote → restart lands on
  H.264); Tizen, whose AVPlay claims are static, records a persistent
  demotion on NOT_SUPPORTED-class errors so overclaimed panels self-correct
  on the next attempt.
- **Desktop installers for Windows and Linux** — a `v*` tag now attaches the
  desktop client's installers to its GitHub release: an MSI and an NSIS
  setup `.exe` for Windows x64, and a `.deb`, an `.rpm` and an `.AppImage`
  for Linux x86_64 (glibc 2.39+: Ubuntu 24.04, Debian 13, Fedora 40 or
  newer), with `onscreen-desktop-checksums.txt`. No release carried them
  before: the Desktop client workflow failed on every tag (its Windows
  upload pattern used `{msi,nsis}`, which upload-artifact doesn't expand,
  and the Linux build had no ALSA headers). The `.deb` and `.rpm` declare
  ALSA and D-Bus, which Tauri's defaults leave out (on a minimal Ubuntu the
  app couldn't start).
  - **Versions follow the tag.** `v2.5.0` builds `OnScreen_2.5.0_*`
    installers, so a newer `.deb`/`.rpm` upgrades an installed one (the
    desktop client was versioned 0.1.0 until now). WiX takes numeric
    versions only, so a pre-release tag such as `v2.5.0-rc1` builds 2.5.0
    installers. The MSI upgrade code is pinned in `tauri.conf.json`, so
    each MSI replaces any earlier one.
  - **Video in the AppImage.** The AppImage carries GStreamer and the
    plugins WebKitGTK plays video with (gst-libav for H.264, HEVC, AAC,
    AC-3 and E-AC-3, plus VP9, Opus and the MP4/Matroska/WebM demuxers, and
    `autovideoflip` so rotated phone clips play upright), so
    it no longer depends on the host's plugins, which its GStreamer
    couldn't find outside Debian-family distros. The `.deb` recommends
    `gstreamer1.0-libav` and `gstreamer1.0-plugins-bad`, which Ubuntu
    leaves out by default.
  - **Icons.** App icons are generated from the 1024 px master (new:
    `docs/store-assets/master/icon-1024.png`) instead of resized favicons:
    a Windows icon up to 256 px and standard Linux icon sizes. Linux menus
    list the client under Audio/Video.
  - **Repeatable builds.** The desktop `Cargo.lock` is committed and
    release builds use `--locked`; CI and the Docker recipe pin Rust 1.93.0
    and tauri-cli 2.12.1, and PRs that touch the desktop client (including
    Dependabot's `Cargo.lock` bumps) run `cargo test --locked` with that
    Rust. When a tag is moved, the newer run cancels the superseded one,
    and `publish` uploads only if the tag still points at the commit it
    built. A manual run of the workflow on a tag (re)publishes that tag's
    installers.

  The installers are not code-signed, so Windows SmartScreen warns on first
  run, and there is no auto-updater yet. macOS is left out until the client
  compiles there. Linux installers also build locally in Docker
  (`clients/desktop/linux-build`); see the desktop client's README.
- **Desktop client: setting and changing the server** — the installed
  desktop app couldn't be pointed at a working server.
  - **Bare addresses work.** `192.168.1.50:7070`, `nas.local:7070` or
    `onscreen.example.com` are accepted. Without a scheme `https://` is tried
    first, then `http://`, as on the TV apps. Before, the field's URL
    validation blocked them with "Please enter a URL."
  - **LAN servers over plain `http://` are allowed.** Release builds refused
    `http://` to everything but loopback, including the setup screen's own
    `http://192.168.1.50:7070` example. Plain `http://` is now accepted for
    local-network hosts, using the same rules as the TV apps, and the sign-in
    screen and the Server page flag it as not encrypted. It is still refused
    for public hosts.
  - **The server is tested before it is saved,** and the error says what's
    wrong: no answer, a bad certificate, not an OnScreen server, or a server
    whose CORS policy refuses the app (naming the origin to allow). Before,
    any well-formed URL was saved and the app opened a sign-in page that
    could never work.
  - **The server can be changed.** Use **Change server** on the sign-in
    screen, **Server** in the sidebar (for every user) or the tray icon's
    **Change server…**. When the saved server doesn't answer at startup, a
    **Can't connect** screen offers **Try again** and **Change server**.
    Before, nothing linked the server page; the only way out was deleting
    `settings.json` by hand. Disconnect now asks for confirmation in the page
    itself: in the desktop webview, `window.confirm` never blocked, so the
    confirmation was skipped.
  - **Needs a server that allows the app's origin.** Servers with the CORS
    fix (see "The desktop app couldn't reach a server" under Fixed) allow
    it automatically; on an older one an admin adds `http://tauri.localhost`
    (Windows) or `tauri://localhost` (Linux) to the CORS allowed origins and
    restarts the server. The setup screen says when that's the problem.

The additions below are server + web client unless marked otherwise; the
Android apps', the Tizen / webOS TV apps' and the Roku channel's shares
are listed at the end of this section.

Requests:

- **Per-user auto-approve** — separate Movies and TV switches on
  Settings → Users (`PUT /api/v1/users/{id}/request-permissions`); an
  eligible request is sent to Radarr/Sonarr when it is created. Admins are
  always auto-approved, a profile with a content-rating ceiling never is, and
  a non-admin request that names a specific *arr instance goes to the queue.
  Defaults for new accounts (registration, invite, OIDC/SAML/LDAP first
  sign-in) are set in the "New users" block on the same page; household
  profiles start off. If the hand-off fails, the request stays pending for an
  admin.
- **Request quotas and a can-request switch** — per-user movie and TV limits
  per rolling window, with server-wide defaults in the "New users" block
  (0 = unlimited, the default; window 1–90 days, default 7). An over-quota
  request is still created but waits for an admin instead of being
  auto-approved; declined and cancelled requests don't count; admins are
  exempt. Switching a user's requests off
  answers `403 REQUESTS_DISABLED`. Search shows the caller's remaining
  allowance (`GET /api/v1/requests/quota`).
- **Admin request queue** — a pending-requests badge in the nav
  (`GET /api/v1/requests/pending-count`), an admin notification for each new
  pending request, the requester's name on every row, and an approve dialog
  that chooses the Radarr/Sonarr instance, quality profile and root folder,
  pre-filled with what a plain approve would use.
- **Live download status** — a new `arr_request_sync` task (every 5 minutes,
  plus targeted syncs from the *arr webhook) reads Radarr/Sonarr and shows
  searching, queued, downloading (progress and ETA), importing, stalled or
  failed on each request. A failed download notifies the requester and the
  admins once. Settings → Arr Services shows each instance's reachability,
  its own health checks, free space behind its root folders and queue size.
- **Season-level TV requests** — a show request can name seasons, and a user
  can hold several requests for one show as long as their seasons don't
  overlap, so "Request more seasons" works for a show already in the
  library (Sonarr gets the new seasons monitored and searched). Each season
  that completes gets a one-time "ready" notice; the request turns available
  when every requested season is complete.
- **Upcoming tab** — Radarr release dates and Sonarr air dates for the next
  30 days as a month grid or agenda (`GET /api/v1/upcoming`), filtered by
  the viewer's library access and rating ceiling.

Watching:

- **Mark watched / unwatched** on a movie, episode, season or show
  (`POST`/`DELETE /api/v1/items/{id}/watched`; a season or show marks every
  episode within the viewer's rating ceiling). Marks don't count as plays,
  so analytics are unaffected.
- **Continue Watching controls and new home rows** — remove a title from
  Continue Watching (it comes back with new activity); a **Next Up** row
  (the next episode of shows in progress) and a **Plan to Watch** row (from
  the watching status), both part of the per-user hub layout. Resume / Play
  on a show or season page starts the right episode and opens on its season
  (`GET /api/v1/items/{id}/up-next`).
- **Library grids** — watched badges, unwatched-episode counts on shows and
  seasons, a watch filter (unwatched / in progress / watched) and "Surprise
  me" (`GET /api/v1/libraries/{id}/random`).

Playback and library:

- **Automatic seek-bar thumbnails** — trickplay sprites are queued for items
  without them after every library scan (one ffmpeg job at a time, paused
  while a transcode runs), and a nightly `trickplay_backfill` task (seeded
  enabled, 45-minute budget) works through the back catalogue. A
  per-library switch, progress and "Generate now" sit in library settings.
- **Play on…** — the web player and item page can send the current title and
  position to another of the user's devices (`POST /api/v1/playback/transfer`
  to a client that has played something in the last 30 days).
- **ReplayGain in the browser** — track or album gain plus a ±15 dB preamp,
  capped against the tagged peak so it never clips, applied through Web
  Audio. It shares the desktop engine's per-device setting (off by default,
  under Audio in the user menu). When media is served cross-origin (an
  object-storage media store redirects streams to presigned URLs) the gain
  stage is skipped and playback continues at unity gain.
- **Music browse** — a music library's page switches between its Artists
  (the grid as before) and an **Albums** index: every album in the library
  as a cover with its artist (linked) and year, sortable by title, artist,
  year or date added, filterable by genre and year. The view, sort and
  filters live in the URL, so Back from an album, reloads and deep links
  keep them. Genres and years are read from the albums (the files' tags;
  artists carry none), so Browse genres / Browse years on a music library
  now list them and open that genre's or year's albums, and a music
  library's Recently Added header on the home page opens its albums
  newest first. API, all additive: `sort=artist` on
  `GET /api/v1/libraries/{id}/items`, `parent_id` / `parent_title` on rows
  listed below the top level (an album's artist), and `?type=` on
  `GET /api/v1/libraries/{id}/genres` and `/years`.
- **TMDB franchise collections** — a movie's TMDB collection becomes a
  server-owned collection once the server holds two or more of its films
  (at enrichment, plus a nightly `franchise_collections` task for movies
  matched earlier). Libraries gain a Collections tab and movie pages a
  "Part of the …" shelf; the collection page lists the films the server
  doesn't have, with a Request button, for profiles without a rating
  ceiling.
- **Report a problem and Library health** — users report video, audio,
  subtitle, wrong-match or other problems from an item or the player (one
  open report per user, item and kind; at most 10 open per user). Admins work
  them on Settings → Library health, which also lists files the integrity
  probe marked damaged plus the unmatched and missing-art counts, and can
  ask Radarr/Sonarr to re-grab a title, optionally blocklisting the release
  it last grabbed.
- **Notification agents** — Settings → Notifications sends server events to
  Discord, Telegram, ntfy, Gotify or email (through the server's SMTP
  settings): requests pending / approved / available / partly available /
  failed, reported problems, new content (one batched message per scan
  window, public libraries only), failed scheduled tasks, failed backups, and
  no transcode worker available. Per-agent event selection, a Send test
  button and last-delivery status. Secrets are encrypted at rest and never
  returned; delivery goes through the SSRF guard (see
  [docs/security.md](docs/security.md)).
- **Last.fm and Trakt scrobbling** — alongside ListenBrainz, per user and
  opt-in. Link under Settings → Scrobbling. It's done once on the web, and
  plays from every client count, because the server exports them from watch
  events.
  - **Last.fm**: finished tracks (past half the track, or 4 minutes) are
    scrobbled, and the playing track shows as scrobbling now.
  - **Trakt**: movies and episodes are sent as start / pause / stop, matched
    by TMDB / TVDB / IMDb id with a title and year fallback. Trakt records a
    play that reaches 80% as watched and keeps a paused one's progress.
    Marking a movie, episode, season or show watched by hand adds what it
    marked to the Trakt history as well (one request per mark; an item
    already marked isn't sent again). Marking unwatched removes nothing
    from Trakt, whose history also holds plays made elsewhere.
  - **ListenBrainz** now also shows the playing track (playing_now).
  - **Admin setup**: enter a Last.fm API account and a Trakt app under
    Settings → Scrobbling apps; secrets are masked like the other keys.
    Users link by approving OnScreen on Last.fm, or by entering a Trakt code
    on any device. No callback URL is needed, so a server that isn't
    reachable from the internet works.
  - **Credentials**: encrypted at rest, bound to their user, and re-sealed
    by `rotate-key`. An expiring Trakt token is refreshed in place, and one
    revoked on trakt.tv unlinks itself. Migration 00031.
  - **API**: `/api/v1/users/me/scrobble/{lastfm,trakt}/link[/complete]`, and
    `DELETE` on each to unlink.
- **Audiobook listening controls** — in the web audio player while a book
  plays:
  - **Speed** from 0.75× to 3×, pitch kept, saved per book and shared by
    its chapters; a book you haven't set starts at your latest speed. Music
    stays at 1×.
  - **Add bookmark** at the current position, with an optional note. The
    book's page lists its bookmarks (chapter, time, note): click one to
    play from there, edit its note or delete it.
  - **Sleep timer** — 15 / 30 / 45 / 60 minutes or end of chapter, then
    pause with the player left open, counting down meanwhile. Music gets it
    too ("end of track").
  - **Single-file books** (one file with embedded chapters, e.g. `.m4b`)
    now play in the audio player instead of the video player: the book
    page lists their chapters, Previous / Next move between chapters, and
    Resume starts the chapter you were in.
  - **API**: `GET`/`PUT /api/v1/items/{id}/playback-rate`,
    `GET`/`POST /api/v1/items/{id}/bookmarks`, `PATCH`/`DELETE
    /api/v1/bookmarks/{id}` (1000 bookmarks per book). Migration 00032.
- **Photo map on the web** — a photo library's geotagged photos on an
  OpenStreetMap map (Map on the library page), as clustered thumbnail
  markers, from the existing `GET /api/v1/photos/map` (library access and
  rating ceiling applied as before). Clicking a marker lists the photos
  taken there; each opens the photo viewer, whose Previous / Next walk that
  spot and whose close returns to the same map view. A library with more
  than 25,000 geotagged photos reloads per view as you pan and zoom.
  Leaflet is bundled and loads only with the map. The browser fetches the
  tiles from tile.openstreetmap.org as images, which the CSP's `img-src`
  already allows (no CSP change; a test now pins it), so that server sees
  which areas are viewed.
- **Photo albums on the web** — `/photos/albums` lists your albums with
  their cover and photo count; create, rename and delete them there, and
  open one to see its photos in the viewer (Previous / Next walk the
  album). Add photos with "Add to album…" in the photo viewer or by
  selecting several in a photo library (Select), and remove them on the
  album's page or from the viewer. Albums stay owner-only. Adding a photo
  that is already in the album is now a no-op (`204`) instead of a `500`
  from `POST /api/v1/photo-albums/{id}/items`; so is adding an item a
  collection or playlist already holds.

Android apps (TV + phone). The request features above are web-only by
design — neither Android app has any request functionality, and the phone
app's Search › Discover tab (which could request titles) was removed:

- **Next Up and Plan to Watch** home rows; **remove from Continue
  Watching** (long-press, or ⋮ on the phone).
- **Show/season Play button** driven by up-next ("Resume S3 · E4", "Play
  S3 · E5", "Watch again"), opening on the right season.
- **Mark watched / unwatched** for movies, episodes, seasons and whole
  shows (confirmation before unwatching a show).
- **Library grids**: watched / unwatched-count / progress badges, a Watch
  filter, and Surprise me.
- **Report a problem** from item pages.
- **Admin Stop** is honoured immediately, with the admin's message: the
  playback.stop event plus the server's 403 PLAYBACK_STOPPED. On the phone
  this covers background music too.
- **Phone music**: ReplayGain (Off / Track / Album + preamp,
  clipping-safe, same math as the web player; off by default), applied at
  the exact sample where each track starts. Albums play as one gapless
  queue. Album and artist pages gained a working Play button: album from
  track 1; artist from its earliest album, then the rest in order.
- **Phone item pages** say "Resume from 20:25" for something part-watched
  and "Watch again" once it's finished.
- **Audiobook listening speed** (0.75×–3×, pitch kept), saved per book on
  the server, so a book resumes at its speed on either app. It carries across
  chapters and into background playback. Music always plays at 1×.
  - **Phone**: a speed control in the player; an **Add bookmark** action
    with an optional note; and a **Bookmarks** list on the book's page. Tap
    a bookmark to play from it, or edit its note or delete it.
  - **Phone sleep timer**: gains **End of chapter**. It stops at the next
    chapter mark in a single-file book, or when the chapter file ends in a
    multi-file book.
  - **Phone multi-file books**: chapters play on from one to the next, and
    the book page has a working Play button.
  - **TV**: the speed control, for books and chapter files.
  - **TV multi-file books**: chapter files play as audio, so they keep
    playing on HOME / BACK like a single-file book, and play on from one
    chapter to the next in the book's order, on screen or in the
    background, at the book's speed. The last chapter ends the book.
  - On a server without these routes, speed still works on the device and
    bookmarks stay hidden.
- `request_*` notifications are never surfaced.

TV apps (Tizen + webOS), in lockstep. No request features: the Discover
page stays as it was.

- **Next Up and Plan to Watch** home rows; **remove from Continue
  Watching** by holding OK on a Continue Watching card, which opens its
  options (a short press still opens the title).
- **Show/season Play button** driven by up-next ("Resume S3 · E4", "Play
  S3 · E5", "Watch again" from the start), opening on the right season.
- **Mark watched / unwatched** for movies and episodes, "Mark all…" on show
  and season pages (confirmation before unwatching a show), and holding OK
  on an episode card.
- **Library grids**: watched / unwatched-count / progress badges, a Watch
  filter (all / unwatched / in progress / watched, kept while you visit a
  title), and Surprise me, for video libraries.
- **Report a problem** from movie, episode, show and season pages, with an
  optional note typed on the on-screen keyboard; kinds already reported
  are marked.
- **Admin Stop** ends playback at once with the admin's message: the
  playback.stop event, plus the server's 403 PLAYBACK_STOPPED on the
  heartbeat, a transcode start or an audio re-issue, and a probe of the
  stream when a direct-play audio stream errors.
- **Last.fm and Trakt** connect / disconnect on the Scrobbling screen,
  beside ListenBrainz. Trakt's code and activation address, and Last.fm's
  approval address, are shown as a QR code (a small in-tree encoder, no new
  dependency) and as text, for a phone; the screen polls until the link
  settles. A service the admin hasn't set up says so.
- **Audiobook listening speed** (0.75×–3×, ↑/↓ on the now-playing screen),
  loaded from and saved to the book on the server; music stays at 1×. Both
  apps play audiobooks through the webview's media element (not AVPlay on
  Tizen), keeping pitch where the engine does. If a TV accepts a speed but
  keeps playing at 1×, which the player checks while playing, the control
  is withdrawn for that play with a note.
- On a server without these routes, each feature stays hidden.

Roku channel (no request features, as on Android):

- **Roku catches up on watching** — as far as a remote allows:
  - **Home**: Next Up and Plan to Watch rows after Continue Watching;
    Continue Watching cards show a progress bar, and ✱ on one offers
    **Remove from Continue Watching**.
  - **Show and season pages**: Play is driven by up-next ("Resume S3 ·
    E4", "Play S3 · E5", "Watch again") and the episode row opens on that
    episode (a show's season row on its season). Seasons now open their
    own page instead of dead-ending in the player.
  - **Mark watched / unwatched** on movie, show and season pages ("Mark
    all…" asks first before unwatching a whole show), and from ✱ on an
    episode or season card.
  - **Watched checks, unwatched-episode counts and progress bars** on
    library grids and episode rows. No Watch filter yet: the library
    screen has no filter or sort control to hang it on.
  - **Report a problem** from movie, show and episode pages (kind only —
    the note is skipped).
  - **Admin Stop** ends playback with the admin's message. Roku has no
    event stream, so it reads the server's 403 PLAYBACK_STOPPED: on the
    next heartbeat, on a transcode start, or by probing the stream when a
    direct play errors.
  - Not on Roku: audiobook speed (neither the Video nor the Audio node has
    a playback-rate control) and Trakt / Last.fm linking (done once on the
    web; Roku plays count through the server).
  - **Plumbing this needed, which was broken on hardware**: requests used
    `roUrlTransfer.SetTimeout`, which doesn't exist (now a minimum
    transfer rate plus a bounded wait); POST responses were read from
    `PostFromString`, which returns the status code and discards the body
    (token refresh, pairing and transcode start now read it);
    pairing polled with POST a route the server serves as GET; the
    pair-code 201 was treated as a failure; `callFunc` navigation targets
    weren't declared on MainScene; the player sent its progress beacons
    and session DELETE from the render thread (now Task nodes); and the
    home header buttons and a detail page's episode row couldn't be
    reached with the D-pad.

- **Android TV: frame rate matching.** Video now plays at a display refresh
  rate that shows it evenly: 24 fps film at 24 Hz (23.976 at 23.976 Hz),
  25p at 50 Hz, 30p at 60 Hz, instead of the 3:2 judder of 24 fps at 60 Hz.
  On Android 11 and older (Nvidia Shield, Fire TV, most TV sets) the app
  requests the mode and holds playback until the TV shows a picture again,
  and the TV goes back to its usual mode when the player closes; moving on
  to the next episode keeps the mode a few seconds longer, so it doesn't
  blank the screen twice. Fire TV applies the request only with its own
  Match Original Frame Rate setting on (playback then starts after a
  moment); Nvidia Shield needs no setting of its own. On Android 12 and later the TV's Match content frame rate setting
  decides. A new Settings switch, Playback > Match frame rate (on by
  default), turns it off.
- **`frame_rate` on item files.** `GET /api/v1/items/{id}` now includes each
  file's probed frame rate, so a TV client can switch the display before the
  player has loaded the stream.
- **Capability keys `hlg` and `vp9MaxBitDepth`.** An HDR10 screen isn't
  necessarily an HLG one (NVIDIA SHIELD sends HLG flagged as SDR: a dark,
  green picture), and a device that decodes 8-bit VP9 may not decode 10-bit
  VP9 (Profile 2). With `hlg=0` an HLG source is transcoded and tone-mapped
  to SDR, as HDR is for an `hdr=0` client; with `vp9MaxBitDepth=8` a 10-bit
  VP9 source is transcoded. A client that sends neither key is decided
  exactly as before. The grammar is in
  [docs/capability-profiles.md](docs/capability-profiles.md) §5.1, and Now
  Playing shows both reasons.
- **`features.upcoming` and `features.live_tv_configured`.** Two capability
  flags that follow the server's configuration: an enabled Radarr or Sonarr
  feeds the Upcoming calendar, and at least one enabled tuner is set up.
  `live_tv` and `dvr` keep meaning "the Live TV subsystem is wired" (they
  are true with no tuner, and existing clients read them that way). Both
  new flags are cached for 30 seconds, since the endpoint is anonymous.

### Changed

- **Tizen app 1.1.0: parity with the Android TV app**, by moving the Samsung
  app onto the webOS app's code (which reached parity in 0.2.0) with
  Samsung's own platform layer. Run on a 2022 Q80B (Tizen 6.5) on 2026-10-05:
  art, H.264 remuxes and transcodes, music and audiobooks play; HEVC needs
  the server's `segment_container: "ts"` (see Fixed), because AVPlay refuses
  the server's fMP4 HLS.
  - Everything webOS 0.2.0 and 0.2.1 list, on Samsung TVs: server sessions
    on the content timeline with re-issues past the produced window,
    subtitles drawn by the app, the Audio / Subtitles / Chapters row, Up
    Next and auto-advance, one `stopped` report per item under the TV's
    name ("Samsung TV — <model> #<tag>"), "play on this TV" from any screen,
    favorites, artist pages, library paging / sort / genre, the saved home
    layout, Trending and Collections rows, the photo slideshow, Back
    returning focus to the card that was opened, and Sign in's Change
    server.
  - The player runs on AVPlay through an adapter that gives it a media
    element's surface; AVPlay's growing-session length is read from the
    HLS playlist, and a stream AVPlay refuses demotes the HEVC claim once
    and restarts as H.264. hls.js is not bundled (400 KB, against 940 KB on
    webOS).
  - The remote: the channel rocker is registered (CH ▲▼ step tracks,
    chapters and channels), Return on Home, Setup or Sign in closes the app
    to Smart Hub as Samsung's checklist asks, and a held key is one press.
  - Tizen 5.5's Chromium 69: the build rewrites Svelte's `:where()`
    selectors (which even the 2022 Q80B's Chromium 85 dropped, so every
    scoped descendant rule had done nothing), writes CSS for Chrome 69, and
    the page shell polyfills `globalThis`, `queueMicrotask` and
    `Object.fromEntries`; a test fails on CSS Chromium 69 drops.
  - config.xml asks for the productinfo privilege (the model name); the
    version is 1.1.0 in config.xml, package.json and Settings > About.
  - The TV's screensaver stays off while video plays and while a photo
    slideshow runs (Samsung's checklist, items 9 and 195); audio, a pause
    and leaving the player let it back. The page shell loads Samsung's
    `webapis.js` on a TV for the switch (`webapis.appcommon`), which the
    platform does not inject the way it injects AVPlay.
  - "Sign in with another device" works: the app polled for the sign-in
    with POST, which the server answers 405 (it routes GET), so a TV showing
    a PIN waited forever however often the PIN was entered. The Roku channel
    had the same bug earlier.
  - A server the TV can't reach, or one that doesn't answer, now says so
    and what to try ("Can't reach your OnScreen server. Check the TV's
    network connection and that the server is running, then try again.")
    instead of the browser's "Network error: Failed to fetch".
  - The player's trickplay thumbnail shows only while a seek is pending.
    It used to show whenever the controls were up, over the play state at
    the start of the bar, on every title with thumbnails (all of the store
    review server's films).
  - Posters no longer all break on a TV signed in more than a day ago. Every
    image URL carries the 24-hour asset token, and nothing renewed it before
    the first screen rendered (the event stream renewed it only after its
    own failed dial, too late for the images already requested). The app
    now renews a stale token, or one of unknown age, before showing
    anything (waiting at most 4 s) and again on return from the
    background. The webOS app had the same bug and gets the same fix (found
    on the Q80B, whose sign-in dated from the old Tizen build).
- **webOS app 0.2.0: parity with the Android TV app**, run on an LG
  OLED77C1PUB (webOS 6, Chromium 79).
  - Server sessions play on the content timeline: an exact start at the
    resume point, a seek outside the produced window re-opens the stream
    there, and audio tracks switch by their 0-based ordinal.
  - Subtitles render (embedded and downloaded tracks, kept in step across
    re-issues); the preferred audio and subtitle languages apply at start;
    an on-screen Audio / Subtitles / Chapters row and a chapter list.
  - Up Next and auto-advance as on Android TV (into the next season or the
    artist's next album; a declined card stays declined), one `stopped`
    report per item with the TV's client name, bounded error recovery with
    an error overlay, and Back returning to the launching screen.
  - "Play on this TV" from any screen through one app-wide event stream,
    which refreshes the sign-in only for an asset token of unknown age or
    near its expiry, never while offline.
  - Browsing: Favorite, "Resume from" + "Play from Beginning", artist
    pages, library paging / sort / genre, Trending and Collections rows,
    the saved home layout, a search library scope, a photo slideshow, and
    Back returning focus to the card that was opened.
  - The remote: a held OK counts as one press; CH ▲▼ skip tracks, step
    chapters and change channels; Up shows the controls.
  - Chromium 79: the build rewrites Svelte's `:where()` selectors and
    lowers `inset`, and layouts no longer use flexbox `gap` or grid on a
    `<button>`, all of which webOS 6 ignored. The capability header reports
    the panel's real size and HDR support (from LG's config service) and
    claims VP9 only when the media stack takes it.
- **webOS app 0.2.1, for the first LG Content Store submission.**
  - `appinfo.json` declares `requiredACG: ["systemconfig.query"]`, the
    group of the app's one Luna call (`com.webos.service.config/getConfigs`,
    the HDR / UHD panel probe). webOS TV 26 enforces the list for an app
    that declares it, and from webOS TV 27 (March 2027) `ares-package` and
    Seller Lounge refuse an app without the field. A test fails when a
    `luna://` or `palm://` URI in the app has no group declared; the
    README's "Luna permissions (ACG)" section says how to look one up.
  - The top nav shows Discover only when the server has requests on (a
    TMDB key) and the account may request (`GET /requests/quota`), and Live
    TV and Recordings only with a configured tuner
    (`features.live_tv_configured`; `live_tv` on a server older than that
    field). They led to screens that only said "requesting is turned off
    for your account" or "No channels configured". The pills appear once
    the server has answered, so nothing flashes up and disappears, and
    stay hidden if the answer can't be loaded. A failed or 10-minute-old
    answer is asked again by the next page, after the hub's Retry, and
    when the app comes back to the front or the network returns (the pills
    stay as they are until the new answer is in); sign-out and Change
    server drop them. Unlike the web app, admins get no
    exception: the TV can't set either feature up. On a server without the
    quota endpoint (v2.4 and earlier, whose `requests` flag was off for a
    TMDB key set in Settings), Discover shows as in 0.2.0.
  - The package icons are LG's sizes, `icon.png` 80x80 and `largeIcon.png`
    130x130 (they were 192 and 512), with the artwork inside LG's padding,
    and the launcher tile colour (`iconColor`) is the icons' black
    background instead of the accent purple: Seller Lounge QA rejects a tile
    that doesn't match the icon. A test checks both.
  - Back is the app's own (`disableBackHistoryAPI: true`). With the
    flag off, webOS answered the remote's Back with `history.back()`, so
    pickers, dialogs and the player's Back never saw the key, and nothing
    offered to leave the app. Each screen's own Back comes first; one that
    nothing takes goes to the previous page, and on a first screen (Home,
    Sign in, Setup) opens an "Exit OnScreen?" popup, Exit
    (`window.close()`) or Cancel, which starts on Cancel so a stray second
    press doesn't end the app. On webOS TV 23 and later, where LG's
    checklist wants Back on the entry page to show the TV's Home screen,
    the app calls `platformBack()` instead; LG's guide doesn't say what
    that does from 23 on, so it is still to be checked in LG's Cloud Test
    Lab. The splash redirects in place (no history entry leads back to
    it), a held Back is one press, and Sign in's steps go back as their
    on-screen buttons do, except while a sign-in is on its way.
  - The Magic Remote: the pointer moves the focus ring to what it is over
    (a card in a row scrolls fully into view, so the pointer works along a
    row past the screen's edge). OK in pointer mode acts once on what the
    cursor is on, whichever of its click and its Enter comes first; over
    empty space it presses nothing but the page's own OK (the player's
    play / pause). A wheel notch is a ↑ / ↓ step, so rows, grids and pages
    move the way the D-pad moves them (a long text with nothing to focus
    scrolls itself). When webOS hides the cursor, the D-pad goes on from
    the ring. In the player, while the pointer is on screen, the controls
    add |◀, -10s, play / pause, +10s and ▶| buttons (previous / next where
    CH ▲▼ step something) and a click on the bar seeks there; the speed
    picker, the online subtitle results, Skip Intro / Credits and the
    error overlay's Try again / Exit take clicks too. The keys do the same
    as before (OK, ← →, CH ▲▼).
  - Subtitles > "Find more online…" shows only when the server has an
    online subtitle search (`features.subtitles_external`), and stays
    hidden while that answer loads or if it fails: elsewhere the search
    only ever answered "not configured". The Subtitles button now needs a
    track or that search (its only row would otherwise be Off).
  - Screensaver type 2 (`screenSaverProperties.preferredType`: dimming
    of the on-screen display, after 30 minutes), so the OLED screensaver no
    longer cuts music, audiobooks or the photo slideshow (only full-screen
    video was exempt). Not type 3, which switches the picture to Gallery
    mode.
  - Settings > About shows the privacy policy
    (`https://onscreen.wolverscreen.com/privacy`) as text and as a QR code,
    as LG asks every app to; the TV has no browser to open it in. The
    policy page now covers the LG app: its storage, the device name and
    playback capabilities it sends the server, and the images it loads
    from where the server points (TMDB posters in Discover, channel logos
    in Live TV and Recordings).
  - Settings > About > Licence & terms: the end-user terms LG's Seller
    Lounge terms (§7.1) ask every app to carry (the app comes from its
    developer, not LG; LG and its affiliates have no liability for it and
    are third-party beneficiaries; it may not be available in every
    country), the app's Apache-2.0 notice, and the notices of the
    open-source code in the bundle with the MIT and Apache licence texts.
    Up / Down read it a block at a time, chips jump to a section, and Back
    returns to the row that opened it. `clients/webos/THIRD_PARTY_NOTICES.md`
    carries the same list; tests check both against the installed
    packages, hls.js's own LICENSE included.
  - `clients/webos` is relicensed under the Apache License 2.0
    (`clients/webos/LICENSE`, `package.json`); the server and every other
    client stay AGPL-3.0. LG's Seller Lounge terms (§16.1(k)) refuse an
    app derived from software that requires its source to be disclosed.
    Contributions under `clients/webos` are Apache-2.0 (README,
    CONTRIBUTING).
  - Sign in no longer traps a wrong but reachable server. Back on its
    username step returns to Setup when Setup opened it this session (the
    exit popup stays for a Sign in the app launched on), and the username
    step and Pair have a Change server button, which forgets the address and
    opens Setup as the home screen's does.
  - The Magic Remote's OK keeps the hold it has with the D-pad. Holding OK
    with the cursor on a Continue Watching card opens its options instead of
    the item, and the click its release sends is dropped. A held OK on the
    on-screen keyboard's delete key goes on deleting. A short press still
    acts once.
  - Cancelling the exit popup while the home screen is still putting focus
    back after a Back no longer lands on the Home pill. The card that restore
    finds behind the popup gets the ring.
  - Skip Intro / Skip Credits takes the pointer's click again. The player's
    full-width action and transport rows covered it; now only their buttons
    take the pointer. The pointer's |◀ and ▶| buttons show only where they
    go somewhere (no previous on the first track, no next in the last
    chapter).
  - Back from Licence & terms keeps its Settings row on screen after the
    preferences and scrobbling rows load in above it. Licence & terms opens
    at its title instead of part way down, where Settings' scroll had left
    it.
  - The third-party notices add the code the hls.js build carries from
    other projects: dash.js's CEA-608 parser (BSD 3-Clause, with its licence
    text), vtt.js and the Common Media Library (Apache-2.0), and
    structured-field-values (MIT). They also add Project Nayuki's QR Code
    generator (MIT), which `src/lib/qr.ts` is ported from; the file's header
    now carries its notice. A test fails on any copyright line in the
    bundled hls.js that the list doesn't carry.
  - The privacy policy covers Last.fm and Trakt next to ListenBrainz: what
    the server sends each linked account, and that the developer receives
    none of it.
  - Styles webOS TV 6 (Chromium 79) dropped or ignored: Discover's cards
    spaced their poster, text and tags with flexbox `gap` (Chrome 84), and
    the focus styles of Settings, Scrobbling, Discover, Search, Live TV,
    Recordings and Sign in sat in rules with `:focus-visible` (Chrome 86),
    which Chromium 79 drops whole. Margins and `:focus` now; a test fails on
    either, or on `:is` / `:where` / `:has` and `aspect-ratio`, in any
    component.
  - The privacy policy has a section on what the LG app uses on the TV:
    the one permission it declares (`systemconfig.query`, to read whether
    the screen shows HDR and whether it is 4K), the model name in its device name, the Magic
    Remote, and what it stores in the web view (all deleted on uninstall).
    It also says the app uses no camera, microphone, location or other TV
    data.
- **TV 1.4.2 (25): Search's microphone shows only where it works.** The
  Amazon Appstore rejected TV 1.4.1 (24) under Performance: the microphone
  button on Search did not respond. Its test Fire TV has no speech
  recognizer for apps (voice there is Alexa's), so the recognizer intent
  failed and the failure was swallowed. The orb now shows only when a
  recognizer resolves (the manifest declares the `RECOGNIZE_SPEECH` intent
  query for Android 11+ package visibility), on Fire TV and Google TV
  alike, so devices where voice search worked keep it. Where it is hidden,
  Leanback's automatic listening on open returns the bar to typing. A
  launch that fails, or a recognizer that returns before anyone could speak
  (an empty result within 1.5 s, or a cancel within 300 ms), shows "Voice
  search isn't available" and removes the orb for the rest of the session
  (silently when it was Leanback's own start on open, not a press). One that
  listened but heard nothing says so. Any result but a query puts back the
  query that was there before, with its results. Verified on a Fire TV Stick
  4K Max (Fire OS 8.1.8.2), which has no recognizer: on 1.4.1 the orb did
  nothing, and on 1.4.2 there is no orb and Search opens on the keyboard.
  Testing there also turned up older Search bugs, now fixed:
  - **The keyboard closed while typing.** The "Search in" row waited for the
    libraries and was then inserted at the top on the first results. That
    moved Leanback's selected row off row 0, so it hid the search bar, the
    keyboard closed after the third letter, focus jumped to the Movies chip
    and the row was drawn under the bar. The row is now built first, when
    Search opens (its picker opens even before the libraries arrive).
  - **One BACK with the keyboard open left Search.** The Fire TV keyboard
    hides itself on the key-down but lets the key through. A BACK that
    closes the keyboard is now consumed, so it only closes the keyboard;
    the next BACK leaves.
  - **On a device with voice, cancelling voice search emptied the field**
    and cleared the results.
  - **Coming back from a title's detail screen put focus on the first card**
    (and now and then on nothing visible), because the query on screen was
    searched again and the rows rebuilt after focus had been restored. A
    query already answered for the same scope isn't searched again, and
    focus returns to the card that was opened.
  - **The MENU key didn't open the "Search in" picker.** Its listener sat on
    the screen's root view, which never has focus.
  - From review: a voice recognizer's own error results (no match, network,
    server, audio) read as a silent cancel, and now say so; an explicit
    Submit searches again even for the query on screen; a focus listener
    that leaked with every Search open is removed for real; and the focus
    restore after a detail screen waits for the results row to come back,
    since the always-present scope and filter rows let it fire too early.

  Both flavors move to versionCode 25 / 1.4.2.
- **Fire TV 1.4.1 (24) has no Live TV, Recordings or online subtitle
  search.** The Amazon Appstore rejected TV 1.4.0 (23) on 2026-10-01 under
  its Deceptive and Malicious Behavior policy, which names apps that "save,
  convert, stream or download media from third-party sources" (the wording
  it also used for 1.1.0–1.1.2). The Fire TV build now leaves out the two
  features that match it: the player's "Find more online…" subtitle entry
  (the server searches OpenSubtitles.com and downloads the chosen file), and
  the Live TV and Recordings cards on Home with the screens behind them
  (channel guide, live channel player, recordings list). Two BuildConfig
  flags, `ONLINE_SUBTITLE_SEARCH` and `LIVE_TV`, are off for the `firetv`
  flavor and on for `googletv`; R8 and the resource shrinker take the
  screens, their strings and icons out of the Fire TV APK. The Google TV
  build is unchanged apart from its version (same R8 keep set, same
  resources). Fire TV users keep both features in the web app, subtitles
  added there still show in the Fire TV picker, and recordings saved to a
  library still play as library items. With no search to come from, the
  Fire TV picker no longer tags attached subtitles "downloaded", and the
  player's Subtitles button shows only when the item has a subtitle track
  (a picker holding just "Off" looked broken). Both flavors move to
  versionCode 24 / 1.4.1.
- **The web app hides what doesn't apply to the signed-in account.** For
  non-admins, the Requests link, the Requests page's tabs, Search's "Ask
  your admin to add" section (its TMDB results, posters and Request
  buttons) and the Request buttons on franchise collections and "Request
  more seasons" appear only when the server has requests on (a TMDB key)
  and the account may request; Upcoming also needs an enabled Radarr or
  Sonarr. Search doesn't ask TMDB at all otherwise. Live TV needs a
  configured tuner. The player's "Search online…" subtitle entry (menu and
  mobile sheet) needs OpenSubtitles set up. Links appear once the server
  has answered, so nothing flashes up and disappears. Admins keep every
  link.
- **Capabilities `requests` and `people_credits` follow the TMDB key in
  Settings.** They read only `TMDB_API_KEY`, so a key entered in Settings,
  the usual way, reported both off while Discover worked. They now follow
  the same key the server uses (Settings, env, or a bundled key).
  `subtitles_external` likewise now needs OpenSubtitles enabled, not only
  a key, which is when search can actually answer.
- **Android TV claims AV1 only with a hardware decoder.** Android 10 and
  later ship a software AV1 decoder on every device, and counting it made
  boxes without AV1 hardware (the Tegra X1 Nvidia Shields among them) get
  AV1 sources and transcodes decoded on the CPU, where 4K stutters. They now
  get H.264 or HEVC. The software decoder no longer makes an 8-bit-only box
  claim 10-bit and HDR either.
- **Android TV claims HDR only for a screen that shows it.** HDR was claimed
  whenever the decoder handles 10-bit HDR, so an HDR film sent to an SDR TV
  (or through a box on one) played washed out. The app now also asks the
  screen (its HDR10 support), and the server tone-maps HDR to SDR for one
  that has none: verified with a 4K HDR film on a 1080p SDR Hisense set.
  The claim follows the screen as it changes (a box moved to another TV).
- **Android TV claims the panel's real resolution.** Many TVs draw their
  menus smaller than the panel and report that as the display size (a 1080p
  Hisense Google TV runs its UI at 1280x720), so the app told the server
  720p and every 1080p file was transcoded down to 720p there. It now reads
  the video output size TVs publish (vendor.display-size), and that file
  direct-plays at 1080p.
- **Android TV sends DTS to a receiver that takes it.** DTS was claimed only
  when the device has a DTS decoder, which Fire TV sticks and Nvidia Shields
  don't, so their DTS audio was converted even with a DTS receiver or
  soundbar attached. It is now also claimed when the HDMI output takes DTS as
  a bitstream at the channel count the app claims (8, so a 7.1 DTS-HD film
  can't play silently on an output limited to fewer), and the claim follows
  the output as it changes (a receiver switched off).
- **Both Android apps: Media3 1.11.1** (from 1.3.1). Amazon recommends 1.5.1
  or later, and with Media3 1.9 a DTS-HD MA or DTS:X track in an MKV reaches
  a receiver in full instead of as its DTS core. Where 1.11 changed a
  behaviour the apps rely on, they keep the old one. System controllers
  (remote and headset keys, Assistant, Bluetooth, watches, cars) keep full
  access to the player. The new stuck-player detectors are off: they would
  end a VBR MP3 whose header under-reports its length a minute past that
  length, and a book or album paused by a phone call longer than 10
  minutes. A paused TV book stays resumable in the background. The phone
  shows no notification for a stopped player, and swiping the app away
  with nothing playing still ends its session (audio that is playing
  carries on with its notification, as before). The TV app's subtitles for HLS streams use
  Media3's legacy decoding, which 1.4 and later need for subtitles loaded
  beside the stream. Upstream's retuned buffering (start after 1 s
  buffered instead of 2.5 s, resume after a stall at 2 s instead of 5 s) is
  undone for server streams in both apps: a remux or transcode only grows as
  fast as the server writes it, and the thinner start flipped between
  buffering and playing after a stall. Downloads, live TV and low-memory TVs
  keep their own values.
- **Android TV on NVIDIA SHIELD.** SHIELD shows HDR10 but sends HLG flagged
  as SDR, and hardware-decodes 8-bit VP9 but not 10-bit. The TV app now
  sends `hlg=1` only for a screen that lists HLG, and never from an NVIDIA
  device, so HLG is tone-mapped to SDR instead of shown dark and green. It
  sends `vp9MaxBitDepth=10` only for a hardware VP9 Profile 2 decoder, so
  10-bit VP9 is transcoded instead of decoded on the CPU. Both keys are
  sent on every device.
- **Android TV sends Dolby TrueHD / Atmos to a receiver that takes it.**
  Android has no TrueHD decoder, so TrueHD was always converted. It is now
  claimed when the HDMI output takes 8-channel TrueHD as a bitstream, and
  the claim follows the output. The server remuxes TrueHD in an MPEG-TS
  file (`.m2ts`) for a client that claims it, converting the audio,
  because Media3 can't read TrueHD out of TS.
- **Android TV's own play decision, used when the server's is unavailable,
  matches its header.** It direct-played AV1 without a hardware AV1
  decoder, and DTS with neither a DTS decoder nor an output that takes it
  (a silent film). It now asks the server to transcode, or remuxes with
  converted audio.
- **Android TV keeps the screen on only for video.** Music and audiobooks
  held the screen on for as long as they played, so the screensaver or
  Ambient Mode never started (Play's TV quality guideline TV-BA). Now the
  screensaver starts as usual and the audio keeps playing, handed to the
  background service as on HOME.
- **webOS requests up to 2160** on transcode start (was hardcoded 1080 —
  which also forced the server to downscale-transcode every 4K source
  instead of direct-playing it).
- **`podcast` library creation is parked** (the manga pattern) until the RSS
  subscription model ships — the scanner behind it is a local-files stub.
  Existing podcast libraries keep working.
- **Watch state has one source.** Migration 00023 adds `watch_progress`, a
  per-user, per-item rollup kept current by a trigger on `watch_events`, plus
  the manual mark; the `user_watch_state` view derives watched / in progress
  / resume point from it for item detail, library grids, Continue Watching,
  Next Up and Plan to Watch. The `watch_state` materialized view is dropped.
  Previously Continue Watching and item detail used different derivations
  and could disagree, and a title finished in a month that retention had
  detached was forgotten. Watched stays set across a rewatch.
- **Seek-bar thumbnails are on by default for video libraries** — migration
  00024 turns the new per-library switch on for existing movie, show, anime,
  cartoons and home-video libraries (new video libraries start on too), so
  the nightly backfill starts working through them; switch it off per
  library to opt out.
- **`GET /api/v1/collections` omits franchise collections** unless called
  with `?include=franchise` (the web client asks for them), so native
  clients see the list they saw before.
- **Migrations 00020–00035** — applied on startup with `AUTO_MIGRATE=true`,
  otherwise by the usual migrate step before starting the new binary
  (`/health/ready` stays unready until they are applied).

### Fixed

- **HEVC titles didn't play on Samsung TVs unless they direct-played.**
  Samsung's AVPlay (tested on a 2022 Q80B, Tizen 6.5) refuses the fMP4 HLS
  ffmpeg writes (`PLAYER_ERROR_NOT_SUPPORTED_FILE`): the same HEVC + 5.1 AAC
  plays as one MP4 file, in MPEG-TS HLS, and in fMP4 HLS only with no `styp`
  box and a single track. The server packages HEVC as fMP4 because browsers
  need it, so every HEVC remux and HEVC re-encode failed on the TV, and the
  app's fallback transcode asked for HEVC again. A transcode start now takes
  `segment_container: "ts"`, which packages HEVC output as MPEG-TS (`.ts`, no
  `#EXT-X-MAP`, no `hvc1` tag) and keeps an Auto (ABR) start on the H.264
  ladder; AV1 stays fMP4, and without the field nothing changes. The Tizen
  app sends it on every start (servers before it ignore it). Also on Tizen
  1.1.0: AVPlay is prepared only once the session's first playlist read
  answers (the server holds it until segment 0 exists, ~30 s for a 4K remux
  on QA, and AVPlay gives up at ~30 s); a remux starts at its beginning
  rather than AVPlay's live edge of the growing playlist (it began ~20 s
  in); and a poster that fails to load shows the no-poster tile instead of
  an empty one, with one retry 15 s later (webOS too). From review, also on
  Tizen: the seek to the start is held only for a session's EVENT / VOD
  playlist, never Live TV's sliding window (it had landed on segments being
  deleted); an AVPlay failure while a source is being set is reported after
  the setter returns, as the element does (it was dropped, leaving "Starting
  playback…" up); the playlist read has a 75 s deadline and is aborted with
  its source; only an explicit "not supported" demotes HEVC, and always HEVC
  (a transient GENEREIC had demoted it for good, and an AV1 source demoted
  "av1", which this TV never claims); "playing" fires on the first tick for
  firmware without buffering events; media, colour and channel keys are
  registered from the remote's supported list and fall back one by one on
  the batch's error callback; chapters and subtitle cues sort stably on
  Chromium 69. Both TV apps: the asset-token renewal on wake makes one try
  per stored token and none offline (a retry with a rotated refresh token
  reads as reuse and signs every device out); Discover keeps only its newest
  search; an options list with two same-named entries no longer throws.
  Found on the Q80B after that: the remote's media, colour and channel keys
  never reached the Tizen app (config.xml lacked the tv.inputdevice
  privilege, so every registration answered SecurityError); and Discover
  opened with nothing focused, so the first press went to the top nav (both
  TV apps now open it on the keyboard, as Search does).
- **A file's default subtitle track now shows on every client.** The Fire TV
  app opened an anime episode with its English dialogue track on (ExoPlayer
  honours the file's default flag); Samsung, LG and the web player opened it
  with subtitles off, because the server never passed the flag on. The
  probe now records ffprobe's `default` disposition on embedded subtitle
  streams (`subtitle_streams[].default`), and with no subtitle-language
  preference the TV apps and the web player show that track (under
  "forced subtitles only", only when it is forced). A set preference still
  wins. Files probed earlier carry the flag after a metadata reprobe.
- **A resumed 4K film's clock jumped ~9 s on Samsung TVs.** Every MPEG-TS
  segment the server wrote started its timestamps at ~10 s (the mpegts
  muxer offsets them by twice `-max_delay`), and AVPlay sometimes reported
  playlist time first and then snapped to the segments' timestamps: the
  clock went 1.4 → 10.4 s, and the position, progress and scrubber with it.
  Sessions that ask for MPEG-TS (`segment_container: "ts"`) now get 0-based
  timestamps (`mpegts_copyts`); nothing changes for anyone else.
- **Audiobook speed on Samsung TVs.** Samsung's webview plays media at 1×
  whatever playback rate is set (measured: `<audio>` and `<video>` alike), so
  the player withdrew its speed control a few seconds into every book, after
  offering it. The TV now remembers this, so later chapters and books open
  with the note "This TV can only play this book at normal speed" and no
  control. Then real speed: a transcode start takes `audio_rate` (0.5-3) for an
  audio-only source and the server speeds the stream up itself (ffmpeg
  `atempo`, pitch kept), echoing the rate it applied. On a TV that ignores
  playbackRate, the Tizen app plays a book at another speed from such a
  session, read back in book time by a wrapper over its media element
  (currentTime, duration, seekable and buffered times the rate, seeks
  divided by it), so position, progress, seeking and chapters stay in book
  time. 1x goes back to the file itself. On a server without `audio_rate`
  the control is withdrawn as before.
- **Movies auto-matched the wrong film although the folder named the title
  and year.** The scanner took TMDB's top search hit, and its year filter
  counts a release in any country, so a popular near-miss won: "Spring
  (2019)" became Spring Breakers (2013), "Hero (2018)" became Chestnut: Hero
  of Central Park (2004). It now searches by first release year (the Fix
  Match query) and accepts only a film whose title, original title or a
  TMDB alternative title is the file's, ignoring case, punctuation and
  accents, released within a year of the file's year. Films that share a
  title and year ("Hero" 2018 is five) are told apart by the file's runtime.
  Anything else is left unmatched for Fix Match rather than guessed. Items
  already matched keep their match.
- **The desktop app couldn't reach a server: every call failed with
  "Failed to fetch".** The installed app calls the server from its own
  webview origin (`http://tauri.localhost` on Windows, `tauri://localhost`
  on macOS and Linux), which the server allowed only when an admin had
  typed it into the CORS list. On a stock server the preflight answered
  405 and no response carried `Access-Control-Allow-Origin`, so the browser
  engine refused them all. The server now allows these origins (and
  `https://tauri.localhost`, for a build with `useHttpsScheme`) without
  configuration, as it already did for the TV apps' `null` / `file://`
  origins: exact matches only (no ports or look-alike hosts), and never
  with `Access-Control-Allow-Credentials` — the desktop app signs in with
  its bearer token, as before. The CSRF checks are unchanged. Until a
  server is updated, add `http://tauri.localhost, tauri://localhost` under
  Settings → General → CORS allowed origins and restart it.
- **Confirmations never stopped anything in the desktop app.** Deleting a
  playlist, collection, photo album, profile, request, tuner, broadcast,
  EPG source or recording schedule, cancelling a recording, removing a
  node's saved config, removing an item from the library and marking a
  whole show unwatched all ask first. In the desktop webview
  `window.confirm` returned straight away: the dialog plugin replaces it
  with an asynchronous call (which the webview isn't even allowed to
  make), so each of these went ahead without asking. They now ask through
  a native OK/Cancel dialog that is modal to the app window, and Cancel
  stops the action. Browsers still show their own confirmation.
- **Some errors were silent in the desktop app.** A failure to remove an
  item from the library, refresh its metadata or change its watch status
  was reported with the browser's alert box, which the dialog plugin also
  replaces in the desktop webview, so nothing appeared. These failures now
  show as an error toast, like the app's other errors, in browsers too.
- **The Upcoming calendar showed restricted users titles from libraries
  they can't see.** An entry was hidden only when its Radarr/Sonarr folder
  mapped into a library the user has no grant on; a folder no library
  claims (most often because `arr_path_mappings` was unset and the *arr
  root never lined up with the scan paths) was shown to everyone, so a
  restricted account saw the whole calendar. For non-admins it now fails
  closed: an entry appears only when its folder, after the mappings, lies
  in a library they can see. Admins still see everything.
- **Discover ignored "Can request".** Switching an account's requests off
  only disabled the Request buttons; `GET /api/v1/discover/search` and the
  season list still returned the TMDB catalogue. Both now answer `403
  REQUESTS_DISABLED` for such an account (read from the account itself,
  like `POST /requests`), before TMDB is asked.
- **Discover answered 500 without a TMDB key,** which the web Search page
  showed as a red error. It now answers the documented `503
  TMDB_UNAVAILABLE`.
- **Online subtitle search answered "HTTP 503" without OpenSubtitles.**
  The 503 had no readable message; search and download now answer `503
  SUBTITLES_NOT_CONFIGURED` with one, in the standard error format.
- **Search found nothing for restricted users on common words.** The
  cross-library search took the top results across every library and only
  then dropped the ones the user can't see, so a word like "night" could
  leave nothing. The user's libraries are now part of the query. The home
  page's mixed Recently Added row had the same flaw (an empty row for a
  restricted user when other libraries had newer items) and the same fix.
- **People search listed everyone.** `GET /api/v1/people?q=` returned the
  cast and crew of every library, names and photos included. A non-admin
  now only finds people credited on something they could open (a library
  they can see, within their rating ceiling).
- **"View as" a user still opened admin-only pages.** Under `?view_as=`,
  admin-only routes checked the real admin's session again instead of the
  viewed user's identity, so an admin viewing as a regular user saw what
  the admin can reach. They now refuse as they would for that user.
- **Two open browser tabs signed the user out everywhere.** Every web tab
  kept its own copy of the tokens in memory after signing in or refreshing,
  and sent that copy (the server prefers it over the shared cookie). Once
  another tab had rotated the refresh token, the next refresh from the first
  tab presented a retired token, which the server treats as theft: it
  deleted every session of the user, so the web, the phone and the TVs were
  all signed out, roughly once an hour with two tabs open. Browser tabs now
  use the cookie alone (the desktop app and cross-origin setups keep their
  tokens), and a refresh runs in one tab at a time across the browser (Web
  Locks, or a best-effort localStorage lease where Web Locks are unavailable,
  e.g. plain http on a LAN address); a tab that waited while another
  refreshed reuses that result. Sign-out takes the same lock, and the first
  refresh after an OIDC/SAML sign-in goes through it too.
- **A server hiccup no longer signs clients out.** A refresh the server could
  not check (a database error, a restart) was answered 401, which the TV and
  phone apps take as "signed out" (the phone also deletes its downloads).
  `POST /api/v1/auth/refresh` now answers 503 and keeps the cookies in that
  case; only an unknown, expired, reused or deleted-user token is a 401. A
  rotation that committed although the database connection then dropped
  returns the new tokens instead of a 503 whose retry would read as reuse.
  The web app treats a 5xx or a network failure during refresh as a failed
  request, not a sign-out, and no longer clears the other tabs' sign-in
  state when one tab's refresh fails.
- **Every "signed out on all devices" is now in the audit log.** Refresh-token
  reuse and an identity-provider admin-role change both delete all of a
  user's sessions, but only logged to the server log. They now write an
  `auth.sessions_revoked` entry with the user, the reason
  (`refresh_token_reuse` with how it was detected, or `idp_admin_sync` with
  the provider), whether the wipe actually succeeded, the client's IP and
  User-Agent, and (for reuse) how long after the session's last rotation the
  retired token came back, which separates a lost response from theft.
- **Multi-disc albums an older scan folded now split on the next scan.** A
  scanner that matched tracks by number alone hung disc 2's track 1 on disc
  1's (The Beatles' "The Beatles" came out as 17 tracks, Piggies playing
  Revolution 9). New scans were already right, but an unchanged file skips
  the tag read, so old folds never split. A music scan now re-reads the
  files of any track holding several, once per server start, moves each to
  its own disc and track, and gives the disc 1 row back its own title. No
  migration: run a music library scan after upgrading.
- **Multi-disc sets named "112-artist-title" place each disc.** Some
  releases put the disc in front of the track number with nothing between
  ("112-…" is disc 1 track 12, "212-…" disc 2 track 12) and carry no disc
  tag. The scanner read that as track 112, or went by the track tag alone,
  so every disc's track 12 was one track. A file with no disc tag whose name
  starts with three digits ending in its tagged track number now gets the
  first digit as its disc, when its folder holds the set named the same
  way: most of that disc's earlier tracks and another disc's first track. A
  real track 112, a title that is a number, a band named with one (311,
  808 State) or a countdown folder numbered straight through doesn't, even
  beside such a set. Albums an older scan folded
  this way split on the next music scan, and a later disc's tracks that no
  other disc has move to their disc without being re-read.
- **Albums with titles in another script no longer merge.** Title matching
  dropped everything but Latin letters, so every Japanese, Cyrillic or Greek
  title matched every other: a Japanese album came in as one track, and an
  artist's non-Latin albums as one album.
- **A scan no longer blanks metadata when it records a length, a cover or a
  photo's date.** The scanner's update of a track's changed length, of an
  album's, artist's, author's, book's or home video's cover, and of a
  photo's capture date went through the full metadata write, which cleared
  the year, genres, summary, tags and artist (a book's author) it wasn't
  given. They now touch only their own column.
- **Phone: a book or album page no longer flashes "No playable files".**
  Coming back to a page from the player reloaded it from scratch, and a book
  made of chapter files looked unplayable until its chapters loaded. The
  page now refreshes in place, and Play shows a small spinner until it
  knows where to start. Multi-disc albums get "Disc 1 / Disc 2" headings.
- **Phone: the full player opens at once over audio already playing.** It
  waited on five network calls in a row (several seconds on a slow link,
  with taps lost) for an item the background service was already playing;
  it now shows the controls straight away and loads the rest alongside,
  including the parental watch limit, which still blocks a paused book
  reopened past it.
- **Phone: a headset or Bluetooth key during a video controls the video.**
  The app's only media session was the audio service's, so a play key
  during a video restarted a finished or paused audiobook underneath it,
  paused the video, and reset the book's resume point. Video now has its
  own session, which starts playback only while its screen is up. The
  audio's notification buttons always work, and pause and stop still reach
  the audio while a video is up.
- **Phone: the audio player shows the cover and what's playing.** It showed
  only art embedded in the file, which most libraries don't have, so the
  page was black with the controls in the middle. It now shows the album's
  or book's cover (a podcast episode: its show's), the title, and the
  artist or author, above the controls (beside them in landscape), and the
  next track's cover appears at once when the queue moves on.
- **Phone: the player's controls show when it opens over audio already
  playing.** Opened from the mini player or the notification, it showed a
  black page with only the top bar, and the first tap hid even that. Audio
  now keeps its controls up; video shows them for a few seconds as it
  starts, then hides them with the top bar.
- **Android TV background audio, from a hardware test run.** Found on a
  Fire TV Stick while testing the session hand-off:
  - Reopening an item while its own player was on screen (a deep link, a
    Watch Next tile) could crash the app ten seconds later. The background
    service was started and stopped within milliseconds, and Media3 then
    restarted it in a mode that must reach the foreground. The service now
    starts a moment after the hand-off, only if the player is still parked,
    and a start with nothing to play promotes itself briefly and stops.
  - After HOME, a player the background service had already released could
    be parked again, so replaying the track landed on Home with nothing
    playing. The screen now drops the released player, and the service only
    releases a player that is still its own.
  - After the last track or chapter ended, the service kept reporting it as
    playing every 10 s. It now stops when nothing follows, and only reports
    "playing" while audio is actually playing, not while a session is still
    buffering its first segment.
  - A paused background player was dropped by Android about a minute later,
    so it could not be resumed from the remote. It now stays resumable for
    10 minutes, and the pause is reported so the server's resume point is
    exact.
  - The now-playing session names the title, artist and album, and shows
    positions in the track's own time for a resumed server session.
  - Handing audio to the background no longer rebuilds the audio output (a
    brief stall), and no longer leaves a gap in the heartbeats.
  - A final position is sent when a parked player is replaced, a skip to
    the next track no longer reports the old one stopped twice, and a
    player's teardown no longer sends the same report two or three times.
  - A background server stream that fails (an admin stop, a refused
    session) is let go at once instead of lingering, and a track that ends
    just as it is handed over still chains to the next one.
  - Fire TV: the app no longer tries to publish Watch Next rows without the
    permission (the Fire TV build leaves it out on purpose).
  - After HOME, the player screen kept following the background player: it
    seeked it whenever the service's own progress report came back from the
    server as a cross-device sync event. A paused track re-buffered, and at
    the end of a server-streamed track the seek hit the closed stream, so
    the music stopped instead of moving on to the next track. The screen now
    lets go of the player until it takes it back.
  - An admin stop reaches audio playing in the background after BACK or
    HOME: the background service now watches for it. Before, a paused track
    or a server-transcoded one carried on after the stop.
  - A Watch Next link opened while the app was in the background landed on
    Home instead of the player: on Fire TV (Android 11) the link arrives
    after the app has started its return-to-Home, which then replaced the
    player.
  - The now-playing session's summary (what Fire TV and Google TV read)
    shows the artist and album under the title, not just the title.
  - The paused and stopped reports sent as a video closes reach the server
    in order; a pause landing second put the video back in Now Playing.
- **An audiobook's "00 - Prologue" played last.** A chapter file whose name
  starts with 0 was left unnumbered, and unnumbered chapters sort after
  numbered ones. A leading 0 now counts as the place before chapter 1.
  Books numbered under the old rule (where the prologue could take chapter
  1's number and push chapter 1 to the end) are cleared by migration 00034
  and renumbered on the next scan.
- **Android TV: background audio from a server transcode stopped.** A
  track, book or chapter the TV can't decode itself (DSD, some ALAC) plays
  from a server session. Pressing BACK or HOME hands the player to the
  background service, but the player screen still ended that session as it
  closed, so the audio stopped seconds later. The session now moves with
  the player and ends when the player is released. Reopening the item no
  longer starts a second session behind the one already playing. When a
  track ends in the background, the next one now follows the server's
  play decision, so a transcoded album keeps playing past the first track
  instead of the next track failing.
- **Photo album covers showed photos the viewer could no longer open.**
  The album list's cover and photo count ignored deleted photos, the
  rating ceiling and library access, so a profile whose ceiling was
  lowered (or whose library access was revoked) after filling an album
  still got those photos as its cover. They now count what the album page
  shows. The album page also filters library access in the query, so a
  page is no longer short and `total` agrees with it.
- **Request notifications were never delivered.** Since requests shipped,
  every `request_*` notification failed the `notifications.type` CHECK
  constraint (it allowed three types) and the failure was only logged, so
  requesters never heard back. Migration 00021 replaces the fixed list with
  a format check.
- **Seek-bar thumbnails never generated in the Docker image.** The sprite
  directory was derived one level above the writable `CACHE_PATH` volume
  (the root-owned `/var/cache`), so generation failed with "permission
  denied"; it now lives under `CACHE_PATH` (an existing, usable legacy
  directory is kept). ffmpeg 8's mjpeg encoder also refused limited-range
  video, so sprites are converted to full range before encoding.
- **Seek previews on Tizen and webOS** — those clients fetch
  `/api/v1/items/{id}/trickplay/…`, which didn't exist. The server now
  serves that path with the same auth, library access and rating ceiling as
  `/trickplay/{id}/{file}`.
- **Track order for "N/M"-tagged FLAC/Ogg/Opus** — Vorbis `TRACKNUMBER` /
  `DISCNUMBER` values like `02/12` parsed as 0, leaving albums unordered.
  They're parsed now, and a tagged file with no usable number takes it
  from its `NN - Title` filename. Tracks already in a library get their
  number on the next scan without being re-imported: the path supplies it
  when it can (`07 - Title`, `2-07 Title`, a `CD2/` folder), otherwise the
  file's tags are read once — no re-hash or ffprobe, and a file with no
  number anywhere isn't re-read on every scan.
- **Multi-disc albums lost tracks.** A track's position was its number
  alone, so disc 2 track 1 was taken for disc 1 track 1 and its file filed
  under that track. Tracks now store their disc (migration 00030 adds
  `media_items.disc_number` and makes the one-track-per-position index
  per disc); albums list in disc-then-track order and the children API
  returns `disc_number`. An album already folded this way separates when
  the affected files are next re-imported. The web album page heads each
  disc ("Disc 2") on an album with more than one, so its restarted track
  numbers read right.
- **A scan merged an author's audiobooks into one.** The post-scan
  dedupe compared books by `original_title`, where the scanner keeps the
  author, so every book by one author without distinct years counted as a
  duplicate of the others. The scan kept one, soft-deleted the rest, and
  moved their files onto it. Books are now compared by title, both under
  one author and across authors; albums are unchanged. **Existing damage
  is repaired automatically** by the first full scan of each audiobook
  library after upgrading (migration 00033). That scan brings back each
  book the old dedupe merged away, along with its chapters, and re-imports
  that author's files so each one goes back under its own book. It runs
  only on books merged before the upgrade, so books a user deleted, or real
  same-title duplicates, stay as they are.
- **Multi-file audiobooks had no chapter order.** The scanner never
  numbered a book's chapter files, so lists and queues took whatever order
  the rows sat in, which a later update could shuffle. A chapter now gets
  its number from the file name's leading number ("01 Title"), else its
  track tag. The number doesn't affect matching, so a bad tag can't merge
  chapters. Chapters already in a library are numbered on the next scan
  without re-importing, and anything still unnumbered sorts by title.
- **Native clients played multi-disc albums out of order.** Android TV,
  the phone app, Tizen, webOS and Roku ordered and advanced tracks by
  number alone, so the discs interleaved: disc 2 track 5 went on to disc 1
  track 6, and the end of disc 1 skipped disc 2 or stopped. They now read
  `disc_number` and use disc-then-track order for auto-advance,
  Previous / Next, an album's Play and an artist's Play All. The phone's
  next-item pick also steps over a missing number instead of leaving the
  season or album, as the TV app's already did.
- **Web home tiles wider than their posters** when a title or Next Up
  subtitle was long.
- **Android TV / Fire TV playback fixes from the 1.4.0 device test.**
  - Waking the TV from the screensaver returns to the screen you were on:
    music comes back on its now-playing screen, still playing, and a paused
    video reopens paused where it was (after an episode has ended, Home as
    before). This follows from 1.4.0 letting the screensaver start during
    music.
  - The sign-in code screen keeps the screen on while it waits (up to 30
    minutes), and the code survives HOME and the screensaver; the Hisense's
    5-minute screensaver used to throw it away.
  - Server streams (remux / transcode) start where they should: at 0:00 from
    the beginning, at the exact resume point (not the keyframe before it or
    several seconds past it), and in place on a pre-encoded ladder; the
    transport bar's scrubs on a resumed stream land where you stop (Back
    cancels one without moving playback), Play after the end and the remote's
    Previous restart from 0:00, Play (on screen or on the remote) after a
    playback error picks up where it stopped, and the system media controls
    no longer offer a Next that jumped inside the same stream.
  - Watched percentage and Up Next go by the file's real length, never by a
    stream the server is still writing: a resumed remux no longer shows Up
    Next mid-film, and the end-of-episode card counts down on its own.
  - Progress keeps reaching the server during a stall (no false 'paused'),
    and the final pause/stop reports can no longer be overtaken by an older
    one.
  - Subtitles: slow first extractions of a large file are retried (with
    "Subtitles unavailable" if they still fail), downloaded subtitles load
    (they were sent with a token the server refuses), cues after a resume no
    longer carry stray lines, and the picker names tracks as "Language ·
    title".
  - The transport controls fade again a few seconds after the last key
    press (Leanback's tickle timeout defaulted to 0, so any D-pad press kept
    them up for good), video and music alike, and OK brings them back even
    on a TV where nothing had focus (the Hisense). Music and audiobooks show
    the album or book cover (it was drawn under the video surface, so the
    screen stayed black) and no subtitles button; an audio item that really
    carries video (an illustrated audiobook) shows its picture and keeps the
    screen on.
  - Back after Up Next or a queue moved on by itself leaves playback; it
    used to reopen and replay the first item (and, for an episode, its
    credits and Up Next again). A finished or declined Up Next, or a
    dismissed playback error, leaves the player when it was opened from a
    Watch Next tile too.
- **Phone playback fixes from the 0.3.0 device test.**
  - Swiping the app away from Recents with the full player open crashed it.
  - Subtitles now work on remuxed and transcoded video (they are loaded
    beside the stream, as on the TV), stay in sync after a resume, and are
    retried while the server extracts them from a large file. The picker
    shows the track that is on, can pick one of several tracks in the same
    language, lists "Find more online…" downloads at once, and its dialogs
    scroll in landscape.
  - Resuming a remuxed or transcoded movie no longer marks it Watched and
    clears its resume point after a few minutes (the app took the growing
    stream's length for the film's), and progress from Play on a show or
    season is saved on the episode that plays.
  - Resume starts at the exact point, switching audio track no longer ends
    or jumps the stream, Up Next goes by the file's length, and Next is
    disabled on a single server stream (it jumped inside the same video).
  - Picking an audio track on a remuxed or transcoded stream plays that
    track: the app sent the file's stream number where the server expects
    the audio track's, so picks were off by one and the last one failed.
    The picker marks the track playing, and a failed start shows the
    server's own message with a Close button instead of a bare "HTTP 422".
- **A file that is slow to open is no longer called corrupt.** The
  transcode-start pre-flight gives ffprobe 5 s, and running out of time was
  answered 422 SOURCE_UNREADABLE ("appears to be corrupt"): on QA a 1080p
  remux in an MP4 (whose index sits at the end of the file) never played,
  and a WEB-DL MKV failed once while its disk spun up. A pre-flight that
  times out now lets the transcode start (ffmpeg opens the file itself and a
  broken one still fails); missing, unsafe and genuinely unreadable files are
  refused as before.
- **A remux plays the audio track that was picked.** Whether audio could be
  copied untouched was decided from the file's first track, not the selected
  one: picking a DTS track behind an AC3 first track passed DTS through to a
  client that can't decode it (silence). An `audio_stream_index` past the
  file's last audio track now gets a 400 instead of an ffmpeg that never
  writes a playlist. (Tizen, webOS and Roku still send the file's stream
  number there; their last-track pick now gets that 400.)
- **A progress report without a duration no longer erases the stored one.**
  The watch rollup copied the latest report's duration over the stored one,
  so a player that couldn't tell a title's length made it look unwatched and
  dropped it from Continue Watching. Migration 00035 keeps the stored
  duration in that case (a report without one can never mark a title
  watched), and the server fills a missing duration from the version being
  played (`file_id`, else the live session's file). It advertises this as
  `features.progress_without_duration` in `/api/v1/system/capabilities`,
  only once 00035 is applied (re-checked every minute); the phone leaves the
  duration out only on servers that say so.
- **The privacy page names the third parties a client can reach.** It said
  no library in the clients sends data off the device, but the web player
  loads Google's Cast sender script, the phone app's Cast framework reports
  diagnostics to Google, and the photo map loads its tiles from
  OpenStreetMap. A new "Third-party services" section lists them, with links
  to their privacy policies (the TV app has no Cast).
- **The phone's photo map follows OpenStreetMap's tile policy.** It showed
  no attribution and identified itself to the tile servers only by package
  name. It now shows "© OpenStreetMap contributors", linked to OSM's
  copyright page, and sends "OnScreen/<version>" with the project's URL.
- **Android CI failed at SDK setup since 2026-09-25.** The setup action's
  default package list starts with the retired `tools` package, which the
  SDK manager no longer serves; the phone and TV workflows now install only
  `platform-tools` and let the Android Gradle plugin fetch the rest.

## [v2.4.0] — 2026-08-05

The server lock was lifted after v2.3.0 (see [docs/server-lock.md](docs/server-lock.md)).
v2.4 adds new auth surface and a multi-node transcode fleet alongside an
on-demand adaptive-bitrate pipeline; first-party clients move in lockstep for
the asset-token migration — **update clients alongside the server**: store
builds shipped since June 2026 already handle the asset token.

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
- **Opt-in file integrity probe** — a new `integrity_probe` scheduled task
  (seeded disabled; enable in **Settings → Tasks**) spot-decodes ~15 frames at
  the 10/50/90% offsets of each video file with the software decoder and marks
  `media_files.integrity_status` ok/damaged. Damaged/fake releases whose
  headers parse but whose bitstream doesn't (the kind that green-frames
  hardware decoders and stalls browsers) get a **"file may be damaged"** badge
  on the item page, a `damaged` playback-decision verdict, and a
  422 `FILE_DAMAGED` refusal on transcode start — instead of users retrying an
  unplayable title. Verdicts reset automatically when a file's content changes
  on disk; probe failures (unreachable share, missing ffmpeg) leave files
  unchecked rather than branding them.

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
