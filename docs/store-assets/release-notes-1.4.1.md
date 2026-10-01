# Release notes — TV client 1.4.1 (versionCode 24)

The Fire TV resubmission after the Amazon Appstore rejected 1.4.0 (23) on
2026-10-01. Amazon's finding, under its Deceptive and Malicious Behavior
policy: the app "facilitates the ability to save, convert, stream or download
media from third-party sources without explicit authorization from those
sources". It rejected 1.1.0–1.1.2 with the same wording (over the request
row, removed in f3758d3b).

1.4.1 is 1.4.0 plus one change, in the Fire TV build only: it leaves out the
two features that match that wording.

- **Online subtitle search.** The player's Subtitles menu ended with "Find
  more online…", which had the user's server search OpenSubtitles.com and
  download the chosen subtitle file. The Fire TV picker now lists only the
  item's own tracks (and any the user added from the web app), with no
  "· downloaded" tag, and the Subtitles button shows only when the item has
  a subtitle track.
- **Live TV and Recordings.** The two cards in Home > Browse and the screens
  behind them (channel guide, live channel player, recordings list) are gone.
  Recordings saved to a `dvr`-type library still play as ordinary library
  items; the review server has no such library.

The 1.4.0 reviewer note also said the app had no way to obtain anything from
a third-party source, which "Find more online…" contradicted. The notes below
correct that rather than repeat it.

The review account also moves. 1.4.0 was reviewed as `testUser` on the QA
server, which is to become production. 1.4.1 is reviewed as `reviewer` on
https://onscreen-beta.wolverscreen.com, a server that exists only for store
review: no outbound internet access, no metadata, subtitle, download-manager
or TV-tuner service configured, and five libraries of openly licensed works
(listed in the testing instructions below).

