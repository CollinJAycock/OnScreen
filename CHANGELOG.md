# Changelog

OnScreen tracks server + first-party-client changes. Versions follow
[semantic versioning](https://semver.org/) — minor releases add
endpoints / features without breaking existing API contracts; major
releases reserve the right to break things.

The v2.2.0 server lock was lifted after v2.3.0. Since v2.4, a breaking
API change lands only when every first-party client moves with it — see
[docs/server-lock.md](docs/server-lock.md).

## [Unreleased]

### Fixed

- **"Play on…" reaches a Roku whichever API instance it lands on.** A
  transfer waiting for the Roku's claim was kept in the memory of the API
  instance that received it, so with several instances behind a load
  balancer the claim could reach one that held nothing (a known limitation
  in the v3.0.0 upgrade notes). Waiting transfers are now shared through
  Valkey and a claim takes one with `GETDEL`, so it still plays once. While
  Valkey is unreachable, each instance hands out the transfers it recorded
  itself.

### Build and CI

- **Node 26 (the LTS line since 2026-10-28) in CI.** Every `setup-node`
  step moves from 24 to 26, matching the Docker images (`node:26-alpine`),
  and the build-from-source prerequisites now say Node.js 26+. The last
  three actions that ran on Node 20 move to their Node 24 releases:
  `docker/metadata-action` 6.2.0, `actions/setup-java` 6.2.0 and
  `android-actions/setup-android` 4.0.4. The web, webOS, Tizen, Xbox and
  Roku tests pass on Node 26 without `--no-experimental-webstorage`
  (#307).

## [v3.0.0] — 2026-11-24

v3.0, the first release of the v3 line (see
[docs/v3-plan.md](docs/v3-plan.md)): the apps and the server's own text in
Spanish and Simplified Chinese, Dolby Vision profile 5 tone-mapped to SDR,
Dolby audio encoded for AV receivers, search that forgives partial words,
accents and typos, with search inside a library and an A–Z rail, and
profile 7 → 8.1 conversion in the standard Docker image. It removes the
`/api/v1` routes and fields that no OnScreen app uses, which is why the
major version changes (see "Removed from the API"); no app needs an update
for that. It also carries a review of the whole server and of every client,
and the server side of the new Roku channel, Meta Quest app, Kodi add-on and
Android Auto.

### Upgrade notes

- **Migrations 00062 to 00065** run at boot with `AUTO_MIGRATE=true` (or
  run the migrate step first). 00062 stores a folded copy of every title
  for search (`media_items.title_fold`, with a trigram index); adding the
  column rewrites every `media_items` row under a table lock, so leave free
  disk for a second copy of the table and its indexes. 00063 adds
  `users.ui_language`, 00064 builds several indexes on `media_items` and
  backfills `photo_taken_at`, and 00065 adds `task_runs.heartbeat_at`. On
  500k items 00062 took about 15 s and 00064 about 2 s; the others take
  milliseconds. Rehearsed from v2.7.0 with data: schema 61 → 65, sign-ins
  kept, nothing lost.
- **Docker tags:** the image is published as `:3.0.0`, `:3.0` and `:3`. An
  install pinned to `:2` or `:2.7` stays on v2.7 until the tag changes.
- **API removals** (see "Removed from the API"): no OnScreen app uses them.
  A removed route answers 404, a removed field is no longer sent or read,
  and `PATCH /libraries/{id}` with `scan_interval_ns` or
  `metadata_refresh_interval_ns` is now 400.
- **Error messages follow `Accept-Language`.** An API error's `message` is
  in Spanish or Chinese when the request asks for one; its `code` never
  changes, so scripts should branch on that. A request without the header
  gets English.
- **Upgrade remote transcode workers with the server.** Only a v3 worker
  (the embedded one is) runs the whole-file (VOD) remux that Roku 1.2 asks
  for. With v2.7 workers alone, Roku gets the growing playlist it had
  before, labelled "Rewind live TV". A v2.7 worker also keeps AAC where a
  v3 worker encodes E-AC-3 for a receiver.
- **SAML:** an IdP set to the artifact binding (`SAMLart`) can't sign
  anyone in now. Switch it to HTTP-POST, the default for Authentik,
  Keycloak, Entra ID, Okta and Google.
- **PIN switching:** the server-wide switch is gone. A server that had it
  off (possible only through the API) now allows PIN switching, which only
  ever reaches the user's own household.
- **"Play on…" to a Roku** waits in the memory of the API instance that
  received it. With several API instances behind a load balancer, the
  Roku's claim can reach one that holds nothing, and the transfer is lost.
- **Status codes:** a rating or label denial on children, `/media/stream`,
  `/media/download`, subtitles, converted downloads, optimized versions,
  static ABR and music seeds is now 404 (was 403), and anonymous calls to
  those routes 401. `GET /items/{id}`, transcode Start and the playback
  decision keep 403, which the Android apps show as "outside your content
  rating limit". The playback decision can now answer 403
  `PARENTAL_LIMIT` / `PLAYBACK_STOPPED`. Clients that fall back to their
  own decision still get the same refusal from Start.
- Changing an unknown user's role is 404 (was 204); a taken profile name is
  409 (was 500).
- **Valkey:** new `transcode:user_sessions:<uid>` sets and a
  `notification:events` pub/sub channel. During a rolling upgrade, older
  instances neither maintain the index nor relay notifications.
- Docker compose files pass `TZ` (default `UTC`) to the server; set it, or
  Settings ▸ System ▸ Time zone, for parental hours in local time.
- `onscreen_hub_cache_refresh_duration_seconds` and the
  `OnScreenHubRefreshSlow` alert are removed with the view they measured.
- **Kodi:** add-on 0.2.1 refuses a saved `http://` address for a server
  that isn't on the local network (nothing is sent); sign in again with
  https.
- **Desktop:** DSD goes through the native engine only when it is turned
  on (Audio (native engine) ▸ Native engine, off by default); DSD over DoP
  needs exclusive mode and the volume at 100 %. In a debug build
  (`tauri dev`) DSD256 can't keep up in real time; release builds use about
  1–4 % of a core.

### Removed from the API

v3 removes from `/api/v1` what no OnScreen app calls or reads (see
[docs/v3-api-audit.md](docs/v3-api-audit.md)). No app changes; a script that
uses one must move.

- **Routes (now 404):** `DELETE /api/v1/optimize/versions/{id}` (use
  `DELETE /api/v1/optimize/jobs/{id}`: same handler, same ids);
  `GET /api/v1/photos/search` (EXIF search);
  `POST /api/v1/maintenance/refresh-missing-art` (the scheduled
  `refresh_missing_art` task stays);
  `POST /api/v1/maintenance/dedupe-shows` and
  `POST /api/v1/maintenance/dedupe-movies` (full scans repair duplicates
  since migration 00033); `POST /api/v1/maintenance/purge-deleted-library`
  (deleting a library still purges it);
  `POST /api/v1/admin/items/re-enrich-unmatched` (the Unmatched page's
  per-item Fix Match stays). `POST /maintenance/reprobe-metadata` and
  `GET /admin/logs` stay.
- **Library intervals:** `scan_interval_ns` and
  `metadata_refresh_interval_ns` in `POST /api/v1/libraries` (ignored now:
  new libraries scan daily and refresh metadata weekly) and in
  `PATCH /api/v1/libraries/{id}` (now 400, since it refuses unknown fields;
  use `scan_interval_minutes`). The metadata refresh interval can no longer
  be set through the API.
- **Transcode Start** (`POST /api/v1/items/{id}/transcode`): the
  `max_audio_channels` request field (ignored; the cap comes only from
  `maxAudioChannels` in `X-Client-Capabilities`) and the
  `burned_subtitle_stream_index` response field.
- `last_client_name` from `GET /api/v1/items/{id}` (still in the user
  export); `owner_id` and `owner_username` from `GET /api/v1/profiles`
  (admin listing) and `owner_id` from the `PATCH /api/v1/profiles/{id}`
  response; `features.abr_ladder` from `GET /api/v1/system/capabilities`.
- `video_profile` and `video_level` from the files in
  `GET /api/v1/items/{id}` (added in v2.7). The server still probes and
  uses them to match files against a client's declared profile and level;
  no app read them.
- **The server-wide PIN-switch toggle:** `pin_switch_enabled` is gone from
  `GET` / `PATCH /api/v1/settings` (a sent value is ignored), and
  `POST /api/v1/auth/pin-switch` no longer answers 403
  `PIN_SWITCH_DISABLED`. It had no UI, and PIN switching only reaches the
  caller's own household, so it never kept one account from another.

### Added

- **The apps and the server's own text come in Spanish and Simplified
  Chinese.** Every app has an interface-language setting: English,
  Español or 简体中文 (Roku: English and Español). The Spanish and Chinese
  are machine translations that no fluent speaker has reviewed yet, so
  expect wording that is stiff or wrong; corrections are welcome (see
  "Correcting a translation" in docs/i18n.md). English wording is unchanged
  apart from singulars ("1 active stream") and thousands separators in
  counts.
  - **Which language:** the account's choice, else this device's, else the
    device's or browser's own languages, else English. The web app's picker
    on the sign-in screen sets it for the device; the one in **Account ›
    Languages**, and **Settings › Language** in the TV, Android and Roku
    apps, also save it to the account (`ui_language`, migration 00063), so
    the user's other apps follow. Against a server before v3 the choice
    stays on that device. "Device language" clears it. Traditional Chinese
    (`zh-TW`, `zh-HK`, `zh-Hant`) isn't shipped: the next language the
    device lists decides. New endpoint `PUT /api/v1/users/me/ui-language`
    (`{"ui_language": "es"}`; `null` or `""` clears it);
    `GET /users/me/preferences` returns `ui_language`.
  - **Web and desktop app:** every screen, Settings and the player
    included.
  - **LG, Samsung and Xbox:** every screen, and so Steam Deck TV mode. The
    Xbox app and TV mode show the server's `/tvapp/`, so they change with
    the server; LG and Samsung change with their next app release.
  - **Android TV / Fire TV and the phone app (also Quest):** every screen.
    On Android 13 and later the choice also shows in the system's per-app
    language settings.
  - **Roku:** Spanish only. Roku has no Chinese system font and a channel
    can't carry one within its 4 MB limit, so an account set to Chinese
    sees English there.
  - **Kodi add-on:** Spanish and Chinese, following Kodi's own interface
    language rather than the account's.
  - **Server:** the errors a person can see, emails (password reset,
    invite, SMTP test, the digest), in-app notifications and the Now
    Playing transcode reasons. An API error's `message` follows the
    request's `Accept-Language` (every app sends the language it shows; no
    header gets English) and carries `Vary: Accept-Language`; `code` never
    changes. Emails and notifications use the recipient's `ui_language`,
    else the language of the request that caused them. A non-ASCII email
    subject is now RFC 2047-encoded and an HTML-only body sent
    quoted-printable (both went out as raw UTF-8).
  - **Kept in English:** the privacy policy and the TV apps' licence text
    (legal text gets a reviewed translation or none), errors only a
    developer meets ("invalid item id"), logs and audit names, the Discord,
    Slack, Gotify, ntfy and agent-email notifications, the Xbox app's
    Diagnostics screen and its first server-address screen, the desktop
    app's tray menu and system dialogs, the photo map's marker tooltips,
    and episode codes ("S2 · E5"). Library data (titles, descriptions,
    genres) is shown as the server has it.
  - **Not checked yet:** that the Xbox's system fonts draw Chinese, that
    "Device language" on the LG, Samsung and Xbox apps follows the TV's
    menu language, and the Quest's player and Chinese text.
- **Dolby Vision profile 5 tone-mapped to SDR.** A profile 5 file (no
  HDR10 base layer) used to be refused on any screen without Dolby Vision.
  Now a worker running OnScreen's GPU image tone-maps it to SDR, applying
  each frame's Dolby Vision metadata (`tonemap_cuda`'s `apply_dovi`, from a
  software decode). Workers advertise it as `dolby_vision_p5_tonemap`, and
  the play decision is `transcode` where one exists and `unsupported` where
  none does. The web, Android TV, phone, Samsung, Xbox and Roku apps now
  follow the server's verdict, refusing profile 5 on their own only when the
  decision call fails. The LG app already did. Checked against Dolby's
  profile 5 and 8.1 samples of the same film. libplacebo isn't used for it
  yet (unchecked on a real Vulkan GPU). See `docs/dolby-vision.md`.
