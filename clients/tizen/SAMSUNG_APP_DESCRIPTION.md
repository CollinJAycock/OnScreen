# OnScreen — Samsung Apps Store App Description Document

> Source for the Samsung Apps Store submission's App Description.
> Paste each section into the corresponding field in Samsung's Word
> template before submission. Keep this file as the canonical copy
> so revisions land in git.

## 1. App Information

| Field | Value |
|---|---|
| App Name | OnScreen |
| Samsung App ID | 3202605045527 |
| Tizen Application ID | _(filled after Distributor cert generated)_ |
| Tizen Package ID | _(filled after Distributor cert generated)_ |
| Version | 1.0.0 |
| Category | Video / Entertainment |
| Required Tizen Version | 5.5 |
| Supported Models | 2020 and newer (Tizen 5.5+); hardware-verified on Samsung QN75Q80B (2022 model, AVPlay HEVC + audio passthrough) |
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
| Test Server URL | https://onscreen.wolverscreen.com |
| Test Username | testUser |
| Test Password | testPassword |
| First-launch flow | (A) App prompts for server URL → enter the URL above. (B) App prompts for pairing PIN → use the test credentials above on the web /pair page to claim. |

**About this review account.** `testUser` is scoped to a demo library
containing **exclusively public-domain and Creative Commons titles** —
Blender Foundation open movies (Big Buck Bunny, Sintel, Tears of Steel,
Spring, etc.) under CC-BY, LibriVox public-domain audiobooks, Kevin MacLeod
CC-BY music, and NASA public-domain imagery. It is representative of the app's
functionality and contains no commercial content. The app itself ships no
media; it is a client for a user's own self-hosted OnScreen server (see §2).
Attribution for the demo content is in
`docs/store-assets/demo-library-CREDITS.md`.

## 4. Geo-IP Whitelist Status

All 50 Samsung QA reviewer IPs across 11 countries have been added to
the OnScreen server's allow-list ahead of submission. The server
applies no geo-blocking, rate-limiting, or fail2ban-style restrictions
that would interfere with Samsung's automated or manual testing from
any region.

## 5. App Features (functional inventory)

### Home page (Hub)
- **Continue Watching** strips (TV shows, movies, other) — items the user has in progress, ordered by most recent activity.
- **Trending** — global "what others on this server are watching" strip from the last 7 days, filtered by the user's library access and parental rating.
- **Recently Added** — newest items across the user's accessible libraries.
- **Recently added to <library>** strips — one per library the user has access to.
- **Collections** — curated groupings if the server operator has defined any.

### Library browse
- Grid view of the items in a single library (movies, shows, anime, music, photos, audiobooks, podcasts, home_video, book, dvr).
- Filter / sort by year, genre, etc. when the server exposes them.
- Drill into a show → seasons → episodes; album → tracks; audiobook → chapters; author → series → book.

### Detail page
- Hero artwork (fanart preferred, poster as fallback).
- Title, year, summary, content rating, runtime.
- Play / Resume button (resume offered when view_offset_ms > 0).
- Audio-quality badges where applicable (Hi-Res, Lossless, FLAC, etc.).
- Cast / crew row when present on the server side.

### Playback
- Hardware decode via Samsung AVPlay for H.264, HEVC, AV1 sources the
  panel supports. Server-side transcoding via HLS for sources the
  panel doesn't support.
- HDR10 passthrough on supported panels; HDR-to-SDR tonemapping is
  done on the server when source and panel don't match.
- Trickplay scrubbing thumbnails on the progress bar.
- Audio track picker (when multiple audio streams present).
- Subtitle picker — embedded streams, on-disk .srt/.ass sidecars,
  and online search via OpenSubtitles. PGS / VOBSUB bitmap subtitles
  are OCR'd on the server to selectable text WebVTT.
- Watching status (Plan to Watch / Watching / On Hold / Completed /
  Dropped) saved per-user and synced to other devices.
- Skip Intro and Skip Credits buttons appear at marker timestamps,
  with optional auto-skip toggle.
- Up Next overlay at end-of-episode for shows; queues the next
  episode with a 10-second countdown.
- Sleep timer (15m / 30m / 45m / 1h / End of episode).
- Progress is reported back to the server every 10 seconds during
  playback for cross-device resume.

### Live TV / DVR (when the server has a tuner configured)
- Channel guide / EPG.
- Live channel playback (full-screen).
- Scheduled recordings.
- Recorded shows browse + playback.