Both flavors ship from this code. `googletv` (Google Play: Android TV,
Google TV, NVIDIA SHIELD) keeps both features and behaves exactly as 1.4.0
(23): against the 1.4.0 AAB its R8 keep set (seeds.txt, compared sorted)
is identical, its `resources.pb` byte-identical, and its dex identical in
every method (only R8's map-id marker differs). `firetv` (Amazon Appstore)
is the one that changed. How the flavors differ is in
[`clients/firetv/README.md`](../../clients/firetv/README.md).

**Google Play.** Nothing user-visible changed for Play. If 23 is already
uploaded there, 24 is optional (upload it only to keep the two stores' codes
in step); if 23 never went up, upload 24 instead of 23.

---

## What's New — Amazon Appstore (Fire TV)

Amazon users never received 1.4.0, so this covers 1.4.0's changes too.
464 characters (limit 500).

```
• Your TV can switch to each video's frame rate (Settings > Playback).
• DTS and Dolby TrueHD/Atmos go to your AV receiver unchanged.
• HDR, 4K and AV1 are used only when your TV and Fire TV support them.
• Music and audiobooks keep playing in the background; book speed, bookmarks and a sleep timer.
• Next Up and Plan to Watch rows, mark watched, Report a problem.
• The Live TV and Recordings screens and online subtitle search are no longer in the Fire TV app.
```

## What's New — Google Play (Android TV / Google TV)

The 1.4.0 text; nothing was removed from this build. 467 characters (limit
500).

```
• New: your TV switches to the video's frame rate (Settings > Playback).
• Surround: DTS and Dolby TrueHD/Atmos go to your receiver untouched.
• HDR, 4K and AV1 are used only when your screen and device support them.
• Music and audiobooks keep playing in the background; book speed,
  bookmarks and a sleep timer that stops at the chapter's end.
• Next Up, Plan to Watch, mark watched, and Report a problem.
• Better NVIDIA SHIELD support; resume and subtitle fixes.
```

---

## Testing instructions (Amazon "Testing instructions")

3,938 characters; check the console field's limit before pasting. If it
is lower, drop the NEW IN 1.4.x paragraph (3,631), then if needed replace
the Movies line with "- Movies: 11 Blender Foundation open movies, among
them Big Buck Bunny, Sintel and Tears of Steel (© Blender Foundation, CC BY
2.5, 3.0 or 4.0)." (3,391). Nothing to fill in. The password goes only in the
submission's sign-in fields (username `reviewer`), never in this text or
any file. The content list must match what `reviewer` actually sees on the
review server (checklist step 2).

```
WHAT THIS APP IS
OnScreen is the Fire TV app for OnScreen Media Server, open-source software (AGPL-3.0) that a person installs on their own computer or NAS, the same model as Plex Media Server, Jellyfin and Emby. The app contains no content and no built-in address of any content service. Its setup screen asks only for the address of the user's own OnScreen server, and it plays only the files in that server's libraries. There is no web browser, no add-ons, and no field for stream links or playlists.

WHAT IT DOES NOT DO
- No downloads to the device: the app requests no storage permission and declares no download service. Video and audio stream from the user's server; the app keeps only an image cache (artwork and photos already viewed), no video or audio.
- No catalog of titles outside the user's library and no title requests (removed after the 1.1.x reviews). Search returns only items in the libraries the signed-in account can see on its server.
- No online subtitle search. Our 1.4.0 notes wrongly said no such feature existed: the 1.4.0 subtitle menu had "Find more online…", which searched OpenSubtitles.com and had the user's server download the chosen subtitle file and store it with the video. It is not in this build.
- No Live TV or Recordings screens (not in this build).

OTHER THINGS YOU MAY NOTICE
- If the Fire TV can't decode a file's format, the user's own server converts that file while it plays, as Plex and Jellyfin servers do. Nothing is saved on the device.
- Artwork and descriptions for the user's files come from the user's server, not from the app.
- Settings > Scrobbling can link the user's own ListenBrainz account so their server logs finished music tracks there (play history only; no media is fetched). The review server has no outbound internet access, so it sends nothing.
- Report a problem sends a note to the server's administrator.

HOW TO TEST
1. Open OnScreen. Server URL: https://onscreen-beta.wolverscreen.com. Select Connect.
2. On the Sign In screen, enter the username (reviewer) and password from this submission's sign-in fields and select Sign In. Do not use "Sign in with another device"; no phone, computer or website is needed.
3. Home has a row for each of the five libraries: Movies, TV Shows, Music, Audiobooks, Photos. Play Sintel and open Subtitles: its 10 subtitle tracks are stored in the video file (titles without subtitle tracks show no Subtitles button). Play Big Buck Bunny and open Audio (it has two audio tracks). Also try a Music album, an audiobook and Settings > Playback.

THE REVIEW SERVER
https://onscreen-beta.wolverscreen.com is an OnScreen Media Server we run only for app-store review. It has no outbound internet access, and no metadata, subtitle, download-manager or TV-tuner service is configured on it. It holds only these openly licensed works:
- Movies: 11 Blender Foundation open movies (© Blender Foundation, Creative Commons Attribution; licence version in brackets): Big Buck Bunny (2008, 3.0), Charge (2022, 4.0), Coffee Run (2020, 4.0), Elephants Dream (2006, 2.5), Glass Half (2015, 3.0), Hero (2018, 4.0), Singularity (2026, 4.0), Sintel (2010, 3.0), Spring (2019, 4.0), Tears of Steel (2012, 3.0), Wing It! (2023, 4.0).
- TV Shows: Caminandes, season 1: Llama Drama, Gran Dillama, Llamigos (Blender Foundation, CC BY).
- Music: Kevin MacLeod, 3 albums: Serenity, Video Classica, Wonder (incompetech.com, CC BY 4.0).
- Audiobooks: LibriVox public-domain recordings of Aesop's Fables, The Adventures of Sherlock Holmes, The Time Machine, Alice's Adventures in Wonderland and Frankenstein.
- Photos: 12 NASA images (public domain).

NEW IN 1.4.x
Frame-rate matching (Settings > Playback); DTS and Dolby TrueHD passed to an AV receiver; HDR, 4K and AV1 used only when the TV supports them; background playback for music and audiobooks with speed, bookmarks and a sleep timer; Next Up and Plan to Watch rows; mark watched; Report a problem.
```

### How each claim was checked (keep in step if the app or the server changes)

| Claim | Evidence |
|---|---|
| No storage permission, no download service | 1.4.1 firetv APK merged manifest (`aapt2 dump badging` / `xmltree`): permissions INTERNET, WAKE_LOCK, FOREGROUND_SERVICE, FOREGROUND_SERVICE_MEDIA_PLAYBACK, POST_NOTIFICATIONS, ACCESS_NETWORK_STATE and the app's own DYNAMIC_RECEIVER_NOT_EXPORTED_PERMISSION; one service, `OnScreenMediaSessionService` (foreground type mediaPlayback). Media3's unused `DownloadService` class and its notification strings (`exo_download_*`: "Download", "Downloading", "Removing downloads" …, in many locales) are in the APK through the blanket `androidx.media3.**` keep rule, but the service is not declared, so it can't run and nothing shows those strings. |
| Only an image cache on the device | `OnScreenApp.kt` Coil disk cache (`cacheDir/artwork`, 100 MB), which holds artwork and the photos opened in `PhotoViewFragment` (1920×1080 requests); no `SimpleCache`, `DownloadManager`, file output or `MediaStore` use in `src/main`. |
| No built-in content address, no browser | No `WebView`, Custom Tabs or `ACTION_VIEW` intent anywhere in `src/main`; the only hosts in code are the user's server, the `localhost` placeholder that `BaseUrlInterceptor` rewrites to it, and the setup screen's example address (which it refuses as input). Every image the firetv build loads comes from the server (`/artwork/…`, `/api/v1/items/{id}/image`); the one exception, Live TV channel logos, is not in this build. |
| Setup asks only for the server address | `ServerSetupFragment` probes `health/live` on the entered URL, then goes to Sign In. |
| Search returns only library items | The app calls only `GET api/v1/search`; `internal/api/v1/search.go` reads library rows and filters them by the caller's library access. No TMDB / discover call remains (scan: no `api/v1/discover` or `api/v1/requests` string in the dex). |
| No online subtitle search, no Live TV / Recordings screens | `BuildConfig.ONLINE_SUBTITLE_SEARCH` / `LIVE_TV` = false for firetv; firetv APK scan below: their strings and the two icons are gone from the APK, the three Live TV screens and the subtitle search and download endpoints from the dex, and `showOnlineSubtitleSearch()` compiles to a bare `return`. |
| Subtitles on Sintel; no Subtitles button without tracks | Review media: `ffprobe` on `Sintel (2010).mkv` shows 10 embedded `subrip` tracks (ger, eng, spa, fre, ita, dut, pol, por, rus, vie); no other demo film has a subtitle stream. `PlaybackFragment.refreshSecondaryActions()` adds the Subtitles button on firetv only when `subtitleRows()` is not empty. |
| Big Buck Bunny has two audio tracks | Review media: `ffprobe` shows an AAC and an AC-3 stream. |
| Review server isolated, content as listed | Review-server runbook verification: the server container sits only on an `internal: true` network, its `wget` to api.themoviedb.org fails, its env has no `TMDB_` / `TVDB_` variables; `/api/v1/system/capabilities` has `requests`, `subtitles_external` and `web_downloads` false; `/api/v1/admin/arr-services` and `/api/v1/tv/tuners` return `[]`. The media matches the build log of `fetch_demo.py` (11 films, Caminandes S01E01–E03, 30 Kevin MacLeod tracks in 3 albums, 5 LibriVox books, 12 NASA JPEGs); licence versions from its `manifest.json`. |
| ListenBrainz link | `ScrobbleRepository` → `PUT api/v1/users/me/scrobble/listenbrainz` (token to the user's server; the server submits listens). |
| Report a problem | `POST api/v1/items/{id}/issues` to the user's server. |
| "Sign in with another device" button | `pair_with_device` string on the Sign In screen; the pairing flow opens the server's web app on another device, which is why the steps say not to use it. |

---

## Contact Us appeal (Appstore → "Content Policy and Test Results")

Send the same day as the resubmission. Fill the placeholders; keep the
`[IF DONE]` line only if the screenshots really were replaced.

**Subject:** Appeal: OnScreen (ASIN B0GX2XH9P2) 1.4.0 rejection, request for specifics; 1.4.1 resubmitted

> Hello,
>
> OnScreen (ASIN B0GX2XH9P2[, submission/case ID <ID>]) version 1.4.0
> (versionCode 23) was not published on 1 October 2026 under the Deceptive
> and Malicious Behavior policy. The finding was that the app "facilitates
> the ability to save, convert, stream or download media from third-party
> sources without explicit authorization." We ask that this be treated as an
> appeal. We also ask which screen, feature or listing element was
> identified.
>
> **What the app is.** OnScreen is the Fire TV app for OnScreen Media
> Server, open-source software (AGPL-3.0) that a person installs on their
> own computer or NAS. Plex, Jellyfin and Emby work the same way, and their
> apps are in the Appstore. The app contains no content. Its setup screen
> asks only for the address of the user's own OnScreen server, and it plays
> only the files in that server's libraries. It has no web browser, no
> add-ons, and no field for stream links or playlists.
>
> **What it does not do.**
> - It does not save video or audio to the device. It requests no storage
>   permission and declares no download service.
> - It has no catalog of titles outside the user's library and no way to
>   request titles. We removed that feature after earlier reviews. Search
>   returns only items already on the user's server.
>
> **A correction.** Our 1.4.0 testing instructions said the app had no way
> to obtain anything from a third-party source. That was wrong. The
> player's subtitle menu had a "Find more online…" option, which searched
> OpenSubtitles.com and had the user's server download the chosen subtitle
> file and store it with the video. We apologize for the error.
>
> **Changes in 1.4.1 (versionCode 24), submitted <DATE>:**
> - "Find more online…" subtitle search is removed from the Fire TV app.
>   It shows only subtitle tracks already stored with the user's files.
> - The Live TV and Recordings screens (channel guide, live channel player,
>   recordings list) are removed from the Fire TV app.
> - The review account is now on a server we run only for store review
>   (below), instead of our test server.
> - [IF DONE] Store screenshots are replaced with captures of that
>   server's library of openly licensed works.
> - The testing instructions now give a step-by-step test path.
>
> **Review account.** Username reviewer on
> https://onscreen-beta.wolverscreen.com; the password is in the
> submission's sign-in fields. The server has no outbound internet access,
> and no metadata, subtitle, download-manager or TV-tuner service is
> configured on it. It holds only openly licensed works:
> - 11 Blender Foundation open movies and the 3-episode Caminandes series
>   (Creative Commons Attribution 2.5, 3.0 or 4.0; the testing instructions
>   list each title)
> - 3 Kevin MacLeod albums (CC BY 4.0)
> - 5 LibriVox public-domain audiobooks
> - 12 NASA public-domain images
>
> If anything in 1.4.1 or its listing still conflicts with the policy,
> please name the screen or element and we will correct it.
>
> Thank you,
> <DEVELOPER NAME>, <DEVELOPER CONTACT EMAIL>

---

## Listing text (Amazon Developer Console)

Suggestions; the console's current text wasn't visible from the repo. The
long description has been drafted from
`clients/tizen/SAMSUNG_APP_DESCRIPTION.md`, which describes features the
Fire TV app no longer has.

**Short description:**

> OnScreen plays the media library on your own OnScreen Media Server, free
> open-source software you install on your own computer or NAS. The app
> includes no movies, shows, music or channels. Connect it to your server to
> browse and play the videos, music, audiobooks and photos you have added
> there.

**Long description, opening paragraph:**

> To use this app you need your own OnScreen Media Server set up and
> running, with media you have added to it. OnScreen does not provide, sell,
> host or link to any movies, shows, music or TV channels. It has no web
> browser, no add-ons and no way to open stream links or playlists; it
> connects only to your OnScreen server. Please add only media you own or
> have the right to use.

**Long description, playback line** (replaces "Server-side transcoding via
HLS"):

> When your Fire TV can't play a file's format, your server adapts it as it
> plays.

**Remove from the long description:**
- the Kodi comparison
- OpenSubtitles online search
- the Live TV / DVR section and the "dvr" library type
- "No third-party content APIs" (the server uses TMDB and OpenSubtitles)
- "reference public server" (say "the OnScreen project's privacy policy")
- pairing step (B)

**Feature bullets:**
- Requires your own OnScreen Media Server (free, open source); the app
  includes no content.
- Plays the movies, shows, music, audiobooks and photos on your server.
- Resume, Next Up and watch progress synced with your other OnScreen apps.
- Surround passthrough, HDR and frame-rate matching on supported TVs.
- No ads, no tracking; the app connects only to your server.

(The last bullet holds for the Fire TV build only: the Google TV build's
Live TV guide loads channel logos from the playlist's own host.)

**Keywords:** personal media, home media server, self-hosted, media library,
music library, audiobooks, photos. Avoid stream, free movies, live tv, iptv,
download, dvr, plex, jellyfin, kodi.

**Optional:** replace the featured-art tagline "Engage. All of your media."
with "Your server. Your library." (the captain artwork evokes Star Trek), and
point the support URL at a support page rather than the repo README, which
advertises Radarr/Sonarr, requests and OpenSubtitles.

---

## Checklist before resubmitting

1. **Developer Console, today.** Look at the five Fire TV screenshots, the
   long description, the feature bullets and the keywords that are live in
   the console. Note which version is live (1.1.0 (14) per the 1.4.0 notes,
   or the 1.0.7 (10) June build per commit 701b4e8b).
2. **Stand up the review server** at https://onscreen-beta.wolverscreen.com
   per the review-server runbook, and pass its verification checklist
   before anything is submitted:
   - no outbound internet: the server container's `wget` to
     api.themoviedb.org fails, and its env has no `TMDB_` / `TVDB_`
     variables;
   - nothing configured: TMDB and TVDB keys, OpenSubtitles, Radarr / Sonarr
     / Lidarr, tuners / M3U / XMLTV / Schedules Direct, SMTP; web downloads
     off; `/api/v1/admin/arr-services` and `/api/v1/tv/tuners` return `[]`;
   - five public libraries named Movies, TV Shows, Music, Audiobooks and
     Photos, holding exactly the works the testing instructions list (11
     films, Caminandes S1, three Kevin MacLeod albums, five LibriVox books,
     12 NASA photos), with the artwork and metadata SQL applied. The
     runbook's "13 posters" check is now 11 (Cosmos Laundromat and Sprite
     Fright were dropped);
   - trim the server's `NOTICE.txt` to the built content: it still credits
     Cosmos Laundromat, Sprite Fright, The Daily Dweebs, the
     behind-the-scenes extras and Dracula, none of which were built;
   - create `reviewer`: a full non-admin account (not a managed profile),
     "Can request" off, a new random password that goes only into the
     console (step 3), never into a file;
   - as `reviewer`: `/api/v1/libraries` lists exactly the five, with no
     `dvr`-type library (recordings in one would still browse and play in
     the Fire TV app as library items); `/api/v1/tv/channels` returns `[]`;
     search finds Sintel and finds nothing for a title that isn't in the
     demo libraries;
   - the hostname `onscreen-beta.wolverscreen.com` answers `/health/live`
     from outside the LAN with no Cloudflare Access redirect.
3. **Put the review credentials in the console.** Username `reviewer` and
   its password, in the submission's sign-in fields, replacing the 1.4.0
   `testUser` credentials.
4. **Replace the screenshots.** The repo's
   `docs/store-assets/amazon-appstore/screenshots/` show commercial titles
   (`01-home.png`: Young Sheldon, The Traitors Ireland, The Grinch;
   `04-playback.png`: *300* (2007) playing) and must not go back to Amazon.
   Re-capture all five on the 1.4.1 Fire TV build signed in as `reviewer`
   on the review server: `01-home` → Home (its Browse row shows Favorites,
   History, Settings), `02-detail` → a Blender film's detail page,
   `03-library` → the Movies grid, `04-playback` → a Blender film playing,
   `05-music` → a Kevin MacLeod album. Replace them in the console.
   `play-store/screenshots/tv/` is byte-identical (same MD5s) and needs the
   same replacement. The old files are still in the repo; delete or
   overwrite them when the new captures land.
5. **Smoke-test APK 24 on the Firestick before uploading.** This build was
   verified by tests, lint and APK inspection only, not installed, and its
   R8 configuration changed (see the internal changelog). Signed in as
   `reviewer` on the review server, check: sign-in with credentials, Home
   (Browse row: Favorites, History, Settings; the five libraries), each
   library, search, a detail page, Sintel with its subtitle tracks (the
   picker ends at the last track: no "Find more online…", no
   "· downloaded"), a film without subtitles (no Subtitles button), Big
   Buck Bunny's audio track switch, a Kevin MacLeod album in the background
   (HOME, then back), an audiobook, a photo, Settings > Playback, sign out.
   Also, against QA: sign-in by pairing and a title the server transcodes
   (every demo film is H.264/AAC).
6. **Submit** `app-firetv-release.apk` (24 / 1.4.1) with the Amazon What's
   New and testing instructions above.
7. **Send the Contact Us appeal** the same day, placeholders filled.
8. **Google Play:** decide whether to upload the googletv AAB (24); see the
   note at the top.

Superseded by the review server, and dropped from this list: the QA
`testUser` cleanup (private non-demo libraries, `can_request` off, removing
OpenSubtitles tracks and the Nosferatu / Metropolis / The General titles)
and the optional "separate review server" step. The 1.4.0 console held
`testUser`'s password, so consider changing it once QA becomes production.

---

## Internal changelog

Relative to 1.4.0 (23).

**Fire TV only (`firetv` flavor)**
- Two BuildConfig booleans in `productFlavors`
  (`clients/android/app/build.gradle.kts`): `ONLINE_SUBTITLE_SEARCH` and
  `LIVE_TV`, true for googletv, false for firetv.
- `PlaybackFragment.showSubtitlePicker()` adds "Find more online…" only
  when `ONLINE_SUBTITLE_SEARCH`; `showOnlineSubtitleSearch()` returns at once
  without it (the method itself stays, because `proguard-rules.pro` keeps
  every Fragment member).
- `PlaybackFragment.refreshSecondaryActions()` shows the Subtitles button
  for every video only when `ONLINE_SUBTITLE_SEARCH`; without it, only when
  the item has a subtitle row, since a picker holding nothing but "Off"
  looks broken.
- `PlaybackViewModel.buildSubtitleSources()` tags attached (external)
  subtitles "· downloaded" only when `ONLINE_SUBTITLE_SEARCH`. On firetv
  they show as their language and title, like embedded tracks.
- `HomeFragment` adds the Live TV and Recordings cards, and routes to their
  screens, only when `LIVE_TV`. Nothing else opens those screens: no deep
  link, search result, setting or notification leads to them.
- R8: `proguard-rules.pro` leaves `tv.onscreen.android.ui.livetv` out of the
  @AndroidEntryPoint, `*_GeneratedInjector` and `ui.**Fragment` keeps,
  keeps the Hilt components with `allowshrinking`, and splits the Retrofit
  rule so every interface is still kept but the `@retrofit2.http` service
  methods only with `allowshrinking` (as Retrofit's own bundled rules do);
  the new `proguard-googletv.pro` (googletv only) restores all of them in
  full. In firetv that lets R8 drop the three screens, and the resource
  shrinker their strings and icons, and drop the six API methods nothing
  calls there: online subtitle search and download, plus `getWatchStatus`,
  `setWatchStatus`, `clearWatchStatus` and `transferPlayback`, which no
  code in either flavor calls (firetv's sorted seeds.txt differs from a
  1.4.1 build without the split by exactly those six methods). Without the
  component change the fragment component's inject method kept each
  screen's class alive, and androidx.fragment's consumer rule then kept its
  constructor and so the whole screen. The firetv components lose only
  members nothing calls: the four Live TV inject methods, the abstract
  `@Binds` methods of the seven `*BuilderModule` interfaces (compile-time
  only), the empty `OnScreenApp_HiltComponents` holder class and one unused
  private constructor (compared member by member against the googletv dex).

**Both flavors**
- versionCode 24, versionName 1.4.1.
- `FlavorFeaturesTest` in `src/testFiretv` and `src/testGoogletv` pins the
  flags; CI (`.github/workflows/android-tv.yml`) runs the firetv copy next to
  the googletv suite.

**googletv against 1.4.0 (23)**, AAB to AAB: sorted `seeds.txt` identical
(57,309 lines each); `base/resources.pb` byte-identical; `dexdump -d`
identical in all 42,811 methods of `classes.dex` and all 1,042 of
`classes2.dex`, the dex files differing only in R8's map-id marker string
and the header checksum and signature. The other entries that differ are
the manifest (version), the upload signature files and BUNDLE-METADATA
(mapping file, `r8.json`, baseline profile).

**APK scan, firetv 1.4.0 (23) → 1.4.1 (24)** (`scan.py` markers, adapted;
counts of occurrences)

| Marker | firetv 1.4.0 | firetv 1.4.1 | googletv 1.4.1 |
|---|---|---|---|
| arsc "Find more online", "OpenSubtitles", "Looking for subtitles", "Subtitle added", "Couldn't download subtitle", "Choose a subtitle" | 1 each | 0 | 1 each |
| arsc "Live TV", "No live channels", "Add a tuner…", "Schedule recordings…", "Recording in progress", "Channel unavailable", "No guide data" | 1 each | 0 | 1 each |
| arsc keys `subtitles_find_more`, `ic_live_tv`, `ic_recordings` | 1 each | 0 | 1 each |
| arsc keys `live_tv` / `recordings` | 5 / 6 | 0 / 0 | 5 / 6 |
| zip `res/gX.xml` / `res/R9.xml` (the `ic_live_tv` / `ic_recordings` drawables) | present | gone | present |
| dex `LiveTVFragment` / `RecordingsFragment` / `LiveChannelPlayerFragment` | 5 / 5 / 6 | 1 / 1 / 1 (Hilt aggregation class names only) | 5 / 5 / 6 |
| dex `api/v1/items/{id}/subtitles/search`, `…/subtitles/download` | 1 each | 0 | 1 each |
| dex `api/v1/tv/recordings` / `api/v1/tv/channels` | 1 / 3 | 1 / 2 | 1 / 3 |
| dex `api/v1/discover`, `api/v1/requests` | 0 | 0 | 0 |
| dex string `downloaded` (the subtitle label) | present | gone | present |
| arsc "Report a problem" | 1 | 1 | 1 |
| dex bytes / arsc bytes / dex strings | 7,112,456 / 708,476 / 48,811 | 7,091,836 / 706,876 / 48,739 | 7,112,460 / 708,476 / 48,811 |

googletv 1.4.1's keyword-matched dex strings are the same as firetv 1.4.0's
(none gone, none added).

Still in the firetv dex, none of it reachable from the UI and none of it
visible text:
- **Retrofit paths** `api/v1/tv/channels`, `api/v1/tv/channels/now-next`,
  `api/v1/tv/recordings`: the Live TV view models below still call them,
  so the shrinkable Retrofit rule keeps them.
- **Models** `OnlineSubtitle`, `Channel`, `NowNext`, `Recording` and their
  field names (`tuner_id` …): `data.model.**` is kept whole for Moshi.
- **Live TV view models and Hilt glue** (`LiveTVViewModel`,
  `RecordingsViewModel`, their `_Factory` / `_HiltModules` classes, the
  `LiveTVRepository_Factory` and `OnlineSubtitleRepository_Factory`, the
  `hilt_aggregated_deps` entries): Hilt binds every `@HiltViewModel` into
  the component, so the view models stay reachable, and the factory and
  aggregation rules keep their names. `PlaybackFragment` still has its
  injected `onlineSubtitleRepo` field and the empty
  `showOnlineSubtitleSearch()`.
- **Media3's offline `DownloadService`** class and its `exo_download_*`
  notification strings, through the blanket `androidx.media3.**` keep; the
  service is not declared in the manifest.

Dropping those too would take moving `ui/livetv` plus the online-search
code into a `src/googletv` source set behind a small per-flavor shim, and
excluding `androidx.media3.exoplayer.offline.**` from the Media3 keep for
firetv only (then smoke-testing playback again). Neither seems worth it for
names a reviewer never sees; revisit only if Amazon cites a static scan.
