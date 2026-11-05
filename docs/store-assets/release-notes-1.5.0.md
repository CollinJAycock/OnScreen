# Release notes — TV client 1.5.0 (versionCode 26)

The first TV-client update since 1.4.2 (25), which Amazon published on
2026-10-06. It brings the Android TV / Fire TV app up to the v2.6 / v2.7
server: Watchlist, search inside a library with an A–Z rail, Dolby Vision,
movie versions and the Playback quality setting, collections, and music
discovery within the user's own library.

Nothing that Amazon's Deceptive and Malicious Behavior finding was about comes
back. The Fire TV build still leaves out online subtitle search and Live TV /
Recordings (`ONLINE_SUBTITLE_SEARCH` / `LIVE_TV` = false, pinned by
`FlavorFeaturesTest`). The new features read only the user's own server.
Similar Artists, for one, ranks artists from the same library
(`GET /music/artists/{id}/similar`, `internal/api/v1/music.go`). It does not
come from an outside catalog.

The Search microphone still shows only where a speech recognizer exists
(the 1.4.2 fix), so the review Fire TV gets the on-screen keyboard.

Smoke-tested on the Fire TV Stick 4K Max (2026-11-05, review server,
`reviewer`) as the upload-key release APK installed over 1.4.2. That run
found two library-screen bugs, both fixed in this build. DOWN from the Sort or
Search button lost focus (the remote stopped responding until BACK), and the
title stayed over the grid after an A–Z jump.

---

## What's New — Amazon Appstore (Fire TV)

446 characters (limit 500).

```
• Search inside any library, and jump through it with an A–Z rail.
• Watchlist: Add to Watchlist on a title's page, then find it on Home.
• Dolby Vision on Fire TVs and TVs that support it.
• Pick between versions of a movie; new Playback quality setting.
• Collections on Home; Similar Artists, Appears On and Instant Mix for music.
• Remote Next/Previous for music and audiobooks in the background.
• Subtitle, multi-episode and playback fixes.
```

## What's New — Google Play (Android TV / Google TV)

The same text. Nothing is left out of this build. The googletv AAB
(`app/build/outputs/bundle/googletvRelease/app-googletv-release.aab`, built
2026-11-05 from 9f948a14) was checked: 26 / 1.5.0, target 36, min 24,
leanback required, touchscreen and fire_tv optional, the EPG permissions
kept, the one native library (`libdatastore_shared_counter.so`, 4 ABIs)
16 KB-aligned, upload key SHA-256 `579bd037…fa3cac75`. Not device-tested
on Google TV; the Fire TV run covers the shared code.

---

## Testing instructions (Amazon "Testing instructions")

The 1.4.1 text with the NEW IN paragraph replaced and one Search sentence
added to HOW TO TEST. 3,910 characters. If the console field's limit is lower,
drop the NEW IN 1.5.0 paragraph first. The password goes only in the
submission's sign-in fields (username `reviewer`), never in this text or any
file.

```
WHAT THIS APP IS
OnScreen is the Fire TV app for OnScreen Media Server, open-source software (AGPL-3.0) that a person installs on their own computer or NAS, the same model as Plex Media Server, Jellyfin and Emby. The app contains no content and no built-in address of any content service. Its setup screen asks only for the address of the user's own OnScreen server, and it plays only the files in that server's libraries. There is no web browser, no add-ons, and no field for stream links or playlists.

WHAT IT DOES NOT DO
- No downloads to the device: the app requests no storage permission and declares no download service. Video and audio stream from the user's server; the app keeps only an image cache (artwork and photos already viewed), no video or audio.
- No catalog of titles outside the user's library and no title requests. Search, Watchlist, Collections and Similar Artists show only items in the libraries the signed-in account can see on its server.
- No online subtitle search and no Live TV or Recordings screens (not in the Fire TV build).

OTHER THINGS YOU MAY NOTICE
- If the Fire TV can't decode a file's format, the user's own server converts that file while it plays, as Plex and Jellyfin servers do. Nothing is saved on the device.
- Artwork and descriptions for the user's files come from the user's server, not from the app.
- Settings > Scrobbling can link the user's own ListenBrainz account so their server logs finished music tracks there (play history only; no media is fetched). No ListenBrainz account is linked on the review server, so it sends nothing.
- Report a problem sends a note to the server's administrator.

HOW TO TEST
1. Open OnScreen. Server URL: https://onscreen-beta.wolverscreen.com. Select Connect.
2. On the Sign In screen, enter the username (reviewer) and password from this submission's sign-in fields and select Sign In. Do not use "Sign in with another device"; no phone, computer or website is needed.
3. Home has a row for each of the five libraries: Movies, TV Shows, Music, Audiobooks, Photos. Play Sintel and open Subtitles: its 10 subtitle tracks are stored in the video file (titles without subtitle tracks show no Subtitles button). Play Big Buck Bunny and open Audio (it has two audio tracks). Also try a Music album, an audiobook and Settings > Playback.
4. Open Movies > View all: the search button at the top searches that library (type with the on-screen keyboard; the microphone appears only on devices with a speech recognizer), and the letters on the left jump through it.

THE REVIEW SERVER
https://onscreen-beta.wolverscreen.com is an OnScreen Media Server we run for testing and app-store review. It looks up artwork and descriptions for its own files from TMDB and TheTVDB; no subtitle, download-manager or TV-tuner service is configured on it. It holds only these openly licensed works:
- Movies: 11 Blender Foundation open movies (© Blender Foundation, Creative Commons Attribution; licence version in brackets): Big Buck Bunny (2008, 3.0), Charge (2022, 4.0), Coffee Run (2020, 4.0), Elephants Dream (2006, 2.5), Glass Half (2015, 3.0), Hero (2018, 4.0), Singularity (2026, 4.0), Sintel (2010, 3.0), Spring (2019, 4.0), Tears of Steel (2012, 3.0), Wing It! (2023, 4.0).
- TV Shows: Caminandes, season 1: Llama Drama, Gran Dillama, Llamigos (Blender Foundation, CC BY).
- Music: Kevin MacLeod, 3 albums: Serenity, Video Classica, Wonder (incompetech.com, CC BY 4.0).
- Audiobooks: LibriVox public-domain recordings of Aesop's Fables, The Adventures of Sherlock Holmes, The Time Machine, Alice's Adventures in Wonderland and Frankenstein.
- Photos: 12 NASA images (public domain).

NEW IN 1.5.0
Search inside a library with an A-Z rail; Watchlist; Dolby Vision where supported; movie versions and a Playback quality setting; collections on Home; Similar Artists, Appears On and Instant Mix built from the user's own music library.
```

