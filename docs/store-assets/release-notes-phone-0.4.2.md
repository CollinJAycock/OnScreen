# Release notes — phone client 0.4.2 (versionCode 1010)

Published to Google Play's closed testing track on 2026-11-05, on the shared
`tv.onscreen.android` listing (phone builds use versionCodes 1000+, the TV app
below 1000; the TV app went out as 26 / 1.5.0 the same day). The phone app
has no production release yet: production waits on 12 testers opted in to
the closed track for 14 days.

Covers everything since 0.3.0 (1007), the last phone release with notes.
1008 (0.4.0) was built on 2026-10-29, but whether it reached Play is not
recorded, so the What's New text below covers both. 1009 went only to the
Meta Horizon Store (as the Quest build), and Quest 1010 is a separate
package (`tv.onscreen.quest`), so there is no clash with this 1010.

Built from main 11a37475: upload key SHA-256 `579bd037…fa3cac75`, target 36,
min 24, 16 KB-aligned native libraries, AAB sha256 `8e1aa691…cafb5234`.

---

## What's New (Play console, phone release)

484 characters (Play's limit is 500).

```
• Android Auto: music, audiobooks and podcasts in the car.
• Cast any video to a Chromecast.
• Watchlist: give a title a status, pick it up on any device.
• Search inside a library, with an A–Z fast-scroll rail.
• Smaller converted downloads; Settings > Playback quality.
• Movie versions, Similar Artists, Appears On, Instant Mix.
• Comic reading position syncs with your server.
• Fixes: no silent Dolby/DTS films; right seek bar after a resume; no black screen after a double Back.
```

---

## Reviewer notes (Play "App access")

The 0.3.0 text, plus the review server and Android Auto. The password goes
only in the console's credential fields, never in this text or any file.

```
OnScreen is a client application for a media server that users install
and run on their own hardware (similar in model to Plex or Jellyfin).
The app browses and plays only the media library on the user's own
server. It does not host, index, or provide access to any content
itself, and it has no feature to search for, request, obtain, or
download media from any third-party source. Downloads are copies of
the user's own library files, made by the user's own server.

To test: open the app, enter the server address
https://onscreen-beta.wolverscreen.com, and sign in with the reviewer
credentials provided with this submission. The account sees five
demonstration libraries (Movies, TV Shows, Music, Audiobooks, Photos)
holding only public-domain and Creative Commons works.

Android Auto: after signing in on the phone, the app appears in
Android Auto as a media app with Music, Audiobooks and Podcasts to
browse and play.
```

---

## Before or after rollout

- **Android Auto in Play Console.** The app declares itself an Android Auto
  media app (`com.google.android.gms.car.application`). Check under
  Release > Advanced settings > Form factors whether Android Auto needs to be
  added there, so that Play reviews it for cars and makes it available to
  them. Until then testers may not see it in the car. It has been tested
  only on the Desktop Head Unit with the S24 FE, never in a real car.
- **Tester check.** The 0.4.2 build itself has not been run on a phone.
  Android Auto was DHU-tested on 1009, and 1010 only changed the Quest
  flavor. Install it from the closed track on the S24 FE and try sign-in,
  playing a video, casting, a download, and the car.

---

## Internal changelog

Relative to 0.3.0 (1007).

**0.4.1 / 0.4.2 (1009, 1010)**
- Android Auto: a MediaLibraryService browse tree for music, audiobooks and
  podcasts, with artwork through a non-exported content provider granted to
  the car and System UI (be4cf813, d1cbf506).
- Search inside a library, and an A–Z fast-scroll rail (d01ce1fe).
- Security: the custom TLS trust manager is gone, leaving the platform's
  own checks (b1cdb541).
- A double tap on Back no longer leaves a black screen (004785e1).
- Dolby and DTS are claimed only where MediaCodecList has a decoder, so
  the server converts them for phones without one instead of sending a
  film that plays silent; the seek bar on a resumed remux / transcode
  shows the film's own time and length (6c98e015).
- Title pages fit wide windows (tablets, landscape) and the pair button
  says "Pair this device" off phones (2a9fbeac).
- Meta Quest flavor (91124d05, 71a0a947). Quest-only: no PiP, no 32-bit
  code, no POST_NOTIFICATIONS, app volume. These changes don't affect the
  mobile build.

**0.4.0 (1008)**
- Chromecast: any video to the Default Media Receiver over HLS, the final
  cast contract, the Cast button opens its picker, receivers placed by
  capability and model, posters with an asset token (374338e0, 4b1f4959,
  6d73e4e1, 66be3dfd, ce9a0a7d, 35816154).
- Watchlist screen (3f0a599a).
- Background heartbeats: bounded, non-blocking, WAKE_MODE_NETWORK
  (97b1d169).
- Downloads: converted (smaller) downloads (7976a641), a converted download
  plays as an MP4, and one part of a stack is never cast (05b82e9b).
- Playback: the server's decision is final (8f519bf7); stream limit,
  optimized versions and stacked parts (ca18d260); "Too many streams" when
  the limit refuses a stream (bfc10e6b); image subtitles on a remux /
  transcode play as OCR text or are burned in (4131b3a3, 5d60885b).
- Title pages: clear logo, studios, version picker, download sizes
  (c67693f0); multi-episode files as a range (41f990d3, 6aaa33ec).
- Settings > Playback quality (01afa8c7). Music: Similar Artists, Appears On,
  Instant Mix (75663657). Comic reader position synced with the server
  (52b95cab).
- App-lifetime coroutines log failures instead of crashing (a0bad27b).
- Fixes from the 0.3.0 device test (7f9dff80).