### Music / Audiobook
- Album browse with cover art.
- Track playback with progress bar, skip prev/next.
- Audiobook navigation through author → series → book → chapter
  hierarchy with chapter-snapped resume.

### Photo
- Library grid with album browse.
- Full-screen viewer with D-pad navigation between siblings.

### Settings
- Server connection details.
- Sign out / re-pair.
- Subtitle styling (font size, color, background).
- Playback preferences (preferred audio language, preferred subtitle
  language, auto-skip-intros toggle).
- About (version, build info).

## 6. Navigation Flow

1. **First launch** → Server-URL entry screen → user types or pastes
   their OnScreen server address → app validates reachability →
   advances to Login / Pair.
2. **Login / Pair** → user enters Pair PIN (created on the server's
   /pair page from another device) → app exchanges PIN for tokens →
   advances to Home.
3. **Home** → Continue Watching / Trending / Recently Added / per-
   library strips, plus a top nav bar (Search, Settings).
4. **Tile click** → Detail page for that item → Play.
5. **Playback** → fullscreen player; **Return** exits playback to
   Detail; **Exit** closes the app.
6. **Return on Home** → exits the app to SmartHub (per Samsung
   Self Checklist item #13).

## 7. Remote-Key Behavior

| Key | Behavior |
|---|---|
| Up / Down / Left / Right | Move focus |
| OK / Enter | Activate focused element |
| Return / Back | On Home: exits to SmartHub. On any sub-page: previous page. During playback: returns to Detail page. |
| Exit | Always closes the app and returns to TV / SmartHub. |
| Play / Pause | During playback: toggles playback. Outside playback: no-op. |
| FF / RW | During playback: jumps ±10 seconds (single press). Long-press: continuous seek. |
| Stop | During playback: stops and returns to Detail. |
| Number Keys 0-9 | Unused (per Samsung Self Checklist #45). |
| Channel +/- | Unused. |
| Volume +/- / Mute | Handled by TV firmware (system OSD); app does not override. |

## 8. Network / Server Requirements

- The app communicates exclusively with the user-configured OnScreen
  server. No third-party content APIs, no advertising networks, no
  analytics, no telemetry.
- All requests are HTTPS (TLS 1.2+). Internal/LAN HTTP is supported
  via the Tizen CSP `connect-src *` allowance for users running the
  server on a home network.
- Network failures display a user-facing dialog with retry guidance.
  The Return/Exit keys remain functional from the error state.

## 9. Multi-Language Behavior

- The app currently ships UI strings in English only.
- When the TV's menu language is set to a non-English locale, the
  app's strings stay in English (no broken UI, no missing-string
  exceptions). Locale changes do not require an app restart.
- Server-supplied metadata (movie titles, show descriptions) is
  shown in whatever language the operator's metadata sources
  returned at scan time — typically the language matching the
  operator's TMDB / TVDB locale settings.

## 10. Privacy / Data Handling

- The app stores only the user-supplied server URL and an
  encrypted authentication token in the Tizen app's local
  preferences. No personally identifiable information is collected
  by the app itself.
- All viewing history, watch progress, and account information live
  on the user's self-hosted OnScreen server. They are not
  transmitted to Samsung, Anthropic, the app developer, or any
  third party.
- The app does not display advertisements. The TIFA / LAT
  advertising-identifier APIs are not used (Samsung Self Checklist
  item #218 marked NA accordingly).
- See https://onscreen.wolverscreen.com/privacy for the full
  privacy policy of the reference public server.

## 11. Permanently Out of Scope

The following are documented as deliberately not implemented so
Samsung QA does not flag them as defects:

- Voice control / Bixby integration.
- 3D screen output.
- DLNA / UPnP rendering.
- Samsung TV Account SSO (users authenticate against their own
  OnScreen server only).
- Picture-in-picture.
- Watch-party / SyncPlay.
- Third-party streaming services or content marketplaces.

## 12. Hardware Verification History

| Date | Panel | Outcome |
|---|---|---|
| 2026-05-11 | Samsung QN75Q80B (2022) | First end-to-end hardware run — navigation, video playback (H.264 / HEVC), audio (FLAC, AAC), music browse, photo viewer, watch state, library hygiene. Sideloaded via Samsung partner cert bound to TV DUID. |
| 2026-05-12 | Samsung QN75Q80B (2022) | Re-verified with v2.2.0 server contract changes. AVPlay HEVC + audio passthrough confirmed; HDR10 source plays through cleanly. |
