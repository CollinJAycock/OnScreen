# OnScreen — Samsung Apps Store App Description Document

> Source for the Samsung Apps Store submission's App Description.
> Paste each section into the corresponding field in Samsung's Word
> template before submission. Keep this file as the canonical copy
> so revisions land in git. The review account's password goes only in
> the Seller Office account fields, never in this file.

## 1. App Information

| Field | Value |
|---|---|
| App Name | OnScreen |
| Samsung App ID | 3202605045527 |
| Tizen Application ID | OnScreenTV.OnScreen |
| Tizen Package ID | OnScreenTV |
| Version | 1.1.0 (first submission) |
| Category | Video / Entertainment |
| Required Tizen Version | 5.5 |
| Supported Models | 2020 and newer (Tizen 5.5+); hardware-verified on a Samsung QN75Q80B (2022, Tizen 6.5) |
| Required Profile | tv-samsung |
| Publisher | Collin Aycock |
| Developer Email | collin.j.aycock@gmail.com |
| Privacy Policy URL | https://onscreen.wolverscreen.com/privacy |
| Support URL | https://github.com/CollinJAycock/OnScreen |

## 2. Overview

OnScreen is a TV client application that connects to a
user's self-hosted OnScreen media server to play movies, TV
shows, anime, music, audiobooks, podcasts, and photos the user
has personally organized on that server. The app does not host,
provide, or stream any media content of its own. Without a
user-configured server URL the app cannot function.

The architecture is identical to mainstream self-hosted media-
server clients (Plex, Jellyfin, Emby, Kodi). The user runs the
OnScreen server software on their own hardware (NAS, mini-PC,
or VPS) and points this TV app at the server's URL. All media
playback streams from that user-owned server.

## 3. Test Account / Server (for store QA review)

| Field | Value |
|---|---|
| Test Server URL | https://onscreen-beta.wolverscreen.com |
| Test Username | reviewer |
| Test Password | In the Seller Office account / password fields of this submission |
| Account validity | Does not expire; one account works on any number of TVs at once |
| First-launch flow | (A) "Add your OnScreen server": enter the URL above and select Connect. (B) "Sign in": enter the username, then the password, with the TV's on-screen keyboard. No phone, computer or website is needed. ("Sign in with another device" is an optional alternative and is not needed for review.) |

**About the review server.** https://onscreen-beta.wolverscreen.com is an
OnScreen server we run for testing and app-store review. It looks up artwork
and descriptions for its own files from TMDB and TheTVDB; no subtitle,
download-manager or TV-tuner service is configured on it. The `reviewer`
account sees five libraries holding **only openly licensed works**: Blender
Foundation open movies and the Caminandes series (Creative Commons
Attribution), Kevin MacLeod music (CC BY), LibriVox public-domain audiobooks
and NASA public-domain photos. Attribution is in
`docs/store-assets/demo-library-CREDITS.md`. The app itself ships no media.

Suggested review path: Home → Movies → Sintel → Play (open Subtitles from the
player's controls: the film's own subtitle tracks), Big Buck Bunny (Audio: two
tracks), Caminandes (a series: seasons and episodes, Up Next at the end of an
episode), a Music album, an audiobook (chapters, listening speed), Photos (OK
starts a slideshow), Search, Settings.

## 4. Geo-IP Whitelist Status

The review server has no IP allow-list, geo-blocking, rate limiting or
fail2ban-style filtering, so every Samsung QA address listed in the
checklist (item 3) reaches it.

## 5. App Features (functional inventory)

### Home page (Hub)
- **Continue Watching** rows (TV shows, movies, other): items in progress, most recent first. Hold OK on a card for its options (remove from Continue Watching, open).
- **Trending**: what others on this server watched in the last 7 days, filtered by the user's library access and parental rating.
- **Recently Added**, plus a **Recently added to <library>** row per library.
- **Collections**, when the server operator has defined any.
- Top navigation: Home, Libraries, Search, Favorites, History, Settings.

### Library browse
- Grid view of one library (movies, shows, anime, music, photos, audiobooks, podcasts, home videos), loading more as the user scrolls.
- Sort, genre and watched / unwatched filters; Surprise me opens a random title.
- Drill into a show → seasons → episodes; album → tracks; audiobook → chapters.