- **Search within a library, and an A–Z jump rail.** Library pages get
  "Search this library" (`GET /api/v1/search?library_id=`; on the web it
  replaces the "Filter…" box, which filtered only loaded rows) and, on
  title-sorted listings, an A–Z rail that loads only the page a letter
  starts on. New `GET /api/v1/libraries/{id}/items/letters` takes the
  filters of `/items` and returns, per first letter of the sort title
  (accents removed, `#` for anything but A–Z), its `count` and the
  `offset` of its first row; any sort but `title` returns an empty list.
  Clients hide the rail on a server without the route (404). Web, Android
  TV / Fire TV, phone / Quest, LG, Samsung and Xbox (#181, #190).
- **A remux can be one complete VOD playlist, for clients that ask.** A
  remux is a growing EVENT playlist, which Roku OS 16 labels "Rewind live
  TV". A client that sends `vodRemux=1` in `X-Client-Capabilities` and
  remuxes an MKV with a keyframe index (Cues) gets one VOD playlist over
  the whole file: Start answers `start_offset_sec` 0 and the new
  `full_timeline: true` (ABR sessions now send it too), and the client
  resumes by seeking. Segments come from one child run
  (`/transcode/sessions/{sid}/vr/seg/{name}`) that restarts at the segment
  boundary a seek needs. Other clients, non-MKV files, MKVs without Cues,
  multi-part items, burn-in and audio-only keep the EVENT remux with
  unchanged ffmpeg arguments, and so does a fleet with no worker that
  advertises the `vod_remux` feature (v2.7 workers);
  `TRANSCODE_VOD_REMUX=false` turns it off.
  Checked frame by frame on a local stack and on a Roku (OS 16.0.4); the
  channel sends it from 1.2. See docs/capability-profiles.md (#269,
  #305).
- **"Play on…" reaches a Roku.** A transfer was only an event on the
  notification stream, which a Roku can't hold open, so one sent to it did
  nothing. `POST /api/v1/playback/transfer` now also keeps it for two
  minutes, keyed by user and target device name, and the new
  `POST /api/v1/playback/transfer/claim` (`{"client_name": …}`) hands it
  over once (200: `item_id`, `position_ms`, `target_client_name`) or
  answers 204 when nothing waits. A newer transfer to the same name
  replaces an older one; names match ignoring case and outer spaces. The
  Roku channel asks every 5 s and starts at the sender's position (#173).
- **`GET /api/v1/libraries/{id}/items/position?item_id=`** answers an
  item's 0-based row in `GET /libraries/{id}/items` for the same sort and
  filters, and that listing's total (404 when the item isn't in it). The
  position SQL is derived from the listing queries themselves, so it can't
  disagree with them. The web, TV, Android TV and phone photo viewers use
  it for a photo opened outside the library grid.
- **Web Now Playing shows "Audio passthrough"** next to the play decision
  when the viewer's AV receiver decodes the audio (copied, or re-encoded to
  E-AC-3 / AC-3 for it); the tooltip names the codec (#211).
- **DSD plays in the desktop app.** DSF and uncompressed DFF files
  (DSD64–256, 44.1 and 48 kHz families) go through the native engine,
  converted to 24-bit 88.2 / 96 kHz PCM (DSD's 0 dB at −6 dBFS) by a
  three-stage decimating filter, so volume, ReplayGain, seeking and
  gapless work as for FLAC, on any output. DST-compressed DFF and DSD512
  are refused with a clear message. Before, a DSD file played nowhere in
  the app. **DSD over DoP** is a new opt-in (the desktop app's Audio
  (native engine) page) for DACs that accept it: it is used only on WASAPI exclusive
  output at 100 % volume with no ReplayGain change, and moving the volume
  switches the track to PCM. Not yet tried on a DoP DAC.
- **Steam Deck TV mode (desktop app).** Launched from Steam's Gaming Mode
  (`SteamGamepadUI=1` or a gamescope session), the desktop app goes full
  screen and shows the server's controller-driven TV app (`<server>/tvapp/`,
  the Xbox app's page) instead of the desktop UI. The Deck's controls drive
  it through the Gamepad API.
  The TV app's Exit and Forget server reach the app as `onscreen-shell://`
  navigations it intercepts; the remote page gets no Tauri IPC. Desktop Mode
  keeps the desktop UI, and `--tv-mode` / `ONSCREEN_TV_MODE=1` forces TV mode
  on any PC. Not yet checked on a Deck.

### Changed

- **Search forgives partial words, accents and typos.**
  `GET /api/v1/search` needed an almost exact title: full-text search
  matched whole words only, and the trigram match compared the query with
  the whole title. A title now also matches when every query word of 3+
  letters starts one of its words ("avat"), when it contains the query
  ignoring case and accents ("amelie" finds "Amélie"), or when the query
  resembles some of its words ("lord of the rinsg"). Exact titles rank
  first, then titles starting with the query, then titles containing it.
  Migration 00062 adds a stored, trigram-indexed `media_items.title_fold`;
  on 120k titles a typical query takes 3–15 ms, a word in a third of all
  titles ("the") about 300 ms (#181).
- **Dolby Vision profile 7 → 8.1 in the standard Docker image.** It
  installs Alpine's own ffmpeg packages rebuilt with OnScreen's `dovi_rpu`
  `convert_p81` patch (`docker/ffmpeg-alpine/`, published by the
  `ffmpeg-alpine` workflow as `ghcr.io/collinjaycock/onscreen-ffmpeg-alpine`,
  built natively for amd64 and arm64) instead of Alpine's stock ffmpeg, so a
  remux for a client that takes Dolby Vision 8.1 converts profile 7 there
  too, as the GPU image already did. The packages are built from Alpine's
  APKBUILD with the same configure flags: same codecs, filters and hardware
  paths (VAAPI, QSV, Vulkan). The Windows installer and the Linux tarball
  are unchanged and still play profile 7 from its HDR10 base.
- **Dolby audio for a receiver instead of AAC.** When a client bitstreams
  to an AV receiver (`audioPassthrough` lists `eac3` or `ac3`) and the
  server would otherwise encode a multichannel track to AAC (TrueHD or DTS
  on a client that asks for MPEG-TS segments, such as Roku, or on an old
  fleet), it encodes 5.1 E-AC-3 (or AC-3) at 640 kb/s instead, so the
  receiver plays Dolby surround rather than getting AAC it can't take over
  HDMI. Remux and transcode, TS and fMP4, single rendition and every ABR
  rung (the master's `CODECS` names `ec-3` / `ac-3`). Stereo and audio-only
  sources, and sessions under a bitrate limit, keep AAC. Workers advertise
  it as the `audio_dolby_encode` feature; a fleet of older workers keeps
  AAC. See docs/capability-profiles.md ("Receiver encode").
- **The web app is built on SvelteKit 3** (adapter-static 4), and the
  plugin host on mcp-go 1.5. No change in behaviour is intended; checked by
  the Go and web test suites and a signed-out browser run (#244).

### Security

From a whole-server review on 2026-11-14 (static analysis plus four code
reviews). Details and the decisions behind them are in docs/security.md.

- **Crafted images no longer take the server down.** A 34-byte JPEG whose
  EXIF directories point at each other made goexif loop forever, allocating
  about 1 GB/s, on every scan and photo view; EXIF is now walked with a
  bounded checker first (`internal/exifsafe`). A 1×20000 PNG fetched with
  `?w=1920` asked the resizer for a 38-million-row bitmap (a fatal,
  unrecoverable out-of-memory); resizes never upscale and check the target
  size against the 100 MP cap.
- **Memory bombs at scan time:** EPUB container/OPF XML is size- and
  count-capped, a CBR cover read keeps one page instead of all of them, and
  the MKV Cues reader no longer materialises a struct per element (cue
  points capped; one parse per file shared by concurrent Starts).
- **Reference files are spotted where ffmpeg would spot them.** The DASH /
  HLS / concat sniff now reads what ffmpeg's probes read (to the first NUL,
  up to 2 MiB, past an ID3 tag), so a manifest behind a long XML comment no
  longer passes, and such a file gets a protocol list that opens nothing.
  Live TV pipes allow only the pipe protocol and the live-stream demuxers.
- **Forgot-password skips accounts without a local password** (SSO, LDAP,
  managed profiles) instead of giving them one that bypasses the IdP.
- **PIN-switched sessions can't create API keys**, and a password reset or
  an admin "sign out everywhere" deletes the user's API keys too.
- **First-run setup refuses cross-origin browsers** (`Origin: null`,
  foreign origins, cross-site fetch metadata); the desktop app's origins and
  header-less scripts still work.
- **Storage path mappings** are validated (absolute, directory to
  directory, `From` at or above a library root) and match only at a path
  separator, so a mapping can no longer point one file at another
  (`PATH_MAPPING_INVALID`).
- **Managed profiles follow their owner:** requests need both the owner's
  and the profile's permission, quotas and the pending cap count the
  household, an admin stream limit counts the whole household, and an
  account can have at most 20 profiles (`PROFILE_LIMIT`).
- **Last-admin protection is atomic**: demote and delete check and write in
  one locked statement, and a role change bumps the session epoch in the
  same transaction.
- Also: LDAP StartTLS / LDAPS verify the server name; SAML metadata
  failures are cached for 30 s and the metadata route is rate-limited; DVR
  schedules can't target a disabled channel for non-admins; profile
  renames follow the username rule; over-long login usernames are refused
  before any lookup; the worker's ffmpeg log line and ffmpeg's own error
  output hide tokened URLs (a stream token or presigned signature); the
  transcode session root refuses symlinks and foreign owners; responses
  from *arr, OpenSubtitles, TVDB, TheAudioDB and ffprobe are size-capped.
- **One item-visibility gate** (`loadVisibleItem`) replaces about 25
  hand-written copies; library access fails closed when unwired, and the
  server refuses to start if a security dependency of a handler is missing.

- **SAML: assertion shapes crewjam/saml mishandled are refused**
  (GO-2026-6909). The library accepted a NameID in any XML namespace
  (OnScreen identifies SAML users by NameID) and crashed on an assertion
  without a Subject; both need an assertion signed by the configured IdP.
  The ACS now checks the raw response first (an encrypted assertion is
  decrypted with the SP key) and answers 401 `SAML_INVALID_ASSERTION` to a
  Subject child outside the SAML namespace, more than one NameID, a
  missing Subject or Issuer, or the artifact binding (`SAMLart`); a signed
  assertion without a NameID gets `SAML_NO_SUBJECT`. crewjam/saml 0.6.3
  handles both itself; the check stays until the advisory names a fixed
  version. CI's govulncheck fails on reachable advisories not reviewed in
  `.github/govulncheck-allow.txt` (#209).

From a review of every client on 2026-11-15 (five code reviews plus
dependency and secret scans; docs/security.md items 39–49):

- **Android phone: other apps can't drive the player.** The playback
  service is exported for Android Auto; an app that isn't trusted, this app
  or a verified car host now gets play / pause / skip / seek only, no
  queue changes, library or artwork, and only OnScreen may hand the player
  an item with its own URL. Car hosts are checked by signing certificate
  (or the platform key / a system install), not by package name.
- **Sign-out leaves less behind.** Phone: it ends an active Chromecast
  session; when the server ends the session, queued downloads stop and
  downloaded files are locked until the same account signs in again
  (another account erases them); cached artwork, books, car artwork and
  recent items are cleared on every change of account. Android TV: it signs
  out on the device before contacting the server (so an unreachable server
  can't leave it signed in), music on the now-playing screen stops, and
  cached artwork is cleared.
- **Both Android apps send the sign-in token only to the server's exact
  scheme, host and port.** An `http://` URL on an https server's own host
  and explicit port no longer receives it. The phone no longer keeps typed
  passwords, 2FA codes or the ListenBrainz token in saved screen state.
- **Xbox: the app stays on your server.** The web view refuses other
  origins and pop-ups, the console bridge accepts messages only from the
  server's page, and the native player loads only http(s) URLs on the
  server.
- **LG and Samsung builds list their inline scripts by hash** instead of
  allowing any inline script, and the Samsung app no longer allows the web
  view to navigate anywhere. The Samsung app now carries its policy in the
  page as well as in `config.xml`: on a Q80B the `config.xml` policy alone
  didn't stop an injected inline script, inline handler or remote script.
- **Kodi: no cleartext to a public server.** An address without a scheme
  tries https first; plain http works only for local-network hosts (the
  same rule as the other clients), and the sign-in token is dropped on any
  redirect that changes scheme, host or port. `auth.json` is owner-only,
  and the artwork proxy refuses encoded path separators.
- **Desktop:** a malformed audio header can no longer crash the app, and
  rustls, rustls-webpki (could accept distrusted intermediates),
  webpki-roots, tokio, rustix and ringbuf are on patched releases. CI now
  runs `cargo audit` on the desktop app and `npm audit` on the TV apps.
- **Web:** third-party posters and channel logos load without a referrer.

### Fixed

- **Web: after "Play on…", the player says where the title went.** The
  sender only paused. The receiver's transcode session then replaced the
  sender's (one per user and item), and the paused player failed with
  "Playback error: fragLoadError". A successful send now hands off the way
  an admin stop does: it saves the position, stops progress reports and
  autoplay, ends its session and shows "Playing on <device>" with Back
  (#174).
- **Roku: stopping a stream ends it on the server.** The channel (through
  1.1) sends `DELETE /api/v1/transcode/sessions/{sid}?token=…` without a
  bearer, and the route required one, so every stop was a 401 and ffmpeg
  ran on until the idle sweep. A request with no credential at all may now
  stop a session with that session's own segment token, which the stop
  revokes (a token whose session is gone gets 204). A bad bearer, cookie or
  API key next to a token is still 401. This fixes the store builds once
  the server is updated; Roku 1.2 also sends its bearer. Not yet run from a
  Roku against a fixed server (#271).
- **Samsung: seeking shows, and the picture is back.** Since the AVPlay
  anchor was made twice the screen (the white-dot fix), the player's bar,
  scrub time and preview, pickers, Skip Intro, Up Next and spinners were
  laid out in the 4080×2295 viewport the anchor widens and drew off the
  1920×1080 screen, so ←/→ seemed to do nothing; they now sit on the
  screen's grid. The page's content security policy (`object-src 'none'`)
  refused AVPlay's object, which left a white screen with the film playing
  behind it on the Q80B; both policies drop `object-src` and `default-src`
  (the object has no URL any source list could allow) and refuse iframes
  with `frame-src 'none'`. A held ←/→ (◀◀/▶▶) now scrubs, faster the longer
  it's held, and plays from where it's let go; a tap still moves 10 s (30 s).
- **LG: a held ←/→ (◀◀/▶▶) scrubs, as on Samsung.** Every auto-repeat of a
  held key was a 10 s tap: about 200 s a second at webOS's ~51 ms repeat,
  landing only after the release. The hold now speeds up the longer it's
  held, at the same pace whatever the repeat rate, and plays from where it's
  let go; a tap still moves 10 s (30 s), and a Magic Remote wheel step is
  always a tap.
- **Web: a watch limit or admin stop from the play decision is shown at
  once**, as transcode Start's is, instead of falling back to a local guess
  (audio-only files ended at a bare media error). Casting shows the
  watch-limit sentence instead of its reason code (`outside_allowed_hours`).
- **Web: libraries open past their first page on tall screens.** The grid
  re-checks the end of the list after every load, so a window taller than 48
  tiles keeps loading instead of stopping at "A".
- **Retention runs on server-only installs.** A new hourly
  `retention_purge` system task deletes expired sessions, spent password
  reset links, notifications over 30 days and old task runs (each task keeps
  its newest 50 or 30 days). Before, only the standalone worker purged
  sessions, and nothing purged the rest.
- **Scheduled tasks don't overlap themselves** (a run in progress, on any
  instance, holds the task), results are recorded even at shutdown, and
  runs left `running` by a dead instance are closed.
- **The library scan concurrency setting is enforced.** It was shown in
  Settings ▸ System but never read; every due library scanned at once.
- **Playback decision applies Start's gates** (watch limit, admin stop), so
  it no longer approves playback Start then refuses.
- **Stopping playback survives a hang-up**: transcode teardown runs on a
  detached context, so a stop sent as an app exits still ends the session.
- Notification "mark read" works with a read replica (it wrote through the
  read-only pool); the VOD-remux segment proxy no longer splices a worker's
  error body into a segment; a Valkey error no longer starts a duplicate
  VOD-remux run; DVR recordings `?offset=` past 2³¹ no longer 500s; 500s
  are logged with their cause, and a database outage is no longer reported
  as 404.
- **Parental time windows use the household's time zone** (new Settings ▸
  System ▸ Time zone; default `TZ`, else UTC as before).
- **SSE notifications reach every API instance** (relayed through Valkey).

Client fixes from the 2026-11-15 review:

- **Web:** after signing in, notifications, "Play on…", admin stop and the
  scan banner work without a reload. Leaving or skipping an episode while a
  stream starts no longer leaves a server session running or plays the old
  episode under the new one; fast moves between titles no longer show the
  wrong one. The Cast button appears on every title, not only the first per
  page load; Back during the next-episode countdown no longer jumps ahead;
  typing in the subtitle search or playlist picker no longer controls the
  player; a subtitle fetched while playing another version attaches to that
  version; pausing just after a progress save no longer rewinds. Live TV
  shows an error for a channel that keeps failing. Photo next / previous
  works anywhere in a large library; search no longer shows an older
  query's results; history, favorites, collections and playlists open
  albums, photos and books on their own pages; backup and recovery-code
  downloads work in every browser.
- **TV apps (LG, Samsung, Xbox):** one failed stream no longer stops HEVC,
  HDR and Dolby Vision titles from playing directly for good — a codec is
  set aside only when the refused stream carried it, for 30 days, and
  sign-out resets it. A server that stops mid-answer can no longer freeze
  the app. Xbox retries a stream the console can't play as a transcode.
  The trickplay thumbnail shows only while seeking (LG, Xbox); the Samsung
  exit prompt closes when a film is started from a phone.
- **Android TV / Fire TV:** "Play on this TV" and admin stops are no longer
  lost after the TV sits idle (the notifications stream was being dropped
  every three minutes); signing back in right after signing out no longer
  bounces to the sign-in screen; a bare LAN name (`nas`, `media.lan`) or a
  Tailscale address connects over http; cancelling server setup no longer
  saves the unchecked address; HOME while an artist's Shuffle loads no
  longer crashes; a dismissed skip-intro prompt stays dismissed; replaced
  posters refresh; a seek during an audio-track change keeps the chosen
  track; background music stops cleanly when the next track can't play.
- **Android phone:** offline downloads no longer fail when they take more
  than two minutes; pairing shows when the server can't be reached and
  backs off instead of retrying silently forever; a server address that
  fails its check isn't remembered; a bare LAN name connects over http;
  photos open at once in large libraries; reopening from Recents doesn't
  replay a voice request; background audio reports progress again after
  signing back in with Android Auto connected.
- **Roku:** no more sign-outs when several screens refresh the session at
  once; posters and search work with accented and non-Latin characters;
  Settings can't wipe the account's quality limits after a failed load,
  and a failed save is shown and undone; a network error in a library no
  longer says "This library is empty"; leaving during a stream start frees
  the stream at once.
- **Kodi:** starting another video no longer records progress for, or
  marks watched, the OnScreen item; cancelling "Sign in" from Settings
  keeps you signed in.
- **Desktop:** the window no longer freezes when the server stalls during
  play, skip, seek or stop; track ends are no longer clipped and a decode
  error no longer stalls the queue; 16-bit music is bit-perfect with
  ReplayGain off (it was scaled by 32767 and truncated); 8-bit WAV plays
  undistorted; a refused exclusive-mode stream falls back to shared mode
  instead of silence, and unplugging the output stops playback; the
  Windows media overlay shows cover art again. Left and right no longer
  swap for the rest of a track after the decoder falls behind for a moment
  (every output now takes whole frames), and the volume slider works on
  Linux (the Linux output never applied it).
- **Photos opened outside the grid** (a hub row, search, the timeline, a
  link) get previous / next anywhere in a large library on web, the TV
  apps, Android TV and the phone, through the new position route; before,
  only the first 200 (web), newest 500 (TV apps) or first 600 (phone)
  photos had neighbours, and Android TV read the whole library first. On an
  older server the viewers behave as before.

### Performance

- **Migration 00064** adds indexes for the library grid's filter and sort,
  the restricted-profile artwork check, the `/jobs` counters and genre
  pages, rewrites `content_rating_rank()` as inlinable SQL, adds
  `media_items.photo_taken_at` (trigger-maintained) for the photo timeline,
  and drops the never-read `hub_recently_added` view. Measured on 493k
  items: a 50-show grid page 19.9 → 0.12 ms, a restricted poster check up
  to 24 → 0.06 ms, the photo timeline's first page 66 → 0.3 ms, watch
  history 89 → 0.8 ms.
- An HLS segment costs 2 Valkey round trips instead of 8 (ABR: 2 instead of
  17, and one fewer worker call); the progress beacon reads only the
  viewer's sessions, through a per-user index.
- Rescanning an unchanged file costs one query instead of four or five, and
  the scan reads narrow file snapshots instead of every column twice.
- Library roots, (user, library) grants, owner ratings, `/jobs` counts and
  the MP4 faststart probe are memoized briefly; `watch_plays` refreshes only
  after new plays; direct play and downloads go through sendfile (the logging and recovery
  wrappers hid it).
- **Clients:** web HLS demuxing runs in hls.js's Web Worker; pages render
  without waiting on the setup check; polls stop with their page, pause in
  hidden tabs, and the notification stream backs off (web, Android TV,
  Roku's paused player, the pairing screens). Photo viewers on web, phone
  and the TV apps read a window around the photo instead of the whole
  library. Going back to a deep library grid loads one page (TV apps) and
  builds off-screen (Roku); Roku grids fetch 240 px posters instead of
  500 px. Android TV trickplay keeps two sprite sheets (RGB_565, one
  download each) instead of every sheet; phone downloads write progress
  once a second instead of per 8 KB. Live TV's back buffer is bounded on
  the TV apps; album tracks need fewer requests; Samsung polls the live
  playlist less and lazy-loads posters on 2020–21 sets. Back to a
  collection or the Watchlist on the TV apps opens at the card instead of
  refetching every page above it.

### Android TV / Fire TV and Android phone

- **A refusal from the play decision is shown at once.** The server's play
  decision now runs transcode Start's gates, so it can answer 403
  `PARENTAL_LIMIT` (watch limit) or `PLAYBACK_STOPPED` (an admin stop). The
  apps used to treat that as "no decision", fall back to their own, and meet
  the same refusal from Start one call later (or, for a direct play, a bare
  stream error). Now the player, the TV app's background audio and the
  phone's cast path show the watch-limit or admin-stop message straight away
  and start nothing.
- **The apps declare their decoders' video profile and level.** Both apps
  now send `h264Profile` / `h264Level`, `hevcProfile` / `hevcLevel` and
  `av1Profile` / `av1Level` (server v2.7) from what their hardware H.264,
  HEVC and AV1 decoders report, so a file above them (an H.264 level 5.2
  rip on a 5.1 decoder, 10-bit HEVC on an 8-bit-only one) is transcoded to
  fit instead of failing to play. A codec with no hardware decoder sends
  neither key and plays as before. The level sent is always one the
  profile sent reaches; HEVC High-tier levels count as their level (the
  server has no tier).
- **Android Auto in the phone app** (#183). One tab per audio library:
  music (artists, albums, tracks), audiobooks (author, series, book,
  chapter) and podcasts, with search, artwork and "play …" by voice from
  the car or the phone's Assistant. A book resumes at its chapter and
  speed. Tested with the Desktop Head Unit and a Galaxy S24 FE, not in a
  car or on Android Automotive.
- **The phone app on tablets and Chromebooks** (#297, 0.6.0 / 1014). On
  windows at least 600 dp wide and 480 dp tall: a navigation rail, a
  two-pane title page, readable widths and larger posters; from 840 dp,
  Settings and libraries open their detail beside the list. A physical
  keyboard drives the player (Space/K, J/L or ←/→, M to mute), the photo
  viewer, the comic and EPUB readers, with Esc as Back. Right-click does
  what a long-press does. Moving a Chromebook window or docking a keyboard
  no longer restarts a video. Phones are unchanged. Tested on tablet
  emulators only; right-click and M weren't tried.
- **Dolby and DTS films are no longer silent on phones without those
  decoders** (#180). The phone claimed AC-3, E-AC-3 and DTS everywhere; it
  now claims each only when Android lists a decoder, so the server
  converts the rest.
- **Phone: the seek bar on a server session shows the film's clock**
  (#180), not the session's (0:00 at the resume point, a growing length).
  A seek outside what's written restarts the session there. Two quick taps
  on Back no longer leave a black screen, and a title page's artwork takes
  at most 42 % of a wide window.
- **Both apps use the platform's certificate checks** (#182). A custom
  trust manager, flagged by Meta's security review, skipped Android's
  per-host network security rules; OkHttp's default trust replaces it.
- **Android TV: e-books open their title page** (#197) instead of the
  player, which ended at "Playback error: HTTP 422". Checked on a Hisense
  Google TV.
- **Android TV: Google Play no longer hides the app from TVs that don't
  report a landscape screen** (#293, 1.6.0 / 29).
- **Phone: pairing waits for the code to be confirmed** (#292). After the
  2026-11-15 review's changes the phone read the server's "still pending"
  reply as an error and gave up after eight polls. No store build carried
  that.
- Also: on Android TV, Right or Down from Sort and "Search this library"
  keeps the focus, and the library title hides after an A–Z jump (#189,
  #191); the empty Watchlist says "Choose Add to Watchlist on a title's
  page", and a phone artist with no albums of its own says so (#195); the
  phone draws edge to edge without the APIs Android 15 deprecates, and
  resource shrinking makes its bundle about 5 % smaller (#294).

### Roku

- **The OnScreen channel is in the Roku Channel Store** (#171–#178,
  #242). 1.0.0 went live on 2026-11-09 in the United States, Canada and
  the United Kingdom (channel code V5XPLDR); 1.1.0 and 1.2.0 are its
  updates. It covers PIN pairing and sign-in, Home, libraries, title pages,
  direct play, remux and transcode with resume, music, audiobooks, podcasts
  and photos. To stay store-safe it has no Live TV / DVR, OpenSubtitles
  search or requests. First run on a Streaming Stick 4K on an LG C1 with
  an AV receiver.
- **Playback on Roku.** Sessions use MPEG-TS segments, because Dolby audio
  in fMP4 HLS is silent on a Roku; E-AC-3 and AC-3 are bitstreamed to a
  receiver. Chapters (Down opens the picker), audiobook speed 0.75–2×
  (applied by the server's `audio_rate`, saved per book), a now-playing
  screen for music, audiobooks and podcasts, text and external subtitles
  through a session, and the account's audio and subtitle languages. Dolby
  Vision is not claimed. Music asks the server's play decision as video
  does (a FLAC album used to fail track after track), and a failed direct
  play is retried once as a transcode.
- **Browsing on Roku.** Home follows the row order saved on the web, with
  each library's Recently added and a Collections row. Libraries are grids
  with the other TV apps' six sorts plus watch-state and genre filters,
  kept per library. Search can be limited to one library, photos have a
  4 s slideshow, and Back returns to the card, page, filters or query you
  left. Settings has the account, sign out, forget server, about, and
  playback preferences (languages, forced subtitles, quality limits; the
  rating ceiling read-only).
- **Sign-in on the device comes first** (#176, Roku certification 2.2):
  username and password, then pairing from a phone or computer, which
  single-sign-on accounts still use; the pair screen names the server's
  OIDC or SAML sign-in.
- **"Play on…" reaches the Roku** (#173): the channel claims a waiting
  transfer every 5 s (see Added) and opens the title at the sender's
  position.
- **1.1.0: watched marks and Up Next use the item's length** (#268). On a
  remux they followed the growing session playlist, so an episode could be
  marked watched after about two minutes.
- **1.2.0: a remux plays as a whole film, and leaving frees the server**
  (#269, #271, #291). For an indexed MKV the channel asks for a complete
  VOD playlist (`vodRemux=1`), so Roku OS 16's pause bar shows the film's
  length instead of "Rewind live TV" and seeks land in the same session.
  The session's DELETE now carries the account's credential; before, every
  stop was a 401 and ffmpeg ran until the idle sweep. Checked on Roku OS
  16.0.4: the pause bar and seeks. Not checked on a device: the DELETE on
  Back, and the single-flight token refresh. Submitted 2026-11-23.
- Also (#175, #178): a deep link to a deleted title goes Home instead of
  crashing the channel; the now-playing total uses the file's length (a
  chapter showed 2:04 of 24:50); a season without art shows its show's
  poster; the empty Favorites and Watchlist messages name the Roku's
  buttons; the deprecated `GetAudioDecodeInfo` call is gone.

### Meta Quest

- **A Meta Quest app** (#180, #182, #184). The phone app's `quest` build
  (`tv.onscreen.quest`, Android 10+) is a 2D panel app for Quest 2, Pro, 3
  and 3S. Horizon OS has no Google services, so there is no Cast. There is
  no picture-in-picture, no HDR claim and no notification permission
  (background audio still plays, without its notification). The player has
  an app-only volume slider with mute, since the panel has no volume keys.
  Checked on a Quest 3 (Horizon OS v69). Submitted to the Meta Horizon
  Store on 2026-11-04 and resubmitted as 1011; not in the store yet.

### TV web apps (webOS, Tizen, Xbox)

- **LG: Full HD models** (#301). LG gives Full HD sets a 1280×720
  graphics plane, where the 1920×1080 layout showed only its top-left two
  thirds. The app now scales the canvas to fit (a transform, so the Magic
  Remote pointer still lands on what it points at), and a second package
  declares 1280×720 (`npm run package:720`). Run on the C1 with that
  package, not on a Full HD set; UHD models render as before.
- **LG: new app ID and title** (#194, #288). LG keeps the deleted
  `com.onscreen.tv` reserved, so the app is `tv.onscreen.webos` 1.0.0. Its
  launcher title is "OnScreen Media Player" after LG's 2026-11-15 pretest
  found "OnScreen" taken; its own screens still say OnScreen. A sideloaded
  `com.onscreen.tv` is a separate app. Not yet resubmitted to LG.
- **Samsung: no white dot over the picture, and no overscan** (#239, #240,
  #245). The firmware draws a dot at the centre of AVPlay's anchor; the
  anchor is now twice the screen so the dot falls off it, and the picture
  sits on AVPlay's 1920×1080 grid (it had been drawn about 2.1× too big).
  Checked on the Q80B. Tizen 1.1.3 (#290) carries these and the seek fixes
  above; its upload waits for 1.1.1's certification.
- **Xbox: B works after the server prompt** (#267). Once the prompt had
  been used, the console sent B as Escape until a restart, and B did
  nothing anywhere. The page comes from the server's `/tvapp/`, so
  consoles get this with the server. The Xbox app has been in the
  Microsoft Store since 2026-11-19 (9N58M48F5XMW).
- **An A–Z jump fills the letter's row** (#190): a letter not yet loaded
  opened with its first title alone in its row. Checked on the C1. The
  empty Watchlist names "Add to Watchlist" (#195).

### Desktop

- **The Windows app in the Microsoft Store** (#296): a full-trust MSIX in
  the Xbox app's Store product (9N58M48F5XMW). PCs get the desktop app and
  consoles keep the Xbox one. Store installs are Store-signed, so the
  MSI's SmartScreen warning doesn't appear, and the Store updates them. It
  passes the Windows App Certification Kit (its one failure only matters
  in S mode). Not yet uploaded; sign-in and playback inside the package
  weren't tried.
- **ALAC plays in the native engine** (#220). Apple Lossless in MP4 failed
  to open and fell back to the web view's player; it now decodes at its
  own bit depth (24-bit isn't cut to 16). A 24-bit AIFF with an ID3 chunk
  no longer ends with two stray samples; FLAC, WAV and AIFF decode as
  before. Saved sign-ins carry over; on Linux the keyring is reached
  without libdbus (compiled, not run there).

### Kodi

- **A Kodi add-on** (#270, `plugin.video.onscreen` 0.2.0, Kodi 20+).
  Browse in Kodi's own interface: Home rows, video, music, audiobook and
  podcast libraries, photos, Live TV channels and recordings, Watchlist,
  favorites, collections and search. Sign in by PIN (single sign-on works
  there) or password and two-factor. Playback follows the server's play
  decision, with InputStream Adaptive offered for growing sessions, the
  account's track languages, and *Play with options…* for version, tracks
  and burn-in. A background service reports progress, ends sessions,
  obeys admin stops and watch limits, feeds the Up Next add-on and
  receives "Play on…". The server lists its sessions as platform `kodi`.
  Install from the repository at https://on-screen.tv/kodi/, which keeps
  it up to date. Checked on Kodi 21.3 for Windows; Live TV playback on a
  tuner wasn't. 0.2.1 (2026-11-23) carries the 2026-11-15 review's Kodi
  fixes (see Security and Fixed); Kodi updates to it from the repository.
  Its zips are now built with LF line endings on Windows too, so a
  release has the same bytes wherever it's built.

### Build and CI

- **`clients/build-packages.ps1`** (#295) builds each store's upload into
  `clients/packages/` as `OnScreen-<Store>-<Platform>-<version>…`, with a
  `.sha256` and a `.json` record (commit, clean tree, signer). It refuses
  a dirty client folder and checks that Android packages carry the upload
  key. Targets include the desktop MSIX (#296) and both LG packages
  (#301); `clients/roku/scripts/sign.mjs` signs a Roku zip on a dev-mode
  device.
- **A Kodi workflow** (#270) runs pytest, ruff and kodi-addon-checker on
  Linux and Windows. The phone job tests and lints the Quest build too
  (#180).
- **Android:** AGP 9.5 with built-in Kotlin, Gradle 9.8, compileSdk 37
  (target 36), OkHttp 5.5, Retrofit 3, Compose UI 1.12, Cast 23 (#212,
  #216, #243); device-checked on a Galaxy S24 FE and a Hisense Google TV.
- **TV web apps:** Vite 8, SvelteKit 3, adapter-static 4, TypeScript 6
  (#219, #266); still one inlined `index.html` for Chromium 69 / 79.
- Actions on the Node 24 runtime (`checkout` 7, `setup-node` 7.1,
  `docker/build-push-action` 7.4, `action-gh-release` 3); Roku packaging
  on archiver 8, with a byte-identical zip (#195, #217).

## [v2.7.0] — 2026-11-03

v2.7, the last server release of the v2 line (see
[docs/v2.7-roadmap.md](docs/v2.7-roadmap.md)): the plugin host's metadata and
task roles, audio passthrough on full transcodes and video profile/level in
the capability profile, Roku seek thumbnails, a Valkey lock for the ABR
ladder, a default backup task with a restore drill, a monitoring pack, and
fixes across the web and Android apps. Since that batch: background ffmpeg
work no longer starves the server, Safari plays again, the Xbox app plays
HEVC, HDR10 and Dolby Vision in the console's own player, and the Roku
channel has its art and Roku's certification hooks. It also carries the
v2.6.2 changes below, which were never tagged.

### Upgrade notes

- **Migration 00058** (applied at boot). Task plugins each get a
  scheduled task, so the rule allowing one enabled task per task type no
  longer applies to plugin tasks; one task per plugin applies instead.
  Task plugins registered earlier get their task on the first start.
- **Migration 00060** adds `media_files.video_profile` and `video_level`
  (applied at startup with `AUTO_MIGRATE=true`). Files already in the
  library have neither until they are re-probed
  (`POST /api/v1/maintenance/reprobe-metadata`, then a scan); until then
  they aren't checked against a client's profile/level, as before.
- **Workers:** the re-encode passthrough of TrueHD and DTS uses the
  `audio_passthrough` worker feature v2.6 workers already have. The encoder
  level flags need v2.7 workers; an older worker ignores them and picks the
  level itself, for an output the server has already sized to fit.
- **Migration 00059** adds a daily `Database backup` task, but only on a
  **fresh install** (a database with no accounts yet). Upgraded servers
  are left as they are; create the task in **Settings ▸ Tasks** if you
  don't back up another way.
- **Migration 00061** (applied at startup) turns the library permission
  check for labels into a plain SQL wrapper that Postgres can inline, so
  unrestricted users skip a function call per row. No data changes.
- v2.6.2 was prepared but never tagged; its changes (draggable Home rows,
  library card hover) ship in v2.7.0.
- Docker: `docker/docker-compose.yml` now mounts a `backup_data` volume
  at `/var/backups/onscreen`. Add the same to your own compose file if
  you use the scheduled backup, or its dumps go when the container is
  recreated.

### Added

- **Metadata plugins.** A plugin registered with the `metadata` role is
  now called during enrichment through its `enrich_item` tool, after
  TMDB, TVDB, AniList, MusicBrainz and NFO files. It gets the item's
  type, title, year, numbering, provider IDs and file name, and may
  return a title, summary, tagline, genres, year, rating, content rating,
  release date, poster URL and provider IDs. By default those fill only
  empty fields; `"mode": "override"` replaces them. Locked fields are
  never changed, provider IDs and posters are only ever filled, and the
  poster must come from one of the plugin's allowed hosts and is fetched
  through the SSRF-guarded artwork client. A slow or failing plugin never
  fails a scan. Contract in [docs/plugins.md](docs/plugins.md).
- **Task plugins.** A plugin registered with the `task` role becomes a
  scheduled task (`Plugin: <name>`, daily at 04:30 until changed) in
  Settings → Tasks, with the usual schedule, Run now, last run, status,
  error and run history. Its `run_task` tool gets the server version and
  the library list (no paths or keys); its text reply is the run output,
  and an error reply fails the run.
- **Settings → Plugins** offers all three roles, shows each one's
  contract, and Test checks that a metadata or task plugin is reachable
  and advertises its tool.
- **Seek thumbnails on Roku.** `GET /trickplay/{id}/index.bif` (alias
  `/api/v1/items/{id}/trickplay/index.bif`) serves an item's trickplay
  thumbnails as a BIF, the format the Roku Video node's trick-play bar
  renders natively. The server cuts it from the existing sprite sheets
  (frames up to 320 px wide) on the first request and caches it next to
  them until the sprites are regenerated; concurrent first requests share
  one build. Same gates as the sprites: Bearer / cookie or an asset
  `?token=`, the library ACL and the rating ceiling; 404 until the item
  has thumbnails. The Roku player points the content's `HDBifUrl` /
  `SDBifUrl` / `FHDBifUrl` at it, so scrubbing shows thumbnails; the
  unused client-side VTT parser is gone. Thumbnails are left off for
  another version (edition) than the one they were cut from, for a
  re-issued session that starts mid-item, and for audio.
- **Audio passthrough on full transcodes** (capability profiles Phase 3).
  When the video has to be re-encoded, a client that lists the audio codec in
  `audioPassthrough` now gets the track copied for its receiver instead of
  AAC: AC-3 and E-AC-3 in the usual segments, TrueHD and DTS in fMP4
  segments on workers with the `audio_passthrough` feature. Every rung of an
  ABR ladder carries the same copied track, and the master playlist's
  `CODECS` and `BANDWIDTH` name it. AAC stays the answer for clients that
  don't list the codec, for MPEG-TS requests or an old fleet (TrueHD/DTS),
  under a bitrate limit (user cap, internet limit or quality profile), and
  for audio-only files. Now Playing shows `audio_passthrough` on the output
  and "TrueHD audio passed through to the receiver".
- **Video profile and level in the capability profile.** New
  `X-Client-Capabilities` keys `h264Profile` / `h264Level`, `hevcProfile` /
  `hevcLevel` and `av1Profile` / `av1Level` (e.g. `h264Profile=high,
  h264Level=4.1,hevcProfile=main10,hevcLevel=5.1`) declare the most a
  client's decoder takes. A source above either is transcoded (the scanner
  now stores each file's profile and level), the re-encode is sized to fit
  the level at the source frame rate and the encoder is told the level, and
  ABR ladders are capped the same way. Clients that don't send the keys get
  the same decisions as before. Item details show `video_profile` /
  `video_level`.
- **Backups by default, and a restore drill.** New installs back up the
  database every night at 03:11 to `/var/backups/onscreen`, keeping the
  newest 7 dumps. The Linux installer creates that directory for the
  service user and the Windows installer creates it readable by SYSTEM and
  Administrators only. [docs/restore-drill.md](docs/restore-drill.md) is a
  step-by-step rehearsal: take a backup, restore it into a throwaway
  server through the API or with `pg_restore`, verify it, tear it down.
- **Alerting pack** in `deploy/monitoring/`: a Grafana dashboard for the
  server's Prometheus metrics, Prometheus alert rules (server down, 5xx
  rate, slow database queries, rate limiter failing open, transcode jobs
  finding no live worker, dispatch errors, stalled library scans, webhook
  failure storms, slow hub refresh, optional per-worker health probes)
  with `promtool` unit tests, and an example `prometheus.yml`. Loading
  them is described in [docs/deployment.md](docs/deployment.md#dashboards-and-alerts).

### Changed

- The per-plugin circuit breaker (three failures in a row, five minutes
  open) now covers all three roles, and opening or closing it is audited
  for each (`plugin.metadata.*`, `plugin.task.*`).
- **ABR works on a replicated API tier.** Starting or restarting a
  ladder rung's encoder now takes a Valkey lease (`SET NX PX` with a
  per-holder token, renewed while held, released with compare-and-delete)
  on top of the in-process mutex, so two API instances serving segments
  for the same session can no longer both start an ffmpeg for one rung.
  The 2-second floor between forced restarts is shared through Valkey
  too. If Valkey errors, the lock falls back to the in-process mutex.
- **Faster rescans of large TV and music libraries.** A scan no longer
  repeats the show / season (or artist / album) lookup for every file: a
  per-scan cache resolves repeats with one primary-key read, which still
  checks the item hasn't been merged, deleted or moved since. For a show
  whose title no longer matches its folder, a 60-episode rescan made 183
  queries instead of 360 and one full-text search instead of 60.
- **Refresh metadata and Fix Match say how it went** (v2.7 item 7, known
  issue 4). Refresh shows "Metadata refreshed" when the item changes, and
  "No changes found" when the 24 s polling window ends without one. Fix
  Match no longer reloads the page after a fixed 2 s: it polls until the
  item carries the new match, then updates the page in place (title,
  artwork, episodes, cast) and says "Matched to …"; if the match is still
  loading after 24 s it says so.
- **Better 4K Cast detection** (v2.7 item 8, known issue 9). The phone app
  and the web player place a Chromecast from what the Cast SDK reports
  instead of the name alone: a receiver without video out (a speaker or a
  group) never gets the 4K profile, known model names decide next (a "Bose
  Smart Ultra Soundbar" no longer passes as a Chromecast Ultra, a Mi Box S
  is 4K), and the name pattern stays as the fallback. The phone adds
  **Settings → Playback → Chromecast quality** (Auto / 4K / HD) for a 4K TV
  the detection can't place, such as a TV with Google TV built in that
  reports its panel's model number.
- **Background ffmpeg work is held back** (#150, #153). Trickplay sprites,
  chapter thumbnails, intro detection (audio fingerprints) and credits
  detection now run ffmpeg with 2 threads at nice 10 (new package
  `internal/lowprio`), and the sprite pass decodes keyframes only. A backlog
  of new episodes had kept a 24-thread server near a load of 12 and starved
  Postgres: the home page took 1.7 s, 7 s every 5 minutes. Playback
  transcodes are unchanged. On a 4K HEVC clip, credits detection drops from
  15.8 to 2 cores and a sprite pass from 108 CPU-seconds to about 1.
- **Faster Home** (#150). Memories reads one index range per date window
  (28 to 1 ms), Trending serves its last result while one background query
  refreshes it, and the label permission check is inlined (migration 00061;
  Recently added 20 to 11 ms).
- **Item page buttons line up** (#155). On the web app's movie and show
  pages the main actions (Play, Play on, Download, Request more seasons)
  share one height in one row, and the smaller tools (Report a problem and
  the like) share another.

### Fixed

- **Safari plays again** (#152, #154). The web player read a video width of
  0 at `loadedmetadata`, which WebKit always reports for a stream until a
  frame is decoded, as "can't decode", and escalated to a transcode, again
  and again until the transcode rate limit answered 429. It now waits for a
  picture or for real playback without one, and escalates once per item.
  Then every Safari stream failed at once with `bufferSeekOverHole`: since
  hls.js 1.7 a start-gap skip counts against `nudgeMaxRetry`, which the
  watch page set to 0. It is back at the default.

- **EPUBs are named from the book, not the file.** The scanner reads the
  EPUB's own title, author and description (its OPF metadata) instead of
  titling the book after its filename ("oscar-wilde the-picture-of-dorian-gray"
  becomes *The Picture of Dorian Gray* by Oscar Wilde). Books scanned before
  are renamed in place on the next scan, unless their title was set by
  enrichment or an admin; locked fields stay as they are. The title is what
  OPDS e-reader apps, the web and the TV apps show, and what OpenLibrary
  enrichment searches with.
- **Covers for "advanced" EPUBs.** A cover that is an SVG (Standard Ebooks'
  Advanced EPUB embeds the painting as a JPEG inside an SVG with the title
  drawn over it) was saved raw under `{id}-poster.jpg` and showed as a broken
  image everywhere, OPDS included. The scanner now takes the image the SVG
  embeds or links, falls back to another raster image in the book, and never
  saves bytes that aren't an image. A poster saved the old way is rewritten
  on the next scan.
- **Book downloads carry their own type** (`application/epub+zip`, PDF, CBZ,
  CBR…) instead of `application/octet-stream`, so an e-reader app fetching
  an OPDS acquisition link files the download correctly.
- **The manual poster picker no longer wipes an item's details.** Choosing
  a poster (Set Poster, or a poster from the item page) cleared the
  summary, tagline, original title, ratings, content rating, runtime,
  tags and release date of every field that wasn't locked. It now changes
  only the poster. Items already affected get their details back on the
  next Refresh metadata.

### Android TV / Fire TV

- **The seek bar's scale after taking back background audio** (v2.7 item
  11). Reopening a transcoded or remuxed track that was playing in the
  background showed the server session's own scale ("1:30 / 2:28") for 4 to
  21 s. The player screen now takes the session's offset and the track's
  length from the hand-off before the transport bar first draws, and tells
  the bar to read them again once the item has loaded.
- **The remote's Next and Previous work for background music and books**
  (v2.7 item 12). The background session's player holds one item, so its
  media session offered no skip at all and MEDIA_NEXT did nothing. It now
  offers Next and Previous for music and audiobooks and hands them to the
  background service, which plays the album's (or instant mix's, or book's)
  next or previous item. Previous more than 3 s into an item restarts it,
  and at the first item of an album or book it restarts too.
- **A hung progress report no longer holds up background heartbeats**
  (v2.7 item 14; the phone app too). The background service awaited each
  'playing' PUT before the next tick, so one that hung (up to the client's
  60 s timeout) delayed every beat after it. Beats now go out without the
  loop waiting, are cut off after 15 s, and a tick is skipped while the last
  beat is still out. On the TV a pause or stop report waits for a beat still
  out for 2 s at most.
- **Background streaming keeps Wi-Fi awake** (v2.7 item 14; the phone app
  too). The background player uses Media3's `WAKE_MODE_NETWORK`: a Wi-Fi
  lock as well as the CPU wake lock, held only while it plays or buffers, so
  Wi-Fi power save with the screen off can't starve a stream. The TV's
  player screen keeps the CPU wake lock alone, as before.
- A buffering stall is not reported as a pause (v2.7 item 13): already true
  since v2.5.0, where a rebuffer holds the heartbeat instead of sending
  'paused'.

### Xbox

- **The console's own player for HEVC, HDR10 and Dolby Vision** (#157).
  The Xbox web view decodes only H.264 and VP9 (it claims HEVC, AV1 and
  AC-3 and plays none of them; #156 stopped claiming them) and can't switch
  the TV into HDR. Video now plays in a `MediaPlayerElement` behind the
  transparent web view, driven by the page through a stand-in for the video
  element, so the player screen, pickers and subtitles are unchanged. With
  it the app claims HEVC to 10-bit, MKV, AC-3 / E-AC-3, HDR when the TV
  takes HDR10, and Dolby Vision 8.1; the shell switches the TV into a 4K
  HDR10 or Dolby Vision mode and back. Checked on a Series X and an LG C1.
  Direct play of a file is limited to a server on the home network. Music
  and audiobooks stay in the web view.
- **Controller input** (#156). The web view gets no controller key events,
  so the shell forwards the controller's keys to the page; B is Back on
  every screen, once.
- **Microsoft Store packaging** (#157, #158): the Partner Center identity,
  tiles and splash at every scale, `build.ps1 -Store` for the
  `.msixupload`, and the listing text and images.

### Roku

- **Channel art** (#160). The images the manifest names didn't exist, so
  the channel couldn't be sideloaded.
- **The capability header follows the device** (#160). It claimed 4K,
  10-bit and HDR on every Roku; an HD Roku, or a 4K one on a TV without HDR,
  would have been sent HDR untouched. 4K, 10-bit HEVC, HDR10 and HLG now
  come from the firmware's own reports.
- **Certification hooks** (#161): the launch beacons (`AppLaunchComplete`,
  and `AppDialogInitiate` / `AppDialogComplete` around a sign-in at launch)
  and deep links at launch and while running. Only an item id is accepted
  as a deep link's `contentId`. The channel has still not run on a Roku.

### TV web apps (webOS, Tizen, Xbox)

- The D-pad can leave the sign-in and token keyboards, Back to Libraries
  returns the focus to the library left, and a GET that stalls is sent once
  more before the app gives up (#156).
- **Tizen 1.1.1 / 1.1.2** (#146, #147): Return on the first screen opens an
  exit popup, as Samsung's review requires.

### CI

- **TV web apps on every PR** (v2.7 item 10). A new `TV web clients`
  workflow runs `svelte-check` and the Vitest suites of the webOS, Tizen and
  Xbox TV apps (about 2,600 tests) on PRs and pushes that touch them.
- **Playwright nightly** (v2.7 item 10). The nightly E2E workflow gains a
  Chromium job that runs the web Playwright specs against its own compose
  stack, seeded by `web/tests/e2e/seed.mjs` (an admin and an empty library).
  Form logins in the specs now wait out the auth rate limiter instead of
  failing on it.
- **The release image builds natively** (#164). The Docker build stages
  run on the build machine's platform and Go cross-compiles, so the arm64
  image no longer compiles the web apps and the Go binaries under QEMU,
  which took over an hour for v2.7.0's first image build. Only the runtime
  stage is per-platform.
- **The Playwright nightly's metrics tests** never passed: the job
  publishes the metrics port as `127.0.0.1:${DEBUG_PORT}` but didn't pass
  `METRICS_URL`, so `metrics.spec.ts` tried `localhost:7071`. The job sets
  it now.

## [v2.6.2] — never tagged (shipped in v2.7.0)

Patch release for the web app: Home rows can be dragged into order, and
the library grid's hover buttons no longer cover the year.

### Upgrade notes (from v2.6.1)

- **No migrations** and nothing to do: pull the new image (`:2.6` or
  `:2.6.2`) or install the new binary.
- The desktop installers are rebuilt as 2.6.2.

### Changed

- **Drag Home rows into order** ([#49](https://github.com/CollinJAycock/OnScreen/issues/49)).
  **Customize rows** on Home, which sets each user's row order and which
  rows show, now lets you drag a row into place. The ↑ / ↓ buttons stay
  for the keyboard and touch screens.
- **Library cards on hover** ([#48](https://github.com/CollinJAycock/OnScreen/issues/48)).
  The play mark is centred, the title (already under the card) is no
  longer repeated on the poster, and the add-to-playlist and refresh-artwork
  buttons sit together bottom-right instead of over the year and running
  time.

### Fixed

- **Android phone app tests:** a test of the EPUB reader let its download
  run on past the end of the test, which now and then failed an unrelated
  Home test in CI. No change to the app's behaviour.

## [v2.6.1] — 2026-10-19

Patch release. The server: History shows artwork for movies and music again,
a Chromecast gets the poster of what it plays, and the TV web app at
`/tvapp/` (the Xbox app) gets the Watchlist and a Status button on title
pages. The native TV, phone and Roku apps get the same Watchlist and Status
screens in their next store builds. Coming from v2.5.x, read the v2.6.0
upgrade notes below first.

### Upgrade notes (from v2.6.0)

- **No migrations** and nothing to do: pull the new image (`:2.6` or
  `:2.6.1`) or install the new binary.
- The desktop installers are rebuilt as 2.6.1.

### Added

- **Watchlist on Android TV / Fire TV.** A new **Watchlist** card in the
  home screen's Browse row opens one row per status — Plan to Watch,
  Watching, On Hold, Completed, Dropped — each headed with its count, with
  empty statuses left out. Rows page in 50 cards at a time as you scroll;
  episodes and seasons read "Show · S1 · E2". OK opens the title's page and
  the list refreshes when you come back; a long-press of OK moves the card
  to another status or removes it. Uses the v2.6.0
  `GET /api/v1/watchlist` and `/watchlist/counts`.
- **Status on Android TV / Fire TV title pages.** Movie, show, season,
  episode, album, artist, audiobook and book pages get a **Status** button
  in the action row (after Play, before the heart): it reads "Add to
  Watchlist", or "Status: Plan to Watch" once one is set, and opens a picker
  of the five statuses with the current one checked, plus "Remove from
  Watchlist". Play keeps the initial focus; a failed change says so and
  keeps the old status. So titles can now be added to the TV app's
  Watchlist from the TV itself. Uses the existing
  `GET/PUT/DELETE /api/v1/items/{id}/watch-status`; no server change.
- **Watchlist on the Android phone app.** A new **Watchlist** entry in the
  home screen's ⋮ menu opens the same lists as the web page: one tab per
  status (Plan to Watch, Watching, On Hold, Completed, Dropped) with its
  count, type chips for the selected tab, a Recently changed / Title sort and
  a poster grid that pages in 50 at a time (episodes read "Show · S1 E2").
  Tap opens the title; long-press or a card's ⋮ moves it to another status
  or removes it. Uses the v2.6 `GET /api/v1/watchlist` and
  `/watchlist/counts`; no server change.
- **Watchlist on the TV web apps (LG webOS, Samsung Tizen and the Xbox /
  `/tvapp/` app).** A **Watchlist** pill in the top bar, next to Favorites,
  opens one row per status — Plan to Watch, Watching, On Hold, Completed,
  Dropped — each headed with its count, empty ones left out. Rows load 50
  posters at a time and fetch more as focus nears the end; an episode reads
  "Show · S1 E2". OK opens the title's page (Back returns to the same card,
  refreshed); holding OK moves a card to another status or off the list.
  The top bar's pills sit a little closer together so all ten fit.
- **Status on a title's page in the TV web apps (LG webOS, Samsung Tizen and
  the Xbox / `/tvapp/` app).** Movies, shows, seasons, episodes, albums,
  artists, audiobooks and books get a button after ♥ Favorite: **Add to
  Watchlist**, or **Status: Watching** (etc.) once set. OK opens a menu of
  Plan to Watch, Watching, On Hold, Completed and Dropped, the current one
  checked, plus **Remove from Watchlist**; Back closes it. The status loads
  without holding up the page, and Play keeps the first focus. Uses the
  per-item `/api/v1/items/{id}/watch-status` endpoints; no server change.
  The Watchlist screen's empty text now points here.
- **Watchlist on Roku.** A **Watchlist** button in the Roku home header
  (between Favorites and History) opens one row per status — Plan to Watch,
  Watching, On Hold, Completed, Dropped — each titled with its count from
  `GET /api/v1/watchlist/counts`; empty statuses are left out. Up to 200
  titles per row, episodes labelled "Show · S1 · E2", and OK opens the
  title's detail page. Not yet tried on a Roku.
- **Watchlist status on Roku.** The Roku detail page of a movie, show,
  season, episode, album, artist, audiobook or book has a **Status** button
  next to Mark watched — "Add to Watchlist", or "Status: Watching" once one
  is set. OK lists the five statuses (the current one marked) plus "Remove
  from Watchlist"; the button changes once the server confirms, and a failed
  save says so and keeps the old status. The Watchlist screen's empty message
  now points there. Uses `GET` / `PUT` / `DELETE /api/v1/items/{id}/watch-status`.
  Not yet tried on a Roku.

### Fixed

- **A Chromecast showed no poster for what it was playing.** The web
  player sent `<server><poster_path>` (no `/artwork/`, no token) and the
  phone app `/artwork/<path>` without a token, but `/artwork` needs auth and
  the receiver has none of the sender's. Both now send
  `/artwork/<path>?w=…&token=<asset token>`; the browser keeps the
  purpose=asset token from its last sign-in or refresh for this (read-only,
  24 h, never used for its own requests).
- **History showed movies and music as blank tiles.** `GET /api/v1/history`
  sent only an item's `thumb_path`, which episodes have and movies and tracks
  don't. `thumb_path` now falls back to the item's poster, then its parent's
  (a track's album), so every app's History shows artwork for them.

## [v2.6.0] — 2026-10-17

The catch-up release: metadata editing with locks, multi-episode files,
editions and multi-part movies, label restrictions, smaller downloads and
optimized versions, studio pages, instant mix, OPDS, import from Plex /
Jellyfin / Emby, and every app updated for them. Burned-in picture
subtitles, audio passthrough on remux, Dolby Vision for apps that decode
it, casting any video to a Chromecast, and a Watchlist page. The server's
play decision becomes final on every app. Release note:
[docs/releases/v2.6.0.md](docs/releases/v2.6.0.md).

### Upgrade notes (from v2.5.x)

- **Migrations 00037–00057** (there is no 00053) apply at startup with
  `AUTO_MIGRATE=true` (otherwise run the migrate step first; `/health/ready`
  stays unready until they are applied).
- **The next library scan does one-off work** without re-probing files: an
  episode filed as `E01` whose file is `E01-E02` becomes one multi-episode
  item, movie editions and multi-part movies are read from file names,
  featured-artist credits are read, and photos are regrouped (they are now
  told apart by file, not by name; RAW + JPEG pairs become one photo).
- **Four settings now apply that used to be ignored.** Check them if they
  were ever set: a library's metadata language (applies on the next metadata
  refresh), a user's concurrent-stream limit (now counts direct plays), a
  user's quality profile (now converts anything above it, at home too) and
  Settings ▸ Transcode's encoder tuning (NVENC preset, tune, rate control and
  peak-bitrate ratio, which now override `TRANSCODE_NVENC_*` /
  `TRANSCODE_MAXRATE_RATIO` on the server itself).
- **`GET /media/stream` follows the play decision.** A video the server would
  convert for the viewer (over the internet limit from outside the home
  network, or over their quality profile) is refused with 409
  `TRANSCODE_REQUIRED`, and a damaged one with 422. Current apps never hit
  this; older app builds that decide on their own, and casting, lose the
  original file for those viewers. Music, audiobooks, books and downloads are
  not affected.
- **The internet streaming limit needs the viewer's real address:** behind a
  reverse proxy or Cloudflare, set `TRUSTED_PROXIES`, or every viewer looks
  local and the limit never applies.
- **New optional environment variables:** `ONSCREEN_DVR_POSTPROCESS_SCRIPT`,
  `ONSCREEN_COMSKIP_PATH`, `ONSCREEN_COMSKIP_INI`, `DOWNLOAD_MAX_CONVERSIONS`,
  `DOWNLOAD_CACHE_TTL_HOURS` and `DOWNLOAD_CACHE_MAX_GB`. New optional keys
  in Settings ▸ General: OMDb and fanart.tv.
- **Upgrade remote transcode workers too.** Burned-in picture subtitles
  and TrueHD / DTS passthrough only go to workers that advertise them (v2.6
  workers do; the embedded worker always does). With only older remote
  workers, a burn-in request is refused with 503 `BURN_IN_UNAVAILABLE` and
  TrueHD / DTS keep being converted to AAC.
- **New tasks off until enabled:** music loudness analysis, photo face
  detection, chapter-thumbnail backfill and automatic subtitle download.
- **Built with Go 1.26.9 and x/net 0.60.0** (GO-2026-6612, -6613, -6617).

### Added

- **Cast any video to a Chromecast from the web player and the Android phone
  app.** The Cast button now shows for every playable video (and audio
  file), not only MP4s the Chromecast plays as-is. A file it can't play
  itself is remuxed or
  transcoded into an HLS stream sized for the device (1080p H.264 for a
  Chromecast or Chromecast HD; 4K HEVC / HDR for an Ultra, Google TV or
  SHIELD, falling back to 1080p if the TV refuses it). It still uses
  Google's built-in receiver: there is nothing to register or install.
  Resume and seeking work, progress is saved under the Chromecast's name in
  Now Playing, the selected subtitle goes along (text tracks side-loaded,
  picture subtitles burned in), and stopping the cast picks up locally where
  the TV left off. Server side: the HLS playlist, segment, static-ladder and
  subtitle routes answer CORS for the receiver (`Access-Control-Allow-Origin:
  *`, token-in-URL only, never credentials); transcode-start returns
  `segment_format` (`ts` / `fmp4`); subtitle routes take `?start_ms=` to
  rebase cues onto a stream that starts mid-title, and
  `/media/subtitles/{fileId}/external/{subId}` serves an external subtitle
  with the file's stream token; every session's playlist is now `EVENT`
  (re-encodes were a live playlist, which some players start at its edge).
  The phone app does the same through its Cast button, with a cast panel
  (position, seek, play/pause, stop) in place of the local controls; casting
  carries on after the player screen closes. Picking a device in the phone's
  Cast button sends what's playing straight away (it only connected before),
  and the button no longer crashes the app. Phone casting tested on a Google
  TV (resume, seek, subtitles); the web sender not yet tried on hardware.
- **Watchlist page.** A new **Watchlist** link in the web sidebar lists
  everything you've given a status — Plan to Watch first, then Watching, On
  Hold, Completed and Dropped, one tab each with a count — with a type filter,
  a title sort and endless scrolling. Each card's ⋯ menu moves it to another
  list or removes it. Behind it, `GET /api/v1/watchlist` (`status`, `type`,
  `sort`, `limit` / `offset`) and `GET /api/v1/watchlist/counts` list your own
  statuses, scoped like every other list: only libraries you can open, nothing
  above your rating ceiling or hidden by your label restriction, never a
  deleted item, and a profile sees only its own. The native apps pick it up
  in a later release.
- **Dolby Vision for apps that decode it.** The capability profile's `dovi`
  key takes a profile list (`dovi=5:8.1:8.4`, plus `7`; `dovi=1` means
  `5:8.1:8.4`). A file whose profile the app lists plays as Dolby Vision:
  profile 5 is no longer refused for it, the HDR tone-map check is skipped,
  and a remux keeps the Dolby Vision configuration record and per-frame
  metadata (fMP4 segments, `-strict unofficial`; `dvh1` sample entry for
  profile 5). Profile 7 for an app that takes only 8.1 is converted to 8.1
  while remuxing, on workers whose ffmpeg has OnScreen's `dovi_rpu`
  conversion patch (the GPU image; worker feature `dolby_vision_p7_to_p81`);
  elsewhere it plays from its HDR10 base layer as before. Profile 5 is never
  re-encoded (it would come out green): an app that lists it but would need a
  re-encode gets `unsupported` / 415 `DOLBY_VISION_UNSUPPORTED`. Apps that
  don't send `dovi` are unchanged.
  - **Android TV / Fire TV** sends `dovi` when the box has a hardware Dolby
    Vision decoder for the profile and the connected screen reports Dolby
    Vision (the Fire TV 4K Max: `5:8.1`, plus `8.4` on an HLG screen; it
    has no profile 7 decoder, so profile 7 is converted for it).
  - **LG (webOS)** sends `dovi` when the TV reports Dolby Vision
    (`tv.config.supportDolbyHDRContents`) and its MSE accepts `dvh1` (`5`,
    `8.1:8.4`).
- **Your own sign-in settings on the Account page, for every user.** Change
  your password (new `PUT /users/me/password`: needs the current one, ends
  your other sign-ins; `GET /users/me/preferences` gains `has_password`), turn
  two-factor sign-in on or off, pick preferred audio and subtitle languages,
  and link Last.fm, ListenBrainz or Trakt
  (`/account/scrobble`; `/settings/scrobble` redirects there). These were only
  under Settings, which only admins can open.
- **Audio passthrough on remux.** A client that bitstreams audio to a receiver
  can say so in its capability profile (`audioPassthrough=ac3:eac3:truehd:dts`
  in `X-Client-Capabilities`), and the server then keeps that audio intact:
  - the play decision ignores the client's channel cap for those codecs, so a
    7.1 TrueHD or DTS-HD track plays as-is instead of being downmixed to 5.1
    AAC;
  - a remux copies the track, TrueHD and DTS included; those two are packaged
    as fMP4 segments, since MPEG-TS can't carry them to a player;
  - opt-in: clients that don't send the key decide exactly as before. Full
    transcodes keep AAC audio.
  - Item details' `audio_streams[]` gain `profile` ("DTS-HD MA", "Dolby
    TrueHD + Dolby Atmos", "Dolby Digital Plus + Dolby Atmos") for track labels,
    on files scanned or re-probed from now on.
  - Workers advertise the new `audio_passthrough` feature; until one does, a
    TrueHD/DTS remux converts to AAC as before.
- **Burned-in picture subtitles.** PGS, VobSub, DVB and XSUB subtitles can now
  be painted into the picture during a transcode, for players that can't show
  them and when there's no OCR'd text version.
  - `POST /items/{id}/transcode` takes `burn_subtitle_stream_index` (the
    stream's `subtitle_streams[].index`) and answers with
    `burned_subtitle_stream_index`. A burn-in is always a full re-encode on a
    single rendition (`video_copy` and the adaptive ladder are set aside).
  - Refusals, before the current stream is touched: 400
    `INVALID_SUBTITLE_STREAM` (not a subtitle stream of the file),
    `SUBTITLE_NOT_IMAGE` (a text track; the player renders those) and
    `BURN_IN_UNSUPPORTED` (a movie stored in parts); 503
    `BURN_IN_UNAVAILABLE` when no worker can do it yet.
  - Item details' `subtitle_streams[]` gain `image_based` and
    `text_version_id` (the stream's OCR'd text version, when there is one).
  - The subtitle timing offset applies (the stream's, else its OCR version's).
    The overlay runs on the CPU, so a burn-in turns off the all-GPU-memory
    decode/scale paths for that session and counts as a heavier job. An HDR
    source still tonemaps on the GPU where `tonemap_cuda` works (NVIDIA): only
    the finished SDR frame comes down for the overlay, which keeps an HDR
    burn-in well ahead of real time (CPU tonemapping held it at about 1×).
    Output is 8-bit. Workers advertise the `burn_in_subtitles` feature, and
    only those get burn-in jobs.
  - ffmpeg reads the subtitle stream through a second input of the same
    file. Read through the video's own input, a film with an audio track ran
    at about real time with no lead and stalled on the Fire TV (0.34× in a
    live session); with the second input the same film runs at about 1.9×.
- **Manual collections.** Admins can now make their own movie and TV
  collections ("Halloween night", "Best Picture winners") and share them with
  everyone, alongside the automatic TMDB film series.
  - **Making one:** "New Collection" on the Collections page (admins only),
    then add titles with "Add to Collection" on a movie or show page, or select
    several in a movie or show library and choose "Add to collection…".
  - **Arranging it:** a collection lists its titles in a custom order (move
    earlier or later), by release date or by title, and an admin can pick which
    member's poster is its cover.
  - **Who sees it:** a collection appears to anyone who can see at least one
    title in it. Library access and content-rating limits apply, its count
    covers only the titles they can see, and its cover comes from one of
    those. A kid profile never sees a collection of grown-up films.
  - **Where it appears:** the Collections page, the library's Collections tab
    (now in show libraries too), and "In collections" links on movie and show
    pages. The TV and phone apps already list every collection in their
    Collections row, so they show these without an update.
  - **API (all additive):**
    - `POST /collections` takes `type: "manual"` (admins only) and
      `item_order`;
    - `PATCH /collections/{id}` takes `item_order` and `poster_item_id`;
    - `POST /collections/{id}/items` takes `media_item_ids` for up to 500 at
      once;
    - `PUT /collections/{id}/items/order` reorders;
    - collections gain `item_order`, `poster_item_id` and `item_count`;
    - movie and show details gain `collections`.

    Without a `type`, `POST /collections` still creates a private playlist.
  - **Migration 00037** adds the type and two columns.
- **Collections on the home screen, smart collections, covers and NFO
  import.**
  - **Show on Home:** an admin can put a collection on everyone's home screen.
    Each person sees it as a row of the titles they can see, and can hide or
    move it under "Customize rows" like any other row. It appears in the web
    app and in the Android TV, Samsung, LG and Xbox apps.
  - **Smart collections:** a collection can follow rules instead of being
    picked by hand: movies and/or shows, a genre, a year range, a minimum
    rating and some libraries ("80s horror", "Animated films rated 8+"). It
    refreshes when the rules are saved and every hour, so new and re-matched
    titles join on their own. "Stop smart updates" keeps the current titles
    and makes it hand-picked again.
  - **Uploaded covers:** an admin can upload any image as a collection's
    cover instead of picking a member's poster. It's stored re-encoded, at
    most 1000×1500.
  - **Kodi NFO import:** a movie library can import the `<set>` (and,
    optionally, `<tag>`) groupings of its movie.nfo files as collections
    (library settings, "Collections from NFO files", off by default). Movies
    join as they're scanned. Turning it on imports the existing ones, and a
    daily task picks up edited NFO files. A set that's a TMDB film series OnScreen
    already groups automatically is skipped, so a Radarr library (which writes
    every movie's TMDB collection as its set) doesn't get duplicates. Radarr's
    tags are TMDB plot keywords, so "Sets and tags" is for hand-tagged libraries.
  - **API (all additive):**
    - `POST` / `PATCH /collections` take `rules` (null makes a collection
      hand-picked again) and `promoted`;
    - `GET`, `POST` and `DELETE /collections/{id}/poster`;
    - collections gain `rules`, `promoted`, `poster_version` and `source`;
    - the hub gains `collection_rows`, with layout keys
      `collection:<id>`;
    - libraries gain `nfo_collections`.

    Hand edits of a smart collection's titles are refused with a 400.
  - **New tasks:** "Refresh smart collections" (hourly) and "Import NFO
    collections" (daily).
  - **Migration 00038** adds the columns, the cover table and the library
    setting.
- **Podcast subscriptions.** A podcast library can follow podcasts' RSS
  feeds, and podcast libraries can be created again (they were hidden in
  v2.5 while podcasts were local files only).
  - **Subscribing:** on a podcast library's new "Subscriptions" tab (admins
    only), paste a feed URL and choose whether new episodes download on their
    own, how many of the newest episodes to download now, and how many
    downloaded episodes to keep.
  - **Episodes** download into the library's folder, one folder per podcast
    (`<podcast>/<date> <episode title>.mp3`), and are scanned into the
    library with the feed's titles, show notes and publish dates, and the
    feed's artwork as the podcast's poster. Every app plays them the way it
    plays any podcast file, so no app needed an update.
  - **Each feed's page** lists its episodes with Download, Play and Delete
    file, and has its settings, "Check for new episodes" and Unsubscribe (with
    or without deleting the downloads). The podcast's page in the library
    links to it.
  - **Refresh:** a new hourly task, "Refresh podcast feeds", checks every
    feed (an unchanged feed answers 304), downloads new episodes where
    auto-download is on, and deletes downloads past each feed's "Keep" limit,
    oldest first. A deleted episode isn't downloaded again automatically.
  - **Limits:** audio episodes only (video podcasts are skipped); feeds and
    episodes on a private or local network address are refused; at most 2
    downloads run at once, 10 per refresh on their own, 4 GB per episode.
  - **API (all additive):** `/admin/podcast-feeds` (list, subscribe, get,
    update, unsubscribe, refresh, episodes) and
    `/admin/podcast-episodes/{id}/download` (`POST` downloads, `DELETE`
    deletes the file); item children gain `originally_available_at`.
    Subscribing, changing and unsubscribing are audited.
  - **Migration 00039** adds the feed and episode tables.
- **Import from Plex, Jellyfin or Emby.** Settings ▸ Integrations ▸ Import
  brings users' history over from the server they're leaving: watched state,
  resume points, ratings, favorites, playlists and play history.
  - **How:** connect with the old server's URL and token (Plex: the owner's
    X-Plex-Token; Jellyfin and Emby: an API key), choose the OnScreen user
    each of its accounts goes to (users with the same name are suggested),
    pick what to bring, then **Preview** to see how much matches before
    **Import** writes anything. The token is used for the run only and never
    stored.
  - **Matching:** by file path first (the two servers usually read the same
    files, wherever each mounts them), shows by their folder, then by TMDB,
    TVDB or IMDb ID, and episodes by their show plus season and episode
    number. A sample of what didn't match is listed.
  - **Safe to repeat:** anything a user already did on OnScreen is kept (a
    newer watched or unwatched mark, a rating, a favorite), and running an
    import again writes only what's new.
  - **Play history** counts toward play counts, Trending and analytics.
    Plays older than the server's watch-history retention are left out (their
    items still count as watched), and imported plays get their own monthly
    history partitions, so retention removes them on time. Imports write
    straight to the database, so they don't send scrobbles to Trakt, Last.fm
    or ListenBrainz.
  - **What each server shows:** a Plex server shows resume points, ratings
    and playlists only for its owner; its other accounts bring their play
    history and what it shows they watched. Plex has no favorites. Jellyfin
    and Emby keep no log of individual plays, so each watched item brings one
    play on its last-played date, and a playlist shared with everyone comes
    over for each user who could see it.
  - **Tested** against Jellyfin 12.2 and Emby 4.10 servers. The Plex reader
    is tested against a fake modelled on its API, not a live server.
  - **API (all additive):** `/admin/imports/connect`, `/admin/imports` (list,
    start), `/admin/imports/{id}` and `/admin/imports/{id}/cancel`. Starting
    and cancelling an import are audited.
  - **Migration 00040** adds the import job table.
- **IMDb, Rotten Tomatoes and Metacritic scores.** Movie and show pages show
  a title's IMDb rating (linking to its IMDb page), Rotten Tomatoes critics'
  score and Metascore.
  - **Setup:** paste a free OMDb API key into Settings ▸ General
    (omdbapi.com). Without one nothing is fetched.
  - **Refresh:** a new hourly task, "Refresh IMDb, Rotten Tomatoes and
    Metacritic scores", looks titles up by IMDb ID: missing scores first,
    then this year's and last year's titles weekly and older ones every 90
    days.
  - **Daily limit:** Settings ▸ General ▸ "OMDb Daily Lookups" (1,000 by
    default, a free key's limit; raise it for a paid key, lower it if
    another app shares the key). Every run, manual ones included, counts the
    lookups made in the last 24 hours and uses only what's left, so the key
    never goes over its limit in any 24 hours; a large library fills in over
    a few days. Runs never overlap. A run stops if OMDb says the limit is
    used up anyway, and fails when OMDb refuses the key.
  - **Shared:** copies of a title in several libraries share one lookup.
    Episodes have no scores.
  - **API (additive):** `ItemDetail.external_ratings` (`imdb_id`,
    `imdb_rating`, `imdb_votes`, `rotten_tomatoes`, `metacritic`) on movies
    and shows; `omdb_api_key` (masked, encrypted at rest) and
    `omdb_daily_limit` in `/settings`.
  - **Migrations:** 00041 adds the `external_ratings` table; 00042 moves an
    install that already seeded the task daily to hourly (a changed schedule
    is kept).
- **Edit metadata, with field locks.** Admins can correct a movie's, show's,
  season's or episode's details and keep them that way.
  - **Editing:** "Edit Metadata" on a movie, show or season page, or the
    pencil on an episode's row, opens an editor for the title, sort title,
    original title, year, release date, content rating, critic rating,
    genres, tagline and summary.
  - **Locks:** every field you change is locked. Scans, metadata refreshes,
    re-enrichment and Fix Match leave a locked field as it is (Fix Match still
    re-points the item at the new match, and fills in the fields you haven't
    locked). Each field shows a padlock; click a locked one, or "Unlock all",
    to hand it back to the next refresh. A locked title keeps its sort title
    too.
  - **Content rating:** an edited rating takes effect at once for profiles
    with a content-rating limit, home screen included. A rating OnScreen
    doesn't recognise counts as the most restrictive.
  - **Home videos and photos** keep their editor, and a title change still
    renames the file. Their edited title, summary and date are now locked as
    well, so a rescan no longer replaces a photo's corrected date with the
    camera's.
  - **API (additive):** `PATCH /items/{id}` also takes `sort_title`,
    `original_title`, `tagline`, `year`, `rating`, `content_rating`, `genres`
    and `unlock_fields`, and answers 409 when another item in the library
    already has the title and year. Item details gain `sort_title`, `tagline`
    and `locked_fields`.
  - **Migration 00043** adds `locked_fields` to media items.
- **Multi-episode files.** A file holding two or more episodes —
  `S01E01E02`, `S01E01-E02`, `S01E01-02`, `S01E01.S01E02`, `1x01-1x02`,
  `1x01-02` — is now one episode covering episodes 1–2, instead of episode 1
  with episode 2 shown as missing.
  - **Shown as** "E1–E2" / "Episodes 1–2" on the episode list, the player and
    Next Up. Its title joins the episodes' titles ("Pilot / The Train Job",
    or "The Best of Both Worlds (1–2)" for a two-parter); summary, still and
    air date come from the first episode, the runtime from the file.
  - **Counted as every episode it covers:** season and show episode counts,
    unwatched badges, a season request's "episodes you have", Next Up and the
    player's next episode (after E01–E02 comes E03).
  - **Existing libraries:** the next scan converts an episode filed as E01
    whose file is really E01–E02 and re-fetches its metadata; no second item
    is created. The anime-style unspaced `Title-01-02` is still not read as
    a range.
  - **API (additive):** episodes gain `index_end` (detail and children),
    Next Up items and `GET /items/{id}/up-next` gain `episode_number_end`;
    omitted for a single episode.
- **Episode order: aired, DVD or absolute.** A show whose files are numbered
  by their DVD release, or by one running number ("Show - 123"), can use
  that order to get the right titles and summaries.
  - **Where:** Settings for a TV, anime or cartoons library sets the default
    ("Aired" unless changed); an admin can override it on a show's page.
    Changing either refreshes the affected shows' episode metadata in the
    background.
  - **Providers:** TMDB episode groups for shows matched on TMDB, TheTVDB's
    DVD and absolute orders for shows matched on TheTVDB. A show whose
    provider has no such order keeps aired order (noted in the server log).
    Anime libraries keep their current behaviour unless you change the order.
  - **API (additive):** `PUT /items/{id}/episode-order` (admins, audited);
    `episode_order` on libraries (create, update, read) and on show details.
- **Migration 00044** adds `media_items.index_end`,
  `media_items.episode_order` and `libraries.episode_order`.
- **Movie editions and multi-part movies.**
  - **Editions:** a movie file's name can now say which edition it is, and
    the version picker shows that name ("Director's Cut", "Extended") instead
    of only its resolution and codec. Read from Plex's
    `{edition-Director's Cut}` (in the file or folder name), Jellyfin's
    `Movie (2010) - Director's Cut.mkv` suffix (also `- 1080p`), and common
    tags after the year: `[Extended]`, Director's Cut, Extended Edition,
    Theatrical, Unrated, Uncut, IMAX, Remastered and a few more. A label only:
    which movie a file belongs to doesn't change.
  - **Multi-part movies:** `Movie (2001) cd1.mkv` + `cd2.mkv` (also
    `part`, `pt`, `disc`, `disk`, `dvd` with a number, or `-a`/`-b`) now play
    as one movie, start to finish, on every client — the server joins the
    parts into one stream, so apps that know nothing about parts play all of
    it. Its length is the parts added up, and resume, seeking, thumbnails and
    progress all use that combined timeline. A lone `cd1` plays like any
    other file. Part words in a title ("Dune Part One (2021)", "Harry Potter
    and the Deathly Hallows Part 1 (2010)") are left alone, and TV episodes
    are never stacked. Each part still downloads on its own.
  - **Playback:** a multi-part movie is never direct-played (one file is
    one part); it is remuxed, or transcoded when the video needs it. Fleet
    workers without shared storage pull each part over HTTP with its own
    stream token. The server writes the list of parts itself; a file in a
    library that is such a list is still refused.
  - **Existing libraries:** the next scan fills in editions and parts from
    the file names without re-reading the files. A yearless
    `Kill Bill part1.mkv` in `Kill Bill (2003)/` needs its file touched (or
    re-added) to move into the right movie.
  - **API (all additive):** item `files[]` gain `edition` and `part_index`
    (both omitted when empty; `part_index` only for a real multi-part
    movie); a multi-part movie's `duration_ms` is the parts' total.
    `POST /items/{id}/transcode` and `/playback-decision` with any part's
    `file_id` start, or judge, the whole movie.
  - **Migration 00045** adds `media_files.edition` and `part_index`.
- **Separate limits for home and internet streams.** Streams to people
  outside your home network can be held to a lower bitrate than streams at
  home.
  - **Server-wide:** Settings ▸ Transcode ▸ "Internet streaming limit" (off by
    default). Takes effect on the next stream, no restart.
  - **Per user:** an "Internet streaming cap" next to the existing bitrate cap
    under Users ▸ Streaming caps. A remote stream gets the lowest of the
    user's cap, their internet cap and the server's limit; a stream at home
    gets only the user's cap, as before.
  - **What happens:** a remote viewer whose video is over the limit is
    transcoded down to it instead of playing the original file, at a
    resolution that suits the rate (720p at 4 Mbps, 1080p from 8 Mbps), and
    the adaptive quality ladder never goes above it. Now Playing shows "Over the
    internet streaming limit". Music, audiobooks and podcasts are never
    limited.
  - **What counts as home:** loopback, private (192.168.x, 10.x, 172.16–31.x,
    fd00::/8), link-local, this server's own IPv6 network, and
    100.64.0.0/10, so Tailscale devices count as home. "Additional local
    networks" adds more (a VPN or office subnet). The address is the one the
    server trusts for the audit log, so behind a reverse proxy or Cloudflare
    it is the viewer's real address only if the proxy forwards it (the bundled
    nginx sample and cloudflared do) AND its address is in `TRUSTED_PROXIES`.
    Otherwise every viewer looks like the proxy, which is usually on your
    network, so the internet limit never applies.
  - **Not covered:** a client that fetches the original file directly
    without asking the server how to play it (older app builds) isn't held to
    the limit. All current OnScreen apps ask first.
  - **API (additive):** the play decision gains `reason`
    (`remote_bitrate_limit` / `user_bitrate_limit`) and `max_bitrate_kbps`;
    `/settings/transcode-config` gains `remote_max_bitrate_kbps` and
    `local_networks`; `/users/{id}/streaming-limits` gains
    `max_remote_stream_bitrate_kbps` (leaving it out keeps the stored value).
    Changing a user's caps is now audited.
  - **Migration 00046** adds the per-user internet cap.
- **Signed-in devices.** Settings ▸ Security (admins) or the new Account page
  (everyone else) lists every device signed in to your account (browser or app, its address, when it signed in and was last
  used) with a "Sign out" button on each and "Sign out everywhere else". On
  the Users page an admin can see and sign out any user's devices, or sign
  them out everywhere.
  - Signing out one device leaves the others alone: no other device has to
    sign in again or loses its stream. The signed-out device's refresh stops
    working at once and its other credentials on its next request.
  - "Last used" is updated when a device renews its sign-in (about hourly
    while it's in use), not on every request. Devices signed in before this
    update show no name until their next renewal; their old credentials
    expire on their own within a day.
  - Each sign-out is audited (`auth.session_revoked`; the bulk ones as
    `auth.sessions_revoked`).
  - **API (additive):** `GET /users/me/sessions`, `DELETE /users/me/sessions`
    (everywhere else), `DELETE /users/me/sessions/{id}`, and for admins
    `GET`/`DELETE /users/{id}/sessions` and
    `DELETE /users/{id}/sessions/{sid}`. Clients may send an `X-Device-Name`
    header when signing in to name themselves.
- **Labels, and restricting users by label.** Admins can label titles
  ("kids", "horror", "dad's films") and limit what each user sees by them, like
  Plex's label restrictions or Jellyfin's blocked and allowed tags.
  - **Labelling:** a "Labels" box on every title's page (admins only — other
    users never see labels), or select titles in a movie, show or photo
    library and choose "Add label…" to add or remove labels on up to 500 at
    once. Labels are lower-cased, and suggestions come from the labels already
    in use. They are separate from the keywords metadata providers supply, so
    a metadata refresh never adds or removes one.
  - **Restricting:** Settings ▸ Users ▸ a user's menu ▸ "Labels…". "Never
    show titles labelled" hides anything carrying one of those labels; "Only
    show titles labelled" hides everything that carries none of them. A label
    on a show covers its seasons and episodes, and one on an artist or album
    covers its tracks; a label on a single episode hides just that episode.
    A user's managed profiles get the same restriction (new profiles inherit
    it), and saving signs them out so it applies straight away. "View as user"
    shows the result.
  - **Where it applies:** everywhere the content-rating limit does — title
    pages, episode lists, library browsing and its counts, search, the home
    screen (Continue Watching, Next Up, Plan to Watch, Recently Added,
    Trending, collection rows), collections and playlists (counts and covers
    included), favorites, history, people pages, photos and albums, posters,
    seek-bar thumbnails, subtitles, playback, downloads, Up Next, the upcoming
    calendar and the "already in your library" marks on requests. A hidden
    title looks exactly like one above the user's rating limit. Phone and TV
    apps need no update.
  - **API (all additive):** `GET /labels`, `POST /labels/apply`,
    `GET`/`PUT /items/{id}/labels`, `PUT /users/{id}/label-restrictions`;
    `labels` on item detail for admins; `blocked_labels` and `allowed_labels`
    on the admin user and profile lists.
  - **Migration 00047** adds `media_items.labels` and the users'
    `blocked_labels` / `allowed_labels`.
- **DVR post-processing.** Settings ▸ Live TV ▸ "Recording post-processing"
  decides what happens to a recording once it finishes. Everything is off by
  default, on new installs and upgrades alike, so recordings stay exactly as
  the tuner wrote them until you opt in. Turning on remux is recommended.
  - **Remux** copies the recording into MKV (or MP4) with clean timestamps, so
    seeking is reliable. It is fast and doesn't change quality.
  - **Transcode to save space** re-encodes the video to H.264 or HEVC at a
    high, balanced or small-file quality, and copies the audio. It uses the
    hardware encoders your Transcode settings allow and falls back to
    software.
  - **Delete original** removes the tuner's file, but only after the new file
    has been checked: same length to within 2%, video and audio both present.
    When it is off, the original moves to a hidden `.dvr-originals` folder
    beside the recording. The scanner skips that folder, and the retention
    clean-up removes the kept file with its recording.
  - **Post-processing script:** set `ONSCREEN_DVR_POSTPROCESS_SCRIPT` to a
    program to run after each recording, with the recording's path as its
    only argument. It runs with a 30-minute timeout and its output goes to
    the server log. It can only be set in the environment, never in the web
    UI; the settings page shows it read-only.
  - **How it runs:** recordings are processed one at a time, after the
    capture finishes. The Recordings page shows "Queued for processing",
    "Processing", "Processed" or "Processing failed", with a note saying what
    was done or what went wrong. A failure leaves the original recording
    untouched and playable, and admins can re-run it from the recording's
    "⋯" menu. The library item keeps its watch state: the same file entry
    points at the new file, so a rescan doesn't add a duplicate.
- **Commercial detection and Skip Ad for DVR recordings.** Set
  "Commercials" in the same panel to "Mark" to get a "Skip Ad" button
  during ad breaks in the web player, or "Cut" to remove the breaks from the
  file during post-processing (Cut needs remux or transcode).
  - **Detection:** if `ONSCREEN_COMSKIP_PATH` (and optionally
    `ONSCREEN_COMSKIP_INI`) is set, OnScreen runs comskip. Otherwise a
    built-in detector looks for black frames that coincide with silence. It
    is deliberately conservative: a break must hold at least two ads, last
    30 seconds to 6 minutes, and sit between at least 3 minutes of programme
    on each side, so some ads are missed rather than any of the show
    skipped.
  - **On demand:** admins can run "Detect commercials" on any finished
    recording from its "⋯" menu on the Recordings page. This never changes
    the file.
  - **Player:** "Skip Ad" (or the S key) jumps to the end of the break.
    "Always skip ads" next to the button skips breaks automatically in that
    browser. It is off by default and separate from "Always skip intros".
  - **Apps:** breaks are a new `commercials` list on each file of the item,
    not part of the intro/credits `markers`. The TV and phone apps show every
    marker as Skip Intro or Skip Credits, so putting ads there would have
    mislabelled them. Current apps ignore the new field.
  - **API (all additive):** `GET`/`PUT /settings/dvr`;
    `POST /tv/recordings/{id}/detect-commercials` and
    `POST /tv/recordings/{id}/postprocess` (admins, 202); recordings gain
    `postprocess_status` and `postprocess_message`; item files gain
    `commercials`.
  - **Migration 00052** adds the post-processing job table and
    `commercial_markers`.
- **Lower-quality downloads.** The Download button on a movie or episode is
  now a menu: Original (the file as it is, as before) or a smaller copy at
  1080p 8 Mbps, 720p 4 Mbps, 720p 2 Mbps or 480p 1 Mbps, for a phone, a
  tablet or a slow connection.
  - **How it works:** the server converts the file once into an MP4 (H.264
    video, stereo AAC audio, text subtitles kept, nothing burned in) and the
    page shows its progress, then starts the download. A multi-part movie
    becomes one file. The same file at the same quality is converted once and
    shared: anyone else who asks for it gets the finished copy straight away.
  - **Your downloads:** the Account page lists them with their progress, a
    download link once ready, and a remove button (which also stops a
    conversion nobody else is waiting for).
  - **Limits:** a user's streaming limit, and from outside the home network
    the internet limit, bounds the qualities offered — someone held to 4 Mbps
    can't take the 1080p 8 Mbps copy — checked again when the file is
    fetched. The server-wide downloads switch, library access, content
    rating, labels, watch limits and admin stops apply as they do to the
    original. Up to three of a user's downloads can be in preparation at once.
  - **Server load:** conversions run on the server with its configured
    H.264 encoder (hardware where Settings ▸ Transcode says so, falling back
    to software), one at a time by default (`DOWNLOAD_MAX_CONVERSIONS`, at
    most 2), and each takes one of the embedded worker's transcode slots, so
    they wait while playback is using every slot.
  - **Cache:** finished files live in `CACHE_PATH/downloads` and are kept 7
    days after they were last asked for or fetched
    (`DOWNLOAD_CACHE_TTL_HOURS`). The new hourly "Converted download cache
    cleanup" task evicts expired files, then the least recently used ones
    while the cache is over `DOWNLOAD_CACHE_MAX_GB` (50 GB).
  - **API (all additive):** `GET`/`POST /items/{id}/downloads`,
    `GET`/`DELETE /downloads/{job}`, `GET /downloads/{job}/file` (Range,
    asset-token auth) and `GET /users/me/downloads`. The original download is
    unchanged; phone and TV apps need no update.
  - **Migration 00051** adds `download_conversions` and `download_jobs`.
- **Chapter thumbnails.** The player's chapter menu shows a picture of each
  chapter, and a title's page shows a chapter strip you can start playing
  from.
  - Thumbnails are made with the seek-bar thumbnails (trickplay): one frame
    per chapter, a few seconds in, from the file the seek-bar thumbnails come
    from. New and regenerated seek-bar thumbnails get them automatically;
    for titles that already have seek-bar thumbnails, enable the new
    "Chapter thumbnails backfill" task (off by default) or regenerate the
    library's seek-bar thumbnails. Nothing is regenerated at startup.
  - They are served beside the seek-bar thumbnails with the same access
    checks (library, content rating, labels).
  - **API (additive):** each chapter in the item detail gains `thumb_url`
    when it has a thumbnail.
  - **Migration 00051** adds `trickplay_status.chapter_thumb_count`.
- **Clear logos, banners and disc art.** Movie and show pages show the
  title's clear logo in place of the text title, and album pages show the disc
  peeking out behind the cover.
  - **Where art comes from:** first, files already next to your media with
    Kodi's names (`clearlogo.png` / `logo.png`, `banner.jpg` / `banner.png`,
    `disc.png` / `discart.png` / `cdart.png`); then fanart.tv (movies by TMDB
    or IMDb id, shows by TheTVDB id, artists and albums by MusicBrainz id),
    preferring the library's language, then English, then language-free art,
    then the most liked; then, for anime, AniList's banner. Downloads are saved
    next to the media under the same Kodi names.
  - **Setup:** paste a free personal fanart.tv API key into Settings ▸ General.
    Without one, local files and AniList banners are still used.
  - **When:** as titles are enriched, refreshed or re-matched with Fix Match
    (which replaces the old title's art). Answers, including "none", are
    remembered for a few hours, so a refresh doesn't ask fanart.tv again.
  - **API (additive):** `logo_path`, `banner_path` and `disc_art_path` on item
    details, served through `/artwork/` like posters (fetch the logo without
    `?w=`, which turns its transparent background black); `fanarttv_api_key`
    (masked, encrypted at rest) in `/settings`.
- **Studio and network pages.** Movie pages list the studios that made the
  film and show pages the networks that aired it, each linking to a page of
  its titles, sortable by title, year, rating or date added. Movie and TV
  libraries get "Browse studios" / "Browse networks" next to genres and years.
  - **Where they come from:** TMDB's production companies (movies) and
    networks (shows); an NFO file's `<studio>` tags win when it has any.
  - **Who sees what:** a studio appears only through titles the user can see —
    library access, the content-rating limit and label restrictions apply to
    the lists, the counts and the studio pages.
  - **API (all additive):** `GET /studios` (`library_id`, `kind`),
    `GET /studios/{id}`, `GET /studios/{id}/items` (paged and sorted like
    library browse); `studios` on movie and show details.
- **Save metadata as NFO files.** A new library setting, "Save metadata as NFO
  files next to media" (movie and TV libraries, off by default), writes
  Kodi-compatible `<file>.nfo` (or the `movie.nfo` already there),
  `tvshow.nfo` and episode NFO files after a title is enriched, edited or
  re-matched, so Kodi, Jellyfin and Emby see the same details. They carry the
  title, original and sort titles, year, plot, tagline, rating, content rating,
  genres, studios, TMDB / IMDb / TheTVDB ids, season and episode numbers, air
  date and runtime.
  - **Safe:** files are written whole (through a temporary file), only NFO
    files OnScreen wrote — marked `<!-- written by OnScreen -->` — are ever
    replaced, and a read-only media folder just logs a warning. Not available
    with object storage.
  - **No refresh loops:** the folder watcher ignores these writes, and when
    OnScreen reads its own NFO back it takes only the ids from it, so newer
    provider data still lands and locked fields still win.
  - **API (additive):** `save_nfo` on libraries.
- **Migration 00049** adds `media_items.logo_path` / `banner_path` /
  `disc_art_path`, `libraries.save_nfo`, and the `studios` and
  `media_item_studios` tables.
- **Loudness analysis for music without ReplayGain tags.** Tracks whose
  files carry no ReplayGain tags now get volume normalisation too. A new task,
  "Music loudness analysis" (Settings ▸ Tasks, off until you enable it),
  measures each such file's loudness and peak the way ReplayGain 2.0 does
  (EBU R128, −18 LUFS reference, true peak) and stores the track gain, plus
  an album gain worked out from the album's tracks together. Every player
  that already applies ReplayGain picks the values up with no update.
  - **Tags always win:** a file's own ReplayGain tags are never replaced. If
    you later tag a file, the tags take over; if its audio changes, it is
    measured again.
  - **Cost:** each file is decoded once in full on the server (a few seconds
    for a CD-length track). A run measures up to 500 files (set `max_files`
    in the task's config, or `library_id` to do one library) and carries on
    the next night. A silent track gets no gain; a file that can't be
    decoded is skipped until it changes.
  - **Where it shows:** a track's info panel says whether its ReplayGain is
    "From tags" or "Analyzed". API (additive): `replaygain_source`
    (`tags` / `analyzed`) on item files.
  - **Also fixed:** MP3 files' MusicBrainz IDs and ReplayGain tags (stored in
    ID3 `TXXX` frames) were never read; they are now, from the next time the
    file is scanned.
  - **Migration 00048** adds `media_files.replaygain_source` (existing values
    are marked as from tags) and the measurement table.
- **Instant mix and similar artists.** "Instant mix" on a track (its info
  panel), an album or an artist starts about 50 tracks: some of its own
  first, then tracks by similar artists, then others from the same genres —
  never the same artist twice in a row while there is anyone else to play,
  without tracks you rated one star, and shuffled afresh each time.
  - **Similar artists:** a new row on the artist page. Artists come first
    when people who listen to one also listen to the other (from play history
    across all users), then by shared genres; a library with no play history
    yet ranks by genre and era.
  - Library access, content-rating limits and label restrictions apply to
    both, for the artist or track you start from and every suggestion.
  - **API (additive):** `GET /music/artists/{id}/similar`,
    `GET /music/mix?seed=<track, album or artist id>` (each track comes with
    the file to stream and its ReplayGain).
- **Featured artists.** "Jay-Z feat. Rihanna" is now Jay-Z's track with
  Rihanna credited, instead of an artist of its own. Credits come from
  MusicBrainz Picard's multi-valued `ARTISTS` tag when a file has one, and
  otherwise from "feat.", "ft." and "featuring" in the artist or title,
  including "(feat. X)". Only the featured part is split into names, so
  "Earth, Wind & Fire" and "Simon & Garfunkel" stay one artist.
  - **Appears on:** an artist's page lists the albums of other artists it is
    featured on, and the compilation tracks it performs, with the tracks. An
    artist known only from features gets a page of its own; it is matched by
    MusicBrainz ID first, then by name, so it is never created twice.
  - **Existing libraries:** the next scan reads the credits of files it has
    already imported, once, without re-probing them. FLAC files with several
    `ARTIST` fields are now filed under the first (they used to go under the
    last).
  - **API (additive):** `GET /music/artists/{id}/appears-on`.
  - **Migration 00048** adds the track–artist credit table.
- **Personal API keys** for scripts and integrations such as Home Assistant
  and Kometa, which can't sign in the way an app does.
  - **Making one:** Account ▸ API keys (Settings ▸ Security for admins) ▸
    "Create API key". Name it, choose full access or read-only, and optionally
    an expiry. The key (`osk_…`) is shown once with a Copy button; the server
    keeps only a hash of it.
  - **Using it:** send `Authorization: Bearer osk_…` or `X-Api-Key: osk_…`.
    Never in a URL. See [docs/api/api-keys.md](docs/api/api-keys.md).
  - **What it can do:** whatever its owner can, with their libraries, rating
    limit and labels as they are at that moment — a restriction an admin adds
    applies to the key straight away. Read-only keys can only read. No key can
    sign in, refresh, change a password, PIN or 2FA, pair a TV, switch
    profiles, manage devices, keys or users, or touch server settings and
    backups.
  - **Revoking:** "Revoke" stops a key on its next request. Admins see every
    user's keys under Settings ▸ Security ▸ All API keys and can revoke any of
    them. Deleting a user deletes their keys. Creating and revoking keys is
    audited (`api_key.create`, `api_key.delete`).
  - **API (additive):** `GET`/`POST /users/me/api-keys`,
    `DELETE /users/me/api-keys/{id}`, and for admins `GET /admin/api-keys`
    and `DELETE /admin/api-keys/{id}`.
  - **Migration 00050** adds the `api_keys` table.
- **Reading position syncs between devices.** The web book reader (EPUB, CBZ,
  CBR) now saves where you are to the server as you read, and when the page is
  closed or hidden, so another browser opens the book at the same place. If
  another device got further than this one, the reader offers "Continue from
  page 40 on Phone" instead of jumping. Without a connection it falls back to
  the position saved in the browser. Reaching the end marks the book read.
  Audiobooks keep their own playback position as before.
  - **API (additive):** `GET`/`PUT /items/{id}/reading-progress` (a locator,
    such as an EPUB CFI or a page number, a 0–1 progression, the device name
    and a timestamp; the newest position wins).
  - **Migration 00050** adds the `reading_progress` table.
- **Automatic subtitle download.** OnScreen can fetch subtitles from
  OpenSubtitles on its own for new movies and episodes. Off by default; turn
  it on under Settings ▸ General ▸ Automatic subtitle download and list the
  languages you want (e.g. `en,es`).
  - It only downloads a language the file has no subtitle in yet — not
    embedded, not in a file next to the video, not downloaded before. It
    prefers a subtitle made for the exact file (OpenSubtitles hash match),
    then the release name closest to the file's, and can prefer or avoid
    hearing-impaired subtitles.
  - The new "Download missing subtitles" task (Settings ▸ Tasks) runs every
    6 hours, newest titles first. A title with nothing found is tried again
    after 1, 7 and 30 days, then left alone, so nothing is searched over and
    over. When the OpenSubtitles daily download limit is reached it stops for
    the day. Without OpenSubtitles set up it does nothing.
  - Each movie or TV library can turn it on or off for itself, or use its own
    languages, on the library's settings page.
  - **API (additive, admin):** `GET`/`PUT /settings/subtitle-auto-download`
    and `GET`/`PUT /libraries/{id}/subtitle-auto-download`.
  - **Migration 00050** adds the per-library overrides and the
    per-title-and-language attempt log.
- **Playlists to and from other players (M3U).** A playlist page has an
  "Export .m3u8" button, and the Playlists page an "Import playlist" one.
  - **Export** writes an extended M3U with each item's length, artist and
    title. Paths are relative to the library folder, so a player that mounts
    the same music elsewhere can use it; tick "Full server paths" for a
    player on the server itself. Only items you can see are written.
  - **Import** takes an `.m3u` or `.m3u8` file (up to 1 MB and 10,000 lines)
    and makes a new playlist of the lines it can place: by exact path, by path
    relative to a library folder, by the same folders and file name on another
    drive or mount (`M:\Music\…` and `/mnt/music/…` match, in any letter
    case), and finally by the artist and title on the `#EXTINF` line. It then
    lists the lines it couldn't place. Web addresses in a playlist are never
    opened, and only items you can see are matched.
  - **API (additive):** `GET /playlists/{id}/export.m3u8` (`?paths=absolute`
    for server paths) and `POST /playlists/import` (multipart `file`, optional
    `name`). No migration.
- **Read your books in an e-reader app (OPDS).** Book libraries are published
  as an OPDS catalog at `/opds/`, so KOReader, Moon+ Reader, Panels, Chunky
  and other reader apps can browse, search and download your EPUB, PDF, CBZ
  and CBR books, with covers, authors and "Recently added".
  - **Signing in:** make a read-only key under Account ▸ API keys and enter
    it as the password (any user name). Your account password is not
    accepted there. The Account page shows the catalog's address.
  - **What you see** follows your library access, content-rating limit and
    labels, as in the web app.
  - The catalog hands out book files, so it is only open while "Allow web
    downloads" is on (Settings ▸ General).
  - Guide: [docs/opds.md](docs/opds.md). No migration.
- **Download my data.** Account ▸ Your data ▸ "Download my data" gives you a
  ZIP of what the server keeps about your account, as JSON files with a
  README: profile and preferences, watch history and progress, ratings,
  favorites, playlists, reading positions, audiobook bookmarks, requests,
  reported problems, notifications, signed-in devices and API key names.
  Titles carry their TMDB, TVDB, IMDb and MusicBrainz ids so the data means
  something elsewhere. Passwords, PINs, two-factor secrets, keys and sign-in
  tokens are never included.
  - Admins can download any user's data to answer a data request.
  - Every export is recorded in the audit log (`user.data_export`). API keys
    can't make one; it needs a signed-in session.
  - **API (additive):** `POST /users/me/export`, and for admins
    `POST /users/{id}/export`. No migration.
- **Edit many titles at once.** In a library, admins can select titles and
  change their metadata in one go.
  - **Selecting:** "Select", the checkbox that appears on a tile on hover, or
    a long press on a touch screen. Shift-click selects everything between
    two tiles; "Select all on page" takes every title loaded so far.
  - **Editing:** "Edit metadata…" sets or clears the content rating; adds,
    removes or replaces genres; adds or removes labels; and locks or unlocks
    any field. Every field starts at "leave unchanged".
  - **Locks work as in the single-title editor.** A field you set is locked,
    so refreshes and Fix Match keep it. A field a title already has locked is
    skipped and listed afterwards, unless you unlock it in the same edit.
    A field that doesn't apply to a title (a content rating on an album, say)
    is skipped too.
  - The whole edit succeeds or fails as one, up to 500 titles at a time.
    Libraries that save NFO files get them rewritten afterwards. The edit is
    recorded once in the audit log (`item.bulk_edit`).
  - "Mark watched" and "Mark unwatched" also work on the selection, and "Add
    to collection…" takes just its movies and shows.
  - **API (additive, admin):** `PATCH /items/bulk`. No migration.
- **Fix subtitle timing for everyone.** When a subtitle is out of sync, an
  admin can correct it on the server once, and every player and app gets the
  corrected timing — the TV and phone apps included, without an update.
  - **In the web player:** the subtitle menu's Sync control now moves the
    subtitles in steps of 0.1 s and 1 s and shows the current value. This
    adjustment only affects your own session. Admins also get "Save for
    everyone", which stores the adjustment on the server and resets their
    local one.
  - It works for downloaded and OCR'd subtitles and for text subtitles inside
    the video file, up to 10 minutes either way. Subtitles that would start
    before the beginning start at 0; ones that would end before it are
    dropped.
  - Players fetch subtitles fresh enough to see a change straight away.
  - **Not covered yet:** converted downloads still carry an embedded
    subtitle's original timing. Image-based subtitles (PGS, VobSub) are never
    sent as text; correct the OCR'd copy instead.
  - **API (additive, admin):** `PUT /subtitles/{id}/offset` and
    `PUT /files/{file_id}/subtitle-streams/{index}/offset`. Subtitle tracks in
    item details gain `offset_ms`, for information only: the server has
    already applied it.
  - **Migration 00055** adds the offset to downloaded subtitles and a table
    for embedded subtitle offsets.
- **My stats and Year in review.** Everyone now has their own stats page
  (My stats, in the sidebar and on the Account page): hours watched and
  listened, plays, titles finished, books read, activity by month, a
  weekday-by-hour chart of when you play, time by type and genre, your top
  movies, shows, artists and albums, your longest streak of days and the
  devices you use most. Pick the last 30 or 90 days, 12 months, all time or a
  single year.
  - **Year in review** (`/year/2026`) sums up a year on cards that look good
    in a screenshot or printed: headline numbers, top fives, the first thing
    you played, your busiest day, new artists you discovered and how the year
    compares with the one before.
  - **Only yours:** each person sees only their own numbers, and a profile
    sees its own. Titles you can't see (library access, rating limit, label
    restrictions) are left out, as in your history. Admins keep the
    server-wide Analytics page.
  - Numbers refresh every few minutes, like Analytics. Time counts how far you
    got into each title, so sampling a film for five minutes counts as five
    minutes.
  - **API (additive):** `GET /users/me/stats?range=30d|90d|365d|all|year:YYYY`
    and `GET /users/me/year-in-review?year=YYYY` (both take `tz`).
  - **Migration 00054** adds an index so these stay fast on long histories.
- **Digest email.** People can get a weekly or monthly email of what was added
  to their libraries: new movies, shows (new episodes grouped per show), music
  albums and books, with posters. Turn it on under Account ▸ Email me a
  digest.
  - It can also list what you're in the middle of and your own numbers for the
    week or month. Only titles you're allowed to see are included.
  - An admin turns digests on and picks the day and hour under Settings ▸
    Email (off by default), and can send themselves a test digest. Links use
    the Base URL from Settings ▸ General. Without SMTP nothing is sent and the
    page says so.
  - The new "Send digest emails" task checks every hour. Each person gets each
    week or month once, a digest with nothing new isn't sent, and sends are
    spaced out. People without an email address are skipped.
  - Posters are attached to the email itself, so it shows them without
    signing in and carries no link that grants access to the server. Every
    digest has an unsubscribe link that works without signing in, and mail
    apps can offer one-click unsubscribe.
  - **API (additive):** `GET`/`PUT /users/me/digest`; admin
    `GET`/`PUT /settings/digest` and `POST /settings/digest/test`;
    `GET`/`POST /digest/unsubscribe?token=…` (no sign-in).
  - **Migration 00054** adds the per-user digest setting.
- **Optimize.** Admins can make smaller, universally playable copies of
  movies and episodes ahead of time, so a phone, a weak TV box or a viewer
  away from home plays a copy directly instead of holding a live transcode
  for the whole film.
  - **Making them:** "Optimize…" on a movie, episode, show or season page,
    on a library ("Optimize…" next to Scan, for the whole library) or on
    titles selected in a library. Pick a profile: Mobile (720p, 4 Mbps), TV
    (1080p, 8 Mbps) or Original quality, compatible (the source's
    resolution, re-encoded to H.264 + AAC so every device can play it).
  - **Progress:** a title's page lists its optimized versions with their
    progress and a delete button; Settings ▸ Library ▸ Optimized lists every
    one on the server, by status.
  - **Playing them:** when a player would otherwise need a full transcode —
    a codec or HDR it can't play, or a bitrate limit away from home — the
    server hands it an optimized version that fits its profile and its
    limits, and it plays directly. The web player does this now; Now Playing
    shows "Optimized" on such a stream. Phone and TV apps keep playing the
    original (and transcoding) until they are updated to ask for it.
  - **Storage:** copies live in `CACHE_PATH/optimized`, not in the library
    folders. They don't expire: they are deleted when the original file is
    deleted or replaced, or when you delete them.
  - **Server load:** copies are encoded by the converted-download runner —
    the same encoder choice, the same `DOWNLOAD_MAX_CONVERSIONS` limit and
    the same transcode slots — with users' downloads going first.
  - **API (all additive):** `POST /optimize`, `GET /optimize/profiles`,
    `GET /optimize/jobs`, `GET`/`DELETE /optimize/jobs/{id}`,
    `DELETE /optimize/versions/{id}` (admin), `GET /media/optimized/{id}`,
    `optimized_versions` on item detail (never part of `files`), and for
    clients that send `optimized=1` in `X-Client-Capabilities`, a play
    decision of `directPlay` with `reason: optimized_version` and
    `optimized_version`.
  - **Migration 00056** lets `download_conversions` hold optimized versions
    (`purpose`, the source's hash, the output's size and bitrate).
- **Remote access: map a port on the router automatically.** Settings ▸
  General ▸ Remote access can ask your router to forward a port to the
  server, so it can be reached from outside your network without setting up
  port forwarding by hand.
  - It speaks UPnP (IGD v1 and v2), PCP and NAT-PMP, keeps the mapping alive
    while the server runs, and removes it when you turn it off or the server
    stops. Only the server's own port is mapped (you can choose the public
    port), never when the server listens on loopback only, and in a cluster
    only one node maps.
  - The page shows whether a router was found, the public address and port,
    the last renewal and any error, with advice on testing from outside —
    and warns plainly that this opens the server to the internet: turn on
    HTTPS and two-factor sign-in first. It is off unless you turn it on.
  - **API (additive, admin, not for API keys):**
    `GET`/`PUT /settings/remote-access`.
- **Camera RAW photos.** Photo libraries now pick up CR2, CR3, NEF, ARW, DNG,
  RAF, ORF, RW2 and PEF files.
  - **Showing them:** thumbnails and the viewer use the full-size JPEG the
    camera embeds in the RAW, read directly from the file, so nothing extra
    needs installing. A RAW without one falls back to `exiftool`, `dcraw`,
    `dcraw_emu` or `ffmpeg` when one is on the server's PATH.
  - **Date, camera, lens, exposure and GPS** are read from the RAW, so RAW
    photos land on the timeline and the map like JPEGs.
  - **RAW + JPEG pairs:** when a RAW and a JPEG with the same name sit in the
    same folder, they are one photo. The JPEG is shown, and "Download
    original" in the viewer gives you the RAW. A new option on a photo
    library's settings page, "Pair RAW files with their JPEGs" (on by
    default), lists them separately instead; the next scan regroups.
  - **Photos are now told apart by file, not by name:** two folders' IMG_0001.JPG
    are two photos. Before, every photo with the same file name in a library
    was one photo. Existing libraries are sorted out on their next scan.
  - HEIC and TIFF photos now show in the web app's grid, albums, map and
    viewer too.
- **Faces and people in photo libraries.** OnScreen can find the faces in
  your photos so you can name the people in them.
  - **Finding faces** is a new task, "Photo face detection" (Settings ▸ Tasks),
    **off by default**: face data is personal, so turning it on is your call.
    It runs nightly, looking at up to 500 new photos within 30 minutes each
    time, with a face detector built into OnScreen (nothing is sent anywhere).
  - **Naming:** hover over a photo in the viewer to see its faces, and click
    one to name it; a name the library already has adds the photo to that
    person. Admins and regular members name faces; managed profiles with a
    rating or label limit only see the names.
  - **Matching is manual.** OnScreen finds faces but doesn't recognise who is
    who: there's no face-recognition model it can ship that is good enough
    and free to redistribute. To speed naming up, a person's page suggests
    unnamed faces taken around the same time as their photos.
  - **People pages:** "People" in a photo library lists everyone named, with
    the unnamed faces to work through; a person's page shows their photos
    and can rename or remove them.
  - **Privacy:** face data belongs to its library and is shown only to people
    who can see the photo — library access, rating limits and labels all
    apply, and a person whose photos you can't see doesn't appear. "Delete
    all face data" on a photo library's settings page removes every face and
    name in it (recorded in the audit log).
- **Memories.** "On this day" for your photos: the home screen shows a
  Memories row — "3 years ago today", or "This week 5 years ago" when a year
  has nothing on the day — whenever there is something to show, and only if
  you have a photo library. Each memory opens as a slideshow; up to 30 photos
  are picked across the day rather than from one burst. Small libraries
  count the day either side too.
  - **API (all additive):** `GET /photos/memories`, `GET /photos/people`,
    `GET`/`PATCH`/`DELETE /photos/people/{id}`, `GET /photos/people/{id}/items`
    and `/suggestions`, `GET /photos/faces`, `PUT`/`DELETE /photos/faces/{id}`,
    `GET /items/{id}/faces`; admin `GET`/`PUT /libraries/{id}/photo-settings`
    and `DELETE /admin/photos/face-data`. `GET /items/{id}/image` shows the
    JPEG of a RAW + JPEG pair and decodes RAW files.
  - **Migration 00057** adds `library_photo_settings`, `photo_people`,
    `photo_faces` and `photo_face_scans`, and takes photos out of the
    title-uniqueness index on `media_items`.

### Changed

- **An account's own PIN is gone.** `PUT` / `DELETE /users/me/pin` and the
  `has_pin` field of `GET /users/me/preferences` are removed, with the
  Profile PIN block in Settings ▸ General. Switching only ever reached a
  managed profile the account owns, so a PIN set on the account itself was
  never checked. Managed profiles keep their PINs (Profiles page,
  `/profiles`); no first-party app used the removed routes.
- **The server's play decision is final.** Every app asks
  `POST /items/{id}/playback-decision` how to play a file, and now plays the
  answer; it decides on its own only when it gets no answer (an older server,
  a failed call).
  - **Transcode start** re-encodes video a client asked it to copy when the
    decision for that client is a transcode (a codec or bit depth it can't
    decode, HDR on an SDR display, a picture above its or the viewer's
    resolution ceiling). It used to copy whatever it was asked to.
  - **`GET /media/stream/{id}`** refuses a video the server would convert for
    this viewer on any device: over the internet streaming limit from outside
    the home network, or over the viewer's quality profile (video bitrate or
    resolution), with 409 `TRANSCODE_REQUIRED`; a damaged file with 422
    `FILE_DAMAGED`. Music, audiobooks and books are never refused, and
    downloads use `/media/download`. An app that decided on its own used to
    get the original file past every one of these.
  - **Roku** plays the server's verdict both ways (it used to keep its own
    and accept only a demotion), refuses Dolby Vision profile 5 and damaged
    files with a message, and declares only the containers it direct-plays
    (`mp4`).
  - **Web** waits up to 4 seconds for the verdict before starting, instead of
    starting on its own guess; a refused direct play retries once through
    the server.
  - **Android TV and phone** refuse a damaged file instead of trying it, and
    retry a refused direct play once through the server. The Android TV app
    holds a server transcode to the screen's height. The phone downloads
    originals from `/media/download`.

- `PATCH /collections/{id}` keeps fields the request leaves out. An omitted
  description used to be cleared, and an empty name was saved.

### Fixed

- **The Status menu on a movie or show page shows the saved status.** On the
  web it always came up blank, and changing it reported an error even though
  the change had been saved; the Android TV / Fire TV app never showed a
  status either. `GET` / `PUT /api/v1/items/{id}/watch-status` answered with
  the bare object while those clients read the `{"data": …}` envelope; the
  body now carries the fields both at the top level (as the phone app reads
  them) and under `data`.
- **Settings that were saved but did nothing now work.**
  - **Library metadata language.** A library's language was stored and never
    used: every library got English from TMDB. Movie and TV titles, summaries,
    genres, seasons and episodes now come in the library's language where TMDB
    has a translation. A missing summary falls back to English. Searches stay
    in English, so automatic matching is unchanged. Items already matched
    change on their next metadata refresh.
  - **Concurrent-stream limit.** An admin's per-user stream limit counted only
    transcodes, so any number of direct plays got past it. Direct plays on
    other devices now count too: the play decision, transcode start and the
    direct stream routes refuse a stream over the limit with 429
    `TOO_MANY_SESSIONS`, and the web player says "Too many streams". A device
    moving to its next track or episode, or a stream taken over from another
    device, is never refused. With no limit set nothing changes.
  - **Quality profile.** `PUT /users/me/quality-profile` (maximum resolution,
    video and audio bitrate, preferred codec) was stored and never applied,
    and no app could set it. The server now applies it on every device and
    network: a video over the limits transcodes (`reason: quality_profile`),
    as does an audio-only file over the audio bitrate in apps that ask for a
    play decision; every transcode's audio is held to the audio bitrate; and
    the codec choice picks H.264 or HEVC output. The web Account page has a
    new **Playback quality** section to set it. Values too small to play are
    refused with 400.
  - **Encoder tuning** (Settings ▸ Transcode: NVENC preset, tune and rate
    control, peak-bitrate ratio) was stored and never read: the server kept
    the `TRANSCODE_NVENC_*` / `TRANSCODE_MAXRATE_RATIO` values. Saved values
    now apply to the built-in transcoder and converted downloads after a
    restart (the page used to say no restart was needed), the page shows the
    running values, and values ffmpeg wouldn't take are refused with 400.
    Remote workers keep their own environment settings.
- **The web My Playlists page** (M3U import and export, smart playlists) had
  no link; it's in the sidebar now.
- **Shows auto-matched the wrong series.** Movies got a strict match in
  v2.5.0; shows hadn't. TMDB's show search took an exact name if there was
  one, else any show whose name began with the folder's ("Avatar" became the
  2024 live-action Avatar: The Last Airbender), else TMDB's top hit, and
  ignored the year. TMDB, the TVDB fallback and, outside anime libraries, the
  AniList fallback now apply the movie rule: the name, original name, alias or
  alternative title must be the folder's, first aired within a year of its
  year. "Avatar" now finds the animated series, which TMDB also lists under
  that name. TVDB/Sonarr qualifiers are understood: "The Office (US)" picks
  the American Office of the six TMDB has, and "Doctor Who (2005)" the 2005
  series. In anime libraries AniList still matches first, and the TMDB and
  TVDB lookups that fetch episode data also try AniList's English and romaji
  names. Items already matched keep their match; use Fix Match on a wrong one.
- **Some HEVC MP4s played as a black or broken picture when remuxed.** An HEVC
  file that keeps its parameter sets only inside the video stream (an `hev1`
  MP4 with an empty `hvcC`) lost them in the remux: the stream is tagged
  `hvc1`, and ffmpeg then writes no `hvcC` at all, so players had nothing to
  decode with. The worker now spots such a source when it starts the job and
  rebuilds the `hvcC` from the stream, so the remux plays everywhere,
  Safari included. Other HEVC files are remuxed as before.
- **The offline anime database stopped loading.** The manami-project
  anime-offline-database no longer keeps its JSON on the master branch, so
  the download at startup failed with 404 and the anime title fallback was
  off. The server now fetches the minified file from the project's latest
  GitHub release (same format, about 60 MB, refreshed weekly).
- **`docker/deploy-gpu.sh` stamped the build as `dev` when run with `sudo`:**
  git refused the repository owned by another user, so the server reported
  version `dev`. The script now tells git the repository is safe.
- **One device's hiccup no longer signs the user out everywhere.** When a
  refresh token that was already used comes back, the server takes it for a
  stolen copy and signs the user out on every device. Most of the time it is
  the user's own app: two requests refreshing at once, or an app re-sending a
  refresh whose answer it never got. On the QA server 4 of 5 such sign-outs
  came within 80 seconds of the token's use. Now a used token that comes back
  within 2 minutes is only refused: that request gets the usual 401 and no new
  tokens, but the user's other devices stay signed in. It is recorded as
  `auth.refresh_reuse_tolerated` in the audit log, not
  `auth.sessions_revoked`. After 2 minutes nothing changes. The trade-off: a
  thief who uses a stolen token within 2 minutes of the real app no longer
  triggers the sign-out everywhere, though they still get no tokens.

### Security

The reasoning behind each is in [docs/security.md](docs/security.md). The
ones operators and client authors notice:

- **Refresh-token reuse within 2 minutes is refused, not punished.** A used
  refresh token that comes back within 2 minutes of the session's last use
  gets a 401 and no tokens, and the user's other devices stay signed in
  (`auth.refresh_reuse_tolerated`); after that, reuse still signs the user
  out everywhere (`auth.sessions_revoked`). A thief racing the real app
  inside that window no longer causes the sign-out, but still gets nothing.
- **Personal API keys** (`osk_…`) are stored as hashes, sent only in a header
  (`Authorization: Bearer` or `X-Api-Key`, never a URL), take their owner's
  library access, rating limit and labels at each request, and can't sign in,
  refresh, change credentials, manage devices, keys or users, or touch server
  settings. Creating and revoking them is audited. The OPDS catalog accepts a
  key as its Basic-auth password, never the account password.
- **Label restrictions and the rating ceiling cover every new list:**
  studios, instant mix, OPDS, M3U export, your stats, the weekly digest and
  the Watchlist.
- **Per-device sign-out.** Signing out one device revokes only its session;
  changing your password ends your other sign-ins.
- **`GET /media/stream` follows the play decision**, so an app can no longer
  fetch the original file past a viewer's internet limit or quality profile
  by skipping the decision.
- **CORS for the Chromecast receiver** is limited to the HLS playlist,
  segment, static-ladder and subtitle routes, which are authorized by the
  token in their URL: `Access-Control-Allow-Origin: *`, never with
  credentials. Every other route keeps the existing CORS rules.
- **Data export** leaves out password hashes, PINs, two-factor secrets, keys
  and sign-in tokens, and every export is audited (`user.data_export`).
- **Built with Go 1.26.9 and x/net 0.60.0** (GO-2026-6612, -6613, -6617).

## [v2.5.1] — 2026-10-06

Server patch release: Dolby Vision files with a base layer every player can
show are no longer refused. Coming from v2.4.x, read the v2.5.0 upgrade notes
below first.

### Upgrade notes (from v2.5.0)

- **Migration 00036** adds `media_files.dv_profile` at startup (with
  `AUTO_MIGRATE=true`, else run the migrate step first). It only adds a column.
- **Run a scan of your movie and show libraries** after upgrading. Until a scan
  re-reads them, files tagged Dolby Vision stay refused, because the clients
  decide from the stored tag. The scan probes each such file once.
- The desktop installers are rebuilt as 2.5.1. The app itself is unchanged.

### Fixed

- **Dolby Vision files that every client can play are no longer refused.**
  The scanner tagged any file with a Dolby Vision configuration record
  `dolby_vision` (whenever the record came before any HDR10 metadata), and
  every client refuses those with "Dolby Vision is not supported". But
  profiles 7 and 8.1 carry an HDR10 base layer and 8.4 an HLG one, and some
  files carry the record over plain SDR video with no Dolby Vision data in
  it. The QA library had 26 such files and no profile 5. A file now reads as
  its base layer, judged from the video's own signalling. Only profile 5,
  which has no base anything else can show, stays `dolby_vision` and is
  refused. The profile is kept in `media_files.dv_profile` (migration 00036)
  and returned as `dv_profile` on item files. **Run a scan of your movie and
  show libraries after upgrading:** it re-reads each file tagged Dolby Vision
  once, changing only its dynamic range (the integrity verdict stays). See
  [docs/dolby-vision.md](docs/dolby-vision.md).
## [v2.5.0] — 2026-10-06

Requests, watch marks, franchise collections, Last.fm/Trakt scrobbling,
audiobook speed and bookmarks, a file integrity probe and the TV web app at
`/tvapp/` (the Xbox app's front end), plus the fixes and hardening from the
September 2026 security review ([docs/security.md](docs/security.md)) and a
round of client releases for Android TV, Fire TV, the phone, Samsung Tizen
and LG webOS. Roadmap: [docs/v2.5-roadmap.md](docs/v2.5-roadmap.md).

### Upgrade notes (from v2.4.x)

Rehearsed by upgrading a populated v2.4.1 compose stack in place: the
migrations ran in under a second on a small library, and users, libraries,
watch progress, favorites and existing sign-ins all carried over.

- **Migrations 00018–00035** apply at startup with `AUTO_MIGRATE=true`
  (otherwise run the migrate step first; `/health/ready` stays unready until
  they are applied). 00023 rebuilds watch state from `watch_events` and holds
  new events back while it runs, so it takes longest on a long history; it
  drops the `watch_state` materialized view, so anything outside OnScreen
  that queried it must move to the `user_watch_state` view. 00030 rebuilds a
  unique index on `media_items`.
- **Sign-ins now last at most 90 days.** Refreshing still extends a session,
  but never past 90 days from sign-in. Sessions that exist at upgrade get 90
  days from the upgrade, so no device is signed out by it; after that, each
  device signs in again (TVs re-pair) every 90 days.
- **Compose needs `DB_PASS`.** There is no `onscreen` default any more, so
  compose refuses to start without it. Set it to the password your Postgres
  volume was created with.
- **A placeholder `SECRET_KEY` is refused at boot.** If yours was a sample
  value, rotate it with `cmd/rotate-key`.
- **Rotate your Radarr/Sonarr API key** if anyone pressed Save on the *arr
  settings under v2.4.x. The save could store the webhook key as `****`,
  which then authenticated anyone.
- **First-run setup** works only from the local network or a local host
  name. Set `ALLOW_PUBLIC_SETUP=true` to finish it over the internet.
- **Reverse proxies:** re-copy `docker/nginx.conf` (headers moved to server
  level, and `X-Forwarded-For` is replaced rather than appended), and set
  `TRUSTED_PROXIES` to the proxy's address. The client address is now read
  from the right of `X-Forwarded-For`, and without `TRUSTED_PROXIES` every
  private address in it counts as a proxy hop. Behind a proxy that sends only
  `X-Forwarded-For` (Caddy's default), every LAN client would then share the
  proxy's rate-limit bucket for sign-in, refresh and pairing, and HLS
  playlist and segment requests (now limited to 1000 a minute per address).
- **New background work:** seek-bar thumbnails are switched on for existing
  video libraries, so the nightly backfill uses CPU and the cache disk grows;
  `arr_request_sync` polls Radarr/Sonarr for request download status. The
  `integrity_probe` task is off until you enable it.
- **Run a music scan and an audiobook scan** after upgrading: multi-disc
  albums an older scan folded together split again, and audiobooks a scan
  merged into one are separated.
- **Clients:** Android TV 1.3+ needs Android 7 or later, so Fire OS 5 sticks
  stay on 1.2.x. The phone app no longer trusts user-installed certificate
  authorities, so a server behind a private CA needs a publicly trusted
  certificate.

### Added

- **Xbox app, phase 0 (not yet run on a console)** — `clients/xbox`.
  - The server embeds a TV web app and serves it at `/tvapp/`
    (`internal/tvui`; `/tvapp` redirects there, unknown paths under it get
    its index, never the main SPA). A server built without it serves a
    placeholder page there. `/tv` is the web app's Live TV, hence the name.
    Makefile (`tvui`), `deploy.ps1`, the Docker images, the installers and
    the release workflow build it.
  - The TV web app is the webOS app's code with an Xbox platform layer:
    the controller (Windows' gamepad key codes; the Gamepad API on a PC),
    B as Back with the Exit popup on the first screens, the device name and
    the display's 4K / HDR from the shell, the API at the page's own origin,
    the 1920x1080 layout scaled to the web view, and Settings > Diagnostics
    (what the web view decodes, the capability header, a live key log).
  - The shell (`clients/xbox/shell`) is a UWP app with WinUI 2's WebView2,
    as Jellyfin's Xbox app is: it asks for the server, checks `/tvapp/`,
    shows it full screen without a pointer, and handles the page's exit and
    change-server requests and the system's Back. Release builds run on a
    Windows PC; the console visit (Dev Mode, Device Portal, a checklist) is
    in `clients/xbox/README.md`.
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

- **Opt-in file integrity probe** — a new `integrity_probe` scheduled task
  (migration 00019; seeded disabled; enable in **Settings → Tasks**) spot-decodes ~15 frames at
  the 10/50/90% offsets of each video file with the software decoder and marks
  `media_files.integrity_status` ok/damaged. Damaged/fake releases whose
  headers parse but whose bitstream doesn't (the kind that green-frames
  hardware decoders and stalls browsers) get a **"file may be damaged"** badge
  on the item page, a `damaged` playback-decision verdict, and a
  422 `FILE_DAMAGED` refusal on transcode start — instead of users retrying an
  unplayable title. Verdicts reset automatically when a file's content changes
  on disk; probe failures (unreachable share, missing ffmpeg) leave files
  unchecked rather than branding them.

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
- **webOS app 0.2.2: "Sign in with another device" works.** The app polled
  for the sign-in with POST, which the server answers 405 (it routes
  `GET /auth/pair/poll`), so a TV showing a PIN waited forever however often
  the PIN was entered. The same bug was found and fixed in the Tizen app
  first; Roku had it earlier. 0.2.1, in LG's QA, has the bug; 0.2.2 is
  0.2.1 with this fix and nothing else.
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
- **Migrations 00018–00035** — applied on startup with `AUTO_MIGRATE=true`,
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
  returns `disc_number`. An album already folded this way splits on the
  next music scan (see above). The web album page heads each
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
  writes a playlist. (Roku still sends the file's stream number there, so
  its last-track pick now gets that 400; Tizen 1.1.0 and webOS 0.2.0 send
  the position in `audio_streams`.)
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

### Security

The full list, with the reasoning behind each fix, is in
[docs/security.md](docs/security.md). The ones operators and client authors
notice:

- **Refresh-token reuse is detected.** A refresh token presented after it
  was rotated ends the whole session chain and leaves an audit row, and
  every session has a 90-day hard limit (migration 00018; sessions that
  exist at upgrade count from the upgrade).
- **Pairing:** the web `/pair?code=…&auto=1` link no longer claims a code
  on its own; the user confirms it (`GET /auth/pair/pending`), and pairing
  is audit-logged.
- **Credential endpoints check the request's origin** (CSRF and login
  CSRF), TOTP codes and the TOTP login challenge are single-use, and SAML
  sign-in is bound to the browser that started it.
- **First-run setup** answers only local peers and local host names unless
  `ALLOW_PUBLIC_SETUP` is set; placeholder `SECRET_KEY` values are refused.
- **The *arr webhook key could be saved as `****`** by an ordinary Save of
  the *arr settings; the mask is now never written. Rotate the key.
- **ffmpeg inputs are restricted** to file protocols, and a playlist-shaped
  "media" file gets `415 UNSUPPORTED_CONTAINER`.
- **The content-rating ceiling covers every item surface** (photos, EXIF,
  lyrics, subtitles, watch status, favorites, progress, markers), and
  `/jobs`, collections and playlists check ownership.
- **`X-Forwarded-For` is read right to left** from the trusted proxies;
  Live TV HLS goes through the watch-limit gate; LAN discovery answers only
  local senders; `/health/version` reports less.
- **Compose:** Postgres and Valkey bind to 127.0.0.1, containers drop all
  capabilities, and `DB_PASS` is required. The installers tighten file ACLs
  and move a bundled Postgres to scram-sha-256.
- **Clients:** TV apps confirm before sending a password over plain http to
  a public host, and signing out revokes the token on the server. The phone
  keeps tokens in the Android keystore and no longer trusts user-installed
  certificate authorities.
- **Dependencies:** `google.golang.org/grpc` 1.83.2 and
  `golang.org/x/crypto` 0.56.0 (govulncheck: none of the fixed
  vulnerabilities was reachable from OnScreen's code).

## [v2.4.1] — 2026-08-05

Server-only patch release. Recommended for every deployment that TV
clients pair against — the v1.1.0 store apps (Amazon Appstore, Google
Play) walk new users straight into the flow the first fix repairs.

### Fixed
- **Pairing no longer rate-limits its own household out of logging
  in.** `/auth/pair/code` and `/auth/pair/poll` moved out of the
  shared per-IP auth bucket (10/min) into their own (60/min). A TV
  sitting on the pairing screen polls every 5 s — 12/min — which
  alone exhausted the household's entire login budget, so the phone
  trying to *claim* the PIN got 429s and the flow deadlocked itself.
  Polling is not a brute-force surface (the device token is the
  credential; the PIN claim runs on the authenticated web session),
  so the wider bucket gives up nothing.
- **The notifications SSE stream now sends a keepalive every 30 s**
  (previously one at connect, then silence). A NAT or router that
  drops an idle mapping without an RST left clients' reads parked
  forever — no error, so no reconnect, and cross-device sync +
  "play on this TV" stayed dead until an app restart. The periodic
  write also makes the server notice a vanished peer and end the
  handler. Client-side liveness watchdogs (shipped in the TV 1.1.0
  apps) time out against a multiple of this cadence.
- **Transcode sessions are no longer orphaned when the client
  disconnects during start.** Backing out during the buffering
  spinner cancels the HTTP call after the session was created but
  before the client learned its id — nothing could ever DELETE it.
  The handler now reaps the session inline when the request context
  is gone by the time the response would be written.

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