### What changed in the claims since 1.4.1

| Claim | Evidence |
|---|---|
| Permissions and services unchanged | 1.5.0 firetv APK (`aapt2 dump badging`, 2026-11-05): INTERNET, WAKE_LOCK, FOREGROUND_SERVICE, FOREGROUND_SERVICE_MEDIA_PLAYBACK, POST_NOTIFICATIONS, ACCESS_NETWORK_STATE and DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION; no EPG permissions; leanback not required. |
| Watchlist, Collections, Similar Artists are library-only | Watchlist holds statuses on the user's own items; Similar Artists ranks artists "from the same library" (`internal/api/v1/music.go` `Similar`); Appears On and Instant Mix (`/music/artists/{id}/appears-on`, `/music/mix`) read the same library. |
| Library search and A–Z rail | `SearchFragment.forLibrary` calls the server's search scoped to that library; the rail reads the server's letter index. Seen on the stick: "sin" → Singularity, Sintel; T → Tears of Steel. |
| HOW TO TEST step 4 | Checked on the stick 2026-11-05: Movies > View all shows the Sort and Search buttons and the B C E G H S T W rail; no microphone on the stick (no recognizer). |

---

## Checklist before submitting

1. Review server: sign in as `reviewer` on a Fire TV (done on the stick
   2026-11-05: five libraries, Watchlist empty, A–Z rail present, so the
   server has the v2.7 letter index). Keep the reviewer's Watchlist empty
   or deliberate. Reviewers see whatever is on it.
2. Upload `clients/firetv/dist/onscreen-firetv-release.apk` (26 / 1.5.0,
   upload key SHA-256 `579bd037…fa3cac75`) as a new version of ASIN
   B0GX2XH9P2. Expect the same spurious "Reduced Device Support" warning
   for the three Vega OS sticks as in 1.4.0 (minSdk 24). Accept it.
3. Paste What's New and Testing instructions from above. Put the
   `reviewer` password only in the sign-in fields.
4. Screenshots: the live set is from the review server. Optional new shot:
   Movies grid with the A–Z rail.
5. Privacy URL unchanged.

---

## Internal changelog (since 1.4.2 / 25)

- Library: search this library (title-bar button) and an A–Z rail beside
  the grid (fe0f74ab); RIGHT on the search button keeps focus (f34ed23f);
  DOWN from the Sort or Search button goes to the grid, and the title hides
  after an A–Z jump (this release).
- Watchlist screen (1d7fd343), empty state (9c2b2588), Status action on
  the details page (4e4e77fb).
- Dolby Vision declared for profiles the box and screen take (1787d363).
- Optimized versions, the stream limit and stacked movies (64a7dfa3);
  version picker, clear logos, studios and chapter thumbnails (45fb6175);
  Playback quality setting (f0145f86).
- Collections: Home rows, smart rules, uploaded covers (68a26218).
- Music: Similar Artists, Appears On, Instant Mix (8a0c41cf); artist tab
  header and D-pad focus fix (aa788072).
- Multi-episode files: range labels, next episode after `index_end`
  (f13c25ea, 078dac7d, d73ba212).
- Playback: the server's decision is final (8f519bf7); a direct play Media3
  can't read falls back to a remux and is remembered (bdefd0e4); the
  optimized-version fallback refuses a source that plays nowhere (3e999a19);
  passthrough codecs sent to the server (d53797fd); image subtitles on a
  remux / transcode play as OCR text or are burned in (4131b3a3); a 503
  BURN_IN_UNAVAILABLE is refused like the burn-in 400s (5d60885b).
- Background audio: remote Next/Previous (82a54478); content-time seek bar
  after taking back a parked track (2ff34cb5); bounded, non-blocking
  heartbeats and WAKE_MODE_NETWORK (97b1d169).
- App-lifetime coroutines log failures instead of crashing (a0bad27b).
- Security: custom TLS trust manager removed (b1cdb541).