### Detail page
- Artwork, title, year, summary, content rating, runtime.
- Play / Resume (resume when the item was partly watched), Mark watched / unwatched, Favorite, Report a problem (sends a note to the server's administrator).
- Seasons and episodes for a series.

### Search
- On-screen keyboard; results from the user's own libraries only.

### Playback
- Hardware decode through Samsung AVPlay (H.264, HEVC). Sources the TV can't play directly are converted by the user's server and streamed as HLS.
- HDR10 sources play as HDR on HDR panels.
- On-screen controls: play / pause, a progress bar with the elapsed and total time, ±10 s with Left / Right; thumbnail previews while seeking when the server has generated them.
- Audio track picker (when a title has several audio tracks) and subtitle picker (the title's own subtitle tracks and subtitle files stored with it). A subtitle track the file marks as default is shown automatically. If the server's operator has set up an online subtitle service, the picker also offers to search it; the review server has none, so the option does not appear.
- Chapters picker for titles with chapters.
- Skip Intro / Skip Credits buttons at the server's marker times.
- Up Next at the end of an episode: the next episode with a countdown; Return declines it.
- Audiobook listening speed (0.5× to 3×); on Samsung TVs the server speeds the audio up.
- Progress is saved to the server every 10 seconds, so playback resumes on any device.
- The TV's screensaver stays off while video plays and while a photo slideshow runs.

### Music / Audiobook
- Album and track playback with cover art, progress bar, previous / next track; the next track starts by itself.
- Audiobooks by chapter, resuming where the user stopped.

### Photo
- Full-screen viewer; Left / Right move between the album's photos; OK starts and stops a slideshow.

### Live TV / DVR (only when the user's server has a tuner configured)
- Channel guide, live channel playback, recordings. The review server has no tuner, so these screens do not appear.

### Settings
- Sign out; Forget server (switch to another server).
- Preferred audio language, preferred subtitle language, forced subtitles only.
- Optional: connect the user's own ListenBrainz, Last.fm or Trakt account through their server, to record what they play.
- Privacy policy (QR code and address).
- About: the app version.

## 6. Navigation Flow

1. **First launch** → "Add your OnScreen server" → the user enters their
   server address → the app checks it → Sign in.
2. **Sign in** → username, then password (or "Sign in with another device":
   a PIN entered on the server's web /pair page) → Home.
3. **Home** → Continue Watching / Trending / Recently Added / per-library
   rows, with the top navigation.
4. **Card selected** → Detail page → Play.
5. **Playback** → full-screen player; **Return** goes back to the Detail
   page; **Exit** closes the app.
6. **Return on Home** (and on the first-launch and Sign in screens) closes
   the app and returns to Smart Hub (Self Checklist item 13). On every other
   screen Return goes to the previous screen (item 14).

## 7. Remote-Key Behavior

| Key | Behavior |
|---|---|
| Up / Down / Left / Right | Move focus. In the player with the controls hidden: Left / Right skip back / forward 10 s and show the controls; Up / Down / OK show the controls. |
| OK / Enter | Activate the focused element. Hold OK on a card for its options. |
| Return | Home and first-launch screens: closes the app to Smart Hub. Elsewhere: previous screen. In the player: closes an open menu or the Up Next card first, then returns to the Detail page. |
| Exit | Always closes the app. |
| Play / Pause / Play-Pause | In the player: play, pause, toggle. Photos: start / stop the slideshow. |
| Stop | In the player: pauses. |
| FF / RW | Video and audiobooks: skip forward / back 30 s. Music: next / previous track. |
| Channel ▲ / ▼ | In the player: next / previous track, audiobook chapter or podcast episode, or next / previous chapter of a film with chapters; otherwise no effect. Live TV: next / previous channel. |
| Red / Green | In the player: previous / next chapter. |
| Yellow / Blue | In the player: open the Audio / Subtitles picker. |
| Number Keys 0-9 | Unused (Self Checklist item 45). |
| Volume / Mute | Handled by the TV (system volume OSD); the app does not override them. |

## 8. Network / Server Requirements

- The app communicates only with the user-configured OnScreen server. It
  loads no third-party content, advertising, analytics or telemetry.
  (Artwork, descriptions and any optional services are fetched by the
  user's server, not by the app.)
- HTTPS is used whenever the server offers it; plain HTTP is allowed for a
  server on the user's home network.
- Network failures show a message saying what failed and offering to try
  again; Return and Exit keep working.

## 9. Multi-Language Behavior

- The app currently ships UI strings in English only.
- When the TV's menu language is set to a non-English locale, the
  app's strings stay in English (no broken UI, no missing-string
  exceptions). Locale changes do not require an app restart.
- Server-supplied metadata (movie titles, show descriptions) is
  shown in whatever language the operator's metadata sources
  returned at scan time.

## 10. Privacy / Data Handling

- The app stores only the user-supplied server URL and the server's
  sign-in tokens in the app's local storage. The app itself collects no
  personally identifiable information.
- Viewing history, watch progress and account information live on the
  user's self-hosted OnScreen server. They are not transmitted to Samsung,
  the app developer, or any third party by the app.
- The app does not display advertisements. The TIFA / LAT
  advertising-identifier APIs are not used (Self Checklist item 218: NA).
- See https://onscreen.wolverscreen.com/privacy for the full privacy policy.

## 11. Permanently Out of Scope

The following are deliberately not implemented, so Samsung QA does not
flag them as defects:

- Voice control / Bixby integration.
- 3D screen output.
- DLNA / UPnP rendering.
- Samsung TV Account SSO (users authenticate against their own
  OnScreen server only).
- Picture-in-picture.
- Watch-party / SyncPlay.
- Third-party streaming services or content marketplaces.
- In-app purchases or subscriptions.

## 12. Hardware Verification History

| Date | Panel | Outcome |
|---|---|---|
| 2026-05-11 | Samsung QN75Q80B (2022) | First end-to-end hardware run: navigation, video playback (H.264 / HEVC), audio (FLAC, AAC), music browse, photo viewer, watch state. |
| 2026-05-12 | Samsung QN75Q80B (2022) | Re-verified with the v2.2.0 server. AVPlay HEVC and HDR10 confirmed. |
| 2026-10-05 / 06 | Samsung QN75Q80B (2022, Tizen 6.5) | 1.1.0: every screen, D-pad navigation, H.264 and 4K HDR HEVC playback (direct and server-converted), seeking past the converted range, audio / subtitle pickers, default subtitle tracks, Up Next, music auto-advance, audiobook speed, photo viewer and slideshow, search, favorites, settings, backgrounding and resuming, the remote's Play/Pause and Channel keys. |

## 13. Screen Guide (annotated images)

The numbered images are in `docs/store-assets/samsung-appstore/` (`menu-*.png`),
captured from version 1.1.0 signed in as `reviewer` on the review server; the
navigation diagram is `ui-structure.png`. Plain screenshots for the store
listing are in `screenshots/` (1920 x 1080).

**menu-home.png — Home**
1. Top navigation: Home, Libraries, Search, Favorites, History, Settings.
2. Row title (Continue Watching Movies).
3. The focused card (purple frame); OK opens it.
4. Hint: hold OK on a card for its options.
5. Trending row.

**menu-detail.png — Detail (a series)**
1. Poster.
2. Title, year and description.
3. Play (the next unwatched episode) / Resume.
4. Mark all watched, Favorite, Report a problem.
5. Seasons.
6. Episodes (hold OK on an episode to mark it watched).

**menu-library.png — Library grid**
1. Library name.
2. Sort and genre.
3. Watched filter (All items / Unwatched / In progress / Watched) and Surprise me.
4. The focused card; the grid loads more titles as the focus moves down.

**menu-playback.png — Player controls** (shown on OK / Up / Down, or any seek)
1. Title.
2. Play state (playing / paused, or the seek target while seeking).
3. Progress bar with the elapsed and remaining time.
4. Audio / Subtitles / Chapters row (each when the title has them; Down moves to it).
5. Remote-key hints.

**menu-music.png — Music player**
1. Album art.
2. Track title.
3. Artist and album.
4. Track number in the album and play state.
5. Progress bar with the elapsed and remaining time.
6. Remote-key hints.
